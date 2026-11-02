package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Band optimization: SIMs from many carriers are used in mainland China, where
// every one of them (roaming or not) attaches to China Mobile, Unicom or
// Telecom. The module picks the strongest cell, which is not the fastest one
// when a strong band is congested or interfered. The optimizer locks each
// band the serving operator uses, measures it, keeps the best per SIM (ICCID),
// and a guard restores all bands if a locked band loses service.

const (
	bandRegisterTimeout = 60 * time.Second
	bandSettleWindow    = 20 * time.Second
	bandReselectEvery   = 12 * time.Second
	bandDataTimeout     = 25 * time.Second
	bandSpeedWindow     = 5 * time.Second
	bandGuardInterval   = 10 * time.Second
	bandFallbackAfter   = 30 * time.Second
	// A locked band must beat "all bands" clearly; otherwise stability wins.
	bandSpeedAdvantage = 1.3
	bandSINRAdvantage  = 3.0
	// Mask the module reported before any lock, used if nothing was saved yet.
	defaultFullBandMask = "42000001E20B0E18DF"
)

// China-side LTE bands per operator PLMN (MCC 460).
var operatorBands = map[string][]int{
	"46000": {3, 8, 34, 39, 40, 41}, "46002": {3, 8, 34, 39, 40, 41}, "46004": {3, 8, 34, 39, 40, 41},
	"46007": {3, 8, 34, 39, 40, 41}, "46008": {3, 8, 34, 39, 40, 41},
	"46001": {1, 3, 8, 41}, "46006": {1, 3, 8, 41}, "46009": {1, 3, 8, 41},
	"46003": {1, 3, 5, 41}, "46005": {1, 3, 5, 41}, "46011": {1, 3, 5, 41},
}
var fallbackCandidateBands = []int{1, 3, 5, 8, 34, 39, 40, 41}

var bandSpeedURLs = []string{
	"https://mirrors.ustc.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.5-live-server-amd64.iso",
	"https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.5-live-server-amd64.iso",
}

type bandRadio struct {
	Band  int     `json:"band"`
	PLMN  string  `json:"plmn"`
	RSRP  int     `json:"rsrp"`
	SINR  float64 `json:"sinr"`
	RSRQ  int     `json:"rsrq"`
	Valid bool    `json:"valid"`
}

type bandResult struct {
	Band      int     `json:"band"` // 0 = all bands
	Status    string  `json:"status"`
	Serving   int     `json:"serving_band"`
	RSRP      int     `json:"rsrp"`
	SINR      float64 `json:"sinr"`
	RSRQ      int     `json:"rsrq"`
	SpeedMBps float64 `json:"speed_mbps"`
	Detail    string  `json:"detail"`
}

type bandCardProfile struct {
	Band     int          `json:"band"` // 0 = all bands
	TestedAt string       `json:"tested_at"`
	SaveData bool         `json:"save_data"`
	Results  []bandResult `json:"results"`
	Fallback bool         `json:"fallback"`
	// Disabled: the user switched the lock off; Band keeps the best result so
	// it can be switched back on without retesting.
	Disabled bool `json:"disabled"`
}

func (p bandCardProfile) locks() bool { return p.Band > 0 && !p.Fallback && !p.Disabled }

type bandProfiles struct {
	FullMask string                     `json:"full_mask"`
	Cards    map[string]bandCardProfile `json:"cards"`
}

type bandOptimizerState struct {
	Running    bool         `json:"running"`
	Phase      string       `json:"phase"`
	Note       string       `json:"note"`
	SaveData   bool         `json:"save_data"`
	Operator   string       `json:"operator"`
	Candidates []int        `json:"candidates"`
	Results    []bandResult `json:"results"`
	Chosen     int          `json:"chosen"`
	Error      string       `json:"error"`
	ICCID      string       `json:"iccid"`
	Roaming    bool         `json:"roaming"`
	Radio      bandRadio    `json:"radio"`
	// RegStatus is the last EPS registration status read by the band guard
	// (1 home, 5 roaming).
	RegStatus int `json:"reg_status"`
	// LockedBand is read back from the module: the single band it is locked
	// to, or 0 when several bands are allowed.
	LockedBand int              `json:"locked_band"`
	MaskKnown  bool             `json:"mask_known"`
	Profile    *bandCardProfile `json:"profile"`
}

type bandOptimizer struct {
	mu        sync.Mutex
	state     bandOptimizerState
	profiles  bandProfiles
	loaded    bool
	path      string
	stop      atomic.Bool
	lastICCID string
	lostSince time.Time
}

// ---- parsing ----

var (
	reQNWBand  = regexp.MustCompile(`LTE BAND (\d+)`)
	reQNWPLMN  = regexp.MustCompile(`\+QNWINFO:\s*"[^"]*","(\d{5,6})"`)
	reQCSQ     = regexp.MustCompile(`\+QCSQ:\s*"LTE",(-?\d+),(-?\d+),(-?\d+),(-?\d+)`)
	reBandMask = regexp.MustCompile(`\+QCFG:\s*"band",0x[0-9a-fA-F]+,0x([0-9a-fA-F]+)`)
	reNeighbor = regexp.MustCompile(`"neighbourcell inter","LTE",(\d+)`)
)

func parseBandRadio(qnwinfo, qcsq string) bandRadio {
	r := bandRadio{}
	if m := reQNWBand.FindStringSubmatch(qnwinfo); m != nil {
		r.Band, _ = strconv.Atoi(m[1])
	}
	if m := reQNWPLMN.FindStringSubmatch(qnwinfo); m != nil {
		r.PLMN = m[1]
	}
	if m := reQCSQ.FindStringSubmatch(qcsq); m != nil {
		r.RSRP, _ = strconv.Atoi(m[2])
		sinr, _ := strconv.Atoi(m[3])
		r.SINR = float64(sinr)/5 - 20
		r.RSRQ, _ = strconv.Atoi(m[4])
		r.Valid = r.Band > 0
	}
	return r
}

func parseLTEBandMask(resp string) string {
	if m := reBandMask.FindStringSubmatch(resp); m != nil {
		return strings.ToUpper(strings.TrimLeft(m[1], "0"))
	}
	return ""
}

func bandMaskFor(bands ...int) string {
	mask := new(big.Int)
	for _, band := range bands {
		mask.SetBit(mask, band-1, 1)
	}
	return strings.ToUpper(mask.Text(16))
}

// lockedBandOf returns the single band a mask allows, or 0 for several bands.
func lockedBandOf(hex string) int {
	if bands := bandsInMask(hex); len(bands) == 1 {
		return bands[0]
	}
	return 0
}

func bandsInMask(hex string) []int {
	mask, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		return nil
	}
	bands := []int{}
	for i := 0; i < mask.BitLen(); i++ {
		if mask.Bit(i) == 1 {
			bands = append(bands, i+1)
		}
	}
	return bands
}

// earfcnBand maps a downlink EARFCN to its LTE band for bands used in China.
func earfcnBand(earfcn int) int {
	switch {
	case earfcn < 600:
		return 1
	case earfcn >= 1200 && earfcn < 1950:
		return 3
	case earfcn >= 2400 && earfcn < 2650:
		return 5
	case earfcn >= 3450 && earfcn < 3800:
		return 8
	case earfcn >= 36200 && earfcn < 36350:
		return 34
	case earfcn >= 37750 && earfcn < 38250:
		return 38
	case earfcn >= 38250 && earfcn < 38650:
		return 39
	case earfcn >= 38650 && earfcn < 39650:
		return 40
	case earfcn >= 39650 && earfcn < 41590:
		return 41
	}
	return 0
}

// candidateBands: the operator's China bands plus inter-frequency neighbours,
// limited to what the module supports, in ascending order.
func candidateBands(plmn, neighbours string, supported []int) []int {
	allowed := map[int]bool{}
	for _, band := range supported {
		allowed[band] = true
	}
	wanted := operatorBands[plmn]
	if wanted == nil {
		wanted = fallbackCandidateBands
	}
	set := map[int]bool{}
	for _, band := range wanted {
		if allowed[band] {
			set[band] = true
		}
	}
	for _, m := range reNeighbor.FindAllStringSubmatch(neighbours, -1) {
		earfcn, _ := strconv.Atoi(m[1])
		if band := earfcnBand(earfcn); band > 0 && allowed[band] {
			set[band] = true
		}
	}
	out := []int{}
	for band := range set {
		out = append(out, band)
	}
	sort.Ints(out)
	return out
}

// chooseBand returns the band to keep (0 = all bands). Without any successful
// speed measurement it falls back to comparing signal quality.
func chooseBand(results []bandResult, saveData bool) int {
	if !saveData {
		measured := false
		for _, r := range results {
			measured = measured || (r.Status == "ok" && r.SpeedMBps > 0)
		}
		saveData = !measured
	}
	var all *bandResult
	var best *bandResult
	for i := range results {
		r := &results[i]
		if r.Status != "ok" {
			continue
		}
		if r.Band == 0 {
			all = r
			continue
		}
		if best == nil || (saveData && bandSignalScore(*r) > bandSignalScore(*best)) || (!saveData && r.SpeedMBps > best.SpeedMBps) {
			best = r
		}
	}
	if best == nil {
		return 0
	}
	if all == nil {
		return best.Band
	}
	if saveData {
		if best.SINR >= all.SINR+bandSINRAdvantage && best.RSRP >= -110 {
			return best.Band
		}
		return 0
	}
	if best.SpeedMBps > 0 && best.SpeedMBps >= all.SpeedMBps*bandSpeedAdvantage {
		return best.Band
	}
	return 0
}

func bandSignalScore(r bandResult) float64 {
	return r.SINR + float64(r.RSRP+100)/10
}

// ---- persistence ----

func (b *bandOptimizer) loadLocked() {
	if b.loaded {
		return
	}
	b.loaded = true
	if b.path == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			b.path = filepath.Join(dir, "DJOneHub", "band-profiles.json")
		}
	}
	if data, err := os.ReadFile(b.path); err == nil {
		_ = json.Unmarshal(data, &b.profiles)
	}
	if b.profiles.Cards == nil {
		b.profiles.Cards = map[string]bandCardProfile{}
	}
}

func (b *bandOptimizer) saveLocked() {
	if b.path == "" {
		return
	}
	data, _ := json.MarshalIndent(b.profiles, "", "  ")
	err := os.MkdirAll(filepath.Dir(b.path), 0700)
	if err == nil {
		err = os.WriteFile(b.path, data, 0600)
	}
	if err != nil {
		log.Printf("band profiles not saved: %v", err)
	}
}

func (b *bandOptimizer) snapshot() bandOptimizerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loadLocked()
	s := b.state
	s.Results = append([]bandResult{}, b.state.Results...)
	s.Candidates = append([]int{}, b.state.Candidates...)
	if p, ok := b.profiles.Cards[s.ICCID]; ok && s.ICCID != "" {
		s.Profile = &p
	}
	return s
}

func (b *bandOptimizer) update(fn func(s *bandOptimizerState)) {
	b.mu.Lock()
	fn(&b.state)
	b.mu.Unlock()
}

// ---- module access ----

func (a *app) setLTEBands(mask string) error {
	resp, err := a.runATCommand(fmt.Sprintf(`AT+QCFG="band",0,%s,0,1`, mask), 10*time.Second)
	if err == nil && !strings.Contains(resp, "OK") {
		err = fmt.Errorf("模块拒绝了频段设置：%s", strings.TrimSpace(resp))
	}
	return err
}

func (a *app) readBandRadio() bandRadio {
	qnw, _ := a.runATCommand("AT+QNWINFO", 3*time.Second)
	qcsq, _ := a.runATCommand("AT+QCSQ", 3*time.Second)
	return parseBandRadio(qnw, qcsq)
}

func (a *app) registrationStatus() int {
	resp, _ := a.runATCommand("AT+CEREG?", 3*time.Second)
	return firstNonZeroRegistration(resp)
}

func (a *app) registered() bool {
	status := a.registrationStatus()
	return status == 1 || status == 5
}

func (a *app) currentICCID() string {
	resp, _ := a.runATCommand("AT+QCCID", 3*time.Second)
	return parseUSBATPrefixed(resp, "+QCCID:")
}

// waitBand waits until the module is registered on want (0 = any band).
// A roaming SIM keeps reporting its old registration for ~15 s after a band
// change, then drops to "searching" (CEREG 2) and stays there until network
// selection is restarted. So for roaming, the registration is trusted only
// after that drop (or bandSettleWindow without one), and each drop is nudged
// with a PS attach plus automatic selection, as the signal watchdog does.
func (a *app) waitBand(ctx context.Context, want int) (bandRadio, error) {
	start := time.Now()
	deadline := start.Add(bandRegisterTimeout)
	settled := a.registrationStatus() != 5
	var nudged time.Time
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return bandRadio{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
		if reg := a.registrationStatus(); reg != 1 && reg != 5 {
			settled = true
			if time.Since(nudged) >= bandReselectEvery {
				nudged = time.Now()
				_, _ = a.runATCommand("AT+CGATT=1", 5*time.Second)
				_, _ = a.runATCommand("AT+COPS=0", 5*time.Second)
			}
			continue
		}
		if !settled && time.Since(start) < bandSettleWindow {
			continue
		}
		radio := a.readBandRadio()
		if radio.Valid && (want == 0 || radio.Band == want) {
			return radio, nil
		}
	}
	return bandRadio{}, errors.New("no service on this band")
}

func waitCellularData(ctx context.Context) (string, error) {
	deadline := time.Now().Add(bandDataTimeout)
	var last error
	for time.Now().Before(deadline) {
		iface, err := proxyInterface()
		if err == nil {
			if _, _, err = probeProxyExit(ctx, iface, ""); err == nil {
				return iface, nil
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return "", fmt.Errorf("no data: %v", last)
}

// measureCellularSpeed downloads over cellular for bandSpeedWindow and returns MB/s.
func measureCellularSpeed(ctx context.Context, iface string) (float64, error) {
	defer beginCellularActivity()()
	var last error
	for _, url := range bandSpeedURLs {
		transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return cellularDial(ctx, iface, addr) }, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
		window, cancel := context.WithTimeout(ctx, bandSpeedWindow+5*time.Second)
		req, _ := http.NewRequestWithContext(window, "GET", url, nil)
		req.Header.Set("Range", "bytes=0-200000000")
		// The mirrors reject Go's default User-Agent with 403.
		req.Header.Set("User-Agent", "curl/8")
		resp, err := (&http.Client{Transport: transport}).Do(req)
		if err != nil {
			cancel()
			transport.CloseIdleConnections()
			last = err
			continue
		}
		if resp.StatusCode != 200 && resp.StatusCode != 206 {
			resp.Body.Close()
			cancel()
			transport.CloseIdleConnections()
			last = fmt.Errorf("speed test HTTP %d", resp.StatusCode)
			continue
		}
		start := time.Now()
		var total int64
		buf := make([]byte, 64*1024)
		for time.Since(start) < bandSpeedWindow {
			n, err := resp.Body.Read(buf)
			total += int64(n)
			if err != nil {
				break
			}
		}
		elapsed := time.Since(start).Seconds()
		resp.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		if elapsed <= 0 {
			elapsed = 1
		}
		return float64(total) / 1e6 / elapsed, nil
	}
	return 0, last
}

// ---- optimizer run ----

func (a *app) runBandOptimizer(saveData bool) {
	defer a.moduleOperationMu.Unlock()
	a.moduleVoiceOpMu.Lock()
	defer a.moduleVoiceOpMu.Unlock()
	b := &a.bands
	p := &a.proxy
	wasRunning := p.snapshot().Running
	parent := p.lifecycle
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()

	finish := func(phase, note string, err error) {
		b.update(func(s *bandOptimizerState) {
			s.Running = false
			s.Phase = phase
			s.Note = note
			if err != nil {
				s.Error = err.Error()
			}
		})
		if wasRunning {
			if e := p.start(); e != nil {
				log.Printf("band optimizer: proxy restart: %v", e)
				return
			}
			// Re-registering changed the exit; refresh it instead of showing the
			// old IP. The data session can take a while to come back (roaming
			// SIMs re-select the network), so keep trying for a while.
			for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
				if iface, err := proxyInterface(); err == nil {
					if ip, source, err := probeProxyExit(parent, iface, ""); err == nil {
						p.recordExit(parent, iface, ip, source)
						break
					}
				}
				select {
				case <-parent.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}

	iccid := a.currentICCID()
	cimi, _ := a.runATCommand("AT+CIMI", 3*time.Second)
	imsi := parseUSBATIMSI(cimi)
	noteSIMIMSI(imsi)
	bandResp, _ := a.runATCommand(`AT+QCFG="band"`, 3*time.Second)
	neighbours, _ := a.runATCommand(`AT+QENG="neighbourcell"`, 5*time.Second)
	radio := a.readBandRadio()

	b.mu.Lock()
	b.loadLocked()
	full := b.profiles.FullMask
	if current := parseLTEBandMask(bandResp); full == "" && len(bandsInMask(current)) > 1 {
		full = current
	}
	if full == "" {
		full = defaultFullBandMask
	}
	b.profiles.FullMask = full
	b.saveLocked()
	b.mu.Unlock()

	candidates := candidateBands(radio.PLMN, neighbours, bandsInMask(full))
	b.update(func(s *bandOptimizerState) {
		s.ICCID = iccid
		s.Roaming = len(imsi) >= 3 && !strings.HasPrefix(imsi, "460")
		s.Operator = radio.PLMN
		s.Candidates = candidates
	})
	if len(candidates) == 0 {
		finish("failed", "没有可测试的频段", errors.New("当前运营商没有模块支持的候选频段"))
		return
	}
	p.stop()

	test := func(band int) bandResult {
		result := bandResult{Band: band, Status: "testing"}
		b.update(func(s *bandOptimizerState) {
			s.Results = append(s.Results, result)
			if band == 0 {
				s.Note = "正在测试：全部频段（基准）"
			} else {
				s.Note = fmt.Sprintf("正在测试：Band %d", band)
			}
		})
		mask := full
		if band > 0 {
			mask = bandMaskFor(band)
		}
		record := func(r bandResult) bandResult {
			b.update(func(s *bandOptimizerState) { s.Results[len(s.Results)-1] = r })
			return r
		}
		if err := a.setLTEBands(mask); err != nil {
			result.Status, result.Detail = "failed", err.Error()
			return record(result)
		}
		radio, err := a.waitBand(ctx, band)
		if err != nil {
			result.Status, result.Detail = "no_service", "此频段无信号或无法注册"
			return record(result)
		}
		result.Serving, result.RSRP, result.SINR, result.RSRQ = radio.Band, radio.RSRP, radio.SINR, radio.RSRQ
		iface, err := waitCellularData(ctx)
		if err != nil {
			result.Status, result.Detail = "failed", "已注册但无法上网"
			return record(result)
		}
		if !saveData {
			speed, err := measureCellularSpeed(ctx, iface)
			if err != nil {
				result.Detail = "测速失败：" + err.Error()
			}
			result.SpeedMBps = float64(int(speed*100)) / 100
		}
		result.Status = "ok"
		return record(result)
	}

	results := []bandResult{test(0)}
	for _, band := range candidates {
		if b.stop.Load() || ctx.Err() != nil {
			break
		}
		results = append(results, test(band))
	}
	if b.stop.Load() || ctx.Err() != nil {
		_ = a.setLTEBands(full)
		_, _ = a.waitBand(ctx, 0)
		finish("stopped", "已停止测试，已恢复全部频段", nil)
		return
	}

	chosen := chooseBand(results, saveData)
	mask := full
	if chosen > 0 {
		mask = bandMaskFor(chosen)
	}
	if err := a.setLTEBands(mask); err != nil {
		_ = a.setLTEBands(full)
		finish("failed", "应用频段失败，已恢复全部频段", err)
		return
	}
	if _, err := a.waitBand(ctx, chosen); err != nil && chosen > 0 {
		_ = a.setLTEBands(full)
		chosen = 0
	}
	b.mu.Lock()
	b.profiles.Cards[iccid] = bandCardProfile{Band: chosen, TestedAt: time.Now().UTC().Format(time.RFC3339), SaveData: saveData, Results: results}
	b.saveLocked()
	b.lastICCID = iccid
	b.lostSince = time.Time{}
	b.mu.Unlock()
	note := "保持全部频段：锁定单个频段没有明显更好"
	if chosen > 0 {
		note = fmt.Sprintf("已锁定 Band %d；信号丢失 30 秒会自动恢复全部频段", chosen)
	}
	b.update(func(s *bandOptimizerState) { s.Chosen, s.LockedBand, s.MaskKnown = chosen, chosen, true })
	finish("done", note, nil)
}

// ---- guard: per-SIM apply and fallback ----

func (a *app) runBandGuard(ctx context.Context) {
	// Read the lock state right away so the App does not show "unlocked" while waiting for the first tick.
	a.bandGuardOnce()
	ticker := time.NewTicker(bandGuardInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.bandGuardOnce()
		}
	}
}

func (a *app) bandGuardOnce() {
	if a.updatePending.Load() || !a.tryModuleOperation() {
		return
	}
	defer a.moduleOperationMu.Unlock()
	b := &a.bands
	iccid := a.currentICCID()
	radio := a.readBandRadio()
	reg := a.registrationStatus()
	online := reg == 1 || reg == 5
	bandResp, _ := a.runATCommand(`AT+QCFG="band"`, 3*time.Second)
	current := parseLTEBandMask(bandResp)
	b.mu.Lock()
	b.loadLocked()
	b.state.ICCID = iccid
	b.state.Radio = radio
	b.state.RegStatus = reg
	b.state.LockedBand, b.state.MaskKnown = lockedBandOf(current), current != ""
	full := b.profiles.FullMask
	profile, known := b.profiles.Cards[iccid]
	changed := iccid != "" && iccid != b.lastICCID
	if changed {
		b.lastICCID = iccid
		b.lostSince = time.Time{}
	}
	b.mu.Unlock()
	// The proxy's DNS policy depends on whether this is a mainland SIM.
	if iccid != "" && (changed || cellularSIMHome.Load() == simHomeUnknown) {
		cimi, _ := a.runATCommand("AT+CIMI", 3*time.Second)
		noteSIMIMSI(parseUSBATIMSI(cimi))
	}
	if iccid == "" || full == "" {
		return
	}
	// A different SIM: apply its saved band, or all bands for an unknown SIM.
	if changed {
		mask := full
		if known && profile.locks() {
			mask = bandMaskFor(profile.Band)
		}
		if current != mask {
			log.Printf("band guard: SIM %s… inserted, applying LTE mask %s", iccid[:min(6, len(iccid))], mask)
			_ = a.setLTEBands(mask)
		}
		return
	}
	if !known || !profile.locks() {
		return
	}
	if online {
		b.mu.Lock()
		b.lostSince = time.Time{}
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	if b.lostSince.IsZero() {
		b.lostSince = time.Now()
	}
	lostFor := time.Since(b.lostSince)
	b.mu.Unlock()
	if lostFor < bandFallbackAfter {
		return
	}
	log.Printf("band guard: Band %d without service for %s, restoring all bands", profile.Band, lostFor.Round(time.Second))
	if err := a.setLTEBands(full); err != nil {
		log.Printf("band guard: restore failed: %v", err)
		return
	}
	b.mu.Lock()
	profile.Fallback = true
	b.profiles.Cards[iccid] = profile
	b.saveLocked()
	b.lostSince = time.Time{}
	b.mu.Unlock()
}

// ---- API ----

func (a *app) bandStatusAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, a.bands.snapshot())
}

func (a *app) bandActionAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action   string `json:"action"`
		SaveData *bool  `json:"save_data"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	b := &a.bands
	if body.Action == "stop" {
		b.stop.Store(true)
		writeJSON(w, 200, b.snapshot())
		return
	}
	if a.demo {
		writeError(w, 409, "演示模式下不能操作射频")
		return
	}
	if !a.tryModuleOperation() {
		writeError(w, 409, errModuleBusy)
		return
	}
	a.callMu.RLock()
	busy := a.activeCall != nil
	a.callMu.RUnlock()
	if busy {
		a.moduleOperationMu.Unlock()
		writeError(w, 409, errCallActive)
		return
	}
	switch body.Action {
	case "optimize":
		cimi, _ := a.runATCommand("AT+CIMI", 3*time.Second)
		imsi := parseUSBATIMSI(cimi)
		noteSIMIMSI(imsi)
		// Speed decides the band, so measure by default even when roaming;
		// the client opts into the signal-only test.
		saveData := false
		if body.SaveData != nil {
			saveData = *body.SaveData
		}
		b.stop.Store(false)
		b.update(func(s *bandOptimizerState) {
			*s = bandOptimizerState{Running: true, Phase: "testing", SaveData: saveData, Note: "正在准备…", ICCID: s.ICCID, Radio: s.Radio}
		})
		go a.runBandOptimizer(saveData)
		writeJSON(w, 202, b.snapshot())
	case "unlock", "reset":
		defer a.moduleOperationMu.Unlock()
		b.mu.Lock()
		b.loadLocked()
		full := b.profiles.FullMask
		if full == "" {
			full = defaultFullBandMask
		}
		iccid := a.currentICCID()
		if p, ok := b.profiles.Cards[iccid]; ok {
			p.Disabled = true
			p.Fallback = false
			b.profiles.Cards[iccid] = p
			b.saveLocked()
		}
		b.mu.Unlock()
		if err := a.setLTEBands(full); err != nil {
			writeError(w, 502, friendlyProxyError(err))
			return
		}
		b.update(func(s *bandOptimizerState) { s.ICCID, s.LockedBand, s.MaskKnown = iccid, 0, true })
		writeJSON(w, 200, b.snapshot())
	case "lock":
		defer a.moduleOperationMu.Unlock()
		iccid := a.currentICCID()
		b.mu.Lock()
		b.loadLocked()
		p, ok := b.profiles.Cards[iccid]
		b.mu.Unlock()
		if !ok || p.Band == 0 {
			writeError(w, 409, "这张卡还没有测出更优的单一频段，请先自动选最优频段")
			return
		}
		if err := a.setLTEBands(bandMaskFor(p.Band)); err != nil {
			writeError(w, 502, friendlyProxyError(err))
			return
		}
		p.Disabled, p.Fallback = false, false
		b.mu.Lock()
		b.profiles.Cards[iccid] = p
		b.saveLocked()
		b.lostSince = time.Time{}
		b.mu.Unlock()
		b.update(func(s *bandOptimizerState) { s.ICCID, s.LockedBand, s.MaskKnown = iccid, p.Band, true })
		writeJSON(w, 200, b.snapshot())
	default:
		a.moduleOperationMu.Unlock()
		writeError(w, 400, "未知的频段操作")
	}
}

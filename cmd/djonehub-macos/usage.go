package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cellular data is metered per SIM (ICCID) for as long as the SIM is used in
// this module, across SIM swaps, backend restarts and updates. The source is
// the module interface's own byte counters, which cover everything sent over
// cellular (proxy traffic, DNS, exit checks, band speed tests), minus the
// 14-byte Ethernet header per packet the carrier never sees. Usage is kept per
// local day so a billing cycle starting on any day of the month can be
// summed, and re-summed when the user changes that day.

const (
	usagePollInterval  = 10 * time.Second
	usageSaveInterval  = time.Minute
	usageKeepDays      = 400
	usageEthernetBytes = 14
	usageMiB           = 1 << 20
)

type interfaceCounterReading struct {
	Rx, Tx, RxPkts, TxPkts uint64
}

type usageBytes struct {
	Rx    int64 `json:"rx"`
	Tx    int64 `json:"tx"`
	Proxy int64 `json:"proxy"`
}

func (u usageBytes) total() int64 { return u.Rx + u.Tx }

func (u *usageBytes) add(o usageBytes) {
	u.Rx += o.Rx
	u.Tx += o.Tx
	u.Proxy += o.Proxy
}

type simPlan struct {
	// CycleDay is the day of month the plan renews (the activation day);
	// months without that day renew on their last day.
	CycleDay int   `json:"cycle_day"`
	QuotaMB  int64 `json:"quota_mb"`
	// AutoStop pauses the proxy once the cycle's quota is used up.
	AutoStop bool `json:"auto_stop"`
}

type simUsageCard struct {
	Name       string                `json:"name"`
	IMSIPrefix string                `json:"imsi_prefix"`
	FirstSeen  string                `json:"first_seen"`
	LastSeen   string                `json:"last_seen"`
	Plan       simPlan               `json:"plan"`
	Total      usageBytes            `json:"total"`
	Days       map[string]usageBytes `json:"days"`
	// Adjust holds per-cycle corrections (keyed by cycle start date) from
	// calibrating against the carrier's figure.
	Adjust map[string]int64 `json:"adjust,omitempty"`
}

type usageCounterState struct {
	Iface string `json:"iface"`
	interfaceCounterReading
	Proxy uint64 `json:"proxy"`
}

type usageFile struct {
	Counter *usageCounterState       `json:"counter,omitempty"`
	Cards   map[string]*simUsageCard `json:"cards"`
}

type usageLedger struct {
	mu       sync.Mutex
	loaded   bool
	path     string
	file     usageFile
	dirty    bool
	saved    time.Time
	current  string
	err      string
	stopNote string
}

// usageDelta turns two counter readings into bytes used since the previous
// one. A smaller reading means the interface was recreated (module replug) or
// the backend restarted, and the counter started again from zero.
func usageDelta(prev *usageCounterState, iface string, now interfaceCounterReading, proxy uint64) usageBytes {
	if prev == nil {
		return usageBytes{}
	}
	since := func(cur, last uint64) uint64 {
		if cur >= last {
			return cur - last
		}
		return cur
	}
	var rx, tx, rxPkts, txPkts uint64
	if prev.Iface == iface && now.Rx >= prev.Rx && now.Tx >= prev.Tx {
		rx, tx = now.Rx-prev.Rx, now.Tx-prev.Tx
		rxPkts, txPkts = since(now.RxPkts, prev.RxPkts), since(now.TxPkts, prev.TxPkts)
	} else {
		// A new interface: everything on it happened after the last reading.
		rx, tx, rxPkts, txPkts = now.Rx, now.Tx, now.RxPkts, now.TxPkts
	}
	ip := func(bytes, pkts uint64) int64 {
		v := int64(bytes) - int64(pkts)*usageEthernetBytes
		if v < 0 {
			return 0
		}
		return v
	}
	return usageBytes{Rx: ip(rx, rxPkts), Tx: ip(tx, txPkts), Proxy: int64(since(proxy, prev.Proxy))}
}

func daysInMonth(year int, month time.Month, loc *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
}

func cycleDate(year int, month time.Month, day int, loc *time.Location) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	return first.AddDate(0, 0, min(day, daysInMonth(first.Year(), first.Month(), loc))-1)
}

// billingCycle returns the [start, end) local dates of the cycle containing t.
func billingCycle(t time.Time, day int) (time.Time, time.Time) {
	if day < 1 || day > 31 {
		day = 1
	}
	loc := t.Location()
	start := cycleDate(t.Year(), t.Month(), day, loc)
	if t.Before(start) {
		prev := time.Date(t.Year(), t.Month()-1, 1, 0, 0, 0, 0, loc)
		start = cycleDate(prev.Year(), prev.Month(), day, loc)
	}
	next := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, loc)
	return start, cycleDate(next.Year(), next.Month(), day, loc)
}

func (c *simUsageCard) cycleUsage(start, end time.Time, adjusted bool) usageBytes {
	var sum usageBytes
	for key, day := range c.Days {
		d, err := time.ParseInLocation("2006-01-02", key, start.Location())
		if err == nil && !d.Before(start) && d.Before(end) {
			sum.add(day)
		}
	}
	if adjusted {
		// Corrections apply to the received side, which dominates usage.
		sum.Rx += c.Adjust[start.Format("2006-01-02")]
		if sum.Rx < 0 {
			sum.Rx = 0
		}
	}
	return sum
}

func (c *simUsageCard) overQuota(now time.Time) bool {
	if c.Plan.QuotaMB <= 0 {
		return false
	}
	start, end := billingCycle(now, c.Plan.CycleDay)
	return c.cycleUsage(start, end, true).total() >= c.Plan.QuotaMB*usageMiB
}

func (l *usageLedger) loadLocked(dir string) {
	if l.loaded {
		return
	}
	l.loaded = true
	l.file.Cards = map[string]*simUsageCard{}
	if dir == "" {
		l.err = "无法确定流量记录的保存位置"
		return
	}
	l.path = filepath.Join(dir, "sim-usage.json")
	b, err := os.ReadFile(l.path)
	if os.IsNotExist(err) {
		return
	}
	if err == nil {
		err = json.Unmarshal(b, &l.file)
	}
	if err != nil {
		// Never overwrite a ledger that could not be read.
		l.err = "流量记录读取失败：" + err.Error()
		l.path = ""
		l.file = usageFile{Cards: map[string]*simUsageCard{}}
		return
	}
	if l.file.Cards == nil {
		l.file.Cards = map[string]*simUsageCard{}
	}
}

func (l *usageLedger) saveLocked(force bool) {
	if l.path == "" || !l.dirty || (!force && time.Since(l.saved) < usageSaveInterval) {
		return
	}
	b, err := json.MarshalIndent(l.file, "", " ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(l.path), 0700)
	}
	if err == nil {
		tmp := l.path + ".tmp"
		if err = os.WriteFile(tmp, b, 0600); err == nil {
			err = os.Rename(tmp, l.path)
		}
	}
	if err != nil {
		l.err = "流量记录保存失败：" + err.Error()
		return
	}
	l.err, l.dirty, l.saved = "", false, time.Now()
}

func (l *usageLedger) cardLocked(iccid string, now time.Time) *simUsageCard {
	card := l.file.Cards[iccid]
	if card == nil {
		card = &simUsageCard{FirstSeen: now.UTC().Format(time.RFC3339), Plan: simPlan{CycleDay: now.Day()}, Days: map[string]usageBytes{}}
		l.file.Cards[iccid] = card
		l.dirty = true
	}
	if card.Days == nil {
		card.Days = map[string]usageBytes{}
	}
	return card
}

// record books the bytes used since the last reading to iccid (the SIM seen
// most recently; nothing flows while no SIM is present).
func (l *usageLedger) record(dir, iccid, iface string, reading interfaceCounterReading, proxy uint64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked(dir)
	if iccid != "" {
		l.current = iccid
	}
	delta := usageDelta(l.file.Counter, iface, reading, proxy)
	l.file.Counter = &usageCounterState{Iface: iface, interfaceCounterReading: reading, Proxy: proxy}
	l.dirty = true
	if l.current != "" {
		card := l.cardLocked(l.current, now)
		card.LastSeen = now.UTC().Format(time.RFC3339)
		if delta != (usageBytes{}) {
			key := now.Format("2006-01-02")
			day := card.Days[key]
			day.add(delta)
			card.Days[key] = day
			card.Total.add(delta)
		}
		if len(card.Days) > usageKeepDays {
			cutoff := now.AddDate(0, 0, -usageKeepDays).Format("2006-01-02")
			for key := range card.Days {
				if key < cutoff {
					delete(card.Days, key)
				}
			}
		}
	}
	l.saveLocked(false)
}

func (l *usageLedger) noteIMSI(iccid, imsi string) {
	if iccid == "" || len(imsi) < 5 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded {
		return
	}
	card := l.cardLocked(iccid, time.Now())
	if prefix := imsi[:min(6, len(imsi))]; card.IMSIPrefix != prefix {
		card.IMSIPrefix = prefix
		l.dirty = true
	}
}

func (l *usageLedger) needsIMSI(iccid string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	card := l.file.Cards[iccid]
	return iccid != "" && l.loaded && (card == nil || card.IMSIPrefix == "")
}

// blocked reports whether the current SIM used up its quota with auto-stop on.
func (l *usageLedger) blocked(now time.Time) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	card := l.file.Cards[l.current]
	if card == nil || !card.Plan.AutoStop || !card.overQuota(now) {
		return false, ""
	}
	start, end := billingCycle(now, card.Plan.CycleDay)
	used := card.cycleUsage(start, end, true).total()
	return true, fmt.Sprintf("本卡本期流量已用 %s，达到套餐 %s，代理已按设置暂停；%s 起的新周期会自动恢复。可在 设置 → 流量统计 中调高套餐或关闭自动暂停。",
		formatUsageBytes(used), formatUsageBytes(card.Plan.QuotaMB*usageMiB), end.Format("1月2日"))
}

func formatUsageBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= usageMiB:
		return fmt.Sprintf("%.1f MB", float64(b)/usageMiB)
	default:
		return fmt.Sprintf("%d KB", b/1024)
	}
}

type usageCycleView struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Used    int64  `json:"used"`
	Rx      int64  `json:"rx"`
	Tx      int64  `json:"tx"`
	Proxy   int64  `json:"proxy"`
	Percent int    `json:"percent"`
}

type usageCardView struct {
	ICCID      string           `json:"iccid"`
	Name       string           `json:"name"`
	IMSIPrefix string           `json:"imsi_prefix"`
	FirstSeen  string           `json:"first_seen"`
	LastSeen   string           `json:"last_seen"`
	Current    bool             `json:"current"`
	Plan       simPlan          `json:"plan"`
	Total      int64            `json:"total"`
	Cycle      usageCycleView   `json:"cycle"`
	History    []usageCycleView `json:"history"`
}

type usageView struct {
	CurrentICCID string          `json:"current_iccid"`
	Blocked      bool            `json:"blocked"`
	BlockedNote  string          `json:"blocked_note"`
	Error        string          `json:"error"`
	Cards        []usageCardView `json:"cards"`
	Network      *usageNetwork   `json:"network,omitempty"`
}

// usageNetwork tells whether the current SIM's data is roaming data.
type usageNetwork struct {
	Registered  bool   `json:"registered"`
	Roaming     bool   `json:"roaming"`
	HomePLMN    string `json:"home_plmn"`
	ServingPLMN string `json:"serving_plmn"`
}

// cellularRoaming reports roaming data. Travel and other foreign profiles
// often list the visited networks as equivalent home PLMNs, so the module
// reports home registration (1) while the carrier still bills roaming; a
// country (MCC) differing between the SIM and the serving network settles it.
func cellularRoaming(reg int, imsi, servingPLMN string) bool {
	if reg == 5 {
		return true
	}
	return reg == 1 && len(imsi) >= 3 && len(servingPLMN) >= 3 && imsi[:3] != servingPLMN[:3]
}

func (l *usageLedger) view(now time.Time) usageView {
	blocked, note := l.blocked(now)
	l.mu.Lock()
	defer l.mu.Unlock()
	v := usageView{CurrentICCID: l.current, Blocked: blocked, BlockedNote: note, Error: l.err, Cards: []usageCardView{}}
	for iccid, card := range l.file.Cards {
		cv := usageCardView{ICCID: iccid, Name: card.Name, IMSIPrefix: card.IMSIPrefix, FirstSeen: card.FirstSeen, LastSeen: card.LastSeen,
			Current: iccid == l.current, Plan: card.Plan, Total: card.Total.total()}
		at := now
		for i := 0; i < 13; i++ {
			start, end := billingCycle(at, card.Plan.CycleDay)
			u := card.cycleUsage(start, end, true)
			cycle := usageCycleView{Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Used: u.total(), Rx: u.Rx, Tx: u.Tx, Proxy: u.Proxy}
			if card.Plan.QuotaMB > 0 {
				cycle.Percent = int(u.total() * 100 / (card.Plan.QuotaMB * usageMiB))
			}
			if i == 0 {
				cv.Cycle = cycle
			} else if u.total() > 0 {
				cv.History = append(cv.History, cycle)
			}
			at = start.AddDate(0, 0, -1)
		}
		v.Cards = append(v.Cards, cv)
	}
	sort.Slice(v.Cards, func(i, j int) bool {
		if v.Cards[i].Current != v.Cards[j].Current {
			return v.Cards[i].Current
		}
		return v.Cards[i].LastSeen > v.Cards[j].LastSeen
	})
	return v
}

func (a *app) usageDir() string {
	p := &a.proxy
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadHistoryLocked()
	if p.historyPath == "" {
		return ""
	}
	return filepath.Dir(p.historyPath)
}

func (a *app) runUsageLedger(ctx context.Context) {
	ticker := time.NewTicker(usagePollInterval)
	defer ticker.Stop()
	defer func() {
		a.usage.mu.Lock()
		a.usage.saveLocked(true)
		a.usage.mu.Unlock()
	}()
	for {
		a.usageOnce()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) usageOnce() {
	iface, err := proxyInterface()
	if err != nil {
		return
	}
	reading, err := interfaceCounters(iface)
	if err != nil {
		return
	}
	iccid := a.bands.snapshot().ICCID
	if iccid == "" && a.tryModuleOperation() {
		iccid = a.currentICCID()
		a.moduleOperationMu.Unlock()
	}
	p := &a.proxy
	now := time.Now()
	a.usage.record(a.usageDir(), iccid, iface, reading, p.uploaded.Load()+p.downloaded.Load(), now)
	if a.usage.needsIMSI(iccid) && a.tryModuleOperation() {
		cimi, _ := a.runATCommand("AT+CIMI", 3*time.Second)
		a.moduleOperationMu.Unlock()
		a.usage.noteIMSI(iccid, parseUSBATIMSI(cimi))
	}
	if blocked, note := a.usage.blocked(now); blocked {
		// Keep the auto-start wish: the proxy resumes by itself in the next
		// cycle or once the quota is raised.
		p.mu.Lock()
		running := p.listener != nil
		if running {
			p.stopLocked()
		}
		p.state.Error = note
		p.mu.Unlock()
		if running {
			log.Printf("cellular proxy paused: SIM %s… reached its data quota", iccid[:min(6, len(iccid))])
		}
	}
}

func (a *app) usageView() usageView {
	v := a.usage.view(time.Now())
	bands := a.bands.snapshot()
	if bands.ICCID == "" || bands.ICCID != v.CurrentICCID {
		return v
	}
	n := &usageNetwork{Registered: bands.RegStatus == 1 || bands.RegStatus == 5, ServingPLMN: bands.Radio.PLMN}
	for _, card := range v.Cards {
		if card.Current {
			n.HomePLMN = card.IMSIPrefix
		}
	}
	n.Roaming = n.Registered && cellularRoaming(bands.RegStatus, n.HomePLMN, n.ServingPLMN)
	v.Network = n
	return v
}

func (a *app) usageAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, a.usageView())
		return
	}
	var body struct {
		Action   string   `json:"action"`
		ICCID    string   `json:"iccid"`
		Name     *string  `json:"name"`
		CycleDay *int     `json:"cycle_day"`
		QuotaMB  *int64   `json:"quota_mb"`
		AutoStop *bool    `json:"auto_stop"`
		UsedMB   *float64 `json:"used_mb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, 400, "请求格式不正确")
		return
	}
	l := &a.usage
	l.mu.Lock()
	card := l.file.Cards[body.ICCID]
	if card == nil {
		l.mu.Unlock()
		writeError(w, 404, "没有这张卡的流量记录")
		return
	}
	switch body.Action {
	case "plan":
		if body.Name != nil {
			card.Name = strings.TrimSpace(*body.Name)
		}
		if body.CycleDay != nil {
			if *body.CycleDay < 1 || *body.CycleDay > 31 {
				l.mu.Unlock()
				writeError(w, 400, "每月开始日需在 1~31 之间")
				return
			}
			card.Plan.CycleDay = *body.CycleDay
		}
		if body.QuotaMB != nil {
			card.Plan.QuotaMB = max(0, *body.QuotaMB)
		}
		if body.AutoStop != nil {
			card.Plan.AutoStop = *body.AutoStop
		}
	case "calibrate":
		if body.UsedMB == nil || *body.UsedMB < 0 {
			l.mu.Unlock()
			writeError(w, 400, "请填写运营商显示的本期已用流量")
			return
		}
		now := time.Now()
		start, end := billingCycle(now, card.Plan.CycleDay)
		measured := card.cycleUsage(start, end, false).total()
		if card.Adjust == nil {
			card.Adjust = map[string]int64{}
		}
		card.Adjust[start.Format("2006-01-02")] = int64(*body.UsedMB*usageMiB) - measured
	default:
		l.mu.Unlock()
		writeError(w, 400, "未知操作")
		return
	}
	l.dirty = true
	l.saveLocked(true)
	l.mu.Unlock()
	writeJSON(w, 200, a.usageView())
}

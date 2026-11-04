package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const proxyAddress = "127.0.0.1:10909"

const (
	reconnectCooldown = 8 * time.Second
	// Like a quick airplane-mode toggle. Measured on a roaming SIM, shorter
	// pauses do not pay off: with 1 s off re-registration took longer and the
	// old IP came back more often, so the total was no faster. Re-registration
	// plus data bring-up (~5 s) cannot be skipped: the module refuses to drop
	// only the default bearer.
	radioOffDuration = 3 * time.Second
	// How often recovery checks whether data works again after the radio is on.
	reconnectPollInterval = 500 * time.Millisecond
	// A second cycle runs only when the exact same IP comes back; a new IP in the
	// same segment counts as success because the carrier's NAT pool decides that.
	maxReconnectAttempts    = 2
	reconnectAttemptTimeout = 60 * time.Second
)

var workerProxyAddress string

func proxyListenAddress() string {
	if workerProxyAddress != "" {
		return workerProxyAddress
	}
	return proxyAddress
}

type proxyState struct {
	IPSource      string         `json:"ip_source"`
	History       []proxyIPVisit `json:"history"`
	HistoryError  string         `json:"history_error"`
	Location      string         `json:"location"`
	ISP           string         `json:"isp"`
	CheckedAt     string         `json:"checked_at"`
	LocationError string         `json:"location_error"`
	Running       bool           `json:"running"`
	Address       string         `json:"address"`
	Interface     string         `json:"interface"`
	PublicIP      string         `json:"public_ip"`
	// The Mac's own address in the carrier IPv6 prefix as sites see it; empty without IPv6.
	PublicIPv6 string `json:"public_ipv6"`
	PreviousIP string `json:"previous_ip"`
	Phase      string `json:"phase"`
	// Filled after a reconnect finishes: wall time from click to verified exit, and radio cycles used.
	ReconnectSeconds  float64 `json:"reconnect_seconds"`
	ReconnectAttempts int     `json:"reconnect_attempts"`
	// Live progress of the running (or last) reconnect: mode "ip" or "segment",
	// attempt cap, a human-readable note and every exit IP seen per cycle.
	ReconnectMode  string   `json:"reconnect_mode"`
	ReconnectMax   int      `json:"reconnect_max"`
	ReconnectNote  string   `json:"reconnect_note"`
	ReconnectTried []string `json:"reconnect_tried"`
	AvoidHours     int      `json:"avoid_hours"`
	// AutoStart: the user turned the proxy on and has not stopped it manually.
	AutoStart   bool   `json:"auto_start"`
	Error       string `json:"error"`
	Connections int    `json:"connections"`
	Uploaded    uint64 `json:"uploaded"`
	Downloaded  uint64 `json:"downloaded"`
}
type cellularProxy struct {
	lifecycle       context.Context
	historyLoaded   bool
	fraudFilling    atomic.Bool
	historyPath     string
	autoStartLoaded bool
	mu              sync.Mutex
	listener        net.Listener
	clients         map[net.Conn]bool
	state           proxyState
	cancel          context.CancelFunc
	lastReconnect   time.Time
	stopRotation    atomic.Bool
	uploaded        atomic.Uint64
	downloaded      atomic.Uint64
}

func (p *cellularProxy) snapshot() proxyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadHistoryLocked()
	p.loadAutoStartLocked()
	s := p.state
	s.History = append([]proxyIPVisit{}, p.state.History...)
	s.ReconnectTried = append([]string{}, p.state.ReconnectTried...)
	s.Address = proxyListenAddress()
	s.Running = p.listener != nil
	s.Connections = len(p.clients)
	s.Uploaded = p.uploaded.Load()
	s.Downloaded = p.downloaded.Load()
	return s
}
func (p *cellularProxy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}
func (p *cellularProxy) stopLocked() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	if p.listener != nil {
		p.listener.Close()
		p.listener = nil
	}
	for c := range p.clients {
		c.Close()
	}
}
func (p *cellularProxy) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		return nil
	}
	iface, err := proxyInterface()
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp4", proxyListenAddress())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.listener = l
	p.cancel = cancel
	p.clients = make(map[net.Conn]bool)
	go p.monitor(ctx, iface)
	p.state.Interface = iface
	p.state.Error = ""
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.listener != l || len(p.clients) >= 128 {
				p.mu.Unlock()
				c.Close()
				continue
			}
			p.clients[c] = true
			p.mu.Unlock()
			go func() {
				defer func() { c.Close(); p.mu.Lock(); delete(p.clients, c); p.mu.Unlock() }()
				p.handle(ctx, c, iface)
			}()
		}
	}()
	return nil
}
func socksReply(c net.Conn, code byte) { _, _ = c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}) }
func (p *cellularProxy) handle(ctx context.Context, c net.Conn, iface string) {
	p.handleWithDial(ctx, c, iface, cellularDial)
}
func (p *cellularProxy) handleWithDial(ctx context.Context, c net.Conn, iface string, dial func(context.Context, string, string) (net.Conn, error)) {
	c.SetDeadline(time.Now().Add(15 * time.Second))
	h := make([]byte, 2)
	if _, err := io.ReadFull(c, h); err != nil || h[0] != 5 || h[1] == 0 {
		return
	}
	methods := make([]byte, int(h[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	allowed := false
	for _, m := range methods {
		if m == 0 {
			allowed = true
		}
	}
	if !allowed {
		c.Write([]byte{5, 255})
		return
	}
	c.Write([]byte{5, 0})
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != 5 || req[2] != 0 {
		return
	}
	if req[1] != 1 {
		socksReply(c, 7)
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		b := make([]byte, 1)
		if _, err := io.ReadFull(c, b); err != nil || b[0] == 0 {
			return
		}
		name := make([]byte, int(b[0]))
		if _, err := io.ReadFull(c, name); err != nil {
			return
		}
		host = string(name)
	case 4:
		b := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		socksReply(c, 8)
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(c, port); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	upstream, err := dial(dialCtx, iface, target)
	cancel()
	if err != nil {
		socksReply(c, 4)
		return
	}
	defer upstream.Close()
	socksReply(c, 0)
	c.SetDeadline(time.Time{})
	// Both directions expire on inactivity. Stop cancels pending dials and closes clients.
	done := make(chan struct{})
	go func() { defer close(done); relayProxy(upstream, c, &p.uploaded); upstream.(*net.TCPConn).CloseWrite() }()
	relayProxy(c, upstream, &p.downloaded)
	c.Close()
	upstream.Close()
	<-done
}
func relayProxy(dst, src net.Conn, counter *atomic.Uint64) {
	b := make([]byte, 32*1024)
	for {
		src.SetReadDeadline(time.Now().Add(2 * time.Minute))
		n, err := src.Read(b)
		if n > 0 {
			dst.SetWriteDeadline(time.Now().Add(30 * time.Second))
			w, e := dst.Write(b[:n])
			counter.Add(uint64(w))
			if e != nil || w != n {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// DNS races the module's DHCP resolver and public resolvers, with every socket
// interface-bound to cellular; broadband DNS is never used.
func cellularDial(ctx context.Context, iface, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else if ips, err = resolveCellularIPs(ctx, iface, host); err != nil {
		return nil, err
	}
	withIPv6 := cellularHasIPv6(iface)
	var v4, v6 []string
	for _, ip := range ips {
		if ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			v4 = append(v4, net.JoinHostPort(ip.String(), port))
		} else if withIPv6 && isPublicIPv6(ip) {
			v6 = append(v6, net.JoinHostPort(ip.String(), port))
		}
	}
	if len(v4) == 0 && len(v6) == 0 {
		if !withIPv6 {
			return nil, errors.New("only public IPv4 destinations are supported")
		}
		return nil, errors.New("only public destinations are supported")
	}
	return dialCellularFamilies(ctx, iface, v4, v6)
}

// cellularDialIPv6 connects over IPv6 only, for IPv6 exit detection.
func cellularDialIPv6(ctx context.Context, iface, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolveCellularIPs(ctx, iface, host)
	if err != nil {
		return nil, err
	}
	var v6 []string
	for _, ip := range ips {
		if isPublicIPv6(ip) {
			v6 = append(v6, net.JoinHostPort(ip.String(), port))
		}
	}
	if len(v6) == 0 {
		return nil, errors.New("no IPv6 address for " + host)
	}
	return dialCellularList(ctx, iface, v6)
}

func isPublicIPv6(ip net.IP) bool {
	return ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate()
}

// IPv4 stays the primary family so sites keep seeing the one exit IP the App
// shows. IPv6 starts after a short delay as a fallback (IPv6-only foreign
// SIMs, IPv6-only sites). Once IPv4 has failed while IPv6 worked, IPv6 leads
// for a while so every connection does not wait out a dead IPv4 attempt.
const (
	cellularFamilyFallbackDelay = time.Second
	cellularIPv6PreferredFor    = 5 * time.Minute
)

var (
	cellularIPv6PreferredUntil atomic.Int64
	cellularIPv6Wins           atomic.Int32
)

// A failed IPv4 attempt, or three IPv6 wins in a row while IPv4 was still
// connecting (a bearer that silently drops IPv4), make IPv6 lead. Any IPv4
// success hands the lead back, so one slow IPv4 handshake changes nothing.
func noteCellularFamilyWin(ipv6, ipv4Failed bool) {
	if !ipv6 {
		cellularIPv6Wins.Store(0)
		cellularIPv6PreferredUntil.Store(0)
		return
	}
	if ipv4Failed || cellularIPv6Wins.Add(1) >= 3 {
		cellularIPv6PreferredUntil.Store(time.Now().Add(cellularIPv6PreferredFor).UnixNano())
	}
}

func dialCellularFamilies(ctx context.Context, iface string, v4, v6 []string) (net.Conn, error) {
	first, second := v4, v6
	ipv6First := time.Now().UnixNano() < cellularIPv6PreferredUntil.Load()
	if ipv6First || len(v4) == 0 {
		first, second = v6, v4
	}
	if len(second) == 0 {
		return dialCellularList(ctx, iface, first)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
		ipv6 bool
	}
	results := make(chan result, 2)
	firstIsIPv6 := ipv6First || len(v4) == 0
	go func() {
		c, err := dialCellularList(ctx, iface, first)
		results <- result{c, err, firstIsIPv6}
	}()
	fallback := time.NewTimer(cellularFamilyFallbackDelay)
	defer fallback.Stop()
	startSecond := func() {
		go func() {
			c, err := dialCellularList(ctx, iface, second)
			results <- result{c, err, !firstIsIPv6}
		}()
	}
	started, pending, ipv4Failed := false, 1, false
	var errs []string
	for pending > 0 {
		select {
		case <-fallback.C:
			if !started {
				started, pending = true, pending+1
				startSecond()
			}
		case r := <-results:
			pending--
			if r.err == nil {
				// While IPv6 leads, its wins must not keep extending the lead;
				// it lapses and IPv4 gets another chance.
				if !r.ipv6 || !firstIsIPv6 {
					noteCellularFamilyWin(r.ipv6, ipv4Failed)
				}
				cancel()
				// The other attempt may still connect; close it when it does.
				for ; pending > 0; pending-- {
					if other := <-results; other.conn != nil {
						other.conn.Close()
					}
				}
				return r.conn, nil
			}
			errs = append(errs, r.err.Error())
			if !r.ipv6 {
				ipv4Failed = true
			}
			if !started {
				started, pending = true, pending+1
				startSecond()
			}
		}
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

func dialCellularList(ctx context.Context, iface string, addresses []string) (net.Conn, error) {
	var err error
	for _, address := range addresses {
		c, e := boundProxyDial(ctx, iface, address)
		if e == nil {
			return c, nil
		}
		err = e
	}
	return nil, err
}
func (a *app) proxyStatusAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.proxy.snapshot())
}
func (a *app) proxyActionAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action     string `json:"action"`
		Confirm    bool   `json:"confirm"`
		AvoidHours int    `json:"avoid_hours"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	// A running reconnect holds the module lock; stopping further attempts must
	// not need it. The current radio cycle always completes first.
	if body.Action == "stop_reconnect" {
		a.proxy.stopRotation.Store(true)
		writeJSON(w, 200, a.proxy.snapshot())
		return
	}
	if !a.tryModuleOperation() {
		writeError(w, 409, errModuleBusy)
		return
	}
	unlock := true
	defer func() {
		if unlock {
			a.moduleOperationMu.Unlock()
		}
	}()
	p := &a.proxy
	switch body.Action {
	case "start":
		policy, err := a.cellularPolicyStatus()
		if err != nil || policy.ForceOff {
			writeError(w, 409, "请先在设置中打开“允许 4G 上网”")
			return
		}
		if blocked, note := a.usage.blocked(time.Now()); blocked {
			writeError(w, 409, note)
			return
		}
		if err := p.start(); err != nil {
			writeError(w, 502, "无法开启代理："+friendlyProxyError(err))
			return
		}
		p.setAutoStart(true)
		// Listener startup is separate from external exit verification.
		p.mu.Lock()
		p.state.PublicIP = ""
		p.state.PublicIPv6 = ""
		p.state.IPSource = ""
		p.state.Location = ""
		p.state.ISP = ""
		p.state.CheckedAt = ""
		p.state.LocationError = ""
		p.mu.Unlock()
		iface := p.snapshot().Interface
		ip, source, err := probeProxyExit(r.Context(), iface, "")
		if err != nil {
			p.mu.Lock()
			log.Printf("cellular proxy: exit check after start failed: %v", err)
			p.state.Error = "代理已开启，但暂时检测不到出口 IP：" + friendlyProxyError(err)
			p.mu.Unlock()
		} else {
			p.recordExit(r.Context(), iface, ip, source)
		}
	case "stop":
		p.setAutoStart(false)
		p.stop()
	case "probe":
		iface, err := proxyInterface()
		if err != nil {
			writeError(w, 502, friendlyProxyError(err))
			return
		}
		ip, source, err := probeProxyExit(r.Context(), iface, "")
		if err != nil {
			log.Printf("cellular proxy: exit probe failed: %v", err)
			writeError(w, 502, "检测出口失败："+friendlyProxyError(err))
			return
		}
		p.recordExit(r.Context(), iface, ip, source)
	case "reconnect", "reconnect_segment":
		mode := ipReconnectMode()
		if body.Action == "reconnect_segment" {
			mode = segmentReconnectMode(body.AvoidHours)
		}
		if !body.Confirm {
			writeError(w, 400, "需要确认：重连会暂时中断代理、短信和来电")
			return
		}
		if status, err := a.launchReconnect(mode); err != "" {
			writeError(w, status, err)
			return
		}
		unlock = false
		writeJSON(w, 202, p.snapshot())
		return
	default:
		writeError(w, 400, "未知的代理操作")
		return
	}
	writeJSON(w, 200, p.snapshot())
}

// launchReconnect starts a reconnect in the background. The caller holds
// moduleOperationMu; on success the reconnect releases it, otherwise the
// caller still owns it and gets the status and message to report.
func (a *app) launchReconnect(mode reconnectMode) (int, string) {
	p := &a.proxy
	if a.demo {
		return 409, "演示模式下不能操作射频"
	}
	a.callMu.RLock()
	busy := a.activeCall != nil
	a.callMu.RUnlock()
	if busy {
		return 409, errCallActive
	}
	p.mu.Lock()
	cooldown := time.Since(p.lastReconnect) < reconnectCooldown
	p.mu.Unlock()
	if cooldown {
		return 429, fmt.Sprintf("两次重连至少间隔 %d 秒，请稍后再试", int(reconnectCooldown.Seconds()))
	}
	iface, err := proxyInterface()
	if err != nil {
		return 502, friendlyProxyError(err)
	}
	p.mu.Lock()
	p.lastReconnect = time.Now()
	p.state.Phase = "reconnecting"
	p.state.ReconnectSeconds = 0
	p.state.ReconnectAttempts = 0
	p.state.ReconnectMode = mode.name
	p.state.ReconnectMax = mode.maxAttempts
	p.state.ReconnectNote = ""
	p.state.ReconnectTried = nil
	p.state.AvoidHours = mode.avoidHours
	p.state.Error = ""
	p.mu.Unlock()
	p.stopRotation.Store(false)
	go func() { defer a.moduleOperationMu.Unlock(); a.reconnectProxy(iface, mode) }()
	return 202, ""
}

func (a *app) reconnectProxy(iface string, mode reconnectMode) {
	a.moduleVoiceOpMu.Lock()
	defer a.moduleVoiceOpMu.Unlock()
	p := &a.proxy
	started := time.Now()
	wasRunning := p.snapshot().Running
	parent := p.lifecycle
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second+time.Duration(mode.maxAttempts)*reconnectAttemptTimeout)
	defer cancel()
	// The light single-request check is enough to remember the old IP; the
	// parallel HTTPS probe costs a second or more on a roaming SIM.
	old, oldSource, oldErr := watchExitIP(ctx, iface, p.snapshot().IPSource)
	p.stop()
	p.mu.Lock()
	if oldErr == nil {
		if p.state.PublicIP != old {
			p.state.Location = ""
			p.state.ISP = ""
		}
		p.state.PublicIP = old
		p.state.IPSource = oldSource
		p.state.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		p.recordHistoryLocked()
	}
	var usedSegments map[string]bool
	if mode.name == "segment" {
		usedSegments = recentSegments(p.state.History, started.Add(-time.Duration(mode.avoidHours)*time.Hour))
		if segment := ipv4Segment(old); segment != "" {
			usedSegments[segment] = true
		}
	}
	p.state.PreviousIP = old
	p.state.PublicIP = ""
	p.state.PublicIPv6 = ""
	p.state.IPSource = ""
	p.state.Location = ""
	p.state.ISP = ""
	p.state.CheckedAt = ""
	p.state.LocationError = ""
	p.state.Error = ""
	p.mu.Unlock()
	finish := func(phase string, attempts int, err error) {
		p.mu.Lock()
		p.state.Phase = phase
		p.state.ReconnectAttempts = attempts
		p.state.ReconnectSeconds = float64(time.Since(started).Milliseconds()) / 1000
		if err != nil {
			log.Printf("cellular reconnect failed: %v", err)
			p.state.Error = "重连未完成：" + friendlyProxyError(err)
		}
		p.mu.Unlock()
		if wasRunning && ctx.Err() == nil {
			if e := p.start(); e != nil {
				p.mu.Lock()
				p.state.Error += "；代理重新开启失败：" + friendlyProxyError(e)
				p.mu.Unlock()
			}
		}
	}
	progress := func(attempts int, note string, tried string) {
		p.mu.Lock()
		p.state.ReconnectAttempts = attempts
		if note != "" {
			p.state.ReconnectNote = note
		}
		if tried != "" {
			p.state.ReconnectTried = append(p.state.ReconnectTried, tried)
		}
		p.mu.Unlock()
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			finish("failed", attempt-1, ctx.Err())
			return
		}
		progress(attempt, fmt.Sprintf("第 %d/%d 次：正在开关射频", attempt, mode.maxAttempts), "")
		current, ip, source, err := a.cycleRadio(ctx, oldSource)
		if err != nil {
			finish("failed", attempt, err)
			return
		}
		progress(attempt, "", ip)
		comparable := oldErr == nil && oldSource == source
		more := attempt < mode.maxAttempts && !p.stopRotation.Load()
		if mode.name == "segment" {
			segment := ipv4Segment(ip)
			if usedSegments[segment] && more {
				progress(attempt, fmt.Sprintf("第 %d/%d 次：%s.x 在最近 %d 小时内用过，继续尝试", attempt, mode.maxAttempts, segment, mode.avoidHours), "")
				log.Printf("cellular reconnect: segment %s.x used recently, cycling radio again", segment)
				continue
			}
			p.recordExit(ctx, current, ip, source)
			phase, note := "new_segment", fmt.Sprintf("已换到新网段 %s.x", segment)
			switch {
			case usedSegments[segment] && p.stopRotation.Load():
				phase, note = "stopped", fmt.Sprintf("已停止后续尝试，保留 %s", ip)
			case usedSegments[segment]:
				phase, note = "segment_reused", fmt.Sprintf("试了 %d 次，运营商只分配了最近 %d 小时内用过的网段", attempt, mode.avoidHours)
			}
			progress(attempt, note, "")
			finish(phase, attempt, nil)
			return
		}
		if comparable && ip == old && more {
			log.Printf("cellular reconnect: carrier returned the same IP, cycling radio once more")
			continue
		}
		p.recordExit(ctx, current, ip, source)
		phase := "changed"
		if !comparable {
			phase = "recovered_unverified"
		} else if ip == old {
			phase = "unchanged"
		}
		finish(phase, attempt, nil)
		return
	}
}

// cycleRadio toggles the radio off and on (AT+CFUN=0/1) and waits until the
// cellular interface serves traffic again, returning the verified exit.
func (a *app) cycleRadio(parent context.Context, ipSource string) (iface, ip, source string, err error) {
	ctx, cancel := context.WithTimeout(parent, reconnectAttemptTimeout)
	defer cancel()
	response, offErr := a.runATCommand("AT+CFUN=0", 5*time.Second)
	if offErr == nil {
		offErr = validateCallATResponse(response)
	}
	if offErr != nil {
		_, _ = a.runATCommand("AT+CFUN=1", 10*time.Second)
		return "", "", "", offErr
	}
	// Always attempt to turn the radio back on, even when later recovery fails.
	select {
	case <-ctx.Done():
	case <-time.After(radioOffDuration):
	}
	response, onErr := a.runATCommand("AT+CFUN=1", 10*time.Second)
	if onErr == nil {
		onErr = validateCallATResponse(response)
	}
	if onErr != nil {
		return "", "", "", onErr
	}
	var last error
	for {
		select {
		case <-ctx.Done():
			return "", "", "", fmt.Errorf("recovery timed out: %v", last)
		case <-time.After(reconnectPollInterval):
		}
		current, err := proxyInterface()
		if err != nil {
			last = err
			continue
		}
		// Each check is bounded so one stalled request while the data path is
		// still coming up does not delay noticing that it works.
		attempt, cancelAttempt := context.WithTimeout(ctx, 3*time.Second)
		ip, source, err := watchExitIP(attempt, current, ipSource)
		cancelAttempt()
		if err != nil {
			last = err
			continue
		}
		return current, ip, source, nil
	}
}

func (p *cellularProxy) monitor(ctx context.Context, iface string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := proxyInterface()
			if err != nil || current != iface {
				p.mu.Lock()
				if ctx.Err() == nil {
					p.state.Error = "蜂窝网络中断，代理已暂停（不会改走宽带）"
					p.state.PublicIP = ""
					p.state.PublicIPv6 = ""
					p.state.IPSource = ""
					p.stopLocked()
				}
				p.mu.Unlock()
				return
			}
		}
	}
}

// Geolocation is approximate carrier egress metadata, never device GPS.
func (p *cellularProxy) recordExit(ctx context.Context, iface, ip, source string) {
	p.mu.Lock()
	p.state.Error = ""
	p.state.PublicIP = ip
	p.state.IPSource = source
	p.state.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	if net.ParseIP(ip).To4() == nil {
		p.state.PublicIPv6 = ip
	} else {
		p.state.PublicIPv6 = ""
	}
	p.state.Location = ""
	p.state.ISP = ""
	p.state.LocationError = ""
	p.mu.Unlock()
	// The IP is final now; the slower (foreign) geolocation lookup fills in
	// afterwards so callers and the App are not held up by it.
	go p.refreshExitIPv6(context.WithoutCancel(ctx), iface, ip)
	go p.fillFraudScores(context.WithoutCancel(ctx))
	go func() {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		location, isp, err := probeProxyLocation(lookupCtx, iface, ip)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.state.PublicIP != ip {
			return
		}
		if err != nil {
			p.state.LocationError = err.Error()
			p.recordHistoryLocked()
			return
		}
		p.state.Location = location
		p.state.ISP = isp
		p.recordHistoryLocked()
	}()
}
func probeProxyLocation(ctx context.Context, iface, ip string) (string, string, error) {
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return cellularDial(ctx, iface, addr) }, DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://ipwho.is/"+ip, nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("location lookup HTTP %d", resp.StatusCode)
	}
	return decodeProxyLocation(resp.Body, ip)
}

func decodeProxyLocation(body io.Reader, ip string) (string, string, error) {
	var result struct {
		IP         string `json:"ip"`
		Success    bool   `json:"success"`
		Country    string `json:"country"`
		Region     string `json:"region"`
		City       string `json:"city"`
		Connection struct {
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 16*1024)).Decode(&result); err != nil {
		return "", "", err
	}
	if !result.Success || result.IP != ip {
		return "", "", errors.New("location unavailable or IP mismatch")
	}
	parts := []string{}
	for _, part := range []string{result.Country, result.Region, result.City} {
		if part != "" && (len(parts) == 0 || parts[len(parts)-1] != part) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · "), result.Connection.ISP, nil
}

package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const proxyAddress = "127.0.0.1:10909"

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
	PreviousIP    string         `json:"previous_ip"`
	Phase         string         `json:"phase"`
	Error         string         `json:"error"`
	Connections   int            `json:"connections"`
	Uploaded      uint64         `json:"uploaded"`
	Downloaded    uint64         `json:"downloaded"`
}
type cellularProxy struct {
	lifecycle     context.Context
	historyLoaded bool
	historyPath   string
	mu            sync.Mutex
	listener      net.Listener
	clients       map[net.Conn]bool
	state         proxyState
	cancel        context.CancelFunc
	lastReconnect time.Time
	uploaded      atomic.Uint64
	downloaded    atomic.Uint64
}

func (p *cellularProxy) snapshot() proxyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadHistoryLocked()
	s := p.state
	s.History = append([]proxyIPVisit{}, p.state.History...)
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

// DNS uses the selected module's DHCP resolver, with every socket interface-bound.
func cellularDial(ctx context.Context, iface, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		server, err := cellularDNSServer(ctx, iface)
		if err != nil {
			return nil, err
		}
		resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			conn, err := boundProxyDial(attempt, iface, server)
			if err == nil {
				return conn, nil
			}
			// Some module firmware does not serve TCP DNS. The alternative
			// resolver is still strictly bound to cellular, never broadband.
			return boundProxyDial(ctx, iface, "223.5.5.5:53")
		}}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err = resolver.LookupIP(lookupCtx, "ip4", host)
		cancel()
		if err != nil {
			fallback := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return boundProxyDial(ctx, iface, "223.5.5.5:53")
			}}
			ips, err = fallback.LookupIP(ctx, "ip4", host)
		}
		if err != nil {
			return nil, err
		}
	}
	for _, ip := range ips {
		if ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		c, e := boundProxyDial(ctx, iface, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
		err = e
	}
	if err == nil {
		err = errors.New("only public IPv4 destinations are supported")
	}
	return nil, err
}
func (a *app) proxyStatusAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.proxy.snapshot())
}
func (a *app) proxyActionAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !a.tryModuleOperation() {
		writeError(w, 409, "module operation in progress")
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
			writeError(w, 409, "enable cellular data in settings first")
			return
		}
		if err := p.start(); err != nil {
			writeError(w, 502, err.Error())
			return
		}
		// Listener startup is separate from external exit verification.
		p.mu.Lock()
		p.state.PublicIP = ""
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
			p.state.Error = "Proxy listening, exit check failed: " + err.Error()
			p.mu.Unlock()
		} else {
			p.recordExit(r.Context(), iface, ip, source)
		}
	case "stop":
		p.stop()
	case "probe":
		iface, err := proxyInterface()
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		ip, source, err := probeProxyExit(r.Context(), iface, "")
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		p.recordExit(r.Context(), iface, ip, source)
	case "reconnect":
		if !body.Confirm {
			writeError(w, 400, "confirmation required: disconnects proxy, SMS and calls temporarily")
			return
		}
		if a.demo {
			writeError(w, 409, "radio operations disabled in demo")
			return
		}
		a.callMu.RLock()
		busy := a.activeCall != nil
		a.callMu.RUnlock()
		if busy {
			writeError(w, 409, "call in progress")
			return
		}
		p.mu.Lock()
		cooldown := time.Since(p.lastReconnect) < 60*time.Second
		p.mu.Unlock()
		if cooldown {
			writeError(w, 429, "wait 60 seconds between reconnects")
			return
		}
		iface, err := proxyInterface()
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		p.mu.Lock()
		p.lastReconnect = time.Now()
		p.state.Phase = "reconnecting"
		p.state.Error = ""
		p.mu.Unlock()
		unlock = false
		go func() { defer a.moduleOperationMu.Unlock(); a.reconnectProxy(iface) }()
		writeJSON(w, 202, p.snapshot())
		return
	default:
		writeError(w, 400, "unknown proxy action")
		return
	}
	writeJSON(w, 200, p.snapshot())
}
func (a *app) reconnectProxy(iface string) {
	a.moduleVoiceOpMu.Lock()
	defer a.moduleVoiceOpMu.Unlock()
	p := &a.proxy
	wasRunning := p.snapshot().Running
	parent := p.lifecycle
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 100*time.Second)
	defer cancel()
	old, oldSource, oldErr := probeProxyExit(ctx, iface, p.snapshot().IPSource)
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
	p.state.PreviousIP = old
	p.state.PublicIP = ""
	p.state.IPSource = ""
	p.state.Location = ""
	p.state.ISP = ""
	p.state.CheckedAt = ""
	p.state.LocationError = ""
	p.state.Error = ""
	p.mu.Unlock()
	finish := func(phase string, err error) {
		p.mu.Lock()
		p.state.Phase = phase
		if err != nil {
			p.state.Error = err.Error()
		}
		p.mu.Unlock()
		if wasRunning && ctx.Err() == nil {
			if e := p.start(); e != nil {
				p.mu.Lock()
				p.state.Error += "; proxy restart: " + e.Error()
				p.mu.Unlock()
			}
		}
	}
	if ctx.Err() != nil {
		finish("failed", ctx.Err())
		return
	}
	response, offErr := a.runATCommand("AT+CFUN=0", 5*time.Second)
	if offErr == nil {
		offErr = validateCallATResponse(response)
	}
	if offErr != nil {
		_, _ = a.runATCommand("AT+CFUN=1", 10*time.Second)
		finish("failed", offErr)
		return
	}
	// Always attempt to turn the radio back on, even when later recovery fails.
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
	}
	response, onErr := a.runATCommand("AT+CFUN=1", 10*time.Second)
	if onErr == nil {
		onErr = validateCallATResponse(response)
	}
	if err := onErr; err != nil {
		finish("failed", err)
		return
	}
	var last error
	for ctx.Err() == nil {
		time.Sleep(3 * time.Second)
		current, err := proxyInterface()
		if err != nil {
			last = err
			continue
		}
		ip, source, err := probeProxyExit(ctx, current, oldSource)
		if err != nil {
			last = err
			continue
		}
		p.recordExit(ctx, current, ip, source)
		phase := "changed"
		if oldErr != nil || oldSource != source {
			phase = "recovered_unverified"
		} else if ip == old {
			phase = "unchanged"
		}
		finish(phase, nil)
		return
	}
	finish("failed", fmt.Errorf("recovery timed out: %v", last))
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
					p.state.Error = "Cellular interface or default route changed; proxy stopped (no broadband fallback)"
					p.state.PublicIP = ""
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
	p.state.Location = ""
	p.state.ISP = ""
	p.state.LocationError = ""
	p.mu.Unlock()
	location, isp, err := probeProxyLocation(ctx, iface, ip)
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

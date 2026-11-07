package main

import (
	"net"
	"net/http"
	"strconv"
	"time"
)

// A switch link answers within rotateWaitDefault: fingerprint browsers give
// the refresh request about 15 s (CloakBrowser Manager exactly 15) and then
// confirm the new exit through the proxy themselves. ?wait=N (seconds) asks
// to wait longer, up to rotateWaitMax, which stays inside the multi-module
// worker request timeout (27 s). A slower reconnect is reported as running.
const (
	rotateWaitDefault = 12 * time.Second
	rotateWaitMax     = 24 * time.Second
)

func rotateWait(r *http.Request) time.Duration {
	n, err := strconv.Atoi(r.URL.Query().Get("wait"))
	if err != nil {
		return rotateWaitDefault
	}
	return min(max(time.Duration(n)*time.Second, 0), rotateWaitMax)
}

// proxyLink is what a fingerprint browser needs for one module: the SOCKS5
// address and the link that switches its exit IP.
type proxyLink struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Socks     string `json:"socks"`
	RotateURL string `json:"rotate_url"`
	PublicIP  string `json:"public_ip"`
	// LAN forms, set while LAN sharing is on.
	LANSocks     string `json:"lan_socks,omitempty"`
	LANRotateURL string `json:"lan_rotate_url,omitempty"`
}

type rotateResult struct {
	OK         bool    `json:"ok"`
	Pending    bool    `json:"pending,omitempty"`
	Phase      string  `json:"phase,omitempty"`
	PreviousIP string  `json:"previous_ip,omitempty"`
	IP         string  `json:"ip,omitempty"`
	Seconds    float64 `json:"seconds,omitempty"`
	Message    string  `json:"message,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// apiBase is the address the request came in on, so copied links match the
// port the backend actually listens on.
func apiBase(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err != nil || h == "" {
		host = "127.0.0.1:7575"
	}
	return "http://" + host
}

func (a *app) proxyLinksAPI(w http.ResponseWriter, r *http.Request) {
	s := a.proxy.snapshot()
	link := proxyLink{
		Name:      "4G 模块",
		Connected: s.Interface != "" || s.Running,
		Socks:     proxyListenAddress(),
		RotateURL: apiBase(r) + "/api/proxy/rotate",
		PublicIP:  s.PublicIP,
	}
	link.LANSocks, link.LANRotateURL = lanLinks(link.Socks, "/api/proxy/rotate")
	writeJSON(w, 200, []proxyLink{link})
}

// proxyRotateAPI is the one-click switch link. GET or POST without a body
// starts the same reconnect as the app's button, waits for the new exit and
// answers with it, so a bookmark or a fingerprint browser's refresh link can
// change the IP without opening the app.
func (a *app) proxyRotateAPI(w http.ResponseWriter, r *http.Request) {
	if !a.tryModuleOperation() {
		writeJSON(w, 409, rotateResult{Error: errModuleBusy})
		return
	}
	before := a.proxy.snapshot().PublicIP
	mode := ipReconnectMode()
	if r.URL.Query().Get("clean") == "1" {
		mode = cleanReconnectMode()
	}
	if status, msg := a.launchReconnect(mode); msg != "" {
		a.moduleOperationMu.Unlock()
		writeJSON(w, status, rotateResult{PreviousIP: before, Error: msg})
		return
	}
	deadline := time.NewTimer(rotateWait(r))
	defer deadline.Stop()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for a.proxy.snapshot().Phase == "reconnecting" {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			writeJSON(w, 202, rotateResult{OK: true, Pending: true, Phase: "reconnecting", PreviousIP: before, Message: "已开始切换，新 IP 稍后出现；加 ?wait=24 可等更久"})
			return
		case <-tick.C:
		}
	}
	s := a.proxy.snapshot()
	result := rotateResult{Phase: s.Phase, PreviousIP: before, IP: s.PublicIP, Seconds: s.ReconnectSeconds, Error: s.Error}
	result.OK = s.Phase != "failed" && s.Error == "" && s.PublicIP != ""
	status := 200
	if !result.OK {
		status = 502
	}
	writeJSON(w, status, result)
}

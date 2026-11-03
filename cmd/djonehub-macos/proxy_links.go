package main

import (
	"net"
	"net/http"
	"time"
)

// rotateWaitLimit keeps a switch link answer inside the multi-module worker
// request timeout (27 s); a slower reconnect is reported as still running.
const rotateWaitLimit = 24 * time.Second

// proxyLink is what a fingerprint browser needs for one module: the SOCKS5
// address and the link that switches its exit IP.
type proxyLink struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Socks     string `json:"socks"`
	RotateURL string `json:"rotate_url"`
	PublicIP  string `json:"public_ip"`
}

type rotateResult struct {
	OK         bool    `json:"ok"`
	Pending    bool    `json:"pending,omitempty"`
	Phase      string  `json:"phase,omitempty"`
	PreviousIP string  `json:"previous_ip,omitempty"`
	IP         string  `json:"ip,omitempty"`
	Seconds    float64 `json:"seconds,omitempty"`
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
	writeJSON(w, 200, []proxyLink{{
		Name:      "4G 模块",
		Connected: s.Interface != "" || s.Running,
		Socks:     proxyListenAddress(),
		RotateURL: apiBase(r) + "/api/proxy/rotate",
		PublicIP:  s.PublicIP,
	}})
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
	if status, msg := a.launchReconnect(ipReconnectMode()); msg != "" {
		a.moduleOperationMu.Unlock()
		writeJSON(w, status, rotateResult{PreviousIP: before, Error: msg})
		return
	}
	deadline := time.NewTimer(rotateWaitLimit)
	defer deadline.Stop()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for a.proxy.snapshot().Phase == "reconnecting" {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			writeJSON(w, 202, rotateResult{Pending: true, Phase: "reconnecting", PreviousIP: before, Error: "仍在切换，稍后在 App 里查看结果"})
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

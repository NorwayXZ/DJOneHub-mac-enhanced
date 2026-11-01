package main

import (
	"context"
	"log"
	"time"
)

// While the proxy runs, the exit IP is re-checked in the background so the
// history shows how long each IP was really in use (last_seen otherwise only
// moves on manual probes) and a silent carrier-side IP change is noticed.
const proxyExitWatchInterval = 5 * time.Minute

func (a *app) watchProxyExitOnce(ctx context.Context) {
	p := &a.proxy
	before := p.snapshot()
	if !before.Running || before.PublicIP == "" || before.Phase == "reconnecting" {
		return
	}
	// Skip while a reconnect, band test or other AT operation is underway.
	if !a.tryModuleOperation() {
		return
	}
	a.moduleOperationMu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ip, source, err := probeProxyExit(probeCtx, before.Interface, before.IPSource)
	if err != nil {
		return
	}
	p.mu.Lock()
	// Something else (a probe, reconnect or restart) changed the state meanwhile.
	if p.listener == nil || p.state.PublicIP != before.PublicIP || p.state.CheckedAt != before.CheckedAt || p.state.Phase == "reconnecting" {
		p.mu.Unlock()
		return
	}
	if ip == before.PublicIP {
		p.state.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		p.recordHistoryLocked()
		p.mu.Unlock()
		return
	}
	p.state.PreviousIP = before.PublicIP
	p.mu.Unlock()
	log.Printf("cellular exit IP changed without a reconnect: %s -> %s", before.PublicIP, ip)
	p.recordExit(ctx, before.Interface, ip, source)
}

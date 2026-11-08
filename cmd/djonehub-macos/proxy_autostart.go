package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// The proxy remembers whether the user wants it on. Losing the cellular
// interface (module unplug, SIM drop, reboot) or restarting the backend for an
// update stops the listener but keeps that wish, and the auto-start loop brings
// it back once the module serves traffic again. Only the explicit stop action
// clears it.

const proxyAutoStartInterval = time.Second

type proxyAutoStartFile struct {
	Enabled bool `json:"enabled"`
}

func (p *cellularProxy) autoStartPathLocked() string {
	p.loadHistoryLocked()
	if p.historyPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p.historyPath), "proxy-autostart.json")
}

func (p *cellularProxy) loadAutoStartLocked() {
	if p.autoStartLoaded {
		return
	}
	p.autoStartLoaded = true
	path := p.autoStartPathLocked()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f proxyAutoStartFile
	if json.Unmarshal(b, &f) == nil {
		p.state.AutoStart = f.Enabled
	}
}

func (p *cellularProxy) setAutoStart(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadAutoStartLocked()
	p.state.AutoStart = enabled
	path := p.autoStartPathLocked()
	if path == "" {
		return
	}
	b, _ := json.Marshal(proxyAutoStartFile{Enabled: enabled})
	err := os.MkdirAll(filepath.Dir(path), 0700)
	if err == nil {
		err = os.WriteFile(path, b, 0600)
	}
	if err != nil {
		log.Printf("cellular proxy auto-start preference not saved: %v", err)
	}
}

func (p *cellularProxy) wantsAutoStart() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadAutoStartLocked()
	return p.state.AutoStart && p.listener == nil
}

func (a *app) runProxyAutoStart(ctx context.Context) {
	ticker := time.NewTicker(proxyAutoStartInterval)
	defer ticker.Stop()
	watch := time.NewTicker(proxyExitWatchInterval)
	defer watch.Stop()
	var lastRetry time.Time
	go a.proxy.fillFraudScores(ctx)
	defer setKeepAwake(false)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.autoStartProxyOnce(ctx)
			setKeepAwake(!a.demo && a.proxy.wantsAwake())
			// While the exit fails, re-check often so a stuck data session is
			// reconnected within a minute instead of after two 5-minute checks.
			if s := a.proxy.snapshot(); s.Running && ((s.PublicIP == "" && time.Since(lastRetry) >= 15*time.Second) || (dataStalled() && time.Since(lastRetry) >= dataStallRecheck)) {
				lastRetry = time.Now()
				a.watchProxyExitOnce(ctx)
			}
		case <-watch.C:
			a.watchProxyExitOnce(ctx)
			go a.proxy.fillFraudScores(ctx)
		}
	}
}

func (a *app) autoStartProxyOnce(ctx context.Context) {
	p := &a.proxy
	if !p.wantsAutoStart() || a.updatePending.Load() {
		return
	}
	if blocked, _ := a.usage.blocked(time.Now()); blocked {
		return
	}
	if _, err := proxyInterface(); err != nil {
		return
	}
	if policy, err := a.cellularPolicyStatus(); err != nil || policy.ForceOff {
		return
	}
	// Reconnects and AT operations hold this lock; never race them.
	if !a.tryModuleOperation() {
		return
	}
	if !p.wantsAutoStart() {
		a.moduleOperationMu.Unlock()
		return
	}
	err := p.start()
	a.moduleOperationMu.Unlock()
	if err != nil {
		p.mu.Lock()
		p.state.Error = "等待蜂窝网络恢复后自动开启：" + friendlyProxyError(err)
		p.mu.Unlock()
		return
	}
	log.Printf("cellular proxy restarted automatically on %s", p.snapshot().Interface)
	p.mu.Lock()
	p.state.PublicIP = ""
	p.state.PublicIPv6 = ""
	p.state.IPSource = ""
	p.state.Location = ""
	p.state.ISP = ""
	p.state.CheckedAt = ""
	p.state.LocationError = ""
	p.mu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	iface := p.snapshot().Interface
	if ip, source, err := probeProxyExit(probeCtx, iface, ""); err != nil {
		p.mu.Lock()
		p.state.Error = "代理已开启，但暂时检测不到出口 IP：" + friendlyProxyError(err)
		p.mu.Unlock()
	} else {
		p.recordExit(probeCtx, iface, ip, source)
	}
}

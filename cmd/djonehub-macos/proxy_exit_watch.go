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

// The watch runs all day, so it asks one plain-HTTP echo service (about 1 KB
// a check) instead of the parallel HTTPS probe (about 100 KB). The service
// that found the current IP goes first: behind carrier NAT another one may
// see a different address.
var proxyExitWatchProviders = []proxyExitProvider{
	{"http://ip.3322.net", "ip.3322.net", ""},
	{"http://ddns.oray.com/checkip", "ddns.oray.com", "Current IP Address:"},
	{"http://myip.ipip.net", "myip.ipip.net", "当前 IP："},
	{"http://ifconfig.me/ip", "ifconfig.me", ""},
}

func watchExitIP(ctx context.Context, iface, preferred string) (string, string, error) {
	providers := make([]proxyExitProvider, 0, len(proxyExitWatchProviders))
	for _, provider := range proxyExitWatchProviders {
		if provider.source == preferred {
			providers = append([]proxyExitProvider{provider}, providers...)
		} else {
			providers = append(providers, provider)
		}
	}
	for _, provider := range providers {
		if ip, err := probeProxyExitProvider(ctx, iface, provider); err == nil {
			return ip, provider.source, nil
		}
	}
	return probeProxyExit(ctx, iface, preferred)
}

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
	ip, source, err := watchExitIP(probeCtx, before.Interface, before.IPSource)
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
		// The Mac rotates its temporary IPv6 address; keep the shown one
		// current, asking a remote service only when the local one changed.
		if localCellularIPv6(before.Interface) != before.PublicIPv6 {
			p.refreshExitIPv6(ctx, before.Interface, ip)
		}
		return
	}
	p.state.PreviousIP = before.PublicIP
	p.mu.Unlock()
	log.Printf("cellular exit IP changed without a reconnect: %s -> %s", before.PublicIP, ip)
	p.recordExit(ctx, before.Interface, ip, source)
}

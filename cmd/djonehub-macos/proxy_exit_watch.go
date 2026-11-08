package main

import (
	"context"
	"log"
	"sync"
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

// exitProviderTimeout bounds one echo service, so a site the carrier's exit
// cannot reach (ip.3322.net from a Philippine exit, say) costs seconds instead
// of the whole check, which would then look like a dead data path.
const (
	exitProviderTimeout = 5 * time.Second
	exitProviderPenalty = 10 * time.Minute
)

// exitProviderFailed: when each service last failed here; it goes last for
// a while.
var exitProviderFailed sync.Map

func exitProviderOrder(preferred string, now time.Time) []proxyExitProvider {
	var good, bad []proxyExitProvider
	for _, provider := range proxyExitWatchProviders {
		if at, ok := exitProviderFailed.Load(provider.source); ok && now.Sub(at.(time.Time)) < exitProviderPenalty {
			bad = append(bad, provider)
		} else if provider.source == preferred {
			good = append([]proxyExitProvider{provider}, good...)
		} else {
			good = append(good, provider)
		}
	}
	return append(good, bad...)
}

func watchExitIP(ctx context.Context, iface, preferred string) (string, string, error) {
	for _, provider := range exitProviderOrder(preferred, time.Now()) {
		pctx, cancel := context.WithTimeout(ctx, exitProviderTimeout)
		ip, err := probeProxyExitProvider(pctx, iface, provider)
		cancel()
		if err == nil {
			exitProviderFailed.Delete(provider.source)
			return ip, provider.source, nil
		}
		if ctx.Err() != nil {
			// The caller's own deadline, not this service, ran out.
			break
		}
		exitProviderFailed.Store(provider.source, time.Now())
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	return probeProxyExit(ctx, iface, preferred)
}

func (a *app) watchProxyExitOnce(ctx context.Context) {
	p := &a.proxy
	before := p.snapshot()
	// An empty IP means the probe after a (re)start failed while the network
	// was still coming back; keep trying so the App does not stay at
	// "not detected" until the user probes by hand.
	if !before.Running || before.Phase == "reconnecting" {
		return
	}
	// Skip while a reconnect, band test or other AT operation is underway.
	if !a.tryModuleOperation() {
		return
	}
	a.moduleOperationMu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ip, source, err := watchExitIP(probeCtx, before.Interface, before.IPSource)
	a.noteExitCheck(err == nil)
	if err != nil {
		return
	}
	p.mu.Lock()
	// Something else (a probe, reconnect or restart) changed the state meanwhile.
	if p.listener == nil || p.state.PublicIP != before.PublicIP || p.state.CheckedAt != before.CheckedAt || p.state.Phase == "reconnecting" {
		p.mu.Unlock()
		return
	}
	if before.PublicIP == "" {
		p.mu.Unlock()
		p.recordExit(ctx, before.Interface, ip, source)
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

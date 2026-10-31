package main

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func parseCellularDNSServer(output string) (string, error) {
	for _, field := range strings.Fields(output) {
		ip := net.ParseIP(strings.Trim(field, "{},"))
		if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return net.JoinHostPort(ip.String(), "53"), nil
		}
	}
	return "", errors.New("模块尚未提供可用的 IPv4 DNS，请检查 USB DHCP")
}
func cellularDNSServer(ctx context.Context, iface string) (string, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/ipconfig", "getoption", iface, "domain_name_server").Output()
	if err != nil {
		return "", errors.New("未能读取模块 DHCP DNS；不会回退到宽带 DNS")
	}
	return parseCellularDNSServer(string(out))
}

// Public resolvers raced alongside the module's DHCP resolver. The module's
// resolver occasionally stalls for seconds; asking all at once (every socket
// still bound to the cellular interface) removes that stall from each new
// hostname instead of waiting for a timeout before falling back.
var cellularPublicDNS = []string{"223.5.5.5:53", "119.29.29.29:53"}

const (
	cellularDNSTimeout    = 5 * time.Second
	cellularDNSCacheTTL   = time.Minute
	cellularDNSCacheLimit = 1024
)

type cellularDNSEntry struct {
	ips     []net.IP
	expires time.Time
}

var cellularDNSCache = struct {
	sync.Mutex
	entries map[string]cellularDNSEntry
}{entries: map[string]cellularDNSEntry{}}

func cachedCellularIPv4(key string, now time.Time) []net.IP {
	cellularDNSCache.Lock()
	defer cellularDNSCache.Unlock()
	if entry, ok := cellularDNSCache.entries[key]; ok && now.Before(entry.expires) {
		return entry.ips
	}
	return nil
}

func storeCellularIPv4(key string, ips []net.IP, now time.Time) {
	cellularDNSCache.Lock()
	defer cellularDNSCache.Unlock()
	if len(cellularDNSCache.entries) >= cellularDNSCacheLimit {
		for k, entry := range cellularDNSCache.entries {
			if !now.Before(entry.expires) {
				delete(cellularDNSCache.entries, k)
			}
		}
		if len(cellularDNSCache.entries) >= cellularDNSCacheLimit {
			cellularDNSCache.entries = map[string]cellularDNSEntry{}
		}
	}
	cellularDNSCache.entries[key] = cellularDNSEntry{ips: ips, expires: now.Add(cellularDNSCacheTTL)}
}

func resolveCellularIPv4(ctx context.Context, iface, host string) ([]net.IP, error) {
	key := iface + "|" + strings.ToLower(host)
	if ips := cachedCellularIPv4(key, time.Now()); ips != nil {
		return ips, nil
	}
	servers := append([]string(nil), cellularPublicDNS...)
	if server, err := cellularDNSServer(ctx, iface); err == nil {
		servers = append([]string{server}, servers...)
	}
	ctx, cancel := context.WithTimeout(ctx, cellularDNSTimeout)
	defer cancel()
	type answer struct {
		ips []net.IP
		err error
	}
	answers := make(chan answer, len(servers))
	for _, server := range servers {
		go func(server string) {
			resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				proto := "udp4"
				if strings.HasPrefix(network, "tcp") {
					proto = "tcp4"
				}
				return boundInterfaceDial(ctx, iface, proto, server)
			}}
			ips, err := resolver.LookupIP(ctx, "ip4", host)
			answers <- answer{ips, err}
		}(server)
	}
	failures := []string{}
	for range servers {
		a := <-answers
		if a.err == nil && len(a.ips) > 0 {
			storeCellularIPv4(key, a.ips, time.Now())
			return a.ips, nil
		}
		if a.err != nil {
			failures = append(failures, a.err.Error())
		}
	}
	if len(failures) == 0 {
		return nil, errors.New("no IPv4 address for " + host)
	}
	return nil, errors.New("cellular DNS failed: " + strings.Join(failures, "; "))
}

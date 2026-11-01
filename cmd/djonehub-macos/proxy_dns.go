package main

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
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

// Carrier IPv6 resolvers from the module's DHCPv6 reply. On an IPv6-only
// bearer they answer with DNS64-synthesised addresses for IPv4-only sites.
func parseCellularDNSv6Servers(output string) []string {
	servers := []string{}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "DNS_SERVERS") {
			continue
		}
		if i := strings.Index(line, ":"); i >= 0 {
			line = line[i+1:]
		}
		for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			ip := net.ParseIP(field)
			if ip != nil && ip.To4() == nil && ip.IsGlobalUnicast() {
				servers = append(servers, net.JoinHostPort(ip.String(), "53"))
			}
		}
	}
	return servers
}
func cellularDNSv6Servers(ctx context.Context, iface string) []string {
	out, err := exec.CommandContext(ctx, "/usr/sbin/ipconfig", "getv6packet", iface).Output()
	if err != nil {
		return nil
	}
	return parseCellularDNSv6Servers(string(out))
}

// Public resolvers raced alongside the module's DHCP resolver. The module's
// resolver occasionally stalls for seconds; asking all at once (every socket
// still bound to the cellular interface) removes that stall from each new
// hostname instead of waiting for a timeout before falling back.
//
// They are Chinese resolvers, so they are only used with a mainland SIM. A
// foreign (roaming) SIM exits through its home carrier, and a site's DNS leak
// test would otherwise pair that foreign IP with AliDNS or DNSPod; it uses the
// carrier's own resolvers only.
var cellularPublicDNS = []string{"223.5.5.5:53", "119.29.29.29:53"}

const (
	simHomeUnknown int32 = iota
	simHomeMainland
	simHomeForeign
)

var cellularSIMHome atomic.Int32

// noteSIMIMSI records whether the SIM is a mainland one (MCC 460). Until an
// IMSI has been read the carrier-only policy applies.
func noteSIMIMSI(imsi string) {
	if len(imsi) < 3 {
		return
	}
	home := simHomeForeign
	if strings.HasPrefix(imsi, "460") {
		home = simHomeMainland
	}
	if cellularSIMHome.Swap(home) != home {
		clearCellularDNSCache()
	}
}

func cellularDNSServers(ctx context.Context, iface string) []string {
	servers := []string{}
	if server, err := cellularDNSServer(ctx, iface); err == nil {
		servers = append(servers, server)
	}
	if cellularHasIPv6(iface) {
		servers = append(servers, cellularDNSv6Servers(ctx, iface)...)
	}
	if cellularSIMHome.Load() == simHomeMainland {
		servers = append(servers, cellularPublicDNS...)
	}
	return servers
}

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

func clearCellularDNSCache() {
	cellularDNSCache.Lock()
	cellularDNSCache.entries = map[string]cellularDNSEntry{}
	cellularDNSCache.Unlock()
}

func cachedCellularIPs(key string, now time.Time) []net.IP {
	cellularDNSCache.Lock()
	defer cellularDNSCache.Unlock()
	if entry, ok := cellularDNSCache.entries[key]; ok && now.Before(entry.expires) {
		return entry.ips
	}
	return nil
}

func storeCellularIPs(key string, ips []net.IP, now time.Time) {
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

// resolveCellularIPs returns A and, when the module provides IPv6, AAAA
// records, IPv4 first. Every query leaves through the cellular interface.
func resolveCellularIPs(ctx context.Context, iface, host string) ([]net.IP, error) {
	withIPv6 := cellularHasIPv6(iface)
	network := "ip4"
	if withIPv6 {
		network = "ip"
	}
	key := iface + "|" + network + "|" + strings.ToLower(host)
	if ips := cachedCellularIPs(key, time.Now()); ips != nil {
		return ips, nil
	}
	servers := cellularDNSServers(ctx, iface)
	if len(servers) == 0 {
		return nil, errors.New("模块尚未提供运营商 DNS，请稍后重试；不会改用宽带或国内公共 DNS")
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
			family := "4"
			if host, _, _ := net.SplitHostPort(server); net.ParseIP(host).To4() == nil {
				family = "6"
			}
			resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				proto := "udp" + family
				if strings.HasPrefix(network, "tcp") {
					proto = "tcp" + family
				}
				return boundInterfaceDial(ctx, iface, proto, server)
			}}
			ips, err := resolver.LookupIP(ctx, network, host)
			answers <- answer{sortIPv4First(ips), err}
		}(server)
	}
	failures := []string{}
	for range servers {
		a := <-answers
		if a.err == nil && len(a.ips) > 0 {
			storeCellularIPs(key, a.ips, time.Now())
			return a.ips, nil
		}
		if a.err != nil {
			failures = append(failures, a.err.Error())
		}
	}
	if len(failures) == 0 {
		return nil, errors.New("no address for " + host)
	}
	return nil, errors.New("cellular DNS failed: " + strings.Join(failures, "; "))
}

func sortIPv4First(ips []net.IP) []net.IP {
	sorted := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip.To4() != nil {
			sorted = append(sorted, ip)
		}
	}
	for _, ip := range ips {
		if ip.To4() == nil {
			sorted = append(sorted, ip)
		}
	}
	return sorted
}

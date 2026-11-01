package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestCellularDNSUsesDHCPResolverOnly(t *testing.T) {
	for _, tt := range []struct {
		input, want string
		fail        bool
	}{
		{"192.168.225.1\n", "192.168.225.1:53", false},
		{"8.8.8.8 8.8.4.4", "8.8.8.8:53", false},
		{"127.0.0.1", "", true}, {"169.254.1.1", "", true}, {"::1", "", true}, {"", "", true},
	} {
		got, err := parseCellularDNSServer(tt.input)
		if got != tt.want || (err != nil) != tt.fail {
			t.Fatalf("DNS %q: %q %v", tt.input, got, err)
		}
	}
}
func TestExitProviderRepliesAreValidated(t *testing.T) {
	for _, tt := range []struct {
		body       string
		ipip, fail bool
	}{
		{"8.8.8.8", false, false},
		{"当前 IP：8.8.8.8 来自于：测试网络", true, false},
		{"10.0.0.1", false, true}, {"127.0.0.1", false, true},
		{"<html>8.8.8.8</html>", false, true}, {"invalid", true, true},
	} {
		_, err := parseProxyExit(tt.body, tt.ipip)
		if (err != nil) != tt.fail {
			t.Fatalf("exit %q: %v", tt.body, err)
		}
	}
	if ip, err := parseProxyExitReply("Current IP Address: 8.8.8.8", "Current IP Address:"); err != nil || ip != "8.8.8.8" {
		t.Fatalf("oray reply: %q %v", ip, err)
	}
	if _, err := parseProxyExitReply("Current IP Address: 192.168.1.2", "Current IP Address:"); err == nil {
		t.Fatal("private oray reply accepted")
	}
}

func TestCellularDNSCacheExpires(t *testing.T) {
	now := time.Now()
	key := "en99|cache.example"
	storeCellularIPs(key, []net.IP{net.ParseIP("8.8.8.8")}, now)
	if ips := cachedCellularIPs(key, now.Add(cellularDNSCacheTTL/2)); len(ips) != 1 {
		t.Fatal("fresh entry missing")
	}
	if ips := cachedCellularIPs(key, now.Add(cellularDNSCacheTTL+time.Second)); ips != nil {
		t.Fatal("expired entry returned")
	}
}

func TestCellularDNSv6ServersFromDHCPv6Reply(t *testing.T) {
	reply := "Options[3] = {\n  DNS_SERVERS (23) Length 32: 2409:8057:2000::8, 2409:8057:2000:4::8\n  SERVERID (2) Length 10: DUID LL\n}"
	got := parseCellularDNSv6Servers(reply)
	if len(got) != 2 || got[0] != "[2409:8057:2000::8]:53" || got[1] != "[2409:8057:2000:4::8]:53" {
		t.Fatalf("v6 DNS: %v", got)
	}
	if got := parseCellularDNSv6Servers("CLIENTID (1) Length 14: DUID LLT"); len(got) != 0 {
		t.Fatalf("unexpected servers: %v", got)
	}
}

func TestForeignSIMSkipsChinesePublicDNS(t *testing.T) {
	defer cellularSIMHome.Store(simHomeUnknown)
	for _, tt := range []struct {
		imsi   string
		public bool
	}{{"460001234567890", true}, {"310260123456789", false}, {"", false}} {
		cellularSIMHome.Store(simHomeUnknown)
		noteSIMIMSI(tt.imsi)
		servers := cellularDNSServers(context.Background(), "not-a-real-interface")
		hasPublic := false
		for _, s := range servers {
			if s == cellularPublicDNS[0] {
				hasPublic = true
			}
		}
		if hasPublic != tt.public {
			t.Fatalf("IMSI %q: servers %v", tt.imsi, servers)
		}
	}
}

func TestIPv4SortedFirst(t *testing.T) {
	got := sortIPv4First([]net.IP{net.ParseIP("2001:4860::8888"), net.ParseIP("8.8.8.8")})
	if got[0].String() != "8.8.8.8" || got[1].String() != "2001:4860::8888" {
		t.Fatalf("order: %v", got)
	}
}

func TestIPv6LeadNeedsRealIPv4Failure(t *testing.T) {
	defer func() { cellularIPv6PreferredUntil.Store(0); cellularIPv6Wins.Store(0) }()
	cellularIPv6PreferredUntil.Store(0)
	cellularIPv6Wins.Store(0)
	noteCellularFamilyWin(true, false)
	noteCellularFamilyWin(true, false)
	if cellularIPv6PreferredUntil.Load() != 0 {
		t.Fatal("two slow IPv4 handshakes made IPv6 lead")
	}
	noteCellularFamilyWin(false, false)
	noteCellularFamilyWin(true, false)
	noteCellularFamilyWin(true, false)
	if cellularIPv6PreferredUntil.Load() != 0 {
		t.Fatal("an IPv4 success did not reset the count")
	}
	noteCellularFamilyWin(true, true)
	if cellularIPv6PreferredUntil.Load() == 0 {
		t.Fatal("a failed IPv4 attempt did not make IPv6 lead")
	}
}

package main

import (
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
	storeCellularIPv4(key, []net.IP{net.ParseIP("8.8.8.8")}, now)
	if ips := cachedCellularIPv4(key, now.Add(cellularDNSCacheTTL/2)); len(ips) != 1 {
		t.Fatal("fresh entry missing")
	}
	if ips := cachedCellularIPv4(key, now.Add(cellularDNSCacheTTL+time.Second)); ips != nil {
		t.Fatal("expired entry returned")
	}
}

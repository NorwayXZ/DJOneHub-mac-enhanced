package main

import (
	"testing"
	"time"
)

func TestIPv4Segment(t *testing.T) {
	for ip, want := range map[string]string{"223.104.79.51": "223.104.79", "111.55.210.108": "111.55.210", "::1": "", "bad": ""} {
		if got := ipv4Segment(ip); got != want {
			t.Fatalf("segment %q = %q, want %q", ip, got, want)
		}
	}
}

func TestRecentSegmentsHonoursWindow(t *testing.T) {
	now := time.Now().UTC()
	visits := []proxyIPVisit{
		{IP: "223.104.68.90", LastSeen: now.Add(-30 * time.Hour).Format(time.RFC3339)},
		{IP: "223.104.79.51", LastSeen: now.Add(-2 * time.Hour).Format(time.RFC3339)},
		{IP: "223.104.79.85", LastSeen: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		{IP: "111.55.210.108", LastSeen: "not-a-time"},
	}
	used := recentSegments(visits, now.Add(-24*time.Hour))
	if !used["223.104.79"] || used["223.104.68"] || used["111.55.210"] || len(used) != 1 {
		t.Fatalf("unexpected recent segments: %v", used)
	}
}

func TestSegmentModeClampsAvoidWindow(t *testing.T) {
	if m := segmentReconnectMode(0); m.avoidHours != defaultSegmentAvoidHours || m.maxAttempts != segmentMaxAttempts {
		t.Fatalf("default mode: %+v", m)
	}
	if m := segmentReconnectMode(10000); m.avoidHours != maxSegmentAvoidHours {
		t.Fatalf("clamp: %+v", m)
	}
}

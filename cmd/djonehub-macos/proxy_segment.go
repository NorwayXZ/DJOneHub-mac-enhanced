package main

import (
	"fmt"
	"net"
	"time"
)

// Segment rotation keeps cycling the radio until the exit lands in a /24 not
// used within the avoid window. Risk platforms commonly treat one /24 as one
// source, and the carrier's pool is small enough that a single cycle often
// returns a recently used segment. Attempts are capped because the pool, not
// this App, decides which segment comes back.
const (
	segmentMaxAttempts       = 4
	defaultSegmentAvoidHours = 24
	maxSegmentAvoidHours     = 168
)

type reconnectMode struct {
	name        string // "ip" or "segment"
	maxAttempts int
	avoidHours  int
}

func ipReconnectMode() reconnectMode {
	return reconnectMode{name: "ip", maxAttempts: maxReconnectAttempts}
}

func segmentReconnectMode(avoidHours int) reconnectMode {
	if avoidHours <= 0 {
		avoidHours = defaultSegmentAvoidHours
	}
	if avoidHours > maxSegmentAvoidHours {
		avoidHours = maxSegmentAvoidHours
	}
	return reconnectMode{name: "segment", maxAttempts: segmentMaxAttempts, avoidHours: avoidHours}
}

// ipv4Segment returns the /24 prefix ("a.b.c") of an IPv4 address.
func ipv4Segment(ip string) string {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", parsed[0], parsed[1], parsed[2])
}

// recentSegments lists the /24 segments of visits last seen at or after since.
func recentSegments(visits []proxyIPVisit, since time.Time) map[string]bool {
	used := map[string]bool{}
	for _, visit := range visits {
		seen, err := time.Parse(time.RFC3339, visit.LastSeen)
		if err != nil || seen.Before(since) {
			continue
		}
		if segment := ipv4Segment(visit.IP); segment != "" {
			used[segment] = true
		}
	}
	return used
}

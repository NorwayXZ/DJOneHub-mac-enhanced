package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIPLedgerCountsEarlierUsesAcrossModules(t *testing.T) {
	root := t.TempDir()
	oldRoot, oldStore := fleetStateRoot, ipLedgerStore.path
	fleetStateRoot, ipLedgerStore.path = root, filepath.Join(root, "ip-ledger.json")
	defer func() { fleetStateRoot, ipLedgerStore.path = oldRoot, oldStore }()

	write := func(path string, visits []proxyIPVisit) {
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		b, _ := json.Marshal(visits)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := []proxyIPVisit{
		{IP: "1.1.1.1", FirstSeen: "2026-11-01T10:00:00Z", LastSeen: "2026-11-01T11:00:00Z"},
		{IP: "2.2.2.2", FirstSeen: "2026-11-02T10:00:00Z", LastSeen: "2026-11-02T10:30:00Z"},
	}
	write(filepath.Join(root, "modules", "m1", "proxy-ip-history.json"), a)
	write(filepath.Join(root, "proxy-ip-history.json"), a) // old copy: must not double count
	write(filepath.Join(root, "modules", "m2", "proxy-ip-history.json"), []proxyIPVisit{
		{IP: "1.1.1.1", FirstSeen: "2026-11-03T10:00:00Z", LastSeen: "2026-11-03T12:00:00Z"},
	})

	if r := recordForIP("1.1.1.1", ""); r != nil {
		// Not seeded yet: no answer rather than a wrong "never used".
		t.Fatalf("unseeded ledger answered %+v", r)
	}

	// The current use of 1.1.1.1 starts now; seeding happens on this first write.
	noteIPUse("1.1.1.1", "2026-11-05T09:00:00Z", "2026-11-05T09:00:00Z")
	r := recordForIP("1.1.1.1", "2026-11-05T09:00:00Z")
	if r == nil || r.Uses != 2 || r.First != "2026-11-01T10:00:00Z" || len(r.Recent) != 2 || r.Recent[0].Start != "2026-11-03T10:00:00Z" {
		t.Fatalf("record = %+v", r)
	}

	// Extending the running use neither adds a use nor rewrites within minutes.
	noteIPUse("1.1.1.1", "2026-11-05T09:00:00Z", "2026-11-05T09:01:00Z")
	noteIPUse("1.1.1.1", "2026-11-05T09:00:00Z", "2026-11-05T09:30:00Z")
	e := ipLedgerStore.get().IPs["1.1.1.1"]
	if e.Uses != 3 || e.Recent[len(e.Recent)-1].End != "2026-11-05T09:30:00Z" {
		t.Fatalf("entry = %+v", e)
	}

	if r := recordForIP("9.9.9.9", "2026-11-05T09:00:00Z"); r == nil || r.Uses != 0 {
		t.Fatalf("new IP record = %+v", r)
	}
	if r := recordForIP("2.2.2.2", "2026-11-05T09:00:00Z"); r.Uses != 1 {
		t.Fatalf("2.2.2.2 record = %+v", r)
	}
}

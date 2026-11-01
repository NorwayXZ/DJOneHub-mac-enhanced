package main

import (
	"testing"
	"time"
)

func TestBillingCycleFollowsActivationDay(t *testing.T) {
	loc := time.Local
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, loc) }
	for _, tt := range []struct {
		now        time.Time
		cycleDay   int
		start, end string
	}{
		{day(2026, 11, 20), 15, "2026-11-15", "2026-12-15"},
		{day(2026, 11, 10), 15, "2026-10-15", "2026-11-15"},
		{day(2026, 11, 15), 15, "2026-11-15", "2026-12-15"},
		{day(2027, 1, 5), 15, "2026-12-15", "2027-01-15"},
		// Activated on the 31st: short months renew on their last day.
		{day(2027, 2, 28), 31, "2027-02-28", "2027-03-31"},
		{day(2027, 2, 27), 31, "2027-01-31", "2027-02-28"},
		{day(2026, 11, 1), 1, "2026-11-01", "2026-12-01"},
	} {
		start, end := billingCycle(tt.now, tt.cycleDay)
		if got := start.Format("2006-01-02") + " " + end.Format("2006-01-02"); got != tt.start+" "+tt.end {
			t.Fatalf("%s day %d: %s", tt.now.Format("2006-01-02"), tt.cycleDay, got)
		}
	}
}

func TestUsageDeltaSurvivesCounterResets(t *testing.T) {
	prev := &usageCounterState{Iface: "en15", interfaceCounterReading: interfaceCounterReading{Rx: 1000000, Tx: 100000, RxPkts: 1000, TxPkts: 500}, Proxy: 900000}
	d := usageDelta(prev, "en15", interfaceCounterReading{Rx: 2000000, Tx: 150000, RxPkts: 2000, TxPkts: 800}, 1800000)
	if d.Rx != 1000000-1000*14 || d.Tx != 50000-300*14 || d.Proxy != 900000 {
		t.Fatalf("normal delta: %+v", d)
	}
	// Module replugged: a new interface counts from zero.
	d = usageDelta(prev, "en16", interfaceCounterReading{Rx: 5000, Tx: 1000}, 900000)
	if d.Rx != 5000 || d.Tx != 1000 || d.Proxy != 0 {
		t.Fatalf("new interface: %+v", d)
	}
	// Backend restarted: the proxy byte counter starts again at zero.
	if d = usageDelta(prev, "en15", prev.interfaceCounterReading, 1234); d.Proxy != 1234 || d.Rx != 0 {
		t.Fatalf("backend restart: %+v", d)
	}
	if d = usageDelta(nil, "en15", interfaceCounterReading{Rx: 99}, 1); d != (usageBytes{}) {
		t.Fatalf("first reading must only set the baseline: %+v", d)
	}
}

func TestUsageFollowsTheSIMAndQuota(t *testing.T) {
	dir := t.TempDir()
	var l usageLedger
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)
	l.record(dir, "8986A", "en15", interfaceCounterReading{Rx: 1000}, 0, now)
	l.record(dir, "8986A", "en15", interfaceCounterReading{Rx: 1000 + 600*usageMiB}, 0, now)
	l.record(dir, "8901B", "en15", interfaceCounterReading{Rx: 1000 + 700*usageMiB}, 0, now)
	// Back to the first SIM: its usage continues from where it was.
	l.record(dir, "8986A", "en15", interfaceCounterReading{Rx: 1000 + 1200*usageMiB}, 0, now)
	l.mu.Lock()
	a, b := l.file.Cards["8986A"], l.file.Cards["8901B"]
	l.mu.Unlock()
	if a.Total.Rx != 1100*usageMiB || b.Total.Rx != 100*usageMiB {
		t.Fatalf("A %d B %d", a.Total.Rx/usageMiB, b.Total.Rx/usageMiB)
	}
	a.Plan = simPlan{CycleDay: 15, QuotaMB: 1024, AutoStop: true}
	if blocked, _ := l.blocked(now); !blocked {
		t.Fatal("1100 MB of a 1024 MB plan did not block")
	}
	// The previous cycle's usage does not count after renewal.
	if blocked, _ := l.blocked(time.Date(2026, 12, 16, 10, 0, 0, 0, time.Local)); blocked {
		t.Fatal("usage carried into the next cycle")
	}
	a.Plan.AutoStop = false
	if blocked, _ := l.blocked(now); blocked {
		t.Fatal("blocked with auto-stop off")
	}
	// Calibrating to the carrier's figure replaces the measured cycle total.
	start, end := billingCycle(now, 15)
	a.Adjust = map[string]int64{start.Format("2006-01-02"): 900*usageMiB - a.cycleUsage(start, end, false).total()}
	if got := a.cycleUsage(start, end, true).total(); got != 900*usageMiB {
		t.Fatalf("calibrated %d MB", got/usageMiB)
	}
}

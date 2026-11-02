package main

import (
	"testing"
	"time"
)

func TestParseFraudPage(t *testing.T) {
	page := `<pre>
      "ip":"110.79.241.3",
      "score":"37",
      "risk":"medium",
      "is_blacklisted_external": false,
</pre><div class="score">Fraud Score: 37</div>`
	score, risk, err := parseFraudPage(page)
	if err != nil || score != 37 || risk != "medium" {
		t.Fatalf("got %d %q %v", score, risk, err)
	}
	if _, _, err := parseFraudPage("<html>blocked</html>"); err == nil {
		t.Fatal("expected error without a score")
	}
}

func TestFraudTargets(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	five := 5
	fresh := now.Add(-time.Hour).Format(time.RFC3339)
	stale := now.Add(-13 * time.Hour).Format(time.RFC3339)
	visits := []proxyIPVisit{
		{IP: "1.1.1.1", FraudScore: &five, FraudChecked: stale},
		{IP: "2.2.2.2"},
		{IP: "3.3.3.3", FraudScore: &five, FraudChecked: fresh},
		{IP: "2.2.2.2"},
		{IP: "1.1.1.1"},
	}
	got := fraudTargets(visits, "1.1.1.1", now, scamalyticsSource)
	if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "2.2.2.2" {
		t.Fatalf("stale current first, then unscored: %v", got)
	}
	got = fraudTargets(visits, "3.3.3.3", now, scamalyticsSource)
	if len(got) != 1 || got[0] != "2.2.2.2" {
		t.Fatalf("fresh current is skipped: %v", got)
	}
}

func TestIPQSTargetsAndFlags(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	score := 89
	visits := []proxyIPVisit{
		{IP: "1.1.1.1", IPQSScore: &score, IPQSChecked: now.Add(-25 * time.Hour).Format(time.RFC3339)},
		{IP: "2.2.2.2"},
		{IP: "1.1.1.1"},
	}
	got := fraudTargets(visits, "1.1.1.1", now, ipqsSource)
	if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "2.2.2.2" {
		t.Fatalf("day-old current refreshed, unscored backfilled: %v", got)
	}
	flags := ipqsResult{Proxy: true, RecentAbuse: true}.flags()
	if len(flags) != 2 || flags[0] != "proxy" || flags[1] != "recent_abuse" {
		t.Fatalf("flags: %v", flags)
	}
	if ipqsKeyHint("abcd1234efgh5678") != "abcd…5678" {
		t.Fatalf("hint: %s", ipqsKeyHint("abcd1234efgh5678"))
	}
}

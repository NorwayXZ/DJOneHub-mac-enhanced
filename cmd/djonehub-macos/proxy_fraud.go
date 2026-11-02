package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Fraud scores come from Scamalytics' public IP page (0-100 plus a risk
// level), the figure fingerprint-browser users usually check. The lookup goes
// over the Mac's default route so it costs no cellular data; it only reveals
// that this Mac asked about the IP.
const (
	fraudLookupURL   = "https://scamalytics.com/ip/"
	fraudRefreshAge  = 12 * time.Hour
	fraudLookupGap   = 2 * time.Second
	fraudBackfillMax = 30
)

var (
	reFraudScore = regexp.MustCompile(`"score"\s*:\s*"(\d{1,3})"`)
	reFraudRisk  = regexp.MustCompile(`"risk"\s*:\s*"([a-z ]+)"`)
)

func parseFraudPage(page string) (int, string, error) {
	m := reFraudScore.FindStringSubmatch(page)
	if m == nil {
		return 0, "", errors.New("fraud score not found on page")
	}
	score, _ := strconv.Atoi(m[1])
	if score > 100 {
		return 0, "", fmt.Errorf("fraud score out of range: %d", score)
	}
	risk := ""
	if r := reFraudRisk.FindStringSubmatch(page); r != nil {
		risk = r[1]
	}
	return score, risk, nil
}

func lookupFraudScore(ctx context.Context, ip string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fraudLookupURL+ip, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, "", fmt.Errorf("fraud lookup HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return 0, "", err
	}
	return parseFraudPage(string(body))
}

// knownFraud returns the most recent visit of ip that has a Scamalytics score.
func knownFraud(visits []proxyIPVisit, ip string) *proxyIPVisit {
	return lastScored(visits, ip, scamalyticsSource.has)
}

// knownIPQS returns the most recent visit of ip that has an IPQS score.
func knownIPQS(visits []proxyIPVisit, ip string) *proxyIPVisit {
	return lastScored(visits, ip, ipqsSource.has)
}

func lastScored(visits []proxyIPVisit, ip string, has func(*proxyIPVisit) bool) *proxyIPVisit {
	for i := len(visits) - 1; i >= 0; i-- {
		if visits[i].IP == ip && has(&visits[i]) {
			return &visits[i]
		}
	}
	return nil
}

// fraudSource describes one score provider stored on history visits.
type fraudSource struct {
	has     func(*proxyIPVisit) bool
	checked func(*proxyIPVisit) string
	refresh time.Duration
	max     int
}

var (
	scamalyticsSource = fraudSource{
		has:     func(v *proxyIPVisit) bool { return v.FraudScore != nil },
		checked: func(v *proxyIPVisit) string { return v.FraudChecked },
		refresh: fraudRefreshAge,
		max:     fraudBackfillMax,
	}
	ipqsSource = fraudSource{
		has:     func(v *proxyIPVisit) bool { return v.IPQSScore != nil },
		checked: func(v *proxyIPVisit) string { return v.IPQSChecked },
		refresh: ipqsRefreshAge,
		max:     ipqsBackfillMax,
	}
)

// fraudTargets lists IPs to look up, newest first: the current IP when its
// score is missing or stale, then history IPs that never got one.
func fraudTargets(visits []proxyIPVisit, current string, now time.Time, src fraudSource) []string {
	var out []string
	seen := map[string]bool{}
	if current != "" {
		seen[current] = true
		known := lastScored(visits, current, src.has)
		var checked time.Time
		if known != nil {
			checked, _ = time.Parse(time.RFC3339, src.checked(known))
		}
		if known == nil || now.Sub(checked) >= src.refresh {
			out = append(out, current)
		}
	}
	for i := len(visits) - 1; i >= 0 && len(out) < src.max; i-- {
		ip := visits[i].IP
		if seen[ip] {
			continue
		}
		seen[ip] = true
		if lastScored(visits, ip, src.has) == nil {
			out = append(out, ip)
		}
	}
	return out
}

// fillFraudScores looks up missing or stale scores one at a time (Scamalytics,
// then IPQS when a key is set) and stores them on every visit of that IP.
// Only one fill runs at a time.
func (p *cellularProxy) fillFraudScores(ctx context.Context) {
	if !p.fraudFilling.CompareAndSwap(false, true) {
		return
	}
	defer p.fraudFilling.Store(false)
	p.mu.Lock()
	p.loadHistoryLocked()
	scam := fraudTargets(p.state.History, p.state.PublicIP, time.Now(), scamalyticsSource)
	var viaIPQS []string
	key := ipqs.apiKey()
	if key != "" {
		viaIPQS = fraudTargets(p.state.History, p.state.PublicIP, time.Now(), ipqsSource)
	}
	p.mu.Unlock()
	first := true
	pause := func() bool {
		if first {
			first = false
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(fraudLookupGap):
			return true
		}
	}
	store := func(ip string, set func(v *proxyIPVisit)) {
		p.mu.Lock()
		defer p.mu.Unlock()
		for j := range p.state.History {
			if p.state.History[j].IP == ip {
				set(&p.state.History[j])
			}
		}
		if p.state.HistoryError == "" {
			p.saveHistoryLocked()
		}
	}
	for _, ip := range scam {
		if !pause() {
			return
		}
		score, risk, err := lookupFraudScore(ctx, ip)
		if err != nil {
			log.Printf("fraud score lookup for %s failed: %v", ip, err)
			continue
		}
		checked := time.Now().UTC().Format(time.RFC3339)
		store(ip, func(v *proxyIPVisit) { s := score; v.FraudScore, v.FraudRisk, v.FraudChecked = &s, risk, checked })
	}
	for _, ip := range viaIPQS {
		if !pause() {
			return
		}
		r, err := lookupIPQS(ctx, key, ip)
		if err != nil {
			log.Printf("IPQS lookup for %s failed: %v", ip, err)
			// Out of credits or a revoked key fails every lookup the same way.
			return
		}
		checked := time.Now().UTC().Format(time.RFC3339)
		store(ip, func(v *proxyIPVisit) {
			s := r.FraudScore
			v.IPQSScore, v.IPQSFlags, v.IPQSChecked = &s, r.flags(), checked
		})
	}
}

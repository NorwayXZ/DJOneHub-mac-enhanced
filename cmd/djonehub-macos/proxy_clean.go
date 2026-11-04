package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// "Switch to a clean IP" keeps cycling the radio until the new exit's fraud
// score is at or below the user's limit: IPQualityScore when a key is set,
// Scamalytics otherwise. Each attempt costs one lookup (an IPQS credit).
const (
	cleanMaxAttempts   = 6
	defaultCleanIPQS   = 75
	defaultCleanScam   = 30
	cleanLookupTimeout = 15 * time.Second
	cleanSettingsFile  = "clean-ip.json"
)

type cleanSettings struct {
	MaxIPQS int `json:"max_ipqs"`
	MaxScam int `json:"max_scamalytics"`
}

var cleanIP = settingsFile[cleanSettings]{name: cleanSettingsFile}

func (s cleanSettings) limits() (ipqsLimit, scamLimit int) {
	ipqsLimit, scamLimit = s.MaxIPQS, s.MaxScam
	if ipqsLimit <= 0 {
		ipqsLimit = defaultCleanIPQS
	}
	if scamLimit <= 0 {
		scamLimit = defaultCleanScam
	}
	return
}

func cleanReconnectMode() reconnectMode {
	return reconnectMode{name: "clean", maxAttempts: cleanMaxAttempts}
}

// cleanVerdict scores ip and stores the score in the history so the
// background filler does not look it up again.
func (p *cellularProxy) cleanVerdict(ctx context.Context, ip string) (label string, clean bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, cleanLookupTimeout)
	defer cancel()
	ipqsLimit, scamLimit := cleanIP.get().limits()
	checked := time.Now().UTC().Format(time.RFC3339)
	if key := ipqs.apiKey(); key != "" {
		r, err := lookupIPQS(ctx, key, ip)
		if err != nil {
			return "", false, err
		}
		p.storeVisit(ip, func(v *proxyIPVisit) {
			s := r.FraudScore
			v.IPQSScore, v.IPQSFlags, v.IPQSChecked = &s, r.flags(), checked
		})
		return fmt.Sprintf("IPQS %d", r.FraudScore), r.FraudScore <= ipqsLimit, nil
	}
	score, risk, err := lookupFraudScore(ctx, ip)
	if err != nil {
		return "", false, err
	}
	p.storeVisit(ip, func(v *proxyIPVisit) { s := score; v.FraudScore, v.FraudRisk, v.FraudChecked = &s, risk, checked })
	return fmt.Sprintf("Scamalytics %d", score), score <= scamLimit, nil
}

func (p *cellularProxy) storeVisit(ip string, set func(v *proxyIPVisit)) {
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

type cleanView struct {
	MaxIPQS int    `json:"max_ipqs"`
	MaxScam int    `json:"max_scamalytics"`
	Source  string `json:"source"` // which score decides: "ipqs" or "scamalytics"
}

func cleanViewNow() cleanView {
	i, s := cleanIP.get().limits()
	source := "scamalytics"
	if ipqs.apiKey() != "" {
		source = "ipqs"
	}
	return cleanView{MaxIPQS: i, MaxScam: s, Source: source}
}

// cleanIPAPI: GET the limits; POST {max_ipqs, max_scamalytics} saves them.
func (a *app) cleanIPAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body cleanSettings
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.MaxIPQS < 1 || body.MaxIPQS > 100 || body.MaxScam < 1 || body.MaxScam > 100 {
			writeError(w, 400, "欺诈值上限要在 1 到 100 之间")
			return
		}
		if err := cleanIP.update(func(s *cleanSettings) { *s = body }); err != nil {
			writeError(w, 500, "保存失败："+err.Error())
			return
		}
	}
	writeJSON(w, 200, cleanViewNow())
}

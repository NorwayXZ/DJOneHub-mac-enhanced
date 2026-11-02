package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const maxProxyIPVisits = 500

type proxyIPVisit struct {
	IP        string `json:"ip"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Location  string `json:"location"`
	ISP       string `json:"isp"`
	// Scamalytics fraud score (0-100) and risk level, filled in the background.
	FraudScore   *int   `json:"fraud_score,omitempty"`
	FraudRisk    string `json:"fraud_risk,omitempty"`
	FraudChecked string `json:"fraud_checked,omitempty"`
	// IPQualityScore, only when the user entered an API key.
	IPQSScore   *int     `json:"ipqs_score,omitempty"`
	IPQSFlags   []string `json:"ipqs_flags,omitempty"`
	IPQSChecked string   `json:"ipqs_checked,omitempty"`
}

// Call with p.mu held. History stays on this Mac, separately from transient proxy state.
func (p *cellularProxy) loadHistoryLocked() {
	if p.historyLoaded {
		return
	}
	p.historyLoaded = true
	if p.historyPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			p.state.HistoryError = err.Error()
			return
		}
		p.historyPath = filepath.Join(dir, "DJOneHub", "proxy-ip-history.json")
		if scopedModuleID != "" {
			base := fleetStateRoot
			if base == "" {
				base = filepath.Join(dir, "DJOneHub")
			}
			p.historyPath = filepath.Join(base, "modules", scopedModuleID, "proxy-ip-history.json")
		}
	}
	b, err := os.ReadFile(p.historyPath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		p.state.HistoryError = err.Error()
		return
	}
	var visits []proxyIPVisit
	if err = json.Unmarshal(b, &visits); err != nil {
		p.state.HistoryError = err.Error()
		return
	}
	if len(visits) > maxProxyIPVisits {
		visits = visits[len(visits)-maxProxyIPVisits:]
	}
	p.state.History = visits
}

func (p *cellularProxy) recordHistoryLocked() {
	p.loadHistoryLocked()
	// Preserve unreadable/corrupt history rather than silently overwriting it.
	if p.state.HistoryError != "" || p.state.PublicIP == "" {
		return
	}
	visits := p.state.History
	visit := proxyIPVisit{IP: p.state.PublicIP, FirstSeen: p.state.CheckedAt, LastSeen: p.state.CheckedAt, Location: p.state.Location, ISP: p.state.ISP}
	if len(visits) > 0 && visits[len(visits)-1].IP == visit.IP {
		visit.FirstSeen = visits[len(visits)-1].FirstSeen
		if visit.Location == "" {
			visit.Location = visits[len(visits)-1].Location
			visit.ISP = visits[len(visits)-1].ISP
		}
		visit.FraudScore = visits[len(visits)-1].FraudScore
		visit.FraudRisk = visits[len(visits)-1].FraudRisk
		visit.FraudChecked = visits[len(visits)-1].FraudChecked
		visit.IPQSScore = visits[len(visits)-1].IPQSScore
		visit.IPQSFlags = visits[len(visits)-1].IPQSFlags
		visit.IPQSChecked = visits[len(visits)-1].IPQSChecked
		visits[len(visits)-1] = visit
	} else {
		// An IP seen before keeps its known score until it is refreshed.
		if known := knownFraud(visits, visit.IP); known != nil {
			visit.FraudScore, visit.FraudRisk, visit.FraudChecked = known.FraudScore, known.FraudRisk, known.FraudChecked
		}
		if known := knownIPQS(visits, visit.IP); known != nil {
			visit.IPQSScore, visit.IPQSFlags, visit.IPQSChecked = known.IPQSScore, known.IPQSFlags, known.IPQSChecked
		}
		visits = append(visits, visit)
	}
	if len(visits) > maxProxyIPVisits {
		visits = visits[len(visits)-maxProxyIPVisits:]
	}
	p.state.History = visits
	p.saveHistoryLocked()
}

func (p *cellularProxy) saveHistoryLocked() {
	visits := p.state.History
	b, err := json.MarshalIndent(visits, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(p.historyPath), 0700)
	}
	if err == nil {
		var f *os.File
		f, err = os.CreateTemp(filepath.Dir(p.historyPath), ".proxy-history-*")
		if err == nil {
			name := f.Name()
			defer os.Remove(name)
			_, err = f.Write(b)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err == nil {
				err = os.Rename(name, p.historyPath)
			}
		}
	}
	if err != nil {
		p.state.HistoryError = err.Error()
	}
}

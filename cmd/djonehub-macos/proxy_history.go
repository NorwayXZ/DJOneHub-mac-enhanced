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
		visits[len(visits)-1] = visit
	} else {
		visits = append(visits, visit)
	}
	if len(visits) > maxProxyIPVisits {
		visits = visits[len(visits)-maxProxyIPVisits:]
	}
	p.state.History = visits
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

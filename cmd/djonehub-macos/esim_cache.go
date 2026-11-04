package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/iniwex5/vohive/internal/esim"
)

// Reading a whole eSIM card over AT+CSIM takes from several seconds to a
// minute, and the module worker (with its in-memory cache) restarts after
// every module reboot or Profile switch. The last overview is kept on disk so
// the page shows it at once while the card is read again in the background.

var (
	savedOverviewMu   sync.Mutex
	savedOverviewLast []byte
)

func esimOverviewCachePath() string { return moduleStatePath("esim-overview.json") }

func loadSavedESIMOverview() *esim.EsimOverview {
	path := esimOverviewCachePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var overview esim.EsimOverview
	if json.Unmarshal(data, &overview) != nil || len(overview.Profiles) == 0 && overview.ChipInfo == nil {
		return nil
	}
	return &overview
}

func saveESIMOverview(overview *esim.EsimOverview) {
	path := esimOverviewCachePath()
	data, err := json.Marshal(overview)
	if path == "" || err != nil {
		return
	}
	savedOverviewMu.Lock()
	defer savedOverviewMu.Unlock()
	if bytes.Equal(data, savedOverviewLast) {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	if os.WriteFile(path, data, 0o600) == nil {
		savedOverviewLast = data
	}
}

func forgetSavedESIMOverview() {
	savedOverviewMu.Lock()
	defer savedOverviewMu.Unlock()
	savedOverviewLast = nil
	if path := esimOverviewCachePath(); path != "" {
		_ = os.Remove(path)
	}
}

// refreshSavedESIMOverview reads the card and keeps the result on disk; a card
// that is no longer an eSIM (or unreadable) drops the saved copy.
func refreshSavedESIMOverview(manager *esim.Manager) {
	overview, err := manager.GetEsimOverview()
	if err != nil {
		forgetSavedESIMOverview()
		return
	}
	saveESIMOverview(overview)
}

// quickESIMOverview answers from memory, else from the saved copy while the
// card is read in the background (refreshing true), else reads the card.
func quickESIMOverview(manager *esim.Manager) (*esim.EsimOverview, bool, error) {
	if overview := manager.CachedOverview(); overview != nil {
		saveESIMOverview(overview)
		return overview, false, nil
	}
	if saved := loadSavedESIMOverview(); saved != nil {
		go refreshSavedESIMOverview(manager)
		return saved, true, nil
	}
	overview, err := manager.GetEsimOverview()
	if err == nil {
		saveESIMOverview(overview)
	}
	return overview, false, err
}

// warmESIMOverview reads the card soon after the worker starts, so the eSIM
// page is ready before anyone opens it.
func warmESIMOverview(manager *esim.Manager) {
	time.Sleep(5 * time.Second)
	if manager.CachedOverview() == nil {
		refreshSavedESIMOverview(manager)
	}
}

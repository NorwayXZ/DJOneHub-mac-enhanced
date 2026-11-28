package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sent messages are kept on disk per module, so what was sent from the app,
// the QR sender, Telegram or SIM keep-alive shows in the conversation after a
// restart too.
type sentSMS struct {
	Phone   string    `json:"phone"`
	Content string    `json:"content"`
	SentAt  time.Time `json:"sent_at"`
}

const sentSMSKeep = 500

var sentSMSMu sync.Mutex

func sentSMSPath() string { return moduleStatePath("sent-sms.json") }

func loadSentSMS() []sentSMS {
	var items []sentSMS
	if path := sentSMSPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &items)
		}
	}
	return items
}

func recordSentSMS(phone, content string) {
	path := sentSMSPath()
	if path == "" {
		return
	}
	sentSMSMu.Lock()
	defer sentSMSMu.Unlock()
	items := append(loadSentSMS(), sentSMS{Phone: phone, Content: content, SentAt: time.Now().UTC().Truncate(time.Second)})
	if len(items) > sentSMSKeep {
		items = items[len(items)-sentSMSKeep:]
	}
	if data, err := json.Marshal(items); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, data, 0o600)
	}
}

func (a *app) listSentSMS(w http.ResponseWriter, _ *http.Request) {
	sentSMSMu.Lock()
	items := loadSentSMS()
	sentSMSMu.Unlock()
	if items == nil {
		items = []sentSMS{}
	}
	writeJSON(w, http.StatusOK, items)
}

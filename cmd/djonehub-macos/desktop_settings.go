package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// desktopSettings are the Windows tray app's preferences (the macOS app keeps
// the same ones in its own defaults). The window edits them here and the tray
// reads them, since the two run as different programs.
type desktopSettings struct {
	Notifications bool   `json:"notifications"`
	ShowContents  bool   `json:"show_contents"`
	Ringtone      string `json:"ringtone"`
	// CopyCode puts a new SMS's verification code on the clipboard.
	CopyCode bool `json:"copy_code"`
	// QuitAt is set by 完全退出; the tray exits when it sees it.
	QuitAt time.Time `json:"quit_at,omitempty"`
}

func desktopSettingsPath() string {
	dir := fleetStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "desktop.json")
}

func loadDesktopSettings() desktopSettings {
	s := desktopSettings{Notifications: true, Ringtone: "Ring01", CopyCode: true}
	if b, err := os.ReadFile(desktopSettingsPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveDesktopSettings(s desktopSettings) error {
	path := desktopSettingsPath()
	return withSharedFileLock(path, func() error {
		b, _ := json.MarshalIndent(s, "", "  ")
		return os.WriteFile(path, b, 0644)
	})
}

func desktopSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if desktopSettingsPath() == "" {
		writeError(w, 500, "无法确定设置保存位置")
		return
	}
	if r.Method == http.MethodPut {
		// Fields the client leaves out keep their saved values.
		current := loadDesktopSettings()
		body := current
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			writeError(w, 400, "invalid request")
			return
		}
		body.QuitAt = current.QuitAt
		if err := saveDesktopSettings(body); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, loadDesktopSettings())
}

// desktopQuitAPI is 设置 › 完全退出 on Windows: the tray exits and the service
// stops. Opening DJOneHub again starts both.
func desktopQuitAPI(w http.ResponseWriter, _ *http.Request) {
	if runtime.GOOS != "windows" {
		writeError(w, 400, "仅用于 Windows 版")
		return
	}
	s := loadDesktopSettings()
	s.QuitAt = time.Now()
	if err := saveDesktopSettings(s); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"quitting": true})
	// Give the tray a few polls to see the request before the API goes away.
	time.AfterFunc(6*time.Second, requestServiceStop)
}

// trayAudio is what the Windows tray reports about the call audio it plays,
// for the in-call screen.
var trayAudio struct {
	sync.Mutex
	State struct {
		Running bool      `json:"running"`
		Error   string    `json:"error"`
		Detail  string    `json:"detail"`
		At      time.Time `json:"at"`
	}
}

func trayAudioAPI(w http.ResponseWriter, r *http.Request) {
	trayAudio.Lock()
	defer trayAudio.Unlock()
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&trayAudio.State); err != nil {
			writeError(w, 400, "invalid request")
			return
		}
		trayAudio.State.At = time.Now()
	}
	s := trayAudio.State
	if time.Since(s.At) > 8*time.Second {
		s.Running, s.Error, s.Detail = false, "", ""
	}
	writeJSON(w, 200, s)
}

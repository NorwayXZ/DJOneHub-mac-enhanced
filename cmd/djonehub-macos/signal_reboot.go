package main

import (
	"os"
	"path/filepath"
	"time"
)

// The signal recovery reboot restarts the module worker, so the reboot
// throttle lives in a file; in memory it reset with every reboot and a module
// that could not find a network was rebooted every couple of minutes, never
// getting the time a roaming SIM needs to scan all bands.
func signalRebootPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	base := filepath.Join(dir, "DJOneHub")
	if scopedModuleID != "" {
		if fleetStateRoot != "" {
			base = fleetStateRoot
		}
		base = filepath.Join(base, "modules", scopedModuleID)
	}
	return filepath.Join(base, "last-signal-reboot")
}

func lastSignalReboot() time.Time {
	if path := signalRebootPath(); path != "" {
		if info, err := os.Stat(path); err == nil {
			return info.ModTime()
		}
	}
	return time.Time{}
}

func markSignalReboot() {
	path := signalRebootPath()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)), 0o600)
}

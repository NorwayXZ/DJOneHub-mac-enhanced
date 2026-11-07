package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The signal recovery reboot restarts the module worker, so the reboot
// throttle lives in a file; in memory it reset with every reboot and a module
// that could not find a network was rebooted every couple of minutes, never
// getting the time a roaming SIM needs to scan all bands.
func signalRebootPath() string { return moduleStatePath("last-signal-reboot") }

// moduleStatePath is where this module's worker keeps a file across restarts.
func moduleStatePath(name string) string {
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
	return filepath.Join(base, name)
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

// Multi-IMSI travel SIMs swap their own identity every couple of minutes
// until one is accepted, and every swap restarts the network search. A radio
// cycle or reboot in the middle throws that search away, so recovery waits
// while the SIM's IMSI has changed recently.
const simSettleTime = 4 * time.Minute

func simIdentityPath() string { return moduleStatePath("sim-identity") }

// simSettling records imsi and reports whether it changed within simSettleTime.
func simSettling(imsi string, now time.Time) bool {
	path := simIdentityPath()
	if imsi == "" || path == "" {
		return false
	}
	if data, err := os.ReadFile(path); err == nil {
		if last, changed, ok := strings.Cut(string(data), "\n"); ok && last == imsi {
			at, err := time.Parse(time.RFC3339, changed)
			return err == nil && now.Sub(at) < simSettleTime
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(imsi+"\n"+now.Format(time.RFC3339)), 0o600)
	return true
}

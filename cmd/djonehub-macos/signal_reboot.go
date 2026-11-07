package main

import (
	"net"
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

// operatorDeregistered: +COPS? mode 2, the radio was told to stop searching.
func operatorDeregistered(resp string) bool { return operatorMode(resp) == "2" }

func operatorMode(resp string) string {
	mode, _, _ := strings.Cut(parseUSBATPrefixed(resp, "+COPS:"), ",")
	return strings.TrimSpace(mode)
}

// clearForbiddenNetworks blanks EF_FPLMN (6F7B, four entries).
const clearForbiddenNetworks = `AT+CRSM=214,28539,0,0,12,"FFFFFFFFFFFFFFFFFFFFFFFF"`

// The module can stay registered with a data session up while its USB
// network stops answering DHCP: the Mac only has a self-assigned 169.254
// address and nothing gets through until the module restarts.
const linkDownReboot = 90 * time.Second

// moduleLinkState looks at this module's USB network interface: found when
// it is up, usable when it also has a real IPv4 address.
func moduleLinkState(items []macNetInterface, session func(string) uint64, scoped uint64) (found, usable bool) {
	for _, item := range items {
		if item.Status != "active" || session(item.Name) == 0 {
			continue
		}
		if scoped != 0 && session(item.Name) != scoped {
			continue
		}
		found = true
		if ip := net.ParseIP(item.IPv4); ip != nil && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() {
			usable = true
		}
	}
	return found, usable
}

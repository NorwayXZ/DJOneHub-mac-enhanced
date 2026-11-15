//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestWindowsADBProbe runs a read-only shell command over each module's ADB
// interface. It needs the real modules, so it runs only with
// DJONEHUB_ADB_PROBE=1 on a Windows machine that has them.
func TestWindowsADBProbe(t *testing.T) {
	if os.Getenv("DJONEHUB_ADB_PROBE") != "1" {
		t.Skip("needs modules")
	}
	modules, err := discoverWinModules()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range modules {
		scopedUSBSession = m.Session
		adb, err := openDJIUSBADB()
		if err != nil {
			t.Logf("%s: %v", m.Instance, err)
			continue
		}
		out, status, err := adb.shellChecked("id -u; uname -r", 10*time.Second)
		adb.Close()
		t.Logf("%s: status=%d err=%v out=%q", m.Instance, status, err, strings.TrimSpace(out))
	}
}

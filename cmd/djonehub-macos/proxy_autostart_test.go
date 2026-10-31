package main

import (
	"path/filepath"
	"testing"
)

func TestProxyAutoStartPersistsUntilManualStop(t *testing.T) {
	history := filepath.Join(t.TempDir(), "proxy-ip-history.json")
	p := &cellularProxy{historyPath: history}
	if p.wantsAutoStart() {
		t.Fatal("auto-start must default to off")
	}
	p.setAutoStart(true)
	// A fresh process (backend restart or update) must keep the user's wish.
	restarted := &cellularProxy{historyPath: history}
	if !restarted.wantsAutoStart() || !restarted.snapshot().AutoStart {
		t.Fatal("auto-start preference was not restored")
	}
	restarted.setAutoStart(false)
	if (&cellularProxy{historyPath: history}).wantsAutoStart() {
		t.Fatal("manual stop must clear auto-start")
	}
}

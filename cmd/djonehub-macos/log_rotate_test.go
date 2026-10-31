package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateLogIfLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launchd.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0644); err != nil {
		t.Fatal(err)
	}
	rotateLogIfLarge(path, 1000) // below limit: untouched
	if info, _ := os.Stat(path); info.Size() != 100 {
		t.Fatal("small log must not rotate")
	}
	rotateLogIfLarge(path, 50)
	if info, _ := os.Stat(path); info.Size() > 80 { // only the rotation notice may remain
		t.Fatalf("log not truncated: %d bytes", info.Size())
	}
	if b, err := os.ReadFile(path + ".1"); err != nil || len(b) != 100 {
		t.Fatalf("previous log not kept: %v", err)
	}
}

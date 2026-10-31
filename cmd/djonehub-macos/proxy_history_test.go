package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProxyHistoryPersistsAndRecognizesRevisit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	p := &cellularProxy{historyPath: path}
	for i, ip := range []string{"8.8.8.8", "8.8.8.8", "1.1.1.1", "8.8.8.8"} {
		p.state.PublicIP = ip
		p.state.CheckedAt = fmt.Sprint(i)
		p.recordHistoryLocked()
	}
	restored := &cellularProxy{historyPath: path}
	s := restored.snapshot()
	if s.HistoryError != "" || len(s.History) != 3 {
		t.Fatalf("restore: %+v", s)
	}
	if s.History[0].FirstSeen != "0" || s.History[0].LastSeen != "1" {
		t.Fatal("consecutive observations not merged", s.History)
	}
	if s.History[0].IP != s.History[2].IP {
		t.Fatal("revisit not preserved", s.History)
	}
	s.History[0].IP = "mutated"
	if restored.snapshot().History[0].IP == "mutated" {
		t.Fatal("snapshot aliases history")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private history file permissions", info, err)
	}
}

func TestProxyHistoryPreservesUnreadableData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	original := []byte("invalid history")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	p := &cellularProxy{historyPath: path}
	p.state.PublicIP = "8.8.8.8"
	p.recordHistoryLocked()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != string(original) || p.state.HistoryError == "" {
		t.Fatal("corrupt history overwritten", err)
	}
}

func TestProxyHistoryRetention(t *testing.T) {
	p := &cellularProxy{historyPath: filepath.Join(t.TempDir(), "history.json"), historyLoaded: true}
	for i := 0; i < maxProxyIPVisits; i++ {
		p.state.History = append(p.state.History, proxyIPVisit{IP: fmt.Sprint(i)})
	}
	p.state.PublicIP = "8.8.8.8"
	p.recordHistoryLocked()
	if len(p.state.History) != maxProxyIPVisits || p.state.History[0].IP != "1" {
		t.Fatal("retention not bounded")
	}
}

package main

import (
	"os"
	"testing"
)

// Tests must never touch the real ~/Library/Application Support/DJOneHub.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "djonehub-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

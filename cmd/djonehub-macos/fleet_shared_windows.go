//go:build windows

package main

func fleetStateDir() string { return "" }

func acquireCallAudio() bool { return true }

func releaseCallAudio() {}

func withSharedFileLock(_ string, fn func() error) error { return fn() }

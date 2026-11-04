//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// fleetStateDir is where module workers share state; the same directory the
// supervisor keeps its bindings in.
func fleetStateDir() string {
	if fleetStateRoot != "" {
		return fleetStateRoot
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "DJOneHub")
}

var callAudio struct {
	sync.Mutex
	file *os.File
}

// acquireCallAudio takes the machine-wide call-audio lock for this worker.
// It is held until the call ends; a second module's call meanwhile gets no
// Mac audio instead of fighting over the same devices. Single-module mode
// never needs it.
func acquireCallAudio() bool {
	callAudio.Lock()
	defer callAudio.Unlock()
	if callAudio.file != nil {
		return true
	}
	dir := fleetStateDir()
	if dir == "" || os.MkdirAll(dir, 0700) != nil {
		return true
	}
	f, err := os.OpenFile(filepath.Join(dir, "call-audio.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return true
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return false
	}
	callAudio.file = f
	return true
}

func releaseCallAudio() {
	callAudio.Lock()
	defer callAudio.Unlock()
	if callAudio.file == nil {
		return
	}
	_ = syscall.Flock(int(callAudio.file.Fd()), syscall.LOCK_UN)
	callAudio.file.Close()
	callAudio.file = nil
}

// withSharedFileLock runs fn while holding an exclusive lock next to path,
// so module workers updating one settings file do not lose each other's
// changes.
func withSharedFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

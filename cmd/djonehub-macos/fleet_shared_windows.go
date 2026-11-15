//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
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

// lockFile takes an exclusive lock on f; with wait false it fails at once
// when another process holds it.
func lockFile(f *os.File, wait bool) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, new(windows.Overlapped))
}

func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

func lockFleetState(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(file, false); err != nil {
		file.Close()
		return nil, errors.New("another module supervisor owns this state directory")
	}
	return func() { unlockFile(file); file.Close() }, nil
}

var callAudio struct {
	sync.Mutex
	file *os.File
}

// acquireCallAudio takes the machine-wide call-audio lock for this worker.
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
	if lockFile(f, false) != nil {
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
	unlockFile(callAudio.file)
	callAudio.file.Close()
	callAudio.file = nil
}

// withSharedFileLock runs fn while holding an exclusive lock next to path.
func withSharedFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f, true); err != nil {
		return err
	}
	defer unlockFile(f)
	return fn()
}

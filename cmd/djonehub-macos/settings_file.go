package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// settingsFile is a small JSON settings file in Application Support/DJOneHub
// shared by every module worker: it rereads the file when another process
// changed it and writes under a file lock so no update is lost.
type settingsFile[T any] struct {
	name    string
	mu      sync.Mutex
	path    string
	modTime time.Time
	value   T
}

func (f *settingsFile[T]) pathLocked() string {
	if f.path == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			f.path = filepath.Join(dir, "DJOneHub", f.name)
		}
	}
	return f.path
}

func (f *settingsFile[T]) reloadLocked() {
	path := f.pathLocked()
	info, err := os.Stat(path)
	if err != nil || info.ModTime().Equal(f.modTime) {
		return
	}
	var next T
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &next) == nil {
		f.value, f.modTime = next, info.ModTime()
	}
}

func (f *settingsFile[T]) get() T {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloadLocked()
	return f.value
}

func (f *settingsFile[T]) update(change func(*T)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := f.pathLocked()
	if path == "" {
		return errors.New("无法确定设置保存位置")
	}
	return withSharedFileLock(path, func() error {
		f.reloadLocked()
		next := f.value
		change(&next)
		b, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0600); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
		f.value = next
		if info, err := os.Stat(path); err == nil {
			f.modTime = info.ModTime()
		}
		return nil
	})
}

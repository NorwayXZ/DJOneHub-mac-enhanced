package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The backend logs to stderr, which launchd appends to launchd.log forever.
// Rotation copies the file to .1 (keeping .2) and truncates it in place: the
// launchd descriptor is opened for appending, so writes continue at the new end.
const (
	logRotateLimit    = 5 << 20
	logRotateKeep     = 2
	logRotateInterval = 10 * time.Minute
	logRepeatWindow   = 10 * time.Minute
)

func launchdLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "DJOneHub", "launchd.log")
}

func runLogRotation(ctx context.Context) {
	path := launchdLogPath()
	// Only rotate when stderr really is that file (not a terminal or a test).
	stderr, err1 := os.Stderr.Stat()
	target, err2 := os.Stat(path)
	if path == "" || err1 != nil || err2 != nil || !os.SameFile(stderr, target) {
		return
	}
	rotateLogIfLarge(path, logRotateLimit)
	ticker := time.NewTicker(logRotateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rotateLogIfLarge(path, logRotateLimit)
		}
	}
}

func rotateLogIfLarge(path string, limit int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < limit {
		return
	}
	for i := logRotateKeep; i > 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i-1), fmt.Sprintf("%s.%d", path, i))
	}
	if err := copyLogFile(path, path+".1"); err != nil {
		log.Printf("log rotation skipped: %v", err)
		return
	}
	if err := os.Truncate(path, 0); err != nil {
		log.Printf("log rotation truncate failed: %v", err)
		return
	}
	log.Printf("log rotated: previous %d bytes kept in %s.1", info.Size(), filepath.Base(path))
}

func copyLogFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// quietLog drops a message identical to the previous one for the same key
// within logRepeatWindow and reports how often it repeated once it changes.
var quietLog = struct {
	sync.Mutex
	last map[string]quietEntry
}{last: map[string]quietEntry{}}

type quietEntry struct {
	msg     string
	at      time.Time
	repeats int
}

func logQuietly(key, format string, args ...any) {
	msg := strings.ReplaceAll(strings.TrimSpace(fmt.Sprintf(format, args...)), "\r", "")
	msg = strings.ReplaceAll(msg, "\n", " | ")
	now := time.Now()
	quietLog.Lock()
	entry := quietLog.last[key]
	if entry.msg == msg && now.Sub(entry.at) < logRepeatWindow {
		entry.repeats++
		quietLog.last[key] = entry
		quietLog.Unlock()
		return
	}
	quietLog.last[key] = quietEntry{msg: msg, at: now}
	quietLog.Unlock()
	if entry.repeats > 0 {
		log.Printf("%s (previous message repeated %d more times)", msg, entry.repeats)
		return
	}
	log.Print(msg)
}

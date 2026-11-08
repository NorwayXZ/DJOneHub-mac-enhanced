//go:build windows

package main

import (
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var (
	setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	awakeMu                 sync.Mutex
	awakeStop               chan struct{}
)

// setKeepAwake stops idle sleep while on; the display still sleeps. The
// request belongs to a thread, so one locked thread holds it.
func setKeepAwake(on bool) {
	awakeMu.Lock()
	defer awakeMu.Unlock()
	switch {
	case on && awakeStop == nil:
		stop := make(chan struct{})
		awakeStop = stop
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			setThreadExecutionState.Call(esContinuous | esSystemRequired)
			<-stop
			setThreadExecutionState.Call(esContinuous)
		}()
		logKeepAwake(true)
	case !on && awakeStop != nil:
		close(awakeStop)
		awakeStop = nil
		logKeepAwake(false)
	}
}

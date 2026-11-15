//go:build windows

package main

import "golang.org/x/sys/windows"

// supervisorGone reports whether the worker's supervisor has exited. Windows
// keeps reporting the original parent PID after it exits, so the worker
// holds a handle to the supervisor process and checks whether it ended.
func supervisorGone(parent int) func() bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parent))
	if err != nil {
		return func() bool { return true }
	}
	return func() bool {
		ev, _ := windows.WaitForSingleObject(h, 0)
		return ev == windows.WAIT_OBJECT_0
	}
}

//go:build !windows

package main

import "os"

// supervisorGone reports whether the worker's supervisor has exited; a
// Unix orphan is re-parented, so its parent PID changes.
func supervisorGone(parent int) func() bool {
	return func() bool { return os.Getppid() != parent }
}

//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/pwr_mgt/IOPMLib.h>
static IOPMAssertionID djonehubHoldAwake(void) {
	IOPMAssertionID id = 0;
	if (IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleSystemSleep, kIOPMAssertionLevelOn,
		CFSTR("DJOneHub cellular proxy is running"), &id) != kIOReturnSuccess) return 0;
	return id;
}
static void djonehubReleaseAwake(IOPMAssertionID id) { IOPMAssertionRelease(id); }
*/
import "C"

import "sync"

var (
	awakeMu sync.Mutex
	awakeID C.IOPMAssertionID
)

// setKeepAwake stops idle sleep while on; the display still sleeps. A sleeping
// Mac drops every proxy until someone wakes it.
func setKeepAwake(on bool) {
	awakeMu.Lock()
	defer awakeMu.Unlock()
	switch {
	case on && awakeID == 0:
		awakeID = C.djonehubHoldAwake()
		if awakeID != 0 {
			logKeepAwake(true)
		}
	case !on && awakeID != 0:
		C.djonehubReleaseAwake(awakeID)
		awakeID = 0
		logKeepAwake(false)
	}
}

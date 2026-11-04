package main

import (
	"log"
	"runtime"
	"sync/atomic"
	"time"
)

// macUSBNetMode is the usbnet mode that gives macOS a network adapter (ECM).
// Factory modules ship with 0, which leaves the Mac without one, so the proxy
// never comes up although the module itself is online.
const macUSBNetMode = "1"

// usbnetChecked keeps the check to once per process: after the reboot the
// module comes back in a new worker, which checks again and finds it set.
var usbnetChecked atomic.Bool

// ensureMacUSBNet sets usbnet to the Mac mode on a module that lacks it and
// reboots the module so the adapter appears. Called after the AT bridge opens.
func (a *app) ensureMacUSBNet() {
	if runtime.GOOS != "darwin" || a.demo || coordinatorOnly || !usbnetChecked.CompareAndSwap(false, true) {
		return
	}
	go func() {
		// Wait for any running module operation (switch, setup) to finish.
		deadline := time.Now().Add(3 * time.Minute)
		for !a.tryModuleOperation() {
			if time.Now().After(deadline) {
				log.Printf("usbnet check: module busy, skipped")
				return
			}
			time.Sleep(10 * time.Second)
		}
		defer a.moduleOperationMu.Unlock()
		current, err := a.runATCommand(`AT+QCFG="usbnet"`, 5*time.Second)
		mode := parseUSBNetMode(current)
		if err != nil || mode == "" || mode == macUSBNetMode {
			return
		}
		log.Printf("usbnet check: mode %s gives the Mac no network adapter, setting %s", mode, macUSBNetMode)
		if _, err := a.runATCommand(`AT+QCFG="usbnet",`+macUSBNetMode, 8*time.Second); err != nil {
			log.Printf("usbnet check: write failed: %v", err)
			return
		}
		if readBack, err := a.runATCommand(`AT+QCFG="usbnet"`, 5*time.Second); err != nil || parseUSBNetMode(readBack) != macUSBNetMode {
			log.Printf("usbnet check: read-back did not confirm mode %s", macUSBNetMode)
			return
		}
		if _, err := a.runATCommand("AT+CFUN=1,1", 4*time.Second); err != nil {
			log.Printf("usbnet check: reboot failed: %v", err)
			return
		}
		log.Printf("usbnet check: module rebooting to bring up the Mac network adapter")
	}()
}

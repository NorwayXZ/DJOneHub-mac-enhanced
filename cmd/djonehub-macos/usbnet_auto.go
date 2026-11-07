package main

import (
	"log"
	"runtime"
	"sync/atomic"
	"time"
)

// hostUSBNetMode is the usbnet mode that gives this computer a driverless
// network adapter: ECM on macOS, RNDIS on Windows (Windows has no in-box ECM
// driver). Factory modules ship with 0, which leaves the computer without one,
// so the proxy never comes up although the module itself is online.
func hostUSBNetMode() string {
	if runtime.GOOS == "windows" {
		return "3"
	}
	return "1"
}

// usbnetChecked keeps the check to once per process: after the reboot the
// module comes back in a new worker, which checks again and finds it set.
var usbnetChecked atomic.Bool

// ensureHostUSBNet sets usbnet to this computer's mode on a module that lacks it and
// reboots the module so the adapter appears. Called after the AT bridge opens.
func (a *app) ensureHostUSBNet() {
	if (runtime.GOOS != "darwin" && runtime.GOOS != "windows") || a.demo || coordinatorOnly || !usbnetChecked.CompareAndSwap(false, true) {
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
		want := hostUSBNetMode()
		current, err := a.runATCommand(`AT+QCFG="usbnet"`, 5*time.Second)
		mode := parseUSBNetMode(current)
		if err != nil || mode == "" || mode == want {
			return
		}
		log.Printf("usbnet check: mode %s gives this computer no network adapter, setting %s", mode, want)
		if _, err := a.runATCommand(`AT+QCFG="usbnet",`+want, 8*time.Second); err != nil {
			log.Printf("usbnet check: write failed: %v", err)
			return
		}
		if readBack, err := a.runATCommand(`AT+QCFG="usbnet"`, 5*time.Second); err != nil || parseUSBNetMode(readBack) != want {
			log.Printf("usbnet check: read-back did not confirm mode %s", want)
			return
		}
		if _, err := a.runATCommand("AT+CFUN=1,1", 4*time.Second); err != nil {
			log.Printf("usbnet check: reboot failed: %v", err)
			return
		}
		log.Printf("usbnet check: module rebooting to bring up the network adapter")
	}()
}

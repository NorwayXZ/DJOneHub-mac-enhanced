package main

import (
	"log"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// After an eSIM switch the module restarts. Sometimes (seen with a XeSIM card
// in the DJI Baiwang module) it comes back registered with an active data
// context but its USB network link to the Mac stays down, so the proxy waits
// forever. One more module restart fixes it; do that automatically, once.
const (
	switchLinkGrace  = 100 * time.Second
	switchLinkGiveUp = 4 * time.Minute
)

var pdpIPv4 = regexp.MustCompile(`\+CGPADDR:\s*1\s*,\s*"?(\d+\.\d+\.\d+\.\d+)`)

// switchGeneration lets only the latest switch's watch act, so switching
// twice in a row never restarts the module twice.
var switchGeneration atomic.Int64

func (a *app) recoverLinkAfterSwitch() {
	generation := switchGeneration.Add(1)
	started := time.Now()
	for time.Since(started) < switchLinkGiveUp {
		time.Sleep(5 * time.Second)
		if switchGeneration.Load() != generation {
			return
		}
		if _, err := proxyInterface(); err == nil {
			return // the Mac has the cellular link again
		}
		if time.Since(started) < switchLinkGrace {
			continue
		}
		reg, _ := a.runATCommand("AT+CEREG?", 3*time.Second)
		addr, _ := a.runATCommand("AT+CGPADDR=1", 3*time.Second)
		registered := strings.Contains(reg, ",1") || strings.Contains(reg, ",5")
		m := pdpIPv4.FindStringSubmatch(addr)
		if !registered || m == nil || m[1] == "0.0.0.0" {
			continue // still registering or attaching: keep waiting
		}
		if !a.tryModuleOperation() {
			continue
		}
		log.Printf("eSIM switch: module is online but the Mac link stayed down; restarting the module once more")
		_, _ = a.runATCommand("AT+CFUN=1,1", 3*time.Second)
		a.moduleOperationMu.Unlock()
		return
	}
}

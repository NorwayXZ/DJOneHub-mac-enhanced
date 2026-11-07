package main

import (
	"testing"
	"time"
)

func TestRecoveryWaitsWhileSIMSwapsIdentity(t *testing.T) {
	t0 := time.Date(2026, 11, 7, 9, 33, 0, 0, time.Local)
	if !simSettling("515661007365508", t0) {
		t.Fatal("a SIM seen for the first time should get time to search")
	}
	if !simSettling("515661007365508", t0.Add(3*time.Minute)) {
		t.Fatal("still inside the settle time")
	}
	if simSettling("515661007365508", t0.Add(5*time.Minute)) {
		t.Fatal("same IMSI for 5 minutes: recovery should run")
	}
	if !simSettling("204047578365508", t0.Add(6*time.Minute)) {
		t.Fatal("the SIM switched to its roaming identity: wait again")
	}
	if simSettling("", t0) {
		t.Fatal("an unreadable IMSI must not block recovery")
	}
}

func TestRecoveryOnlyWakesAStoppedRadio(t *testing.T) {
	if !operatorDeregistered("AT+COPS?\r\n+COPS: 2\r\nOK") {
		t.Fatal("deregistered radio should be told to search again")
	}
	for _, resp := range []string{"+COPS: 0", "+COPS: 1", "+COPS: 0,0,\"CHINA MOBILE\",7", ""} {
		if operatorDeregistered(resp) {
			t.Fatalf("%q: a searching or manual radio must be left alone", resp)
		}
	}
}

func TestModuleLinkWithSelfAssignedAddressIsDown(t *testing.T) {
	session := func(name string) uint64 {
		return map[string]uint64{"en13": 2, "en15": 1}[name]
	}
	items := []macNetInterface{
		{Name: "en0", Status: "active", IPv4: "192.168.1.5"},
		{Name: "en15", Status: "active", IPv4: "192.168.225.30"},
		{Name: "en13", Status: "active", IPv4: "169.254.6.111"},
	}
	if found, usable := moduleLinkState(items, session, 2); !found || usable {
		t.Fatalf("module 2 with 169.254: found=%v usable=%v", found, usable)
	}
	if found, usable := moduleLinkState(items, session, 1); !found || !usable {
		t.Fatalf("module 1 with a DHCP address: found=%v usable=%v", found, usable)
	}
	items[2].IPv4 = "192.168.225.28"
	if _, usable := moduleLinkState(items, session, 2); !usable {
		t.Fatal("module 2 recovered")
	}
	if found, _ := moduleLinkState(items, session, 3); found {
		t.Fatal("a module without its own interface must not trigger a reboot")
	}
}

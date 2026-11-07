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

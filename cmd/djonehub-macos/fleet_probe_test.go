package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFleetModuleMovedToHubKeepsItsBinding(t *testing.T) {
	devices := []fleetDevice{{Session: "21", Location: 0x03140000}, {Session: "22", Location: 0x03110000}}
	c, created := makeFleet(t, &devices)
	dir := filepath.Dir(c.path)
	// Two modules bound by USB position before IMEIs were recorded, plus the
	// unconfirmed entries an older version made for their new hub ports.
	c.registry.Bindings = []fleetBinding{
		{ID: "slot-03100000", Name: "蜂窝模块 1", Port: 10909, Confirmed: true},
		{ID: "slot-02120000", Name: "蜂窝模块 2", Port: 10910, Confirmed: true},
		{ID: "slot-03140000", Name: "蜂窝模块 3", Port: 10911},
		{ID: "slot-03110000", Name: "蜂窝模块 4", Port: 10912},
	}
	for id, iccid := range map[string]string{"slot-03100000": "89852342022591516017", "slot-02120000": "89852342022584542027"} {
		_ = os.MkdirAll(filepath.Join(dir, "modules", id), 0o700)
		_ = os.WriteFile(filepath.Join(dir, "modules", id, "esim-overview.json"), []byte(`{"profiles":[{"profiles":[{"iccid":"`+iccid+`"}]}]}`), 0o600)
	}
	probe := map[string]fleetProbeResult{
		"21": {IMEI: "860000000000002", ICCID: "89852342022584542027"},
		"22": {IMEI: "860000000000001", ICCID: "89852342022591516017"},
	}
	c.probe = func(d fleetDevice) (fleetProbeResult, error) { return probe[d.Session], nil }
	settle := func() {
		c.reconcile()
		for i := 0; i < 100; i++ {
			c.mu.Lock()
			busy := false
			for _, p := range c.probes {
				busy = busy || p.running
			}
			c.mu.Unlock()
			if !busy {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		c.reconcile()
	}
	settle()
	s := c.snapshot()
	if len(s.Modules) != 2 {
		t.Fatalf("want the 2 original modules, got %+v", s.Modules)
	}
	for _, m := range s.Modules {
		if !m.Connected || m.NeedsConfirmation || m.IMEI == "" {
			t.Fatalf("module not recognised: %+v", m)
		}
	}
	if len(created["slot-03100000"]) != 1 || len(created["slot-02120000"]) != 1 {
		t.Fatalf("workers: %v", created)
	}
	// Swap the two modules between ports: each keeps its own binding.
	devices = []fleetDevice{{Session: "31", Location: 0x03140000}, {Session: "32", Location: 0x03110000}}
	probe["31"], probe["32"] = probe["22"], probe["21"]
	settle()
	s = c.snapshot()
	if len(s.Modules) != 2 || s.Modules[0].IMEI != "860000000000001" || s.Modules[0].Port != 10909 {
		t.Fatalf("after swap: %+v", s.Modules)
	}
	// A new module is trusted at once and gets the next free name and port.
	devices = append(devices, fleetDevice{Session: "33", Location: 0x03120000})
	probe["33"] = fleetProbeResult{IMEI: "860000000000003"}
	settle()
	s = c.snapshot()
	if len(s.Modules) != 3 || s.Modules[2].Name != "蜂窝模块 3" || s.Modules[2].Port != 10911 || s.Modules[2].NeedsConfirmation {
		t.Fatalf("new module: %+v", s.Modules)
	}
}

func TestParseIMEI(t *testing.T) {
	if got := parseIMEI("AT+GSN\r\n867400000000001\r\nOK"); got != "867400000000001" {
		t.Fatal(got)
	}
	if got := parseIMEI("AT+GSN\r\nERROR"); got != "" {
		t.Fatal(got)
	}
}

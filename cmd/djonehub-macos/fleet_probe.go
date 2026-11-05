package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// These modules have no USB serial number, so a USB position alone cannot
// tell two modules apart: moving one to a hub port made it look like a new
// module that needed confirming. Before a module's worker starts, the
// supervisor reads its IMEI in a short-lived child process (the worker will
// own the USB AT interface afterwards) and finds the module by that.
type fleetProbeResult struct {
	IMEI  string `json:"imei"`
	ICCID string `json:"iccid,omitempty"`
}

type fleetProbeFunc func(fleetDevice) (fleetProbeResult, error)

type fleetProbeState struct {
	result  fleetProbeResult
	done    bool
	running bool
	fails   int
	next    time.Time
}

// After this many failed reads the module falls back to its USB position.
const fleetProbeMaxFails = 4

func fleetProber() fleetProbeFunc {
	return func(d fleetDevice) (fleetProbeResult, error) {
		executable, err := os.Executable()
		if err != nil {
			return fleetProbeResult{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, executable, "--probe-module-session", d.Session).Output()
		if err != nil {
			return fleetProbeResult{}, fmt.Errorf("read IMEI: %w", err)
		}
		var r fleetProbeResult
		if err := json.Unmarshal(out, &r); err != nil || r.IMEI == "" {
			return fleetProbeResult{}, errors.New("read IMEI: no answer")
		}
		return r, nil
	}
}

// runModuleProbe is the child side: open this USB session's AT interface,
// print the IMEI and ICCID, and let go of the module.
func runModuleProbe() error {
	dev, err := openDJIUSBAT()
	if err != nil {
		return err
	}
	defer dev.Close()
	var r fleetProbeResult
	for attempt := 0; attempt < 5 && r.IMEI == ""; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		resp, err := dev.Command("AT+GSN", 3*time.Second)
		if err == nil {
			r.IMEI = parseIMEI(resp)
		}
	}
	if r.IMEI == "" {
		return errors.New("module did not report an IMEI")
	}
	if resp, err := dev.Command("AT+QCCID", 3*time.Second); err == nil {
		r.ICCID = normICCID(parseUSBATPrefixed(resp, "+QCCID:"))
	}
	return json.NewEncoder(os.Stdout).Encode(r)
}

func parseIMEI(resp string) string {
	for _, line := range splitATLines(resp) {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "+GSN:"), "+CGSN:"))
		line = strings.Trim(line, `" `)
		if len(line) >= 14 && len(line) <= 17 && strings.Trim(line, "0123456789") == "" && strings.Trim(line, "0") != "" {
			return line
		}
	}
	return ""
}

// savedICCIDs: the numbers a module's saved eSIM card list holds, to find a
// module that was bound by USB position before IMEIs were recorded.
func savedICCIDs(stateDir, id string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(stateDir + "/modules/" + id + "/esim-overview.json")
	if err != nil {
		return out
	}
	var overview struct {
		Profiles []struct {
			Profiles []struct {
				ICCID string `json:"iccid"`
			} `json:"profiles"`
		} `json:"profiles"`
	}
	if json.Unmarshal(data, &overview) != nil {
		return out
	}
	for _, g := range overview.Profiles {
		for _, p := range g.Profiles {
			if p.ICCID != "" {
				out[normICCID(p.ICCID)] = true
			}
		}
	}
	return out
}

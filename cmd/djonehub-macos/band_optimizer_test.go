package main

import (
	"reflect"
	"testing"
)

func TestParseBandRadio(t *testing.T) {
	r := parseBandRadio(`+QNWINFO: "TDD LTE","46000","LTE BAND 41",40936`, `+QCSQ: "LTE",60,-92,144,-9`)
	if !r.Valid || r.Band != 41 || r.PLMN != "46000" || r.RSRP != -92 || r.SINR != 8.8 || r.RSRQ != -9 {
		t.Fatalf("radio: %+v", r)
	}
}

func TestBandMasks(t *testing.T) {
	if got := bandMaskFor(41); got != "10000000000" {
		t.Fatalf("B41 mask %s", got)
	}
	if got := bandMaskFor(34, 38, 39, 40, 41); got != "1E200000000" {
		t.Fatalf("TDD mask %s", got)
	}
	if got := parseLTEBandMask(`+QCFG: "band",0xbff,0x42000001e20b0e18df,0x0`); got != defaultFullBandMask {
		t.Fatalf("parsed mask %s", got)
	}
	want := []int{1, 2, 3, 4, 5, 7, 8, 12, 13, 18, 19, 20, 25, 26, 28, 34, 38, 39, 40, 41, 66, 71}
	if got := bandsInMask(defaultFullBandMask); !reflect.DeepEqual(got, want) {
		t.Fatalf("bands %v", got)
	}
}

func TestCandidateBandsUseOperatorAndNeighbours(t *testing.T) {
	supported := bandsInMask(defaultFullBandMask)
	if got := candidateBands("46000", "", supported); !reflect.DeepEqual(got, []int{3, 8, 34, 39, 40, 41}) {
		t.Fatalf("China Mobile: %v", got)
	}
	// Unicom plus a Band 40 neighbour seen by the module.
	if got := candidateBands("46001", `+QENG: "neighbourcell inter","LTE",38950,-,-`, supported); !reflect.DeepEqual(got, []int{1, 3, 8, 40, 41}) {
		t.Fatalf("Unicom: %v", got)
	}
	if got := candidateBands("46000", "", []int{3, 41}); !reflect.DeepEqual(got, []int{3, 41}) {
		t.Fatalf("limited module: %v", got)
	}
}

func TestChooseBandPrefersStability(t *testing.T) {
	results := []bandResult{
		{Band: 0, Status: "ok", SINR: -1, RSRP: -80, SpeedMBps: 0.8},
		{Band: 39, Status: "ok", SINR: 3, RSRP: -90, SpeedMBps: 0.6},
		{Band: 41, Status: "ok", SINR: 8.8, RSRP: -91, SpeedMBps: 3.2},
		{Band: 40, Status: "no_service"},
	}
	if got := chooseBand(results, false); got != 41 {
		t.Fatalf("speed choice %d", got)
	}
	if got := chooseBand(results, true); got != 41 {
		t.Fatalf("signal choice %d", got)
	}
	results[2].SpeedMBps = 0.9 // not clearly faster than all bands
	if got := chooseBand(results, false); got != 0 {
		t.Fatalf("marginal gain should keep all bands, got %d", got)
	}
	for i := range results {
		results[i].SpeedMBps = 0 // every speed test failed
	}
	if got := chooseBand(results, false); got != 41 {
		t.Fatalf("failed speed tests should fall back to signal, got %d", got)
	}
}

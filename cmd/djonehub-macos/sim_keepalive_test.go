package main

import (
	"testing"
	"time"
)

func TestESIMKeepAliveRunsOnlyAtNight(t *testing.T) {
	day := time.Date(2026, 11, 4, 0, 0, 0, 0, time.Local)
	for hour, want := range map[int]bool{2: false, 3: true, 5: true, 6: false, 14: false} {
		if got := inQuietHours(day.Add(time.Duration(hour) * time.Hour)); got != want {
			t.Errorf("%d:00 quiet=%v, want %v", hour, got, want)
		}
	}
}

func TestPaddedICCIDMatchesTheESIMProfile(t *testing.T) {
	if normICCID("8932042000009150901F") != "8932042000009150901" || normICCID("89012804332292337510") != "89012804332292337510" {
		t.Fatal("ICCID padding not dropped")
	}
}

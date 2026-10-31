package main

import "testing"

func TestUSBATProbeExcludesDiagnosticNetworkADBAndAudio(t *testing.T) {
	for _, tt := range []struct {
		n, c, s, p int
		want       bool
	}{
		{0, 255, 255, 255, false}, {1, 255, 0, 0, false}, {2, 255, 0, 0, true}, {3, 255, 0, 0, true},
		{4, 2, 6, 0, false}, {5, 10, 0, 0, false}, {6, 255, 66, 1, false}, {6, 1, 1, 0, false},
		{4, 255, 0, 0, true}, {5, 255, 0, 0, true},
	} {
		if got := isUSBATInterface(tt.n, tt.c, tt.s, tt.p); got != tt.want {
			t.Fatalf("interface %d: got %v", tt.n, got)
		}
	}
}

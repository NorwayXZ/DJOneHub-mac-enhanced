//go:build darwin && cgo

package main

import "testing"

func TestADBTargetID(t *testing.T) {
	raw := "(no serial number)\tdevice usb:34734080X transport_id:6\nother\tdevice usb:123X transport_id:7\n"
	id, err := adbTargetID(raw, "34734080X")
	if err != nil || id != "6" {
		t.Fatalf("%q %v", id, err)
	}
	if got, e := adbTargetID("(no serial number)     device usb:34734080X transport_id:6\n", "34734080X"); e != nil || got != "6" {
		t.Fatal(got, e)
	}
	for _, bad := range []string{raw + raw, "a\toffline usb:34734080X transport_id:6", "a\tdevice usb:34734080X transport_id:bad", "a\tdevice usb:34734080 transport_id:6"} {
		if _, err := adbTargetID(bad, "34734080X"); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

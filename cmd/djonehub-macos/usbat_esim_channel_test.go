package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A module like the DJI Baiwang QDC507: AT+CCHO and AT+CGLA answer ERROR,
// AT+CSIM works. The card replies 61xx and needs GET RESPONSE.
func fakeCSIMModule(t *testing.T, sent *[]string) func(string, time.Duration) (string, error) {
	return func(cmd string, _ time.Duration) (string, error) {
		*sent = append(*sent, cmd)
		switch {
		case strings.HasPrefix(cmd, "AT+CCHO"), strings.HasPrefix(cmd, "AT+CGLA"), strings.HasPrefix(cmd, "AT+CCHC"):
			return cmd + "\r\nERROR", nil
		case cmd == `AT+CSIM=10,"0070000001"`:
			return `+CSIM: 6,"029000"` + "\r\nOK", nil
		case strings.HasPrefix(cmd, `AT+CSIM=42,"02A4040010A0000005591010FFFFFFFF8900000177"`):
			return `+CSIM: 4,"6121"` + "\r\nOK", nil
		case strings.HasPrefix(cmd, `AT+CSIM=42,"02A4040010`):
			return `+CSIM: 4,"6A82"` + "\r\nOK", nil
		case cmd == `AT+CSIM=10,"02C0000021"`:
			return `+CSIM: 4,"9000"` + "\r\nOK", nil
		case cmd == `AT+CSIM=16,"82E2910003BF2000"`:
			return `+CSIM: 4,"6103"` + "\r\nOK", nil
		case cmd == `AT+CSIM=10,"02C0000003"`:
			return `+CSIM: 10,"BF20019000"` + "\r\nOK", nil
		case cmd == `AT+CSIM=10,"0070800200"`:
			return `+CSIM: 4,"9000"` + "\r\nOK", nil
		}
		t.Fatalf("unexpected command %s", cmd)
		return "", nil
	}
}

func TestESIMChannelFallsBackToCSIM(t *testing.T) {
	var sent []string
	c := newUSBATESIMChannel(fakeCSIMModule(t, &sent))
	xesim := []byte{0xA0, 0x00, 0x00, 0x05, 0x59, 0x10, 0x10, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x00, 0x00, 0x01, 0x77}
	ch, err := c.OpenLogicalChannel(xesim)
	if err != nil || ch != 2 {
		t.Fatal(ch, err)
	}
	resp, err := c.Transmit([]byte{0x80, 0xE2, 0x91, 0x00, 0x03, 0xBF, 0x20, 0x00})
	if err != nil || !bytes.Equal(resp, []byte{0xBF, 0x20, 0x01, 0x90, 0x00}) {
		t.Fatalf("%X %v", resp, err)
	}
	if err := c.CloseLogicalChannel(ch); err != nil {
		t.Fatal(err)
	}
}

func TestESIMChannelReportsAPlainSIMLikeBefore(t *testing.T) {
	var sent []string
	c := newUSBATESIMChannel(fakeCSIMModule(t, &sent))
	gsma := []byte{0xA0, 0x00, 0x00, 0x05, 0x59, 0x10, 0x10, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x00, 0x00, 0x01, 0x00}
	_, err := c.OpenLogicalChannel(gsma)
	if err == nil || !strings.Contains(err.Error(), "AT+CCHO ERROR") {
		t.Fatal(err)
	}
	// The channel opened for the failed SELECT was closed again.
	if sent[len(sent)-1] != `AT+CSIM=10,"0070800200"` {
		t.Fatal(sent)
	}
}

func TestChannelCLA(t *testing.T) {
	for _, tt := range []struct{ cla, ch, want byte }{{0x80, 2, 0x82}, {0x00, 1, 0x01}, {0x80, 4, 0xC0}, {0x00, 19, 0x4F}} {
		if got := channelCLA(tt.cla, tt.ch); got != tt.want {
			t.Fatalf("%02X ch %d: %02X", tt.cla, tt.ch, got)
		}
	}
}

//go:build darwin && cgo

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Reuse an already running local ADB server, without killing it or claiming
// other devices. Pin every connection to a transport ID and its USB location.
type adbServerTarget struct {
	id, usb string
	session uint64
}

func adbServerRequest(c net.Conn, service string) error {
	if _, err := fmt.Fprintf(c, "%04x%s", len(service), service); err != nil {
		return err
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(c, b); err != nil {
		return err
	}
	if string(b) == "OKAY" {
		return nil
	}
	if string(b) != "FAIL" {
		return fmt.Errorf("invalid ADB server status")
	}
	msg, err := adbServerRead(c)
	if err != nil {
		return err
	}
	return fmt.Errorf("ADB server: %s", msg)
}
func adbServerRead(c net.Conn) (string, error) {
	b := make([]byte, 4)
	if _, e := io.ReadFull(c, b); e != nil {
		return "", e
	}
	n, e := strconv.ParseUint(string(b), 16, 16)
	if e != nil {
		return "", e
	}
	b = make([]byte, n)
	_, e = io.ReadFull(c, b)
	return string(b), e
}
func adbServerConnect(timeout time.Duration) (net.Conn, error) {
	c, e := net.DialTimeout("tcp4", "127.0.0.1:5037", time.Second)
	if e == nil {
		c.SetDeadline(time.Now().Add(timeout))
	}
	return c, e
}
func adbServerDevices() (string, error) {
	c, e := adbServerConnect(3 * time.Second)
	if e != nil {
		return "", e
	}
	defer c.Close()
	if e = adbServerRequest(c, "host:devices-l"); e != nil {
		return "", e
	}
	return adbServerRead(c)
}
func adbTargetID(raw, usb string) (string, error) {
	id := ""
	for _, line := range strings.Split(raw, "\n") {
		// Serial numbers can contain spaces; only parse the tab-delimited state.
		// ADB versions use either a tab or padded spaces after the serial.
		_, rest, ok := strings.Cut(line, "\t")
		if !ok {
			for _, state := range []string{"device", "offline", "unauthorized"} {
				if at := strings.Index(line, "     "+state+" "); at >= 0 {
					rest = strings.TrimSpace(line[at:])
					ok = true
					break
				}
			}
		}
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || fields[0] != "device" {
			continue
		}
		match := false
		candidate := ""
		for _, f := range fields[1:] {
			if f == "usb:"+usb {
				match = true
			}
			if strings.HasPrefix(f, "transport_id:") {
				candidate = strings.TrimPrefix(f, "transport_id:")
			}
		}
		if match {
			if id != "" {
				return "", fmt.Errorf("ambiguous USB ADB target")
			}
			if n, e := strconv.ParseUint(candidate, 10, 64); e != nil || n == 0 {
				return "", fmt.Errorf("invalid ADB transport ID")
			}
			id = candidate
		}
	}
	if id == "" {
		return "", fmt.Errorf("no matching USB ADB transport")
	}
	return id, nil
}
func openADBServerTarget(session uint64) (*adbServerTarget, error) {
	devices, e := discoverFleetDevices()
	if e != nil {
		return nil, e
	}
	usb := ""
	for _, d := range devices {
		if d.Session == strconv.FormatUint(session, 10) {
			usb = strconv.FormatUint(uint64(d.Location), 10) + "X"
		}
	}
	if usb == "" {
		return nil, fmt.Errorf("USB session not found")
	}
	raw, e := adbServerDevices()
	if e != nil {
		return nil, e
	}
	id, e := adbTargetID(raw, usb)
	if e != nil {
		return nil, e
	}
	return &adbServerTarget{id: id, usb: usb, session: session}, nil
}
func (t *adbServerTarget) service(name string, timeout time.Duration) (net.Conn, error) {
	current, e := openADBServerTarget(t.session)
	if e != nil {
		return nil, e
	}
	if current.id != t.id || current.usb != t.usb {
		return nil, fmt.Errorf("ADB target changed; reopen required")
	}
	c, e := adbServerConnect(timeout)
	if e != nil {
		return nil, e
	}
	if e = adbServerRequest(c, "host:transport-id:"+t.id); e == nil {
		e = adbServerRequest(c, name)
	}
	if e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}
func (t *adbServerTarget) shellChecked(command string, timeout time.Duration) (string, int, error) {
	token := adbToken()
	wrapped := "{ " + command + "; }; __mavo_status=$?; printf '\\n__MAVO_STATUS_" + token + "_%u__\\n' \"$__mavo_status\""
	c, e := t.service("shell:"+wrapped, timeout)
	if e != nil {
		return "", 0, e
	}
	defer c.Close()
	b, e := io.ReadAll(io.LimitReader(c, 2<<20))
	if e != nil {
		return string(b), 0, e
	}
	status, ok := parseADBStatus(string(b), token)
	if !ok {
		return string(b), 0, fmt.Errorf("module shell exit status missing")
	}
	return string(b), status, nil
}
func (t *adbServerTarget) push(data []byte, path string, mode uint32, timeout time.Duration) error {
	if strings.ContainsAny(path, ",\x00") {
		return fmt.Errorf("invalid push path")
	}
	c, e := t.service("sync:", timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	write := func(tag string, payload []byte) error {
		h := make([]byte, 8)
		copy(h, tag)
		binary.LittleEndian.PutUint32(h[4:], uint32(len(payload)))
		if _, e := c.Write(h); e != nil {
			return e
		}
		_, e := c.Write(payload)
		return e
	}
	if e = write("SEND", []byte(fmt.Sprintf("%s,%d", path, mode))); e != nil {
		return e
	}
	for off := 0; off < len(data); off += 32768 {
		end := off + 32768
		if end > len(data) {
			end = len(data)
		}
		if e = write("DATA", data[off:end]); e != nil {
			return e
		}
	}
	h := make([]byte, 8)
	copy(h, "DONE")
	binary.LittleEndian.PutUint32(h[4:], uint32(time.Now().Unix()))
	if _, e = c.Write(h); e != nil {
		return e
	}
	if _, e = io.ReadFull(c, h); e != nil {
		return e
	}
	if string(h[:4]) != "OKAY" {
		return fmt.Errorf("ADB sync push failed")
	}
	return nil
}

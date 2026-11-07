package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSOCKSRejectsUnsupportedRequests(t *testing.T) {
	for _, tt := range []struct {
		name    string
		request []byte
		want    byte
	}{
		{"udp", []byte{5, 3, 0, 1}, 7},
		{"unknown address type", []byte{5, 1, 0, 9}, 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			p := &cellularProxy{}
			go func() { defer server.Close(); p.handle(context.Background(), server, "not-a-real-interface") }()
			client.SetDeadline(time.Now().Add(time.Second))
			client.Write([]byte{5, 1, 0})
			b := make([]byte, 2)
			if _, err := io.ReadFull(client, b); err != nil || !bytes.Equal(b, []byte{5, 0}) {
				t.Fatal(b, err)
			}
			client.Write(tt.request)
			reply := make([]byte, 10)
			if _, err := io.ReadFull(client, reply); err != nil || reply[1] != tt.want {
				t.Fatal(reply, err)
			}
		})
	}
}
func TestSOCKSNoAuthMethod(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	p := &cellularProxy{}
	go func() { defer server.Close(); p.handle(context.Background(), server, "") }()
	client.SetDeadline(time.Now().Add(time.Second))
	client.Write([]byte{5, 1, 2})
	b := make([]byte, 2)
	io.ReadFull(client, b)
	if !bytes.Equal(b, []byte{5, 255}) {
		t.Fatal(b)
	}
}
func TestCellularDialRejectsNonPublicTargets(t *testing.T) {
	for _, address := range []string{"127.0.0.1:80", "192.168.225.1:80", "169.254.1.1:80", "[::1]:80", "224.0.0.1:80"} {
		if c, err := cellularDial(context.Background(), "invalid", address); err == nil {
			c.Close()
			t.Fatalf("accepted %s", address)
		}
	}
}
func TestProxyStopClosesClients(t *testing.T) {
	c, s := net.Pipe()
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	p := cellularProxy{clients: map[net.Conn]bool{c: true}, cancel: cancel}
	p.stop()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("dial context not canceled")
	}
	if _, err := s.Write([]byte{1}); err == nil {
		t.Fatal("client still open")
	}
	p.stop()
}
func TestProxyRadioRequiresConfirmationAndExclusiveAccess(t *testing.T) {
	a := newDemoApp()
	request := func(body string) int {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7575/api/proxy", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		a.proxyActionAPI(w, r)
		return w.Code
	}
	if code := request(`{"action":"reconnect"}`); code != 400 {
		t.Fatal(code)
	}
	a.moduleOperationMu.Lock()
	if code := request(`{"action":"start"}`); code != 409 {
		t.Fatal(code)
	}
	a.moduleOperationMu.Unlock()
	if code := request(`{"action":"unknown"}`); code != 400 {
		t.Fatal(code)
	}
}
func TestAPIRejectsCrossSiteAndRebinding(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, tt := range []struct {
		url, origin string
		code        int
	}{
		{"http://127.0.0.1:7575/api/proxy", "https://evil.example", 403},
		{"http://evil.example/api/proxy", "", 403},
		{"http://127.0.0.1:7575/api/proxy", "", 204},
		{"http://127.0.0.1:7575/api/proxy", "http://127.0.0.1:7575", 204},
		// The change-IP link works through a tunnel's own host name.
		{"http://abc.tunnel.example/api/proxy/rotate", "", 204},
		{"http://abc.tunnel.example/api/modules/slot-03100000/proxy/rotate", "", 204},
		{"http://abc.tunnel.example/api/modules/x/y/proxy/rotate", "", 403},
		{"http://abc.tunnel.example/api/proxy/rotate", "https://evil.example", 403},
	} {
		r := httptest.NewRequest("POST", tt.url, nil)
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tt.code {
			t.Fatal(tt, w.Code)
		}
	}
}

func TestSOCKSConnectRelayAndCounters(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()
	client, server := net.Pipe()
	defer client.Close()
	p := &cellularProxy{}
	go func() {
		defer server.Close()
		p.handleWithDial(context.Background(), server, "test", func(ctx context.Context, iface, target string) (net.Conn, error) {
			if iface != "test" || target != "example.com:443" {
				return nil, fmt.Errorf("unexpected destination %s %s", iface, target)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp4", l.Addr().String())
		})
	}()
	client.SetDeadline(time.Now().Add(2 * time.Second))
	client.Write([]byte{5, 1, 0})
	b := make([]byte, 2)
	if _, err := io.ReadFull(client, b); err != nil {
		t.Fatal(err)
	}
	req := append([]byte{5, 1, 0, 3, 11}, []byte("example.com")...)
	req = append(req, 1, 187)
	client.Write(req)
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil || reply[1] != 0 {
		t.Fatal(reply, err)
	}
	client.Write([]byte("hello"))
	echo := make([]byte, 5)
	if _, err := io.ReadFull(client, echo); err != nil || string(echo) != "hello" {
		t.Fatal(string(echo), err)
	}
	deadline := time.Now().Add(time.Second)
	for (p.uploaded.Load() != 5 || p.downloaded.Load() != 5) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.uploaded.Load() != 5 || p.downloaded.Load() != 5 {
		t.Fatal("incorrect counters", p.snapshot())
	}
}

func TestUpdateLeaseBlocksModuleOperations(t *testing.T) {
	a := newDemoApp()
	w := httptest.NewRecorder()
	a.prepareUpdateAPI(w, httptest.NewRequest("POST", "/api/update/prepare", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if a.tryModuleOperation() {
		a.moduleOperationMu.Unlock()
		t.Fatal("operation allowed while update pending")
	}
	a.updatePending.Store(false)
	if !a.tryModuleOperation() {
		t.Fatal("operation still locked")
	}
	a.moduleOperationMu.Unlock()
}

func TestDecodeProxyLocation(t *testing.T) {
	for _, tt := range []struct {
		name, body, location, isp string
		fail                      bool
	}{
		{"success", `{"success":true,"ip":"8.8.8.8","country":"US","region":"California","city":"San Jose","connection":{"isp":"Google"}}`, "US · California · San Jose", "Google", false},
		{"duplicate region", `{"success":true,"ip":"8.8.8.8","country":"Singapore","region":"Singapore","city":"Singapore"}`, "Singapore", "", false},
		{"mismatched ip", `{"success":true,"ip":"1.1.1.1"}`, "", "", true},
		{"provider failure", `{"success":false,"ip":"8.8.8.8"}`, "", "", true},
		{"bad json", `{`, "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			location, isp, err := decodeProxyLocation(bytes.NewBufferString(tt.body), "8.8.8.8")
			if (err != nil) != tt.fail || location != tt.location || isp != tt.isp {
				t.Fatalf("got %q %q %v", location, isp, err)
			}
		})
	}
}

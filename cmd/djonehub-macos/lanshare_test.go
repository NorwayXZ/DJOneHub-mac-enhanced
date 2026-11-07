package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func useLANShare(t *testing.T, settings lanShareSettings) {
	t.Helper()
	lanShare.mu.Lock()
	saved := lanShare.path
	lanShare.path, lanShare.loaded, lanShare.modTime = filepath.Join(t.TempDir(), lanShareFile), true, time.Time{}
	lanShare.mu.Unlock()
	t.Cleanup(func() {
		lanShare.mu.Lock()
		lanShare.path, lanShare.loaded, lanShare.modTime, lanShare.settings = saved, saved != "", time.Time{}, lanShareSettings{}
		lanShare.mu.Unlock()
	})
	if err := lanShare.set(settings); err != nil {
		t.Fatal(err)
	}
}

// lanConn makes an in-process pipe look like a peer on the LAN.
type lanConn struct{ net.Conn }

func (lanConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(192, 168, 1, 20), Port: 50000} }

// socksGreeting runs a handshake from a LAN peer and returns what the proxy
// answered and whether it went on to dial.
func socksGreeting(t *testing.T, client []byte) ([]byte, bool) {
	t.Helper()
	a, b := net.Pipe()
	dialed := make(chan bool, 1)
	go func() {
		var p cellularProxy
		p.handleWithDial(context.Background(), lanConn{b}, "test", func(context.Context, string, string) (net.Conn, error) {
			dialed <- true
			return nil, errors.New("stop")
		})
		b.Close()
	}()
	go func() { _, _ = a.Write(client) }()
	reply, _ := io.ReadAll(a)
	select {
	case <-dialed:
		return reply, true
	default:
		return reply, false
	}
}

func TestLANClientMustLogIn(t *testing.T) {
	useLANShare(t, lanShareSettings{Enabled: true, Username: "u", Password: "secret"})
	connect := []byte{5, 1, 0, 1, 1, 2, 3, 4, 0, 80}
	login := func(user, pass string) []byte {
		b := []byte{5, 1, 2, 1, byte(len(user))}
		b = append(b, user...)
		b = append(b, byte(len(pass)))
		return append(b, pass...)
	}
	if reply, dialed := socksGreeting(t, []byte{5, 1, 0}); dialed || string(reply) != string([]byte{5, 255}) {
		t.Fatalf("no-auth LAN client: %v dialed=%v", reply, dialed)
	}
	if reply, dialed := socksGreeting(t, login("u", "wrong")); dialed || string(reply) != string([]byte{5, 2, 1, 1}) {
		t.Fatalf("wrong password: %v dialed=%v", reply, dialed)
	}
	if _, dialed := socksGreeting(t, append(login("u", "secret"), connect...)); !dialed {
		t.Fatal("right password did not reach the dial")
	}
	useLANShare(t, lanShareSettings{Enabled: false, Username: "u", Password: "secret"})
	if reply, dialed := socksGreeting(t, append(login("u", "secret"), connect...)); dialed || string(reply) != string([]byte{5, 255}) {
		t.Fatalf("login accepted while sharing is off: %v", reply)
	}
}

func TestLANBindFollowsTheSwitch(t *testing.T) {
	useLANShare(t, lanShareSettings{})
	if got := lanBindAddress("127.0.0.1:10909"); got != "127.0.0.1:10909" {
		t.Fatal(got)
	}
	useLANShare(t, lanShareSettings{Enabled: true, Username: "u", Password: "p"})
	if got := lanBindAddress("127.0.0.1:10909"); got != "0.0.0.0:10909" {
		t.Fatal(got)
	}
}

func TestLANControlOnlyRotatesWithLogin(t *testing.T) {
	useLANShare(t, lanShareSettings{Enabled: true, Username: "u", Password: "p w"})
	var got string
	h := lanRotateHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RequestURI()
		w.WriteHeader(204)
	}))
	for _, tt := range []struct {
		url  string
		code int
	}{
		{"http://192.168.1.5:7578/api/proxy", 404},
		{"http://192.168.1.5:7578/api/proxy/rotate", 401},
		{"http://192.168.1.5:7578/api/proxy/rotate?user=u&pass=bad", 401},
		{"http://192.168.1.5:7578/api/modules/slot-1/proxy/rotate?user=u&pass=p+w&wait=5", 204},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tt.url, nil))
		if w.Code != tt.code {
			t.Fatal(tt.url, w.Code)
		}
	}
	if got != "/api/modules/slot-1/proxy/rotate?wait=5" {
		t.Fatal("login not stripped:", got)
	}
	r := httptest.NewRequest("POST", "http://192.168.1.5:7578/api/proxy/rotate", nil)
	r.SetBasicAuth("u", "p w")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("basic auth", w.Code)
	}
}

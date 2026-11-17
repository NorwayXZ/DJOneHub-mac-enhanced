package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func useShareAccounts(t *testing.T, accounts ...shareAccount) {
	t.Helper()
	saved := fleetStateRoot
	fleetStateRoot = t.TempDir()
	shareAccounts.Lock()
	shareAccounts.modTime, shareAccounts.list = time.Time{}, nil
	shareAccounts.Unlock()
	shareMeter.Lock()
	shareMeter.pending, shareMeter.used, shareMeter.blocked = nil, nil, nil
	shareMeter.Unlock()
	t.Cleanup(func() { fleetStateRoot = saved })
	if err := saveShareAccounts(accounts); err != nil {
		t.Fatal(err)
	}
}

func socksLoginBytes(user, pass string) []byte {
	b := []byte{5, 1, 2, 1, byte(len(user))}
	b = append(b, user...)
	b = append(b, byte(len(pass)))
	return append(b, pass...)
}

// shareSession runs one client on the share port (from loopback, as through
// a tunnel) against an upstream that echoes. It returns the proxy's replies
// and whether it dialed.
func shareSession(t *testing.T, client []byte, payload []byte) ([]byte, bool) {
	t.Helper()
	a, b := net.Pipe()
	dialed := make(chan bool, 1)
	go func() {
		var p cellularProxy
		p.handleConn(context.Background(), b, "test", func(context.Context, string, string) (net.Conn, error) {
			dialed <- true
			up, down := net.Pipe()
			go func() { io.Copy(down, down); down.Close() }()
			return tcpLike{up}, nil
		}, true)
		b.Close()
	}()
	go func() {
		a.Write(client)
		time.Sleep(50 * time.Millisecond)
		if payload != nil {
			a.Write(payload)
			time.Sleep(100 * time.Millisecond)
		}
		a.Close()
	}()
	reply, _ := io.ReadAll(a)
	select {
	case <-dialed:
		return reply, true
	default:
		return reply, false
	}
}

// tcpLike lets an in-process pipe stand in for the upstream TCP connection.
type tcpLike struct{ net.Conn }

func (tcpLike) CloseWrite() error { return nil }

func TestSharePortAlwaysNeedsFriendLogin(t *testing.T) {
	useShareAccounts(t, shareAccount{ID: "f1", Username: "amy", Password: "pw", Enabled: true})
	useLANShare(t, lanShareSettings{Enabled: true, Username: "owner", Password: "ownerpw"})
	connect := []byte{5, 1, 0, 1, 1, 2, 3, 4, 0, 80}
	// No login from 127.0.0.1 (a tunnel) is refused on the share port.
	if reply, dialed := shareSession(t, []byte{5, 1, 0}, nil); dialed || !bytes.Equal(reply, []byte{5, 255}) {
		t.Fatalf("no-auth on share port: %v dialed=%v", reply, dialed)
	}
	// The LAN sharing login is not a friend login.
	if _, dialed := shareSession(t, append(socksLoginBytes("owner", "ownerpw"), connect...), nil); dialed {
		t.Fatal("LAN login accepted on share port")
	}
	if _, dialed := shareSession(t, append(socksLoginBytes("amy", "pw"), connect...), nil); !dialed {
		t.Fatal("friend login refused")
	}
}

func TestShareQuotaCountsAndCutsOff(t *testing.T) {
	useShareAccounts(t, shareAccount{ID: "f1", Username: "amy", Password: "pw", Enabled: true, QuotaBytes: 4000})
	connect := []byte{5, 1, 0, 1, 1, 2, 3, 4, 0, 80}
	_, dialed := shareSession(t, append(socksLoginBytes("amy", "pw"), connect...), bytes.Repeat([]byte("x"), 3000))
	if !dialed {
		t.Fatal("friend login refused")
	}
	shareMeter.Lock()
	counted := shareMeter.pending["f1"]
	shareMeter.Unlock()
	// 3000 up + 3000 echoed down, plus the SOCKS reply.
	if counted < 6000 {
		t.Fatalf("counted %d bytes, want >= 6000", counted)
	}
	flushShareMeter()
	if u := readShareUsage()["f1"].Used; u < 6000 {
		t.Fatalf("flushed usage %d", u)
	}
	if !shareBlockFlag("f1").Load() {
		t.Fatal("over-quota account not blocked")
	}
	if _, dialed := shareSession(t, append(socksLoginBytes("amy", "pw"), connect...), nil); dialed {
		t.Fatal("over-quota friend could still connect")
	}
	// Owner adds traffic by resetting usage.
	if err := updateShareUsage(func(u map[string]shareUsage) { u["f1"] = shareUsage{} }); err != nil {
		t.Fatal(err)
	}
	flushShareMeter()
	if _, dialed := shareSession(t, append(socksLoginBytes("amy", "pw"), connect...), nil); !dialed {
		t.Fatal("friend still blocked after reset")
	}
}

func TestShareAccountRules(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)
	if ok, _ := shareAllowed(shareAccount{Enabled: true, Expires: "2026-11-16"}, "", now); ok {
		t.Fatal("expired account allowed")
	}
	if ok, _ := shareAllowed(shareAccount{Enabled: true, Expires: "2026-11-17"}, "", now); !ok {
		t.Fatal("account refused on its last day")
	}
	if ok, _ := shareAllowed(shareAccount{Enabled: true, Modules: []string{"m1"}}, "m2", now); ok {
		t.Fatal("other module allowed")
	}
	if got := periodStart(20, now); !got.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)) {
		t.Fatal(got)
	}
	if got := nextReset(5, now); !got.Equal(time.Date(2026, 12, 5, 0, 0, 0, 0, time.Local)) {
		t.Fatal(got)
	}
	a := shareAccount{ResetDay: 5}
	old := shareUsage{Used: 999, PeriodStart: time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local)}
	if u := currentUsage(a, old, now); u.Used != 0 {
		t.Fatal("monthly reset not applied", u)
	}
}

func TestSharePageNeedsLogin(t *testing.T) {
	useShareAccounts(t, shareAccount{ID: "f1", Name: "Amy", Username: "amy", Password: "pw", Enabled: true, QuotaBytes: 10 << 30})
	rec := httptest.NewRecorder()
	sharePageHandler(rec, httptest.NewRequest("GET", "/share/api", nil))
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	req := httptest.NewRequest("GET", "/share/api", nil)
	req.SetBasicAuth("amy", "pw")
	rec = httptest.NewRecorder()
	sharePageHandler(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"quota":10737418240`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	sharePageHandler(rec, httptest.NewRequest("GET", "/share", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "我的代理流量") {
		t.Fatal("share page html")
	}
}

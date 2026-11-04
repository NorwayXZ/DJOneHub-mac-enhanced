package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProxyLinksUseTheRequestAddress(t *testing.T) {
	a := newDemoApp()
	w := httptest.NewRecorder()
	a.proxyLinksAPI(w, httptest.NewRequest("GET", "http://127.0.0.1:7576/api/proxy/links", nil))
	var links []proxyLink
	if err := json.Unmarshal(w.Body.Bytes(), &links); err != nil || len(links) != 1 {
		t.Fatal(w.Body.String(), err)
	}
	if links[0].Socks != proxyListenAddress() || links[0].RotateURL != "http://127.0.0.1:7576/api/proxy/rotate" {
		t.Fatal(links[0])
	}
}

func TestRotateLinkReportsWhyItCannotStart(t *testing.T) {
	a := newDemoApp()
	w := httptest.NewRecorder()
	a.proxyRotateAPI(w, httptest.NewRequest("GET", "http://127.0.0.1:7575/api/proxy/rotate", nil))
	var result rotateResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusConflict || result.OK || result.Error == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	// The module lock is released, so the app's own button still works.
	if !a.tryModuleOperation() {
		t.Fatal("module lock left held")
	}
	a.moduleOperationMu.Unlock()
}

func TestRotateWaitStaysUnderFingerprintBrowserTimeouts(t *testing.T) {
	for query, want := range map[string]time.Duration{"": 12 * time.Second, "?wait=0": 0, "?wait=20": 20 * time.Second, "?wait=90": 24 * time.Second, "?wait=-3": 0, "?wait=x": 12 * time.Second} {
		if got := rotateWait(httptest.NewRequest("GET", "http://127.0.0.1:7575/api/proxy/rotate"+query, nil)); got != want {
			t.Fatal(query, got)
		}
	}
}

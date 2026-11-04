package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scriptedWorker answers by path, recording what it was asked.
type scriptedWorker struct {
	mu       sync.Mutex
	replies  map[string]string
	requests []string
}

func (w *scriptedWorker) Alive() bool { return true }
func (w *scriptedWorker) Stop()       {}
func (w *scriptedWorker) Request(method, path string, _ []byte) (int, []byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests = append(w.requests, method+" "+path)
	if reply, ok := w.replies[path]; ok {
		return 200, []byte(reply), nil
	}
	return 200, []byte(`{"ok":true}`), nil
}
func (w *scriptedWorker) asked(entry string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.requests {
		if r == entry {
			return true
		}
	}
	return false
}

func twoModuleController(a, b *scriptedWorker) *fleetController {
	return &fleetController{
		registry:       fleetRegistry{Bindings: []fleetBinding{{ID: "slot-a", Name: "模块 1", Port: 10909}, {ID: "slot-b", Name: "模块 2", Port: 10910}}},
		workers:        map[string]fleetProcess{"slot-a": a, "slot-b": b},
		workerSessions: map[string]string{"slot-a": "1", "slot-b": "2"},
		devices:        map[string]fleetDevice{"slot-a": {}, "slot-b": {}},
		approved:       map[string]string{}, errors: map[string]string{},
	}
}

func serveFleet(c *fleetController, method, path, module string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7575"+path, strings.NewReader(`{}`))
	if module != "" {
		r.Header.Set(moduleHeader, module)
	}
	w := httptest.NewRecorder()
	c.handler(http.NotFoundHandler()).ServeHTTP(w, r)
	return w
}

func TestFleetMergesSMSFromEveryModule(t *testing.T) {
	a := &scriptedWorker{replies: map[string]string{"/api/sms": `[{"sender":"10086","content":"旧","timestamp":"2026-11-04T09:00:00+08:00"}]`}}
	b := &scriptedWorker{replies: map[string]string{"/api/sms": `[{"sender":"Amazon","content":"新","timestamp":"2026-11-04T10:00:00+08:00"}]`}}
	w := serveFleet(twoModuleController(a, b), "GET", "/api/sms", "")
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || len(items) != 2 {
		t.Fatal(w.Body.String())
	}
	if items[0]["sender"] != "Amazon" || items[0]["module_name"] != "模块 2" || items[1]["module_id"] != "slot-a" {
		t.Fatal(items)
	}
}

func TestFleetSendsCallControlToTheModuleWithTheCall(t *testing.T) {
	a := &scriptedWorker{replies: map[string]string{"/api/calls/status": `{"active":null,"history":[],"polling":true}`}}
	b := &scriptedWorker{replies: map[string]string{"/api/calls/status": `{"active":{"id":"c1","state":"incoming","direction":"incoming","number":"13800138000"},"history":[],"polling":true}`}}
	c := twoModuleController(a, b)
	w := serveFleet(c, "GET", "/api/calls/status", "slot-a")
	var status map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	active, _ := status["active"].(map[string]any)
	if active["module_name"] != "模块 2" {
		t.Fatal(w.Body.String())
	}
	// The page shows module 1, but answering goes to module 2, which rings.
	serveFleet(c, "POST", "/api/calls/answer", "slot-a")
	if !b.asked("POST /api/calls/answer") || a.asked("POST /api/calls/answer") {
		t.Fatal(a.requests, b.requests)
	}
	// Dialling uses the module the page shows.
	serveFleet(c, "POST", "/api/calls/dial", "slot-a")
	if !a.asked("POST /api/calls/dial") {
		t.Fatal(a.requests)
	}
}

func TestFleetRoutesPagesByModuleHeader(t *testing.T) {
	a, b := &scriptedWorker{}, &scriptedWorker{}
	c := twoModuleController(a, b)
	serveFleet(c, "POST", "/api/forwarding/calls", "slot-b")
	serveFleet(c, "GET", "/api/usage?x=1", "slot-a")
	if !b.asked("POST /api/forwarding/calls") || !a.asked("GET /api/usage?x=1") {
		t.Fatal(a.requests, b.requests)
	}
	// Without a module and with two connected, writes are refused rather than guessed.
	if w := serveFleet(c, "POST", "/api/sms/send", ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestFleetTellsEveryWorkerAboutTheAudioHost(t *testing.T) {
	a, b := &scriptedWorker{}, &scriptedWorker{}
	serveFleet(twoModuleController(a, b), "POST", "/api/calls/audio/host/register", "")
	if !a.asked("POST /api/calls/audio/host/register") || !b.asked("POST /api/calls/audio/host/register") {
		t.Fatal(a.requests, b.requests)
	}
}

func TestFleetTimeoutsFitCarrierOperations(t *testing.T) {
	if fleetRequestTimeout("GET", "/api/forwarding/calls") < 3*60e9 || fleetRequestTimeout("GET", "/api/health") > 2e9 {
		t.Fatal("timeouts")
	}
}

func TestRestartJobRemovesItself(t *testing.T) {
	b, _ := os.ReadFile("fleet_mode.go")
	if !strings.Contains(string(b), `"; /bin/launchctl remove " + label`) {
		t.Fatal("the submitted restart job must remove itself, or launchd restarts the service forever")
	}
}

func TestModulesInheritSingleModuleState(t *testing.T) {
	shared := t.TempDir()
	for name, body := range map[string]string{"proxy-autostart.json": `{"enabled":true}`, "proxy-ip-history.json": `[{"ip":"1.2.3.4"}]`, "sim-usage.json": `{"cards":{}}`} {
		_ = os.WriteFile(filepath.Join(shared, name), []byte(body), 0600)
	}
	first, second := filepath.Join(shared, "modules", "a"), filepath.Join(shared, "modules", "b")
	seedModuleState(first, shared, "a")
	seedModuleState(second, shared, "b")
	for _, dir := range []string{first, second} {
		if b, _ := os.ReadFile(filepath.Join(dir, "proxy-autostart.json")); string(b) != `{"enabled":true}` {
			t.Fatal("autostart not inherited", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(first, "proxy-ip-history.json")); err != nil {
		t.Fatal("first module lost the history")
	}
	if _, err := os.Stat(filepath.Join(second, "sim-usage.json")); err == nil {
		t.Fatal("second module got the first SIM's usage")
	}
}

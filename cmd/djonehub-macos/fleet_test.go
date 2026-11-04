package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeFleetProcess struct {
	mu       sync.Mutex
	alive    bool
	stops    int
	port     int
	requests []string
	code     int
}

func (w *fakeFleetProcess) Alive() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.alive }
func (w *fakeFleetProcess) Stop()       { w.mu.Lock(); defer w.mu.Unlock(); w.alive = false; w.stops++ }
func (w *fakeFleetProcess) Request(method, path string, body []byte) (int, []byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.alive {
		return 0, nil, errors.New("worker exited")
	}
	w.requests = append(w.requests, method+" "+path)
	if method == "POST" {
		return w.code, []byte(`{"ready":true}`), nil
	}
	b, _ := json.Marshal(proxyState{Address: fmt.Sprintf("127.0.0.1:%d", w.port), Running: true})
	return 200, b, nil
}
func makeFleet(t *testing.T, devices *[]fleetDevice) (*fleetController, map[string][]*fakeFleetProcess) {
	t.Helper()
	created := map[string][]*fakeFleetProcess{}
	c, err := newFleetController(filepath.Join(t.TempDir(), "bindings.json"), func() ([]fleetDevice, error) { return *devices, nil }, func(b fleetBinding, _ fleetDevice) (fleetProcess, error) {
		w := &fakeFleetProcess{alive: true, port: b.Port, code: 200}
		created[b.ID] = append(created[b.ID], w)
		return w, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c, created
}
func TestFleetPortsSurviveReorderAndRestart(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "A"}, {Session: "2", Location: 2, Serial: "B"}}
	c, _ := makeFleet(t, &devices)
	c.reconcile()
	first := c.snapshot()
	if len(first.Modules) != 2 || first.Modules[0].Port != 10909 || first.Modules[1].Port != 10910 {
		t.Fatal(first)
	}
	devices[0], devices[1] = devices[1], devices[0]
	c.reconcile()
	restored, err := loadFleetRegistry(c.path)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range restored.Bindings {
		if b.ID != first.Modules[i].ID || b.Port != first.Modules[i].Port {
			t.Fatal("binding changed", restored)
		}
	}
}
func TestFleetAnonymousModuleIsConfirmedOncePerUSBPosition(t *testing.T) {
	devices := []fleetDevice{{Session: "11", Location: 1}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	id := c.registry.Bindings[0].ID
	if !c.snapshot().Modules[0].NeedsConfirmation || len(created) != 0 {
		t.Fatal("anonymous USB opened without confirmation")
	}
	if err := c.confirm(id, "11"); err != nil {
		t.Fatal(err)
	}
	c.reconcile()
	original := created[id][0]
	// A replug at the same USB position (or a reboot) is trusted again.
	devices[0].Session = "12"
	c.reconcile()
	if original.Alive() || c.snapshot().Modules[0].NeedsConfirmation {
		t.Fatal("confirmed USB position asked again")
	}
	c.reconcile()
	if len(created[id]) != 2 || c.registry.Bindings[0].Port != 10909 {
		t.Fatal("port changed on replug")
	}
	if restored, _ := loadFleetRegistry(c.path); !restored.Bindings[0].Confirmed {
		t.Fatal("confirmation not saved")
	}
	// Another USB position is a new module and still needs one confirmation.
	devices = append(devices, fleetDevice{Session: "13", Location: 2})
	c.reconcile()
	if !c.snapshot().Modules[1].NeedsConfirmation {
		t.Fatal("new position trusted without confirmation")
	}
}
func TestFleetUnplugOrCrashDoesNotRestartOtherModule(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "A"}, {Session: "2", Location: 2, Serial: "B"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	a, b := c.registry.Bindings[0].ID, c.registry.Bindings[1].ID
	originalB := created[b][0]
	devices = devices[1:]
	c.reconcile()
	if originalB.stops != 0 || len(created[b]) != 1 || created[a][0].Alive() {
		t.Fatal("unplug affected another worker")
	}
	// Reattach A, then simulate its worker crashing while B continues.
	devices = append(devices, fleetDevice{Session: "3", Location: 1, Serial: "A"})
	c.reconcile()
	created[a][1].Stop()
	c.reconcile()
	c.retryAt[a] = time.Time{}
	c.reconcile()
	if len(created[a]) != 3 || len(created[b]) != 1 || originalB.stops != 0 {
		t.Fatal("crash recovery was not isolated")
	}
}
func TestFleetDuplicateSerialNeedsManualBinding(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "same"}, {Session: "2", Location: 2, Serial: "same"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	if len(created) != 0 {
		t.Fatal("duplicate serial automatically trusted")
	}
	for _, m := range c.snapshot().Modules {
		if !m.NeedsConfirmation {
			t.Fatal(m)
		}
	}
}
func TestFleetActionTargetsOnlyRequestedModule(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "A"}, {Session: "2", Location: 2, Serial: "B"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	a, b := c.registry.Bindings[0].ID, c.registry.Bindings[1].ID
	request := httptest.NewRequest("POST", "/api/modules/"+b+"/proxy", strings.NewReader(`{"action":"stop"}`))
	recorder := httptest.NewRecorder()
	c.handler(http.NotFoundHandler()).ServeHTTP(recorder, request)
	if recorder.Code != 200 || len(created[a][0].requests) != 0 || len(created[b][0].requests) != 1 {
		t.Fatal("action crossed device boundary", recorder.Code)
	}
}
func TestFleetBadRegistryFailsClosed(t *testing.T) {
	r := fleetRegistry{Bindings: []fleetBinding{{ID: "a", Port: 10909}, {ID: "b", Port: 10909}}}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := saveFleetRegistry(path, r); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFleetRegistry(path); err == nil {
		t.Fatal("duplicate ports accepted")
	}
}
func TestFleetNoHardwareDoesNotClaimSupportOrOpenProxy(t *testing.T) {
	devices := []fleetDevice{}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	if len(c.snapshot().Modules) != 0 || len(created) != 0 {
		t.Fatal("invented device")
	}
	recorder := httptest.NewRecorder()
	c.handler(http.NotFoundHandler()).ServeHTTP(recorder, httptest.NewRequest("POST", "/api/proxy", strings.NewReader(`{"action":"start"}`)))
	if recorder.Code != 409 {
		t.Fatal("no-device start accepted", recorder.Code)
	}
}
func TestFleetUpdatePreparesEveryWorkerAndBlocksActions(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "A"}, {Session: "2", Location: 2, Serial: "B"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	handler := c.handler(http.NotFoundHandler())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/update/prepare", strings.NewReader(`{}`)))
	if recorder.Code != 200 {
		t.Fatal(recorder.Code)
	}
	for _, workers := range created {
		if len(workers[0].requests) != 1 || workers[0].requests[0] != "POST /api/update/prepare" {
			t.Fatal("worker not paused")
		}
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/modules/"+c.registry.Bindings[0].ID+"/proxy", strings.NewReader(`{"action":"start"}`)))
	if recorder.Code != 409 {
		t.Fatal("action accepted during update")
	}
}

func TestModuleModeArgumentsPreserveOtherOptions(t *testing.T) {
	original := []string{"/path with spaces/backend", "-listen", "127.0.0.1:7575", "--multi-module=false", "--web-console"}
	enabled := modeArguments(original, true)
	if strings.Join(enabled, "|") != "/path with spaces/backend|-listen|127.0.0.1:7575|--web-console|--multi-module" {
		t.Fatal(enabled)
	}
	disabled := modeArguments(enabled, false)
	if strings.Contains(strings.Join(disabled, "|"), "multi-module") {
		t.Fatal(disabled)
	}
}
func TestFleetInventoryErrorStopsWorkersAndReportsFailure(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "A"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	c.inventory = func() ([]fleetDevice, error) { return nil, errors.New("inventory failed") }
	c.reconcile()
	if c.snapshot().Error == "" || created[c.registry.Bindings[0].ID][0].Alive() {
		t.Fatal("inventory failure kept a stale worker")
	}
}
func TestFleetRejectsInvalidEnumerationSession(t *testing.T) {
	devices := []fleetDevice{{Session: "0", Location: 1, Serial: "A"}, {Session: "not-a-number", Location: 2, Serial: "B"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	if len(created) != 0 || c.snapshot().Error == "" {
		t.Fatal("invalid USB session accepted")
	}
}

func TestFleetRejectsAmbiguousSessionOrTopology(t *testing.T) {
	for _, devices := range [][]fleetDevice{
		{{Session: "1", Location: 1, Serial: "A"}, {Session: "1", Location: 2, Serial: "B"}},
		{{Session: "1", Location: 1, Serial: "A"}, {Session: "2", Location: 1, Serial: "B"}},
	} {
		c, created := makeFleet(t, &devices)
		c.reconcile()
		if len(created) != 0 || c.snapshot().Error == "" {
			t.Fatal("ambiguous device opened")
		}
	}
}

type slowStoppingWorker struct {
	*fakeFleetProcess
	entered chan struct{}
	release chan struct{}
}

func (w *slowStoppingWorker) Stop() { close(w.entered); <-w.release; w.fakeFleetProcess.Stop() }
func TestFleetSlowDisconnectDoesNotBlockOtherControls(t *testing.T) {
	devices := []fleetDevice{{Session: "1", Location: 1, Serial: "A"}, {Session: "2", Location: 2, Serial: "B"}}
	c, created := makeFleet(t, &devices)
	c.reconcile()
	a, b := c.registry.Bindings[0].ID, c.registry.Bindings[1].ID
	slow := &slowStoppingWorker{fakeFleetProcess: created[a][0], entered: make(chan struct{}), release: make(chan struct{})}
	c.workers[a] = slow
	devices = devices[1:]
	done := make(chan struct{})
	go func() { c.reconcile(); close(done) }()
	<-slow.entered
	controlDone := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		c.handler(http.NotFoundHandler()).ServeHTTP(recorder, httptest.NewRequest("POST", "/api/modules/"+b+"/proxy", strings.NewReader(`{"action":"probe"}`)))
		controlDone <- recorder.Code
	}()
	select {
	case code := <-controlDone:
		if code != 200 {
			t.Error(code)
		}
	case <-time.After(time.Second):
		t.Error("one device blocked all controls")
	}
	close(slow.release)
	<-done
}

func TestFleetUnixIPCUsesAcceptedHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix IPC is macOS-only")
	}
	dir, err := os.MkdirTemp("/tmp", "djo-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "worker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) }))}
	defer server.Close()
	go server.Serve(listener)
	worker := &fleetChild{socket: socket}
	code, _, err := worker.Request("GET", "/api/proxy", nil)
	if err != nil || code != 200 {
		t.Fatalf("IPC rejected: %d %v", code, err)
	}
}

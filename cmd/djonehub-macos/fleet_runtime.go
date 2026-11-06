package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/iniwex5/vohive/pkg/smscodec"
)

type fleetChild struct {
	cmd    *exec.Cmd
	socket string
	done   chan struct{}
	once   sync.Once
	// The socket can be deleted under a running worker (temp folder cleanup);
	// such a worker is unreachable and counts as dead so it gets restarted.
	started    time.Time
	socketSeen atomic.Bool
}

func (w *fleetChild) exited() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *fleetChild) Alive() bool {
	if w.exited() {
		return false
	}
	if _, err := os.Stat(w.socket); err == nil {
		w.socketSeen.Store(true)
		return true
	}
	return !w.socketSeen.Load() && time.Since(w.started) < 30*time.Second
}
func (w *fleetChild) Request(method, path string, body []byte) (int, []byte, error) {
	timeout := fleetRequestTimeout(method, path)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", w.socket)
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:7575"+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	return response.StatusCode, b, err
}
func (w *fleetChild) Stop() {
	w.once.Do(func() {
		if !w.exited() {
			_ = w.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-w.done:
		case <-time.After(20 * time.Second):
			_ = w.cmd.Process.Kill()
			<-w.done
		}
		_ = os.Remove(w.socket)
	})
}
func fleetSpawner(dir string) fleetStartFunc {
	return func(b fleetBinding, d fleetDevice) (fleetProcess, error) {
		socket := filepath.Join(dir, fmt.Sprintf("%d.sock", b.Port))
		if len(socket) > 100 {
			return nil, errors.New("worker socket path too long")
		}
		// Recreate the socket folder in case something deleted it meanwhile.
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		_ = os.Remove(socket)
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		command := exec.Command(executable, "--module-session", d.Session, "--module-id", b.ID, "--worker-socket", socket, "--proxy-listen", fmt.Sprintf("127.0.0.1:%d", b.Port))
		if fleetStateRoot != "" {
			command.Args = append(command.Args, "--state-dir", fleetStateRoot)
		}
		command.Env = append(os.Environ(), fmt.Sprintf("DJONEHUB_SUPERVISOR_PID=%d", os.Getpid()))
		log.Printf("starting isolated worker %s on port %d", b.ID, b.Port)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err = command.Start(); err != nil {
			return nil, err
		}
		w := &fleetChild{cmd: command, socket: socket, done: make(chan struct{}), started: time.Now()}
		go func() { err := command.Wait(); log.Printf("module worker %s exited: %v", b.ID, err); close(w.done) }()
		return w, nil
	}
}
func scopedUSBDeviceStatus() *usbDeviceStatus {
	devices, err := discoverFleetDevices()
	if err != nil {
		return nil
	}
	for _, d := range devices {
		if d.Session == strconv.FormatUint(scopedUSBSession, 10) {
			return &usbDeviceStatus{Product: d.Product, Vendor: "DJI / Quectel", VendorID: fmt.Sprintf("%04x", d.VendorID), ProductID: fmt.Sprintf("%04x", d.ProductID), LocationID: fmt.Sprintf("0x%08x", d.Location), Mode: "isolated USB module"}
		}
	}
	return nil
}
func newWorkerApp() *app {
	a := &app{port: "等待指定模块", smsPollInterval: 8 * time.Second, smsAutoCleanupME: false, smsReassembler: smscodec.NewReassembler(), callPollInterval: 3 * time.Second, audio: newAudioRouter()}
	if scopedModuleID != "" {
		base := fleetStateRoot
		if base == "" {
			if config, err := os.UserConfigDir(); err == nil {
				base = filepath.Join(config, "DJOneHub")
			}
		}
		a.profileNotesPath = filepath.Join(base, "modules", scopedModuleID, "profile-notes.json")
		a.usbProfileIntentPath = filepath.Join(base, "modules", scopedModuleID, "usb-profile-intent.json")
	}
	return a
}

func serveModuleWorker(socket string) error {
	if scopedModuleID == "" || strings.ContainsAny(scopedModuleID, "/\\") || !strings.HasPrefix(workerProxyAddress, "127.0.0.1:") || socket == "" {
		return errors.New("invalid worker identity or endpoint")
	}
	a := newWorkerApp()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a.proxy.lifecycle = ctx
	defer a.proxy.stop()
	parent, _ := strconv.Atoi(os.Getenv("DJONEHUB_SUPERVISOR_PID"))
	if parent <= 1 || os.Getppid() != parent {
		return errors.New("worker must be started by supervisor")
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if os.Getppid() != parent {
					cancel()
					return
				}
			}
		}
	}()
	go a.startSMSPoller(ctx)
	go a.runSIMKeepAlive(ctx)
	go a.startCallPoller(ctx)
	go a.startSignalRecovery(ctx)
	go a.runProxyAutoStart(ctx)
	go a.runUsageLedger(ctx)
	routes := a.routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.URL.Path == "/api/network/cellular-policy" {
			writeError(w, 409, "请切回单模块模式配置系统网络；多模块工作进程不会修改系统网关")
			return
		}
		routes.ServeHTTP(w, r)
	})
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); a.proxy.stop(); _ = server.Close() }()
	err = server.Serve(listener)
	a.moduleOperationMu.Lock()
	a.moduleOperationMu.Unlock()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func serveFleetSupervisor(listen string) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	stateDir := fleetStateRoot
	if stateDir == "" {
		stateDir = filepath.Join(config, "DJOneHub")
	}
	if err = os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	unlock, err := lockFleetState(filepath.Join(stateDir, "module-bindings.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	// Worker sockets live in DJOneHub's own folder: the system temp folder
	// gets emptied by macOS and cleanup apps while DJOneHub runs. Very long
	// home paths fall back to the temp folder (socket paths max ~100 bytes).
	dir := filepath.Join(stateDir, "run")
	if len(filepath.Join(dir, "10909.sock")) > 100 {
		if dir, err = os.MkdirTemp("", "djo-"); err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	} else if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	c, err := newFleetController(filepath.Join(stateDir, "module-bindings.json"), discoverFleetDevices, fleetSpawner(dir))
	if err != nil {
		return fmt.Errorf("module bindings: %w", err)
	}
	c.probe = fleetProber()
	defer c.close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c.reconcile()
	go runTelegramBot(ctx, fleetBot{c})
	go func() {
		// Listing USB devices takes about 15 ms, so a replugged or rebooted
		// module is picked up within a second.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.reconcile()
			}
		}
	}()
	fallback := newWorkerApp().routes()
	server := &http.Server{Addr: listen, Handler: securityHeaders(c.handler(fallback)), ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	log.Printf("multi-module supervisor listening on %s", listen)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (c *fleetController) forward(w http.ResponseWriter, r *http.Request, id, path string) {
	c.mu.Lock()
	worker := c.workers[id]
	paused := c.paused
	c.mu.Unlock()
	if paused && r.Method != "GET" {
		writeError(w, 409, errUpdateActive)
		return
	}
	if worker == nil {
		writeError(w, 503, "模块未连接、未确认绑定或后台未就绪")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		writeError(w, 400, "request too large")
		return
	}
	code, data, err := worker.Request(r.Method, path, b)
	if err != nil {
		writeError(w, 502, "模块后台未响应，其他模块不受影响")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}
func (c *fleetController) handler(fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/module-mode", moduleModeStatus)
	mux.HandleFunc("POST /api/module-mode", func(w http.ResponseWriter, r *http.Request) {
		enabled, ok := requestedModuleMode(w, r)
		if !ok {
			return
		}
		if err := c.prepareAllWorkers(); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		finishModuleModeChange(w, enabled)
	})
	mux.HandleFunc("GET /api/modules", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, c.snapshot()) })
	mux.HandleFunc("GET /api/checkup", c.fleetCheckup)
	mux.HandleFunc("POST /api/modules/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Generation string `json:"generation"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if err := c.confirm(r.PathValue("id"), body.Generation); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		go c.reconcile()
		writeJSON(w, 200, map[string]bool{"confirmed": true})
	})
	mux.HandleFunc("PATCH /api/modules/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" || len([]rune(name)) > 40 || strings.ContainsAny(name, "\r\n\x00") {
			writeError(w, 400, "名称需为 1–40 个字符")
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, b := range c.registry.Bindings {
			if b.ID == r.PathValue("id") {
				c.registry.Bindings[i].Name = name
				if err := saveFleetRegistry(c.path, c.registry); err != nil {
					c.registry.Bindings[i] = b
					writeError(w, 500, err.Error())
					return
				}
				writeJSON(w, 200, c.registry.Bindings[i])
				return
			}
		}
		writeError(w, 404, "unknown module")
	})
	mux.HandleFunc("GET /api/modules/{id}/proxy", func(w http.ResponseWriter, r *http.Request) { c.forward(w, r, r.PathValue("id"), "/api/proxy") })
	mux.HandleFunc("POST /api/modules/{id}/proxy", func(w http.ResponseWriter, r *http.Request) { c.forward(w, r, r.PathValue("id"), "/api/proxy") })
	rotate := func(w http.ResponseWriter, r *http.Request) { c.forward(w, r, r.PathValue("id"), "/api/proxy/rotate") }
	mux.HandleFunc("GET /api/modules/{id}/proxy/rotate", rotate)
	mux.HandleFunc("POST /api/modules/{id}/proxy/rotate", rotate)
	mux.HandleFunc("GET /api/proxy/links", func(w http.ResponseWriter, r *http.Request) {
		links := []proxyLink{}
		for _, m := range c.snapshot().Modules {
			link := proxyLink{ID: m.ID, Name: m.Name, Connected: m.Connected, Socks: fmt.Sprintf("127.0.0.1:%d", m.Port), RotateURL: apiBase(r) + "/api/modules/" + m.ID + "/proxy/rotate"}
			if m.Proxy != nil {
				link.PublicIP = m.Proxy.PublicIP
			}
			links = append(links, link)
		}
		writeJSON(w, 200, links)
	})
	mux.HandleFunc("POST /api/update/prepare", func(w http.ResponseWriter, r *http.Request) {
		if err := c.prepareAllWorkers(); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ready": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// The 4G route policy covers every cellular service on this Mac, so the
		// supervisor applies it itself instead of one module's worker.
		if r.URL.Path == "/api/platform" || r.URL.Path == "/api/network/cellular-policy" {
			fallback.ServeHTTP(w, r)
			return
		}
		if c.route(w, r) {
			return
		}
		c.mu.Lock()
		id := ""
		connected := 0
		for candidate := range c.devices {
			connected++
			id = candidate
		}
		available := connected == 1 && c.workers[id] != nil
		c.mu.Unlock()
		if available {
			c.forward(w, r, id, r.URL.RequestURI())
			return
		}
		// Existing notifier polling must stay healthy with no device or several devices.
		switch r.URL.Path {
		case "/api/calls/status":
			writeJSON(w, 200, map[string]any{"active": nil, "history": []any{}, "polling": true, "poll_interval_s": 3, "last_poll_error": "辅助工具仅在单模块连接时可用"})
		case "/api/sms":
			writeJSON(w, 200, []any{})
		case "/api/proxy":
			if r.Method == "GET" {
				writeJSON(w, 200, (&cellularProxy{}).snapshot())
			} else {
				writeError(w, 409, "请在多模块列表选择具体模块")
			}
		default:
			if r.Method == "GET" {
				fallback.ServeHTTP(w, r)
			} else {
				writeError(w, 409, "请连接并确认一个模块后使用辅助工具")
			}
		}
	})
	return mux
}

func (c *fleetController) prepareAllWorkers() error {
	c.mu.Lock()
	if c.paused {
		c.mu.Unlock()
		return errors.New(errUpdateActive)
	}
	c.paused = true
	workers := []fleetProcess{}
	for _, w := range c.workers {
		workers = append(workers, w)
	}
	c.mu.Unlock()
	time.AfterFunc(90*time.Second, func() { c.mu.Lock(); c.paused = false; c.mu.Unlock() })
	for _, worker := range workers {
		code, _, err := worker.Request("POST", "/api/update/prepare", []byte("{}"))
		if err != nil || code != 200 {
			return errors.New("模块正在操作或有通话，暂不能重启后台")
		}
	}
	return nil
}

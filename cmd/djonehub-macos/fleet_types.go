package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A session is one USB enumeration, not a persistent device identity.
var scopedUSBSession uint64
var scopedModuleID string
var coordinatorOnly bool
var fleetStateRoot string

type fleetDevice struct {
	Session    string   `json:"session"`
	Location   uint32   `json:"location"`
	Serial     string   `json:"serial"`
	Product    string   `json:"product"`
	VendorID   int      `json:"vendor"`
	ProductID  int      `json:"product_id"`
	Interfaces []string `json:"interfaces"`
}
type fleetBinding struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Identity string `json:"identity"`
	// Confirmed: the user confirmed this USB position once. A module there is
	// trusted again after a replug or reboot, so an unattended Mac comes back
	// with every proxy running.
	Confirmed bool `json:"confirmed,omitempty"`
	// IMEI: the module's own identity, so it is the same module in any USB
	// port or hub.
	IMEI string `json:"imei,omitempty"`
}
type fleetModule struct {
	fleetBinding
	Connected         bool        `json:"connected"`
	NeedsConfirmation bool        `json:"needs_confirmation"`
	Interface         string      `json:"interface"`
	Error             string      `json:"error"`
	Generation        string      `json:"generation"`
	Proxy             *proxyState `json:"proxy,omitempty"`
}
type fleetSnapshot struct {
	Modules          []fleetModule `json:"modules"`
	Error            string        `json:"error"`
	MultiDeviceTools bool          `json:"multi_device_tools"`
}
type fleetRegistry struct {
	Bindings []fleetBinding `json:"bindings"`
}

var fleetIDPattern = regexp.MustCompile(`^(serial-[0-9a-f]{24}|slot-[0-9a-f]{8}|imei-[0-9a-f]{24})$`)

func fleetIdentity(d fleetDevice, serialCounts map[string]int) (string, bool) {
	if d.Serial != "" && strings.Trim(d.Serial, "0 ") == "" {
		d.Serial = ""
	}
	if d.Serial != "" && serialCounts[d.Serial] == 1 {
		sum := sha256.Sum256([]byte(d.Serial))
		return "serial-" + hex.EncodeToString(sum[:12]), true
	}
	return fmt.Sprintf("slot-%08x", d.Location), false
}
func (r *fleetRegistry) confirmed(id string) bool {
	for _, b := range r.Bindings {
		if b.ID == id {
			return b.Confirmed
		}
	}
	return false
}

func (r *fleetRegistry) bind(id string) (fleetBinding, error) {
	for _, b := range r.Bindings {
		if b.ID == id {
			return b, nil
		}
	}
	used, names := map[int]bool{}, map[string]bool{}
	for _, b := range r.Bindings {
		used[b.Port] = true
		names[b.Name] = true
	}
	port := 10909
	for used[port] {
		port++
	}
	if port > 10940 {
		return fleetBinding{}, errors.New("module capacity reached (32 reserved ports)")
	}
	number := 1
	for names[fmt.Sprintf("蜂窝模块 %d", number)] {
		number++
	}
	b := fleetBinding{ID: id, Name: fmt.Sprintf("蜂窝模块 %d", number), Port: port, Identity: "USB 位置，重插需确认"}
	switch {
	case strings.HasPrefix(id, "serial-"):
		b.Identity = "唯一 USB 序列号"
	case strings.HasPrefix(id, "imei-"):
		b.Identity = "模块 IMEI"
	}
	r.Bindings = append(r.Bindings, b)
	return b, nil
}
func loadFleetRegistry(path string) (fleetRegistry, error) {
	var r fleetRegistry
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	ids, ports := map[string]bool{}, map[int]bool{}
	for _, v := range r.Bindings {
		if !fleetIDPattern.MatchString(v.ID) || ids[v.ID] || ports[v.Port] || v.Port < 10909 || v.Port > 10940 {
			return r, errors.New("invalid or duplicate module binding")
		}
		ids[v.ID] = true
		ports[v.Port] = true
	}
	return r, nil
}
func saveFleetRegistry(path string, r fleetRegistry) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".fleet-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func sortFleetBindings(r *fleetRegistry) {
	sort.Slice(r.Bindings, func(i, j int) bool { return r.Bindings[i].Port < r.Bindings[j].Port })
}

// Injected boundaries let enumeration, topology changes and worker failures be tested without hardware.
type fleetInventoryFunc func() ([]fleetDevice, error)
type fleetProcess interface {
	Request(method, path string, body []byte) (int, []byte, error)
	Stop()
	Alive() bool
}
type fleetStartFunc func(fleetBinding, fleetDevice) (fleetProcess, error)
type fleetController struct {
	mu             sync.Mutex
	reconcileMu    sync.Mutex
	registry       fleetRegistry
	path           string
	devices        map[string]fleetDevice
	approved       map[string]string
	workers        map[string]fleetProcess
	workerSessions map[string]string
	errors         map[string]string
	inventory      fleetInventoryFunc
	inventoryFails int
	start          fleetStartFunc
	// probe reads a module's IMEI before it starts; nil keeps USB positions.
	probe  fleetProbeFunc
	probes map[string]*fleetProbeState
	dirty  bool
	// reconnects: when each module came back on USB, for the checkup.
	reconnects map[string][]time.Time
	reconciled bool
	retryAt    map[string]time.Time
	paused     bool
	lastError  string
	// The module with a call in progress, from the last call status sweep,
	// and the App's audio host registration replayed to every worker.
	callOwner     string
	callOwnerAt   time.Time
	audioHostBody []byte
	audioHostSent map[string]string
}

// fleetInventoryFailLimit is about a minute of consecutive failed listings.
const fleetInventoryFailLimit = 60

func newFleetController(path string, inventory fleetInventoryFunc, start fleetStartFunc) (*fleetController, error) {
	r, err := loadFleetRegistry(path)
	if err != nil {
		return nil, err
	}
	sortFleetBindings(&r)
	return &fleetController{registry: r, path: path, inventory: inventory, start: start, devices: map[string]fleetDevice{}, approved: map[string]string{}, workers: map[string]fleetProcess{}, workerSessions: map[string]string{}, errors: map[string]string{}, retryAt: map[string]time.Time{}, probes: map[string]*fleetProbeState{}, reconnects: map[string][]time.Time{}}, nil
}
func (c *fleetController) reconcile() {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	devices, err := c.inventory()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastError = err.Error()
		// A listing that fails once says nothing about the modules. Unplugged
		// modules show up in a listing that works, and each worker notices its
		// own USB going away, so only a lasting failure stops them.
		c.inventoryFails++
		if c.inventoryFails == 1 {
			log.Printf("USB inventory failed, keeping module workers running: %v", err)
		}
		if c.inventoryFails < fleetInventoryFailLimit {
			return
		}
		stops := []fleetProcess{}
		for id, w := range c.workers {
			stops = append(stops, w)
			delete(c.workers, id)
			delete(c.workerSessions, id)
		}
		c.devices = map[string]fleetDevice{}
		c.mu.Unlock()
		stopFleetProcesses(stops)
		c.mu.Lock()
		return
	}
	if c.inventoryFails > 1 {
		log.Printf("USB inventory recovered after %d failed listings", c.inventoryFails)
	}
	c.inventoryFails = 0
	c.lastError = ""
	counts := map[string]int{}
	sessions := map[string]int{}
	locations := map[uint32]int{}
	for _, d := range devices {
		sessions[d.Session]++
		locations[d.Location]++
		if d.Serial != "" {
			counts[d.Serial]++
		}
	}
	next := map[string]fleetDevice{}
	previousLen := len(c.registry.Bindings)
	before := append([]fleetBinding(nil), c.registry.Bindings...)
	for _, d := range devices {
		if session, parseErr := strconv.ParseUint(d.Session, 10, 64); parseErr != nil || session == 0 || d.Location == 0 || sessions[d.Session] != 1 || locations[d.Location] != 1 {
			c.lastError = "USB identity unavailable; ambiguous devices are disabled"
			continue
		}
		id, reliable := fleetIdentity(d, counts)
		if !reliable && c.probe != nil {
			imeiID, state := c.identifyByIMEILocked(d, id, next)
			if state == probePending {
				continue
			}
			if state == probeKnown {
				id, reliable = imeiID, true
			}
		}
		if _, exists := next[id]; exists {
			c.lastError = "duplicate USB topology; affected devices disabled"
			delete(next, id)
			continue
		}
		if _, err := c.registry.bind(id); err != nil {
			c.lastError = err.Error()
			continue
		}
		next[id] = d
		if reliable || c.registry.confirmed(id) {
			c.approved[id] = d.Session
		}
	}
	if c.dropStalePositionsLocked(next) {
		c.dirty = true
	}
	for session := range c.probes {
		if sessions[session] == 0 {
			delete(c.probes, session)
		}
	}
	if len(c.registry.Bindings) != previousLen || c.dirty {
		c.dirty = false
		sortFleetBindings(&c.registry)
		if err := saveFleetRegistry(c.path, c.registry); err != nil {
			c.registry.Bindings = before
			c.lastError = "binding save failed: " + err.Error()
			next = map[string]fleetDevice{}
		}
	}
	stops := []fleetProcess{}
	for id, w := range c.workers {
		d, ok := next[id]
		if !ok || c.workerSessions[id] != d.Session || c.approved[id] != d.Session || !w.Alive() {
			if ok && c.workerSessions[id] == d.Session && !w.Alive() {
				c.retryAt[id] = time.Now().Add(6 * time.Second)
			}
			stops = append(stops, w)
			delete(c.workers, id)
			delete(c.workerSessions, id)
		}
	}
	if c.reconciled {
		for id, d := range next {
			if old, ok := c.devices[id]; !ok || old.Session != d.Session {
				c.noteReconnectLocked(id, time.Now())
			}
		}
	}
	c.reconciled = true
	c.devices = next
	c.mu.Unlock()
	stopFleetProcesses(stops)
	c.mu.Lock()
	if c.paused {
		return
	}
	for _, b := range c.registry.Bindings {
		d, ok := next[b.ID]
		if !ok || c.approved[b.ID] != d.Session || c.workers[b.ID] != nil || time.Now().Before(c.retryAt[b.ID]) {
			continue
		}
		w, err := c.start(b, d)
		if err != nil {
			c.errors[b.ID] = err.Error()
			c.retryAt[b.ID] = time.Now().Add(6 * time.Second)
			continue
		}
		c.workers[b.ID] = w
		c.workerSessions[b.ID] = d.Session
		delete(c.errors, b.ID)
	}
}
func (c *fleetController) confirm(id, expectedSession string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused {
		return errors.New(errUpdateActive)
	}
	d, ok := c.devices[id]
	if !ok {
		return errors.New("module is disconnected")
	}
	if d.Session != expectedSession {
		return errors.New("设备已经重新连接，请刷新后确认")
	}
	c.approved[id] = d.Session
	for i, b := range c.registry.Bindings {
		if b.ID == id && !b.Confirmed {
			c.registry.Bindings[i].Confirmed = true
			if err := saveFleetRegistry(c.path, c.registry); err != nil {
				c.registry.Bindings[i].Confirmed = false
				return err
			}
		}
	}
	return nil
}
func (c *fleetController) snapshot() fleetSnapshot {
	c.mu.Lock()
	out := fleetSnapshot{Modules: []fleetModule{}, Error: c.lastError}
	workers := map[string]fleetProcess{}
	for _, b := range c.registry.Bindings {
		d, connected := c.devices[b.ID]
		m := fleetModule{fleetBinding: b, Connected: connected, NeedsConfirmation: connected && c.approved[b.ID] != d.Session, Error: c.errors[b.ID], Generation: d.Session}
		if len(d.Interfaces) == 1 {
			m.Interface = d.Interfaces[0]
		}
		out.Modules = append(out.Modules, m)
		workers[b.ID] = c.workers[b.ID]
		if workers[b.ID] == nil {
			history := &cellularProxy{historyPath: filepath.Join(filepath.Dir(c.path), "modules", b.ID, "proxy-ip-history.json")}
			state := history.snapshot()
			state.Address = fmt.Sprintf("127.0.0.1:%d", b.Port)
			out.Modules[len(out.Modules)-1].Proxy = &state
		}
	}
	c.mu.Unlock()
	var wg sync.WaitGroup
	for i := range out.Modules {
		if w := workers[out.Modules[i].ID]; w != nil {
			wg.Add(1)
			go func(i int, w fleetProcess) {
				defer wg.Done()
				code, b, err := w.Request("GET", "/api/proxy", nil)
				if err != nil || code != 200 {
					out.Modules[i].Error = "模块后台尚未就绪或已退出"
					return
				}
				var s proxyState
				if json.Unmarshal(b, &s) == nil {
					out.Modules[i].Proxy = &s
				}
			}(i, w)
		}
	}
	wg.Wait()
	return out
}
func stopFleetProcesses(workers []fleetProcess) {
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(w fleetProcess) { defer wg.Done(); w.Stop() }(w)
	}
	wg.Wait()
}
func (c *fleetController) close() {
	c.mu.Lock()
	c.paused = true
	workers := []fleetProcess{}
	for _, w := range c.workers {
		workers = append(workers, w)
	}
	c.workers = map[string]fleetProcess{}
	c.mu.Unlock()
	stopFleetProcesses(workers)
}

const (
	probeUnavailable = iota // fall back to the USB position
	probePending
	probeKnown
)

// identifyByIMEILocked finds the binding for a module without a USB serial
// by its IMEI, starting a read when there is none yet for this enumeration.
func (c *fleetController) identifyByIMEILocked(d fleetDevice, slotID string, taken map[string]fleetDevice) (string, int) {
	p := c.probes[d.Session]
	if p == nil {
		p = &fleetProbeState{}
		c.probes[d.Session] = p
	}
	if !p.done {
		if p.fails >= fleetProbeMaxFails {
			return "", probeUnavailable
		}
		// A module that already runs under its position keeps running.
		if c.workers[slotID] != nil && c.workerSessions[slotID] == d.Session {
			return "", probeUnavailable
		}
		if !p.running && time.Now().After(p.next) {
			p.running = true
			go c.runProbe(d, p)
		}
		return "", probePending
	}
	imei := p.result.IMEI
	for _, b := range c.registry.Bindings {
		if b.IMEI == imei {
			return b.ID, probeKnown
		}
	}
	// First time this IMEI is seen: it may be a module bound by USB position
	// before IMEIs were recorded. Prefer the confirmed one whose saved eSIM
	// card holds the number in the module now, then the binding at this
	// position.
	adopt := -1
	if p.result.ICCID != "" {
		for i, b := range c.registry.Bindings {
			if b.IMEI == "" && b.Confirmed && !strings.HasPrefix(b.ID, "serial-") {
				if _, busy := taken[b.ID]; !busy && savedICCIDs(filepath.Dir(c.path), b.ID)[p.result.ICCID] {
					adopt = i
					break
				}
			}
		}
	}
	if adopt < 0 {
		for i, b := range c.registry.Bindings {
			if b.ID == slotID && b.IMEI == "" {
				adopt = i
			}
		}
	}
	if adopt >= 0 {
		c.registry.Bindings[adopt].IMEI = imei
		c.registry.Bindings[adopt].Confirmed = true
		c.registry.Bindings[adopt].Identity = "模块 IMEI"
		c.dirty = true
		return c.registry.Bindings[adopt].ID, probeKnown
	}
	sum := sha256.Sum256([]byte("imei:" + imei))
	id := "imei-" + hex.EncodeToString(sum[:12])
	b, err := c.registry.bind(id)
	if err != nil {
		c.lastError = err.Error()
		return "", probeUnavailable
	}
	for i := range c.registry.Bindings {
		if c.registry.Bindings[i].ID == b.ID {
			c.registry.Bindings[i].IMEI, c.registry.Bindings[i].Confirmed = imei, true
		}
	}
	c.dirty = true
	return id, probeKnown
}

func (c *fleetController) runProbe(d fleetDevice, p *fleetProbeState) {
	result, err := c.probe(d)
	c.mu.Lock()
	p.running = false
	if err != nil {
		p.fails++
		p.next = time.Now().Add(time.Duration(p.fails) * 4 * time.Second)
		log.Printf("module at USB 0x%08x: %v", d.Location, err)
	} else {
		p.result, p.done = result, true
	}
	c.mu.Unlock()
	go c.reconcile()
}

// dropStalePositionsLocked forgets bindings that were only ever a USB
// position nobody confirmed, once no module is there: they were a known
// module seen in a new port before it could be recognised.
func (c *fleetController) dropStalePositionsLocked(connected map[string]fleetDevice) bool {
	kept := c.registry.Bindings[:0]
	dropped := false
	for _, b := range c.registry.Bindings {
		if _, on := connected[b.ID]; !on && !b.Confirmed && b.IMEI == "" && strings.HasPrefix(b.ID, "slot-") {
			dropped = true
			continue
		}
		kept = append(kept, b)
	}
	c.registry.Bindings = kept
	return dropped
}

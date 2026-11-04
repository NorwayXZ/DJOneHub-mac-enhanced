package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// moduleHeader carries the module the App's page is showing. Requests with it
// go to that module's worker, so every page works on any module; without it
// the supervisor keeps its single-module fallback.
const moduleHeader = "X-DJOneHub-Module"

// callOwnerFresh is how long a call status sweep decides where call control goes.
const callOwnerFresh = 3 * time.Second

func fleetRequestTimeout(method, path string) time.Duration {
	switch {
	case method == "GET" && (path == "/api/proxy" || path == "/api/health"):
		return 1500 * time.Millisecond
	case method == "GET" && (path == "/api/sms" || path == "/api/calls/status"):
		return 4 * time.Second
	case strings.HasPrefix(path, "/api/forwarding/calls"), strings.HasPrefix(path, "/api/voice/provision"),
		strings.HasPrefix(path, "/api/esim/download"), strings.HasPrefix(path, "/api/esim/switch"), strings.HasPrefix(path, "/api/module/setup"), strings.HasPrefix(path, "/api/band"):
		// Carrier and module operations that legitimately take minutes.
		return 7 * time.Minute
	}
	return 27 * time.Second
}

type fleetTarget struct {
	id, name string
	worker   fleetProcess
}

// targets lists the running module workers in binding order.
func (c *fleetController) targets() []fleetTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []fleetTarget{}
	for _, b := range c.registry.Bindings {
		if w := c.workers[b.ID]; w != nil {
			name := b.Name
			if name == "" {
				name = b.ID
			}
			out = append(out, fleetTarget{id: b.ID, name: name, worker: w})
		}
	}
	return out
}

func (c *fleetController) hasWorker(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return id != "" && c.workers[id] != nil
}

// route handles requests that are not addressed to one module by path:
// merged SMS and call status, call control for the module with the call,
// and anything a page sends with moduleHeader. false leaves the request to
// the single-module fallback.
func (c *fleetController) route(w http.ResponseWriter, r *http.Request) bool {
	selected := r.Header.Get(moduleHeader)
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/sms":
		c.mergedSMS(w)
		return true
	case r.Method == "GET" && r.URL.Path == "/api/calls/status":
		c.mergedCalls(w)
		return true
	case r.Method == "POST" && r.URL.Path == "/api/calls/audio/host/register":
		c.registerAudioHost(w, r)
		return true
	case strings.HasPrefix(r.URL.Path, "/api/calls/") && r.URL.Path != "/api/calls/dial":
		if owner := c.currentCallOwner(); owner != "" {
			c.forward(w, r, owner, r.URL.RequestURI())
			return true
		}
	}
	if c.hasWorker(selected) {
		c.forward(w, r, selected, r.URL.RequestURI())
		return true
	}
	return false
}

// sweep asks every worker for path at once.
func (c *fleetController) sweep(path string) map[string][]byte {
	targets := c.targets()
	out := map[string][]byte{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t fleetTarget) {
			defer wg.Done()
			code, data, err := t.worker.Request("GET", path, nil)
			if err == nil && code == 200 {
				mu.Lock()
				out[t.id] = data
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	return out
}

func (c *fleetController) moduleNames() map[string]string {
	names := map[string]string{}
	for _, t := range c.targets() {
		names[t.id] = t.name
	}
	return names
}

func recordTime(item map[string]any, key string) time.Time {
	s, _ := item[key].(string)
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// mergedSMS lists every module's messages, newest first, each tagged with its module.
func (c *fleetController) mergedSMS(w http.ResponseWriter) {
	names := c.moduleNames()
	all := []map[string]any{}
	for id, data := range c.sweep("/api/sms") {
		var items []map[string]any
		if json.Unmarshal(data, &items) != nil {
			continue
		}
		for _, item := range items {
			item["module_id"], item["module_name"] = id, names[id]
			all = append(all, item)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return recordTime(all[i], "timestamp").After(recordTime(all[j], "timestamp")) })
	writeJSON(w, 200, all)
}

func callPriority(call map[string]any) int {
	switch call["state"] {
	case "active", "held":
		return 3
	case "incoming", "waiting":
		return 2
	case nil:
		return 0
	}
	return 1
}

// mergedCalls reports the most urgent call of any module (a call in progress
// before a ringing one) and every module's history, and remembers which
// module call control goes to.
func (c *fleetController) mergedCalls(w http.ResponseWriter) {
	names := c.moduleNames()
	responses := c.sweep("/api/calls/status")
	var active map[string]any
	var audio any
	owner := ""
	history := []map[string]any{}
	errorsSeen := []string{}
	polling := len(responses) > 0
	ids := make([]string, 0, len(responses))
	for id := range responses {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var status struct {
			Active  map[string]any   `json:"active"`
			History []map[string]any `json:"history"`
			Polling bool             `json:"polling"`
			Error   string           `json:"last_poll_error"`
			Audio   any              `json:"audio"`
		}
		if json.Unmarshal(responses[id], &status) != nil {
			continue
		}
		polling = polling && status.Polling
		if status.Error != "" {
			errorsSeen = append(errorsSeen, names[id]+"："+status.Error)
		}
		if status.Active != nil && callPriority(status.Active) > callPriority(active) {
			status.Active["module_id"], status.Active["module_name"] = id, names[id]
			active, owner, audio = status.Active, id, status.Audio
		}
		for _, item := range status.History {
			item["module_id"], item["module_name"] = id, names[id]
			history = append(history, item)
		}
	}
	sort.SliceStable(history, func(i, j int) bool {
		return recordTime(history[i], "started_at").After(recordTime(history[j], "started_at"))
	})
	if len(history) > 100 {
		history = history[:100]
	}
	c.mu.Lock()
	c.callOwner, c.callOwnerAt = owner, time.Now()
	c.mu.Unlock()
	c.replayAudioHost()
	writeJSON(w, 200, map[string]any{"active": active, "history": history, "polling": polling, "poll_interval_s": 3, "last_poll_error": strings.Join(errorsSeen, "；"), "audio": audio})
}

func (c *fleetController) currentCallOwner() string {
	c.mu.Lock()
	owner, at := c.callOwner, c.callOwnerAt
	c.mu.Unlock()
	if time.Since(at) > callOwnerFresh {
		c.mergedCalls(discardResponse{})
		c.mu.Lock()
		owner = c.callOwner
		c.mu.Unlock()
	}
	if !c.hasWorker(owner) {
		return ""
	}
	return owner
}

// registerAudioHost tells every worker that the App plays call audio, and
// remembers it for workers that start later.
func (c *fleetController) registerAudioHost(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if !decodeJSON(w, r, &body) {
		return
	}
	c.mu.Lock()
	c.audioHostBody = append([]byte{}, body...)
	c.audioHostSent = map[string]string{}
	c.mu.Unlock()
	c.replayAudioHost()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (c *fleetController) replayAudioHost() {
	c.mu.Lock()
	body := c.audioHostBody
	pending := []fleetTarget{}
	if body != nil {
		if c.audioHostSent == nil {
			c.audioHostSent = map[string]string{}
		}
		for _, b := range c.registry.Bindings {
			if w := c.workers[b.ID]; w != nil && c.audioHostSent[b.ID] != c.workerSessions[b.ID] {
				pending = append(pending, fleetTarget{id: b.ID, worker: w})
			}
		}
	}
	c.mu.Unlock()
	for _, t := range pending {
		if code, _, err := t.worker.Request("POST", "/api/calls/audio/host/register", body); err == nil && code < 300 {
			c.mu.Lock()
			c.audioHostSent[t.id] = c.workerSessions[t.id]
			c.mu.Unlock()
		}
	}
}

// discardResponse lets mergedCalls refresh the call owner without a client.
type discardResponse struct{}

func (discardResponse) Header() http.Header         { return http.Header{} }
func (discardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (discardResponse) WriteHeader(int)             {}

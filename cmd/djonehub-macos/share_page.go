package main

import (
	_ "embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

//go:embed web/share/index.html
var sharePageHTML []byte

// shareSettings are the owner's public addresses for friends: the tunnel
// address of each module's share port and of the share page.
type shareSettings struct {
	PublicSOCKS map[string]string `json:"public_socks"` // module ID -> host:port
	PublicPage  string            `json:"public_page"`
}

const shareSettingsFile = "share-settings.json"

func loadShareSettings() shareSettings {
	s := shareSettings{PublicSOCKS: map[string]string{}}
	if b, err := os.ReadFile(sharePath(shareSettingsFile)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.PublicSOCKS == nil {
		s.PublicSOCKS = map[string]string{}
	}
	return s
}

func shareSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body shareSettings
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
			writeError(w, 400, "invalid request")
			return
		}
		path := sharePath(shareSettingsFile)
		if err := withSharedFileLock(path, func() error {
			b, _ := json.MarshalIndent(body, "", "  ")
			return os.WriteFile(path, b, 0600)
		}); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, loadShareSettings())
}

// shareModule is one module as a friend sees it.
type shareModule struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Public    string `json:"public,omitempty"`
	Connected bool   `json:"connected"`
	IP        string `json:"ip,omitempty"`
	Rotate    string `json:"rotate,omitempty"`
}

// shareModuleSource lists the modules with their SOCKS ports; the fleet
// supervisor sets it, single-module mode uses this computer's proxy.
var shareModuleSource struct {
	sync.Mutex
	fn func() []shareModule
}

func setShareModuleSource(fn func() []shareModule) {
	shareModuleSource.Lock()
	shareModuleSource.fn = fn
	shareModuleSource.Unlock()
}

func shareModules() []shareModule {
	shareModuleSource.Lock()
	fn := shareModuleSource.fn
	shareModuleSource.Unlock()
	if fn != nil {
		return fn()
	}
	_, port, _ := net.SplitHostPort(proxyListenAddress())
	n, _ := strconv.Atoi(port)
	return []shareModule{{ID: "", Name: "蜂窝代理", Port: n, Connected: true}}
}

type sharePageView struct {
	Name      string        `json:"name"`
	Username  string        `json:"username"`
	Used      int64         `json:"used"`
	Quota     int64         `json:"quota"`
	Status    string        `json:"status"`
	OK        bool          `json:"ok"`
	NextReset *time.Time    `json:"next_reset,omitempty"`
	Expires   string        `json:"expires,omitempty"`
	Modules   []shareModule `json:"modules"`
	CanRotate bool          `json:"can_rotate"`
	Updated   time.Time     `json:"updated"`
}

// sharePageHandler serves the friend page and, with the friend's login,
// their usage.
func sharePageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/share/api" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(sharePageHTML)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		user, pass = r.URL.Query().Get("user"), r.URL.Query().Get("pass")
	}
	a, ok := shareLogin(user, pass)
	if !ok {
		// Slow down guessing.
		time.Sleep(600 * time.Millisecond)
		writeError(w, 401, "用户名或密码不对")
		return
	}
	now := time.Now()
	var view *shareAccountView
	for _, v := range shareViews(now) {
		if v.ID == a.ID {
			v := v
			view = &v
		}
	}
	if view == nil {
		writeError(w, 401, "用户名或密码不对")
		return
	}
	settings := loadShareSettings()
	out := sharePageView{Name: a.Name, Username: a.Username, Used: view.Used, Quota: a.QuotaBytes, Status: view.Status, OK: view.Status == "正常",
		NextReset: view.NextReset, Expires: a.Expires, CanRotate: a.AllowRotate, Updated: now, Modules: []shareModule{}}
	for _, m := range shareModules() {
		if len(a.Modules) > 0 {
			allowed := false
			for _, id := range a.Modules {
				allowed = allowed || id == m.ID
			}
			if !allowed {
				continue
			}
		}
		m.Port += sharePortOffset
		m.Public = settings.PublicSOCKS[m.ID]
		if a.AllowRotate {
			if m.ID != "" {
				m.Rotate = "/api/modules/" + m.ID + "/proxy/rotate"
			} else {
				m.Rotate = "/api/proxy/rotate"
			}
		}
		out.Modules = append(out.Modules, m)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

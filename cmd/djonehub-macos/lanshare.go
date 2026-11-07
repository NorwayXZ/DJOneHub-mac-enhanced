package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LAN sharing opens the SOCKS ports and the change-IP link to other devices
// on the local network. Clients on this computer keep using them without a
// password; every LAN client must log in. The file is shared by all module
// workers, which rebind their ports when it changes.
const (
	lanShareFile = "lan-share.json"
	// lanControlPort serves only the change-IP link on the LAN; the control
	// API on 7575 stays on loopback.
	lanControlPort  = 7578
	lanShareRecheck = 2 * time.Second
)

type lanShareSettings struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type lanShareStore struct {
	mu       sync.Mutex
	loaded   bool
	path     string
	modTime  time.Time
	settings lanShareSettings
}

var lanShare lanShareStore

// loadLocked rereads the file when it changed, since every module worker
// shares it in multi-module mode.
func (s *lanShareStore) loadLocked() {
	if !s.loaded {
		s.loaded = true
		if s.path == "" {
			dir, err := os.UserConfigDir()
			if err != nil {
				return
			}
			s.path = filepath.Join(dir, "DJOneHub", lanShareFile)
		}
	}
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.settings, s.modTime = lanShareSettings{}, time.Time{}
		}
		return
	}
	if info.ModTime().Equal(s.modTime) {
		return
	}
	var settings lanShareSettings
	if b, err := os.ReadFile(s.path); err == nil && json.Unmarshal(b, &settings) == nil {
		s.settings, s.modTime = settings, info.ModTime()
	}
}

func (s *lanShareStore) get() lanShareSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.settings
}

func (s *lanShareStore) set(settings lanShareSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if s.path == "" {
		return errors.New("无法确定设置保存位置")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, _ := json.Marshal(settings)
	if err := os.WriteFile(s.path, b, 0600); err != nil {
		return err
	}
	_ = os.Chmod(s.path, 0600)
	s.settings = settings
	if info, err := os.Stat(s.path); err == nil {
		s.modTime = info.ModTime()
	}
	return nil
}

// checkLogin compares in constant time; a disabled share accepts nobody.
func (s lanShareSettings) checkLogin(user, pass string) bool {
	if !s.Enabled || s.Username == "" || s.Password == "" {
		return false
	}
	u := subtle.ConstantTimeCompare([]byte(user), []byte(s.Username))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(s.Password))
	return u&p == 1
}

// lanBindAddress is where a SOCKS port listens: loopback, or every interface
// while LAN sharing is on.
func lanBindAddress(loopback string) string {
	if !lanShare.get().Enabled {
		return loopback
	}
	_, port, err := net.SplitHostPort(loopback)
	if err != nil {
		return loopback
	}
	return net.JoinHostPort("0.0.0.0", port)
}

// isLANClient is a TCP peer from another machine. In-process pipes and
// loopback peers are this computer.
func isLANClient(c net.Conn) bool {
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	return ok && !addr.IP.IsLoopback()
}

// lanAddress is this computer's address on the default (broadband or Wi-Fi)
// network. Connecting a UDP socket sends nothing.
func lanAddress() string {
	c, err := net.Dial("udp4", "223.5.5.5:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if addr, ok := c.LocalAddr().(*net.UDPAddr); ok && !addr.IP.IsLoopback() {
		return addr.IP.String()
	}
	return ""
}

// lanLinks gives the LAN form of a module's SOCKS address and change-IP
// link, or empty strings while sharing is off.
func lanLinks(socks, rotatePath string) (string, string) {
	settings := lanShare.get()
	ip := lanAddress()
	if !settings.Enabled || ip == "" {
		return "", ""
	}
	_, port, err := net.SplitHostPort(socks)
	if err != nil {
		return "", ""
	}
	query := url.Values{"user": {settings.Username}, "pass": {settings.Password}}
	return net.JoinHostPort(ip, port), "http://" + net.JoinHostPort(ip, strconv.Itoa(lanControlPort)) + rotatePath + "?" + query.Encode()
}

type lanShareView struct {
	lanShareSettings
	Address     string `json:"address"`
	ControlPort int    `json:"control_port"`
}

func lanShareAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body lanShareSettings
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			writeError(w, 400, "invalid request")
			return
		}
		body.Username, body.Password = strings.TrimSpace(body.Username), strings.TrimSpace(body.Password)
		if body.Enabled && (body.Username == "" || body.Password == "") {
			writeError(w, 400, "请先填写用户名和密码")
			return
		}
		if err := lanShare.set(body); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		log.Printf("LAN sharing %s", map[bool]string{true: "enabled", false: "disabled"}[body.Enabled])
	}
	writeJSON(w, 200, lanShareView{lanShareSettings: lanShare.get(), Address: lanAddress(), ControlPort: lanControlPort})
}

// lanRotateHandler admits only the change-IP link, and only with the LAN
// login, given as ?user=&pass= (fingerprint browsers take a bare URL) or as
// HTTP basic auth.
func lanRotateHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRotatePath(r.URL.Path) {
			writeError(w, 404, "局域网只开放换 IP 链接")
			return
		}
		user, pass, ok := r.BasicAuth()
		query := r.URL.Query()
		if !ok {
			user, pass = query.Get("user"), query.Get("pass")
		}
		if !lanShare.get().checkLogin(user, pass) {
			writeError(w, 401, "用户名或密码不对")
			return
		}
		query.Del("user")
		query.Del("pass")
		r = r.Clone(r.Context())
		r.URL.RawQuery = query.Encode()
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r)
	})
}

// runLANControl serves the change-IP link on the LAN while sharing is on.
func runLANControl(ctx context.Context, handler http.Handler) {
	var server *http.Server
	lastErr := ""
	stop := func() {
		if server != nil {
			_ = server.Close()
			server = nil
			log.Printf("LAN change-IP link closed")
		}
	}
	defer stop()
	ticker := time.NewTicker(lanShareRecheck)
	defer ticker.Stop()
	for {
		enabled := lanShare.get().Enabled
		if enabled && server == nil {
			l, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(lanControlPort)))
			if err != nil {
				if err.Error() != lastErr {
					lastErr = err.Error()
					log.Printf("LAN change-IP link: %v", err)
				}
			} else {
				lastErr = ""
				server = &http.Server{Handler: lanRotateHandler(handler), ReadHeaderTimeout: 5 * time.Second}
				go func(s *http.Server) { _ = s.Serve(l) }(server)
				log.Printf("LAN change-IP link listening on %s", l.Addr())
			}
		} else if !enabled {
			stop()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

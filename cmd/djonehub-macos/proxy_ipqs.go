package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IPQualityScore is optional: with an API key the user entered in the App,
// each exit IP also gets an IPQS fraud score. The free plan has a small
// monthly quota, so only new IPs, the current IP once a day and a short
// history backfill are looked up. Lookups go over the default route.
const (
	ipqsAPIBase      = "https://ipqualityscore.com/api/json/"
	ipqsRefreshAge   = 24 * time.Hour
	ipqsBackfillMax  = 10
	ipqsSettingsFile = "ipqs.json"
)

type ipqsSettings struct {
	APIKey string `json:"api_key"`
}

type ipqsStore struct {
	mu     sync.Mutex
	loaded bool
	path   string
	key    string
}

var ipqs ipqsStore

func (s *ipqsStore) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return
		}
		s.path = filepath.Join(dir, "DJOneHub", ipqsSettingsFile)
	}
	var settings ipqsSettings
	if b, err := os.ReadFile(s.path); err == nil && json.Unmarshal(b, &settings) == nil {
		s.key = strings.TrimSpace(settings.APIKey)
	}
}

func (s *ipqsStore) apiKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.key
}

func (s *ipqsStore) setAPIKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if s.path == "" {
		return errors.New("无法确定设置保存位置")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, _ := json.Marshal(ipqsSettings{APIKey: key})
	if err := os.WriteFile(s.path, b, 0600); err != nil {
		return err
	}
	_ = os.Chmod(s.path, 0600)
	s.key = key
	return nil
}

// ipqsKeyHint shows enough of the key to recognise it, never the whole key.
func ipqsKeyHint(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return key[:4] + "…" + key[len(key)-4:]
}

func ipqsGet(ctx context.Context, path string, query url.Values, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	target := ipqsAPIBase + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The URL carries the key; keep it out of logs and the App.
		return errors.New("无法连接 IPQualityScore")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("IPQualityScore HTTP %d", resp.StatusCode)
	}
	var status struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return errors.New("IPQualityScore 返回了无法识别的内容")
	}
	if !status.Success {
		return errors.New("IPQualityScore：" + status.Message)
	}
	return json.Unmarshal(body, into)
}

type ipqsResult struct {
	FraudScore  int  `json:"fraud_score"`
	Proxy       bool `json:"proxy"`
	VPN         bool `json:"vpn"`
	Tor         bool `json:"tor"`
	RecentAbuse bool `json:"recent_abuse"`
	BotStatus   bool `json:"bot_status"`
}

func (r ipqsResult) flags() []string {
	var out []string
	for _, f := range []struct {
		on   bool
		name string
	}{{r.Proxy, "proxy"}, {r.VPN, "vpn"}, {r.Tor, "tor"}, {r.RecentAbuse, "recent_abuse"}, {r.BotStatus, "bot"}} {
		if f.on {
			out = append(out, f.name)
		}
	}
	return out
}

func lookupIPQS(ctx context.Context, key, ip string) (ipqsResult, error) {
	var r ipqsResult
	// Default strictness; mobile=true because these are cellular exits.
	q := url.Values{"strictness": {"0"}, "allow_public_access_points": {"true"}, "mobile": {"true"}}
	err := ipqsGet(ctx, "ip/"+url.PathEscape(key)+"/"+url.PathEscape(ip), q, &r)
	return r, err
}

type ipqsAccount struct {
	Credits int `json:"credits"`
	Usage   int `json:"usage"`
}

func ipqsAccountInfo(ctx context.Context, key string) (ipqsAccount, error) {
	var a ipqsAccount
	err := ipqsGet(ctx, "account/"+url.PathEscape(key), nil, &a)
	return a, err
}

type ipqsSettingsView struct {
	Configured bool   `json:"configured"`
	KeyHint    string `json:"key_hint,omitempty"`
	Credits    *int   `json:"credits,omitempty"`
	Usage      *int   `json:"usage,omitempty"`
	Error      string `json:"error,omitempty"`
}

func ipqsView(ctx context.Context, key string) ipqsSettingsView {
	view := ipqsSettingsView{Configured: key != ""}
	if key == "" {
		return view
	}
	view.KeyHint = ipqsKeyHint(key)
	if account, err := ipqsAccountInfo(ctx, key); err != nil {
		view.Error = err.Error()
	} else {
		view.Credits, view.Usage = &account.Credits, &account.Usage
	}
	return view
}

// GET shows whether a key is set (masked) and the account quota; POST
// {"api_key": "..."} checks and saves a key, an empty key turns IPQS off.
func (a *app) ipqsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, ipqsView(r.Context(), ipqs.apiKey()))
		return
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if key != "" {
		if _, err := ipqsAccountInfo(r.Context(), key); err != nil {
			writeError(w, 400, "API Key 无效或无法验证："+err.Error())
			return
		}
	}
	if err := ipqs.setAPIKey(key); err != nil {
		writeError(w, 500, "保存失败："+err.Error())
		return
	}
	if key != "" {
		go a.proxy.fillFraudScores(context.WithoutCancel(r.Context()))
	}
	writeJSON(w, 200, ipqsView(r.Context(), key))
}

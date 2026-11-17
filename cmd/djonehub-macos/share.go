package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Friend sharing: each friend gets an own SOCKS5 login with a traffic quota.
// Friends connect to a module's share port (its SOCKS port + 10000), which
// always asks for a login, also through a tunnel that makes them look like
// this computer. Every byte up and down counts against the friend; at the
// quota new connections are refused and open ones end. Friends see their
// usage on the share page (port 7578, /share).
//
// Accounts live in share-accounts.json and usage in share-usage.json, both
// shared by every module worker. Workers add what they counted every few
// seconds under a file lock, so the quota holds across modules.

const (
	shareAccountsFile = "share-accounts.json"
	shareUsageFile    = "share-usage.json"
	sharePortOffset   = 10000
	shareFlushEvery   = 3 * time.Second
)

type shareAccount struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Username    string    `json:"username"`
	Password    string    `json:"password"`
	QuotaBytes  int64     `json:"quota_bytes"` // 0 = no limit
	ResetDay    int       `json:"reset_day"`   // 1-28: monthly reset; 0 = never
	Expires     string    `json:"expires"`     // YYYY-MM-DD, empty = never
	Modules     []string  `json:"modules"`     // empty = every module
	AllowRotate bool      `json:"allow_rotate"`
	Enabled     bool      `json:"enabled"`
	Created     time.Time `json:"created"`
}

type shareUsage struct {
	Used        int64            `json:"used"`
	PeriodStart time.Time        `json:"period_start"`
	ByModule    map[string]int64 `json:"by_module,omitempty"`
	LastSeen    time.Time        `json:"last_seen,omitempty"`
}

func shareDir() string { return fleetStateDir() }

func sharePath(name string) string {
	dir := shareDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// ---- account store (cached, reread when the file changes) ------------------

var shareAccounts struct {
	sync.Mutex
	modTime time.Time
	list    []shareAccount
}

func loadShareAccounts() []shareAccount {
	shareAccounts.Lock()
	defer shareAccounts.Unlock()
	path := sharePath(shareAccountsFile)
	info, err := os.Stat(path)
	if err != nil {
		shareAccounts.list, shareAccounts.modTime = nil, time.Time{}
		return nil
	}
	if !info.ModTime().Equal(shareAccounts.modTime) {
		var list []shareAccount
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &list) == nil {
			shareAccounts.list, shareAccounts.modTime = list, info.ModTime()
		}
	}
	return append([]shareAccount(nil), shareAccounts.list...)
}

func saveShareAccounts(list []shareAccount) error {
	path := sharePath(shareAccountsFile)
	if path == "" {
		return errors.New("无法确定设置保存位置")
	}
	return withSharedFileLock(path, func() error {
		b, _ := json.MarshalIndent(list, "", "  ")
		if err := os.WriteFile(path, b, 0600); err != nil {
			return err
		}
		return os.Chmod(path, 0600)
	})
}

func shareAnyEnabled() bool {
	for _, a := range loadShareAccounts() {
		if a.Enabled {
			return true
		}
	}
	return false
}

// shareLogin finds the friend account for a login, comparing in constant time.
func shareLogin(user, pass string) (shareAccount, bool) {
	var found shareAccount
	ok := 0
	for _, a := range loadShareAccounts() {
		if a.Username == "" || a.Password == "" {
			continue
		}
		u := subtle.ConstantTimeCompare([]byte(user), []byte(a.Username))
		p := subtle.ConstantTimeCompare([]byte(pass), []byte(a.Password))
		if u&p == 1 {
			found, ok = a, 1
		}
	}
	return found, ok == 1
}

// ---- usage -----------------------------------------------------------------

// periodStart is the start of the current monthly period for a reset day.
func periodStart(resetDay int, now time.Time) time.Time {
	if resetDay < 1 || resetDay > 28 {
		return time.Time{}
	}
	y, m, d := now.Date()
	if d < resetDay {
		m--
	}
	return time.Date(y, m, resetDay, 0, 0, 0, 0, now.Location())
}

func nextReset(resetDay int, now time.Time) time.Time {
	start := periodStart(resetDay, now)
	if start.IsZero() {
		return start
	}
	return start.AddDate(0, 1, 0)
}

func readShareUsage() map[string]shareUsage {
	usage := map[string]shareUsage{}
	if b, err := os.ReadFile(sharePath(shareUsageFile)); err == nil {
		_ = json.Unmarshal(b, &usage)
	}
	return usage
}

// currentUsage applies a due monthly reset to one account's usage.
func currentUsage(a shareAccount, u shareUsage, now time.Time) shareUsage {
	start := periodStart(a.ResetDay, now)
	if !start.IsZero() && u.PeriodStart.Before(start) {
		u = shareUsage{PeriodStart: start}
	}
	return u
}

// updateShareUsage changes usage under the shared lock.
func updateShareUsage(fn func(map[string]shareUsage)) error {
	path := sharePath(shareUsageFile)
	if path == "" {
		return errors.New("无法确定设置保存位置")
	}
	return withSharedFileLock(path, func() error {
		usage := readShareUsage()
		fn(usage)
		b, _ := json.Marshal(usage)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0600); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	})
}

// ---- metering in this process ----------------------------------------------

var shareMeter struct {
	sync.Mutex
	pending  map[string]int64
	used     map[string]int64 // last flushed totals, for quota checks
	blocked  map[string]*atomic.Bool
	started  bool
	moduleID string
}

func shareBlockFlag(id string) *atomic.Bool {
	shareMeter.Lock()
	defer shareMeter.Unlock()
	if shareMeter.blocked == nil {
		shareMeter.blocked = map[string]*atomic.Bool{}
	}
	f := shareMeter.blocked[id]
	if f == nil {
		f = &atomic.Bool{}
		shareMeter.blocked[id] = f
	}
	return f
}

func shareAdd(id string, n int) {
	shareMeter.Lock()
	if shareMeter.pending == nil {
		shareMeter.pending = map[string]int64{}
	}
	shareMeter.pending[id] += int64(n)
	used := shareMeter.used[id] + shareMeter.pending[id]
	shareMeter.Unlock()
	for _, a := range loadShareAccounts() {
		if a.ID == id && a.QuotaBytes > 0 && used >= a.QuotaBytes {
			shareBlockFlag(id).Store(true)
		}
	}
}

// shareAllowed says whether a friend may use this module now, and why not.
func shareAllowed(a shareAccount, moduleID string, now time.Time) (bool, string) {
	if !a.Enabled {
		return false, "账号已停用"
	}
	if a.Expires != "" {
		if day, err := time.ParseInLocation("2006-01-02", a.Expires, now.Location()); err == nil && !now.Before(day.AddDate(0, 0, 1)) {
			return false, "账号已过期"
		}
	}
	if len(a.Modules) > 0 && moduleID != "" {
		ok := false
		for _, m := range a.Modules {
			ok = ok || m == moduleID
		}
		if !ok {
			return false, "这个账号不能用这个模块"
		}
	}
	if a.QuotaBytes > 0 {
		shareMeter.Lock()
		used := shareMeter.used[a.ID] + shareMeter.pending[a.ID]
		shareMeter.Unlock()
		if used == 0 {
			used = currentUsage(a, readShareUsage()[a.ID], now).Used
		}
		if used >= a.QuotaBytes {
			return false, "流量已用完"
		}
	}
	return true, ""
}

// startShareMeter flushes counted traffic to the shared usage file.
func startShareMeter(moduleID string) {
	shareMeter.Lock()
	if shareMeter.started {
		shareMeter.Unlock()
		return
	}
	shareMeter.started, shareMeter.moduleID = true, moduleID
	shareMeter.Unlock()
	go func() {
		for range time.Tick(shareFlushEvery) {
			flushShareMeter()
		}
	}()
}

func flushShareMeter() {
	shareMeter.Lock()
	pending := shareMeter.pending
	shareMeter.pending = map[string]int64{}
	module := shareMeter.moduleID
	shareMeter.Unlock()
	accounts := loadShareAccounts()
	if len(accounts) == 0 {
		return
	}
	now := time.Now()
	totals := map[string]int64{}
	err := updateShareUsage(func(usage map[string]shareUsage) {
		for _, a := range accounts {
			u := currentUsage(a, usage[a.ID], now)
			if n := pending[a.ID]; n > 0 {
				u.Used += n
				u.LastSeen = now
				if module != "" {
					if u.ByModule == nil {
						u.ByModule = map[string]int64{}
					}
					u.ByModule[module] += n
				}
			}
			usage[a.ID] = u
			totals[a.ID] = u.Used
		}
	})
	shareMeter.Lock()
	if err != nil {
		// Keep the counts for the next try.
		for id, n := range pending {
			shareMeter.pending[id] += n
		}
		shareMeter.Unlock()
		return
	}
	shareMeter.used = totals
	shareMeter.Unlock()
	for _, a := range accounts {
		over := a.QuotaBytes > 0 && totals[a.ID] >= a.QuotaBytes
		ok, _ := shareAllowed(a, "", now)
		shareBlockFlag(a.ID).Store(over || !ok)
	}
}

// meteredConn counts a friend's traffic and ends the connection once the
// friend is blocked (quota used up, account stopped or expired).
type meteredConn struct {
	net.Conn
	id      string
	blocked *atomic.Bool
}

var errShareBlocked = errors.New("share account blocked")

func (c *meteredConn) Read(b []byte) (int, error) {
	if c.blocked.Load() {
		return 0, errShareBlocked
	}
	n, err := c.Conn.Read(b)
	if n > 0 {
		shareAdd(c.id, n)
	}
	return n, err
}

func (c *meteredConn) Write(b []byte) (int, error) {
	if c.blocked.Load() {
		return 0, errShareBlocked
	}
	n, err := c.Conn.Write(b)
	if n > 0 {
		shareAdd(c.id, n)
	}
	return n, err
}

// ---- owner API (7575) ------------------------------------------------------

type shareAccountView struct {
	shareAccount
	Used      int64            `json:"used"`
	ByModule  map[string]int64 `json:"by_module,omitempty"`
	NextReset *time.Time       `json:"next_reset,omitempty"`
	LastSeen  *time.Time       `json:"last_seen,omitempty"`
	Status    string           `json:"status"`
}

type shareOverview struct {
	Accounts   []shareAccountView `json:"accounts"`
	Address    string             `json:"address"`
	PageURL    string             `json:"page_url"`
	PagePort   int                `json:"page_port"`
	PortOffset int                `json:"port_offset"`
	LANSharing bool               `json:"lan_sharing"`
}

func shareViews(now time.Time) []shareAccountView {
	usage := readShareUsage()
	views := []shareAccountView{}
	for _, a := range loadShareAccounts() {
		u := currentUsage(a, usage[a.ID], now)
		v := shareAccountView{shareAccount: a, Used: u.Used, ByModule: u.ByModule, Status: "正常"}
		if r := nextReset(a.ResetDay, now); !r.IsZero() {
			v.NextReset = &r
		}
		if !u.LastSeen.IsZero() {
			ls := u.LastSeen
			v.LastSeen = &ls
		}
		if ok, why := shareAllowed(a, "", now); !ok {
			v.Status = why
		} else if a.QuotaBytes > 0 && u.Used >= a.QuotaBytes {
			v.Status = "流量已用完"
		}
		views = append(views, v)
	}
	return views
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func shareAccountsAPI(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	switch r.Method {
	case http.MethodPost:
		var body shareAccount
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
			writeError(w, 400, "invalid request")
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		body.Username = strings.TrimSpace(body.Username)
		body.Password = strings.TrimSpace(body.Password)
		if body.Name == "" {
			body.Name = body.Username
		}
		if body.Username == "" || strings.ContainsAny(body.Username, " :@/") || len(body.Username) > 64 {
			writeError(w, 400, "用户名只能是字母、数字等，不能有空格、冒号、@ 或 /")
			return
		}
		if body.Password == "" {
			body.Password = randomToken(6)
		}
		if body.QuotaBytes < 0 || body.ResetDay < 0 || body.ResetDay > 28 {
			writeError(w, 400, "流量或重置日期无效")
			return
		}
		if body.Expires != "" {
			if _, err := time.Parse("2006-01-02", body.Expires); err != nil {
				writeError(w, 400, "到期日期格式应为 2026-12-31")
				return
			}
		}
		list := loadShareAccounts()
		if lan := lanShare.get(); lan.Username != "" && lan.Username == body.Username {
			writeError(w, 400, "这个用户名和局域网共享的登录重复了")
			return
		}
		found := false
		for i := range list {
			if list[i].ID != body.ID && list[i].Username == body.Username {
				writeError(w, 400, "用户名已被其他朋友使用")
				return
			}
		}
		for i := range list {
			if body.ID != "" && list[i].ID == body.ID {
				body.Created = list[i].Created
				list[i] = body
				found = true
			}
		}
		if !found {
			body.ID = "f" + randomToken(5)
			body.Created = now
			list = append(list, body)
			// A new period starts with the account.
			_ = updateShareUsage(func(u map[string]shareUsage) { u[body.ID] = shareUsage{PeriodStart: periodStart(body.ResetDay, now)} })
		}
		if err := saveShareAccounts(list); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		log.Printf("share account %s saved (quota %d bytes)", body.Username, body.QuotaBytes)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		list := loadShareAccounts()
		out := list[:0]
		for _, a := range list {
			if a.ID != id {
				out = append(out, a)
			}
		}
		if err := saveShareAccounts(out); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		_ = updateShareUsage(func(u map[string]shareUsage) { delete(u, id) })
	}
	writeJSON(w, 200, shareOverviewFor(r, now))
}

func shareOverviewFor(r *http.Request, now time.Time) shareOverview {
	addr := lanAddress()
	o := shareOverview{Accounts: shareViews(now), Address: addr, PagePort: lanControlPort, PortOffset: sharePortOffset, LANSharing: lanShare.get().Enabled}
	if addr != "" {
		o.PageURL = "http://" + net.JoinHostPort(addr, strconv.Itoa(lanControlPort)) + "/share"
	}
	return o
}

// shareResetAPI zeroes a friend's usage (POST {"id": ...}).
func shareResetAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		writeError(w, 400, "invalid request")
		return
	}
	now := time.Now()
	var reset shareAccount
	for _, a := range loadShareAccounts() {
		if a.ID == body.ID {
			reset = a
		}
	}
	if reset.ID == "" {
		writeError(w, 404, "没有这个账号")
		return
	}
	if err := updateShareUsage(func(u map[string]shareUsage) {
		u[body.ID] = shareUsage{PeriodStart: periodStart(reset.ResetDay, now)}
	}); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, shareOverviewFor(r, now))
}

// shareDeleteAPI removes a friend account (POST {"id": ...}), for clients
// that only send POST.
func shareDeleteAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		writeError(w, 400, "invalid request")
		return
	}
	q := r.URL.Query()
	q.Set("id", body.ID)
	r2 := r.Clone(r.Context())
	r2.Method = http.MethodDelete
	r2.URL.RawQuery = q.Encode()
	shareAccountsAPI(w, r2)
}

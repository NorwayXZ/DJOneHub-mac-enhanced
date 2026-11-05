package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// The IP ledger remembers every exit IP any module ever used, kept forever and
// shared by all modules, so the app can say whether the current IP was used
// before, how often and when. A "use" is one stretch on the IP (one history
// visit); per IP only the latest uses are kept in detail, the count is exact.
type ipUse struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Module string `json:"module,omitempty"`
	Card   string `json:"card,omitempty"`
}

type ipLedgerEntry struct {
	Uses   int     `json:"uses"`
	First  string  `json:"first"`
	Recent []ipUse `json:"recent"`
}

type ipLedgerFile struct {
	Seeded bool                     `json:"seeded"`
	IPs    map[string]ipLedgerEntry `json:"ips"`
}

// ipRecord is what the app shows under the current IP: earlier uses only.
type ipRecord struct {
	Uses   int     `json:"uses"`
	First  string  `json:"first,omitempty"`
	Recent []ipUse `json:"recent,omitempty"`
}

const (
	ipLedgerKeep    = 20
	ipLedgerRefresh = 5 * time.Minute
)

var (
	ipLedgerStore    = settingsFile[ipLedgerFile]{name: "ip-ledger.json"}
	ipLedgerSeedOnce sync.Once
)

// ledgerCard is the current card's name, set by the module's network checks.
var ledgerCard atomic.Value

func currentLedgerCard() string {
	s, _ := ledgerCard.Load().(string)
	return s
}

// addUse records (or extends) the use that started at start. Idempotent per start.
func (f *ipLedgerFile) addUse(ip string, use ipUse) {
	if f.IPs == nil {
		f.IPs = map[string]ipLedgerEntry{}
	}
	e := f.IPs[ip]
	for i := range e.Recent {
		if e.Recent[i].Start == use.Start {
			if use.End > e.Recent[i].End {
				e.Recent[i].End = use.End
			}
			if e.Recent[i].Card == "" {
				e.Recent[i].Card = use.Card
			}
			f.IPs[ip] = e
			return
		}
	}
	e.Uses++
	if e.First == "" || use.Start < e.First {
		e.First = use.Start
	}
	e.Recent = append(e.Recent, use)
	sort.Slice(e.Recent, func(i, j int) bool { return e.Recent[i].Start < e.Recent[j].Start })
	if len(e.Recent) > ipLedgerKeep {
		e.Recent = e.Recent[len(e.Recent)-ipLedgerKeep:]
	}
	f.IPs[ip] = e
}

// noteIPUse records that ip is in use since start, as of now. Writes for a
// running use are spread out to every few minutes.
func noteIPUse(ip, start, now string) {
	if ip == "" || start == "" {
		return
	}
	if e, ok := ipLedgerStore.get().IPs[ip]; ok {
		for _, u := range e.Recent {
			if u.Start == start {
				end, _ := time.Parse(time.RFC3339, u.End)
				at, _ := time.Parse(time.RFC3339, now)
				if at.Sub(end) < ipLedgerRefresh {
					return
				}
			}
		}
	}
	use := ipUse{Start: start, End: now, Module: moduleLabel(), Card: currentLedgerCard()}
	_ = ipLedgerStore.update(func(f *ipLedgerFile) {
		seedIPLedger(f)
		f.addUse(ip, use)
	})
}

// recordForIP: earlier uses of ip, leaving out the one that started at current.
func recordForIP(ip, current string) *ipRecord {
	if ip == "" {
		return nil
	}
	f := ipLedgerStore.get()
	if !f.Seeded {
		ipLedgerSeedOnce.Do(func() { go ipLedgerStore.update(seedIPLedger) })
		return nil // unknown until the old histories are read in
	}
	e, ok := f.IPs[ip]
	if !ok {
		return &ipRecord{}
	}
	r := &ipRecord{First: e.First}
	r.Uses = e.Uses
	for i := len(e.Recent) - 1; i >= 0; i-- {
		if e.Recent[i].Start == current {
			r.Uses--
			continue
		}
		r.Recent = append(r.Recent, e.Recent[i]) // newest first
	}
	if r.Uses == 0 {
		r.First = ""
	}
	return r
}

// seedIPLedger fills a new ledger once from the per-module IP histories already on this Mac.
func seedIPLedger(f *ipLedgerFile) {
	if f.Seeded {
		return
	}
	f.Seeded = true
	root := fleetStateDir()
	if root == "" {
		return
	}
	names := map[string]string{}
	if r, err := loadFleetRegistry(filepath.Join(root, "module-bindings.json")); err == nil && len(r.Bindings) > 1 {
		for _, b := range r.Bindings {
			names[b.ID] = b.Name
		}
	}
	paths, _ := filepath.Glob(filepath.Join(root, "modules", "*", "proxy-ip-history.json"))
	paths = append(paths, filepath.Join(root, "proxy-ip-history.json")) // single-module era; copies of module 1's
	seen := map[[2]string]bool{}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var visits []proxyIPVisit
		if json.Unmarshal(b, &visits) != nil {
			continue
		}
		module := names[filepath.Base(filepath.Dir(path))]
		for _, v := range visits {
			key := [2]string{v.IP, v.FirstSeen}
			if v.IP != "" && v.FirstSeen != "" && !seen[key] {
				seen[key] = true
				f.addUse(v.IP, ipUse{Start: v.FirstSeen, End: v.LastSeen, Module: module})
			}
		}
	}
}

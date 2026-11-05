package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// networkIssue tells the user, in plain words, why the module has no network
// and whose problem it is: the card, the location, or the module.
type networkIssue struct {
	Level  string    `json:"level"` // info, warn, error
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
	Since  time.Time `json:"since"`
}

// networkLoss survives worker restarts (signal recovery reboots the module),
// so "searching for N minutes" keeps counting.
type networkLoss struct {
	LostSince time.Time `json:"lost_since"`
	DeniedAt  time.Time `json:"denied_at,omitempty"`
	// CardWorked: the card was denied after it had worked before, so it has
	// most likely expired or run out of data.
	CardWorked bool `json:"card_worked,omitempty"`
	// Pushed: the reason went to the phone; recovery is pushed too.
	Pushed bool `json:"pushed,omitempty"`
}

const (
	searchGrace  = 90 * time.Second
	deniedMemory = 15 * time.Minute
	// pushLossAfter: how long the module is offline before the phone hears.
	pushLossAfter = 5 * time.Minute
)

var (
	networkIssueMu sync.Mutex
	networkIssueOf *networkIssue
	networkLossOf  *networkLoss
)

func networkLossPath() string { return moduleStatePath("network-loss.json") }

func loadNetworkLossLocked() *networkLoss {
	if networkLossOf != nil {
		return networkLossOf
	}
	networkLossOf = &networkLoss{}
	if path := networkLossPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, networkLossOf)
		}
	}
	return networkLossOf
}

func saveNetworkLossLocked() {
	path := networkLossPath()
	if path == "" {
		return
	}
	if networkLossOf == nil || networkLossOf.LostSince.IsZero() {
		_ = os.Remove(path)
		return
	}
	if data, err := json.Marshal(networkLossOf); err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

func currentNetworkIssue() *networkIssue {
	networkIssueMu.Lock()
	defer networkIssueMu.Unlock()
	issue := networkIssueOf
	if issue == nil {
		issue = dataIssueOf
	}
	if issue == nil {
		return nil
	}
	copied := *issue
	return &copied
}

// noteNetworkRegistered clears the loss; when its reason had been pushed it
// reports how long the module was offline, so the recovery is pushed too.
func noteNetworkRegistered() (offline time.Duration, wasPushed bool) {
	networkIssueMu.Lock()
	defer networkIssueMu.Unlock()
	loss := loadNetworkLossLocked()
	networkIssueOf = nil
	if !loss.LostSince.IsZero() {
		offline, wasPushed = time.Since(loss.LostSince), loss.Pushed
		*loss = networkLoss{}
		saveNetworkLossLocked()
	}
	return offline, wasPushed
}

// takeLossPush returns the reason to push once the module has been offline
// for pushLossAfter, at most once per loss.
func takeLossPush(now time.Time) *networkIssue {
	networkIssueMu.Lock()
	defer networkIssueMu.Unlock()
	loss := loadNetworkLossLocked()
	if networkIssueOf == nil || loss.LostSince.IsZero() || loss.Pushed || now.Sub(loss.LostSince) < pushLossAfter {
		return nil
	}
	loss.Pushed = true
	saveNetworkLossLocked()
	issue := *networkIssueOf
	return &issue
}

// noteNetworkLost records one failed registration check: reg is the +CREG /
// +CEREG state (3 = denied), signal is in dBm (0 = none).
func noteNetworkLost(reg, signal int, simReady, cardWorked bool) {
	networkIssueMu.Lock()
	defer networkIssueMu.Unlock()
	now := time.Now()
	loss := loadNetworkLossLocked()
	changed := false
	if loss.LostSince.IsZero() {
		loss.LostSince, changed = now, true
	}
	if reg == 3 {
		loss.DeniedAt, changed = now, true
		if cardWorked && !loss.CardWorked {
			loss.CardWorked = true
		}
	}
	if changed {
		saveNetworkLossLocked()
	}
	networkIssueOf = diagnoseNetwork(now, loss, signal, simReady)
}

func diagnoseNetwork(now time.Time, loss *networkLoss, signal int, simReady bool) *networkIssue {
	issue := &networkIssue{Since: loss.LostSince}
	switch {
	case !simReady:
		issue.Level, issue.Title = "error", "模块里没有可用的号码"
		issue.Detail = "SIM 卡没插好，或者 eSIM 卡里没有启用的号码。到「模块与网络 → eSIM / 卡片」启用一个号码。"
	case !loss.DeniedAt.IsZero() && now.Sub(loss.DeniedAt) < deniedMemory && loss.CardWorked:
		issue.Level, issue.Title = "error", "这张卡可能已失效"
		issue.Detail = "这张卡之前能正常上网，现在运营商拒绝它注册，多半是套餐到期或流量用完了。模块是好的：换一个号码，或给这张卡续费后就能恢复。"
	case !loss.DeniedAt.IsZero() && now.Sub(loss.DeniedAt) < deniedMemory:
		issue.Level, issue.Title = "error", "运营商拒绝了这张卡"
		issue.Detail = "模块正常，也搜到了网络，但运营商不让这张卡注册。常见原因：这张卡不支持在当前国家或地区使用（不支持漫游）、套餐还没开通或已过期。请向卖家或官网确认。"
	case now.Sub(loss.LostSince) < searchGrace:
		issue.Level, issue.Title = "info", "正在搜索网络"
		issue.Detail = "模块正在找这张卡能用的网络，一般 1 分钟内能连上。"
	case signal != 0:
		issue.Level, issue.Title = "warn", "能收到信号，但这张卡一直注册不上"
		issue.Detail = "模块和天线都正常，多半是这张卡在当前地区不能用（不支持漫游、未开通或已过期）。DJOneHub 会继续自动重试。"
	default:
		issue.Level, issue.Title = "warn", "收不到这张卡能用的网络"
		issue.Detail = "附近没有这张卡支持的网络，或者位置信号太差。可以换个位置；也可能是这张卡不支持当前地区。DJOneHub 会继续自动重试。"
	}
	return issue
}

// dataIssueOf: the module is registered but nothing gets through, which is
// how many travel cards behave once their data is used up or expired.
var (
	dataIssueOf   *networkIssue
	dataFailSince time.Time
	dataPushed    bool
)

// dataBlockedAfter: failed exit checks for this long while registered.
const dataBlockedAfter = 5 * time.Minute

// noteDataPath records one exit check through the cellular proxy. It returns
// the issue to push when the data path has just been judged blocked, and
// whether a pushed block has just ended.
func noteDataPath(ok, cardWorked bool, now time.Time) (push *networkIssue, recovered bool) {
	networkIssueMu.Lock()
	defer networkIssueMu.Unlock()
	if ok {
		recovered = dataPushed
		dataIssueOf, dataFailSince, dataPushed = nil, time.Time{}, false
		return nil, recovered
	}
	if dataFailSince.IsZero() {
		dataFailSince = now
	}
	if now.Sub(dataFailSince) < dataBlockedAfter || networkIssueOf != nil {
		return nil, false
	}
	issue := &networkIssue{Level: "warn", Title: "连上了网络，但上不了网", Since: dataFailSince,
		Detail: "模块已注册到运营商，但流量走不出去。常见原因：流量用完、套餐到期或还没开通。模块是好的，可以换一个号码试试。"}
	if cardWorked {
		issue.Level, issue.Title = "error", "这张卡可能已失效"
		issue.Detail = "这张卡之前能正常上网，现在连得上运营商却上不了网，多半是流量用完或套餐到期。模块是好的：换一个号码，或给这张卡续费后就能恢复。"
	}
	dataIssueOf = issue
	if dataPushed {
		return nil, false
	}
	dataPushed = true
	copied := *issue
	return &copied, false
}

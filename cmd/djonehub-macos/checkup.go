package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// One-click checkup: every part a module needs, checked in order, with a one
// line verdict that says which part is at fault.
type checkupItem struct {
	Name   string `json:"name"`
	Level  string `json:"level"` // ok, warn, fail, off
	Detail string `json:"detail"`
}

type checkupModule struct {
	ID      string        `json:"id,omitempty"`
	Name    string        `json:"name,omitempty"`
	Level   string        `json:"level"`
	Summary string        `json:"summary"`
	Items   []checkupItem `json:"items"`
	Seconds float64       `json:"seconds"`
}

func (m *checkupModule) add(name, level, detail string) {
	m.Items = append(m.Items, checkupItem{Name: name, Level: level, Detail: detail})
}

// finish sets the verdict from the first failing item, then the first warning.
func (m *checkupModule) finish(started time.Time) {
	m.Seconds = float64(time.Since(started).Milliseconds()) / 1000
	m.Level, m.Summary = "ok", "全部正常"
	for _, want := range []string{"fail", "warn"} {
		for _, it := range m.Items {
			if it.Level == want {
				m.Level, m.Summary = want, it.Name+"："+it.Detail
				return
			}
		}
	}
}

func signalWords(dbm int) (string, string) {
	switch {
	case dbm == 0:
		return "fail", "收不到信号"
	case dbm >= -85:
		return "ok", fmt.Sprintf("信号好（%d dBm）", dbm)
	case dbm >= -100:
		return "ok", fmt.Sprintf("信号一般（%d dBm）", dbm)
	default:
		return "warn", fmt.Sprintf("信号弱（%d dBm），可以换个位置或接好天线", dbm)
	}
}

func (a *app) checkup(ctx context.Context) (m checkupModule) {
	started := time.Now()
	defer func() { m.finish(started) }()

	if err := a.ensureUSBAT(); err != nil || a.usbAT == nil {
		m.add("模块连接", "fail", "Mac 和模块说不上话。请检查 USB 线和分线器，或拔插一次模块。")
		return m
	}
	a.forgetStatus()
	t := time.Now()
	status, err := a.usbATStatus()
	if err != nil {
		a.resetUSBATIfGone(err)
		m.add("模块连接", "fail", "模块没有回应："+err.Error())
		return m
	}
	m.add("模块连接", "ok", fmt.Sprintf("模块正常回应（%d 毫秒）", time.Since(t).Milliseconds()))

	if !status.SimInserted {
		m.add("卡片", "fail", "模块里没有可用的号码：SIM 卡没插好，或 eSIM 卡里没有启用的号码。")
		return m
	}
	card := cardName(status.ICCID)
	if c := cardCapabilityFor(status.ICCID); c != nil {
		card += " · " + c.Title
	}
	m.add("卡片", "ok", card)

	registered := status.RegStatus == 1 || status.RegStatus == 5
	if registered {
		level, words := signalWords(status.SignalDBM)
		if level == "fail" {
			level = "warn"
		}
		m.add("网络", level, fmt.Sprintf("已连上 %s %s，%s", status.Operator, status.NetworkMode, words))
	} else if issue := currentNetworkIssue(); issue != nil {
		m.add("网络", "fail", issue.Title+"。"+issue.Detail)
		return m
	} else {
		_, words := signalWords(status.SignalDBM)
		m.add("网络", "fail", "还没连上运营商网络（"+status.RegStatusText+"），"+words)
		return m
	}

	iface, err := proxyInterface()
	if err != nil {
		m.add("Mac 网卡", "fail", friendlyProxyError(err))
		return m
	}
	m.add("Mac 网卡", "ok", iface+" 已拿到地址")

	proxy := a.proxy.snapshot()
	if proxy.Running {
		m.add("蜂窝代理", "ok", "已开启："+proxy.Address)
	} else {
		m.add("蜂窝代理", "off", "没有开启（需要时在代理页开启）")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	t = time.Now()
	ip, _, err := watchExitIP(probeCtx, iface, proxy.IPSource)
	if err != nil {
		if issue := currentNetworkIssue(); issue != nil {
			m.add("上网测试", "fail", issue.Title+"。"+issue.Detail)
		} else {
			m.add("上网测试", "fail", "连上了运营商，但流量走不出去：可能流量用完、套餐到期或未开通。")
		}
		return m
	}
	ms := time.Since(t).Milliseconds()
	level := "ok"
	words := fmt.Sprintf("出口 %s，用时 %d 毫秒", ip, ms)
	if ms > 3000 {
		level, words = "warn", words+"，网速偏慢"
	}
	m.add("上网测试", level, words)
	return m
}

func (a *app) checkupModuleAPI(w http.ResponseWriter, r *http.Request) {
	if a.demo {
		m := checkupModule{Level: "ok", Summary: "全部正常", Items: []checkupItem{{"模块连接", "ok", "演示模式"}}}
		writeJSON(w, 200, m)
		return
	}
	writeJSON(w, 200, a.checkup(r.Context()))
}

func (a *app) checkupAPI(w http.ResponseWriter, r *http.Request) {
	m := checkupModule{Level: "ok", Summary: "全部正常", Items: []checkupItem{{"模块连接", "ok", "演示模式"}}}
	if !a.demo {
		m = a.checkup(r.Context())
	}
	writeJSON(w, 200, map[string]any{"modules": []checkupModule{m}})
}

// fleetCheckup checks every module at once and adds what only the supervisor
// sees: how often each module dropped off USB in the last hour.
func (c *fleetController) fleetCheckup(w http.ResponseWriter, r *http.Request) {
	snap := c.snapshot()
	out := make([]checkupModule, len(snap.Modules))
	var wg sync.WaitGroup
	for i, mod := range snap.Modules {
		out[i] = checkupModule{ID: mod.ID, Name: mod.Name}
		if !mod.Connected {
			out[i].add("模块连接", "fail", "没有检测到这个模块：没插上、线松了，或分线器没供上电。")
			out[i].finish(time.Now())
			continue
		}
		c.mu.Lock()
		worker := c.workers[mod.ID]
		c.mu.Unlock()
		if worker == nil {
			level, detail := "warn", "模块正在启动，请几秒后再试。"
			if mod.NeedsConfirmation {
				detail = "这个模块还没确认绑定，请在代理页确认。"
			}
			out[i].add("模块连接", level, detail)
			out[i].finish(time.Now())
			continue
		}
		wg.Add(1)
		go func(i int, worker fleetProcess) {
			defer wg.Done()
			code, body, err := worker.Request("GET", "/api/checkup/module", nil)
			var m checkupModule
			if err != nil || code != 200 || json.Unmarshal(body, &m) != nil {
				out[i].add("模块连接", "fail", "模块后台没有回应，正在自动重启它。")
				out[i].finish(time.Now())
				return
			}
			m.ID, m.Name = out[i].ID, out[i].Name
			out[i] = m
		}(i, worker)
	}
	wg.Wait()
	for i := range out {
		if !snap.Modules[i].Connected {
			continue
		}
		if n := c.recentReconnects(out[i].ID, time.Hour); n >= 3 {
			out[i].Items = append(out[i].Items, checkupItem{Name: "USB 稳定", Level: "warn",
				Detail: fmt.Sprintf("过去 1 小时 USB 断开又接上 %d 次。如果这段时间你没有切换号码或重启模块，多半是分线器供电不足或线松了，建议换带电源的分线器。", n)})
			seconds := out[i].Seconds
			out[i].finish(time.Now())
			out[i].Seconds = seconds
		} else {
			out[i].Items = append(out[i].Items, checkupItem{Name: "USB 稳定", Level: "ok", Detail: "过去 1 小时没有异常断开"})
		}
	}
	writeJSON(w, 200, map[string]any{"modules": out})
}

// noteReconnectLocked remembers when a module came back on USB.
func (c *fleetController) noteReconnectLocked(id string, now time.Time) {
	kept := c.reconnects[id][:0]
	for _, t := range c.reconnects[id] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	c.reconnects[id] = append(kept, now)
}

func (c *fleetController) recentReconnects(id string, within time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.reconnects[id] {
		if time.Since(t) < within {
			n++
		}
	}
	return n
}

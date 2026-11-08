package main

import (
	"testing"
	"time"
)

func TestDiagnoseNetworkNamesWhoseProblemItIs(t *testing.T) {
	now := time.Now()
	long := &networkLoss{LostSince: now.Add(-5 * time.Minute)}
	cases := []struct {
		name   string
		loss   *networkLoss
		signal int
		ready  bool
		title  string
	}{
		{"no sim", long, -80, false, "模块里没有可用的号码"},
		{"denied", &networkLoss{LostSince: now.Add(-time.Minute), DeniedAt: now.Add(-time.Minute)}, -80, true, "运营商拒绝了这张卡"},
		{"just lost", &networkLoss{LostSince: now.Add(-20 * time.Second)}, 0, true, "正在搜索网络"},
		{"signal but no registration", long, -95, true, "能收到信号，但这张卡一直注册不上"},
		{"no signal", long, 0, true, "收不到这张卡能用的网络"},
		{"expired", &networkLoss{LostSince: now.Add(-time.Minute), DeniedAt: now.Add(-time.Minute), CardWorked: true}, -80, true, "这张卡可能已失效"},
		{"old denial forgotten", &networkLoss{LostSince: now.Add(-time.Hour), DeniedAt: now.Add(-time.Hour)}, 0, true, "收不到这张卡能用的网络"},
	}
	for _, c := range cases {
		if got := diagnoseNetwork(now, c.loss, c.signal, c.ready); got.Title != c.title {
			t.Errorf("%s: got %q, want %q", c.name, got.Title, c.title)
		}
	}
}

func TestDataStallReconnectsBeforeBlamingTheCard(t *testing.T) {
	t.Cleanup(func() { dataIssueOf, dataFailSince, dataPushed, dataFixes = nil, time.Time{}, false, 0 })
	now := time.Now()
	fixes := 0
	var push *networkIssue
	// Checks every 20 s for 12 minutes: three reconnects, then one push.
	for at := time.Duration(0); at <= 12*time.Minute; at += 20 * time.Second {
		p, _, fix := noteDataPath(false, true, now.Add(-time.Hour), now.Add(at))
		if fix {
			fixes++
		}
		if p != nil {
			if push != nil {
				t.Fatal("pushed twice")
			}
			if fixes < dataFixesBeforeBlame {
				t.Fatalf("blamed the card after %d reconnects", fixes)
			}
			push = p
		}
	}
	if fixes != 3 || push == nil || push.Title != "这张卡可能已失效" {
		t.Fatalf("fixes=%d push=%+v", fixes, push)
	}
	if _, _, fix := noteDataPath(false, true, now.Add(-time.Hour), now.Add(23*time.Minute)); !fix {
		t.Fatal("stopped reconnecting after blaming the card")
	}
	if _, recovered, _ := noteDataPath(true, true, now.Add(-time.Hour), now.Add(24*time.Minute)); !recovered {
		t.Fatal("recovery not reported")
	}
}

func TestShortDataStallIsFixedWithoutAPush(t *testing.T) {
	t.Cleanup(func() { dataIssueOf, dataFailSince, dataPushed, dataFixes = nil, time.Time{}, false, 0 })
	now := time.Now()
	running := now.Add(-time.Hour)
	if _, _, fix := noteDataPath(false, true, running, now); fix {
		t.Fatal("reconnected on the first failure")
	}
	if _, _, fix := noteDataPath(false, true, running, now.Add(40*time.Second)); fix {
		t.Fatal("reconnected before a minute")
	}
	if _, _, fix := noteDataPath(false, true, running, now.Add(60*time.Second)); !fix {
		t.Fatal("no reconnect after a minute")
	}
	if p, recovered, _ := noteDataPath(true, true, running, now.Add(80*time.Second)); p != nil || recovered {
		t.Fatal("a fixed stall must not notify")
	}
}

func TestFreshProxyFailuresAreNotAStall(t *testing.T) {
	t.Cleanup(func() { dataIssueOf, dataFailSince, dataPushed, dataFixes = nil, time.Time{}, false, 0 })
	start := time.Now()
	// Checks every 15 s while a restarted proxy settles for 40 s.
	for at := time.Duration(0); at <= 40*time.Second; at += 15 * time.Second {
		noteDataPath(false, true, start, start.Add(at))
	}
	if dataStalled() {
		t.Fatal("startup failures started the stall clock")
	}
	if _, _, fix := noteDataPath(false, true, start, start.Add(50*time.Second)); fix {
		t.Fatal("reconnected right after the settle time")
	}
	if _, _, fix := noteDataPath(false, true, start, start.Add(110*time.Second)); !fix {
		t.Fatal("a real stall after start was not reconnected")
	}
}

func TestCheckupVerdictNamesTheFirstProblem(t *testing.T) {
	m := checkupModule{}
	m.add("模块连接", "ok", "好")
	m.add("网络", "warn", "信号弱")
	m.add("上网测试", "fail", "走不出去")
	m.finish(time.Now())
	if m.Level != "fail" || m.Summary != "上网测试：走不出去" {
		t.Fatalf("%+v", m)
	}
}

func TestUnreachableExitServiceGoesLast(t *testing.T) {
	t.Cleanup(func() { exitProviderFailed.Clear() })
	now := time.Now()
	if got := exitProviderOrder("", now)[0].source; got != "ip.3322.net" {
		t.Fatal(got)
	}
	if got := exitProviderOrder("ddns.oray.com", now)[0].source; got != "ddns.oray.com" {
		t.Fatal(got)
	}
	exitProviderFailed.Store("ip.3322.net", now)
	order := exitProviderOrder("ip.3322.net", now)
	if order[0].source == "ip.3322.net" || order[len(order)-1].source != "ip.3322.net" {
		t.Fatalf("a failing service stayed in front: %v", order)
	}
	if got := exitProviderOrder("ip.3322.net", now.Add(11*time.Minute))[0].source; got != "ip.3322.net" {
		t.Fatal("penalty never expires")
	}
}

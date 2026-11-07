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
		p, _, fix := noteDataPath(false, true, now.Add(at))
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
	if _, _, fix := noteDataPath(false, true, now.Add(23*time.Minute)); !fix {
		t.Fatal("stopped reconnecting after blaming the card")
	}
	if _, recovered, _ := noteDataPath(true, true, now.Add(24*time.Minute)); !recovered {
		t.Fatal("recovery not reported")
	}
}

func TestShortDataStallIsFixedWithoutAPush(t *testing.T) {
	t.Cleanup(func() { dataIssueOf, dataFailSince, dataPushed, dataFixes = nil, time.Time{}, false, 0 })
	now := time.Now()
	if _, _, fix := noteDataPath(false, true, now); fix {
		t.Fatal("reconnected on the first failure")
	}
	if _, _, fix := noteDataPath(false, true, now.Add(40*time.Second)); !fix {
		t.Fatal("no reconnect after 40 s")
	}
	if p, recovered, _ := noteDataPath(true, true, now.Add(80*time.Second)); p != nil || recovered {
		t.Fatal("a fixed stall must not notify")
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

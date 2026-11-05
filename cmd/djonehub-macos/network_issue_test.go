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

func TestDataPathBlockedIsPushedOnceAndRecovers(t *testing.T) {
	t.Cleanup(func() { dataIssueOf, dataFailSince, dataPushed = nil, time.Time{}, false })
	now := time.Now()
	if push, _ := noteDataPath(false, true, now); push != nil {
		t.Fatal("pushed on the first failure")
	}
	push, _ := noteDataPath(false, true, now.Add(6*time.Minute))
	if push == nil || push.Title != "这张卡可能已失效" {
		t.Fatalf("want expiry push, got %+v", push)
	}
	if again, _ := noteDataPath(false, true, now.Add(12*time.Minute)); again != nil {
		t.Fatal("pushed twice")
	}
	if _, recovered := noteDataPath(true, true, now.Add(13*time.Minute)); !recovered {
		t.Fatal("recovery not reported")
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

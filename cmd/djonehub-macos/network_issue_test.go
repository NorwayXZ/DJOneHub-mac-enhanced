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
		{"old denial forgotten", &networkLoss{LostSince: now.Add(-time.Hour), DeniedAt: now.Add(-time.Hour)}, 0, true, "收不到这张卡能用的网络"},
	}
	for _, c := range cases {
		if got := diagnoseNetwork(now, c.loss, c.signal, c.ready); got.Title != c.title {
			t.Errorf("%s: got %q, want %q", c.name, got.Title, c.title)
		}
	}
}

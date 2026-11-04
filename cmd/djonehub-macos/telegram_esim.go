package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/esim"
)

// /esim lists the numbers on each module's eSIM card; /esim N (or /esim M N
// with several modules) switches to one and reports the new exit IP.

const esimSwitchFollow = 5 * time.Minute

// esimSwitchSettle: the module is restarting; don't read the old exit IP.
var esimSwitchSettle = 20 * time.Second

type botProfile struct {
	esim.ProfileItem
	AID string
}

// botCardProfiles reads a module's eSIM card; nil with a reason otherwise.
func botCardProfiles(backend botBackend, m botModule) ([]botProfile, string) {
	code, data, err := backend.call(m.ID, "GET", "/api/esim", nil)
	if err != nil {
		return nil, err.Error()
	}
	if code != 200 {
		return nil, apiErrorText(data)
	}
	var overview struct {
		esim.EsimOverview
		CardType string `json:"card_type"`
	}
	if json.Unmarshal(data, &overview) != nil {
		return nil, "读取失败"
	}
	if overview.CardType == "physical_sim" {
		return nil, "实体卡，不是 eSIM"
	}
	out := []botProfile{}
	for _, group := range overview.Profiles {
		for _, p := range group.Profiles {
			out = append(out, botProfile{ProfileItem: p, AID: group.AIDHex})
		}
	}
	if len(out) == 0 {
		return nil, "eSIM 卡上没有号码"
	}
	return out, ""
}

func profileTitle(p esim.ProfileItem) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = strings.TrimSpace(p.ServiceProviderName)
	}
	if name == "" {
		name = "未命名"
	}
	return fmt.Sprintf("%s（%s）", name, shortICCID(p.ICCID))
}

func botESIM(ctx context.Context, backend botBackend, args []string) string {
	mods := connectedModules(backend)
	if len(mods) == 0 {
		return "没有接入模块。"
	}
	if len(args) == 0 {
		return botESIMList(backend, mods)
	}
	nums := []int{}
	for _, a := range args {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 {
			return "格式：/esim 号码编号" + multiHint(mods)
		}
		nums = append(nums, n)
	}
	module, pick := mods[0], nums[0]
	switch {
	case len(nums) >= 2:
		if nums[0] > len(mods) {
			return "没有这个模块。发 /esim 看编号。"
		}
		module, pick = mods[nums[0]-1], nums[1]
	case len(mods) > 1:
		return "有多个模块，请写上模块编号：/esim 模块 号码，例如 /esim 2 1"
	}
	profiles, reason := botCardProfiles(backend, module)
	if profiles == nil {
		return module.Name + "：" + reason
	}
	if pick > len(profiles) {
		return "没有这个号码。发 /esim 看编号。"
	}
	target := profiles[pick-1]
	if target.State == 1 {
		return "已经在用 " + profileTitle(target.ProfileItem) + "。"
	}
	return botSwitchProfile(ctx, backend, module, target)
}

func multiHint(mods []botModule) string {
	if len(mods) > 1 {
		return "（多个模块时：/esim 模块 号码）"
	}
	return ""
}

func botESIMList(backend botBackend, mods []botModule) string {
	lines := []string{}
	for i, m := range mods {
		header := m.Name
		if len(mods) > 1 {
			header = fmt.Sprintf("%d. %s", i+1, m.Name)
		}
		profiles, reason := botCardProfiles(backend, m)
		if profiles == nil {
			lines = append(lines, header+"："+reason)
			continue
		}
		lines = append(lines, header+"：")
		for j, p := range profiles {
			mark := "▫️"
			if p.State == 1 {
				mark = "✅"
			}
			lines = append(lines, fmt.Sprintf("  %d. %s %s", j+1, mark, profileTitle(p.ProfileItem)))
		}
	}
	if len(mods) > 1 {
		lines = append(lines, "\n切换：/esim 模块 号码，例如 /esim 1 2")
	} else {
		lines = append(lines, "\n切换：/esim 号码，例如 /esim 2")
	}
	return strings.Join(lines, "\n")
}

func botSwitchProfile(ctx context.Context, backend botBackend, m botModule, target botProfile) string {
	var before proxyState
	if _, data, err := backend.call(m.ID, "GET", "/api/proxy", nil); err == nil {
		_ = json.Unmarshal(data, &before)
	}
	body, _ := json.Marshal(map[string]string{"iccid": target.ICCID, "aid": target.AID})
	code, data, err := backend.call(m.ID, "POST", "/api/esim/switch", body)
	title := profileTitle(target.ProfileItem)
	if err != nil {
		return "切换失败：" + err.Error()
	}
	if code != 200 {
		return "切换失败：" + apiErrorText(data)
	}
	// The module restarts and the proxy comes back on the new number.
	started := time.Now()
	deadline := started.Add(esimSwitchFollow)
	sleepCtx(ctx, esimSwitchSettle)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		var s proxyState
		if _, data, err := backend.call(m.ID, "GET", "/api/proxy", nil); err == nil && json.Unmarshal(data, &s) == nil &&
			s.Interface != "" && s.PublicIP != "" && s.PublicIP != before.PublicIP {
			return fmt.Sprintf("✅ %s 已切换到 %s\n出口 IP：%s → %s（%.0f 秒）", m.Name, title, orDash(before.PublicIP), s.PublicIP, time.Since(started).Seconds())
		}
		sleepCtx(ctx, 5*time.Second)
	}
	return fmt.Sprintf("已切换到 %s，网络还在恢复，稍后发 /ip 查看。", title)
}

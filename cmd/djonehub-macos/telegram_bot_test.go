package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeBotBackend struct {
	mods  []botModule
	calls []string
	proxy proxyState
	esim  string
	// switched is the exit IP the proxy reports after an eSIM switch.
	switched string
}

func (f *fakeBotBackend) modules() []botModule { return f.mods }
func (f *fakeBotBackend) call(module, method, path string, body []byte) (int, []byte, error) {
	f.calls = append(f.calls, module+" "+method+" "+path+" "+string(body))
	if path == "/api/proxy" {
		b, _ := json.Marshal(f.proxy)
		return 200, b, nil
	}
	if path == "/api/esim" {
		return 200, []byte(f.esim), nil
	}
	if path == "/api/esim/switch" {
		f.proxy.PublicIP = f.switched
		return 200, []byte(`{"switch_accepted":true}`), nil
	}
	if strings.HasPrefix(path, "/api/proxy/rotate") {
		return 200, []byte(`{"ok":true,"phase":"changed","previous_ip":"1.1.1.1","ip":"2.2.2.2","seconds":8}`), nil
	}
	return 200, []byte(`{"sent":true}`), nil
}

func useTempBotState(t *testing.T) {
	telegramBot.path = filepath.Join(t.TempDir(), "telegram-bot.json")
	telegramBot.modTime, telegramBot.value = time.Time{}, botState{}
}

func botMessage(text string, replyTo int64) tgUpdate {
	var u tgUpdate
	raw := map[string]any{"update_id": 1, "message": map[string]any{"message_id": 99, "text": text, "chat": map[string]any{"id": 42}}}
	if replyTo != 0 {
		raw["message"].(map[string]any)["reply_to_message"] = map[string]any{"message_id": replyTo}
	}
	b, _ := json.Marshal(raw)
	_ = json.Unmarshal(b, &u)
	return u
}

func TestReplyToPushedSMSGoesBackThroughTheSameModule(t *testing.T) {
	useTempBotState(t)
	rememberSMSPush(500, "slot-b", "+447700900123")
	f := &fakeBotBackend{mods: []botModule{{ID: "slot-a", Name: "模块 1", Connected: true}, {ID: "slot-b", Name: "模块 2", Connected: true}}}
	reply := handleBotMessage(context.Background(), f, botMessage("好的，马上到", 500))
	if !strings.Contains(reply, "已发送给 +447700900123") || len(f.calls) != 1 {
		t.Fatal(reply, f.calls)
	}
	if !strings.HasPrefix(f.calls[0], "slot-b POST /api/sms/send") || !strings.Contains(f.calls[0], "好的，马上到") {
		t.Fatal(f.calls[0])
	}
	// A reply to something that is not a pushed SMS is not sent anywhere.
	if reply := handleBotMessage(context.Background(), f, botMessage("hi", 777)); !strings.Contains(reply, "没法回复") || len(f.calls) != 1 {
		t.Fatal(reply)
	}
}

func TestBotCommandsPickModulesByNumber(t *testing.T) {
	useTempBotState(t)
	f := &fakeBotBackend{mods: []botModule{{ID: "slot-a", Name: "模块 1", Connected: true}, {ID: "slot-b", Name: "模块 2", Connected: true}}}
	if reply := handleBotMessage(context.Background(), f, botMessage("/rotate 2", 0)); !strings.Contains(reply, "1.1.1.1 → 2.2.2.2") {
		t.Fatal(reply)
	}
	if !strings.HasPrefix(f.calls[0], "slot-b GET /api/proxy/rotate?wait=24 ") {
		t.Fatal(f.calls)
	}
	f.calls = nil
	handleBotMessage(context.Background(), f, botMessage("/clean", 0))
	if !strings.HasPrefix(f.calls[0], "slot-a GET /api/proxy/rotate?wait=24&clean=1") {
		t.Fatal(f.calls)
	}
	f.calls = nil
	handleBotMessage(context.Background(), f, botMessage("/send 2 10086 查余额", 0))
	if !strings.HasPrefix(f.calls[0], "slot-b POST /api/sms/send") || !strings.Contains(f.calls[0], `"phone":"10086"`) {
		t.Fatal(f.calls)
	}
	if reply := handleBotMessage(context.Background(), f, botMessage("/rotate 5", 0)); !strings.Contains(reply, "没有这个模块") {
		t.Fatal(reply)
	}
}

func TestBotStatusShowsIPAndScores(t *testing.T) {
	score := 12
	f := &fakeBotBackend{mods: []botModule{{Name: "模块", Socks: "127.0.0.1:10909", Connected: true}},
		proxy: proxyState{Running: true, PublicIP: "111.55.210.82", History: []proxyIPVisit{{IP: "111.55.210.82", IPQSScore: &score}}}}
	if reply := botStatus(f); !strings.Contains(reply, "111.55.210.82") || !strings.Contains(reply, "IPQS 12") {
		t.Fatal(reply)
	}
}

func TestKeepAliveDueAndDefaults(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	p := keepAlivePlan{Enabled: true, Days: 90, LastDone: now.Add(-89 * 24 * time.Hour).Format(time.RFC3339)}
	if p.due(now) {
		t.Fatal("due a day early")
	}
	p.LastDone = now.Add(-91 * 24 * time.Hour).Format(time.RFC3339)
	if !p.due(now) {
		t.Fatal("not due after the interval")
	}
	if !(keepAlivePlan{Enabled: true}).due(now) || (keepAlivePlan{}).due(now) {
		t.Fatal("a new enabled plan sends at once; a disabled one never")
	}
	if (keepAlivePlan{}).days() != defaultKeepAliveDay {
		t.Fatal("default interval")
	}
}

func TestCleanLimitsDefault(t *testing.T) {
	if i, s := (cleanSettings{}).limits(); i != defaultCleanIPQS || s != defaultCleanScam {
		t.Fatal(i, s)
	}
	if i, _ := (cleanSettings{MaxIPQS: 50}).limits(); i != 50 {
		t.Fatal(i)
	}
}

const xesimOverview = `{"profiles":[{"eid":"8904","aid_hex":"A0000005591010FFFFFFFF8900000177","profiles":[
 {"iccid":"8932000000000000901","name":"ESIM_ACCESS_CARRIER","state":0},
 {"iccid":"8901000000000007510","name":"I320_RedPocket","state":1}]}]}`

func TestESIMCommandListsAndSwitchesProfiles(t *testing.T) {
	esimSwitchSettle = 0
	f := &fakeBotBackend{mods: []botModule{{ID: "a", Name: "模块", Connected: true}}, esim: xesimOverview, switched: "9.9.9.9"}
	f.proxy.PublicIP, f.proxy.Interface = "1.1.1.1", "en15"
	list := handleBotMessage(context.Background(), f, botMessage("/esim", 0))
	if !strings.Contains(list, "1. ▫️ ESIM_ACCESS_CARRIER（…000901）") || !strings.Contains(list, "2. ✅ I320_RedPocket") {
		t.Fatal(list)
	}
	if reply := handleBotMessage(context.Background(), f, botMessage("/esim 2", 0)); !strings.Contains(reply, "已经在用") {
		t.Fatal(reply)
	}
	reply := handleBotMessage(context.Background(), f, botMessage("/esim 1", 0))
	if !strings.Contains(reply, "已切换到 ESIM_ACCESS_CARRIER") || !strings.Contains(reply, "1.1.1.1 → 9.9.9.9") {
		t.Fatal(reply)
	}
	var switched string
	for _, c := range f.calls {
		if strings.Contains(c, "POST /api/esim/switch") {
			switched = c
		}
	}
	if !strings.Contains(switched, `"iccid":"8932000000000000901"`) || !strings.Contains(switched, `"aid":"A0000005591010FFFFFFFF8900000177"`) {
		t.Fatal(f.calls)
	}
}

func TestESIMCommandNeedsModuleNumberWithSeveralModules(t *testing.T) {
	f := &fakeBotBackend{mods: []botModule{{ID: "a", Name: "模块 1", Connected: true}, {ID: "b", Name: "模块 2", Connected: true}}, esim: `{"card_type":"physical_sim"}`}
	if reply := handleBotMessage(context.Background(), f, botMessage("/esim 2", 0)); !strings.Contains(reply, "/esim 模块 号码") {
		t.Fatal(reply)
	}
	if reply := handleBotMessage(context.Background(), f, botMessage("/esim", 0)); !strings.Contains(reply, "1. 模块 1：实体卡") {
		t.Fatal(reply)
	}
}

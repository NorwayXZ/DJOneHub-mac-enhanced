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
}

func (f *fakeBotBackend) modules() []botModule { return f.mods }
func (f *fakeBotBackend) call(module, method, path string, body []byte) (int, []byte, error) {
	f.calls = append(f.calls, module+" "+method+" "+path+" "+string(body))
	if path == "/api/proxy" {
		b, _ := json.Marshal(f.proxy)
		return 200, b, nil
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

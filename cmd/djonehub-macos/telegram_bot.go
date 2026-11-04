package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
)

// The Telegram bot that pushes SMS also takes orders from the user's chat:
// replying to a pushed SMS sends the reply from the SIM that received it, and
// a few commands report and switch exit IPs. It runs once per Mac, in the
// single-module backend or the multi-module supervisor, and only obeys the
// configured chat.

const (
	telegramPollTimeout = 50 // seconds Telegram holds a getUpdates request
	smsPushMemory       = 500
)

type smsPush struct {
	MessageID int64  `json:"message_id"`
	Module    string `json:"module"`
	Sender    string `json:"sender"`
	At        string `json:"at"`
}

type botState struct {
	Offset int64     `json:"offset"`
	Pushes []smsPush `json:"pushes"`
}

var telegramBot = settingsFile[botState]{name: "telegram-bot.json"}

// rememberSMSPush maps a pushed SMS's Telegram message to the module and
// sender, so a reply to it can be sent back as an SMS.
func rememberSMSPush(messageID int64, module, sender string) {
	if messageID == 0 || sender == "" {
		return
	}
	_ = telegramBot.update(func(s *botState) {
		s.Pushes = append(s.Pushes, smsPush{MessageID: messageID, Module: module, Sender: sender, At: time.Now().UTC().Format(time.RFC3339)})
		if len(s.Pushes) > smsPushMemory {
			s.Pushes = s.Pushes[len(s.Pushes)-smsPushMemory:]
		}
	})
}

func findSMSPush(messageID int64) (smsPush, bool) {
	for _, p := range telegramBot.get().Pushes {
		if p.MessageID == messageID {
			return p, true
		}
	}
	return smsPush{}, false
}

type botModule struct {
	ID, Name, Socks string
	Connected       bool
}

// botBackend is how the bot reaches modules: the local backend in
// single-module mode, the module workers in multi-module mode.
type botBackend interface {
	modules() []botModule
	call(moduleID, method, path string, body []byte) (int, []byte, error)
}

type localBot struct{ handler http.Handler }

func (b localBot) modules() []botModule {
	return []botModule{{Name: "模块", Socks: proxyListenAddress(), Connected: true}}
}

func (b localBot) call(_, method, path string, body []byte) (int, []byte, error) {
	r := httptest.NewRequest(method, "http://127.0.0.1:7575"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	b.handler.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes(), nil
}

type fleetBot struct{ c *fleetController }

func (b fleetBot) modules() []botModule {
	out := []botModule{}
	for _, m := range b.c.snapshot().Modules {
		out = append(out, botModule{ID: m.ID, Name: m.Name, Socks: fmt.Sprintf("127.0.0.1:%d", m.Port), Connected: m.Connected && b.c.hasWorker(m.ID)})
	}
	return out
}

func (b fleetBot) call(moduleID, method, path string, body []byte) (int, []byte, error) {
	b.c.mu.Lock()
	w := b.c.workers[moduleID]
	b.c.mu.Unlock()
	if w == nil {
		return 0, nil, fmt.Errorf("模块未连接")
	}
	return w.Request(method, path, body)
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
		Chat      struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		ReplyTo *struct {
			MessageID int64 `json:"message_id"`
		} `json:"reply_to_message"`
	} `json:"message"`
}

func runTelegramBot(ctx context.Context, backend botBackend) {
	telegramSOCKS = func() string {
		for _, m := range backend.modules() {
			if m.Connected {
				return m.Socks
			}
		}
		return ""
	}
	failures := 0
	for ctx.Err() == nil {
		settings := forwarding.get()
		if !settings.telegramOn() {
			sleepCtx(ctx, 20*time.Second)
			continue
		}
		var updates []tgUpdate
		params := map[string]any{"offset": telegramBot.get().Offset, "timeout": telegramPollTimeout, "allowed_updates": []string{"message"}}
		err := telegramCall(ctx, settings, "getUpdates", params, (telegramPollTimeout+15)*time.Second, &updates)
		if err != nil {
			failures++
			if failures == 1 || failures%30 == 0 {
				log.Printf("telegram bot: %v", err)
			}
			sleepCtx(ctx, time.Duration(min(failures, 6))*5*time.Second)
			continue
		}
		failures = 0
		for _, u := range updates {
			_ = telegramBot.update(func(s *botState) { s.Offset = u.UpdateID + 1 })
			if u.Message == nil || strconv.FormatInt(u.Message.Chat.ID, 10) != settings.TelegramChatID {
				continue // only the owner's chat gives orders
			}
			reply := handleBotMessage(ctx, backend, u)
			if reply != "" {
				if _, err := sendTelegram(ctx, settings, reply, u.Message.MessageID); err != nil {
					log.Printf("telegram bot reply failed: %v", err)
				}
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

const botHelp = `可以这样用：
• 回复一条推送来的短信：从收到它的卡回短信
• /ip ：各模块的出口 IP 和欺诈值
• /rotate ：换 IP（多个模块时 /rotate 2 换 2 号模块）
• /clean ：换到干净 IP（同样可加模块号）
• /send 号码 内容 ：发短信（多个模块时 /send 2 号码 内容）`

func handleBotMessage(ctx context.Context, backend botBackend, u tgUpdate) string {
	m := u.Message
	text := strings.TrimSpace(m.Text)
	if m.ReplyTo != nil && !strings.HasPrefix(text, "/") {
		push, ok := findSMSPush(m.ReplyTo.MessageID)
		if !ok {
			return "这条不是推送来的短信，没法回复。\n\n" + botHelp
		}
		if text == "" {
			return "只能回复文字。"
		}
		return botSendSMS(backend, push.Module, push.Sender, text)
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	command := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	args := fields[1:]
	switch command {
	case "/start", "/help":
		return botHelp
	case "/ip", "/status":
		return botStatus(backend)
	case "/rotate", "/clean":
		module, ok := pickModule(backend, args)
		if !ok {
			return "没有这个模块。发 /ip 看模块编号。"
		}
		return botRotate(ctx, backend, module, command == "/clean")
	case "/send":
		module, rest := botModule{}, args
		if mods := connectedModules(backend); len(mods) > 1 && len(args) > 2 {
			if n, err := strconv.Atoi(args[0]); err == nil && n >= 1 && n <= len(mods) {
				module, rest = mods[n-1], args[1:]
			}
		}
		if module.Name == "" {
			if mods := connectedModules(backend); len(mods) > 0 {
				module = mods[0]
			}
		}
		if len(rest) < 2 {
			return "格式：/send 号码 内容"
		}
		return botSendSMS(backend, module.ID, rest[0], strings.Join(rest[1:], " "))
	}
	return botHelp
}

func connectedModules(backend botBackend) []botModule {
	out := []botModule{}
	for _, m := range backend.modules() {
		if m.Connected {
			out = append(out, m)
		}
	}
	return out
}

func pickModule(backend botBackend, args []string) (botModule, bool) {
	mods := connectedModules(backend)
	if len(mods) == 0 {
		return botModule{}, false
	}
	if len(args) == 0 {
		return mods[0], true
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 || n > len(mods) {
		return botModule{}, false
	}
	return mods[n-1], true
}

func botSendSMS(backend botBackend, module, phone, text string) string {
	body, _ := json.Marshal(map[string]string{"phone": phone, "message": text})
	code, data, err := backend.call(module, "POST", "/api/sms/send", body)
	if err != nil {
		return "发送失败：" + err.Error()
	}
	if code != 200 {
		return "发送失败：" + apiErrorText(data)
	}
	return "✅ 已发送给 " + phone
}

func apiErrorText(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(data))
}

func botStatus(backend botBackend) string {
	mods := backend.modules()
	if len(mods) == 0 {
		return "没有接入模块。"
	}
	lines := []string{}
	n := 0
	for _, m := range mods {
		if !m.Connected {
			lines = append(lines, fmt.Sprintf("— %s：未连接", m.Name))
			continue
		}
		n++
		label := m.Name
		if len(mods) > 1 {
			label = fmt.Sprintf("%d. %s", n, m.Name)
		}
		_, data, err := backend.call(m.ID, "GET", "/api/proxy", nil)
		var s proxyState
		if err != nil || json.Unmarshal(data, &s) != nil {
			lines = append(lines, label+"：读取失败")
			continue
		}
		ip := s.PublicIP
		if ip == "" {
			ip = "出口未检测"
		}
		line := fmt.Sprintf("%s · %s · %s", label, ip, m.Socks)
		if v := knownFraud(s.History, s.PublicIP); v != nil && v.FraudScore != nil {
			line += fmt.Sprintf(" · Scamalytics %d", *v.FraudScore)
		}
		if v := knownIPQS(s.History, s.PublicIP); v != nil && v.IPQSScore != nil {
			line += fmt.Sprintf(" · IPQS %d", *v.IPQSScore)
		}
		if !s.Running {
			line += " · 代理已停止"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func botRotate(ctx context.Context, backend botBackend, m botModule, clean bool) string {
	path := "/api/proxy/rotate?wait=24"
	what := "换 IP"
	if clean {
		path += "&clean=1"
		what = "换干净 IP"
	}
	code, data, err := backend.call(m.ID, "GET", path, nil)
	if err != nil {
		return what + "失败：" + err.Error()
	}
	var r rotateResult
	_ = json.Unmarshal(data, &r)
	if code >= 300 && !r.Pending {
		if r.Error == "" {
			r.Error = apiErrorText(data)
		}
		return what + "失败：" + r.Error
	}
	// A clean switch may take several radio cycles: follow it to the end.
	deadline := time.Now().Add(4 * time.Minute)
	var s proxyState
	for r.Pending && time.Now().Before(deadline) && ctx.Err() == nil {
		sleepCtx(ctx, 3*time.Second)
		if _, data, err := backend.call(m.ID, "GET", "/api/proxy", nil); err == nil && json.Unmarshal(data, &s) == nil && s.Phase != "reconnecting" {
			r = rotateResult{OK: s.Phase != "failed" && s.Error == "", Phase: s.Phase, PreviousIP: r.PreviousIP, IP: s.PublicIP, Seconds: s.ReconnectSeconds, Error: s.Error, Message: s.ReconnectNote}
		}
	}
	if clean && s.Phase == "" {
		if _, data, err := backend.call(m.ID, "GET", "/api/proxy", nil); err == nil {
			_ = json.Unmarshal(data, &s)
		}
	}
	switch {
	case r.Pending:
		return what + "还在进行，稍后发 /ip 查看。"
	case !r.OK:
		return what + "失败：" + r.Error
	}
	reply := fmt.Sprintf("✅ %s：%s → %s（%.0f 秒）", m.Name, orDash(r.PreviousIP), orDash(r.IP), r.Seconds)
	if s.ReconnectNote != "" && clean {
		reply += "\n" + s.ReconnectNote
	}
	return reply
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

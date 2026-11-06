package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// New SMS are pushed to the user's phone: Telegram (a bot the user created)
// and/or WeChat through PushPlus or Server酱. Settings live in
// Application Support/DJOneHub/forwarding.json (0600); tokens never leave
// the backend except masked.
const (
	forwardSettingsFile = "forwarding.json"
	telegramAPIBase     = "https://api.telegram.org"
	pushplusURL         = "https://www.pushplus.plus/send"
	serverChanBase      = "https://sctapi.ftqq.com/"
	// Messages older than this when first seen (SIM storage read after a
	// restart) are cached but not pushed again.
	forwardMaxAge = 15 * time.Minute
)

type forwardSettings struct {
	TelegramToken  string `json:"telegram_token,omitempty"`
	TelegramChatID string `json:"telegram_chat_id,omitempty"`
	// TelegramAPI replaces api.telegram.org, e.g. a reverse proxy reachable
	// from the Mac's broadband.
	TelegramAPI string `json:"telegram_api,omitempty"`
	// WeChat goes through one of these services, chosen by WeChatService.
	WeChatService string `json:"wechat_service,omitempty"` // "pushplus" | "serverchan"
	WeChatKey     string `json:"wechat_key,omitempty"`
	// Call forwarding as the user last set it, and the SIM's own forwarding
	// (e.g. voicemail) before that, keyed by ICCID, restored when turned off.
	CallForward map[string]callForwardSaved `json:"call_forward,omitempty"`
}

func (s forwardSettings) telegramOn() bool {
	return s.TelegramToken != "" && s.TelegramChatID != ""
}

func (s forwardSettings) weChatOn() bool {
	return s.WeChatKey != "" && (s.WeChatService == "pushplus" || s.WeChatService == "serverchan")
}

type forwardStore struct {
	mu       sync.Mutex
	loaded   bool
	path     string
	modTime  time.Time
	settings forwardSettings
}

var forwarding forwardStore

// loadLocked reads the file when it changed on disk: in multi-module mode
// every module worker shares it, and any of them may have saved it.
func (s *forwardStore) loadLocked() {
	if !s.loaded {
		s.loaded = true
		if s.path == "" {
			dir, err := os.UserConfigDir()
			if err != nil {
				return
			}
			s.path = filepath.Join(dir, "DJOneHub", forwardSettingsFile)
		}
	}
	info, err := os.Stat(s.path)
	if err != nil || info.ModTime().Equal(s.modTime) {
		return
	}
	if b, err := os.ReadFile(s.path); err == nil {
		var next forwardSettings
		if json.Unmarshal(b, &next) == nil {
			s.settings, s.modTime = next, info.ModTime()
		}
	}
}

func (s *forwardStore) get() forwardSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	out := s.settings
	out.CallForward = map[string]callForwardSaved{}
	for k, v := range s.settings.CallForward {
		out.CallForward[k] = v
	}
	return out
}

func (s *forwardStore) update(change func(*forwardSettings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if s.path == "" {
		return errors.New("无法确定设置保存位置")
	}
	return withSharedFileLock(s.path, func() error {
		s.loadLocked()
		next := s.settings
		change(&next)
		b, _ := json.MarshalIndent(next, "", "  ")
		tmp := s.path + ".tmp"
		if err := os.WriteFile(tmp, b, 0600); err != nil {
			return err
		}
		if err := os.Rename(tmp, s.path); err != nil {
			return err
		}
		s.settings = next
		if info, err := os.Stat(s.path); err == nil {
			s.modTime = info.ModTime()
		}
		return nil
	})
}

// secretHint shows enough of a token to recognise it, never the whole token.
func secretHint(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return strings.Repeat("•", len(value))
	}
	return value[:4] + "…" + value[len(value)-4:]
}

// moduleLabel names this module worker's module in pushed messages, so SMS
// from several modules can be told apart. Empty with a single module.
func moduleLabel() string {
	if scopedModuleID == "" {
		return ""
	}
	r, _ := loadFleetRegistry(filepath.Join(fleetStateDir(), "module-bindings.json"))
	if len(r.Bindings) < 2 {
		return "" // only one module ever: nothing to tell apart
	}
	for _, b := range r.Bindings {
		if b.ID == scopedModuleID {
			if b.Name != "" {
				return b.Name
			}
			return fmt.Sprintf("端口 %d", b.Port)
		}
	}
	return scopedModuleID
}

func smsForwardText(item receivedSMS, module string) (title, body string) {
	title = "短信 · " + item.Sender
	if item.Code != "" {
		title = "验证码 " + item.Code + " · " + item.Sender
	}
	if module != "" {
		title += " · " + module
	}
	body = item.Content + "\n\n" + item.Sender + " · " + item.Timestamp.Local().Format("2006-01-02 15:04:05")
	return title, body
}

// forwardNewSMS pushes messages that just arrived; it never blocks the poller.
func (a *app) forwardNewSMS(items []receivedSMS) {
	settings := forwarding.get()
	if !settings.telegramOn() && !settings.weChatOn() {
		return
	}
	now := time.Now()
	module := moduleLabel()
	for _, item := range items {
		if !item.Timestamp.IsZero() && now.Sub(item.Timestamp) > forwardMaxAge {
			continue
		}
		title, body := smsForwardText(item, module)
		remember := func(id int64) { rememberSMSPush(id, scopedModuleID, item.Sender) }
		go deliverPush(settings, title, body, 3, remember)
	}
}

// sendForward delivers one message to every configured channel, retrying
// each up to attempts times, and returns one error per failed channel.
// onTelegram, when given, receives the id of the Telegram message.
func sendForward(ctx context.Context, s forwardSettings, title, body string, attempts int, onTelegram ...func(int64)) map[string]error {
	failed := map[string]error{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	try := func(name string, send func(context.Context) error) {
		defer wg.Done()
		// A push problem must never take the backend (and every module) down.
		defer func() {
			if r := recover(); r != nil {
				mu.Lock()
				failed[name] = fmt.Errorf("推送出错：%v", r)
				mu.Unlock()
			}
		}()
		var err error
		for i := 0; i < attempts; i++ {
			if i > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(i*i) * 10 * time.Second):
				}
			}
			if err = send(ctx); err == nil {
				return
			}
		}
		mu.Lock()
		failed[name] = err
		mu.Unlock()
	}
	if s.telegramOn() {
		wg.Add(1)
		go try("telegram", func(ctx context.Context) error {
			text := title + "\n\n" + body
			if len(onTelegram) > 0 {
				text += "\n\n↩ 直接回复这条消息，就从收到它的卡回短信"
			}
			id, err := sendTelegram(ctx, s, text, 0)
			for _, f := range onTelegram {
				if err == nil {
					f(id)
				}
			}
			return err
		})
	}
	if s.weChatOn() {
		wg.Add(1)
		go try("wechat", func(ctx context.Context) error { return sendWeChat(ctx, s, title, body) })
	}
	wg.Wait()
	return failed
}

// telegramSOCKS, when set, lists the local SOCKS5 proxies (one per connected
// module) the multi-module supervisor, which owns no cellular interface, can
// reach Telegram through.
var telegramSOCKS func() []string

type telegramRejected struct{ msg string }

func (e telegramRejected) Error() string { return e.msg }

// telegramCall calls a Bot API method and decodes its result into into. It
// tries the Mac's broadband first; api.telegram.org is often unreachable from
// mainland broadband, so it then goes over the cellular interface, or a
// module's SOCKS5 proxy. A reply from Telegram itself is never retried.
func telegramCall(ctx context.Context, s forwardSettings, method string, params any, timeout time.Duration, into any) error {
	base := strings.TrimRight(s.TelegramAPI, "/")
	if base == "" {
		base = telegramAPIBase
	}
	payload, _ := json.Marshal(params)
	target := base + "/bot" + s.TelegramToken + "/" + method
	err := postTelegram(ctx, http.DefaultClient, target, payload, timeout, into)
	var rejected telegramRejected
	if err == nil || s.TelegramAPI != "" || errors.As(err, &rejected) || ctx.Err() != nil {
		return err
	}
	routes := []*http.Transport{}
	if iface, ifaceErr := proxyInterface(); ifaceErr == nil && !coordinatorOnly {
		routes = append(routes, &http.Transport{
			Proxy:       nil,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return cellularDial(ctx, iface, addr) },
		})
	}
	// Then every module's proxy: one card may be dead while another works.
	if telegramSOCKS != nil {
		for _, socks := range telegramSOCKS() {
			routes = append(routes, &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "socks5h", Host: socks})})
		}
	}
	if len(routes) == 0 {
		return err
	}
	defer beginCellularActivity()()
	var cellErr error
	for _, transport := range routes {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		transport.DisableKeepAlives = true
		cellErr = postTelegram(ctx, &http.Client{Transport: transport}, target, payload, timeout, into)
		transport.CloseIdleConnections()
		if cellErr == nil || errors.As(cellErr, &rejected) || ctx.Err() != nil {
			return cellErr
		}
	}
	return fmt.Errorf("宽带：%v；4G：%v", err, cellErr)
}

// sendTelegram posts text to the configured chat, as a reply when replyTo is
// set, and returns the new message's id.
func sendTelegram(ctx context.Context, s forwardSettings, text string, replyTo int64) (int64, error) {
	params := map[string]any{"chat_id": s.TelegramChatID, "text": text, "disable_web_page_preview": true}
	if replyTo != 0 {
		params["reply_to_message_id"] = replyTo
	}
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	err := telegramCall(ctx, s, "sendMessage", params, 12*time.Second, &sent)
	return sent.MessageID, err
}

func postTelegram(ctx context.Context, client *http.Client, target string, payload []byte, timeout time.Duration, into any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(payload))
	if err != nil {
		return telegramRejected{"Telegram 机器人 Token 或接口地址格式不对"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// The URL carries the bot token; keep it out of logs and the App.
		return errors.New("连不上 Telegram")
	}
	defer resp.Body.Close()
	var reply struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&reply)
	if !reply.OK {
		if reply.Description == "" {
			reply.Description = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return telegramRejected{"Telegram 拒绝：" + reply.Description}
	}
	if into != nil && len(reply.Result) > 0 {
		return json.Unmarshal(reply.Result, into)
	}
	return nil
}

func sendWeChat(ctx context.Context, s forwardSettings, title, body string) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var req *http.Request
	var err error
	if s.WeChatService == "serverchan" {
		form := url.Values{"title": {title}, "desp": {body}}
		req, err = http.NewRequestWithContext(ctx, "POST", serverChanBase+url.PathEscape(s.WeChatKey)+".send", strings.NewReader(form.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		payload, _ := json.Marshal(map[string]string{"token": s.WeChatKey, "title": title, "content": body, "template": "txt"})
		req, err = http.NewRequestWithContext(ctx, "POST", pushplusURL, bytes.NewReader(payload))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return errors.New("微信推送的 Token / SendKey 格式不对")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("连不上微信推送服务")
	}
	defer resp.Body.Close()
	var reply struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&reply)
	// PushPlus answers code 200 on success, Server酱 code 0.
	if (s.WeChatService == "serverchan" && reply.Code == 0 && resp.StatusCode == 200) || (s.WeChatService != "serverchan" && reply.Code == 200) {
		return nil
	}
	msg := reply.Msg + reply.Message
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return errors.New("微信推送失败：" + msg)
}

type forwardView struct {
	TelegramConfigured bool   `json:"telegram_configured"`
	TelegramTokenHint  string `json:"telegram_token_hint,omitempty"`
	TelegramChatID     string `json:"telegram_chat_id,omitempty"`
	TelegramAPI        string `json:"telegram_api,omitempty"`
	WeChatConfigured   bool   `json:"wechat_configured"`
	WeChatService      string `json:"wechat_service,omitempty"`
	WeChatKeyHint      string `json:"wechat_key_hint,omitempty"`
}

func forwardViewOf(s forwardSettings) forwardView {
	return forwardView{
		TelegramConfigured: s.telegramOn(), TelegramTokenHint: secretHint(s.TelegramToken), TelegramChatID: s.TelegramChatID, TelegramAPI: s.TelegramAPI,
		WeChatConfigured: s.weChatOn(), WeChatService: s.WeChatService, WeChatKeyHint: secretHint(s.WeChatKey),
	}
}

// smsForwardAPI: GET shows the settings masked. POST {channel, ...} saves one
// channel after sending it a test message (empty values turn it off), or
// {channel, test:true} only sends a test message with the saved settings.
func (a *app) smsForwardAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, forwardViewOf(forwarding.get()))
		return
	}
	var body struct {
		Channel        string `json:"channel"`
		Test           bool   `json:"test"`
		TelegramToken  string `json:"telegram_token"`
		TelegramChatID string `json:"telegram_chat_id"`
		TelegramAPI    string `json:"telegram_api"`
		WeChatService  string `json:"wechat_service"`
		WeChatKey      string `json:"wechat_key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	next := forwarding.get()
	switch body.Channel {
	case "telegram":
		if !body.Test {
			next.TelegramToken = strings.TrimSpace(body.TelegramToken)
			next.TelegramChatID = strings.TrimSpace(body.TelegramChatID)
			next.TelegramAPI = strings.TrimSpace(body.TelegramAPI)
		}
		if next.TelegramAPI != "" && !strings.HasPrefix(next.TelegramAPI, "https://") && !strings.HasPrefix(next.TelegramAPI, "http://") {
			writeError(w, 400, "Telegram 接口地址要以 http:// 或 https:// 开头")
			return
		}
		if next.TelegramToken != "" && next.TelegramChatID != "" {
			only := forwardSettings{TelegramToken: next.TelegramToken, TelegramChatID: next.TelegramChatID, TelegramAPI: next.TelegramAPI}
			if err := sendForward(r.Context(), only, "DJOneHub 测试消息", "收到这条，说明短信会推送到这里。", 1)["telegram"]; err != nil {
				writeError(w, 502, err.Error())
				return
			}
		} else if body.Test {
			writeError(w, 400, "还没有填写 Telegram")
			return
		}
	case "wechat":
		if !body.Test {
			next.WeChatService = strings.TrimSpace(body.WeChatService)
			next.WeChatKey = strings.TrimSpace(body.WeChatKey)
		}
		if next.WeChatKey != "" {
			if next.WeChatService != "pushplus" && next.WeChatService != "serverchan" {
				writeError(w, 400, "请选择 PushPlus 或 Server酱")
				return
			}
			only := forwardSettings{WeChatService: next.WeChatService, WeChatKey: next.WeChatKey}
			if err := sendForward(r.Context(), only, "DJOneHub 测试消息", "收到这条，说明短信会推送到这里。", 1)["wechat"]; err != nil {
				writeError(w, 502, err.Error())
				return
			}
		} else if body.Test {
			writeError(w, 400, "还没有填写微信推送")
			return
		}
	default:
		writeError(w, 400, "未知的推送渠道")
		return
	}
	if !body.Test {
		if err := forwarding.update(func(s *forwardSettings) {
			s.TelegramToken, s.TelegramChatID, s.TelegramAPI = next.TelegramToken, next.TelegramChatID, next.TelegramAPI
			s.WeChatService, s.WeChatKey = next.WeChatService, next.WeChatKey
		}); err != nil {
			writeError(w, 500, "保存失败："+err.Error())
			return
		}
	}
	writeJSON(w, 200, forwardViewOf(forwarding.get()))
}

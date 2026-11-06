package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"
)

// Pushes that could not be delivered (Telegram is often unreachable from the
// mainland, a card may be dead) wait in an outbox shared by all modules and
// are sent again by the one process that runs the Telegram bot, marked as
// late. Only the failed channel is retried, so nothing arrives twice.
type pushItem struct {
	ID      string    `json:"id"`
	Channel string    `json:"channel"` // telegram | wechat
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Created time.Time `json:"created"`
	Tries   int       `json:"tries"`
	Error   string    `json:"error,omitempty"`
}

type pushOutboxFile struct {
	Items       []pushItem `json:"items"`
	LastOK      time.Time  `json:"last_ok,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt time.Time  `json:"last_error_at,omitempty"`
}

const (
	pushOutboxMax   = 200
	pushOutboxKeep  = 48 * time.Hour
	pushOutboxEvery = 30 * time.Second
)

var pushOutbox = settingsFile[pushOutboxFile]{name: "push-outbox.json"}

// deliverPush sends now and queues every channel that failed.
func deliverPush(s forwardSettings, title, body string, attempts int, onTelegram ...func(int64)) {
	failed := sendForward(context.Background(), s, title, body, attempts, onTelegram...)
	notePushResult(failed)
	for channel, err := range failed {
		log.Printf("push %q via %s failed, queued for retry: %v", title, channel, err)
		queuePush(channel, title, body, err)
	}
}

func queuePush(channel, title, body string, err error) {
	now := time.Now()
	_ = pushOutbox.update(func(f *pushOutboxFile) {
		f.Items = append(f.Items, pushItem{
			ID:      strconv.FormatInt(now.UnixNano(), 36) + channel,
			Channel: channel, Title: title, Body: body, Created: now, Tries: 1, Error: err.Error(),
		})
		if len(f.Items) > pushOutboxMax {
			f.Items = f.Items[len(f.Items)-pushOutboxMax:]
		}
	})
}

func notePushResult(failed map[string]error) {
	now := time.Now()
	_ = pushOutbox.update(func(f *pushOutboxFile) {
		if len(failed) == 0 {
			f.LastOK = now
			return
		}
		for channel, err := range failed {
			f.LastError, f.LastErrorAt = channelName(channel)+"："+err.Error(), now
		}
	})
}

func channelName(channel string) string {
	if channel == "wechat" {
		return "微信"
	}
	return "Telegram"
}

func runPushOutbox(ctx context.Context) {
	for ctx.Err() == nil {
		sleepCtx(ctx, pushOutboxEvery)
		flushPushOutbox(ctx, time.Now())
	}
}

// flushPushOutbox tries every waiting push once, oldest first.
func flushPushOutbox(ctx context.Context, now time.Time) {
	items := pushOutbox.get().Items
	if len(items) == 0 {
		return
	}
	settings := forwarding.get()
	done := map[string]string{} // id -> "" when sent, else the error
	down := map[string]bool{}   // channels that failed this round
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		if down[it.Channel] {
			continue // still offline: keep the order, try again next round
		}
		if now.Sub(it.Created) > pushOutboxKeep {
			done[it.ID] = "expired"
			continue
		}
		only := settings
		switch it.Channel {
		case "telegram":
			only.WeChatKey = ""
		case "wechat":
			only.TelegramToken = ""
		}
		if !only.telegramOn() && !only.weChatOn() {
			done[it.ID] = "expired" // channel turned off meanwhile
			continue
		}
		body := fmt.Sprintf("（补发：%s 时未能送达）\n%s", it.Created.Local().Format("01-02 15:04"), it.Body)
		failed := sendForward(ctx, only, it.Title, body, 1)
		if err := failed[it.Channel]; err != nil {
			done[it.ID] = err.Error()
			down[it.Channel] = true
			continue
		}
		done[it.ID] = ""
	}
	_ = pushOutbox.update(func(f *pushOutboxFile) {
		kept := f.Items[:0]
		for _, it := range f.Items {
			result, tried := done[it.ID]
			switch {
			case !tried:
				kept = append(kept, it)
			case result == "":
				f.LastOK = now
			case result == "expired":
			default:
				it.Tries++
				it.Error = result
				f.LastError, f.LastErrorAt = channelName(it.Channel)+"："+result, now
				kept = append(kept, it)
			}
		}
		f.Items = kept
	})
}

// pushCheck is the checkup line for phone pushes.
func pushCheck() (level, detail string, configured bool) {
	settings := forwarding.get()
	if !settings.telegramOn() && !settings.weChatOn() {
		return "off", "没有设置 Telegram 或微信推送", false
	}
	f := pushOutbox.get()
	if n := len(f.Items); n > 0 {
		oldest := f.Items[0].Created
		return "warn", fmt.Sprintf("有 %d 条推送没送到（最早 %s），每 30 秒自动重发。最近的原因：%s",
			n, oldest.Local().Format("01-02 15:04"), f.Items[n-1].Error), true
	}
	if f.LastErrorAt.After(f.LastOK) && time.Since(f.LastErrorAt) < time.Hour {
		return "warn", "最近一次推送失败：" + f.LastError, true
	}
	if !f.LastOK.IsZero() {
		return "ok", "正常，最近一次送达 " + f.LastOK.Local().Format("01-02 15:04"), true
	}
	return "ok", "已设置，还没有需要推送的消息", true
}

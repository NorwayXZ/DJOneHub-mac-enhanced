package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFailedPushIsQueuedAndSentLateOnce(t *testing.T) {
	var up atomic.Bool
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Gateway"}`))
			return
		}
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		texts = append(texts, got["text"].(string))
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()
	if err := forwarding.update(func(s *forwardSettings) {
		*s = forwardSettings{TelegramToken: "T", TelegramChatID: "1", TelegramAPI: server.URL}
	}); err != nil {
		t.Fatal(err)
	}
	defer forwarding.update(func(s *forwardSettings) { *s = forwardSettings{} })

	deliverPush(forwarding.get(), "这张卡可能已失效", "尾号 2027", 1)
	if n := len(pushOutbox.get().Items); n != 1 {
		t.Fatalf("queued %d, want 1", n)
	}
	if level, _, _ := pushCheck(); level != "warn" {
		t.Fatalf("checkup level %q, want warn", level)
	}

	flushPushOutbox(context.Background(), time.Now()) // still down
	if f := pushOutbox.get(); len(f.Items) != 1 || f.Items[0].Tries != 2 {
		t.Fatalf("after failed retry: %+v", f.Items)
	}

	up.Store(true)
	flushPushOutbox(context.Background(), time.Now())
	flushPushOutbox(context.Background(), time.Now())
	if len(texts) != 1 || !strings.Contains(texts[0], "补发") || !strings.Contains(texts[0], "尾号 2027") {
		t.Fatalf("delivered %q", texts)
	}
	if n := len(pushOutbox.get().Items); n != 0 {
		t.Fatalf("outbox still has %d", n)
	}
	if level, detail, _ := pushCheck(); level != "ok" {
		t.Fatalf("checkup %q %q", level, detail)
	}

	// Old pushes are dropped instead of arriving days late.
	queuePush("telegram", "旧消息", "x", context.DeadlineExceeded)
	flushPushOutbox(context.Background(), time.Now().Add(49*time.Hour))
	if n := len(pushOutbox.get().Items); n != 0 || len(texts) != 1 {
		t.Fatalf("expired push: outbox %d, sent %d", n, len(texts))
	}
}

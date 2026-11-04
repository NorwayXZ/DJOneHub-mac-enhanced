package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseCCFC(t *testing.T) {
	off, err := parseCCFC(0, "AT+CCFC=0,2\r\n+CCFC: 0,255\r\nOK")
	if err != nil || off.Active {
		t.Fatal(off, err)
	}
	vm, err := parseCCFC(2, "+CCFC: 1,1,\"+12142269138\",145,,,20\r\nOK")
	if err != nil || !vm.Active || vm.Number != "+12142269138" || vm.Type != 145 || vm.Time != 20 {
		t.Fatal(vm, err)
	}
	// Forwarding of data only does not forward calls.
	data, _ := parseCCFC(0, "+CCFC: 1,2,\"10086\",129\r\nOK")
	if data.Active {
		t.Fatal(data)
	}
	if _, err := parseCCFC(0, "+CME ERROR: 30\r\n"); err == nil || !strings.Contains(err.Error(), "CME ERROR: 30") {
		t.Fatal(err)
	}
}

func TestCCFCSetCommands(t *testing.T) {
	for want, e := range map[string]ccfcEntry{
		`AT+CCFC=0,3,"+8613800138000",145`:      {Reason: 0, Active: true, Number: "+8613800138000"},
		`AT+CCFC=1,3,"13800138000",129`:         {Reason: 1, Active: true, Number: "13800138000"},
		`AT+CCFC=2,3,"+12142269138",145,1,,,20`: {Reason: 2, Active: true, Number: "+12142269138", Type: 145, Time: 20},
		`AT+CCFC=3,4`:                           {Reason: 3},
	} {
		if got := ccfcSetCommand(e); got != want {
			t.Fatal(got, want)
		}
	}
}

func TestSMSForwardTextLeadsWithTheCode(t *testing.T) {
	title, body := smsForwardText(receivedSMS{Sender: "Amazon", Content: "Your code is 123456", Code: "123456", Timestamp: time.Now()}, "")
	if title != "验证码 123456 · Amazon" || !strings.HasPrefix(body, "Your code is 123456") {
		t.Fatal(title, body)
	}
	if title, _ := smsForwardText(receivedSMS{Sender: "10086", Content: "余额"}, "模块 2"); title != "短信 · 10086 · 模块 2" {
		t.Fatal(title)
	}
}

func TestTelegramSendUsesTheConfiguredAPI(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN123/sendMessage" {
			http.Error(w, "bad path", 404)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	s := forwardSettings{TelegramToken: "TOKEN123", TelegramChatID: "42", TelegramAPI: server.URL}
	if failed := sendForward(context.Background(), s, "短信 · 10086", "余额不足", 1); len(failed) != 0 {
		t.Fatal(failed)
	}
	if got["chat_id"] != "42" || !strings.Contains(got["text"].(string), "余额不足") {
		t.Fatal(got)
	}
}

func TestTelegramRejectionIsReportedWithoutTheToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer server.Close()
	err := sendForward(context.Background(), forwardSettings{TelegramToken: "SECRETTOKEN", TelegramChatID: "1", TelegramAPI: server.URL}, "t", "b", 1)["telegram"]
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatal(err)
	}
}

func TestMalformedPushSettingsFailWithoutCrashing(t *testing.T) {
	failed := sendForward(context.Background(), forwardSettings{TelegramToken: "12:ab\ncd", TelegramChatID: "1", TelegramAPI: "http://bad host"}, "t", "b", 1)
	if failed["telegram"] == nil {
		t.Fatal("expected an error")
	}
}

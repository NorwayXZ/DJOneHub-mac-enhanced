package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type aiWebRTCInit struct {
	Mode    string          `json:"mode"`     // "realtime" or "translation"
	BaseURL string          `json:"base_url"` // e.g. "https://api.openai.com"
	APIKey  string          `json:"api_key"`
	Session json.RawMessage `json:"session"`
}

var aiWebRTCUpgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// aiWebRTCBridgeAPI bridges a localhost WebSocket from the macOS/Windows UI to
// a real UDP/ICE/DTLS/SRTP WebRTC PeerConnection against OpenAI's
// /v1/realtime/calls or /v1/realtime/translations/calls endpoint.
//
// Wire format over the local WebSocket after the initial JSON config frame:
//   - Binary frames: raw 20 ms Opus packets (48 kHz mono/stereo)
//   - Text frames:   OpenAI oai-events DataChannel JSON (or {"_bridge":...} control)
func aiWebRTCBridgeAPI(w http.ResponseWriter, r *http.Request) {
	ws, err := aiWebRTCUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	var wsMu sync.Mutex
	sendWS := func(msgType int, payload []byte) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return ws.WriteMessage(msgType, payload)
	}
	sendBridgeError := func(msg string) {
		b, _ := json.Marshal(map[string]any{"_bridge": "error", "message": msg})
		_ = sendWS(websocket.TextMessage, b)
	}

	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	mt, rawInit, err := ws.ReadMessage()
	if err != nil || mt != websocket.TextMessage {
		sendBridgeError("缺少 WebRTC 初始化参数")
		return
	}
	_ = ws.SetReadDeadline(time.Time{})

	var cfg aiWebRTCInit
	if err := json.Unmarshal(rawInit, &cfg); err != nil {
		sendBridgeError("WebRTC 初始化参数无效：" + err.Error())
		return
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.APIKey == "" {
		sendBridgeError("未提供 OpenAI API Key")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.openai.com"
	}
	if strings.HasSuffix(base, "/v1") {
		base = strings.TrimSuffix(base, "/v1")
	}
	if cfg.Mode == "" {
		cfg.Mode = "realtime"
	}

	startedAt := time.Now()
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		sendBridgeError("注册 Opus 编解码失败：" + err.Error())
		return
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		sendBridgeError("创建 WebRTC 连接失败：" + err.Error())
		return
	}
	defer pc.Close()

	localTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		"audio",
		"djonehub",
	)
	if err != nil {
		sendBridgeError("创建本地音频轨道失败：" + err.Error())
		return
	}
	if _, err := pc.AddTrack(localTrack); err != nil {
		sendBridgeError("绑定本地音频轨道失败：" + err.Error())
		return
	}

	dc, err := pc.CreateDataChannel("oai-events", nil)
	if err != nil {
		sendBridgeError("创建数据通道失败：" + err.Error())
		return
	}

	var dcMu sync.Mutex
	dcReady := false
	var queuedEvents []string

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	dc.OnOpen(func() {
		dcMu.Lock()
		dcReady = true
		pending := queuedEvents
		queuedEvents = nil
		dcMu.Unlock()

		for _, ev := range pending {
			_ = dc.SendText(ev)
		}
		readyMsg, _ := json.Marshal(map[string]any{
			"_bridge": "ready",
			"ice_ms":  time.Since(startedAt).Milliseconds(),
		})
		_ = sendWS(websocket.TextMessage, readyMsg)
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if ctx.Err() != nil {
			return
		}
		if err := sendWS(websocket.TextMessage, msg.Data); err != nil {
			cancel()
		}
	})
	dc.OnClose(func() {
		cancel()
	})

	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for ctx.Err() == nil {
			pkt, _, err := remote.ReadRTP()
			if err != nil {
				return
			}
			if len(pkt.Payload) == 0 {
				continue
			}
			if err := sendWS(websocket.BinaryMessage, pkt.Payload); err != nil {
				cancel()
				return
			}
		}
	})

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			if ctx.Err() == nil {
				sendBridgeError("WebRTC UDP 连接已断开（" + state.String() + "）")
				cancel()
			}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		sendBridgeError("生成 SDP Offer 失败：" + err.Error())
		return
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		sendBridgeError("设置本地 SDP 失败：" + err.Error())
		return
	}
	select {
	case <-gatherDone:
	case <-time.After(6 * time.Second):
	case <-ctx.Done():
		return
	}

	localSDP := pc.LocalDescription().SDP
	answerSDP, err := exchangeOpenAIWebRTCSDP(ctx, base, cfg.APIKey, cfg.Mode, localSDP, cfg.Session)
	if err != nil {
		log.Printf("ai webrtc exchange failed (%s): %v", cfg.Mode, err)
		sendBridgeError(err.Error())
		return
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}); err != nil {
		sendBridgeError("应用远端 SDP Answer 失败：" + err.Error())
		return
	}

	// Read audio Opus packets & control JSON from Swift until socket closes.
	go func() {
		<-ctx.Done()
		_ = ws.Close()
	}()

	for {
		msgType, payload, err := ws.ReadMessage()
		if err != nil {
			cancel()
			return
		}
		switch msgType {
		case websocket.BinaryMessage:
			if len(payload) > 0 {
				_ = localTrack.WriteSample(media.Sample{
					Data:     payload,
					Duration: 20 * time.Millisecond,
				})
			}
		case websocket.TextMessage:
			text := string(payload)
			dcMu.Lock()
			if dcReady {
				dcMu.Unlock()
				_ = dc.SendText(text)
			} else {
				queuedEvents = append(queuedEvents, text)
				dcMu.Unlock()
			}
		}
	}
}

func exchangeOpenAIWebRTCSDP(ctx context.Context, base, apiKey, mode, offerSDP string, session json.RawMessage) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	if mode == "translation" {
		// Step 1: obtain short-lived ephemeral token for /v1/realtime/translations/calls.
		tokenBody, err := json.Marshal(map[string]any{"session": json.RawMessage(session)})
		if err != nil {
			return "", fmt.Errorf("编码同传会话失败：%w", err)
		}
		req1, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/realtime/translations/client_secrets", bytes.NewReader(tokenBody))
		if err != nil {
			return "", err
		}
		req1.Header.Set("Authorization", "Bearer "+apiKey)
		req1.Header.Set("Content-Type", "application/json")
		resp1, err := client.Do(req1)
		if err != nil {
			return "", fmt.Errorf("申请同传令牌失败：%w", err)
		}
		raw1, _ := io.ReadAll(resp1.Body)
		resp1.Body.Close()
		if resp1.StatusCode >= 300 {
			return "", fmt.Errorf("同传令牌被拒 (%d)：%s", resp1.StatusCode, strings.TrimSpace(string(raw1)))
		}
		var tok struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw1, &tok); err != nil || tok.Value == "" {
			return "", fmt.Errorf("同传令牌响应缺少 value")
		}

		// Step 2: exchange SDP directly with /v1/realtime/translations/calls.
		req2, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/realtime/translations/calls", strings.NewReader(offerSDP))
		if err != nil {
			return "", err
		}
		req2.Header.Set("Authorization", "Bearer "+tok.Value)
		req2.Header.Set("Content-Type", "application/sdp")
		resp2, err := client.Do(req2)
		if err != nil {
			return "", fmt.Errorf("同传 WebRTC 握手失败：%w", err)
		}
		raw2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if resp2.StatusCode >= 300 {
			return "", fmt.Errorf("同传 WebRTC 握手被拒 (%d)：%s", resp2.StatusCode, strings.TrimSpace(string(raw2)))
		}
		return string(raw2), nil
	}

	// Unified interface for Realtime voice sessions: multipart form (sdp + session).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("sdp", offerSDP); err != nil {
		return "", err
	}
	if len(session) > 0 {
		if err := mw.WriteField("session", string(session)); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/realtime/calls", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("WebRTC 握手失败：%w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("WebRTC 握手被拒 (%d)：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return string(raw), nil
}

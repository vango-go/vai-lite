package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/lifecycle"
	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
	"github.com/vango-go/vai-lite/pkg/gateway/live/session"
	"github.com/vango-go/vai-lite/pkg/gateway/live/sessions"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
)

func TestLiveHandler_HandshakeUnsupportedVersion(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("2")
	mustWriteJSON(t, conn, hello)

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("type=%v", msg["type"])
	}
	if msg["code"] != "unsupported_version" {
		t.Fatalf("code=%v", msg["code"])
	}
}

func TestLiveHandler_AudioFrameToTranscriptAndAssistantAudio(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{{{Text: "hello there", IsFinal: true}}},
		ttsSlow:                false,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v", ack["type"])
	}

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      1,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	seenTranscript := false
	seenUtteranceFinal := false
	seenAudioStart := false
	seenAudioChunk := false
	seenAudioEnd := false
	var readErr error
	for i := 0; i < 12; i++ {
		msg, err := readJSON(conn, 1500*time.Millisecond)
		if err != nil {
			readErr = err
			break
		}
		if msg["type"] == "error" {
			t.Fatalf("received error frame: %+v", msg)
		}
		switch msg["type"] {
		case "transcript_delta":
			seenTranscript = true
		case "utterance_final":
			seenUtteranceFinal = true
		case "assistant_audio_start":
			seenAudioStart = true
		case "assistant_audio_chunk":
			seenAudioChunk = true
		case "assistant_audio_end":
			seenAudioEnd = true
		}
		if seenTranscript && seenUtteranceFinal && seenAudioStart && seenAudioChunk && seenAudioEnd {
			break
		}
	}

	if !seenTranscript || !seenUtteranceFinal || !seenAudioStart || !seenAudioChunk || !seenAudioEnd {
		sendCalls := 0
		if h.sttProvider != nil {
			h.sttProvider.mu.Lock()
			sendCalls = h.sttProvider.sendCalls
			h.sttProvider.mu.Unlock()
		}
		t.Fatalf("missing expected events transcript=%v utterance=%v start=%v chunk=%v end=%v stt_send_calls=%d read_err=%v", seenTranscript, seenUtteranceFinal, seenAudioStart, seenAudioChunk, seenAudioEnd, sendCalls, readErr)
	}
}

func TestLiveHandler_AudioInAckTimestampReflectsClientTimestamp(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	_ = mustReadJSON(t, conn, 2*time.Second) // hello_ack

	mustWriteJSON(t, conn, map[string]any{
		"type":         "audio_frame",
		"seq":          25,
		"timestamp_ms": 5000,
		"data_b64":     base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg := mustReadJSON(t, conn, 2*time.Second)
		if msg["type"] != "audio_in_ack" {
			continue
		}
		ts, ok := msg["timestamp_ms"].(float64)
		if !ok {
			t.Fatalf("timestamp_ms type=%T", msg["timestamp_ms"])
		}
		if ts < 5000 {
			t.Fatalf("timestamp_ms=%v, want >= 5000", ts)
		}
		if ts > 7000 {
			t.Fatalf("timestamp_ms=%v, want < 7000", ts)
		}
		return
	}

	t.Fatalf("did not observe audio_in_ack")
}

func TestLiveHandler_ControlInterruptEmitsAudioReset(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{{{Text: "interrupt test", IsFinal: true}}},
		ttsSlow:                true,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	_ = mustReadJSON(t, conn, 2*time.Second) // hello_ack

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      1,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	seenAudioStart := false
	var assistantID string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		msg := mustReadJSON(t, conn, 4*time.Second)
		if msg["type"] == "assistant_audio_start" {
			seenAudioStart = true
			if v, ok := msg["assistant_audio_id"].(string); ok {
				assistantID = v
			}
			break
		}
	}
	if !seenAudioStart {
		t.Fatalf("did not observe assistant_audio_start")
	}
	if assistantID == "" {
		t.Fatalf("missing assistant_audio_id on assistant_audio_start")
	}

	mustWriteJSON(t, conn, map[string]any{"type": "control", "op": "interrupt"})

	seenReset := false
	for time.Now().Before(deadline) {
		msg := mustReadJSON(t, conn, 4*time.Second)
		if msg["type"] == "audio_reset" {
			if msg["reason"] != "barge_in" {
				t.Fatalf("reason=%v", msg["reason"])
			}
			if msg["assistant_audio_id"] != assistantID {
				t.Fatalf("assistant_audio_id=%v want %v", msg["assistant_audio_id"], assistantID)
			}
			seenReset = true
			break
		}
	}
	if !seenReset {
		t.Fatalf("did not observe audio_reset")
	}

	// After audio_reset, the server must not deliver any queued stale assistant audio for this assistant_audio_id.
	//
	// gorilla/websocket treats read deadlines as terminal read errors, so we do a single timed read window:
	// read until timeout (end of window) and fail if any stale assistant audio arrives.
	windowEnd := time.Now().Add(300 * time.Millisecond)
	_ = conn.SetReadDeadline(windowEnd)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			break
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["assistant_audio_id"] != assistantID {
			continue
		}
		switch msg["type"] {
		case "assistant_audio_chunk", "assistant_audio_chunk_header", "assistant_audio_end":
			t.Fatalf("received stale assistant audio after audio_reset: %+v", msg)
		}
	}
}

func TestLiveHandler_HelloAckIncludesRunTimeoutMS(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		liveTurnTimeout: 1234 * time.Millisecond,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v", ack["type"])
	}
	limits, ok := ack["limits"].(map[string]any)
	if !ok {
		t.Fatalf("missing limits")
	}
	if limits["run_timeout_ms"] != float64(1234) {
		t.Fatalf("run_timeout_ms=%v, want %v", limits["run_timeout_ms"], 1234)
	}
}

func TestLiveHandler_HandshakeGroqRequiresGroqBYOK(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["model"] = "groq/llama-3"
	hello["byok"] = map[string]any{
		"groq":     "sk-groq-test",
		"cartesia": "sk-car-test",
	}
	mustWriteJSON(t, conn, hello)
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v", ack["type"])
	}
}

func TestLiveHandler_HandshakeCerebrasRequiresCerebrasBYOK(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["model"] = "cerebras/llama-3"
	hello["byok"] = map[string]any{
		"cerebras": "sk-cerebras-test",
		"cartesia": "sk-car-test",
	}
	mustWriteJSON(t, conn, hello)
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v", ack["type"])
	}
}

func TestLiveHandler_HandshakeOpenRouterRequiresOpenRouterBYOK(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["model"] = "openrouter/openai/gpt-4o"
	hello["byok"] = map[string]any{
		"openrouter": "sk-openrouter-test",
		"cartesia":   "sk-car-test",
	}
	mustWriteJSON(t, conn, hello)
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v", ack["type"])
	}
}

func TestLiveHandler_HandshakeUnsupportedProviderIsUnsupported(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["model"] = "unknown/some-model"
	hello["byok"] = map[string]any{
		"keys": map[string]any{
			"unknown": "sk-unknown-test",
		},
		"cartesia": "sk-car-test",
	}
	mustWriteJSON(t, conn, hello)

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("type=%v", msg["type"])
	}
	if msg["code"] != "unsupported" {
		t.Fatalf("code=%v", msg["code"])
	}
	if msg["close"] != true {
		t.Fatalf("close=%v", msg["close"])
	}
}

func TestLiveHandler_ServerTools_InferSearchProviderFromBYOK(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["tools"] = map[string]any{
		"server_tools": []any{"vai_web_search"},
	}
	hello["byok"] = map[string]any{
		"anthropic": "sk-ant-test",
		"cartesia":  "sk-car-test",
		"keys": map[string]any{
			"tavily": "tvly-test",
		},
	}
	mustWriteJSON(t, conn, hello)

	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v payload=%+v", ack["type"], ack)
	}
}

func TestLiveHandler_ServerTools_ExplicitExaWithoutKeyUnauthorized(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["tools"] = map[string]any{
		"server_tools": []any{"vai_web_search"},
		"server_tool_config": map[string]any{
			"vai_web_search": map[string]any{"provider": "exa"},
		},
	}
	mustWriteJSON(t, conn, hello)

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("type=%v payload=%+v", msg["type"], msg)
	}
	if msg["code"] != "unauthorized" {
		t.Fatalf("code=%v payload=%+v", msg["code"], msg)
	}
}

func TestLiveHandler_ServerTools_AmbiguousInferenceBadRequest(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["tools"] = map[string]any{
		"server_tools": []any{"vai_web_search"},
	}
	hello["byok"] = map[string]any{
		"anthropic": "sk-ant-test",
		"cartesia":  "sk-car-test",
		"keys": map[string]any{
			"tavily": "tvly-test",
			"exa":    "exa-test",
		},
	}
	mustWriteJSON(t, conn, hello)

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("type=%v payload=%+v", msg["type"], msg)
	}
	if msg["code"] != "bad_request" {
		t.Fatalf("code=%v payload=%+v", msg["code"], msg)
	}
}

func TestByokForProvider_AliasesAndKeyPrecedence(t *testing.T) {
	byok := protocol.HelloBYOK{
		OpenAI: "sk-openai-typed",
		Gemini: "sk-gemini-typed",
		Keys: map[string]string{
			"openai":       "sk-openai-map",
			"oai-resp":     "sk-oai-resp-map",
			"gemini":       "sk-gemini-map",
			"gemini-oauth": "sk-gemini-oauth-map",
		},
	}

	if got := byokForProvider(byok, "oai-resp"); got != "sk-oai-resp-map" {
		t.Fatalf("oai-resp=%q", got)
	}
	if got := byokForProvider(byok, "openai"); got != "sk-openai-map" {
		t.Fatalf("openai=%q", got)
	}
	if got := byokForProvider(byok, "gemini-oauth"); got != "sk-gemini-oauth-map" {
		t.Fatalf("gemini-oauth=%q", got)
	}
}

func TestLiveHandler_LiveSessionsTracker_CancelAllClosesConnAndDeregisters(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v", ack["type"])
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.tracker != nil && h.tracker.Count() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.tracker == nil || h.tracker.Count() != 1 {
		count := -1
		if h.tracker != nil {
			count = h.tracker.Count()
		}
		t.Fatalf("tracker count=%d, want 1", count)
	}

	h.tracker.CancelAll()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("expected websocket to close after cancel")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if ok := h.tracker.Wait(ctx); !ok {
		t.Fatalf("expected tracker to drain")
	}
	if h.tracker.Count() != 0 {
		t.Fatalf("tracker count=%d, want 0", h.tracker.Count())
	}
}

func TestLiveHandler_WSSessionCap(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{wsMaxSessions: 1})
	defer h.close()

	conn1 := mustDialWS(t, serverURL)
	defer conn1.Close()
	mustWriteJSON(t, conn1, baseHello("1"))
	ack1 := mustReadJSON(t, conn1, 2*time.Second)
	if ack1["type"] != "hello_ack" {
		t.Fatalf("first handshake failed: %v", ack1)
	}

	conn2 := mustDialWS(t, serverURL)
	defer conn2.Close()
	mustWriteJSON(t, conn2, baseHello("1"))
	errMsg := mustReadJSON(t, conn2, 2*time.Second)
	if errMsg["type"] != "error" {
		t.Fatalf("expected error, got %v", errMsg)
	}
	if errMsg["code"] != "rate_limited" {
		t.Fatalf("code=%v", errMsg["code"])
	}
}

func TestLiveHandler_InboundAudioRateLimitClosesSession(t *testing.T) {
	fps := 1
	burst := 2
	bps := int64(0)
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		liveMaxAudioFPS:         &fps,
		liveMaxAudioBPS:         &bps,
		liveInboundBurstSeconds: &burst,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack=%v", ack)
	}

	for i := 0; i < 3; i++ {
		mustWriteJSON(t, conn, map[string]any{
			"type":     "audio_frame",
			"seq":      i + 1,
			"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
		})
	}

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("expected error, got %v", msg)
	}
	if msg["code"] != "rate_limited" {
		t.Fatalf("code=%v", msg["code"])
	}
	if msg["close"] != true {
		t.Fatalf("close=%v, want true", msg["close"])
	}
}

func TestLiveHandler_STTLanguageUsesHelloVoiceLanguage(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	voice := hello["voice"].(map[string]any)
	voice["language"] = "es"
	mustWriteJSON(t, conn, hello)

	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack=%v", ack)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.sttProvider.mu.Lock()
		got := h.sttProvider.lastCfg.Language
		h.sttProvider.mu.Unlock()
		if got == "es" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.sttProvider.mu.Lock()
	got := h.sttProvider.lastCfg.Language
	h.sttProvider.mu.Unlock()
	t.Fatalf("stt language=%q, want es", got)
}

type liveHarness struct {
	server      *httptest.Server
	sttProvider *fakeSTTProvider
	tracker     *sessions.Tracker
}

func (h *liveHarness) close() {
	if h != nil && h.server != nil {
		h.server.Close()
	}
}

type liveTestOptions struct {
	sttDeltasPerAudioFrame  [][]stt.TranscriptDelta
	ttsSlow                 bool
	wsMaxSessions           int
	liveTurnTimeout         time.Duration
	liveMaxAudioFPS         *int
	liveMaxAudioBPS         *int64
	liveInboundBurstSeconds *int
}

func newLiveTestServer(t *testing.T, opts liveTestOptions) (*liveHarness, string) {
	t.Helper()
	if opts.wsMaxSessions <= 0 {
		opts.wsMaxSessions = 2
	}

	sttProvider := &fakeSTTProvider{deltasPerAudioFrame: opts.sttDeltasPerAudioFrame}
	ttsProvider := &fakeTTSProvider{slow: opts.ttsSlow}
	factory := fakeProviderFactory{provider: newTalkToUserProvider("test audio")}
	tracker := sessions.NewTracker()

	cfg := config.Config{
		AuthMode:                      config.AuthModeRequired,
		APIKeys:                       map[string]struct{}{"vai_sk_test": {}},
		CORSAllowedOrigins:            map[string]struct{}{},
		ModelAllowlist:                map[string]struct{}{},
		WSMaxSessionDuration:          30 * time.Second,
		WSMaxSessionsPerPrincipal:     opts.wsMaxSessions,
		LiveMaxAudioFrameBytes:        8192,
		LiveMaxJSONMessageBytes:       64 * 1024,
		LiveMaxAudioFPS:               120,
		LiveMaxAudioBytesPerSecond:    128 * 1024,
		LiveInboundBurstSeconds:       2,
		LiveSilenceCommitDuration:     30 * time.Millisecond,
		LiveGraceDuration:             500 * time.Millisecond,
		LiveWSPingInterval:            5 * time.Second,
		LiveWSWriteTimeout:            2 * time.Second,
		LiveWSReadTimeout:             0,
		LiveHandshakeTimeout:          2 * time.Second,
		LiveTurnTimeout:               opts.liveTurnTimeout,
		UpstreamConnectTimeout:        2 * time.Second,
		UpstreamResponseHeaderTimeout: 2 * time.Second,
	}
	if opts.liveMaxAudioFPS != nil {
		cfg.LiveMaxAudioFPS = *opts.liveMaxAudioFPS
	}
	if opts.liveMaxAudioBPS != nil {
		cfg.LiveMaxAudioBytesPerSecond = *opts.liveMaxAudioBPS
	}
	if opts.liveInboundBurstSeconds != nil {
		cfg.LiveInboundBurstSeconds = *opts.liveInboundBurstSeconds
	}

	handler := LiveHandler{
		Config:       cfg,
		Upstreams:    factory,
		HTTPClient:   &http.Client{},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		LiveSessions: tracker,
		Limiter: ratelimit.New(ratelimit.Config{
			MaxConcurrentWSSessions: opts.wsMaxSessions,
		}),
		Lifecycle: &lifecycle.Lifecycle{},
		NewSTTProvider: func(string, *http.Client) session.STTProvider {
			return sttProvider
		},
		NewTTSProvider: func(string, *http.Client) session.TTSProvider {
			return ttsProvider
		},
	}

	srv := httptest.NewServer(handler)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/live"
	return &liveHarness{server: srv, sttProvider: sttProvider, tracker: tracker}, url
}

func baseHello(version string) map[string]any {
	return map[string]any{
		"type":             "hello",
		"protocol_version": version,
		"model":            "anthropic/claude-sonnet-4",
		"auth": map[string]any{
			"mode":            "api_key",
			"gateway_api_key": "vai_sk_test",
		},
		"byok": map[string]any{
			"anthropic": "sk-ant-test",
			"cartesia":  "sk-car-test",
		},
		"audio_in":  map[string]any{"encoding": "pcm_s16le", "sample_rate_hz": 16000, "channels": 1},
		"audio_out": map[string]any{"encoding": "pcm_s16le", "sample_rate_hz": 24000, "channels": 1},
		"voice":     map[string]any{"voice_id": "voice_test", "language": "en", "speed": 1.0, "volume": 1.0},
		"features":  map[string]any{"audio_transport": "base64_json", "want_partial_transcripts": true, "want_assistant_text": true, "client_has_aec": true},
	}
}

func mustDialWS(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func mustWriteJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	if err := conn.WriteJSON(v); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
}

func mustReadJSON(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	out, err := readJSON(conn, timeout)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	return out
}

func readJSON(conn *websocket.Conn, timeout time.Duration) (map[string]any, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type fakeProviderFactory struct {
	provider core.Provider
}

func (f fakeProviderFactory) New(providerName, apiKey string) (core.Provider, error) {
	return f.provider, nil
}

type fakeTalkToUserProvider struct {
	text string
}

func newTalkToUserProvider(text string) core.Provider {
	return &fakeTalkToUserProvider{text: text}
}

func (p *fakeTalkToUserProvider) Name() string { return "fake" }

func (p *fakeTalkToUserProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, io.EOF
}

func (p *fakeTalkToUserProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	return &sliceEventStream{events: talkToUserEvents(p.text)}, nil
}

func (p *fakeTalkToUserProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true, ToolStreaming: true}
}

type sliceEventStream struct {
	events []types.StreamEvent
	idx    int
}

func (s *sliceEventStream) Next() (types.StreamEvent, error) {
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *sliceEventStream) Close() error { return nil }

func talkToUserEvents(text string) []types.StreamEvent {
	msg := types.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant", Model: "test", Content: []types.ContentBlock{}}
	toolStart := types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "tool_1", Name: "talk_to_user", Input: map[string]any{}}}
	toolDelta := types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `{"text":"` + text + `"}`}}
	msgDelta := types.MessageDeltaEvent{Type: "message_delta", Usage: types.Usage{}}
	msgDelta.Delta.StopReason = types.StopReasonToolUse
	return []types.StreamEvent{
		types.MessageStartEvent{Type: "message_start", Message: msg},
		toolStart,
		toolDelta,
		types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
		msgDelta,
		types.MessageStopEvent{Type: "message_stop"},
	}
}

type fakeSTTProvider struct {
	mu                  sync.Mutex
	deltasPerAudioFrame [][]stt.TranscriptDelta
	sendCalls           int
	lastCfg             session.STTConfig
}

func (p *fakeSTTProvider) NewSession(ctx context.Context, cfg session.STTConfig) (session.STTSession, error) {
	p.mu.Lock()
	p.lastCfg = cfg
	p.mu.Unlock()
	return &fakeSTTSession{provider: p, deltas: make(chan stt.TranscriptDelta, 16)}, nil
}

type fakeSTTSession struct {
	provider *fakeSTTProvider
	deltas   chan stt.TranscriptDelta
	calls    int
}

func (s *fakeSTTSession) SendAudio(data []byte) error {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	s.provider.sendCalls++
	if s.calls < len(s.provider.deltasPerAudioFrame) {
		for _, d := range s.provider.deltasPerAudioFrame[s.calls] {
			s.deltas <- d
		}
	}
	s.calls++
	return nil
}

func (s *fakeSTTSession) FinalizeUtterance() error { return nil }

func (s *fakeSTTSession) Deltas() <-chan stt.TranscriptDelta { return s.deltas }

func (s *fakeSTTSession) Close() error {
	close(s.deltas)
	return nil
}

type fakeTTSProvider struct {
	slow bool
}

func (p *fakeTTSProvider) NewContext(ctx context.Context, cfg session.TTSConfig) (session.TTSContext, error) {
	return newFakeTTSContext(ctx, p.slow), nil
}

type fakeTTSContext struct {
	ctx    context.Context
	audio  chan []byte
	done   chan struct{}
	closeM sync.Once
	slow   bool
}

func newFakeTTSContext(ctx context.Context, slow bool) *fakeTTSContext {
	return &fakeTTSContext{
		ctx:   ctx,
		audio: make(chan []byte, 16),
		done:  make(chan struct{}),
		slow:  slow,
	}
}

func (c *fakeTTSContext) SendText(text string, isFinal bool) error {
	if !c.slow {
		select {
		case <-c.done:
			return nil
		default:
		}
		c.audio <- []byte("chunk1")
		if isFinal {
			c.close()
		}
		return nil
	}

	go func() {
		defer func() {
			_ = recover()
		}()
		c.audio <- []byte("chunk1")
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-c.ctx.Done():
				c.close()
				return
			case <-c.done:
				return
			case <-ticker.C:
				c.audio <- []byte("chunk_more")
			}
		}
	}()
	return nil
}

func (c *fakeTTSContext) Flush() error {
	if !c.slow {
		c.close()
	}
	return nil
}

func (c *fakeTTSContext) Audio() <-chan []byte { return c.audio }

func (c *fakeTTSContext) Done() <-chan struct{} { return c.done }

func (c *fakeTTSContext) Err() error { return nil }

func (c *fakeTTSContext) Close() error {
	c.close()
	return nil
}

func (c *fakeTTSContext) close() {
	c.closeM.Do(func() {
		close(c.done)
		close(c.audio)
	})
}

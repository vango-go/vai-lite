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

func TestLiveHandler_ElevenLabsRequiresPlaybackMarks(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	voice := hello["voice"].(map[string]any)
	voice["provider"] = "elevenlabs"
	byok := hello["byok"].(map[string]any)
	byok["elevenlabs"] = "sk-el-test"
	mustWriteJSON(t, conn, hello)

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("type=%v", msg["type"])
	}
	if msg["code"] != "bad_request" {
		t.Fatalf("code=%v", msg["code"])
	}
}

func TestLiveHandler_ElevenLabsRequiresBYOK(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	voice := hello["voice"].(map[string]any)
	voice["provider"] = "elevenlabs"
	features := hello["features"].(map[string]any)
	features["send_playback_marks"] = true
	mustWriteJSON(t, conn, hello)

	msg := mustReadJSON(t, conn, 2*time.Second)
	if msg["type"] != "error" {
		t.Fatalf("type=%v", msg["type"])
	}
	if msg["code"] != "unauthorized" {
		t.Fatalf("code=%v", msg["code"])
	}
}

func TestLiveHandler_ElevenLabsAckAdvertisesAlignment(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	fakeEleven := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer fakeEleven.Close()

	elevenBase := "ws" + strings.TrimPrefix(fakeEleven.URL, "http") + "/v1/text-to-speech/{voice_id}/stream-input"
	h, serverURL := newLiveTestServer(t, liveTestOptions{liveElevenLabsWSBaseURL: elevenBase})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	voice := hello["voice"].(map[string]any)
	voice["provider"] = "elevenlabs"
	features := hello["features"].(map[string]any)
	features["send_playback_marks"] = true
	byok := hello["byok"].(map[string]any)
	byok["elevenlabs"] = "sk-el-test"
	mustWriteJSON(t, conn, hello)

	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("type=%v payload=%+v", ack["type"], ack)
	}
	featuresResp, ok := ack["features"].(map[string]any)
	if !ok {
		t.Fatalf("missing features in ack")
	}
	if featuresResp["supports_alignment"] != true {
		t.Fatalf("supports_alignment=%v", featuresResp["supports_alignment"])
	}
	if featuresResp["alignment_kind"] != "char" {
		t.Fatalf("alignment_kind=%v", featuresResp["alignment_kind"])
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

func TestLiveHandler_EarlyTalkToUserStreamingStartsAudioAndCaptionsBeforeToolStop(t *testing.T) {
	gate := make(chan struct{})
	provider := &gatedTalkToUserProvider{
		gates: map[int]<-chan struct{}{
			3: gate, // block before the second delta (and therefore before tool stop)
		},
		events: streamingTalkToUserEvents([]string{
			`{"text":"Hello you `,
			`there."}`,
		}),
	}

	h, serverURL := newLiveTestServer(t, liveTestOptions{
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{{{Text: "hello", IsFinal: true}}},
		ttsSlow:                false,
		provider:               provider,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	_ = mustReadJSON(t, conn, 2*time.Second) // hello_ack

	_ = conn.SetReadDeadline(time.Time{})
	msgCh, errCh := startWSJSONReader(conn)

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      1,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	var (
		assistantID string
		deltas      strings.Builder
		seenStart   bool
		seenDelta   bool
		seenChunk   bool
		seenFinal   bool
		seenTypes   = make(map[string]int)
	)

	earlyDeadline := time.NewTimer(2 * time.Second)
	defer earlyDeadline.Stop()
	for !(seenStart && seenDelta && seenChunk) {
		select {
		case <-earlyDeadline.C:
			t.Fatalf("missing early events start=%v delta=%v audio_chunk=%v assistant_id=%q deltas=%q seen=%v ws_err=%v", seenStart, seenDelta, seenChunk, assistantID, deltas.String(), seenTypes, drainErr(errCh))
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatalf("websocket closed while waiting for early events: ws_err=%v seen=%v", drainErr(errCh), seenTypes)
			}
			typ, _ := msg["type"].(string)
			if typ != "" {
				seenTypes[typ]++
			}
			id := assistantIDFromMsg(msg)
			if assistantID == "" && id != "" {
				assistantID = id
			}
			switch typ {
			case "assistant_audio_start":
				if assistantID == "" {
					t.Fatalf("missing assistant_audio_id on assistant_audio_start: %+v", msg)
				}
				if txt, ok := msg["text"].(string); ok && txt != "" {
					t.Fatalf("assistant_audio_start.text=%q, want empty/omitted for early captions", txt)
				}
				seenStart = true
			case "assistant_text_delta":
				if assistantID == "" || id != assistantID {
					continue
				}
				delta, _ := msg["delta"].(string)
				deltas.WriteString(delta)
				seenDelta = true
			case "assistant_audio_chunk", "assistant_audio_chunk_header":
				if assistantID == "" || id != assistantID {
					continue
				}
				seenChunk = true
			case "assistant_text_final":
				if assistantID == "" || id != assistantID {
					continue
				}
				seenFinal = true
			}
		}
	}
	if seenFinal {
		t.Fatalf("saw assistant_text_final before tool stop was allowed")
	}

	close(gate) // allow the tool JSON to complete and tool block to stop

	var finalText string
	finalDeadline := time.NewTimer(2 * time.Second)
	defer finalDeadline.Stop()
	for finalText == "" {
		select {
		case <-finalDeadline.C:
			t.Fatalf("did not observe assistant_text_final: assistant_id=%q deltas=%q seen=%v ws_err=%v", assistantID, deltas.String(), seenTypes, drainErr(errCh))
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatalf("websocket closed while waiting for assistant_text_final: ws_err=%v", drainErr(errCh))
			}
			if assistantIDFromMsg(msg) != assistantID {
				continue
			}
			typ, _ := msg["type"].(string)
			if typ == "assistant_text_delta" {
				if delta, ok := msg["delta"].(string); ok {
					deltas.WriteString(delta)
				}
			}
			if typ == "assistant_text_final" {
				if text, ok := msg["text"].(string); ok {
					finalText = text
				}
			}
		}
	}

	if finalText != deltas.String() {
		t.Fatalf("assistant_text_final.text=%q, want concatenation of deltas=%q", finalText, deltas.String())
	}
	if finalText != "Hello you there." {
		t.Fatalf("assistant_text_final.text=%q, want %q", finalText, "Hello you there.")
	}
}

func TestLiveHandler_EarlyTalkToUserStreaming_InterruptCancelsStreamingAudio(t *testing.T) {
	gate := make(chan struct{})
	provider := &gatedTalkToUserProvider{
		gates: map[int]<-chan struct{}{
			3: gate,
		},
		events: streamingTalkToUserEvents([]string{
			`{"text":"Hello you `,
			`there."}`,
		}),
	}

	h, serverURL := newLiveTestServer(t, liveTestOptions{
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{{{Text: "interrupt", IsFinal: true}}},
		ttsSlow:                false,
		provider:               provider,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	mustWriteJSON(t, conn, baseHello("1"))
	_ = mustReadJSON(t, conn, 2*time.Second) // hello_ack

	_ = conn.SetReadDeadline(time.Time{})
	msgCh, errCh := startWSJSONReader(conn)

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      1,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	var assistantID string
	startDeadline := time.NewTimer(2 * time.Second)
	defer startDeadline.Stop()
	for assistantID == "" {
		select {
		case <-startDeadline.C:
			t.Fatalf("did not observe assistant_audio_start with assistant_audio_id: ws_err=%v", drainErr(errCh))
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatalf("websocket closed while waiting for assistant_audio_start: ws_err=%v", drainErr(errCh))
			}
			if msg["type"] != "assistant_audio_start" {
				continue
			}
			assistantID = assistantIDFromMsg(msg)
		}
	}

	mustWriteJSON(t, conn, map[string]any{"type": "control", "op": "interrupt"})

	seenReset := false
	resetDeadline := time.NewTimer(2 * time.Second)
	defer resetDeadline.Stop()
	for !seenReset {
		select {
		case <-resetDeadline.C:
			t.Fatalf("did not observe audio_reset: assistant_id=%q ws_err=%v", assistantID, drainErr(errCh))
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatalf("websocket closed while waiting for audio_reset: ws_err=%v", drainErr(errCh))
			}
			if msg["type"] != "audio_reset" {
				continue
			}
			if msg["assistant_audio_id"] != assistantID {
				t.Fatalf("assistant_audio_id=%v want %v", msg["assistant_audio_id"], assistantID)
			}
			seenReset = true
		}
	}
	if !seenReset {
		t.Fatalf("did not observe audio_reset")
	}

	close(gate)

	window := time.NewTimer(300 * time.Millisecond)
	defer window.Stop()
	for {
		select {
		case <-window.C:
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			if assistantIDFromMsg(msg) != assistantID {
				continue
			}
			switch msg["type"] {
			case "assistant_audio_chunk", "assistant_audio_chunk_header", "assistant_audio_end", "assistant_text_delta", "assistant_text_final":
				t.Fatalf("received stale assistant output after audio_reset: %+v", msg)
			}
		}
	}
}

func TestLiveHandler_GraceStopsAppendingAfterAssistantFinishes(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		liveGraceDuration: 2 * time.Second,
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{
			{{Text: "hello world", IsFinal: true}},
			{{Text: "more", IsFinal: true}},
		},
		ttsSlow: false,
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

	var firstFinal string
	seenAssistantEnd := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg := mustReadJSON(t, conn, 2*time.Second)
		if msg["type"] == "utterance_final" && firstFinal == "" {
			if text, ok := msg["text"].(string); ok {
				firstFinal = text
			}
		}
		if msg["type"] == "assistant_audio_end" {
			seenAssistantEnd = true
			break
		}
	}
	if firstFinal != "hello world" {
		t.Fatalf("first utterance_final text=%q, want %q", firstFinal, "hello world")
	}
	if !seenAssistantEnd {
		t.Fatalf("did not observe assistant_audio_end for first turn")
	}

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      2,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	var secondFinal string
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg := mustReadJSON(t, conn, 2*time.Second)
		if msg["type"] != "utterance_final" {
			continue
		}
		if text, ok := msg["text"].(string); ok {
			secondFinal = text
		}
		break
	}
	if secondFinal != "more" {
		t.Fatalf("second utterance_final text=%q, want %q", secondFinal, "more")
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

func TestLiveHandler_HelloMessagesSeedFirstTurnHistory(t *testing.T) {
	provider := newSeedHistoryProvider("seeded reply")
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{{{Text: "live input", IsFinal: true}}},
		provider:               provider,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["messages"] = []any{
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "seed user"},
			},
		},
		map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "text", "text": "seed assistant"},
			},
		},
	}
	mustWriteJSON(t, conn, hello)

	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v payload=%+v", ack["type"], ack)
	}

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      1,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	req := provider.awaitFirstReq(t, 3*time.Second)
	if len(req.Messages) < 3 {
		t.Fatalf("expected seeded history + utterance, got %d messages", len(req.Messages))
	}
	if req.Messages[0].Role != "user" || req.Messages[0].TextContent() != "seed user" {
		t.Fatalf("message[0]=role:%q text:%q", req.Messages[0].Role, req.Messages[0].TextContent())
	}
	if req.Messages[1].Role != "assistant" || req.Messages[1].TextContent() != "seed assistant" {
		t.Fatalf("message[1]=role:%q text:%q", req.Messages[1].Role, req.Messages[1].TextContent())
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

func TestLiveHandler_ServerTools_InferFetchProviderFromBYOK(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["tools"] = map[string]any{
		"server_tools": []any{"vai_web_fetch"},
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

func TestLiveHandler_ServerTools_ExplicitFetchTavilyWithoutKeyUnauthorized(t *testing.T) {
	h, serverURL := newLiveTestServer(t, liveTestOptions{})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	hello["tools"] = map[string]any{
		"server_tools": []any{"vai_web_fetch"},
		"server_tool_config": map[string]any{
			"vai_web_fetch": map[string]any{"provider": "tavily"},
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
	details, ok := msg["details"].(map[string]any)
	if !ok {
		t.Fatalf("details missing in payload=%+v", msg)
	}
	if details["requires_byok_header"] != "X-Provider-Key-Tavily" {
		t.Fatalf("requires_byok_header=%v payload=%+v", details["requires_byok_header"], msg)
	}
}

func TestLiveHandler_ClientToolRPCRoundTrip(t *testing.T) {
	provider := &clientToolRPCProvider{}
	h, serverURL := newLiveTestServer(t, liveTestOptions{
		sttDeltasPerAudioFrame: [][]stt.TranscriptDelta{{{Text: "do tool", IsFinal: true}}},
		ttsSlow:                false,
		provider:               provider,
	})
	defer h.close()

	conn := mustDialWS(t, serverURL)
	defer conn.Close()

	hello := baseHello("1")
	features := hello["features"].(map[string]any)
	features["want_run_events"] = true
	hello["tools"] = map[string]any{
		"client_tools": []any{
			map[string]any{
				"type":        "function",
				"name":        "client_echo",
				"description": "Echo text",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
					"required":             []any{"value"},
					"additionalProperties": false,
				},
			},
		},
	}
	mustWriteJSON(t, conn, hello)

	ack := mustReadJSON(t, conn, 2*time.Second)
	if ack["type"] != "hello_ack" {
		t.Fatalf("ack type=%v payload=%+v", ack["type"], ack)
	}

	mustWriteJSON(t, conn, map[string]any{
		"type":     "audio_frame",
		"seq":      1,
		"data_b64": base64.StdEncoding.EncodeToString([]byte("pcm")),
	})

	var (
		gotToolCall         bool
		toolCallID          string
		toolCallTurnID      float64
		seenRunToolResult   bool
		seenAssistantSpeech bool
	)
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()

	for !(seenRunToolResult && seenAssistantSpeech) {
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for client tool flow events: gotToolCall=%v seenRunToolResult=%v seenAssistantSpeech=%v", gotToolCall, seenRunToolResult, seenAssistantSpeech)
		default:
		}
		msg, err := readJSON(conn, 500*time.Millisecond)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("read websocket frame: %v", err)
		}
		switch msg["type"] {
		case "tool_call":
			gotToolCall = true
			toolCallID, _ = msg["id"].(string)
			toolCallTurnID, _ = msg["turn_id"].(float64)
			mustWriteJSON(t, conn, map[string]any{
				"type":    "tool_result",
				"turn_id": int(toolCallTurnID),
				"id":      toolCallID,
				"content": []any{
					map[string]any{"type": "text", "text": "tool ok"},
				},
			})
		case "run_event":
			evRaw, _ := msg["event"].(map[string]any)
			if evRaw != nil && evRaw["type"] == "tool_result" {
				seenRunToolResult = true
			}
		case "assistant_audio_start":
			seenAssistantSpeech = true
		case "error":
			t.Fatalf("received error frame: %+v", msg)
		}
	}

	if !gotToolCall {
		t.Fatalf("expected tool_call frame")
	}
	if toolCallID == "" || toolCallTurnID <= 0 {
		t.Fatalf("invalid tool_call payload id=%q turn_id=%v", toolCallID, toolCallTurnID)
	}
	if !provider.sawToolResult() {
		t.Fatalf("expected provider to observe tool_result in follow-up model turn")
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
	liveGraceDuration       time.Duration
	liveMaxAudioFPS         *int
	liveMaxAudioBPS         *int64
	liveInboundBurstSeconds *int
	liveElevenLabsWSBaseURL string
	provider                core.Provider
}

func newLiveTestServer(t *testing.T, opts liveTestOptions) (*liveHarness, string) {
	t.Helper()
	if opts.wsMaxSessions <= 0 {
		opts.wsMaxSessions = 2
	}

	sttProvider := &fakeSTTProvider{deltasPerAudioFrame: opts.sttDeltasPerAudioFrame}
	ttsProvider := &fakeTTSProvider{slow: opts.ttsSlow}
	provider := opts.provider
	if provider == nil {
		provider = newTalkToUserProvider("test audio")
	}
	factory := fakeProviderFactory{provider: provider}
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
		LiveMaxUnplayedDuration:       2500 * time.Millisecond,
		LivePlaybackStopWait:          500 * time.Millisecond,
		LiveToolTimeout:               10 * time.Second,
		LiveMaxToolCallsPerTurn:       5,
		LiveMaxModelCallsPerTurn:      8,
		LiveMaxBackpressurePerMin:     3,
		LiveElevenLabsWSBaseURL:       opts.liveElevenLabsWSBaseURL,
		UpstreamConnectTimeout:        2 * time.Second,
		UpstreamResponseHeaderTimeout: 2 * time.Second,
	}
	if opts.liveGraceDuration > 0 {
		cfg.LiveGraceDuration = opts.liveGraceDuration
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
		"voice":     map[string]any{"provider": "cartesia", "voice_id": "voice_test", "language": "en", "speed": 1.0, "volume": 1.0},
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

func startWSJSONReader(conn *websocket.Conn) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any, 64)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			out <- msg
		}
	}()
	return out, errCh
}

func assistantIDFromMsg(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	if v, ok := msg["assistant_audio_id"].(string); ok {
		return v
	}
	return ""
}

func drainErr(ch <-chan error) error {
	select {
	case err, ok := <-ch:
		if !ok {
			return nil
		}
		return err
	default:
		return nil
	}
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

type seedHistoryProvider struct {
	text       string
	firstReqCh chan types.MessageRequest
}

func newSeedHistoryProvider(text string) *seedHistoryProvider {
	return &seedHistoryProvider{
		text:       text,
		firstReqCh: make(chan types.MessageRequest, 1),
	}
}

func (p *seedHistoryProvider) Name() string { return "seed_history_provider" }

func (p *seedHistoryProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, io.EOF
}

func (p *seedHistoryProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	reqCopy := types.MessageRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Stream:    req.Stream,
	}
	reqCopy.Messages = append(reqCopy.Messages, req.Messages...)
	reqCopy.Tools = append(reqCopy.Tools, req.Tools...)
	select {
	case p.firstReqCh <- reqCopy:
	default:
	}
	return &sliceEventStream{events: talkToUserEvents(p.text)}, nil
}

func (p *seedHistoryProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true, ToolStreaming: true}
}

func (p *seedHistoryProvider) awaitFirstReq(t *testing.T, timeout time.Duration) types.MessageRequest {
	t.Helper()
	select {
	case req := <-p.firstReqCh:
		return req
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for first provider request")
		return types.MessageRequest{}
	}
}

type clientToolRPCProvider struct {
	mu            sync.Mutex
	streamCalls   int
	sawResultCall bool
}

func (p *clientToolRPCProvider) Name() string { return "client_tool_rpc_provider" }

func (p *clientToolRPCProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, io.EOF
}

func (p *clientToolRPCProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	p.mu.Lock()
	p.streamCalls++
	call := p.streamCalls
	if call > 1 && containsToolResultFor(req.Messages, "tool_client_1") {
		p.sawResultCall = true
	}
	p.mu.Unlock()

	if call == 1 {
		return &sliceEventStream{events: functionToolUseEvents("tool_client_1", "client_echo", `{"value":"ping"}`)}, nil
	}
	return &sliceEventStream{events: talkToUserEvents("done")}, nil
}

func (p *clientToolRPCProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true, ToolStreaming: true}
}

func (p *clientToolRPCProvider) sawToolResult() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawResultCall
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

func functionToolUseEvents(toolID, toolName, inputJSON string) []types.StreamEvent {
	msg := types.MessageResponse{ID: "msg_tool_1", Type: "message", Role: "assistant", Model: "test", Content: []types.ContentBlock{}}
	toolStart := types.ContentBlockStartEvent{
		Type:  "content_block_start",
		Index: 0,
		ContentBlock: types.ToolUseBlock{
			Type:  "tool_use",
			ID:    toolID,
			Name:  toolName,
			Input: map[string]any{},
		},
	}
	toolDelta := types.ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: inputJSON},
	}
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

func containsToolResultFor(messages []types.Message, toolUseID string) bool {
	for i := range messages {
		for _, block := range messages[i].ContentBlocks() {
			switch b := block.(type) {
			case types.ToolResultBlock:
				if strings.TrimSpace(b.ToolUseID) == strings.TrimSpace(toolUseID) {
					return true
				}
			case *types.ToolResultBlock:
				if b != nil && strings.TrimSpace(b.ToolUseID) == strings.TrimSpace(toolUseID) {
					return true
				}
			}
		}
	}
	return false
}

type gatedTalkToUserProvider struct {
	events []types.StreamEvent
	gates  map[int]<-chan struct{}
}

func (p *gatedTalkToUserProvider) Name() string { return "gated_fake" }

func (p *gatedTalkToUserProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, io.EOF
}

func (p *gatedTalkToUserProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	_ = req
	return &gatedEventStream{ctx: ctx, events: p.events, gates: p.gates}, nil
}

func (p *gatedTalkToUserProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true, ToolStreaming: true}
}

type gatedEventStream struct {
	ctx    context.Context
	events []types.StreamEvent
	gates  map[int]<-chan struct{}
	idx    int
}

func (s *gatedEventStream) Next() (types.StreamEvent, error) {
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	if ch := s.gates[s.idx]; ch != nil {
		select {
		case <-ch:
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *gatedEventStream) Close() error { return nil }

func streamingTalkToUserEvents(parts []string) []types.StreamEvent {
	msg := types.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant", Model: "test", Content: []types.ContentBlock{}}
	toolStart := types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "tool_1", Name: "talk_to_user", Input: map[string]any{}}}
	msgDelta := types.MessageDeltaEvent{Type: "message_delta", Usage: types.Usage{}}
	msgDelta.Delta.StopReason = types.StopReasonToolUse

	events := []types.StreamEvent{
		types.MessageStartEvent{Type: "message_start", Message: msg},
		toolStart,
	}
	for _, part := range parts {
		events = append(events, types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: part},
		})
	}
	events = append(events,
		types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
		msgDelta,
		types.MessageStopEvent{Type: "message_stop"},
	)
	return events
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

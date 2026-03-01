package vai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLiveConnect_MissingModelProviderKey(t *testing.T) {
	t.Parallel()

	client := NewClient(
		WithBaseURL("http://127.0.0.1:8080"),
		WithProviderKey("cartesia", "sk-cartesia"),
	)

	_, err := client.Live.Connect(context.Background(), &LiveConnectRequest{
		Model: "anthropic/claude-sonnet-4",
		Voice: LiveVoice{Provider: "cartesia", VoiceID: "voice_test"},
	})
	if err == nil {
		t.Fatalf("expected missing provider key error")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("error=%q, expected ANTHROPIC_API_KEY hint", err.Error())
	}
}

func TestLiveConnect_MissingCartesiaKey(t *testing.T) {
	t.Parallel()

	client := NewClient(
		WithBaseURL("http://127.0.0.1:8080"),
		WithProviderKey("anthropic", "sk-ant"),
	)

	_, err := client.Live.Connect(context.Background(), &LiveConnectRequest{
		Model: "anthropic/claude-sonnet-4",
		Voice: LiveVoice{Provider: "cartesia", VoiceID: "voice_test"},
	})
	if err == nil {
		t.Fatalf("expected missing cartesia key error")
	}
	if !strings.Contains(err.Error(), "CARTESIA_API_KEY") {
		t.Fatalf("error=%q, expected CARTESIA_API_KEY hint", err.Error())
	}
}

func TestLiveRunStream_MapsAssistantTextDeltaToTextDeltaFrom(t *testing.T) {
	t.Parallel()

	serverURL, closeServer := newLiveWebsocketTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()

		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			return
		}

		_ = conn.WriteJSON(map[string]any{
			"type":             "hello_ack",
			"protocol_version": "1",
			"session_id":       "sess_test",
			"audio_in":         map[string]any{"encoding": "pcm_s16le", "sample_rate_hz": 16000, "channels": 1},
			"audio_out":        map[string]any{"encoding": "pcm_s16le", "sample_rate_hz": 24000, "channels": 1},
			"features":         map[string]any{"audio_transport": "base64_json"},
			"resume":           map[string]any{"supported": false, "accepted": false},
		})
		_ = conn.WriteJSON(map[string]any{
			"type":               "assistant_text_delta",
			"assistant_audio_id": "a_1",
			"delta":              "hello live",
		})
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(2*time.Second))
	})
	defer closeServer()

	client := NewClient(
		WithBaseURL(serverURL),
		WithProviderKey("anthropic", "sk-ant"),
		WithProviderKey("cartesia", "sk-cartesia"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Live.RunStream(ctx, &LiveRunRequest{
		Model: "anthropic/claude-sonnet-4",
		Voice: LiveVoice{Provider: "cartesia", VoiceID: "voice_test"},
	})
	if err != nil {
		t.Fatalf("RunStream error: %v", err)
	}
	defer stream.Close()

	var gotText strings.Builder
	for event := range stream.Events() {
		if text, ok := TextDeltaFrom(event); ok {
			gotText.WriteString(text)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if gotText.String() != "hello live" {
		t.Fatalf("text=%q, want %q", gotText.String(), "hello live")
	}
}

func TestLiveRunStream_ClientToolCallbackSendsToolResult(t *testing.T) {
	t.Parallel()

	toolResultCh := make(chan map[string]any, 1)
	serverURL, closeServer := newLiveWebsocketTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()

		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			return
		}

		_ = conn.WriteJSON(map[string]any{
			"type":             "hello_ack",
			"protocol_version": "1",
			"session_id":       "sess_tools",
			"audio_in":         map[string]any{"encoding": "pcm_s16le", "sample_rate_hz": 16000, "channels": 1},
			"audio_out":        map[string]any{"encoding": "pcm_s16le", "sample_rate_hz": 24000, "channels": 1},
			"features":         map[string]any{"audio_transport": "base64_json"},
			"resume":           map[string]any{"supported": false, "accepted": false},
		})

		_ = conn.WriteJSON(map[string]any{
			"type":    "tool_call",
			"turn_id": 1,
			"id":      "tc_1",
			"name":    "echo_tool",
			"input":   map[string]any{"text": "ping"},
		})

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var result map[string]any
		if err := conn.ReadJSON(&result); err == nil {
			toolResultCh <- result
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(2*time.Second))
	})
	defer closeServer()

	client := NewClient(
		WithBaseURL(serverURL),
		WithProviderKey("anthropic", "sk-ant"),
		WithProviderKey("cartesia", "sk-cartesia"),
	)

	tool := MakeTool("echo_tool", "Echo input", func(ctx context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "pong: " + in.Text, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Live.RunStream(ctx, &LiveRunRequest{
		Model: "anthropic/claude-sonnet-4",
		Voice: LiveVoice{Provider: "cartesia", VoiceID: "voice_test"},
	}, WithTools(tool))
	if err != nil {
		t.Fatalf("RunStream error: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
		// drain until close
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}

	select {
	case result := <-toolResultCh:
		if result["type"] != "tool_result" {
			t.Fatalf("type=%v payload=%+v", result["type"], result)
		}
		if result["id"] != "tc_1" {
			t.Fatalf("id=%v payload=%+v", result["id"], result)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("content missing in payload=%+v", result)
		}
		first, _ := content[0].(map[string]any)
		if first["type"] != "text" || first["text"] != "pong: ping" {
			t.Fatalf("content[0]=%+v", first)
		}
	default:
		t.Fatalf("expected tool_result frame from client")
	}
}

func newLiveWebsocketTestServer(t *testing.T, handler func(conn *websocket.Conn)) (string, func()) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/live" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler(conn)
	}))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	return wsURL, server.Close
}

func TestLiveConnect_FirstFrameErrorSurfaces(t *testing.T) {
	t.Parallel()

	serverURL, closeServer := newLiveWebsocketTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		var hello json.RawMessage
		_ = conn.ReadJSON(&hello)
		_ = conn.WriteJSON(map[string]any{
			"type":    "error",
			"scope":   "session",
			"code":    "unauthorized",
			"message": "missing key",
			"close":   true,
		})
	})
	defer closeServer()

	client := NewClient(
		WithBaseURL(serverURL),
		WithProviderKey("anthropic", "sk-ant"),
		WithProviderKey("cartesia", "sk-cartesia"),
	)

	_, err := client.Live.Connect(context.Background(), &LiveConnectRequest{
		Model: "anthropic/claude-sonnet-4",
		Voice: LiveVoice{Provider: "cartesia", VoiceID: "voice_test"},
	})
	if err == nil {
		t.Fatalf("expected connect error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "missing key") {
		t.Fatalf("error=%q", err.Error())
	}
}

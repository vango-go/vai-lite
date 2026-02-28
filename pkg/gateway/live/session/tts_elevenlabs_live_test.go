package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestElevenLabsLiveConn_ParsesNormalizedAlignment(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read init + text messages.
		for i := 0; i < 2; i++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}

		audio := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04})
		_ = conn.WriteJSON(map[string]any{
			"context_id": "a_1",
			"audio":      audio,
			"normalizedAlignment": map[string]any{
				"chars":            []string{"h", "i"},
				"charStartTimesMs": []int{0, 40},
				"charDurationsMs":  []int{40, 40},
			},
		})
		_ = conn.WriteJSON(map[string]any{
			"context_id": "a_1",
			"isFinal":    true,
		})
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/text-to-speech/{voice_id}/stream-input"
	conn, err := newElevenLabsLiveConn(context.Background(), elevenLabsLiveConfig{
		APIKey:    "sk-el-test",
		VoiceID:   "voice_1",
		BaseWSURL: base,
	})
	if err != nil {
		t.Fatalf("newElevenLabsLiveConn error: %v", err)
	}
	defer conn.Close()

	if err := conn.StartContext(context.Background(), "a_1"); err != nil {
		t.Fatalf("StartContext error: %v", err)
	}
	if err := conn.SendText(context.Background(), "a_1", "hi", true); err != nil {
		t.Fatalf("SendText error: %v", err)
	}

	select {
	case chunk := <-conn.Chunks():
		if chunk.ContextID != "a_1" {
			t.Fatalf("context_id=%q", chunk.ContextID)
		}
		if len(chunk.Audio) == 0 {
			t.Fatalf("expected audio chunk")
		}
		if chunk.Alignment == nil {
			t.Fatalf("expected alignment")
		}
		if chunk.Alignment.Kind != "char" || !chunk.Alignment.Normalized {
			payload, _ := json.Marshal(chunk.Alignment)
			t.Fatalf("alignment=%s", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for audio chunk")
	}

	select {
	case final := <-conn.Chunks():
		if !final.Final {
			t.Fatalf("expected final chunk")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for final chunk")
	}
}

func TestElevenLabsLiveConn_DecodesUnpaddedBase64Audio(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read init + text messages.
		for i := 0; i < 2; i++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}

		// RawStdEncoding omits '=' padding.
		audio := base64.RawStdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04})
		_ = conn.WriteJSON(map[string]any{
			"context_id": "a_1",
			"audio":      audio,
		})
		_ = conn.WriteJSON(map[string]any{
			"context_id": "a_1",
			"isFinal":    true,
		})
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/text-to-speech/{voice_id}/stream-input"
	conn, err := newElevenLabsLiveConn(context.Background(), elevenLabsLiveConfig{
		APIKey:    "sk-el-test",
		VoiceID:   "voice_1",
		BaseWSURL: base,
	})
	if err != nil {
		t.Fatalf("newElevenLabsLiveConn error: %v", err)
	}
	defer conn.Close()

	if err := conn.StartContext(context.Background(), "a_1"); err != nil {
		t.Fatalf("StartContext error: %v", err)
	}
	if err := conn.SendText(context.Background(), "a_1", "hi", true); err != nil {
		t.Fatalf("SendText error: %v", err)
	}

	select {
	case chunk := <-conn.Chunks():
		if chunk.ContextID != "a_1" {
			t.Fatalf("context_id=%q", chunk.ContextID)
		}
		if got := chunk.Audio; len(got) != 4 {
			t.Fatalf("audio len=%d, want 4", len(got))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for audio chunk")
	}
}

package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestCreateMessage_AppliesPathAuthAndExtraHeaders(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotReferer string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("X-API-Key")
		gotReferer = r.Header.Get("HTTP-Referer")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"chatcmpl_1",
			"model":"gpt-4o-mini",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer server.Close()

	p := New(
		"test-key",
		WithBaseURL(server.URL),
		WithChatCompletionsPath("custom/chat/completions"),
		WithAuth(AuthConfig{Header: "X-API-Key"}),
		WithExtraHeader("HTTP-Referer", "https://example.com"),
	)

	resp, err := p.CreateMessage(t.Context(), &types.MessageRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}

	if gotPath != "/custom/chat/completions" {
		t.Fatalf("path = %q, want /custom/chat/completions", gotPath)
	}
	if gotAuth != "test-key" {
		t.Fatalf("X-API-Key header = %q, want test-key", gotAuth)
	}
	if gotReferer != "https://example.com" {
		t.Fatalf("HTTP-Referer header = %q, want https://example.com", gotReferer)
	}
	if _, exists := gotBody["max_completion_tokens"]; !exists {
		t.Fatalf("request missing max_completion_tokens field: %#v", gotBody)
	}
	if resp.Model != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q, want openai/gpt-4o-mini", resp.Model)
	}
}

func TestStreamMessage_StreamIncludeUsageOption(t *testing.T) {
	t.Run("default true includes stream_options", func(t *testing.T) {
		var gotBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer server.Close()

		p := New("test-key", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
		stream, err := p.StreamMessage(t.Context(), &types.MessageRequest{
			Model: "gpt-4o-mini",
			Messages: []types.Message{
				{Role: "user", Content: "hello"},
			},
		})
		if err != nil {
			t.Fatalf("StreamMessage() error = %v", err)
		}
		defer stream.Close()

		if _, err := stream.Next(); err != nil {
			t.Fatalf("Next() error = %v", err)
		}

		streamOptions, ok := gotBody["stream_options"].(map[string]any)
		if !ok {
			t.Fatalf("stream_options missing: %#v", gotBody)
		}
		if includeUsage, _ := streamOptions["include_usage"].(bool); !includeUsage {
			t.Fatalf("include_usage = %v, want true", streamOptions["include_usage"])
		}
	})

	t.Run("false omits stream_options", func(t *testing.T) {
		var gotBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer server.Close()

		p := New(
			"test-key",
			WithBaseURL(server.URL),
			WithHTTPClient(server.Client()),
			WithStreamIncludeUsage(false),
		)
		stream, err := p.StreamMessage(t.Context(), &types.MessageRequest{
			Model: "gpt-4o-mini",
			Messages: []types.Message{
				{Role: "user", Content: "hello"},
			},
		})
		if err != nil {
			t.Fatalf("StreamMessage() error = %v", err)
		}
		defer stream.Close()

		if _, err := stream.Next(); err != nil {
			t.Fatalf("Next() error = %v", err)
		}

		if _, exists := gotBody["stream_options"]; exists {
			t.Fatalf("stream_options should be omitted when disabled: %#v", gotBody)
		}
	})
}

package groq

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestNew_AppliesOptionsAndCapabilities(t *testing.T) {
	client := &http.Client{}
	p := New(
		"test-key",
		WithBaseURL("https://example.com"),
		WithHTTPClient(client),
		WithMaxTokensField("max_completion_tokens"),
	)

	if p.baseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, want https://example.com", p.baseURL)
	}
	if p.httpClient != client {
		t.Fatal("httpClient option was not applied")
	}
	if p.maxTokensField != "max_completion_tokens" {
		t.Fatalf("maxTokensField = %q, want max_completion_tokens", p.maxTokensField)
	}
	if p.inner == nil {
		t.Fatal("expected inner OpenAI provider to be initialized")
	}
	if p.Name() != "groq" {
		t.Fatalf("name = %q, want groq", p.Name())
	}

	caps := p.Capabilities()
	if !caps.Tools || !caps.ToolStreaming {
		t.Fatalf("capabilities = %#v, want tool support", caps)
	}
}

func TestCreateMessage_UsesGroqPrefixAndMaxTokensField(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"chatcmpl_1",
			"model":"moonshot/model-x",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer server.Close()

	p := New("test-key", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	resp, err := p.CreateMessage(t.Context(), &types.MessageRequest{
		Model: "moonshot/model-x",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
		MaxTokens: 42,
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if resp.Model != "groq/moonshot/model-x" {
		t.Fatalf("model = %q, want groq/moonshot/model-x", resp.Model)
	}
	if _, exists := gotBody["max_tokens"]; !exists {
		t.Fatalf("request missing max_tokens field: %#v", gotBody)
	}
	if _, exists := gotBody["max_completion_tokens"]; exists {
		t.Fatalf("request unexpectedly included max_completion_tokens: %#v", gotBody)
	}
}

func TestStreamMessage_UsesGroqPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl_1\",\"model\":\"moonshot/model-x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := New("test-key", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	stream, err := p.StreamMessage(t.Context(), &types.MessageRequest{
		Model: "moonshot/model-x",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	start, ok := event.(types.MessageStartEvent)
	if !ok {
		t.Fatalf("event type = %T, want MessageStartEvent", event)
	}
	if start.Message.Model != "groq/moonshot/model-x" {
		t.Fatalf("model = %q, want groq/moonshot/model-x", start.Message.Model)
	}
}

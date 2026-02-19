package cerebras

import (
	"io"
	"net/http"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

type fakeCerebrasInnerStream struct {
	events []types.StreamEvent
	index  int
	closed bool
}

func (f *fakeCerebrasInnerStream) Next() (types.StreamEvent, error) {
	if f.index >= len(f.events) {
		return nil, io.EOF
	}
	ev := f.events[f.index]
	f.index++
	return ev, nil
}

func (f *fakeCerebrasInnerStream) Close() error {
	f.closed = true
	return nil
}

func TestNew_AppliesOptionsAndCapabilities(t *testing.T) {
	client := &http.Client{}
	p := New("test-key", WithBaseURL("https://example.com"), WithHTTPClient(client))

	if p.baseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, want https://example.com", p.baseURL)
	}
	if p.httpClient != client {
		t.Fatal("httpClient option was not applied")
	}
	if p.inner == nil {
		t.Fatal("expected inner OpenAI provider to be initialized")
	}
	if p.Name() != "cerebras" {
		t.Fatalf("name = %q, want cerebras", p.Name())
	}

	caps := p.Capabilities()
	if !caps.Tools || !caps.ToolStreaming {
		t.Fatalf("capabilities = %#v, want tool support", caps)
	}
}

func TestCerebrasEventStream_RewritesMessageStartModel(t *testing.T) {
	inner := &fakeCerebrasInnerStream{
		events: []types.StreamEvent{
			types.MessageStartEvent{
				Type: "message_start",
				Message: types.MessageResponse{
					Model: "openai/llama-3.1-8b",
				},
			},
			types.MessageStopEvent{Type: "message_stop"},
		},
	}

	stream := &cerebrasEventStream{inner: inner}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	start, ok := first.(types.MessageStartEvent)
	if !ok {
		t.Fatalf("first event type = %T, want MessageStartEvent", first)
	}
	if start.Message.Model != "cerebras/llama-3.1-8b" {
		t.Fatalf("rewritten model = %q, want cerebras/llama-3.1-8b", start.Message.Model)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if _, ok := second.(types.MessageStopEvent); !ok {
		t.Fatalf("second event type = %T, want MessageStopEvent", second)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("third Next() error = %v, want io.EOF", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !inner.closed {
		t.Fatal("expected inner stream to be closed")
	}
}

func TestStripOpenAIPrefix(t *testing.T) {
	if got := stripOpenAIPrefix("openai/gpt-oss"); got != "gpt-oss" {
		t.Fatalf("stripOpenAIPrefix(openai/gpt-oss) = %q, want gpt-oss", got)
	}
	if got := stripOpenAIPrefix("cerebras/model"); got != "cerebras/model" {
		t.Fatalf("stripOpenAIPrefix(cerebras/model) = %q, want cerebras/model", got)
	}
}

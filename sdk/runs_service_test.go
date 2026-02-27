package vai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestRunsCreate_DecodesEnvelopeAndHeaders(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotVersion string
	var gotProviderKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("X-VAI-Version")
		gotProviderKey = r.Header.Get("X-Provider-Key-Anthropic")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.RunResultEnvelope{
			Result: &types.RunResult{
				Steps:         []types.RunStep{},
				StopReason:    types.RunStopReasonEndTurn,
				ToolCallCount: 0,
				TurnCount:     1,
			},
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL+"/gw/"),
		WithProviderKey("anthropic", "sk-ant"),
		WithHTTPClient(server.Client()),
	)

	resp, err := client.Runs.Create(context.Background(), &types.RunRequest{
		Request: types.MessageRequest{
			Model: "anthropic/claude-sonnet-4",
			Messages: []types.Message{
				{Role: "user", Content: "hello"},
			},
			MaxTokens: 128,
		},
		Run: types.RunConfig{MaxTurns: 4, ParallelTools: true},
	})
	if err != nil {
		t.Fatalf("Runs.Create() error = %v", err)
	}
	if resp == nil || resp.Result == nil {
		t.Fatalf("expected run result envelope")
	}
	if resp.Result.StopReason != types.RunStopReasonEndTurn {
		t.Fatalf("stop_reason = %q, want %q", resp.Result.StopReason, types.RunStopReasonEndTurn)
	}
	if gotPath != "/gw/v1/runs" {
		t.Fatalf("path = %q, want %q", gotPath, "/gw/v1/runs")
	}
	if gotVersion != "1" {
		t.Fatalf("X-VAI-Version = %q, want %q", gotVersion, "1")
	}
	if gotProviderKey != "sk-ant" {
		t.Fatalf("X-Provider-Key-Anthropic = %q, want configured key", gotProviderKey)
	}
}

func TestRunsCreate_RejectsClientFunctionTools(t *testing.T) {
	t.Parallel()

	var hitServer bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitServer = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	_, err := client.Runs.Create(context.Background(), &types.RunRequest{
		Request: types.MessageRequest{
			Model: "openai/gpt-4o-mini",
			Messages: []types.Message{
				{Role: "user", Content: "hello"},
			},
			Tools: []types.Tool{
				{
					Type: types.ToolTypeFunction,
					Name: "local_tool",
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if hitServer {
		t.Fatalf("request should fail before hitting server")
	}

	var apiErr *core.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *core.Error", err)
	}
	if apiErr.Type != core.ErrInvalidRequest {
		t.Fatalf("error type = %q, want %q", apiErr.Type, core.ErrInvalidRequest)
	}
}

func TestRunsCreate_AttachesServerToolProviderHeaderForExplicitProvider(t *testing.T) {
	t.Parallel()

	var gotTavily string
	var gotExa string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTavily = r.Header.Get("X-Provider-Key-Tavily")
		gotExa = r.Header.Get("X-Provider-Key-Exa")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.RunResultEnvelope{
			Result: &types.RunResult{
				Steps:      []types.RunStep{},
				StopReason: types.RunStopReasonEndTurn,
			},
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("anthropic", "sk-ant"),
		WithProviderKey("exa", "sk-exa"),
		WithProviderKey("tavily", "sk-tvly"),
		WithHTTPClient(server.Client()),
	)

	_, err := client.Runs.Create(context.Background(), &types.RunRequest{
		Request: types.MessageRequest{
			Model:    "anthropic/claude-sonnet-4",
			Messages: []types.Message{{Role: "user", Content: "hello"}},
		},
		ServerTools: []string{"vai_web_search"},
		ServerToolConfig: map[string]any{
			"vai_web_search": map[string]any{"provider": "exa"},
		},
	})
	if err != nil {
		t.Fatalf("Runs.Create() error = %v", err)
	}
	if gotExa != "sk-exa" {
		t.Fatalf("X-Provider-Key-Exa=%q", gotExa)
	}
	if gotTavily != "" {
		t.Fatalf("X-Provider-Key-Tavily=%q, want empty", gotTavily)
	}
}

func TestRunsCreate_AttachesBothSearchHeadersWhenProviderOmitted(t *testing.T) {
	t.Parallel()

	var gotTavily string
	var gotExa string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTavily = r.Header.Get("X-Provider-Key-Tavily")
		gotExa = r.Header.Get("X-Provider-Key-Exa")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.RunResultEnvelope{
			Result: &types.RunResult{
				Steps:      []types.RunStep{},
				StopReason: types.RunStopReasonEndTurn,
			},
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("anthropic", "sk-ant"),
		WithProviderKey("exa", "sk-exa"),
		WithProviderKey("tavily", "sk-tvly"),
		WithHTTPClient(server.Client()),
	)

	_, err := client.Runs.Create(context.Background(), &types.RunRequest{
		Request: types.MessageRequest{
			Model:    "anthropic/claude-sonnet-4",
			Messages: []types.Message{{Role: "user", Content: "hello"}},
		},
		ServerTools: []string{"vai_web_search"},
	})
	if err != nil {
		t.Fatalf("Runs.Create() error = %v", err)
	}
	if gotExa != "sk-exa" {
		t.Fatalf("X-Provider-Key-Exa=%q", gotExa)
	}
	if gotTavily != "sk-tvly" {
		t.Fatalf("X-Provider-Key-Tavily=%q", gotTavily)
	}
}

func TestRunsStream_ParsesEventCatalogAndNestedStreamEvents(t *testing.T) {
	t.Parallel()

	var gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEJSON(t, w, "run_start", types.RunStartEvent{
			Type:      "run_start",
			RequestID: "req_123",
			Model:     "anthropic/claude-sonnet-4",
		})
		writeSSEJSON(t, w, "step_start", types.RunStepStartEvent{
			Type:  "step_start",
			Index: 0,
		})
		writeSSEJSON(t, w, "stream_event", map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": "hello",
				},
			},
		})
		writeSSEJSON(t, w, "tool_call_start", types.RunToolCallStartEvent{
			Type:  "tool_call_start",
			ID:    "call_1",
			Name:  "vai_web_search",
			Input: map[string]any{"query": "go release"},
		})
		writeSSEJSON(t, w, "tool_result", types.RunToolResultEvent{
			Type:    "tool_result",
			ID:      "call_1",
			Name:    "vai_web_search",
			Content: []types.ContentBlock{types.TextBlock{Type: "text", Text: "result"}},
		})
		writeSSEJSON(t, w, "history_delta", types.RunHistoryDeltaEvent{
			Type:        "history_delta",
			ExpectedLen: 2,
			Append:      []types.Message{{Role: "assistant", Content: "hello"}},
		})
		writeSSEJSON(t, w, "run_complete", types.RunCompleteEvent{
			Type: "run_complete",
			Result: &types.RunResult{
				Steps:      []types.RunStep{},
				StopReason: types.RunStopReasonEndTurn,
			},
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("anthropic", "sk-ant"),
		WithHTTPClient(server.Client()),
	)

	stream, err := client.Runs.Stream(context.Background(), &types.RunRequest{
		Request: types.MessageRequest{
			Model: "anthropic/claude-sonnet-4",
			Messages: []types.Message{
				{Role: "user", Content: "hello"},
			},
			MaxTokens: 256,
		},
		Run: types.RunConfig{MaxTurns: 4, ParallelTools: true},
	})
	if err != nil {
		t.Fatalf("Runs.Stream() error = %v", err)
	}
	defer stream.Close()

	var sawNestedText bool
	for event := range stream.Events() {
		if wrapper, ok := event.(types.RunStreamEventWrapper); ok {
			if delta, ok := wrapper.Event.(types.ContentBlockDeltaEvent); ok {
				if text, ok := delta.Delta.(types.TextDelta); ok && text.Text == "hello" {
					sawNestedText = true
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v, want nil", err)
	}
	if !sawNestedText {
		t.Fatalf("expected nested stream_event text delta")
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q, want %q", gotAccept, "text/event-stream")
	}
	if result := stream.Result(); result == nil || result.StopReason != types.RunStopReasonEndTurn {
		t.Fatalf("unexpected stream result: %#v", result)
	}
}

func TestRunsStream_TerminalErrorEvent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEJSON(t, w, "error", types.RunErrorEvent{
			Type: "error",
			Error: types.Error{
				Type:      "api_error",
				Message:   "run failed",
				RequestID: "req_err",
			},
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("openai", "sk-openai"),
		WithHTTPClient(server.Client()),
	)

	stream, err := client.Runs.Stream(context.Background(), &types.RunRequest{
		Request: types.MessageRequest{
			Model: "openai/gpt-4o-mini",
			Messages: []types.Message{
				{Role: "user", Content: "hello"},
			},
			MaxTokens: 64,
		},
	})
	if err != nil {
		t.Fatalf("Runs.Stream() setup error = %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	var apiErr *core.Error
	if !errors.As(stream.Err(), &apiErr) {
		t.Fatalf("stream.Err() type = %T, want *core.Error", stream.Err())
	}
	if apiErr.RequestID != "req_err" {
		t.Fatalf("request_id = %q, want %q", apiErr.RequestID, "req_err")
	}
}

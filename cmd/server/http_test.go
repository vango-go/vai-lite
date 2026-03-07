package main

import (
	"encoding/json"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestParseInt64Value(t *testing.T) {
	t.Parallel()

	got, err := parseInt64Value(" 2500 ")
	if err != nil {
		t.Fatalf("parseInt64Value() error = %v", err)
	}
	if got != 2500 {
		t.Fatalf("parseInt64Value() = %d, want 2500", got)
	}
}

func TestExtractRunResultFromSSE(t *testing.T) {
	t.Parallel()

	event := types.RunCompleteEvent{
		Type: "run_complete",
		Result: &types.RunResult{
			Response: &types.MessageResponse{
				ID:    "msg_123",
				Type:  "message",
				Role:  "assistant",
				Model: "oai-resp/gpt-5-mini",
				Content: []types.ContentBlock{
					types.TextBlock{Type: "text", Text: "hello from stream"},
				},
				Usage: types.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
			},
			Usage: types.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
		},
	}
	rawEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	raw := []byte("event: run_start\ndata: {\"type\":\"run_start\",\"model\":\"oai-resp/gpt-5-mini\"}\n\n" +
		"event: run_complete\ndata: " + string(rawEvent) + "\n\n")

	result, err := extractRunResult(raw)
	if err != nil {
		t.Fatalf("extractRunResult() error = %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("extractRunResult() returned nil result")
	}
	if got := result.Response.TextContent(); got != "hello from stream" {
		t.Fatalf("TextContent() = %q", got)
	}
	if got := result.Usage.TotalTokens; got != 125 {
		t.Fatalf("Usage.TotalTokens = %d", got)
	}
}

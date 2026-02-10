package vai

import (
	"context"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestRunStream_ExecutesToolWithReconstructedStreamInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := newScriptedProvider(
		"test",
		toolTurnEventsForRunStream(),
		finalTextTurnEventsForRunStream("done"),
	)

	svc := newMessagesServiceForRunStreamTest(provider)

	type multiplyInput struct {
		A int `json:"a"`
		B int `json:"b"`
	}

	var got multiplyInput
	var calls int

	multiply := MakeTool("multiply", "Multiply two numbers", func(ctx context.Context, in multiplyInput) (int, error) {
		calls++
		got = in
		return in.A * in.B, nil
	})

	stream, err := svc.RunStream(ctx, &MessageRequest{
		Model: "test/fake-model",
		Messages: []Message{
			{Role: "user", Content: Text("Multiply 6 by 7")},
		},
		MaxTokens: 1024,
	}, WithTools(multiply))
	if err != nil {
		t.Fatalf("RunStream() returned error: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v, want nil", err)
	}

	result := stream.Result()
	if result == nil {
		t.Fatal("stream.Result() returned nil")
	}
	if result.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", result.ToolCallCount)
	}
	if calls != 1 {
		t.Fatalf("tool handler calls = %d, want 1", calls)
	}
	if got.A != 6 || got.B != 7 {
		t.Fatalf("tool handler input = %+v, want {A:6 B:7}", got)
	}
}

func TestRunStream_ToolCallStartEvent_EmittedOnceWithFullInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := newScriptedProvider(
		"test",
		toolTurnEventsForRunStream(),
		finalTextTurnEventsForRunStream("done"),
	)

	svc := newMessagesServiceForRunStreamTest(provider)

	multiply := MakeTool("multiply", "Multiply two numbers", func(ctx context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (int, error) {
		return in.A * in.B, nil
	})

	stream, err := svc.RunStream(ctx, &MessageRequest{
		Model: "test/fake-model",
		Messages: []Message{
			{Role: "user", Content: Text("Multiply 6 by 7")},
		},
		MaxTokens: 1024,
	}, WithTools(multiply))
	if err != nil {
		t.Fatalf("RunStream() returned error: %v", err)
	}
	defer stream.Close()

	var starts []ToolCallStartEvent
	for ev := range stream.Events() {
		if e, ok := ev.(ToolCallStartEvent); ok {
			starts = append(starts, e)
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v, want nil", err)
	}

	if len(starts) != 1 {
		t.Fatalf("len(ToolCallStartEvent) = %d, want 1", len(starts))
	}
	if starts[0].Name != "multiply" {
		t.Fatalf("ToolCallStartEvent.Name = %q, want %q", starts[0].Name, "multiply")
	}

	input := starts[0].Input
	if len(input) == 0 {
		t.Fatalf("expected populated ToolCallStartEvent input, got %#v", input)
	}
	if got, ok := input["a"].(float64); !ok || got != 6 {
		t.Fatalf("input[a] = %v (%T), want 6", input["a"], input["a"])
	}
	if got, ok := input["b"].(float64); !ok || got != 7 {
		t.Fatalf("input[b] = %v (%T), want 7", input["b"], input["b"])
	}
}

func newMessagesServiceForRunStreamTest(provider core.Provider) *MessagesService {
	engine := core.NewEngine(nil)
	engine.RegisterProvider(provider)
	client := &Client{core: engine}
	return &MessagesService{client: client}
}

func toolTurnEventsForRunStream() []types.StreamEvent {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonToolUse

	return []types.StreamEvent{
		types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				Role:  "assistant",
				Model: "fake-model",
			},
		},
		types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: types.ToolUseBlock{
				Type:  "tool_use",
				ID:    "call_1",
				Name:  "multiply",
				Input: map[string]any{},
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.InputJSONDelta{
				Type:        "input_json_delta",
				PartialJSON: `{"a":6,`,
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.InputJSONDelta{
				Type:        "input_json_delta",
				PartialJSON: `"b":7}`,
			},
		},
		types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		},
		delta,
		types.MessageStopEvent{Type: "message_stop"},
	}
}

func finalTextTurnEventsForRunStream(text string) []types.StreamEvent {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonEndTurn

	return []types.StreamEvent{
		types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				Role:  "assistant",
				Model: "fake-model",
			},
		},
		types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: types.TextBlock{
				Type: "text",
				Text: "",
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.TextDelta{
				Type: "text_delta",
				Text: text,
			},
		},
		types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		},
		delta,
		types.MessageStopEvent{Type: "message_stop"},
	}
}

package vai

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestStream_Response_ReconstructsToolInputFromInputJSONDelta(t *testing.T) {
	stream := newStreamFromEventStream(&scriptedEventStream{
		events: toolUseEventsForStreamTest([]string{`{"a":6,`, `"b":7}`}, true),
	})

	for range stream.Events() {
	}

	resp := stream.Response()
	if resp == nil {
		t.Fatal("expected response")
	}
	uses := resp.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("len(tool uses) = %d, want 1", len(uses))
	}

	input := uses[0].Input
	if got, ok := input["a"].(float64); !ok || got != 6 {
		t.Fatalf("input[a] = %v (%T), want 6", input["a"], input["a"])
	}
	if got, ok := input["b"].(float64); !ok || got != 7 {
		t.Fatalf("input[b] = %v (%T), want 7", input["b"], input["b"])
	}
}

func TestStream_Response_ReconstructsToolInputWithoutContentBlockStop(t *testing.T) {
	stream := newStreamFromEventStream(&scriptedEventStream{
		events: toolUseEventsForStreamTest([]string{`{"a":6,`, `"b":7}`}, false),
	})

	for range stream.Events() {
	}

	resp := stream.Response()
	if resp == nil {
		t.Fatal("expected response")
	}
	uses := resp.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("len(tool uses) = %d, want 1", len(uses))
	}

	input := uses[0].Input
	if got, ok := input["a"].(float64); !ok || got != 6 {
		t.Fatalf("input[a] = %v (%T), want 6", input["a"], input["a"])
	}
	if got, ok := input["b"].(float64); !ok || got != 7 {
		t.Fatalf("input[b] = %v (%T), want 7", input["b"], input["b"])
	}
}

func TestStream_Response_InvalidInputJSONDelta_DoesNotFail(t *testing.T) {
	stream := newStreamFromEventStream(&scriptedEventStream{
		events: toolUseEventsForStreamTest([]string{`{"a":6`}, true),
	})

	for range stream.Events() {
	}

	resp := stream.Response()
	if resp == nil {
		t.Fatal("expected response")
	}
	uses := resp.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("len(tool uses) = %d, want 1", len(uses))
	}
	if len(uses[0].Input) != 0 {
		t.Fatalf("expected empty input after invalid JSON, got %#v", uses[0].Input)
	}
}

func toolUseEventsForStreamTest(inputParts []string, includeBlockStop bool) []types.StreamEvent {
	events := []types.StreamEvent{
		types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				Role:  "assistant",
				Model: "test-model",
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
	}

	for _, part := range inputParts {
		events = append(events, types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.InputJSONDelta{
				Type:        "input_json_delta",
				PartialJSON: part,
			},
		})
	}

	if includeBlockStop {
		events = append(events, types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		})
	}

	delta := types.MessageDeltaEvent{
		Type: "message_delta",
	}
	delta.Delta.StopReason = types.StopReasonToolUse

	events = append(events,
		delta,
		types.MessageStopEvent{Type: "message_stop"},
	)

	return events
}

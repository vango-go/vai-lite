package gemini

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestEventStream_EmitsTerminalMessageDeltaBeforeEOF(t *testing.T) {
	stream := newEventStream(io.NopCloser(strings.NewReader("")), "gemini-2.0-flash")
	stream.accumulator.finishReason = "STOP"
	stream.accumulator.inputTokens = 9
	stream.accumulator.outputTokens = 4

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}

	delta, ok := event.(types.MessageDeltaEvent)
	if !ok {
		t.Fatalf("event type = %T, want MessageDeltaEvent", event)
	}
	if delta.Delta.StopReason != types.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", delta.Delta.StopReason, types.StopReasonEndTurn)
	}
	if delta.Usage.InputTokens != 9 || delta.Usage.OutputTokens != 4 || delta.Usage.TotalTokens != 13 {
		t.Fatalf("usage = %+v, want input=9 output=4 total=13", delta.Usage)
	}

	event, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
	if event != nil {
		t.Fatalf("second Next() event = %T, want nil", event)
	}
}

func TestEventStream_EmitsThoughtSignatureDeltaWhenFunctionArgsOmitted(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "do_something",
							},
							"thoughtSignature": "sig-1",
						},
					},
				},
				"finishReason": "FUNCTION_CALL",
			},
		},
	}
	data, _ := json.Marshal(chunk)

	stream := newEventStream(io.NopCloser(strings.NewReader("data: "+string(data)+"\n\n")), "gemini-3-flash-preview")

	if event, err := stream.Next(); err != nil {
		t.Fatalf("first Next() error = %v, want nil", err)
	} else if _, ok := event.(types.MessageStartEvent); !ok {
		t.Fatalf("first event type = %T, want MessageStartEvent", event)
	}

	if event, err := stream.Next(); err != nil {
		t.Fatalf("second Next() error = %v, want nil", err)
	} else if _, ok := event.(types.ContentBlockStartEvent); !ok {
		t.Fatalf("second event type = %T, want ContentBlockStartEvent", event)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("third Next() error = %v, want nil", err)
	}
	deltaEvent, ok := event.(types.ContentBlockDeltaEvent)
	if !ok {
		t.Fatalf("third event type = %T, want ContentBlockDeltaEvent", event)
	}
	inputDelta, ok := deltaEvent.Delta.(types.InputJSONDelta)
	if !ok {
		t.Fatalf("delta type = %T, want InputJSONDelta", deltaEvent.Delta)
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(inputDelta.PartialJSON), &input); err != nil {
		t.Fatalf("failed to parse input delta JSON %q: %v", inputDelta.PartialJSON, err)
	}
	if got, ok := input["__thought_signature"].(string); !ok || got != "sig-1" {
		t.Fatalf("input thought signature = %v, want %q", input["__thought_signature"], "sig-1")
	}
	if len(input) != 1 {
		t.Fatalf("input map = %#v, want only thought signature", input)
	}
}

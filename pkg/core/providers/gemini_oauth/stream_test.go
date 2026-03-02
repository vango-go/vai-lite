package gemini_oauth

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestEventStream_EmitsTerminalMessageDeltaBeforeEOF(t *testing.T) {
	stream := newEventStream(io.NopCloser(strings.NewReader("")), "gemini-2.0-flash")
	stream.accumulator.finishReason = "TOOL_USE"
	stream.accumulator.inputTokens = 13
	stream.accumulator.outputTokens = 5

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}

	delta, ok := event.(types.MessageDeltaEvent)
	if !ok {
		t.Fatalf("event type = %T, want MessageDeltaEvent", event)
	}
	if delta.Delta.StopReason != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", delta.Delta.StopReason, types.StopReasonToolUse)
	}
	if delta.Usage.InputTokens != 13 || delta.Usage.OutputTokens != 5 || delta.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want input=13 output=5 total=18", delta.Usage)
	}

	event, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
	if event != nil {
		t.Fatalf("second Next() event = %T, want nil", event)
	}
}

func TestEventStream_EmitsThoughtSignatureDeltaWhenFunctionArgsOmitted_OnFirstChunk(t *testing.T) {
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

	stream := newEventStream(io.NopCloser(strings.NewReader("data: "+string(data)+"\n\n")), "gemini-3-pro-preview")

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
	assertThoughtSignatureDelta(t, event, "sig-1")
}

func TestEventStream_EmitsThoughtSignatureDeltaWhenFunctionArgsOmitted_OnLaterChunk(t *testing.T) {
	textChunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{"text": "hello"},
					},
				},
				"finishReason": "",
			},
		},
	}
	toolChunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "do_something",
							},
							"thoughtSignature": "sig-2",
						},
					},
				},
				"finishReason": "FUNCTION_CALL",
			},
		},
	}
	textData, _ := json.Marshal(textChunk)
	toolData, _ := json.Marshal(toolChunk)
	sse := "data: " + string(textData) + "\n\n" + "data: " + string(toolData) + "\n\n"

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "gemini-3-pro-preview")

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
	if event, err := stream.Next(); err != nil {
		t.Fatalf("third Next() error = %v, want nil", err)
	} else if _, ok := event.(types.ContentBlockDeltaEvent); !ok {
		t.Fatalf("third event type = %T, want ContentBlockDeltaEvent", event)
	}
	if event, err := stream.Next(); err != nil {
		t.Fatalf("fourth Next() error = %v, want nil", err)
	} else if _, ok := event.(types.ContentBlockStartEvent); !ok {
		t.Fatalf("fourth event type = %T, want ContentBlockStartEvent", event)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("fifth Next() error = %v, want nil", err)
	}
	assertThoughtSignatureDelta(t, event, "sig-2")
}

func assertThoughtSignatureDelta(t *testing.T, event types.StreamEvent, wantSig string) {
	t.Helper()

	deltaEvent, ok := event.(types.ContentBlockDeltaEvent)
	if !ok {
		t.Fatalf("event type = %T, want ContentBlockDeltaEvent", event)
	}
	inputDelta, ok := deltaEvent.Delta.(types.InputJSONDelta)
	if !ok {
		t.Fatalf("delta type = %T, want InputJSONDelta", deltaEvent.Delta)
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(inputDelta.PartialJSON), &input); err != nil {
		t.Fatalf("failed to parse input delta JSON %q: %v", inputDelta.PartialJSON, err)
	}
	if got, ok := input["__thought_signature"].(string); !ok || got != wantSig {
		t.Fatalf("input thought signature = %v, want %q", input["__thought_signature"], wantSig)
	}
	if len(input) != 1 {
		t.Fatalf("input map = %#v, want only thought signature", input)
	}
}

func TestEventStream_PartialArgsAcrossChunks_ReconstructsFullInput(t *testing.T) {
	chunk1 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "talk_to_user",
								"partialArgs": []any{
									map[string]any{"jsonPath": "$.message", "value": "Hel"},
								},
							},
						},
					},
				},
			},
		},
	}
	chunk2 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "talk_to_user",
								"partialArgs": []any{
									map[string]any{"jsonPath": "$.message", "value": "lo"},
								},
							},
						},
					},
				},
			},
		},
	}
	chunk3 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content":      map[string]any{"parts": []any{}},
				"finishReason": "FUNCTION_CALL",
			},
		},
	}

	sse := encodeOAuthSSEChunks(chunk1, chunk2, chunk3)
	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "gemini-2.5-pro")
	events := collectOAuthStreamEvents(t, stream)

	input := parseOAuthToolInputJSON(t, events, 0)
	if got, _ := input["message"].(string); got != "Hello" {
		t.Fatalf("message = %q, want %q", got, "Hello")
	}
	if countOAuthToolStarts(events, 0) != 1 {
		t.Fatalf("tool starts = %d, want 1", countOAuthToolStarts(events, 0))
	}
	if stop := finalOAuthStopReason(events); stop != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", stop, types.StopReasonToolUse)
	}
}

func TestEventStream_PartialArgsAndThoughtSignature_Preserved(t *testing.T) {
	chunk1 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "talk_to_user",
								"partialArgs": []any{
									map[string]any{"jsonPath": "$.message", "value": "Hi"},
								},
							},
							"thoughtSignature": "sig-1",
						},
					},
				},
			},
		},
	}
	chunk2 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content":      map[string]any{"parts": []any{}},
				"finishReason": "FUNCTION_CALL",
			},
		},
	}

	sse := encodeOAuthSSEChunks(chunk1, chunk2)
	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "gemini-2.5-pro")
	events := collectOAuthStreamEvents(t, stream)
	input := parseOAuthToolInputJSON(t, events, 0)

	if got, _ := input["message"].(string); got != "Hi" {
		t.Fatalf("message = %q, want %q", got, "Hi")
	}
	if got, _ := input["__thought_signature"].(string); got != "sig-1" {
		t.Fatalf("thought signature = %q, want %q", got, "sig-1")
	}
}

func TestEventStream_PartialThenFinalArgs_UsesAuthoritativeFinalMap(t *testing.T) {
	chunk1 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "talk_to_user",
								"partialArgs": []any{
									map[string]any{"jsonPath": "$.message", "value": "Hel"},
								},
							},
						},
					},
				},
			},
		},
	}
	chunk2 := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "talk_to_user",
								"args": map[string]any{
									"message": "Hello",
									"tone":    "warm",
								},
							},
						},
					},
				},
				"finishReason": "FUNCTION_CALL",
			},
		},
	}

	sse := encodeOAuthSSEChunks(chunk1, chunk2)
	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "gemini-2.5-pro")
	events := collectOAuthStreamEvents(t, stream)
	input := parseOAuthToolInputJSON(t, events, 0)

	if got, _ := input["message"].(string); got != "Hello" {
		t.Fatalf("message = %q, want %q", got, "Hello")
	}
	if got, _ := input["tone"].(string); got != "warm" {
		t.Fatalf("tone = %q, want %q", got, "warm")
	}
}

func TestEventStream_NonRootPartialArgs_BufferedUntilCompletion(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "talk_to_user",
								"partialArgs": []any{
									map[string]any{"jsonPath": "$.config.level", "value": "high"},
								},
							},
						},
					},
				},
				"finishReason": "FUNCTION_CALL",
			},
		},
	}

	sse := encodeOAuthSSEChunks(chunk)
	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "gemini-2.5-pro")
	events := collectOAuthStreamEvents(t, stream)
	input := parseOAuthToolInputJSON(t, events, 0)

	config, ok := input["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want map", input["config"])
	}
	if got, _ := config["level"].(string); got != "high" {
		t.Fatalf("config.level = %q, want %q", got, "high")
	}
}

func TestEventStream_ParsesFinalChunkWithoutTrailingNewline(t *testing.T) {
	finalChunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"content":      map[string]any{"parts": []any{}},
				"finishReason": "FUNCTION_CALL",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     6,
			"candidatesTokenCount": 2,
		},
	}
	data, _ := json.Marshal(finalChunk)
	stream := newEventStream(io.NopCloser(strings.NewReader("data: "+string(data))), "gemini-2.5-pro")
	events := collectOAuthStreamEvents(t, stream)

	stop := finalOAuthStopReason(events)
	if stop != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", stop, types.StopReasonToolUse)
	}

	var final types.MessageDeltaEvent
	var found bool
	for _, ev := range events {
		if delta, ok := ev.(types.MessageDeltaEvent); ok {
			final = delta
			found = true
		}
	}
	if !found {
		t.Fatal("missing message_delta")
	}
	if final.Usage.InputTokens != 6 || final.Usage.OutputTokens != 2 || final.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v, want input=6 output=2 total=8", final.Usage)
	}
}

func TestEventStream_ParsesMultilineDataFrame(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]`,
		`data: }}]}`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "gemini-2.5-pro")
	events := collectOAuthStreamEvents(t, stream)

	var sawText bool
	for _, ev := range events {
		delta, ok := ev.(types.ContentBlockDeltaEvent)
		if !ok {
			continue
		}
		text, ok := delta.Delta.(types.TextDelta)
		if !ok {
			continue
		}
		if text.Text == "hello" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatal("expected text delta from multiline frame")
	}
}

func collectOAuthStreamEvents(t *testing.T, stream *eventStream) []types.StreamEvent {
	t.Helper()
	var events []types.StreamEvent
	for {
		ev, err := stream.Next()
		if ev != nil {
			events = append(events, ev)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream.Next() error = %v", err)
		}
	}
	return events
}

func encodeOAuthSSEChunks(chunks ...map[string]any) string {
	var b strings.Builder
	for _, chunk := range chunks {
		raw, _ := json.Marshal(chunk)
		b.WriteString("data: ")
		b.Write(raw)
		b.WriteString("\n\n")
	}
	return b.String()
}

func parseOAuthToolInputJSON(t *testing.T, events []types.StreamEvent, index int) map[string]any {
	t.Helper()
	var fragments strings.Builder
	for _, event := range events {
		delta, ok := event.(types.ContentBlockDeltaEvent)
		if !ok || delta.Index != index {
			continue
		}
		inputDelta, ok := delta.Delta.(types.InputJSONDelta)
		if !ok {
			continue
		}
		fragments.WriteString(inputDelta.PartialJSON)
	}

	if fragments.Len() == 0 {
		t.Fatalf("no input_json_delta fragments for index %d", index)
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(fragments.String()), &input); err != nil {
		t.Fatalf("failed to parse tool input JSON %q: %v", fragments.String(), err)
	}
	return input
}

func countOAuthToolStarts(events []types.StreamEvent, index int) int {
	count := 0
	for _, event := range events {
		start, ok := event.(types.ContentBlockStartEvent)
		if !ok || start.Index != index {
			continue
		}
		if _, ok := start.ContentBlock.(types.ToolUseBlock); ok {
			count++
		}
	}
	return count
}

func finalOAuthStopReason(events []types.StreamEvent) types.StopReason {
	for i := len(events) - 1; i >= 0; i-- {
		if delta, ok := events[i].(types.MessageDeltaEvent); ok {
			return delta.Delta.StopReason
		}
	}
	return ""
}

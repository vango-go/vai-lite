package oai_resp

import (
	"io"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestEventStream_EmitsTerminalEventsBeforeEOF(t *testing.T) {
	stream := newEventStream(io.NopCloser(strings.NewReader("")))
	stream.accumulator.finishReason = "tool_calls"
	stream.accumulator.inputTokens = 6
	stream.accumulator.outputTokens = 2

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want nil", err)
	}
	delta, ok := event.(types.MessageDeltaEvent)
	if !ok {
		t.Fatalf("first event type = %T, want MessageDeltaEvent", event)
	}
	if delta.Delta.StopReason != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", delta.Delta.StopReason, types.StopReasonToolUse)
	}
	if delta.Usage.InputTokens != 6 || delta.Usage.OutputTokens != 2 || delta.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v, want input=6 output=2 total=8", delta.Usage)
	}

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v, want nil", err)
	}
	if _, ok := event.(types.MessageStopEvent); !ok {
		t.Fatalf("second event type = %T, want MessageStopEvent", event)
	}

	event, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("third Next() error = %v, want io.EOF", err)
	}
	if event != nil {
		t.Fatalf("third Next() event = %T, want nil", event)
	}
}

func TestEventStream_FunctionCallArgumentsDoneAppendsTailAndDefersStop(t *testing.T) {
	// Simulates a Responses stream where output_item.done arrives before function_call_arguments.done,
	// and the final full tool arguments are delivered via the `.done` event.
	//
	// This ordering must not truncate tool args for live talk_to_user streaming.
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`,
		``,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"tool_1","name":"talk_to_user"}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"text\":\"Hello"}`,
		``,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call"}}`,
		``,
		`data: {"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"text\":\"Hello\\nWorld\"}"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-test","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)))

	var got []types.StreamEvent
	for {
		ev, err := stream.Next()
		if ev != nil {
			got = append(got, ev)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next err=%v", err)
		}
	}

	// Expect: message_start, content_block_start (tool_use), content_block_delta (args delta),
	// content_block_delta (tail from args.done), content_block_stop, message_delta, message_stop.
	typesSeq := make([]string, 0, len(got))
	var seenArgs strings.Builder
	for _, ev := range got {
		switch e := ev.(type) {
		case types.MessageStartEvent:
			typesSeq = append(typesSeq, e.Type)
		case types.ContentBlockStartEvent:
			typesSeq = append(typesSeq, e.Type)
		case types.ContentBlockDeltaEvent:
			typesSeq = append(typesSeq, e.Type)
			if d, ok := e.Delta.(types.InputJSONDelta); ok {
				seenArgs.WriteString(d.PartialJSON)
			}
		case types.ContentBlockStopEvent:
			typesSeq = append(typesSeq, e.Type)
		case types.MessageDeltaEvent:
			typesSeq = append(typesSeq, e.Type)
		case types.MessageStopEvent:
			typesSeq = append(typesSeq, e.Type)
		default:
			typesSeq = append(typesSeq, ev.EventType())
		}
	}

	gotSeq := strings.Join(typesSeq, ",")
	if !strings.Contains(gotSeq, "content_block_stop") {
		t.Fatalf("missing content_block_stop in seq=%s", gotSeq)
	}
	if !strings.Contains(seenArgs.String(), "\\nWorld") {
		t.Fatalf("args missing tail: %q", seenArgs.String())
	}
}

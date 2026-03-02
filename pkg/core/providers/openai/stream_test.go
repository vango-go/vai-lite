package openai

import (
	"io"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestEventStream_EmitsTerminalMessageDeltaBeforeEOF(t *testing.T) {
	stream := newEventStream(io.NopCloser(strings.NewReader("")), "openai")
	stream.accumulator.finishReason = "tool_calls"
	stream.accumulator.inputTokens = 11
	stream.accumulator.outputTokens = 7

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
	if delta.Usage.InputTokens != 11 || delta.Usage.OutputTokens != 7 || delta.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want input=11 output=7 total=18", delta.Usage)
	}

	event, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
	if event != nil {
		t.Fatalf("second Next() event = %T, want nil", event)
	}
}

func TestEventStream_UsesConfiguredModelPrefix(t *testing.T) {
	stream := newEventStream(io.NopCloser(strings.NewReader("data: {\"id\":\"chatcmpl_1\",\"model\":\"model-x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n")), "groq")

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}

	start, ok := event.(types.MessageStartEvent)
	if !ok {
		t.Fatalf("event type = %T, want MessageStartEvent", event)
	}
	if start.Message.Model != "groq/model-x" {
		t.Fatalf("model = %q, want groq/model-x", start.Message.Model)
	}
}

func TestEventStream_ToolStartAndArgumentsInSameChunk(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"talk_to_user","arguments":"{\"text\":\"Hel"}}]}}]}`,
		``,
		`data: {"id":"chatcmpl_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"lo\"}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "openai")
	events := collectStreamEvents(t, stream)

	var sawStart bool
	var parts []string
	for _, ev := range events {
		switch e := ev.(type) {
		case types.ContentBlockStartEvent:
			if block, ok := e.ContentBlock.(types.ToolUseBlock); ok {
				if e.Index != 0 {
					t.Fatalf("tool block index = %d, want 0", e.Index)
				}
				if block.ID != "call_1" || block.Name != "talk_to_user" {
					t.Fatalf("tool block = %#v, want call_1/talk_to_user", block)
				}
				sawStart = true
			}
		case types.ContentBlockDeltaEvent:
			if d, ok := e.Delta.(types.InputJSONDelta); ok {
				parts = append(parts, d.PartialJSON)
			}
		}
	}

	if !sawStart {
		t.Fatal("missing tool content_block_start event")
	}
	if len(parts) != 2 {
		t.Fatalf("input_json_delta parts = %d, want 2; parts=%v", len(parts), parts)
	}
	if parts[0] != `{"text":"Hel` {
		t.Fatalf("first args fragment = %q, want %q", parts[0], `{"text":"Hel`)
	}
	if strings.Join(parts, "") != `{"text":"Hello"}` {
		t.Fatalf("joined args = %q, want %q", strings.Join(parts, ""), `{"text":"Hello"}`)
	}
}

func TestEventStream_ProcessesAllToolCallsInSingleChunk(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl_2","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"tool_a","arguments":"{\"a\":1}"}},{"index":1,"id":"call_2","type":"function","function":{"name":"tool_b","arguments":"{\"b\":2}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "openai")
	events := collectStreamEvents(t, stream)

	starts := map[int]types.ToolUseBlock{}
	deltas := map[int]string{}
	for _, ev := range events {
		switch e := ev.(type) {
		case types.ContentBlockStartEvent:
			if block, ok := e.ContentBlock.(types.ToolUseBlock); ok {
				starts[e.Index] = block
			}
		case types.ContentBlockDeltaEvent:
			if d, ok := e.Delta.(types.InputJSONDelta); ok {
				deltas[e.Index] += d.PartialJSON
			}
		}
	}

	if len(starts) != 2 {
		t.Fatalf("tool starts = %d, want 2 (starts=%v)", len(starts), starts)
	}
	if len(deltas) != 2 {
		t.Fatalf("tool deltas = %d, want 2 (deltas=%v)", len(deltas), deltas)
	}
	if starts[0].Name != "tool_a" || starts[0].ID != "call_1" {
		t.Fatalf("start[0] = %#v, want tool_a/call_1", starts[0])
	}
	if starts[1].Name != "tool_b" || starts[1].ID != "call_2" {
		t.Fatalf("start[1] = %#v, want tool_b/call_2", starts[1])
	}
	if deltas[0] != `{"a":1}` {
		t.Fatalf("delta[0] = %q, want %q", deltas[0], `{"a":1}`)
	}
	if deltas[1] != `{"b":2}` {
		t.Fatalf("delta[1] = %q, want %q", deltas[1], `{"b":2}`)
	}
}

func TestEventStream_SameIndexMultiEntryInSingleChunk_StartOnceAndKeepAllArgs(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl_4","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"tool_a","arguments":"{\"a\":"}},{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "openai")
	events := collectStreamEvents(t, stream)

	var startCount int
	var toolStart types.ToolUseBlock
	var args strings.Builder
	for _, ev := range events {
		switch e := ev.(type) {
		case types.ContentBlockStartEvent:
			if block, ok := e.ContentBlock.(types.ToolUseBlock); ok {
				if e.Index != 0 {
					t.Fatalf("tool block index = %d, want 0", e.Index)
				}
				startCount++
				toolStart = block
			}
		case types.ContentBlockDeltaEvent:
			if d, ok := e.Delta.(types.InputJSONDelta); ok && e.Index == 0 {
				args.WriteString(d.PartialJSON)
			}
		}
	}

	if startCount != 1 {
		t.Fatalf("tool start count = %d, want 1", startCount)
	}
	if toolStart.ID != "call_1" || toolStart.Name != "tool_a" {
		t.Fatalf("tool start = %#v, want call_1/tool_a", toolStart)
	}
	if got := args.String(); got != `{"a":1}` {
		t.Fatalf("joined args = %q, want %q", got, `{"a":1}`)
	}
}

func TestEventStream_PreservesArgumentContinuityAcrossChunks(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl_3","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"talk_to_user","arguments":"{\"text\":\"Hel"}}]}}]}`,
		``,
		`data: {"id":"chatcmpl_3","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"lo\"}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "openai")
	events := collectStreamEvents(t, stream)

	var toolArgs strings.Builder
	var stopReason types.StopReason
	for _, ev := range events {
		switch e := ev.(type) {
		case types.ContentBlockDeltaEvent:
			if d, ok := e.Delta.(types.InputJSONDelta); ok {
				toolArgs.WriteString(d.PartialJSON)
			}
		case types.MessageDeltaEvent:
			stopReason = e.Delta.StopReason
		}
	}

	if got := toolArgs.String(); got != `{"text":"Hello"}` {
		t.Fatalf("tool args = %q, want %q", got, `{"text":"Hello"}`)
	}
	if stopReason != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", stopReason, types.StopReasonToolUse)
	}
}

func TestEventStream_ParsesFinalChunkWithoutTrailingNewline(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}`,
		``,
		`data: {"id":"chatcmpl_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "openai")
	events := collectStreamEvents(t, stream)

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
	if final.Delta.StopReason != types.StopReasonEndTurn {
		t.Fatalf("stop reason=%q, want %q", final.Delta.StopReason, types.StopReasonEndTurn)
	}
	if final.Usage.InputTokens != 2 || final.Usage.OutputTokens != 3 || final.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%+v, want input=2 output=3 total=5", final.Usage)
	}
}

func TestEventStream_ParsesMultilineDataFrame(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"gpt-4o-mini","choices":[{"index":0,`,
		`data: "delta":{"role":"assistant","content":"hello"}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)), "openai")
	events := collectStreamEvents(t, stream)

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

func collectStreamEvents(t *testing.T, stream *eventStream) []types.StreamEvent {
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
			t.Fatalf("Next() error = %v", err)
		}
	}
	return events
}

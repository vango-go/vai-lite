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

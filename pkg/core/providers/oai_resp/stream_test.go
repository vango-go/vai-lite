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

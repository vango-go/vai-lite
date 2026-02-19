package anthropic

import (
	"io"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestEventStream_DeliversMessageDeltaBeforeEOF(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
		"",
	}, "\n")

	stream := newEventStream(io.NopCloser(strings.NewReader(sse)))

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want nil", err)
	}
	delta, ok := event.(types.MessageDeltaEvent)
	if !ok {
		t.Fatalf("first event type = %T, want MessageDeltaEvent", event)
	}
	if delta.Delta.StopReason != types.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", delta.Delta.StopReason, types.StopReasonEndTurn)
	}
	if delta.Usage.InputTokens != 2 || delta.Usage.OutputTokens != 3 || delta.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, want input=2 output=3 total=5", delta.Usage)
	}

	event, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
	if event != nil {
		t.Fatalf("second Next() event = %T, want nil", event)
	}
}

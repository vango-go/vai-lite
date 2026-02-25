package runloop

import (
	"io"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestStreamAccumulator_ReconstructsToolInput(t *testing.T) {
	a := NewStreamAccumulator()
	a.Apply(types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{Type: "message", Role: "assistant", Model: "x"}})
	a.Apply(types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "call_1", Name: "fn", Input: map[string]any{}}})
	a.Apply(types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `{"a":1,`}})
	a.Apply(types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `"b":2}`}})
	a.Apply(types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0})
	d := types.MessageDeltaEvent{Type: "message_delta"}
	d.Delta.StopReason = types.StopReasonToolUse
	a.Apply(d)

	resp := a.Response()
	if resp == nil {
		t.Fatal("nil response")
	}
	uses := resp.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("tool uses=%d", len(uses))
	}
	if uses[0].Input["a"].(float64) != 1 {
		t.Fatalf("input=%v", uses[0].Input)
	}
}

type scriptedEventStream struct {
	events []types.StreamEvent
	i      int
}

func (s *scriptedEventStream) Next() (types.StreamEvent, error) {
	if s.i >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.i]
	s.i++
	if s.i == len(s.events) {
		return ev, io.EOF
	}
	return ev, nil
}

func (s *scriptedEventStream) Close() error { return nil }

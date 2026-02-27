package session

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
)

type fakeProvider struct {
	mu      sync.Mutex
	lastCtx context.Context
	lastReq *types.MessageRequest

	streamFn func(ctx context.Context, req *types.MessageRequest) core.EventStream
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, io.EOF
}

func (p *fakeProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	p.mu.Lock()
	p.lastCtx = ctx
	p.lastReq = req
	p.mu.Unlock()
	return p.streamFn(ctx, req), nil
}

func (p *fakeProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true, ToolStreaming: true}
}

type countingStream struct {
	events    []types.StreamEvent
	nextCalls int
	idx       int
	closed    bool
}

func (s *countingStream) Next() (types.StreamEvent, error) {
	s.nextCalls++
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *countingStream) Close() error {
	s.closed = true
	return nil
}

type ctxBlockingStream struct {
	ctx    context.Context
	closed bool
}

func (s *ctxBlockingStream) Next() (types.StreamEvent, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func (s *ctxBlockingStream) Close() error {
	s.closed = true
	return nil
}

func TestRunTurn_EarlyStopsOnTalkToUserContentBlockStop(t *testing.T) {
	var stream *countingStream
	p := &fakeProvider{
		streamFn: func(ctx context.Context, req *types.MessageRequest) core.EventStream {
			_ = ctx
			_ = req
			stream = &countingStream{events: []types.StreamEvent{
				types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant", Model: "test"}},
				types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "tool_1", Name: "talk_to_user"}},
				types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `{"text":"hel`}},
				types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `lo"}`}},
				types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
				// Trailing event that must not be consumed.
				types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 1, Delta: types.TextDelta{Type: "text_delta", Text: "should not read"}},
			}}
			return stream
		},
	}

	s := &LiveSession{
		provider:  p,
		modelName: "test",
	}

	text, err := s.runTurn(context.Background(), []types.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("runTurn error = %v", err)
	}
	if text != "hello" {
		t.Fatalf("text=%q, want %q", text, "hello")
	}
	if stream == nil {
		t.Fatalf("expected stream to be created")
	}
	if stream.nextCalls != 5 {
		t.Fatalf("Next calls=%d, want %d (stop after content_block_stop)", stream.nextCalls, 5)
	}
	if !stream.closed {
		t.Fatalf("expected stream to be closed")
	}
}

func TestNewTurnContext_UsesTimeoutWhenConfigured(t *testing.T) {
	s := &LiveSession{
		ctx: context.Background(),
		cfg: Config{TurnTimeout: 25 * time.Millisecond},
	}
	ctx, cancel := s.newTurnContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected ctx deadline")
	}
	until := time.Until(deadline)
	if until <= 0 || until > time.Second {
		t.Fatalf("deadline delta=%v, want within (0, 1s]", until)
	}
}

func TestRunTurn_PropagatesDeadlineExceeded(t *testing.T) {
	p := &fakeProvider{
		streamFn: func(ctx context.Context, req *types.MessageRequest) core.EventStream {
			_ = req
			return &ctxBlockingStream{ctx: ctx}
		},
	}
	s := &LiveSession{
		provider:  p,
		modelName: "test",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err := s.runTurn(ctx, []types.Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("err=%v, want %v", err, context.DeadlineExceeded)
	}
}

type fakeServerToolExecutor struct {
	name string
}

func (f fakeServerToolExecutor) Name() string { return f.name }
func (f fakeServerToolExecutor) Definition() types.Tool {
	return types.Tool{
		Type:        types.ToolTypeFunction,
		Name:        f.name,
		Description: "d",
		InputSchema: &types.JSONSchema{Type: "object"},
	}
}
func (f fakeServerToolExecutor) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	_ = ctx
	_ = input
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: "search result"}}, nil
}

func TestRunTurn_ServerToolThenTalkToUser(t *testing.T) {
	var callCount int
	p := &fakeProvider{
		streamFn: func(ctx context.Context, req *types.MessageRequest) core.EventStream {
			_ = ctx
			callCount++
			if callCount == 1 {
				msgDelta := types.MessageDeltaEvent{Type: "message_delta"}
				msgDelta.Delta.StopReason = types.StopReasonToolUse
				return &countingStream{events: []types.StreamEvent{
					types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant", Model: "test"}},
					types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "tool_search", Name: "vai_web_search"}},
					types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `{"query":"go 1.23"}`}},
					types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
					msgDelta,
					types.MessageStopEvent{Type: "message_stop"},
				}}
			}
			msgDelta := types.MessageDeltaEvent{Type: "message_delta"}
			msgDelta.Delta.StopReason = types.StopReasonToolUse
			return &countingStream{events: []types.StreamEvent{
				types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{ID: "msg_2", Type: "message", Role: "assistant", Model: "test"}},
				types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "tool_talk", Name: "talk_to_user"}},
				types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `{"text":"Go one point two three is available"}`}},
				types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
				msgDelta,
				types.MessageStopEvent{Type: "message_stop"},
			}}
		},
	}

	s := &LiveSession{
		provider:    p,
		modelName:   "test",
		serverTools: servertools.NewRegistry(fakeServerToolExecutor{name: "vai_web_search"}),
	}

	text, err := s.runTurn(context.Background(), []types.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("runTurn error = %v", err)
	}
	if text != "Go one point two three is available" {
		t.Fatalf("text=%q", text)
	}
	if callCount != 2 {
		t.Fatalf("callCount=%d, want 2", callCount)
	}
}

func TestRunTurn_MaxToolCallsExceeded(t *testing.T) {
	p := &fakeProvider{
		streamFn: func(ctx context.Context, req *types.MessageRequest) core.EventStream {
			_ = ctx
			_ = req
			msgDelta := types.MessageDeltaEvent{Type: "message_delta"}
			msgDelta.Delta.StopReason = types.StopReasonToolUse
			return &countingStream{events: []types.StreamEvent{
				types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant", Model: "test"}},
				types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.ToolUseBlock{Type: "tool_use", ID: "tool_search", Name: "vai_web_search"}},
				types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: `{"query":"loop"}`}},
				types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
				msgDelta,
				types.MessageStopEvent{Type: "message_stop"},
			}}
		},
	}

	s := &LiveSession{
		provider:    p,
		modelName:   "test",
		serverTools: servertools.NewRegistry(fakeServerToolExecutor{name: "vai_web_search"}),
	}

	_, err := s.runTurn(context.Background(), []types.Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "max tool calls exceeded" {
		t.Fatalf("err=%v", err)
	}
}

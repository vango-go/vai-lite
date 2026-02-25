package runloop

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/builtins"
)

type fakeExecutor struct {
	name string
}

func (f fakeExecutor) Name() string { return f.name }
func (f fakeExecutor) Definition() types.Tool {
	return types.Tool{Type: types.ToolTypeFunction, Name: f.name, Description: "d", InputSchema: &types.JSONSchema{Type: "object"}}
}
func (f fakeExecutor) Configured() bool { return true }
func (f fakeExecutor) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: "ok"}}, nil
}

type scriptedProvider struct {
	name string

	mu      sync.Mutex
	calls   int
	resps   []*types.MessageResponse
	streams [][]streamItem
}

type streamItem struct {
	event types.StreamEvent
	err   error
}

func (p *scriptedProvider) Name() string { return p.name }
func (p *scriptedProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true, ToolStreaming: true}
}

func (p *scriptedProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.resps) {
		return nil, io.EOF
	}
	resp := p.resps[p.calls]
	p.calls++
	return resp, nil
}

func (p *scriptedProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.streams) {
		return nil, io.EOF
	}
	items := p.streams[p.calls]
	p.calls++
	cpy := make([]streamItem, len(items))
	copy(cpy, items)
	return &scriptedEventStream2{items: cpy}, nil
}

type scriptedEventStream2 struct {
	items []streamItem
	i     int
}

func (s *scriptedEventStream2) Next() (types.StreamEvent, error) {
	if s.i >= len(s.items) {
		return nil, io.EOF
	}
	item := s.items[s.i]
	s.i++
	return item.event, item.err
}

func (s *scriptedEventStream2) Close() error { return nil }

func TestRunBlocking_NoToolCompletion(t *testing.T) {
	provider := &scriptedProvider{name: "test", resps: []*types.MessageResponse{{
		Type:       "message",
		Role:       "assistant",
		Model:      "m",
		StopReason: types.StopReasonEndTurn,
		Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "done"}},
	}}}

	controller := &Controller{Provider: provider, Builtins: builtins.NewRegistry(fakeExecutor{name: builtins.BuiltinWebSearch})}
	result, err := controller.RunBlocking(context.Background(), &types.RunRequest{Request: types.MessageRequest{Model: "m", Messages: []types.Message{{Role: "user", Content: "hi"}}, Tools: nil}, Run: types.RunConfig{MaxTurns: 8, MaxToolCalls: 20, TimeoutMS: 60000, ParallelTools: true, ToolTimeoutMS: 30000}})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.StopReason != types.RunStopReasonEndTurn {
		t.Fatalf("stop_reason=%q", result.StopReason)
	}
	if result.TurnCount != 1 {
		t.Fatalf("turn_count=%d", result.TurnCount)
	}
	if got := result.Response.TextContent(); got != "done" {
		t.Fatalf("response=%q", got)
	}
}

func TestRunBlocking_ToolLoop(t *testing.T) {
	provider := &scriptedProvider{name: "test", resps: []*types.MessageResponse{
		{
			Type:       "message",
			Role:       "assistant",
			Model:      "m",
			StopReason: types.StopReasonToolUse,
			Content: []types.ContentBlock{types.ToolUseBlock{
				Type:  "tool_use",
				ID:    "call_1",
				Name:  builtins.BuiltinWebSearch,
				Input: map[string]any{"query": "q"},
			}},
		},
		{
			Type:       "message",
			Role:       "assistant",
			Model:      "m",
			StopReason: types.StopReasonEndTurn,
			Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "final"}},
		},
	}}

	controller := &Controller{Provider: provider, Builtins: builtins.NewRegistry(fakeExecutor{name: builtins.BuiltinWebSearch})}
	result, err := controller.RunBlocking(context.Background(), &types.RunRequest{Request: types.MessageRequest{Model: "m", Messages: []types.Message{{Role: "user", Content: "hi"}}}, Run: types.RunConfig{MaxTurns: 8, MaxToolCalls: 20, TimeoutMS: 60000, ParallelTools: true, ToolTimeoutMS: 30000}})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.StopReason != types.RunStopReasonEndTurn {
		t.Fatalf("stop_reason=%q", result.StopReason)
	}
	if result.ToolCallCount != 1 {
		t.Fatalf("tool_call_count=%d", result.ToolCallCount)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps=%d", len(result.Steps))
	}
	if len(result.Messages) != 4 {
		b, _ := json.Marshal(result.Messages)
		t.Fatalf("messages=%d %s", len(result.Messages), string(b))
	}
}

func TestRunStream_OrderingAndEOFTerminal(t *testing.T) {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonEndTurn
	provider := &scriptedProvider{name: "test", streams: [][]streamItem{{
		{event: types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{Type: "message", Role: "assistant", Model: "m"}}},
		{event: types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.TextBlock{Type: "text", Text: ""}}},
		{event: types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.TextDelta{Type: "text_delta", Text: "done"}}},
		{event: delta, err: io.EOF},
	}}}

	controller := &Controller{Provider: provider, Builtins: builtins.NewRegistry(fakeExecutor{name: builtins.BuiltinWebSearch}), StreamIdleTimeout: time.Second, PublicModel: "anthropic/test", RequestID: "req_1"}
	events := make([]string, 0, 16)
	result, err := controller.RunStream(context.Background(), &types.RunRequest{Request: types.MessageRequest{Model: "m", Messages: []types.Message{{Role: "user", Content: "hi"}}}, Run: types.RunConfig{MaxTurns: 8, MaxToolCalls: 20, TimeoutMS: 60000, ParallelTools: true, ToolTimeoutMS: 30000}}, func(ev types.RunStreamEvent) error {
		events = append(events, ev.EventType())
		return nil
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "run_start") || !strings.Contains(joined, "step_start") || !strings.Contains(joined, "stream_event") || !strings.Contains(joined, "step_complete") || !strings.Contains(joined, "history_delta") || !strings.Contains(joined, "run_complete") {
		t.Fatalf("events=%s", joined)
	}
	if result.Response == nil || result.Response.TextContent() != "done" {
		t.Fatalf("result=%+v", result)
	}
}

type fakeTTSProvider struct{}

func (f *fakeTTSProvider) Name() string { return "fake" }
func (f *fakeTTSProvider) Synthesize(ctx context.Context, text string, opts tts.SynthesizeOptions) (*tts.Synthesis, error) {
	return &tts.Synthesis{Audio: []byte{1, 2, 3}, Format: "pcm"}, nil
}
func (f *fakeTTSProvider) SynthesizeStream(ctx context.Context, text string, opts tts.SynthesizeOptions) (*tts.SynthesisStream, error) {
	stream := tts.NewSynthesisStream()
	stream.Send([]byte{1, 2, 3})
	stream.FinishSending()
	return stream, nil
}
func (f *fakeTTSProvider) NewStreamingContext(ctx context.Context, opts tts.StreamingContextOptions) (*tts.StreamingContext, error) {
	sc := tts.NewStreamingContext()
	sc.SendFunc = func(text string, isFinal bool) error {
		if strings.TrimSpace(text) != "" {
			sc.PushAudio([]byte{1, 2, 3})
		}
		if isFinal {
			sc.FinishAudio()
		}
		return nil
	}
	sc.CloseFunc = func() error {
		sc.FinishAudio()
		return nil
	}
	return sc, nil
}

func TestRunBlocking_VoiceOutputAppended(t *testing.T) {
	provider := &scriptedProvider{name: "test", resps: []*types.MessageResponse{{
		Type:       "message",
		Role:       "assistant",
		Model:      "m",
		StopReason: types.StopReasonEndTurn,
		Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "hello"}},
	}}}

	pipeline := voice.NewPipelineWithProviders(nil, &fakeTTSProvider{})
	controller := &Controller{Provider: provider, Builtins: builtins.NewRegistry(fakeExecutor{name: builtins.BuiltinWebSearch}), VoicePipeline: pipeline}
	result, err := controller.RunBlocking(context.Background(), &types.RunRequest{Request: types.MessageRequest{Model: "m", Messages: []types.Message{{Role: "user", Content: "hi"}}, Voice: &types.VoiceConfig{Output: &types.VoiceOutputConfig{Voice: "v", Format: "pcm"}}}, Run: types.RunConfig{MaxTurns: 8, MaxToolCalls: 20, TimeoutMS: 60000, ParallelTools: true, ToolTimeoutMS: 30000}})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if audio := result.Response.AudioContent(); audio == nil {
		t.Fatalf("expected audio content in response: %+v", result.Response.Content)
	}
}

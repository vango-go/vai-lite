package vai

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestRunStream_ExecutesToolWithReconstructedStreamInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := newScriptedProvider(
		"test",
		toolTurnEventsForRunStream(),
		finalTextTurnEventsForRunStream("done"),
	)

	svc := newMessagesServiceForRunStreamTest(provider)

	type multiplyInput struct {
		A int `json:"a"`
		B int `json:"b"`
	}

	var got multiplyInput
	var calls int

	multiply := MakeTool("multiply", "Multiply two numbers", func(ctx context.Context, in multiplyInput) (int, error) {
		calls++
		got = in
		return in.A * in.B, nil
	})

	stream, err := svc.RunStream(ctx, &MessageRequest{
		Model: "test/fake-model",
		Messages: []Message{
			{Role: "user", Content: Text("Multiply 6 by 7")},
		},
		MaxTokens: 1024,
	}, WithTools(multiply))
	if err != nil {
		t.Fatalf("RunStream() returned error: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v, want nil", err)
	}

	result := stream.Result()
	if result == nil {
		t.Fatal("stream.Result() returned nil")
	}
	if result.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", result.ToolCallCount)
	}
	if calls != 1 {
		t.Fatalf("tool handler calls = %d, want 1", calls)
	}
	if got.A != 6 || got.B != 7 {
		t.Fatalf("tool handler input = %+v, want {A:6 B:7}", got)
	}
}

func TestRunStream_ToolCallStartEvent_EmittedOnceWithFullInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := newScriptedProvider(
		"test",
		toolTurnEventsForRunStream(),
		finalTextTurnEventsForRunStream("done"),
	)

	svc := newMessagesServiceForRunStreamTest(provider)

	multiply := MakeTool("multiply", "Multiply two numbers", func(ctx context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (int, error) {
		return in.A * in.B, nil
	})

	stream, err := svc.RunStream(ctx, &MessageRequest{
		Model: "test/fake-model",
		Messages: []Message{
			{Role: "user", Content: Text("Multiply 6 by 7")},
		},
		MaxTokens: 1024,
	}, WithTools(multiply))
	if err != nil {
		t.Fatalf("RunStream() returned error: %v", err)
	}
	defer stream.Close()

	var starts []ToolCallStartEvent
	for ev := range stream.Events() {
		if e, ok := ev.(ToolCallStartEvent); ok {
			starts = append(starts, e)
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v, want nil", err)
	}

	if len(starts) != 1 {
		t.Fatalf("len(ToolCallStartEvent) = %d, want 1", len(starts))
	}
	if starts[0].Name != "multiply" {
		t.Fatalf("ToolCallStartEvent.Name = %q, want %q", starts[0].Name, "multiply")
	}

	input := starts[0].Input
	if len(input) == 0 {
		t.Fatalf("expected populated ToolCallStartEvent input, got %#v", input)
	}
	if got, ok := input["a"].(float64); !ok || got != 6 {
		t.Fatalf("input[a] = %v (%T), want 6", input["a"], input["a"])
	}
	if got, ok := input["b"].(float64); !ok || got != 7 {
		t.Fatalf("input[b] = %v (%T), want 7", input["b"], input["b"])
	}
}

func TestRunStream_ContinuesWhenTerminalDeltaArrivesWithEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstTerminal := types.MessageDeltaEvent{Type: "message_delta"}
	firstTerminal.Delta.StopReason = types.StopReasonToolUse
	secondTerminal := types.MessageDeltaEvent{Type: "message_delta"}
	secondTerminal.Delta.StopReason = types.StopReasonEndTurn

	provider := &eofTerminalProvider{
		name: "test",
		streams: []eofTerminalStreamScript{
			{
				events: []types.StreamEvent{
					types.MessageStartEvent{
						Type: "message_start",
						Message: types.MessageResponse{
							Type:  "message",
							Role:  "assistant",
							Model: "fake-model",
						},
					},
					types.ContentBlockStartEvent{
						Type:  "content_block_start",
						Index: 0,
						ContentBlock: types.ToolUseBlock{
							Type:  "tool_use",
							ID:    "call_1",
							Name:  "multiply",
							Input: map[string]any{},
						},
					},
					types.ContentBlockDeltaEvent{
						Type:  "content_block_delta",
						Index: 0,
						Delta: types.InputJSONDelta{
							Type:        "input_json_delta",
							PartialJSON: `{"a":6,"b":7}`,
						},
					},
					types.ContentBlockStopEvent{
						Type:  "content_block_stop",
						Index: 0,
					},
				},
				terminal: firstTerminal,
			},
			{
				events: []types.StreamEvent{
					types.MessageStartEvent{
						Type: "message_start",
						Message: types.MessageResponse{
							Type:  "message",
							Role:  "assistant",
							Model: "fake-model",
						},
					},
					types.ContentBlockStartEvent{
						Type:  "content_block_start",
						Index: 0,
						ContentBlock: types.TextBlock{
							Type: "text",
							Text: "",
						},
					},
					types.ContentBlockDeltaEvent{
						Type:  "content_block_delta",
						Index: 0,
						Delta: types.TextDelta{
							Type: "text_delta",
							Text: "done",
						},
					},
					types.ContentBlockStopEvent{
						Type:  "content_block_stop",
						Index: 0,
					},
				},
				terminal: secondTerminal,
			},
		},
	}
	svc := newMessagesServiceForRunStreamTest(provider)

	multiply := MakeTool("multiply", "Multiply two numbers", func(ctx context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (int, error) {
		return in.A * in.B, nil
	})

	stream, err := svc.RunStream(ctx, &MessageRequest{
		Model: "test/fake-model",
		Messages: []Message{
			{Role: "user", Content: Text("Multiply 6 by 7")},
		},
		MaxTokens: 1024,
	}, WithTools(multiply))
	if err != nil {
		t.Fatalf("RunStream() returned error: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v, want nil", err)
	}

	result := stream.Result()
	if result == nil {
		t.Fatal("stream.Result() returned nil")
	}
	if result.StopReason != RunStopEndTurn {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, RunStopEndTurn)
	}
	if result.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", result.ToolCallCount)
	}
	if result.Response == nil {
		t.Fatalf("final response is nil")
	}
	if result.Response.TextContent() != "done" {
		t.Fatalf("final response text = %q, want %q", result.Response.TextContent(), "done")
	}
}

func newMessagesServiceForRunStreamTest(provider core.Provider) *MessagesService {
	engine := core.NewEngine(nil)
	engine.RegisterProvider(provider)
	client := &Client{core: engine}
	return &MessagesService{client: client}
}

func toolTurnEventsForRunStream() []types.StreamEvent {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonToolUse

	return []types.StreamEvent{
		types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				Role:  "assistant",
				Model: "fake-model",
			},
		},
		types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: types.ToolUseBlock{
				Type:  "tool_use",
				ID:    "call_1",
				Name:  "multiply",
				Input: map[string]any{},
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.InputJSONDelta{
				Type:        "input_json_delta",
				PartialJSON: `{"a":6,`,
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.InputJSONDelta{
				Type:        "input_json_delta",
				PartialJSON: `"b":7}`,
			},
		},
		types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		},
		delta,
		types.MessageStopEvent{Type: "message_stop"},
	}
}

func finalTextTurnEventsForRunStream(text string) []types.StreamEvent {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonEndTurn

	return []types.StreamEvent{
		types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				Role:  "assistant",
				Model: "fake-model",
			},
		},
		types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: types.TextBlock{
				Type: "text",
				Text: "",
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.TextDelta{
				Type: "text_delta",
				Text: text,
			},
		},
		types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		},
		delta,
		types.MessageStopEvent{Type: "message_stop"},
	}
}

type eofTerminalStreamScript struct {
	events   []types.StreamEvent
	terminal types.StreamEvent
}

type eofTerminalStream struct {
	events     []types.StreamEvent
	terminal   types.StreamEvent
	index      int
	emittedEOF bool
}

func (s *eofTerminalStream) Next() (types.StreamEvent, error) {
	if s.index < len(s.events) {
		ev := s.events[s.index]
		s.index++
		return ev, nil
	}
	if !s.emittedEOF {
		s.emittedEOF = true
		return s.terminal, io.EOF
	}
	return nil, io.EOF
}

func (s *eofTerminalStream) Close() error {
	return nil
}

type eofTerminalProvider struct {
	name string

	mu sync.Mutex

	streams     []eofTerminalStreamScript
	streamCalls int
}

func (p *eofTerminalProvider) Name() string { return p.name }

func (p *eofTerminalProvider) CreateMessage(context.Context, *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *eofTerminalProvider) StreamMessage(_ context.Context, _ *types.MessageRequest) (core.EventStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamCalls >= len(p.streams) {
		return nil, fmt.Errorf("no scripted stream for call %d", p.streamCalls)
	}
	script := p.streams[p.streamCalls]
	p.streamCalls++

	events := make([]types.StreamEvent, len(script.events))
	copy(events, script.events)
	return &eofTerminalStream{events: events, terminal: script.terminal}, nil
}

func (p *eofTerminalProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{
		Tools:         true,
		ToolStreaming: true,
	}
}

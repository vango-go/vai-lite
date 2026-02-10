package vai

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

type scriptedEventStream struct {
	events []types.StreamEvent
	index  int
	closed bool
}

func (s *scriptedEventStream) Next() (types.StreamEvent, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.index]
	s.index++
	return ev, nil
}

func (s *scriptedEventStream) Close() error {
	s.closed = true
	return nil
}

type scriptedProvider struct {
	name string

	mu      sync.Mutex
	streams [][]types.StreamEvent
	calls   int
}

func newScriptedProvider(name string, streams ...[]types.StreamEvent) *scriptedProvider {
	return &scriptedProvider{
		name:    name,
		streams: streams,
	}
}

func (p *scriptedProvider) Name() string {
	return p.name
}

func (p *scriptedProvider) CreateMessage(context.Context, *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, fmt.Errorf("CreateMessage not implemented in scriptedProvider")
}

func (p *scriptedProvider) StreamMessage(context.Context, *types.MessageRequest) (core.EventStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.calls >= len(p.streams) {
		return nil, fmt.Errorf("no scripted stream for call %d", p.calls)
	}
	events := p.streams[p.calls]
	p.calls++

	cloned := make([]types.StreamEvent, len(events))
	copy(cloned, events)
	return &scriptedEventStream{events: cloned}, nil
}

func (p *scriptedProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{
		Tools:         true,
		ToolStreaming: true,
	}
}

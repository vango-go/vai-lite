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

	mu sync.Mutex

	streams     [][]types.StreamEvent
	streamCalls int

	createResponses []*types.MessageResponse
	createCalls     int
	createErr       error

	createRequests []*types.MessageRequest
	streamRequests []*types.MessageRequest
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

func (p *scriptedProvider) CreateMessage(_ context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.createRequests = append(p.createRequests, cloneMessageRequest(req))

	if p.createErr != nil {
		return nil, p.createErr
	}
	if p.createCalls >= len(p.createResponses) {
		return nil, fmt.Errorf("no scripted create response for call %d", p.createCalls)
	}
	resp := cloneMessageResponse(p.createResponses[p.createCalls])
	p.createCalls++
	return resp, nil
}

func (p *scriptedProvider) StreamMessage(_ context.Context, req *types.MessageRequest) (core.EventStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.streamRequests = append(p.streamRequests, cloneMessageRequest(req))

	if p.streamCalls >= len(p.streams) {
		return nil, fmt.Errorf("no scripted stream for call %d", p.streamCalls)
	}
	events := p.streams[p.streamCalls]
	p.streamCalls++

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

func (p *scriptedProvider) withCreateResponses(responses ...*types.MessageResponse) *scriptedProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.createResponses = responses
	return p
}

func cloneMessageRequest(req *types.MessageRequest) *types.MessageRequest {
	if req == nil {
		return nil
	}
	copied := *req
	return &copied
}

func cloneMessageResponse(resp *types.MessageResponse) *types.MessageResponse {
	if resp == nil {
		return nil
	}
	copied := *resp
	if resp.Content != nil {
		content := make([]types.ContentBlock, len(resp.Content))
		copy(content, resp.Content)
		copied.Content = content
	}
	return &copied
}

package vai

import (
	"context"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// MessagesService executes tool-loop runs.
type MessagesService struct {
	client *Client
}

// MessageRequest is an alias for the core types.MessageRequest.
type MessageRequest = types.MessageRequest

// Message is an alias for the core types.Message.
type Message = types.Message

// Response is the SDK's response type wrapping the core response.
type Response struct {
	*types.MessageResponse
}

// Run executes a tool execution loop.
// It automatically handles tool calls until a stop condition is met.
func (s *MessagesService) Run(ctx context.Context, req *MessageRequest, opts ...RunOption) (*RunResult, error) {
	cfg := defaultRunConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return s.runLoop(ctx, req, &cfg)
}

// RunStream executes a streaming tool execution loop.
// It streams events as they occur and handles tool calls automatically.
func (s *MessagesService) RunStream(ctx context.Context, req *MessageRequest, opts ...RunOption) (*RunStream, error) {
	cfg := defaultRunConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return s.runStreamLoop(ctx, req, &cfg), nil
}

func (s *MessagesService) createTurn(ctx context.Context, req *types.MessageRequest) (*Response, error) {
	resp, err := s.client.core.CreateMessage(ctx, req)
	if err != nil {
		return nil, err
	}
	return &Response{MessageResponse: resp}, nil
}

func (s *MessagesService) streamTurn(ctx context.Context, req *types.MessageRequest) (*Stream, error) {
	reqCopy := *req
	reqCopy.Stream = true

	eventStream, err := s.client.core.StreamMessage(ctx, &reqCopy)
	if err != nil {
		return nil, err
	}
	return newStreamFromEventStream(eventStream), nil
}

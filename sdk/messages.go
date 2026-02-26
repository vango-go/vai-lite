package vai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice"
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

// Create sends a non-streaming single-turn message request.
func (s *MessagesService) Create(ctx context.Context, req *MessageRequest) (*Response, error) {
	if req == nil {
		return nil, fmt.Errorf("req must not be nil")
	}

	processedReq := req
	userTranscript := ""

	if !s.client.isProxyMode() {
		var err error
		processedReq, userTranscript, err = s.preprocessVoiceInput(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	resp, err := s.createTurn(ctx, processedReq)
	if err != nil {
		return nil, err
	}

	if userTranscript != "" {
		if resp.Metadata == nil {
			resp.Metadata = make(map[string]any)
		}
		resp.Metadata["user_transcript"] = userTranscript
	}

	if !s.client.isProxyMode() {
		if err := s.appendVoiceOutput(ctx, req, resp.MessageResponse); err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// Stream sends a streaming single-turn message request.
func (s *MessagesService) Stream(ctx context.Context, req *MessageRequest) (*Stream, error) {
	if req == nil {
		return nil, fmt.Errorf("req must not be nil")
	}

	processedReq := req
	userTranscript := ""

	if !s.client.isProxyMode() {
		var err error
		processedReq, userTranscript, err = s.preprocessVoiceInput(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	reqCopy := *processedReq
	reqCopy.Stream = true

	eventStream, err := s.client.core.StreamMessage(ctx, &reqCopy)
	if err != nil {
		return nil, err
	}

	opts := []streamOption{withStreamUserTranscript(userTranscript)}
	if s.client.isProxyMode() {
		if req.Voice != nil && req.Voice.Output != nil {
			opts = append(opts, withStreamGatewayAudioPassthrough())
		}
	} else if req.Voice != nil && req.Voice.Output != nil {
		if err := s.requireVoicePipeline(); err != nil {
			return nil, err
		}
		ttsCtx, err := s.client.voicePipeline.NewStreamingTTSContext(ctx, req.Voice)
		if err != nil {
			return nil, fmt.Errorf("initialize voice stream: %w", err)
		}
		opts = append(opts, withStreamVoiceOutput(ttsCtx, normalizeStreamingAudioFormat(req.Voice.Output.Format), req.Voice.Output.Voice))
	}

	return newStreamFromEventStream(eventStream, opts...), nil
}

// CreateStream is an alias for Stream.
func (s *MessagesService) CreateStream(ctx context.Context, req *MessageRequest) (*Stream, error) {
	return s.Stream(ctx, req)
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

// Extract executes a request and unmarshals structured output into dest.
// If req.OutputFormat is unset, it injects a JSON schema generated from dest's type.
func (s *MessagesService) Extract(ctx context.Context, req *MessageRequest, dest any) (*Response, error) {
	if req == nil {
		return nil, fmt.Errorf("req must not be nil")
	}

	destValue := reflect.ValueOf(dest)
	if !destValue.IsValid() || destValue.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("dest must be a non-nil pointer to a struct")
	}
	if destValue.IsNil() {
		return nil, fmt.Errorf("dest must be a non-nil pointer to a struct")
	}
	if destValue.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("dest must be a non-nil pointer to a struct")
	}

	reqCopy := *req
	if reqCopy.OutputFormat == nil {
		reqCopy.OutputFormat = &types.OutputFormat{
			Type:       "json_schema",
			JSONSchema: GenerateJSONSchema(destValue.Elem().Type()),
		}
	}

	resp, err := s.Create(ctx, &reqCopy)
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, fmt.Errorf("no response returned")
	}
	text := resp.TextContent()
	if text == "" {
		return nil, fmt.Errorf("no text content in response")
	}

	if err := unmarshalStructuredOutput(text, dest); err != nil {
		return nil, err
	}

	return resp, nil
}

// ExtractTyped is a package-level generic helper because Go does not support
// type parameters on methods. Usage:
//
//	value, resp, err := vai.ExtractTyped[MyType](ctx, client.Messages, req)
func ExtractTyped[T any](ctx context.Context, messages *MessagesService, req *MessageRequest) (T, *Response, error) {
	var zero T
	if messages == nil {
		return zero, nil, fmt.Errorf("messages service must not be nil")
	}

	var value T
	resp, err := messages.Extract(ctx, req, &value)
	if err != nil {
		return zero, nil, err
	}
	return value, resp, nil
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

func (s *MessagesService) preprocessVoiceInput(ctx context.Context, req *MessageRequest) (*MessageRequest, string, error) {
	if req == nil {
		return nil, "", fmt.Errorf("req must not be nil")
	}
	if req.Voice == nil || req.Voice.Input == nil {
		return req, "", nil
	}
	if err := s.requireVoicePipeline(); err != nil {
		return nil, "", err
	}

	processedReq, transcript, err := voice.PreprocessMessageRequestInputAudio(ctx, s.client.voicePipeline, req)
	if err != nil {
		return nil, "", fmt.Errorf("transcribe input audio: %w", err)
	}
	return processedReq, transcript, nil
}

func (s *MessagesService) appendVoiceOutput(ctx context.Context, req *MessageRequest, resp *types.MessageResponse) error {
	if req == nil || resp == nil || req.Voice == nil || req.Voice.Output == nil {
		return nil
	}
	if err := s.requireVoicePipeline(); err != nil {
		return err
	}

	err := voice.AppendVoiceOutputToMessageResponse(ctx, s.client.voicePipeline, req.Voice, resp)
	if err != nil {
		return fmt.Errorf("synthesize voice output: %w", err)
	}
	return nil
}

func (s *MessagesService) requireVoicePipeline() error {
	if s.client != nil && s.client.voicePipeline != nil {
		return nil
	}
	return fmt.Errorf("voice mode requested but Cartesia is not configured (set CARTESIA_API_KEY or WithProviderKey(\"cartesia\", ...))")
}

func normalizeAudioFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3":
		return "mp3"
	case "pcm", "raw":
		return "pcm"
	default:
		return "wav"
	}
}

func normalizeStreamingAudioFormat(format string) string {
	// Cartesia WebSocket streaming endpoint emits raw PCM chunks.
	return "pcm"
}

func mediaTypeForAudioFormat(format string) string {
	switch normalizeAudioFormat(format) {
	case "mp3":
		return "audio/mpeg"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/wav"
	}
}

func unmarshalStructuredOutput(text string, dest any) error {
	if err := json.Unmarshal([]byte(text), dest); err == nil {
		return nil
	}

	candidate := extractFencedJSON(text)
	if candidate == "" {
		return fmt.Errorf("failed to unmarshal response as JSON")
	}

	if err := json.Unmarshal([]byte(candidate), dest); err != nil {
		return fmt.Errorf("failed to unmarshal fenced JSON response: %w", err)
	}
	return nil
}

func extractFencedJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.Contains(trimmed, "```") {
		return ""
	}

	start := strings.Index(trimmed, "```")
	for start >= 0 {
		rest := trimmed[start+3:]
		newline := strings.Index(rest, "\n")
		if newline < 0 {
			return ""
		}

		bodyStart := start + 3 + newline + 1
		endRel := strings.Index(trimmed[bodyStart:], "```")
		if endRel < 0 {
			return ""
		}

		body := strings.TrimSpace(trimmed[bodyStart : bodyStart+endRel])
		if body != "" {
			return body
		}

		nextStart := strings.Index(trimmed[bodyStart+endRel+3:], "```")
		if nextStart < 0 {
			return ""
		}
		start = bodyStart + endRel + 3 + nextStart
	}

	return ""
}

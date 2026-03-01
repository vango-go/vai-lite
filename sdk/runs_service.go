package vai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

// RunsService calls gateway server-side tool loop endpoints.
//
// This service is available in proxy mode (`WithBaseURL(...)`) and provides
// direct access to `/v1/runs` and `/v1/runs:stream`.
type RunsService struct {
	client *Client
}

// Create executes a blocking server-side run via POST /v1/runs.
func (s *RunsService) Create(ctx context.Context, req *types.RunRequest) (*types.RunResultEnvelope, error) {
	if req == nil {
		return nil, core.NewInvalidRequestError("req must not be nil")
	}
	if s == nil || s.client == nil || !s.client.isProxyMode() {
		return nil, core.NewInvalidRequestError("Runs.Create requires proxy mode (set WithBaseURL)")
	}
	if err := validateServerRunRequest(req); err != nil {
		return nil, err
	}

	requestCopy := *req
	requestCopy.Request.Stream = false
	ctx, cancel := withDefaultGatewayTimeout(ctx)
	defer cancel()

	headers, err := s.client.buildGatewayHeaders(requestCopy.Request.Model, requestCopy.Request.Voice, false)
	if err != nil {
		return nil, err
	}
	s.client.attachServerToolProviderHeaders(headers, &requestCopy)

	resp, endpoint, err := s.client.postGatewayJSON(ctx, "/v1/runs", &requestCopy, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGatewayErrorResponse(resp, endpoint, http.MethodPost)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.Error{
			Type:      core.ErrAPI,
			Message:   "failed to read run response",
			RequestID: requestIDFromHeader(resp.Header),
		}
	}

	decoded, err := types.UnmarshalRunResultEnvelope(body)
	if err != nil {
		return nil, &core.Error{
			Type:      core.ErrAPI,
			Message:   "failed to decode run response",
			RequestID: requestIDFromHeader(resp.Header),
		}
	}
	return decoded, nil
}

// Stream executes a streaming server-side run via POST /v1/runs:stream.
func (s *RunsService) Stream(ctx context.Context, req *types.RunRequest) (*RunsStream, error) {
	if req == nil {
		return nil, core.NewInvalidRequestError("req must not be nil")
	}
	if s == nil || s.client == nil || !s.client.isProxyMode() {
		return nil, core.NewInvalidRequestError("Runs.Stream requires proxy mode (set WithBaseURL)")
	}
	if err := validateServerRunRequest(req); err != nil {
		return nil, err
	}

	requestCopy := *req
	requestCopy.Request.Stream = false

	headers, err := s.client.buildGatewayHeaders(requestCopy.Request.Model, requestCopy.Request.Voice, true)
	if err != nil {
		return nil, err
	}
	s.client.attachServerToolProviderHeaders(headers, &requestCopy)

	resp, endpoint, err := s.client.postGatewayJSON(ctx, "/v1/runs:stream", &requestCopy, headers)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeGatewayErrorResponse(resp, endpoint, http.MethodPost)
	}

	return newRunsStream(resp.Body, endpoint), nil
}

func validateServerRunRequest(req *types.RunRequest) error {
	for i, tool := range req.Request.Tools {
		if tool.Type != types.ToolTypeFunction {
			continue
		}
		return &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: "server-side runs do not support caller function tools; use server_tools or Messages.Run/RunStream",
			Param:   fmt.Sprintf("request.tools[%d]", i),
			Code:    "client_function_tools_not_supported",
		}
	}
	return nil
}

func (c *Client) attachServerToolProviderHeaders(headers http.Header, req *types.RunRequest) {
	if c == nil || headers == nil || req == nil {
		return
	}
	enabled := req.ServerTools
	if len(enabled) == 0 {
		enabled = req.Builtins
	}
	if len(enabled) == 0 {
		return
	}

	toolEnabled := make(map[string]struct{}, len(enabled))
	for _, name := range enabled {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		toolEnabled[name] = struct{}{}
	}

	if _, ok := toolEnabled["vai_web_search"]; ok {
		if provider, hasProvider := extractServerToolProvider(req.ServerToolConfig, "vai_web_search"); hasProvider {
			c.setProviderHeader(headers, provider)
		} else {
			c.setProviderHeader(headers, "tavily")
			c.setProviderHeader(headers, "exa")
		}
	}

	if _, ok := toolEnabled["vai_web_fetch"]; ok {
		if provider, hasProvider := extractServerToolProvider(req.ServerToolConfig, "vai_web_fetch"); hasProvider {
			c.setProviderHeader(headers, provider)
		} else {
			c.setProviderHeader(headers, "tavily")
			c.setProviderHeader(headers, "firecrawl")
		}
	}
}

func (c *Client) setProviderHeader(headers http.Header, provider string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return
	}
	headerName, ok := providerByokHeaders[provider]
	if !ok {
		return
	}
	key := c.providerKeyForProvider(provider)
	if key == "" {
		return
	}
	headers.Set(headerName, key)
}

func extractServerToolProvider(config map[string]any, toolName string) (string, bool) {
	if len(config) == 0 {
		return "", false
	}
	rawTool, ok := config[toolName]
	if !ok || rawTool == nil {
		return "", false
	}
	encoded, err := json.Marshal(rawTool)
	if err != nil {
		return "", false
	}
	var decoded struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return "", false
	}
	provider := strings.ToLower(strings.TrimSpace(decoded.Provider))
	if provider == "" {
		return "", false
	}
	return provider, true
}

// RunsStream is an SSE stream from /v1/runs:stream.
type RunsStream struct {
	events <-chan types.RunStreamEvent
	done   chan struct{}
	stop   chan struct{}

	streamURL string
	body      io.ReadCloser

	result *types.RunResult
	err    error

	stopOnce  sync.Once
	closeOnce sync.Once
}

func newRunsStream(body io.ReadCloser, streamURL string) *RunsStream {
	events := make(chan types.RunStreamEvent, 100)
	rs := &RunsStream{
		events:    events,
		done:      make(chan struct{}),
		stop:      make(chan struct{}),
		streamURL: streamURL,
		body:      body,
		result:    nil,
		err:       nil,
		stopOnce:  sync.Once{},
		closeOnce: sync.Once{},
	}

	go rs.read(events)
	return rs
}

func (s *RunsStream) read(events chan<- types.RunStreamEvent) {
	defer close(events)
	defer close(s.done)
	defer s.closeBody()

	parser := newSSEParser(s.body)
	for {
		frame, err := parser.Next()
		if err != nil {
			if err == io.EOF || s.isStopped() {
				return
			}
			s.err = &TransportError{
				Op:  "POST",
				URL: s.streamURL,
				Err: err,
			}
			return
		}

		if len(frame.Data) == 0 {
			continue
		}

		event, err := types.UnmarshalRunStreamEvent(frame.Data)
		if err != nil {
			s.err = core.NewAPIError("failed to decode run stream event")
			return
		}
		if _, unknown := event.(types.UnknownRunStreamEvent); unknown {
			continue
		}

		select {
		case events <- event:
		case <-s.stop:
			return
		}

		switch e := event.(type) {
		case types.RunCompleteEvent:
			s.result = e.Result
			return
		case types.RunErrorEvent:
			s.err = typesErrorToCoreError(e.Error)
			return
		}
	}
}

func (s *RunsStream) isStopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *RunsStream) closeBody() {
	s.closeOnce.Do(func() {
		if s.body != nil {
			_ = s.body.Close()
		}
	})
}

// Events returns streamed run events.
func (s *RunsStream) Events() <-chan types.RunStreamEvent {
	return s.events
}

// Result returns the terminal run result after stream completion.
func (s *RunsStream) Result() *types.RunResult {
	<-s.done
	return s.result
}

// Err returns the terminal stream error, if any.
func (s *RunsStream) Err() error {
	<-s.done
	return s.err
}

// Close terminates the stream and releases resources.
func (s *RunsStream) Close() error {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.closeBody()
	})
	<-s.done
	return nil
}

package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

type fakeFactory struct {
	p core.Provider
}

func (f fakeFactory) New(providerName, apiKey string) (core.Provider, error) {
	return f.p, nil
}

type fakeProvider struct {
	streamEvents []types.StreamEvent
}

func (p *fakeProvider) Name() string { return "anthropic" }

func (p *fakeProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true}
}

func (p *fakeProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return &types.MessageResponse{
		ID:         "msg_1",
		Type:       "message",
		Role:       "assistant",
		Model:      "anthropic/" + req.Model,
		Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "hi"}},
		StopReason: types.StopReasonEndTurn,
	}, nil
}

func (p *fakeProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	return &fakeEventStream{events: p.streamEvents}, nil
}

type fakeEventStream struct {
	events []types.StreamEvent
	i      int
	closed bool
}

func (s *fakeEventStream) Next() (types.StreamEvent, error) {
	if s.i >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.i]
	s.i++
	return ev, nil
}

func (s *fakeEventStream) Close() error {
	s.closed = true
	return nil
}

func TestMessagesHandler_NonStream(t *testing.T) {
	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEMaxStreamDuration: time.Minute,
			SSEPingInterval:      time.Second,
		},
		Upstreams: fakeFactory{p: &fakeProvider{}},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"anthropic/test",
		"messages":[{"role":"user","content":"hello"}]
	}`)))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type=%q", ct)
	}
	if got := rr.Header().Get("X-Model"); got != "anthropic/test" {
		t.Fatalf("X-Model=%q, expected anthropic/test", got)
	}
	if !strings.Contains(rr.Body.String(), `"model":"anthropic/test"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}

func TestMessagesHandler_MissingUpstreamHeader(t *testing.T) {
	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEMaxStreamDuration: time.Minute,
			SSEPingInterval:      time.Second,
		},
		Upstreams: fakeFactory{p: &fakeProvider{}},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"anthropic/test",
		"messages":[{"role":"user","content":"hello"}]
	}`)))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"type":"authentication_error"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}

func TestMessagesHandler_StreamSSE(t *testing.T) {
	fp := &fakeProvider{
		streamEvents: []types.StreamEvent{
			types.MessageStartEvent{
				Type: "message_start",
				Message: types.MessageResponse{
					ID:    "msg_1",
					Type:  "message",
					Role:  "assistant",
					Model: "anthropic/test",
				},
			},
			types.MessageStopEvent{Type: "message_stop"},
		},
	}

	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEMaxStreamDuration: time.Minute,
			SSEPingInterval:      time.Second,
		},
		Upstreams: fakeFactory{p: fp},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"anthropic/test",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`)))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type=%q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: message_start\n") {
		t.Fatalf("missing message_start event: %q", body)
	}
	if !strings.Contains(body, `"type":"message_start"`) {
		t.Fatalf("missing message_start payload: %q", body)
	}
	if !strings.Contains(body, "event: message_stop\n") {
		t.Fatalf("missing message_stop event: %q", body)
	}
}

type blockingEventStream struct {
	closed chan struct{}
}

func newBlockingEventStream() *blockingEventStream {
	return &blockingEventStream{closed: make(chan struct{})}
}

func (s *blockingEventStream) Next() (types.StreamEvent, error) {
	<-s.closed
	return nil, io.EOF
}

func (s *blockingEventStream) Close() error {
	select {
	case <-s.closed:
		// already closed
	default:
		close(s.closed)
	}
	return nil
}

type blockingProvider struct {
	es *blockingEventStream
}

func (p *blockingProvider) Name() string { return "anthropic" }

func (p *blockingProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true}
}

func (p *blockingProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, core.NewInvalidRequestError("not supported")
}

func (p *blockingProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	return p.es, nil
}

func TestMessagesHandler_Stream_PingAndMaxDurationTimeout(t *testing.T) {
	es := newBlockingEventStream()
	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEPingInterval:      10 * time.Millisecond,
			SSEMaxStreamDuration: 80 * time.Millisecond,
		},
		Upstreams: fakeFactory{p: &blockingProvider{es: es}},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"anthropic/test",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`)))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "event: ping\n") {
		t.Fatalf("missing ping event: %q", body)
	}
	if !strings.Contains(body, "event: error\n") {
		t.Fatalf("missing error event: %q", body)
	}
	if !strings.Contains(body, "request timeout") {
		t.Fatalf("missing timeout message: %q", body)
	}
}

func TestMessagesHandler_Stream_ClientDisconnect_ClosesUpstream(t *testing.T) {
	es := newBlockingEventStream()
	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEPingInterval:      1 * time.Second,
			SSEMaxStreamDuration: 5 * time.Second,
		},
		Upstreams: fakeFactory{p: &blockingProvider{es: es}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"anthropic/test",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`))).WithContext(ctx)
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")

	done := make(chan struct{})
	go func() {
		defer close(done)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}()

	cancel()
	<-done

	select {
	case <-es.closed:
		// ok
	default:
		t.Fatalf("expected upstream stream to be closed on client disconnect")
	}
}

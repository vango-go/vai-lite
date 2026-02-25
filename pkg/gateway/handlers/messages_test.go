package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
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

type delayedEvent struct {
	delay time.Duration
	ev    types.StreamEvent
	err   error
}

type delayedEventStream struct {
	events []delayedEvent
	i      int
	closed chan struct{}
}

func newDelayedEventStream(events []delayedEvent) *delayedEventStream {
	return &delayedEventStream{
		events: events,
		closed: make(chan struct{}),
	}
}

func (s *delayedEventStream) Next() (types.StreamEvent, error) {
	if s.i >= len(s.events) {
		return nil, io.EOF
	}
	next := s.events[s.i]
	s.i++

	timer := time.NewTimer(next.delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return next.ev, next.err
	case <-s.closed:
		return nil, io.EOF
	}
}

func (s *delayedEventStream) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

type delayedProvider struct {
	es core.EventStream
}

func (p *delayedProvider) Name() string { return "anthropic" }

func (p *delayedProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true}
}

func (p *delayedProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return nil, core.NewInvalidRequestError("not supported")
}

func (p *delayedProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	return p.es, nil
}

type fakeStreamingTTSProvider struct {
	failOnSend bool
}

func (p *fakeStreamingTTSProvider) Name() string { return "fake-tts" }

func (p *fakeStreamingTTSProvider) Synthesize(ctx context.Context, text string, opts tts.SynthesizeOptions) (*tts.Synthesis, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *fakeStreamingTTSProvider) SynthesizeStream(ctx context.Context, text string, opts tts.SynthesizeOptions) (*tts.SynthesisStream, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *fakeStreamingTTSProvider) NewStreamingContext(ctx context.Context, opts tts.StreamingContextOptions) (*tts.StreamingContext, error) {
	sc := tts.NewStreamingContext()
	sc.SendFunc = func(text string, isFinal bool) error {
		if p.failOnSend && strings.TrimSpace(text) != "" {
			err := fmt.Errorf("forced tts failure")
			sc.SetError(err)
			return err
		}
		if strings.TrimSpace(text) != "" {
			if !sc.PushAudio([]byte{0x01, 0x02, 0x03}) {
				return nil
			}
		}
		return nil
	}
	sc.CloseFunc = func() error {
		sc.FinishAudio()
		return nil
	}
	return sc, nil
}

func newVoiceStreamingRequest() *types.MessageRequest {
	return &types.MessageRequest{
		Model: "test",
		Voice: &types.VoiceConfig{
			Output: &types.VoiceOutputConfig{
				Voice:      "test-voice",
				Format:     types.VoiceFormatPCM,
				SampleRate: 24000,
			},
		},
	}
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

func TestMessagesHandler_Stream_UpstreamIdleTimeout(t *testing.T) {
	es := newBlockingEventStream()
	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEPingInterval:      10 * time.Millisecond,
			SSEMaxStreamDuration: 5 * time.Second,
			StreamIdleTimeout:    60 * time.Millisecond,
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
	if !strings.Contains(body, `"type":"api_error"`) {
		t.Fatalf("missing api_error type: %q", body)
	}
	if !strings.Contains(body, "upstream stream idle timeout") {
		t.Fatalf("missing idle-timeout message: %q", body)
	}

	select {
	case <-es.closed:
	default:
		t.Fatalf("expected upstream stream to be closed on idle timeout")
	}
}

func TestMessagesHandler_Stream_UpstreamActivityResetsIdleTimer(t *testing.T) {
	es := newDelayedEventStream([]delayedEvent{
		{
			delay: 20 * time.Millisecond,
			ev: types.MessageStartEvent{
				Type: "message_start",
				Message: types.MessageResponse{
					ID:    "msg_1",
					Type:  "message",
					Role:  "assistant",
					Model: "anthropic/test",
				},
			},
		},
		{
			delay: 20 * time.Millisecond,
			ev:    types.MessageStopEvent{Type: "message_stop"},
		},
	})

	h := MessagesHandler{
		Config: config.Config{
			MaxBodyBytes:         1 << 20,
			ModelAllowlist:       map[string]struct{}{},
			SSEPingInterval:      time.Second,
			SSEMaxStreamDuration: 2 * time.Second,
			StreamIdleTimeout:    80 * time.Millisecond,
		},
		Upstreams: fakeFactory{p: &delayedProvider{es: es}},
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
	if !strings.Contains(body, "event: message_stop\n") {
		t.Fatalf("expected normal stream completion, got %q", body)
	}
	if strings.Contains(body, "upstream stream idle timeout") {
		t.Fatalf("unexpected idle-timeout error for active upstream stream: %q", body)
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

func TestMessagesHandler_Stream_MessageStartIncludesUserTranscript(t *testing.T) {
	provider := &fakeProvider{
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
			SSEMaxStreamDuration: time.Second,
			StreamIdleTimeout:    time.Second,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	h.serveStream(rr, req, req.Context(), "req_test", provider, &types.MessageRequest{Model: "test"}, nil, "hello from stt")

	body := rr.Body.String()
	if !strings.Contains(body, "event: message_start\n") {
		t.Fatalf("missing message_start event: %q", body)
	}
	if !strings.Contains(body, `"user_transcript":"hello from stt"`) {
		t.Fatalf("missing user transcript metadata in message_start: %q", body)
	}
}

func TestMessagesHandler_Stream_VoiceAudioChunkFormatAndFinalMarker(t *testing.T) {
	provider := &fakeProvider{
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
			types.ContentBlockStartEvent{
				Type:         "content_block_start",
				Index:        0,
				ContentBlock: types.TextBlock{Type: "text", Text: ""},
			},
			types.ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: 0,
				Delta: types.TextDelta{Type: "text_delta", Text: "Hello."},
			},
			types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
			types.MessageStopEvent{Type: "message_stop"},
		},
	}

	voicePipeline := voice.NewPipelineWithProviders(nil, &fakeStreamingTTSProvider{})
	h := MessagesHandler{
		Config: config.Config{
			SSEMaxStreamDuration: time.Second,
			StreamIdleTimeout:    time.Second,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	h.serveStream(rr, req, req.Context(), "req_test", provider, newVoiceStreamingRequest(), voicePipeline, "")

	body := rr.Body.String()
	if !strings.Contains(body, "event: audio_chunk\n") {
		t.Fatalf("missing audio_chunk events: %q", body)
	}
	if !strings.Contains(body, `"format":"pcm_s16le"`) {
		t.Fatalf("expected pcm_s16le format in audio_chunk event: %q", body)
	}
	if !strings.Contains(body, `"is_final":true`) {
		t.Fatalf("missing final audio chunk marker: %q", body)
	}
	if !strings.Contains(body, "event: message_stop\n") {
		t.Fatalf("missing message_stop event: %q", body)
	}
}

func TestMessagesHandler_Stream_TTSFailureEmitsAudioUnavailableAndContinues(t *testing.T) {
	provider := &fakeProvider{
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
			types.ContentBlockStartEvent{
				Type:         "content_block_start",
				Index:        0,
				ContentBlock: types.TextBlock{Type: "text", Text: ""},
			},
			types.ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: 0,
				Delta: types.TextDelta{Type: "text_delta", Text: "Hello."},
			},
			types.ContentBlockStopEvent{Type: "content_block_stop", Index: 0},
			types.MessageStopEvent{Type: "message_stop"},
		},
	}

	voicePipeline := voice.NewPipelineWithProviders(nil, &fakeStreamingTTSProvider{failOnSend: true})
	h := MessagesHandler{
		Config: config.Config{
			SSEMaxStreamDuration: time.Second,
			StreamIdleTimeout:    time.Second,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	h.serveStream(rr, req, req.Context(), "req_test", provider, newVoiceStreamingRequest(), voicePipeline, "")

	body := rr.Body.String()
	if !strings.Contains(body, "event: audio_unavailable\n") {
		t.Fatalf("missing audio_unavailable event: %q", body)
	}
	if !strings.Contains(body, `"reason":"tts_failed"`) {
		t.Fatalf("missing tts_failed reason in audio_unavailable event: %q", body)
	}
	if !strings.Contains(body, "TTS synthesis failed: forced tts failure") {
		t.Fatalf("missing deterministic tts failure message: %q", body)
	}
	if !strings.Contains(body, "event: message_stop\n") {
		t.Fatalf("text stream did not complete after TTS failure: %q", body)
	}
	if strings.Contains(body, "event: error\n") {
		t.Fatalf("tts failure should not emit terminal error event: %q", body)
	}

	unavailableIdx := strings.Index(body, "event: audio_unavailable\n")
	if unavailableIdx == -1 {
		t.Fatalf("missing audio_unavailable event: %q", body)
	}
	if strings.Contains(body[unavailableIdx:], "event: audio_chunk\n") {
		t.Fatalf("audio_chunk emitted after audio_unavailable: %q", body)
	}
	if strings.Contains(body, `"is_final":true`) {
		t.Fatalf("final audio marker should not be emitted after audio_unavailable: %q", body)
	}
}

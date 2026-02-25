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
	"github.com/vango-go/vai-lite/pkg/gateway/lifecycle"
)

type fakeRunProvider struct {
	createResp   *types.MessageResponse
	streamEvents []types.StreamEvent
}

func (p *fakeRunProvider) Name() string { return "anthropic" }
func (p *fakeRunProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true}
}
func (p *fakeRunProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	if p.createResp != nil {
		return p.createResp, nil
	}
	return &types.MessageResponse{Type: "message", Role: "assistant", Model: req.Model, StopReason: types.StopReasonEndTurn, Content: []types.ContentBlock{types.TextBlock{Type: "text", Text: "ok"}}}, nil
}
func (p *fakeRunProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	return &fakeEventStream{events: p.streamEvents}, nil
}

func baseRunsConfig() config.Config {
	return config.Config{
		MaxBodyBytes:              1 << 20,
		ModelAllowlist:            map[string]struct{}{},
		SSEPingInterval:           10 * time.Millisecond,
		SSEMaxStreamDuration:      time.Minute,
		StreamIdleTimeout:         time.Second,
		TavilyBaseURL:             "https://api.tavily.com",
		FirecrawlBaseURL:          "https://api.firecrawl.dev",
		LimitMaxConcurrentStreams: 4,
	}
}

func TestRunsHandler_MethodNotAllowed(t *testing.T) {
	h := RunsHandler{Config: baseRunsConfig(), Upstreams: fakeFactory{p: &fakeRunProvider{}}, Stream: false}
	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestRunsHandler_MissingProviderHeader(t *testing.T) {
	h := RunsHandler{Config: baseRunsConfig(), Upstreams: fakeFactory{p: &fakeRunProvider{}}, Stream: false}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader([]byte(`{
		"request":{"model":"anthropic/test","messages":[{"role":"user","content":"hi"}]}
	}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"code":"provider_key_missing"`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestRunsHandler_BuiltinMissingConfigFailFast(t *testing.T) {
	h := RunsHandler{Config: baseRunsConfig(), Upstreams: fakeFactory{p: &fakeRunProvider{}}, Stream: false}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader([]byte(`{
		"request":{"model":"anthropic/test","messages":[{"role":"user","content":"hi"}]},
		"builtins":["vai_web_search"]
	}`)))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"code":"builtin_not_configured"`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestRunsHandler_BlockingSuccess(t *testing.T) {
	h := RunsHandler{Config: baseRunsConfig(), Upstreams: fakeFactory{p: &fakeRunProvider{}}, Stream: false}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader([]byte(`{
		"request":{"model":"anthropic/test","messages":[{"role":"user","content":"hi"}]}
	}`)))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"result"`) || !strings.Contains(rr.Body.String(), `"stop_reason":"end_turn"`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestRunsHandler_StreamSSE(t *testing.T) {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonEndTurn

	h := RunsHandler{
		Config: baseRunsConfig(),
		Upstreams: fakeFactory{p: &fakeRunProvider{streamEvents: []types.StreamEvent{
			types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{Type: "message", Role: "assistant", Model: "test"}},
			types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.TextBlock{Type: "text", Text: ""}},
			types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.TextDelta{Type: "text_delta", Text: "ok"}},
			delta,
		}}},
		Stream: true,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs:stream", bytes.NewReader([]byte(`{
		"request":{"model":"anthropic/test","messages":[{"role":"user","content":"hi"}]}
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
	if !strings.Contains(body, "event: run_start") || !strings.Contains(body, "event: step_start") || !strings.Contains(body, "event: stream_event") || !strings.Contains(body, "event: run_complete") {
		t.Fatalf("body=%s", body)
	}
}

func TestRunsHandler_Stream_DrainingRejected(t *testing.T) {
	lc := &lifecycle.Lifecycle{}
	lc.SetDraining(true)
	h := RunsHandler{Config: baseRunsConfig(), Upstreams: fakeFactory{p: &fakeRunProvider{streamEvents: []types.StreamEvent{}}}, Stream: true, Lifecycle: lc}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs:stream", bytes.NewReader([]byte(`{
		"request":{"model":"anthropic/test","messages":[{"role":"user","content":"hi"}]}
	}`)))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 529 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRunsHandler_Stream_ClientDisconnectSilent(t *testing.T) {
	h := RunsHandler{Config: baseRunsConfig(), Upstreams: fakeFactory{p: &fakeRunProvider{streamEvents: []types.StreamEvent{}}}, Stream: true}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs:stream", bytes.NewReader([]byte(`{
		"request":{"model":"anthropic/test","messages":[{"role":"user","content":"hi"}]}
	}`))).WithContext(ctx)
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != 0 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

var _ = io.EOF

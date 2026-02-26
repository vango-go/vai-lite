package vai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestProxyMessagesCreate_SetsHeadersAndDecodesResponse(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuthorization string
	var gotVersion string
	var gotProviderKey string
	var gotModel string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-VAI-Version")
		gotProviderKey = r.Header.Get("X-Provider-Key-OpenAI")

		var req types.MessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = req.Model

		resp := types.MessageResponse{
			Type:       "message",
			ID:         "msg_123",
			Model:      req.Model,
			Role:       "assistant",
			Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "hello"}},
			StopReason: types.StopReasonEndTurn,
			Usage:      types.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL+"/proxy/"),
		WithGatewayAPIKey("vai_sk_test"),
		WithProviderKey("openai", "sk-openai"),
		WithHTTPClient(server.Client()),
	)

	resp, err := client.Messages.Create(context.Background(), &MessageRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resp == nil || resp.MessageResponse == nil {
		t.Fatalf("Create() returned nil response")
	}

	if gotPath != "/proxy/v1/messages" {
		t.Fatalf("path = %q, want %q", gotPath, "/proxy/v1/messages")
	}
	if gotAuthorization != "Bearer vai_sk_test" {
		t.Fatalf("authorization = %q, want bearer token", gotAuthorization)
	}
	if gotVersion != "1" {
		t.Fatalf("X-VAI-Version = %q, want %q", gotVersion, "1")
	}
	if gotProviderKey != "sk-openai" {
		t.Fatalf("X-Provider-Key-OpenAI = %q, want configured key", gotProviderKey)
	}
	if gotModel != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q, want full public model", gotModel)
	}
	if got := resp.TextContent(); got != "hello" {
		t.Fatalf("TextContent() = %q, want %q", got, "hello")
	}
}

func TestProxyMessagesCreate_UsesOpenAIHeaderForResponsesModels(t *testing.T) {
	t.Parallel()

	var gotProviderKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProviderKey = r.Header.Get("X-Provider-Key-OpenAI")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.MessageResponse{
			Type:       "message",
			ID:         "msg_oairesp",
			Model:      "oai-resp/gpt-5",
			Role:       "assistant",
			Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "ok"}},
			StopReason: types.StopReasonEndTurn,
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("openai", "sk-openai"),
		WithHTTPClient(server.Client()),
	)

	_, err := client.Messages.Create(context.Background(), &MessageRequest{
		Model:    "oai-resp/gpt-5",
		Messages: []Message{{Role: "user", Content: Text("hello")}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if gotProviderKey != "sk-openai" {
		t.Fatalf("X-Provider-Key-OpenAI = %q, want %q", gotProviderKey, "sk-openai")
	}
}

func TestProxyMessagesCreate_ForwardsVoiceInputAndCartesiaHeader(t *testing.T) {
	t.Parallel()

	var gotCartesiaKey string
	var gotAudioBlock bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCartesiaKey = r.Header.Get("X-Provider-Key-Cartesia")

		var req types.MessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		blocks := req.Messages[0].ContentBlocks()
		if len(blocks) > 0 {
			_, gotAudioBlock = blocks[0].(types.AudioBlock)
		}

		resp := types.MessageResponse{
			Type:       "message",
			ID:         "msg_voice",
			Model:      req.Model,
			Role:       "assistant",
			Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "done"}},
			StopReason: types.StopReasonEndTurn,
			Metadata:   map[string]any{"user_transcript": "hello transcript"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("openai", "sk-openai"),
		WithProviderKey("cartesia", "sk-cartesia"),
		WithHTTPClient(server.Client()),
	)

	resp, err := client.Messages.Create(context.Background(), &MessageRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []Message{
			{
				Role: "user",
				Content: ContentBlocks(
					Audio([]byte("voice-bytes"), "audio/wav"),
				),
			},
		},
		Voice: VoiceInput(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if gotCartesiaKey != "sk-cartesia" {
		t.Fatalf("X-Provider-Key-Cartesia = %q, want %q", gotCartesiaKey, "sk-cartesia")
	}
	if !gotAudioBlock {
		t.Fatalf("expected request audio block to be forwarded unchanged in proxy mode")
	}
	if resp.UserTranscript() != "hello transcript" {
		t.Fatalf("UserTranscript() = %q, want %q", resp.UserTranscript(), "hello transcript")
	}
}

func TestProxyMessagesCreate_DecodesErrorEnvelopeAndRequestIDHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req_from_header")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"missing provider key","code":"provider_key_missing"}}`))
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	_, err := client.Messages.Create(context.Background(), &MessageRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err == nil {
		t.Fatalf("expected error")
	}

	var apiErr *core.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *core.Error", err)
	}
	if apiErr.Type != core.ErrAuthentication {
		t.Fatalf("error type = %q, want %q", apiErr.Type, core.ErrAuthentication)
	}
	if apiErr.Code != "provider_key_missing" {
		t.Fatalf("error code = %q, want %q", apiErr.Code, "provider_key_missing")
	}
	if apiErr.RequestID != "req_from_header" {
		t.Fatalf("request_id = %q, want %q", apiErr.RequestID, "req_from_header")
	}
}

func TestProxyMessagesCreate_ReturnsTransportError(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{}
	client := NewClient(
		WithBaseURL("http://127.0.0.1:1"),
		WithHTTPClient(httpClient),
	)

	_, err := client.Messages.Create(context.Background(), &MessageRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err == nil {
		t.Fatalf("expected transport error")
	}
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error type = %T, want *TransportError", err)
	}
}

func TestProxyMessagesStream_ParsesGatewaySSEAndAudio(t *testing.T) {
	t.Parallel()

	audioPayload := []byte("pcm-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		writeSSEJSON(t, w, "message_start", types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				ID:    "msg_stream",
				Role:  "assistant",
				Model: "openai/gpt-4o-mini",
			},
		})
		writeSSEJSON(t, w, "ping", types.PingEvent{Type: "ping"})
		_, _ = fmt.Fprint(w, "event: future_event\ndata: {\"type\":\"future_event\",\"foo\":\"bar\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		writeSSEJSON(t, w, "content_block_start", types.ContentBlockStartEvent{
			Type:         "content_block_start",
			Index:        0,
			ContentBlock: types.TextBlock{Type: "text", Text: ""},
		})
		writeSSEJSON(t, w, "content_block_delta", types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.TextDelta{Type: "text_delta", Text: "hello"},
		})
		writeSSEJSON(t, w, "audio_chunk", types.AudioChunkEvent{
			Type:   "audio_chunk",
			Format: "pcm_s16le",
			Audio:  base64.StdEncoding.EncodeToString(audioPayload),
		})
		writeSSEJSON(t, w, "audio_unavailable", types.AudioUnavailableEvent{
			Type:    "audio_unavailable",
			Reason:  "tts_failed",
			Message: "tts failed",
		})
		writeSSEJSON(t, w, "content_block_stop", types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		})
		delta := types.MessageDeltaEvent{Type: "message_delta"}
		delta.Delta.StopReason = types.StopReasonEndTurn
		writeSSEJSON(t, w, "message_delta", delta)
		writeSSEJSON(t, w, "message_stop", types.MessageStopEvent{Type: "message_stop"})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("openai", "sk-openai"),
		WithProviderKey("cartesia", "sk-cartesia"),
		WithHTTPClient(server.Client()),
	)

	stream, err := client.Messages.Stream(context.Background(), &MessageRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
		Voice: VoiceOutput("voice-id"),
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	var sawPing bool
	var sawAudioChunk bool
	for event := range stream.Events() {
		switch event.(type) {
		case types.PingEvent:
			sawPing = true
		case types.AudioChunkEvent:
			sawAudioChunk = true
		}
	}

	if !sawPing {
		t.Fatalf("expected ping event")
	}
	if !sawAudioChunk {
		t.Fatalf("expected audio_chunk event")
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("stream.Err() = %v, want nil or EOF", err)
	}
	if got := stream.TextContent(); got != "hello" {
		t.Fatalf("TextContent() = %q, want %q", got, "hello")
	}

	var chunks [][]byte
	for chunk := range stream.AudioEvents() {
		chunks = append(chunks, chunk.Data)
	}
	if len(chunks) != 1 {
		t.Fatalf("audio chunk count = %d, want 1", len(chunks))
	}
	if string(chunks[0]) != string(audioPayload) {
		t.Fatalf("audio chunk = %q, want %q", string(chunks[0]), string(audioPayload))
	}
}

func TestProxyMessagesStream_TerminalErrorEvent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEJSON(t, w, "error", types.ErrorEvent{
			Type: "error",
			Error: types.Error{
				Type:      "api_error",
				Message:   "upstream timeout",
				RequestID: "req_stream",
			},
		})
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithProviderKey("openai", "sk-openai"),
		WithHTTPClient(server.Client()),
	)

	stream, err := client.Messages.Stream(context.Background(), &MessageRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	var apiErr *core.Error
	if !errors.As(stream.Err(), &apiErr) {
		t.Fatalf("stream.Err() type = %T, want *core.Error", stream.Err())
	}
	if apiErr.RequestID != "req_stream" {
		t.Fatalf("request_id = %q, want %q", apiErr.RequestID, "req_stream")
	}
}

func TestGatewayEndpoint_JoinVariants(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		baseURL  string
		endpoint string
		want     string
	}{
		{
			name:     "root without trailing slash",
			baseURL:  "https://api.example.com",
			endpoint: "/v1/messages",
			want:     "https://api.example.com/v1/messages",
		},
		{
			name:     "root with trailing slash",
			baseURL:  "https://api.example.com/",
			endpoint: "/v1/messages",
			want:     "https://api.example.com/v1/messages",
		},
		{
			name:     "prefixed path without trailing slash",
			baseURL:  "https://api.example.com/gateway",
			endpoint: "/v1/messages",
			want:     "https://api.example.com/gateway/v1/messages",
		},
		{
			name:     "prefixed path with trailing slash",
			baseURL:  "https://api.example.com/gateway/",
			endpoint: "/v1/messages",
			want:     "https://api.example.com/gateway/v1/messages",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(WithBaseURL(tc.baseURL))
			got, err := client.gatewayEndpoint(tc.endpoint)
			if err != nil {
				t.Fatalf("gatewayEndpoint() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("gatewayEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeSSEJSON(t *testing.T, w http.ResponseWriter, event string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SSE payload: %v", err)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

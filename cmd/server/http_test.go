package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
	s3store "github.com/vango-go/vango-s3"
)

func TestParseInt64Value(t *testing.T) {
	t.Parallel()

	got, err := parseInt64Value(" 2500 ")
	if err != nil {
		t.Fatalf("parseInt64Value() error = %v", err)
	}
	if got != 2500 {
		t.Fatalf("parseInt64Value() = %d, want 2500", got)
	}
}

func TestParseUSDCents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want int64
	}{
		{name: "whole dollars", in: "25", want: 2500},
		{name: "two decimals", in: "25.50", want: 2550},
		{name: "one decimal", in: "25.5", want: 2550},
		{name: "leading dollar", in: "$1.25", want: 125},
		{name: "commas", in: "1,234.56", want: 123456},
		{name: "fraction only", in: ".99", want: 99},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseUSDCents(tt.in)
			if err != nil {
				t.Fatalf("parseUSDCents(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseUSDCents(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseUSDCentsRejectsInvalid(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "-1.00", "12.345", "abc"} {
		if _, err := parseUSDCents(in); err == nil {
			t.Fatalf("parseUSDCents(%q) expected error", in)
		}
	}
}

func TestExtractRunResultFromSSE(t *testing.T) {
	t.Parallel()

	event := types.RunCompleteEvent{
		Type: "run_complete",
		Result: &types.RunResult{
			Response: &types.MessageResponse{
				ID:    "msg_123",
				Type:  "message",
				Role:  "assistant",
				Model: "oai-resp/gpt-5-mini",
				Content: []types.ContentBlock{
					types.TextBlock{Type: "text", Text: "hello from stream"},
				},
				Usage: types.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
			},
			Usage: types.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
		},
	}
	rawEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	raw := []byte("event: run_start\ndata: {\"type\":\"run_start\",\"model\":\"oai-resp/gpt-5-mini\"}\n\n" +
		"event: run_complete\ndata: " + string(rawEvent) + "\n\n")

	result, err := extractRunResult(raw)
	if err != nil {
		t.Fatalf("extractRunResult() error = %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("extractRunResult() returned nil result")
	}
	if got := result.Response.TextContent(); got != "hello from stream" {
		t.Fatalf("TextContent() = %q", got)
	}
	if got := result.Usage.TotalTokens; got != 125 {
		t.Fatalf("Usage.TotalTokens = %d", got)
	}
}

func TestExtractGatewayErrorMessage(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"error":{"type":"authentication_error","message":"missing upstream provider api key header","param":"X-Provider-Key-OpenAI"}}`)
	if got := extractGatewayErrorMessage(raw); got != "missing upstream provider api key header" {
		t.Fatalf("extractGatewayErrorMessage() = %q", got)
	}
}

func TestExtractGatewayErrorMessageSupportsStringEnvelope(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"error":"workspace key missing"}`)
	if got := extractGatewayErrorMessage(raw); got != "workspace key missing" {
		t.Fatalf("extractGatewayErrorMessage() = %q", got)
	}
}

func TestMaterializeMessageResponseFromSSE(t *testing.T) {
	t.Parallel()

	startPayload, err := json.Marshal(types.MessageStartEvent{
		Type: "message_start",
		Message: types.MessageResponse{
			Type:  "message",
			ID:    "msg_123",
			Model: "oai-resp/gpt-5-mini",
			Role:  "assistant",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(start) error = %v", err)
	}
	blockStartPayload, err := json.Marshal(types.ContentBlockStartEvent{
		Type:  "content_block_start",
		Index: 0,
		ContentBlock: types.TextBlock{
			Type: "text",
			Text: "",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(block start) error = %v", err)
	}
	blockDeltaPayload, err := json.Marshal(types.ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: types.TextDelta{Type: "text_delta", Text: "hello from stream"},
	})
	if err != nil {
		t.Fatalf("json.Marshal(block delta) error = %v", err)
	}
	messageDelta := types.MessageDeltaEvent{
		Type:  "message_delta",
		Usage: types.Usage{InputTokens: 12, OutputTokens: 4, TotalTokens: 16},
	}
	messageDelta.Delta.StopReason = types.StopReasonEndTurn
	messageDeltaPayload, err := json.Marshal(messageDelta)
	if err != nil {
		t.Fatalf("json.Marshal(message delta with stop reason) error = %v", err)
	}
	raw := []byte(
		"event: message_start\ndata: " + string(startPayload) + "\n\n" +
			"event: content_block_start\ndata: " + string(blockStartPayload) + "\n\n" +
			"event: content_block_delta\ndata: " + string(blockDeltaPayload) + "\n\n" +
			"event: message_delta\ndata: " + string(messageDeltaPayload) + "\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	)

	resp, streamErr, err := materializeMessageResponseFromSSE(raw)
	if err != nil {
		t.Fatalf("materializeMessageResponseFromSSE() error = %v", err)
	}
	if streamErr != nil {
		t.Fatalf("streamErr = %#v, want nil", streamErr)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
	if got := resp.TextContent(); got != "hello from stream" {
		t.Fatalf("TextContent() = %q", got)
	}
	if got := resp.Usage.TotalTokens; got != 16 {
		t.Fatalf("Usage.TotalTokens = %d", got)
	}
}

func TestBuildConversationRunRequestUsesGatewayCompatibleTimeout(t *testing.T) {
	t.Parallel()

	srv := &betaServer{
		services: &services.AppServices{},
	}
	detail := &services.ConversationDetail{
		Conversation: services.Conversation{
			ID:    "conv_123",
			Title: "New chat",
			Model: "oai-resp/gpt-5-mini",
		},
		Messages: []services.ConversationMessage{
			{
				ID:        "msg_1",
				Role:      "user",
				BodyText:  "hi",
				CreatedAt: time.Now(),
			},
			{
				ID:       "msg_2",
				Role:     "assistant",
				BodyText: "hello",
				Attachments: []services.Attachment{
					{
						ID:          "att_1",
						Filename:    "image.png",
						ContentType: "image/png",
						SizeBytes:   128,
						BlobRef:     s3store.BlobRef{Key: "files/t/org/img"},
					},
				},
				CreatedAt: time.Now(),
			},
		},
	}

	headers := http.Header{
		servertools.HeaderProviderKeyTavily:    []string{"tavily-key"},
		servertools.HeaderProviderKeyExa:       []string{"exa-key"},
		servertools.HeaderProviderKeyFirecrawl: []string{"firecrawl-key"},
	}

	req, err := srv.buildConversationRunRequest(context.Background(), detail, headers)
	if err != nil {
		t.Fatalf("buildConversationRunRequest() error = %v", err)
	}
	if got := req.Run.TimeoutMS; got != 300000 {
		t.Fatalf("Run.TimeoutMS = %d, want 300000", got)
	}
	if got := req.Run.ToolTimeoutMS; got != 30000 {
		t.Fatalf("Run.ToolTimeoutMS = %d, want 30000", got)
	}
	if len(req.ServerTools) != 2 {
		t.Fatalf("len(ServerTools) = %d, want 2", len(req.ServerTools))
	}
	if got := req.ServerTools[0]; got != servertools.ToolWebSearch {
		t.Fatalf("ServerTools[0] = %q, want %q", got, servertools.ToolWebSearch)
	}
	if got := req.ServerTools[1]; got != servertools.ToolWebFetch {
		t.Fatalf("ServerTools[1] = %q, want %q", got, servertools.ToolWebFetch)
	}
	searchConfig, ok := req.ServerToolConfig[servertools.ToolWebSearch].(map[string]any)
	if !ok {
		t.Fatalf("search tool config has unexpected type %T", req.ServerToolConfig[servertools.ToolWebSearch])
	}
	if got := searchConfig["provider"]; got != servertools.ProviderTavily {
		t.Fatalf("search provider = %#v, want %q", got, servertools.ProviderTavily)
	}
	fetchConfig, ok := req.ServerToolConfig[servertools.ToolWebFetch].(map[string]any)
	if !ok {
		t.Fatalf("fetch tool config has unexpected type %T", req.ServerToolConfig[servertools.ToolWebFetch])
	}
	if got := fetchConfig["provider"]; got != servertools.ProviderFirecrawl {
		t.Fatalf("fetch provider = %#v, want %q", got, servertools.ProviderFirecrawl)
	}
	if got := fetchConfig["format"]; got != "markdown" {
		t.Fatalf("fetch format = %#v, want %q", got, "markdown")
	}
}

func TestConversationServerToolsSkipsUnavailableTools(t *testing.T) {
	t.Parallel()

	tools, config := conversationServerTools(http.Header{})
	if len(tools) != 0 {
		t.Fatalf("len(tools) = %d, want 0", len(tools))
	}
	if config != nil {
		t.Fatalf("config = %#v, want nil", config)
	}
}

func TestConversationServerToolsFallsBackToAvailableProviders(t *testing.T) {
	t.Parallel()

	headers := http.Header{
		servertools.HeaderProviderKeyExa:    []string{"exa-key"},
		servertools.HeaderProviderKeyTavily: []string{"tavily-key"},
	}

	tools, config := conversationServerTools(headers)
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}
	if got := tools[0]; got != servertools.ToolWebSearch {
		t.Fatalf("tools[0] = %q, want %q", got, servertools.ToolWebSearch)
	}
	if got := tools[1]; got != servertools.ToolWebFetch {
		t.Fatalf("tools[1] = %q, want %q", got, servertools.ToolWebFetch)
	}
	searchConfig := config[servertools.ToolWebSearch].(map[string]any)
	if got := searchConfig["provider"]; got != servertools.ProviderTavily {
		t.Fatalf("search provider = %#v, want %q", got, servertools.ProviderTavily)
	}
	fetchConfig := config[servertools.ToolWebFetch].(map[string]any)
	if got := fetchConfig["provider"]; got != servertools.ProviderTavily {
		t.Fatalf("fetch provider = %#v, want %q", got, servertools.ProviderTavily)
	}
}

func TestPricingMetadataForGatewayRequestDetectsAudioAndImage(t *testing.T) {
	t.Parallel()

	req := &types.MessageRequest{
		Model: "gem-dev/gemini-3.1-flash-image-preview",
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.AudioSTTBlock{
						Type: "audio_stt",
						Source: types.AudioSource{
							Type:      "base64",
							MediaType: "audio/wav",
							Data:      "AAAA",
						},
					},
				},
			},
		},
		Output: &types.OutputConfig{
			Modalities: []string{"image"},
		},
	}

	meta := pricingMetadataForGatewayRequest(req)
	if got := meta["input_modality"]; got != string(services.PricingInputModalityAudio) {
		t.Fatalf("input_modality = %#v, want %q", got, services.PricingInputModalityAudio)
	}
	if got := meta["output_modality"]; got != string(services.PricingOutputModalityImage) {
		t.Fatalf("output_modality = %#v, want %q", got, services.PricingOutputModalityImage)
	}
}

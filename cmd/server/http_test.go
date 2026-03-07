package main

import (
	"encoding/json"
	"testing"

	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vai-lite/pkg/core/types"
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

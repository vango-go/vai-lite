//go:build integration_proxy
// +build integration_proxy

package integration_proxy_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	vai "github.com/vango-go/vai-lite/sdk"
)

const integrationProxyCartesiaVoiceID = "a0e99841-438c-4a64-b679-ae501e7d6091"

func selectedProxyOAIRespProvider(t *testing.T) proxyProviderConfig {
	t.Helper()

	for _, provider := range selectedProxyProviders(t) {
		if provider.Name == "oai-resp" {
			return provider
		}
	}

	t.Skip("oai-resp not selected by VAI_INTEGRATION_PROXY_PROVIDER")
	return proxyProviderConfig{}
}

func requireCartesiaKey(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("CARTESIA_API_KEY")) == "" {
		t.Skip("CARTESIA_API_KEY not set")
	}
}

func requireProxyVoiceKeys(t *testing.T, provider proxyProviderConfig) {
	t.Helper()
	provider.RequireKey(t)
	requireCartesiaKey(t)
}

func synthesizeVoiceSeedAudio(t *testing.T, ctx context.Context, text string) []byte {
	t.Helper()

	seedClient := vai.NewClient(
		vai.WithProviderKey("cartesia", strings.TrimSpace(os.Getenv("CARTESIA_API_KEY"))),
	)

	pipeline := seedClient.VoicePipeline()
	if pipeline == nil {
		t.Fatal("expected direct seed client voice pipeline to be initialized")
	}

	seedCfg := vai.VoiceOutput(integrationProxyCartesiaVoiceID, vai.WithAudioFormat(vai.AudioFormatWAV))
	seedAudio, err := pipeline.SynthesizeResponse(ctx, text, seedCfg, "sonic-3")
	if err != nil {
		t.Fatalf("failed generating seed audio for STT roundtrip: %v", err)
	}
	if len(seedAudio) == 0 {
		t.Fatal("seed audio is empty")
	}

	return seedAudio
}

func TestProxy_Voice_Create_STTRoundTripAndFinalAudio_OAIResp(t *testing.T) {
	provider := selectedProxyOAIRespProvider(t)
	requireProxyVoiceKeys(t, provider)
	model := providerModel(provider)
	ctx := testContext(t, 150*time.Second)

	seedAudio := synthesizeVoiceSeedAudio(t, ctx, "Hello from Cartesia proxy integration test.")

	resp, err := testClient.Messages.Create(ctx, &vai.MessageRequest{
		Model: model,
		Messages: []vai.Message{
			{Role: "user", Content: vai.ContentBlocks(vai.AudioSTT(seedAudio, "audio/wav", vai.WithSTTLanguage("en")))},
		},
		STTModel:  "cartesia/ink-whisper",
		TTSModel:  "cartesia/sonic-3",
		Voice:     vai.VoiceOutput(integrationProxyCartesiaVoiceID, vai.WithAudioFormat(vai.AudioFormatWAV)),
		MaxTokens: 600,
	})
	if err != nil {
		t.Fatalf("Create voice request failed: %v", err)
	}
	if resp == nil || resp.MessageResponse == nil {
		t.Fatalf("expected non-nil response")
	}

	if got := strings.TrimSpace(resp.UserTranscript()); got == "" {
		t.Fatalf("expected non-empty user transcript from STT")
	}

	audio := resp.AudioContent()
	if audio == nil {
		t.Fatalf("expected synthesized audio block in response")
	}
	decoded, err := base64.StdEncoding.DecodeString(audio.Source.Data)
	if err != nil {
		t.Fatalf("failed decoding response audio: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatalf("decoded response audio is empty")
	}
}

func TestProxy_Voice_Stream_EmitsAudioEvents_OAIResp(t *testing.T) {
	provider := selectedProxyOAIRespProvider(t)
	requireProxyVoiceKeys(t, provider)
	model := providerModel(provider)
	ctx := testContext(t, 150*time.Second)

	stream, err := testClient.Messages.Stream(ctx, &vai.MessageRequest{
		Model: model,
		Messages: []vai.Message{
			{Role: "user", Content: vai.Text("Give me a short two sentence summary of Mars.")},
		},
		Voice:     vai.VoiceOutput(integrationProxyCartesiaVoiceID, vai.WithAudioFormat(vai.AudioFormatWAV)),
		MaxTokens: 500,
	})
	if err != nil {
		t.Fatalf("Stream voice request failed: %v", err)
	}
	defer stream.Close()

	audioDone := make(chan struct{})
	var chunkCount int
	var totalAudioBytes int
	go func() {
		defer close(audioDone)
		for chunk := range stream.AudioEvents() {
			chunkCount++
			totalAudioBytes += len(chunk.Data)
		}
	}()

	for range stream.Events() {
	}
	<-audioDone

	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("stream err: %v", err)
	}
	if chunkCount == 0 {
		t.Fatalf("expected audio chunks from Stream.AudioEvents")
	}
	if totalAudioBytes == 0 {
		t.Fatalf("expected non-empty audio chunks")
	}

	resp := stream.Response()
	if resp == nil {
		t.Fatalf("expected non-nil stream response")
	}
	if audio := resp.AudioContent(); audio != nil {
		decoded, decodeErr := base64.StdEncoding.DecodeString(audio.Source.Data)
		if decodeErr != nil {
			t.Fatalf("failed decoding optional final stream audio block: %v", decodeErr)
		}
		if len(decoded) == 0 {
			t.Fatalf("decoded optional final stream audio block is empty")
		}
	}
}

func TestProxy_Voice_RunStream_EmitsAudioChunkEvents_OAIResp(t *testing.T) {
	provider := selectedProxyOAIRespProvider(t)
	requireProxyVoiceKeys(t, provider)
	model := providerModel(provider)
	ctx := testContext(t, 180*time.Second)

	stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
		Model: model,
		Messages: []vai.Message{
			{Role: "user", Content: vai.Text("Explain what a binary search tree is in two concise paragraphs.")},
		},
		Voice:     vai.VoiceOutput(integrationProxyCartesiaVoiceID, vai.WithAudioFormat(vai.AudioFormatWAV)),
		MaxTokens: 800,
	})
	if err != nil {
		t.Fatalf("RunStream voice request failed: %v", err)
	}
	defer stream.Close()

	audioChunkEvents := 0
	totalAudioBytes := 0
	for event := range stream.Events() {
		if audioEvent, ok := event.(vai.AudioChunkEvent); ok {
			audioChunkEvents++
			totalAudioBytes += len(audioEvent.Data)
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("run stream err: %v", err)
	}
	if audioChunkEvents == 0 {
		t.Fatalf("expected AudioChunkEvent events")
	}
	if totalAudioBytes == 0 {
		t.Fatalf("expected non-empty audio in AudioChunkEvent")
	}

	result := stream.Result()
	if result == nil || result.Response == nil {
		t.Fatalf("expected final run response")
	}
	if audio := result.Response.AudioContent(); audio != nil {
		decoded, decodeErr := base64.StdEncoding.DecodeString(audio.Source.Data)
		if decodeErr != nil {
			t.Fatalf("failed decoding optional final run audio block: %v", decodeErr)
		}
		if len(decoded) == 0 {
			t.Fatalf("decoded optional final run audio block is empty")
		}
	}
}

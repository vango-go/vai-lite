package vai

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestMessagesCreate_TranscribesAudioInputAndSetsUserTranscript(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(textResponse("hello"))
	svc := newMessagesServiceForMessagesTest(provider)
	attachVoicePipelineForSDKTests(
		svc,
		&fakeSDKSTTProvider{transcripts: []string{"transcribed user audio"}},
		&fakeSDKTTSProvider{},
	)

	resp, err := svc.Create(context.Background(), &MessageRequest{
		Model: "test/model",
		Messages: []Message{{
			Role: "user",
			Content: ContentBlocks(
				Audio([]byte("audio-data"), "audio/wav"),
			),
		}},
		Voice: VoiceInput(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resp.UserTranscript() != "transcribed user audio" {
		t.Fatalf("UserTranscript() = %q, want %q", resp.UserTranscript(), "transcribed user audio")
	}
	if len(provider.createRequests) != 1 {
		t.Fatalf("len(createRequests) = %d, want 1", len(provider.createRequests))
	}
	requestBlocks := provider.createRequests[0].Messages[0].ContentBlocks()
	if len(requestBlocks) != 1 {
		t.Fatalf("len(request blocks) = %d, want 1", len(requestBlocks))
	}
	textBlock, ok := requestBlocks[0].(types.TextBlock)
	if !ok {
		t.Fatalf("expected transcribed text block, got %T", requestBlocks[0])
	}
	if textBlock.Text != "transcribed user audio" {
		t.Fatalf("transcribed text = %q, want %q", textBlock.Text, "transcribed user audio")
	}
}

func TestMessagesCreate_AppendsSynthesizedAudioBlock(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(textResponse("assistant response"))
	svc := newMessagesServiceForMessagesTest(provider)
	ttsProvider := &fakeSDKTTSProvider{synthAudio: []byte("audio-bytes")}
	attachVoicePipelineForSDKTests(svc, &fakeSDKSTTProvider{}, ttsProvider)

	resp, err := svc.Create(context.Background(), &MessageRequest{
		Model: "test/model",
		Messages: []Message{{
			Role:    "user",
			Content: Text("hello"),
		}},
		Voice: VoiceOutput("voice-id", WithAudioFormat(AudioFormatWAV)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if ttsProvider.synthCalls != 1 {
		t.Fatalf("synthCalls = %d, want 1", ttsProvider.synthCalls)
	}

	audio := resp.AudioContent()
	if audio == nil {
		t.Fatalf("expected response audio block")
	}
	if audio.Source.Data != base64.StdEncoding.EncodeToString([]byte("audio-bytes")) {
		t.Fatalf("audio data mismatch")
	}
	if audio.Transcript == nil || *audio.Transcript != "assistant response" {
		t.Fatalf("audio transcript = %#v, want assistant response", audio.Transcript)
	}
}

func TestMessagesCreate_VoiceRequestedWithoutPipelineErrors(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(textResponse("hello"))
	svc := newMessagesServiceForMessagesTest(provider)

	_, err := svc.Create(context.Background(), &MessageRequest{
		Model: "test/model",
		Messages: []Message{{
			Role:    "user",
			Content: Text("hello"),
		}},
		Voice: VoiceInput(),
	})
	if err == nil {
		t.Fatalf("expected missing Cartesia error")
	}
	if !strings.Contains(err.Error(), "Cartesia") {
		t.Fatalf("error = %v, expected Cartesia setup hint", err)
	}
}

func TestMessagesStream_AudioEventsClosedWithoutVoice(t *testing.T) {
	provider := newScriptedProvider("test", singleTurnTextStream("hello"))
	svc := newMessagesServiceForMessagesTest(provider)

	stream, err := svc.Stream(context.Background(), &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("hello")}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	if _, ok := <-stream.AudioEvents(); ok {
		t.Fatalf("expected closed audio channel for non-voice stream")
	}
}

func TestMessagesStream_EmitsAudioChunksAndFinalAudioBlock(t *testing.T) {
	provider := newScriptedProvider("test", singleTurnTextStream("hello world"))
	svc := newMessagesServiceForMessagesTest(provider)
	attachVoicePipelineForSDKTests(svc, &fakeSDKSTTProvider{}, &fakeSDKTTSProvider{})

	stream, err := svc.Stream(context.Background(), &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("hello")}},
		Voice:    VoiceOutput("voice-id", WithAudioFormat(AudioFormatWAV)),
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	var chunks [][]byte
	for chunk := range stream.AudioEvents() {
		chunks = append(chunks, chunk.Data)
	}
	if len(chunks) == 0 {
		t.Fatalf("expected at least one audio chunk")
	}

	resp := stream.Response()
	if resp == nil {
		t.Fatalf("expected response")
	}
	if resp.AudioContent() == nil {
		t.Fatalf("expected final synthesized audio block in response")
	}
}

func TestMessagesStream_TTSFailureSetsStreamError(t *testing.T) {
	provider := newScriptedProvider("test", singleTurnTextStream("hello world"))
	svc := newMessagesServiceForMessagesTest(provider)
	attachVoicePipelineForSDKTests(
		svc,
		&fakeSDKSTTProvider{},
		&fakeSDKTTSProvider{streamSendErr: errors.New("tts send failed")},
	)

	stream, err := svc.Stream(context.Background(), &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("hello")}},
		Voice:    VoiceOutput("voice-id"),
	})
	if err != nil {
		t.Fatalf("Stream() returned setup error: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	if stream.Err() == nil {
		t.Fatalf("expected stream error from TTS failure")
	}
	if !strings.Contains(stream.Err().Error(), "voice output") {
		t.Fatalf("stream.Err() = %v, expected voice output error", stream.Err())
	}
}

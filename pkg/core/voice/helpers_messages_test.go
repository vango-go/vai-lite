package voice

import (
	"context"
	"encoding/base64"
	"io"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
)

type fakeSTT struct{}

func (f *fakeSTT) Name() string { return "fake" }
func (f *fakeSTT) Transcribe(ctx context.Context, audio io.Reader, opts stt.TranscribeOptions) (*stt.Transcript, error) {
	return &stt.Transcript{Text: "hello"}, nil
}
func (f *fakeSTT) TranscribeStream(ctx context.Context, audio io.Reader, opts stt.TranscribeOptions) (<-chan stt.TranscriptDelta, error) {
	ch := make(chan stt.TranscriptDelta)
	close(ch)
	return ch, nil
}
func (f *fakeSTT) NewStreamingSTT(ctx context.Context, opts stt.TranscribeOptions) (*stt.StreamingSTT, error) {
	return nil, nil
}

type fakeTTS struct{}

func (f *fakeTTS) Name() string { return "fake" }
func (f *fakeTTS) Synthesize(ctx context.Context, text string, opts tts.SynthesizeOptions) (*tts.Synthesis, error) {
	return &tts.Synthesis{Audio: []byte{0x01, 0x02, 0x03}, Format: "wav"}, nil
}
func (f *fakeTTS) SynthesizeStream(ctx context.Context, text string, opts tts.SynthesizeOptions) (*tts.SynthesisStream, error) {
	return nil, nil
}
func (f *fakeTTS) NewStreamingContext(ctx context.Context, opts tts.StreamingContextOptions) (*tts.StreamingContext, error) {
	return nil, nil
}

func TestPreprocessMessageRequestInputAudio_DoesNotMutateOriginal(t *testing.T) {
	p := NewPipelineWithProviders(&fakeSTT{}, &fakeTTS{})

	audioData := []byte{0x00, 0x01, 0x02}
	req := &types.MessageRequest{
		Model: "anthropic/test",
		Voice: &types.VoiceConfig{Input: &types.VoiceInputConfig{}},
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.AudioBlock{
						Type: "audio",
						Source: types.AudioSource{
							Type:      "base64",
							MediaType: "audio/wav",
							Data:      base64.StdEncoding.EncodeToString(audioData),
						},
					},
				},
			},
		},
	}

	processed, transcript, err := PreprocessMessageRequestInputAudio(context.Background(), p, req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if processed == req {
		t.Fatalf("expected a copy of req")
	}
	if transcript != "hello" {
		t.Fatalf("transcript=%q, expected hello", transcript)
	}

	// Original request still contains audio block.
	origBlocks := req.Messages[0].ContentBlocks()
	if _, ok := origBlocks[0].(types.AudioBlock); !ok {
		t.Fatalf("expected original to contain audio block, got %T", origBlocks[0])
	}

	// Processed request contains text block.
	newBlocks := processed.Messages[0].ContentBlocks()
	tb, ok := newBlocks[0].(types.TextBlock)
	if !ok {
		t.Fatalf("expected processed to contain text block, got %T", newBlocks[0])
	}
	if tb.Text != "hello" {
		t.Fatalf("text=%q, expected hello", tb.Text)
	}
}

func TestAppendVoiceOutputToMessageResponse_AppendsAudioBlock(t *testing.T) {
	p := NewPipelineWithProviders(&fakeSTT{}, &fakeTTS{})

	cfg := &types.VoiceConfig{
		Output: &types.VoiceOutputConfig{
			Voice:  "voice_1",
			Format: "wav",
		},
	}

	resp := &types.MessageResponse{
		ID:    "msg_1",
		Type:  "message",
		Role:  "assistant",
		Model: "anthropic/test",
		Content: []types.ContentBlock{
			types.TextBlock{Type: "text", Text: "hi"},
		},
		StopReason: types.StopReasonEndTurn,
	}

	if err := AppendVoiceOutputToMessageResponse(context.Background(), p, cfg, resp); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(resp.Content))
	}
	ab, ok := resp.Content[1].(types.AudioBlock)
	if !ok {
		t.Fatalf("expected audio block, got %T", resp.Content[1])
	}
	if ab.Source.Type != "base64" {
		t.Fatalf("source.type=%q", ab.Source.Type)
	}
	if ab.Source.MediaType != "audio/wav" {
		t.Fatalf("source.media_type=%q", ab.Source.MediaType)
	}
	if ab.Source.Data != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}) {
		t.Fatalf("unexpected audio data")
	}
	if ab.Transcript == nil || *ab.Transcript != "hi" {
		t.Fatalf("unexpected transcript: %#v", ab.Transcript)
	}
}

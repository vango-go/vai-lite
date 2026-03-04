package voice

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
)

type fakeSTTProvider struct {
	transcripts []string
	calls       int
	err         error
}

func (f *fakeSTTProvider) Name() string { return "fake-stt" }

func (f *fakeSTTProvider) Transcribe(_ context.Context, _ io.Reader, _ stt.TranscribeOptions) (*stt.Transcript, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.calls >= len(f.transcripts) {
		return &stt.Transcript{Text: ""}, nil
	}
	text := f.transcripts[f.calls]
	f.calls++
	return &stt.Transcript{Text: text}, nil
}

func (f *fakeSTTProvider) TranscribeStream(context.Context, io.Reader, stt.TranscribeOptions) (<-chan stt.TranscriptDelta, error) {
	return nil, nil
}

func (f *fakeSTTProvider) NewStreamingSTT(context.Context, stt.TranscribeOptions) (*stt.StreamingSTT, error) {
	return nil, nil
}

type fakeTTSProvider struct {
	audio          []byte
	err            error
	lastSynthOpts  tts.SynthesizeOptions
	lastStreamOpts tts.StreamingContextOptions
	streamingCtx   *tts.StreamingContext
}

func (f *fakeTTSProvider) Name() string { return "fake-tts" }

func (f *fakeTTSProvider) Synthesize(_ context.Context, _ string, opts tts.SynthesizeOptions) (*tts.Synthesis, error) {
	f.lastSynthOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	return &tts.Synthesis{Audio: f.audio, Format: opts.Format}, nil
}

func (f *fakeTTSProvider) SynthesizeStream(context.Context, string, tts.SynthesizeOptions) (*tts.SynthesisStream, error) {
	return nil, nil
}

func (f *fakeTTSProvider) NewStreamingContext(_ context.Context, opts tts.StreamingContextOptions) (*tts.StreamingContext, error) {
	f.lastStreamOpts = opts
	if f.streamingCtx != nil {
		return f.streamingCtx, nil
	}
	return tts.NewStreamingContext(), nil
}

func TestProcessAudioSTT_ReplacesAudioSTTBlocksAndPreservesOtherBlocks(t *testing.T) {
	pipeline := NewPipelineWithProviders(
		&fakeSTTProvider{transcripts: []string{"first transcript"}},
		&fakeTTSProvider{},
	)

	msg := types.Message{
		Role: "user",
		Content: []types.ContentBlock{
			types.TextBlock{Type: "text", Text: "before"},
			types.AudioSTTBlock{
				Type: "audio_stt",
				Source: types.AudioSource{
					Type:      "base64",
					MediaType: "audio/wav",
					Data:      base64.StdEncoding.EncodeToString([]byte("audio-bytes")),
				},
			},
			types.AudioBlock{
				Type: "audio",
				Source: types.AudioSource{
					Type:      "base64",
					MediaType: "audio/wav",
					Data:      base64.StdEncoding.EncodeToString([]byte("raw-audio")),
				},
			},
			types.TextBlock{Type: "text", Text: "after"},
		},
	}

	processed, transcript, err := pipeline.ProcessAudioSTT(context.Background(), []types.Message{msg}, "ink-whisper")
	if err != nil {
		t.Fatalf("ProcessAudioSTT() error = %v", err)
	}
	if transcript != "first transcript" {
		t.Fatalf("transcript = %q, want %q", transcript, "first transcript")
	}
	if len(processed) != 1 {
		t.Fatalf("len(processed) = %d, want 1", len(processed))
	}

	blocks := processed[0].ContentBlocks()
	if len(blocks) != 4 {
		t.Fatalf("len(blocks) = %d, want 4", len(blocks))
	}
	if tb, ok := blocks[1].(types.TextBlock); !ok || tb.Text != "first transcript" {
		t.Fatalf("audio_stt block was not replaced with transcript text, got %#v", blocks[1])
	}
	if _, ok := blocks[2].(types.AudioBlock); !ok {
		t.Fatalf("raw audio block should be preserved, got %T", blocks[2])
	}
}

func TestProcessAudioSTT_AggregatesMultipleTranscripts(t *testing.T) {
	pipeline := NewPipelineWithProviders(
		&fakeSTTProvider{transcripts: []string{"one", "two"}},
		&fakeTTSProvider{},
	)

	audioBlock := types.AudioSTTBlock{
		Type: "audio_stt",
		Source: types.AudioSource{
			Type:      "base64",
			MediaType: "audio/wav",
			Data:      base64.StdEncoding.EncodeToString([]byte("bytes")),
		},
	}

	processed, transcript, err := pipeline.ProcessAudioSTT(context.Background(), []types.Message{{
		Role:    "user",
		Content: []types.ContentBlock{audioBlock, audioBlock},
	}}, "ink-whisper")
	if err != nil {
		t.Fatalf("ProcessAudioSTT() error = %v", err)
	}
	if transcript != "one\ntwo" {
		t.Fatalf("transcript = %q, want %q", transcript, "one\\ntwo")
	}
	if got := len(processed[0].ContentBlocks()); got != 2 {
		t.Fatalf("len(processed blocks) = %d, want 2", got)
	}
}

func TestProcessAudioSTT_PropagatesSTTError(t *testing.T) {
	wantErr := errors.New("stt failed")
	pipeline := NewPipelineWithProviders(
		&fakeSTTProvider{err: wantErr},
		&fakeTTSProvider{},
	)

	_, _, err := pipeline.ProcessAudioSTT(context.Background(), []types.Message{{
		Role: "user",
		Content: []types.ContentBlock{types.AudioSTTBlock{
			Type: "audio_stt",
			Source: types.AudioSource{
				Type:      "base64",
				MediaType: "audio/wav",
				Data:      base64.StdEncoding.EncodeToString([]byte("audio")),
			},
		}},
	}}, "ink-whisper")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestSynthesizeResponse_UsesConfiguredOutputOptions(t *testing.T) {
	fakeTTS := &fakeTTSProvider{audio: []byte("audio-data")}
	pipeline := NewPipelineWithProviders(&fakeSTTProvider{}, fakeTTS)

	cfg := &types.VoiceConfig{Output: &types.VoiceOutputConfig{
		Voice:      "voice-id",
		Speed:      1.2,
		Volume:     0.9,
		Emotion:    types.EmotionCalm,
		Format:     types.VoiceFormatMP3,
		SampleRate: 22050,
	}}

	audio, err := pipeline.SynthesizeResponse(context.Background(), "hello", cfg, "sonic-3")
	if err != nil {
		t.Fatalf("SynthesizeResponse() error = %v", err)
	}
	if string(audio) != "audio-data" {
		t.Fatalf("audio = %q, want %q", string(audio), "audio-data")
	}

	if fakeTTS.lastSynthOpts.Model != "sonic-3" || fakeTTS.lastSynthOpts.Voice != "voice-id" || fakeTTS.lastSynthOpts.Format != types.VoiceFormatMP3 {
		t.Fatalf("unexpected synth opts: %#v", fakeTTS.lastSynthOpts)
	}
}

func TestNewStreamingTTSContext_PassesOptions(t *testing.T) {
	fakeCtx := tts.NewStreamingContext()
	fakeTTS := &fakeTTSProvider{streamingCtx: fakeCtx}
	pipeline := NewPipelineWithProviders(&fakeSTTProvider{}, fakeTTS)

	cfg := &types.VoiceConfig{Output: &types.VoiceOutputConfig{
		Voice:   "voice-id",
		Speed:   1.1,
		Volume:  1.0,
		Emotion: types.EmotionHappy,
		Format:  types.VoiceFormatWAV,
	}}

	ctx, err := pipeline.NewStreamingTTSContext(context.Background(), cfg, "sonic-3")
	if err != nil {
		t.Fatalf("NewStreamingTTSContext() error = %v", err)
	}
	if !reflect.DeepEqual(ctx, fakeCtx) {
		t.Fatalf("returned unexpected streaming context")
	}
	if fakeTTS.lastStreamOpts.Model != "sonic-3" || fakeTTS.lastStreamOpts.Voice != "voice-id" || fakeTTS.lastStreamOpts.Emotion != types.EmotionHappy {
		t.Fatalf("unexpected streaming opts: %#v", fakeTTS.lastStreamOpts)
	}
}

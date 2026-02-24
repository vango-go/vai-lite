package voice

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// PreprocessMessageRequestInputAudio preprocesses req for voice input (STT).
// If req.Voice.Input is set, it transcribes any audio blocks into text blocks and returns a copy of req.
// It never mutates the caller's req in-place.
func PreprocessMessageRequestInputAudio(ctx context.Context, pipeline *Pipeline, req *types.MessageRequest) (*types.MessageRequest, string, error) {
	if req == nil {
		return nil, "", fmt.Errorf("req must not be nil")
	}
	if req.Voice == nil || req.Voice.Input == nil {
		return req, "", nil
	}
	if pipeline == nil {
		return nil, "", fmt.Errorf("voice pipeline is not configured")
	}

	processed, transcript, err := pipeline.ProcessInputAudio(ctx, req.Messages, req.Voice)
	if err != nil {
		return nil, "", err
	}

	reqCopy := *req
	reqCopy.Messages = processed
	return &reqCopy, transcript, nil
}

// AppendVoiceOutputToMessageResponse appends synthesized voice output (TTS) to resp when cfg.Output is set.
// It mutates resp by appending an audio content block when synthesis succeeds.
func AppendVoiceOutputToMessageResponse(ctx context.Context, pipeline *Pipeline, cfg *types.VoiceConfig, resp *types.MessageResponse) error {
	if pipeline == nil {
		return fmt.Errorf("voice pipeline is not configured")
	}
	if cfg == nil || cfg.Output == nil || resp == nil {
		return nil
	}

	text := strings.TrimSpace(resp.TextContent())
	if text == "" {
		return nil
	}

	audioData, err := pipeline.SynthesizeResponse(ctx, text, cfg)
	if err != nil {
		return err
	}
	if len(audioData) == 0 {
		return nil
	}

	transcript := text
	resp.Content = append(resp.Content, types.AudioBlock{
		Type: "audio",
		Source: types.AudioSource{
			Type:      "base64",
			MediaType: mediaTypeForAudioFormat(cfg.Output.Format),
			Data:      base64.StdEncoding.EncodeToString(audioData),
		},
		Transcript: &transcript,
	})
	return nil
}

func mediaTypeForAudioFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3":
		return "audio/mpeg"
	case "pcm", "raw":
		return "audio/pcm"
	default:
		return "audio/wav"
	}
}

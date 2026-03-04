package voice

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// PreprocessMessageRequestAudioSTT preprocesses req for audio_stt input blocks.
// It transcribes audio_stt blocks into text blocks and returns a copied request.
// It never mutates the caller's req in-place.
func PreprocessMessageRequestAudioSTT(ctx context.Context, pipeline *Pipeline, req *types.MessageRequest) (*types.MessageRequest, string, error) {
	if req == nil {
		return nil, "", fmt.Errorf("req must not be nil")
	}
	if !types.RequestHasAudioSTT(req) {
		return req, "", nil
	}
	if pipeline == nil {
		return nil, "", fmt.Errorf("voice pipeline is not configured")
	}

	resolvedSTTModel, err := ResolveSTTModel(req.STTModel)
	if err != nil {
		return nil, "", err
	}

	processed, transcript, err := pipeline.ProcessAudioSTT(ctx, req.Messages, resolvedSTTModel.Model)
	if err != nil {
		return nil, "", err
	}

	reqCopy := *req
	reqCopy.Messages = processed
	return &reqCopy, transcript, nil
}

// PreprocessMessageRequestInputAudio is a compatibility alias for PreprocessMessageRequestAudioSTT.
func PreprocessMessageRequestInputAudio(ctx context.Context, pipeline *Pipeline, req *types.MessageRequest) (*types.MessageRequest, string, error) {
	return PreprocessMessageRequestAudioSTT(ctx, pipeline, req)
}

// AppendVoiceOutputToMessageResponse appends synthesized voice output (TTS) to resp when cfg.Output is set.
// It mutates resp by appending an audio content block when synthesis succeeds.
func AppendVoiceOutputToMessageResponse(
	ctx context.Context,
	pipeline *Pipeline,
	cfg *types.VoiceConfig,
	ttsModelRaw string,
	resp *types.MessageResponse,
) error {
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

	resolvedTTSModel, err := ResolveTTSModel(ttsModelRaw)
	if err != nil {
		return err
	}

	audioData, err := pipeline.SynthesizeResponse(ctx, text, cfg, resolvedTTSModel.Model)
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

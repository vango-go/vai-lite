package voice

import (
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
)

const (
	// DefaultSTTModel is the default STT model id in provider/model format.
	DefaultSTTModel = "cartesia/ink-whisper"
	// DefaultTTSModel is the default TTS model id in provider/model format.
	DefaultTTSModel = "cartesia/sonic-3"
)

// ResolvedModel identifies a voice model after defaults and parsing.
type ResolvedModel struct {
	Raw      string
	Provider string
	Model    string
}

func ResolveSTTModel(raw string) (ResolvedModel, error) {
	ref, err := parseVoiceModel(raw, DefaultSTTModel, "stt_model")
	if err != nil {
		return ResolvedModel{}, err
	}
	if ref.Provider != "cartesia" {
		return ResolvedModel{}, core.NewInvalidRequestErrorWithParam(
			fmt.Sprintf("unsupported STT model provider %q; only cartesia/* is currently supported", ref.Provider),
			"stt_model",
		)
	}
	return ref, nil
}

func ResolveTTSModel(raw string) (ResolvedModel, error) {
	ref, err := parseVoiceModel(raw, DefaultTTSModel, "tts_model")
	if err != nil {
		return ResolvedModel{}, err
	}
	if ref.Provider != "cartesia" {
		return ResolvedModel{}, core.NewInvalidRequestErrorWithParam(
			fmt.Sprintf("unsupported TTS model provider %q; only cartesia/* is currently supported", ref.Provider),
			"tts_model",
		)
	}
	return ref, nil
}

func parseVoiceModel(raw, defaultModel, param string) (ResolvedModel, error) {
	modelID := strings.TrimSpace(raw)
	if modelID == "" {
		modelID = defaultModel
	}
	parts := strings.Split(modelID, "/")
	if len(parts) != 2 {
		return ResolvedModel{}, core.NewInvalidRequestErrorWithParam(param+" must be in provider/model format", param)
	}
	provider := strings.ToLower(strings.TrimSpace(parts[0]))
	model := strings.TrimSpace(parts[1])
	if provider == "" || model == "" {
		return ResolvedModel{}, core.NewInvalidRequestErrorWithParam(param+" must be in provider/model format", param)
	}
	return ResolvedModel{
		Raw:      provider + "/" + model,
		Provider: provider,
		Model:    model,
	}, nil
}

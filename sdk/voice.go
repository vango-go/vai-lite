package vai

import "github.com/vango-go/vai-lite/pkg/core/types"

// VoiceOutput creates a VoiceConfig with output settings for TTS.
func VoiceOutput(voice string, opts ...VoiceOutputOption) *VoiceConfig {
	cfg := &VoiceConfig{
		Output: &types.VoiceOutputConfig{
			Voice:  voice,
			Speed:  1.0,
			Volume: 1.0,
			Format: "wav",
		},
	}
	for _, opt := range opts {
		opt(cfg.Output)
	}
	return cfg
}

// VoiceOutputOption configures voice output.
type VoiceOutputOption func(*types.VoiceOutputConfig)

// WithSpeed sets the TTS speed (0.6-1.5).
func WithSpeed(speed float64) VoiceOutputOption {
	return func(c *types.VoiceOutputConfig) {
		c.Speed = speed
	}
}

// WithVolume sets the TTS volume (0.5-2.0).
func WithVolume(volume float64) VoiceOutputOption {
	return func(c *types.VoiceOutputConfig) {
		c.Volume = volume
	}
}

// WithEmotion sets the TTS emotion.
func WithEmotion(emotion string) VoiceOutputOption {
	return func(c *types.VoiceOutputConfig) {
		c.Emotion = emotion
	}
}

// WithAudioFormat sets the output audio format (wav, mp3, pcm).
func WithAudioFormat(format string) VoiceOutputOption {
	return func(c *types.VoiceOutputConfig) {
		c.Format = format
	}
}

// Emotion constants for convenience.
const (
	EmotionNeutral      = types.EmotionNeutral
	EmotionHappy        = types.EmotionHappy
	EmotionExcited      = types.EmotionExcited
	EmotionEnthusiastic = types.EmotionEnthusiastic
	EmotionCalm         = types.EmotionCalm
	EmotionPeaceful     = types.EmotionPeaceful
	EmotionAngry        = types.EmotionAngry
	EmotionFrustrated   = types.EmotionFrustrated
	EmotionSad          = types.EmotionSad
	EmotionMelancholic  = types.EmotionMelancholic
	EmotionScared       = types.EmotionScared
	EmotionAnxious      = types.EmotionAnxious
	EmotionConfident    = types.EmotionConfident
	EmotionCurious      = types.EmotionCurious
)

// Audio format constants.
const (
	AudioFormatWAV = types.VoiceFormatWAV
	AudioFormatMP3 = types.VoiceFormatMP3
	AudioFormatPCM = types.VoiceFormatPCM
)

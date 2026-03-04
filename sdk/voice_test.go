package vai

import "testing"

func TestVoiceHelpers(t *testing.T) {
	out := VoiceOutput("voice-id", WithSpeed(1.2), WithVolume(0.8), WithEmotion(EmotionCalm), WithAudioFormat(AudioFormatMP3))
	if out == nil || out.Output == nil {
		t.Fatalf("VoiceOutput returned nil")
	}
	if out.Output.Voice != "voice-id" || out.Output.Format != AudioFormatMP3 {
		t.Fatalf("unexpected output config: %#v", out.Output)
	}
}

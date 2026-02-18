package vai

import "testing"

func TestVoiceHelpers(t *testing.T) {
	in := VoiceInput(WithLanguage("es"), WithSTTModel("ink-whisper"))
	if in == nil || in.Input == nil {
		t.Fatalf("VoiceInput returned nil")
	}
	if in.Input.Language != "es" {
		t.Fatalf("language = %q, want es", in.Input.Language)
	}

	out := VoiceOutput("voice-id", WithSpeed(1.2), WithVolume(0.8), WithEmotion(EmotionCalm), WithAudioFormat(AudioFormatMP3))
	if out == nil || out.Output == nil {
		t.Fatalf("VoiceOutput returned nil")
	}
	if out.Output.Voice != "voice-id" || out.Output.Format != AudioFormatMP3 {
		t.Fatalf("unexpected output config: %#v", out.Output)
	}

	full := VoiceFull("voice-id", WithLanguage("en"), WithAudioFormat(AudioFormatWAV))
	if full == nil || full.Input == nil || full.Output == nil {
		t.Fatalf("VoiceFull returned incomplete config")
	}
}

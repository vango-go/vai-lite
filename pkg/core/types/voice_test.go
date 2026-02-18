package types

import "testing"

func TestVoiceConstants(t *testing.T) {
	if VoiceFormatWAV == "" || VoiceFormatMP3 == "" || VoiceFormatPCM == "" {
		t.Fatalf("voice format constants must not be empty")
	}
	if EmotionNeutral == "" || EmotionHappy == "" || EmotionCurious == "" {
		t.Fatalf("emotion constants must not be empty")
	}
}

func TestVoiceSupportedLanguageLists(t *testing.T) {
	if len(SupportedSTTLanguages) == 0 {
		t.Fatalf("SupportedSTTLanguages should not be empty")
	}
	if len(SupportedTTSLanguages) == 0 {
		t.Fatalf("SupportedTTSLanguages should not be empty")
	}
}

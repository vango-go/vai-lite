package session

import (
	"context"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

func TestIsConfirmedSpeech(t *testing.T) {
	if IsConfirmedSpeech("...", true, false, true) {
		t.Fatalf("punctuation-only should not confirm speech")
	}
	if !IsConfirmedSpeech("hello", true, false, true) {
		t.Fatalf("expected normal speech to confirm")
	}
	if IsConfirmedSpeech("short", false, true, false) {
		t.Fatalf("assistant-speaking/no-aec should require stricter threshold")
	}
	if !IsConfirmedSpeech("this is a long enough utterance", false, true, false) {
		t.Fatalf("long utterance should confirm speech")
	}
}

func TestIsMeaningfulTranscript(t *testing.T) {
	if isMeaningfulTranscript("hello", "hello") {
		t.Fatalf("same transcript should not be meaningful")
	}
	if !isMeaningfulTranscript("hello world", "hello") {
		t.Fatalf("updated transcript should be meaningful")
	}
}

func TestLiveSession_HandleBackpressure_CancelsAndEnqueuesReset(t *testing.T) {
	s := &LiveSession{
		outboundPriority: make(chan outboundFrame, 1),
		outboundNormal:   make(chan outboundFrame, 1),
	}
	s.canceledAssistant.Store(canceledAssistantState{set: make(map[string]struct{}), order: nil})

	// Fill the priority queue so handleBackpressure must evict to enqueue the latest reset.
	s.outboundPriority <- outboundFrame{textPayload: []byte(`{"type":"audio_reset","reason":"old","assistant_audio_id":"a_old"}`)}

	var ttsCanceled, runCanceled bool
	var ttsCancel context.CancelFunc = func() { ttsCanceled = true }
	var runCancel context.CancelFunc = func() { runCanceled = true }

	err := s.handleBackpressure("a_1", &ttsCancel, &runCancel)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != errBackpressure {
		t.Fatalf("err=%v, want errBackpressure", err)
	}
	if !ttsCanceled || !runCanceled {
		t.Fatalf("expected cancels tts=%v run=%v", ttsCanceled, runCanceled)
	}
	if !s.isAssistantCanceled("a_1") {
		t.Fatalf("expected assistant audio to be marked canceled")
	}

	select {
	case frame := <-s.outboundPriority:
		if !strings.Contains(string(frame.textPayload), `"type":"audio_reset"`) {
			t.Fatalf("expected audio_reset frame, got %q", string(frame.textPayload))
		}
		if !strings.Contains(string(frame.textPayload), `"reason":"backpressure"`) {
			t.Fatalf("expected backpressure reason, got %q", string(frame.textPayload))
		}
		if !strings.Contains(string(frame.textPayload), `"assistant_audio_id":"a_1"`) {
			t.Fatalf("expected assistant_audio_id a_1, got %q", string(frame.textPayload))
		}
	default:
		t.Fatalf("expected a priority frame enqueued")
	}
}

func TestSTTLanguageFromHello_DefaultsToEnWhenMissing(t *testing.T) {
	if got := sttLanguageFromHello(protocol.ClientHello{}); got != "en" {
		t.Fatalf("lang=%q, want en", got)
	}
	if got := sttLanguageFromHello(protocol.ClientHello{Voice: &protocol.HelloVoice{Language: "   "}}); got != "en" {
		t.Fatalf("lang=%q, want en", got)
	}
}

func TestSTTLanguageFromHello_UsesHelloVoiceLanguage(t *testing.T) {
	got := sttLanguageFromHello(protocol.ClientHello{Voice: &protocol.HelloVoice{Language: "es"}})
	if got != "es" {
		t.Fatalf("lang=%q, want es", got)
	}
	got = sttLanguageFromHello(protocol.ClientHello{Voice: &protocol.HelloVoice{Language: "  fr-FR  "}})
	if got != "fr-FR" {
		t.Fatalf("lang=%q, want fr-FR", got)
	}
}

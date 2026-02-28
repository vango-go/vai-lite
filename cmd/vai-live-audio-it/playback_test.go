package main

import (
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

func TestPlaybackMarkMath_PlayedAndBufferedMS(t *testing.T) {
	// 24kHz mono PCM16 => 24000 samples/s * 2 bytes = 48000 bytes/s.
	sampleRate := 24000
	channels := 1
	bytesPerSample := 2

	if got := playedMSFromBytes(0, sampleRate, channels, bytesPerSample); got != 0 {
		t.Fatalf("playedMSFromBytes(0) = %d, want 0", got)
	}
	if got := bufferedMSFromBytes(0, sampleRate, channels, bytesPerSample); got != 0 {
		t.Fatalf("bufferedMSFromBytes(0) = %d, want 0", got)
	}

	// 20ms of audio => 0.02s * 48000 = 960 bytes.
	if got := playedMSFromBytes(960, sampleRate, channels, bytesPerSample); got != 20 {
		t.Fatalf("playedMSFromBytes(960) = %d, want 20", got)
	}
	if got := bufferedMSFromBytes(960, sampleRate, channels, bytesPerSample); got != 20 {
		t.Fatalf("bufferedMSFromBytes(960) = %d, want 20", got)
	}

	// 1 second => 48000 bytes.
	if got := playedMSFromBytes(48000, sampleRate, channels, bytesPerSample); got != 1000 {
		t.Fatalf("playedMSFromBytes(48000) = %d, want 1000", got)
	}
	if got := bufferedMSFromBytes(48000, sampleRate, channels, bytesPerSample); got != 1000 {
		t.Fatalf("bufferedMSFromBytes(48000) = %d, want 1000", got)
	}
}

func TestPlaybackManager_AudioResetClearsStateAndEmitsStopped(t *testing.T) {
	pm := newPlaybackManager(playbackConfig{
		sampleRateHz:   24000,
		channels:       1,
		bytesPerSample: 2,
		noSpeaker:      true,
	})
	defer pm.Close()

	var gotStates []string
	pm.SetMarkSender(func(mark protocol.ClientPlaybackMark) {
		gotStates = append(gotStates, mark.State)
	})

	pm.AssistantAudioStart("a_1")
	pm.AssistantAudioChunk("a_1", make([]byte, 960))
	pm.AudioReset("a_1")

	if len(gotStates) == 0 || gotStates[len(gotStates)-1] != "stopped" {
		t.Fatalf("expected last mark state to be stopped, got %#v", gotStates)
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.activeAssistantID != "" {
		t.Fatalf("activeAssistantID = %q, want empty", pm.activeAssistantID)
	}
	if pm.buffer.Len() != 0 {
		t.Fatalf("buffer len = %d, want 0", pm.buffer.Len())
	}
	if pm.playedBytes != 0 {
		t.Fatalf("playedBytes = %d, want 0", pm.playedBytes)
	}
}

func TestPlaybackManager_AssistantAudioEndEmitsFinishedWhenDrained(t *testing.T) {
	pm := newPlaybackManager(playbackConfig{
		sampleRateHz:   24000,
		channels:       1,
		bytesPerSample: 2,
		noSpeaker:      true,
		tick:           5 * time.Millisecond,
		markInterval:   5 * time.Millisecond,
	})
	defer pm.Close()

	done := make(chan struct{})
	pm.SetMarkSender(func(mark protocol.ClientPlaybackMark) {
		if mark.State == "finished" {
			close(done)
		}
	})

	pm.AssistantAudioStart("a_1")
	pm.AssistantAudioChunk("a_1", make([]byte, 960)) // 20ms
	pm.AssistantAudioEnd("a_1")

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for finished mark")
	}
}

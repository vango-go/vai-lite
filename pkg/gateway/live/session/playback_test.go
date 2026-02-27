package session

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

func TestSpeechSegment_PlayedPrefixWithAlignment(t *testing.T) {
	seg := newSpeechSegment("a_1", "hello world")
	seg.addChunk(make([]byte, 24000/10*2), &protocol.Alignment{
		Kind:        protocol.AlignmentKindChar,
		Normalized:  true,
		Chars:       []string{"h", "e", "l", "l", "o"},
		CharStartMS: []int{0, 20, 40, 60, 80},
		CharDurMS:   []int{20, 20, 20, 20, 20},
	}, 24000)
	seg.updateMark(protocol.ClientPlaybackMark{
		AssistantAudioID: "a_1",
		PlayedMS:         60,
		State:            "playing",
	})

	got := seg.playedPrefix(24000)
	if got != "hel" {
		t.Fatalf("playedPrefix=%q, want %q", got, "hel")
	}
}

func TestSpeechSegment_PlayedPrefixWithoutAlignment(t *testing.T) {
	seg := newSpeechSegment("a_1", "hello world")
	seg.addChunk(make([]byte, 24000/10*2), nil, 24000)

	seg.updateMark(protocol.ClientPlaybackMark{
		AssistantAudioID: "a_1",
		PlayedMS:         100,
		State:            "finished",
	})
	if got := seg.playedPrefix(24000); got != "hello world" {
		t.Fatalf("finished playedPrefix=%q", got)
	}

	seg2 := newSpeechSegment("a_2", "hello world")
	seg2.addChunk(make([]byte, 24000/10*2), nil, 24000)
	seg2.updateMark(protocol.ClientPlaybackMark{
		AssistantAudioID: "a_2",
		PlayedMS:         20,
		State:            "playing",
	})
	if got := seg2.playedPrefix(24000); got != "" {
		t.Fatalf("playing without alignment should be conservative empty, got=%q", got)
	}
}

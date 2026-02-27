package session

import (
	"strings"
	"sync"

	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

type alignedChar struct {
	ch      string
	startMS int64
	durMS   int64
}

type speechSegment struct {
	id       string
	fullText string

	mu         sync.Mutex
	sentSample int64
	lastMark   protocol.ClientPlaybackMark
	hasMark    bool
	chars      []alignedChar
}

func newSpeechSegment(id, fullText string) *speechSegment {
	return &speechSegment{
		id:       strings.TrimSpace(id),
		fullText: strings.TrimSpace(fullText),
		chars:    make([]alignedChar, 0, 128),
	}
}

func (s *speechSegment) addChunk(audio []byte, alignment *protocol.Alignment, sampleRate int) {
	if s == nil {
		return
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sentMSBefore := (s.sentSample * 1000) / int64(sampleRate)
	sentSamples := int64(len(audio) / 2)
	if sentSamples > 0 {
		s.sentSample += sentSamples
	}

	if alignment == nil || len(alignment.Chars) == 0 {
		return
	}
	if len(alignment.Chars) != len(alignment.CharStartMS) || len(alignment.Chars) != len(alignment.CharDurMS) {
		return
	}
	for i := range alignment.Chars {
		start := sentMSBefore + int64(alignment.CharStartMS[i])
		dur := int64(alignment.CharDurMS[i])
		if dur < 0 {
			dur = 0
		}
		s.chars = append(s.chars, alignedChar{
			ch:      alignment.Chars[i],
			startMS: start,
			durMS:   dur,
		})
	}
}

func (s *speechSegment) updateMark(mark protocol.ClientPlaybackMark) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastMark = mark
	s.hasMark = true
}

func (s *speechSegment) sentMS(sampleRate int) int64 {
	if s == nil {
		return 0
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return (s.sentSample * 1000) / int64(sampleRate)
}

func (s *speechSegment) unplayedMS(sampleRate int) int64 {
	if s == nil {
		return 0
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sentMS := (s.sentSample * 1000) / int64(sampleRate)
	playedMS := int64(0)
	if s.hasMark {
		playedMS = s.lastMark.PlayedMS
	}
	if playedMS < 0 {
		playedMS = 0
	}
	if playedMS > sentMS {
		playedMS = sentMS
	}
	return sentMS - playedMS
}

func (s *speechSegment) shouldFinalizeFromMark() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasMark {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(s.lastMark.State))
	return state == "stopped" || state == "finished"
}

func (s *speechSegment) playedPrefix(sampleRate int) string {
	if s == nil {
		return ""
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sentMS := (s.sentSample * 1000) / int64(sampleRate)
	playedMS := int64(0)
	state := ""
	if s.hasMark {
		playedMS = s.lastMark.PlayedMS
		state = strings.ToLower(strings.TrimSpace(s.lastMark.State))
	}
	if playedMS < 0 {
		playedMS = 0
	}
	if playedMS > sentMS {
		playedMS = sentMS
	}
	if len(s.chars) > 0 {
		lastIdx := -1
		for i, c := range s.chars {
			if c.startMS+c.durMS <= playedMS {
				lastIdx = i
				continue
			}
			break
		}
		if lastIdx >= 0 {
			var b strings.Builder
			for i := 0; i <= lastIdx; i++ {
				b.WriteString(s.chars[i].ch)
			}
			return normalizeSpace(b.String())
		}
	}
	if state == "finished" || playedMS >= sentMS {
		return normalizeSpace(s.fullText)
	}
	return ""
}

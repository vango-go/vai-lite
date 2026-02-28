package session

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type TextChunkConfig struct {
	SentenceMinChars   int
	MaxChunkChars      int
	MinChunkChars      int
	FirstChunkMinChars int
	MaxHold            time.Duration
}

func DefaultTextChunkConfig() TextChunkConfig {
	return TextChunkConfig{
		SentenceMinChars:   12,
		MaxChunkChars:      120,
		MinChunkChars:      24,
		FirstChunkMinChars: 8,
		MaxHold:            180 * time.Millisecond,
	}
}

// TextChunker turns an append-only text stream into sentence-ish TTS chunks.
// It is deterministic given a tick channel; pass nil to use a real ticker.
type TextChunker struct {
	cfg  TextChunkConfig
	tick <-chan time.Time

	buf     strings.Builder
	sentAny bool
}

func NewTextChunker(cfg TextChunkConfig, tick <-chan time.Time) *TextChunker {
	if cfg.SentenceMinChars <= 0 {
		cfg.SentenceMinChars = 12
	}
	if cfg.MaxChunkChars <= 0 {
		cfg.MaxChunkChars = 120
	}
	if cfg.MinChunkChars <= 0 {
		cfg.MinChunkChars = 24
	}
	if cfg.FirstChunkMinChars <= 0 {
		cfg.FirstChunkMinChars = 8
	}
	if cfg.MaxHold <= 0 {
		cfg.MaxHold = 180 * time.Millisecond
	}
	return &TextChunker{cfg: cfg, tick: tick}
}

func (c *TextChunker) Run(ctx context.Context, in <-chan string, out chan<- string) {
	if out == nil {
		return
	}
	defer close(out)

	tick := c.tick
	var ticker *time.Ticker
	if tick == nil {
		ticker = time.NewTicker(c.cfg.MaxHold)
		defer ticker.Stop()
		tick = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-in:
			if !ok {
				c.flushAll(out)
				return
			}
			if s != "" {
				c.buf.WriteString(s)
			}
			c.emitReady(false, out)
		case <-tick:
			c.emitReady(true, out)
		}
	}
}

func (c *TextChunker) emitReady(fromTick bool, out chan<- string) {
	for {
		buf := c.buf.String()
		if buf == "" {
			return
		}
		n := runeLen(buf)

		// Sentence boundary rule (only once we have enough buffered overall).
		if n >= c.cfg.SentenceMinChars {
			if idx := firstSentenceBoundaryCut(buf, c.cfg.MaxChunkChars); idx > 0 {
				c.emitCut(idx, out)
				continue
			}
		}

		// First-chunk early start: split on whitespace/boundary as soon as we have enough.
		if !c.sentAny && n >= c.cfg.FirstChunkMinChars {
			if idx := firstWhitespaceOrBoundaryAtOrAfter(buf, c.cfg.FirstChunkMinChars, c.cfg.MaxChunkChars); idx > 0 {
				c.emitCut(idx, out)
				continue
			}
		}

		// Hard size cap.
		if n > c.cfg.MaxChunkChars {
			if idx := bestCutAtOrBefore(buf, c.cfg.MaxChunkChars); idx > 0 {
				c.emitCut(idx, out)
				continue
			}
		}

		// Timer-driven emission.
		if fromTick && n >= c.cfg.MinChunkChars {
			if idx := bestCutAtOrBefore(buf, minInt(c.cfg.MaxChunkChars, n)); idx > 0 {
				c.emitCut(idx, out)
				continue
			}
		}

		return
	}
}

func (c *TextChunker) emitCut(cut int, out chan<- string) {
	buf := c.buf.String()
	if cut <= 0 || cut > len(buf) {
		return
	}
	chunk := buf[:cut]
	if strings.TrimSpace(chunk) == "" {
		return
	}
	rest := buf[cut:]
	c.buf.Reset()
	c.buf.WriteString(rest)
	c.sentAny = true
	out <- chunk
}

func (c *TextChunker) flushAll(out chan<- string) {
	for {
		buf := c.buf.String()
		if strings.TrimSpace(buf) == "" {
			return
		}
		n := runeLen(buf)
		if n <= c.cfg.MaxChunkChars {
			c.emitCut(len(buf), out)
			return
		}
		if idx := bestCutAtOrBefore(buf, c.cfg.MaxChunkChars); idx > 0 {
			c.emitCut(idx, out)
			continue
		}
		// Fallback: split at max chars even if mid-word (should be rare).
		cut := cutByteIndexAtRuneCount(buf, c.cfg.MaxChunkChars)
		if cut <= 0 {
			return
		}
		c.emitCut(cut, out)
	}
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

func cutByteIndexAtRuneCount(s string, runes int) int {
	if runes <= 0 {
		return 0
	}
	i := 0
	for r := 0; r < runes && i < len(s); r++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			return i
		}
		i += size
	}
	return i
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isSentenceBoundary(r rune) bool {
	return r == '.' || r == '?' || r == '!' || r == '\n'
}

func firstSentenceBoundaryCut(s string, maxChars int) int {
	// Find the earliest sentence boundary (plus any immediately following whitespace),
	// but do not exceed maxChars runes.
	if strings.TrimSpace(s) == "" {
		return 0
	}
	runes := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			return 0
		}
		runes++
		if runes > maxChars {
			return 0
		}
		if isSentenceBoundary(r) {
			j := i + size
			// Include trailing spaces/newlines after the boundary.
			for j < len(s) {
				r2, sz2 := utf8.DecodeRuneInString(s[j:])
				if sz2 <= 0 || !unicode.IsSpace(r2) {
					break
				}
				j += sz2
			}
			return j
		}
		i += size
	}
	return 0
}

func firstWhitespaceOrBoundaryAtOrAfter(s string, minChars int, maxChars int) int {
	if minChars <= 0 {
		minChars = 1
	}
	runes := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			return 0
		}
		runes++
		if runes > maxChars {
			return 0
		}
		if runes >= minChars && (unicode.IsSpace(r) || isSentenceBoundary(r)) {
			return i + size
		}
		i += size
	}
	return 0
}

func bestCutAtOrBefore(s string, maxChars int) int {
	if maxChars <= 0 {
		return 0
	}
	runes := 0
	lastSpaceCut := 0
	lastBoundaryCut := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			break
		}
		runes++
		if runes > maxChars {
			break
		}
		if isSentenceBoundary(r) {
			lastBoundaryCut = i + size
		}
		if unicode.IsSpace(r) {
			lastSpaceCut = i + size
		}
		i += size
	}
	if lastBoundaryCut > 0 {
		return lastBoundaryCut
	}
	if lastSpaceCut > 0 {
		return lastSpaceCut
	}
	return cutByteIndexAtRuneCount(s, maxChars)
}

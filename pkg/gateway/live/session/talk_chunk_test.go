package session

import (
	"context"
	"testing"
	"time"
)

func collectChunks(t *testing.T, c *TextChunker, inputs []string, ticks int) []string {
	t.Helper()
	in := make(chan string, len(inputs))
	out := make(chan string, 16)
	tick := make(chan time.Time, 16)
	c.tick = tick
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, in, out)
	for _, s := range inputs {
		in <- s
	}
	for i := 0; i < ticks; i++ {
		tick <- time.Now()
	}
	close(in)
	var got []string
	for ch := range out {
		got = append(got, ch)
	}
	return got
}

func TestTextChunker_SentenceBoundary(t *testing.T) {
	c := NewTextChunker(DefaultTextChunkConfig(), nil)
	got := collectChunks(t, c, []string{"Hello world. How are you?"}, 0)
	if len(got) < 2 {
		t.Fatalf("chunks=%v, want >=2", got)
	}
	if got[0] != "Hello world. " {
		t.Fatalf("first=%q, want %q", got[0], "Hello world. ")
	}
}

func TestTextChunker_HardSplitAtMax(t *testing.T) {
	cfg := DefaultTextChunkConfig()
	cfg.MaxChunkChars = 10
	cfg.FirstChunkMinChars = 2
	c := NewTextChunker(cfg, nil)
	got := collectChunks(t, c, []string{"one two three four five"}, 0)
	if len(got) < 2 {
		t.Fatalf("chunks=%v, want >=2", got)
	}
}

func TestTextChunker_TimerEmission(t *testing.T) {
	cfg := DefaultTextChunkConfig()
	cfg.MinChunkChars = 5
	cfg.FirstChunkMinChars = 100
	cfg.SentenceMinChars = 100
	c := NewTextChunker(cfg, nil)
	got := collectChunks(t, c, []string{"hello there"}, 1)
	if len(got) == 0 {
		t.Fatalf("expected at least one chunk on tick")
	}
}

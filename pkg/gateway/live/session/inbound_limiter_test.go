package session

import (
	"testing"
	"time"
)

func TestInboundLimiter_AllowsWithinBurstThenDenies(t *testing.T) {
	now := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	lim := newInboundAudioLimiter(clock, 1, 0, 2) // 2 frame burst
	if !lim.Allow(10) {
		t.Fatalf("expected allow 1")
	}
	if !lim.Allow(10) {
		t.Fatalf("expected allow 2")
	}
	if lim.Allow(10) {
		t.Fatalf("expected deny 3")
	}
}

func TestInboundLimiter_RefillsOverTime(t *testing.T) {
	now := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	lim := newInboundAudioLimiter(clock, 10, 0, 2) // 20 frame burst
	for i := 0; i < 20; i++ {
		if !lim.Allow(1) {
			t.Fatalf("expected allow at i=%d", i)
		}
	}
	if lim.Allow(1) {
		t.Fatalf("expected deny once tokens exhausted")
	}

	now = now.Add(100 * time.Millisecond) // should refill 1 token
	if !lim.Allow(1) {
		t.Fatalf("expected allow after refill")
	}
	if lim.Allow(1) {
		t.Fatalf("expected deny again without enough time")
	}
}

func TestInboundLimiter_BPSDeniesWhenBytesExceed(t *testing.T) {
	now := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	lim := newInboundAudioLimiter(clock, 0, 100, 2) // 200 byte burst
	if !lim.Allow(150) {
		t.Fatalf("expected allow 150 bytes")
	}
	if lim.Allow(60) {
		t.Fatalf("expected deny 60 bytes due to bps tokens")
	}
}

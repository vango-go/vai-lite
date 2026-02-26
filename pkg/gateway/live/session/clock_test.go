package session

import (
	"testing"
	"time"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func TestLiveSession_SessionTimeMS_FollowsClientTimestamp(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	s := &LiveSession{
		startTime: time.Unix(900, 0),
		now:       clk.Now,
	}

	s.observeClientTimestampMS(5000)
	if got := s.sessionTimeMS(); got != 5000 {
		t.Fatalf("t0=%d, want 5000", got)
	}

	clk.t = clk.t.Add(120 * time.Millisecond)
	if got := s.sessionTimeMS(); got != 5120 {
		t.Fatalf("t1=%d, want 5120", got)
	}

	// Backwards timestamp must not reduce the session clock.
	s.observeClientTimestampMS(4900)
	clk.t = clk.t.Add(80 * time.Millisecond)
	if got := s.sessionTimeMS(); got != 5200 {
		t.Fatalf("t2=%d, want 5200", got)
	}

	// Jump forward should update the max client timestamp and continue advancing from there.
	s.observeClientTimestampMS(7000)
	if got := s.sessionTimeMS(); got != 7000 {
		t.Fatalf("t3=%d, want 7000", got)
	}
	clk.t = clk.t.Add(50 * time.Millisecond)
	if got := s.sessionTimeMS(); got != 7050 {
		t.Fatalf("t4=%d, want 7050", got)
	}
}

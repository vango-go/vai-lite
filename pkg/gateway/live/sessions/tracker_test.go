package sessions

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTracker_RegisterUnregister_CountAndWait(t *testing.T) {
	tr := NewTracker()
	if tr.Count() != 0 {
		t.Fatalf("initial count=%d, want 0", tr.Count())
	}

	u1 := tr.Register("s1", Handle{})
	u2 := tr.Register("s2", Handle{})
	if tr.Count() != 2 {
		t.Fatalf("count=%d, want 2", tr.Count())
	}

	u1()
	if tr.Count() != 1 {
		t.Fatalf("count=%d, want 1", tr.Count())
	}

	u2()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if ok := tr.Wait(ctx); !ok {
		t.Fatalf("expected Wait to return true")
	}
	if tr.Count() != 0 {
		t.Fatalf("count=%d, want 0", tr.Count())
	}
}

func TestTracker_CancelAll_CallsCancel(t *testing.T) {
	tr := NewTracker()
	var c1, c2 atomic.Int64
	tr.Register("s1", Handle{Cancel: func() { c1.Add(1) }})
	tr.Register("s2", Handle{Cancel: func() { c2.Add(1) }})

	if n := tr.CancelAll(); n != 2 {
		t.Fatalf("canceled=%d, want 2", n)
	}
	if c1.Load() != 1 || c2.Load() != 1 {
		t.Fatalf("cancel calls=%d/%d, want 1/1", c1.Load(), c2.Load())
	}
}

func TestTracker_WarnAll_BestEffort(t *testing.T) {
	tr := NewTracker()
	var w1, w2 atomic.Int64
	tr.Register("s1", Handle{Warn: func(code, message string) error {
		_ = code
		_ = message
		w1.Add(1)
		return nil
	}})
	tr.Register("s2", Handle{Warn: func(code, message string) error {
		_ = code
		_ = message
		w2.Add(1)
		return errors.New("nope")
	}})

	if sent := tr.WarnAll("draining", "test"); sent != 2 {
		t.Fatalf("sent=%d, want 2", sent)
	}
	if w1.Load() != 1 || w2.Load() != 1 {
		t.Fatalf("warn calls=%d/%d, want 1/1", w1.Load(), w2.Load())
	}
}

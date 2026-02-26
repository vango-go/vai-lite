package ratelimit

import (
	"testing"
	"time"
)

func TestAcquireWSSession_EnforcesConcurrency(t *testing.T) {
	l := New(Config{MaxConcurrentWSSessions: 1})
	now := time.Now()

	first := l.AcquireWSSession("p1", now)
	if !first.Allowed || first.Permit == nil {
		t.Fatalf("first allowed=%v permit=%v", first.Allowed, first.Permit)
	}

	second := l.AcquireWSSession("p1", now)
	if second.Allowed {
		t.Fatalf("second should be denied")
	}

	first.Permit.Release()
	third := l.AcquireWSSession("p1", now)
	if !third.Allowed {
		t.Fatalf("third should be allowed after release")
	}
}

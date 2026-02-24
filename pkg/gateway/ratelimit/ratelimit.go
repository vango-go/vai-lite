package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sync"
	"time"
)

type Config struct {
	RPS   float64
	Burst int

	MaxConcurrentRequests int
	MaxConcurrentStreams  int

	// Operational bounds for the in-memory map (single-process only).
	MaxEntries int
	EntryTTL   time.Duration
}

type Limiter struct {
	cfg Config

	mu sync.Mutex
	m  map[string]*principalLimiter
}

type principalLimiter struct {
	mu sync.Mutex

	tb tokenBucket

	reqSem    chan struct{}
	streamSem chan struct{}

	lastSeen time.Time
}

type tokenBucket struct {
	rps      float64
	capacity float64

	tokens float64
	last   time.Time
}

func New(cfg Config) *Limiter {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 10_000
	}
	if cfg.EntryTTL <= 0 {
		cfg.EntryTTL = 30 * time.Minute
	}
	return &Limiter{
		cfg: cfg,
		m:   make(map[string]*principalLimiter),
	}
}

func PrincipalKeyFromAPIKey(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	// 16 bytes => 32 hex chars; enough to avoid collisions in practice.
	return "k_" + hex.EncodeToString(sum[:16])
}

type Permit struct {
	release func()
}

func (p *Permit) Release() {
	if p == nil || p.release == nil {
		return
	}
	p.release()
	p.release = nil
}

type Decision struct {
	Allowed    bool
	RetryAfter int
	Permit     *Permit
}

func (l *Limiter) AcquireRequest(principal string, now time.Time) Decision {
	if principal == "" {
		principal = "anonymous"
	}

	pl := l.getOrCreate(principal, now)
	pl.touch(now)

	// RPS/burst (token bucket).
	if l.cfg.RPS > 0 && l.cfg.Burst > 0 {
		ok, retryAfter := pl.allowToken(now, l.cfg.RPS, l.cfg.Burst)
		if !ok {
			return Decision{Allowed: false, RetryAfter: retryAfter}
		}
	}

	// Concurrency cap.
	if l.cfg.MaxConcurrentRequests > 0 {
		select {
		case pl.reqSem <- struct{}{}:
			return Decision{
				Allowed: true,
				Permit:  &Permit{release: func() { <-pl.reqSem }},
			}
		default:
			return Decision{Allowed: false, RetryAfter: 1}
		}
	}

	return Decision{Allowed: true, Permit: &Permit{release: func() {}}}
}

func (l *Limiter) AcquireStream(principal string, now time.Time) Decision {
	if principal == "" {
		principal = "anonymous"
	}

	pl := l.getOrCreate(principal, now)
	pl.touch(now)

	if l.cfg.MaxConcurrentStreams > 0 {
		select {
		case pl.streamSem <- struct{}{}:
			return Decision{
				Allowed: true,
				Permit:  &Permit{release: func() { <-pl.streamSem }},
			}
		default:
			return Decision{Allowed: false, RetryAfter: 1}
		}
	}

	return Decision{Allowed: true, Permit: &Permit{release: func() {}}}
}

func (l *Limiter) getOrCreate(principal string, now time.Time) *principalLimiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.m) >= l.cfg.MaxEntries {
		l.gcLocked(now)
		// If still too big, drop one arbitrary entry (bounded memory > perfect fairness).
		if len(l.m) >= l.cfg.MaxEntries {
			for k := range l.m {
				delete(l.m, k)
				break
			}
		}
	}

	if pl, ok := l.m[principal]; ok {
		return pl
	}
	pl := &principalLimiter{
		reqSem:    make(chan struct{}, max(1, l.cfg.MaxConcurrentRequests)),
		streamSem: make(chan struct{}, max(1, l.cfg.MaxConcurrentStreams)),
		lastSeen:  now,
	}
	l.m[principal] = pl
	return pl
}

func (l *Limiter) gcLocked(now time.Time) {
	ttl := l.cfg.EntryTTL
	for k, v := range l.m {
		if now.Sub(v.lastSeen) > ttl {
			delete(l.m, k)
		}
	}
}

func (pl *principalLimiter) touch(now time.Time) {
	pl.lastSeen = now
}

func (pl *principalLimiter) allowToken(now time.Time, rps float64, burst int) (bool, int) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if burst <= 0 || rps <= 0 {
		return true, 0
	}
	capacity := float64(burst)
	if pl.tb.capacity == 0 {
		pl.tb = tokenBucket{
			rps:      rps,
			capacity: capacity,
			tokens:   capacity,
			last:     now,
		}
	}

	// If config changes at runtime (rare), adapt.
	pl.tb.rps = rps
	pl.tb.capacity = capacity

	elapsed := now.Sub(pl.tb.last).Seconds()
	if elapsed > 0 {
		pl.tb.tokens = math.Min(pl.tb.capacity, pl.tb.tokens+(elapsed*pl.tb.rps))
		pl.tb.last = now
	}

	if pl.tb.tokens >= 1.0 {
		pl.tb.tokens -= 1.0
		return true, 0
	}

	needed := 1.0 - pl.tb.tokens
	seconds := needed / pl.tb.rps
	retryAfter := int(math.Ceil(seconds))
	if retryAfter < 1 {
		retryAfter = 1
	}
	return false, retryAfter
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

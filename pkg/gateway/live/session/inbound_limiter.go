package session

import "time"

type inboundAudioLimiter struct {
	now          func() time.Time
	fpsRate      int64
	fpsTokens    int64
	bpsRate      int64
	bpsTokens    int64
	burstSeconds int64
	lastRefill   time.Time
}

func newInboundAudioLimiter(now func() time.Time, fps int, bps int64, burstSeconds int) *inboundAudioLimiter {
	if fps <= 0 && bps <= 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	if burstSeconds <= 0 {
		burstSeconds = 1
	}

	l := &inboundAudioLimiter{
		now:          now,
		fpsRate:      int64(fps),
		bpsRate:      bps,
		burstSeconds: int64(burstSeconds),
		lastRefill:   now(),
	}
	if l.fpsRate > 0 {
		l.fpsTokens = l.fpsRate * l.burstSeconds
	}
	if l.bpsRate > 0 {
		l.bpsTokens = l.bpsRate * l.burstSeconds
	}
	return l
}

func (l *inboundAudioLimiter) Allow(frameBytes int) bool {
	if l == nil {
		return true
	}
	l.refill()

	if l.fpsRate > 0 && l.fpsTokens < 1 {
		return false
	}
	if frameBytes < 0 {
		frameBytes = 0
	}
	if l.bpsRate > 0 && l.bpsTokens < int64(frameBytes) {
		return false
	}
	if l.fpsRate > 0 {
		l.fpsTokens--
	}
	if l.bpsRate > 0 {
		l.bpsTokens -= int64(frameBytes)
	}
	return true
}

func (l *inboundAudioLimiter) refill() {
	now := l.now()
	if l.lastRefill.IsZero() {
		l.lastRefill = now
		return
	}
	elapsed := now.Sub(l.lastRefill)
	if elapsed <= 0 {
		return
	}

	if l.fpsRate > 0 {
		add := (elapsed.Nanoseconds() * l.fpsRate) / int64(time.Second)
		if add > 0 {
			l.fpsTokens += add
			maxTokens := l.fpsRate * l.burstSeconds
			if l.fpsTokens > maxTokens {
				l.fpsTokens = maxTokens
			}
		}
	}
	if l.bpsRate > 0 {
		add := (elapsed.Nanoseconds() * l.bpsRate) / int64(time.Second)
		if add > 0 {
			l.bpsTokens += add
			maxTokens := l.bpsRate * l.burstSeconds
			if l.bpsTokens > maxTokens {
				l.bpsTokens = maxTokens
			}
		}
	}

	l.lastRefill = now
}

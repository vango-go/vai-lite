package lifecycle

import "sync/atomic"

// Lifecycle is a tiny process lifecycle state holder shared across handlers.
// It is used for readiness draining during graceful shutdown.
type Lifecycle struct {
	draining atomic.Bool
}

func (l *Lifecycle) SetDraining(draining bool) {
	if l == nil {
		return
	}
	l.draining.Store(draining)
}

func (l *Lifecycle) IsDraining() bool {
	if l == nil {
		return false
	}
	return l.draining.Load()
}

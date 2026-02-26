package sessions

import (
	"context"
	"sync"
)

type Handle struct {
	Cancel func()
	Warn   func(code, message string) error
}

type Tracker struct {
	mu       sync.Mutex
	sessions map[string]*trackedSession
	wg       sync.WaitGroup
}

type trackedSession struct {
	handle Handle
	once   sync.Once
}

func NewTracker() *Tracker {
	return &Tracker{
		sessions: make(map[string]*trackedSession),
	}
}

func (t *Tracker) Register(sessionID string, h Handle) (unregister func()) {
	if t == nil {
		return func() {}
	}

	entry := &trackedSession{handle: h}

	t.mu.Lock()
	if t.sessions == nil {
		t.sessions = make(map[string]*trackedSession)
	}
	old := t.sessions[sessionID]
	t.sessions[sessionID] = entry
	t.wg.Add(1)
	t.mu.Unlock()

	if old != nil {
		t.unregister(sessionID, old)
	}

	return func() { t.unregister(sessionID, entry) }
}

func (t *Tracker) unregister(sessionID string, entry *trackedSession) {
	if t == nil || entry == nil {
		return
	}
	entry.once.Do(func() {
		t.mu.Lock()
		if t.sessions != nil && t.sessions[sessionID] == entry {
			delete(t.sessions, sessionID)
		}
		t.mu.Unlock()
		t.wg.Done()
	})
}

func (t *Tracker) Count() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sessions)
}

func (t *Tracker) WarnAll(code, message string) (sent int) {
	if t == nil {
		return 0
	}

	var warns []func(code, message string) error
	t.mu.Lock()
	for _, entry := range t.sessions {
		if entry == nil || entry.handle.Warn == nil {
			continue
		}
		warns = append(warns, entry.handle.Warn)
	}
	t.mu.Unlock()

	for _, warn := range warns {
		_ = warn(code, message)
		sent++
	}
	return sent
}

func (t *Tracker) CancelAll() (canceled int) {
	if t == nil {
		return 0
	}

	var cancels []func()
	t.mu.Lock()
	for _, entry := range t.sessions {
		if entry == nil || entry.handle.Cancel == nil {
			continue
		}
		cancels = append(cancels, entry.handle.Cancel)
	}
	t.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
		canceled++
	}
	return canceled
}

func (t *Tracker) Wait(ctx context.Context) bool {
	if t == nil {
		return true
	}
	if ctx == nil {
		t.wg.Wait()
		return true
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		t.wg.Wait()
	}()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

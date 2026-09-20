package router

import (
	"sync"
	"time"
)

type breakerState struct {
	failures  int
	openUntil time.Time
	probing   bool
}

// Breakers holds one circuit breaker per target. State is per replica; that is intentional:
// each replica sees its own network path to the provider.
type Breakers struct {
	mu        sync.Mutex
	threshold int
	openFor   time.Duration
	now       func() time.Time
	m         map[Target]*breakerState
}

func NewBreakers(threshold int, openFor time.Duration, now func() time.Time) *Breakers {
	return &Breakers{threshold: threshold, openFor: openFor, now: now, m: map[Target]*breakerState{}}
}

func (b *Breakers) get(t Target) *breakerState {
	s, ok := b.m[t]
	if !ok {
		s = &breakerState{}
		b.m[t] = s
	}
	return s
}

// Allow reports whether a call to t may proceed. After the open period it admits exactly one probe.
func (b *Breakers) Allow(t Target) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(t)
	if s.openUntil.IsZero() {
		return true
	}
	if b.now().Before(s.openUntil) || s.probing {
		return false
	}
	s.probing = true
	return true
}

func (b *Breakers) Success(t Target) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*b.get(t) = breakerState{}
}

func (b *Breakers) Failure(t Target) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(t)
	s.failures++
	if s.probing || s.failures >= b.threshold {
		s.openUntil = b.now().Add(b.openFor)
		s.probing = false
	}
}

func (b *Breakers) State(t Target) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(t)
	switch {
	case s.openUntil.IsZero():
		return "closed"
	case s.probing:
		return "half-open"
	case b.now().Before(s.openUntil):
		return "open"
	default:
		return "half-open"
	}
}

// open reports whether t should be deprioritised by the planner (open and not yet probe-able).
func (b *Breakers) open(t Target) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(t)
	return !s.openUntil.IsZero() && (b.now().Before(s.openUntil) || s.probing)
}

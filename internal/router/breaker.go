// Package router provides enterprise-grade capabilities, configuration, and structural components for the router subsystem.
package router

import (
	"sync"
	"time"
)

// breakerState tracks the momentary health of a specific upstream target.
type breakerState struct {
	failures  int
	openUntil time.Time
	probing   bool
}

// Breakers holds one circuit breaker per target. State is per replica; that is intentional:
// each replica sees its own network path to the provider and can fail independently.
type Breakers struct {
	mu        sync.Mutex
	threshold int
	openFor   time.Duration
	now       func() time.Time
	m         map[Target]*breakerState
}

// NewBreakers initializes a concurrency-safe Circuit Breaker registry for all route targets.
func NewBreakers(threshold int, openFor time.Duration, now func() time.Time) *Breakers {
	return &Breakers{threshold: threshold, openFor: openFor, now: now, m: map[Target]*breakerState{}}
}

// get lazily initializes or retrieves the breaker state for a target.
func (b *Breakers) get(t Target) *breakerState {
	s, ok := b.m[t]
	if !ok {
		s = &breakerState{}
		b.m[t] = s
	}
	return s
}

// Allow reports whether a call to t may proceed.
// State Machine:
// - Closed: returns true.
// - Open (within timeout): returns false.
// - Half-Open (timeout expired): admits exactly ONE probe request and returns true. Subsequent checks return false until the probe resolves.
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

// Success records a successful execution, resetting the breaker back to the Closed state.
func (b *Breakers) Success(t Target) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*b.get(t) = breakerState{}
}

// Failure records a failed execution against the target.
// If the failure threshold is reached, or a Half-Open probe fails, the circuit transitions
// to Open for the configured 'openFor' duration.
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

// OpenFor explicitly opens the circuit breaker for target t for a specific duration.
// This is typically used to respect HTTP 429 Retry-After headers from providers.
func (b *Breakers) OpenFor(t Target, d time.Duration) {
	if d <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(t)
	until := b.now().Add(d)
	if until.After(s.openUntil) {
		s.openUntil = until
		s.probing = false
	}
}

// State returns a human-readable representation of the breaker's current phase.
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

// open reports whether t should be deprioritized by the planner.
// True if the circuit is Open and not yet eligible for a probe.
func (b *Breakers) open(t Target) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(t)
	return !s.openUntil.IsZero() && (b.now().Before(s.openUntil) || s.probing)
}

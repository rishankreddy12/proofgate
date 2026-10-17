package server

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const readinessTimeout = 2 * time.Second

// ReadinessDependency is a named runtime dependency check. Checks receive the
// probe context so network calls can stop when the bounded readiness window
// expires.
type ReadinessDependency struct {
	Name  string
	Check func(context.Context) error
}

// LivenessHandler reports only that the HTTP process is serving requests. It
// deliberately does not touch the database, Redis, providers, or runtime state;
// those failures belong to readiness and should not cause a restart loop.
func LivenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ReadinessHandler reports whether the active runtime and its required
// dependencies are usable. Every dependency check shares a bounded context so
// a stuck network dependency cannot leave a probe hanging indefinitely.
func ReadinessHandler(state *State, dependencies ...ReadinessDependency) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		if err := runtimeReady(state); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":     "not_ready",
				"dependency": "runtime",
				"reason":     "not_initialized",
			})
			return
		}

		for _, dependency := range dependencies {
			if dependency.Check == nil {
				continue
			}
			name := dependency.Name
			if name == "" {
				name = "dependency"
			}
			if err := runReadinessCheck(ctx, dependency.Check); err != nil {
				reason := "unavailable"
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					reason = "timeout"
				}
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"status":     "not_ready",
					"dependency": name,
					"reason":     reason,
				})
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// Readiness exposes the standard bounded readiness handler for a gateway's
// active runtime. The executable can add its concrete database and Redis
// checks at listener wiring time without coupling this package to those stores.
func (h *Handlers) Readiness(dependencies ...ReadinessDependency) http.HandlerFunc {
	if h == nil {
		return ReadinessHandler(nil, dependencies...)
	}
	return ReadinessHandler(h.State, dependencies...)
}

func runtimeReady(state *State) error {
	if state == nil {
		return errors.New("runtime state is nil")
	}
	rt := state.Load()
	if rt == nil || rt.Config == nil || rt.Router == nil || rt.Registry == nil || rt.Pricing == nil {
		return errors.New("runtime is not initialized")
	}
	return nil
}

func runReadinessCheck(ctx context.Context, check func(context.Context) error) error {
	result := make(chan error, 1)
	go func() { result <- check(ctx) }()
	select {
	case err := <-result:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

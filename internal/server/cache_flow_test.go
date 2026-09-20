package server

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/cache"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

type mapStore struct {
	mu sync.Mutex
	m  map[string]cache.Entry
}

func (s *mapStore) Get(_ context.Context, k string) (*cache.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[k]; ok {
		return &e, nil
	}
	return nil, nil
}
func (s *mapStore) Put(_ context.Context, _, k string, e cache.Entry, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = e
	return nil
}

func TestCachedResponseSkipsUpstream(t *testing.T) {
	stage := cache.NewStage(&mapStore{m: map[string]cache.Entry{}}, nil, nil, nil, nil)
	mode := mockllm.Mode{Reply: "echo: hi lorem lorem"}
	e := setup(t, mode, mode, stage)
	// turn on exact caching for the default route in the live runtime
	for _, r := range e.h.State.Load().Router.Routes() {
		if r.Name == "default" {
			r.Cache.Mode, r.Cache.Exact, r.Cache.TTL, r.Cache.Version, r.Cache.MaxEntryBytes = "on", true, time.Hour, 1, 1<<16
		}
	}
	first := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	io.Copy(io.Discard, first.Body)
	first.Body.Close()
	require.Equal(t, "miss", first.Header.Get("X-ProofGate-Cache"))
	stage.Wait()

	second := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	io.Copy(io.Discard, second.Body)
	second.Body.Close()
	require.Equal(t, "hit-exact", second.Header.Get("X-ProofGate-Cache"))
	require.Equal(t, "0.000000", second.Header.Get("X-ProofGate-Cost-USD"))
	require.EqualValues(t, 1, e.mocks[0].Requests())

	streamed := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	defer streamed.Body.Close()
	require.Equal(t, "hit-exact", streamed.Header.Get("X-ProofGate-Cache"))
	a, _, _ := readStream(t, streamed)
	require.Equal(t, "echo: hi lorem lorem", a.Text())
	require.EqualValues(t, 1, e.mocks[0].Requests())
}

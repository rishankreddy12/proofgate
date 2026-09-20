package cache

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

type memExact struct {
	mu  sync.Mutex
	m   map[string]Entry
	err error
}

func (x *memExact) Get(_ context.Context, k string) (*Entry, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.err != nil {
		return nil, x.err
	}
	if e, ok := x.m[k]; ok {
		return &e, nil
	}
	return nil, nil
}
func (x *memExact) Put(_ context.Context, _, k string, e Entry, _ time.Duration) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.m[k] = e
	return nil
}

type memSem struct {
	mu   sync.Mutex
	rows []struct {
		tenant, scope string
		emb           []float32
		e             Entry
	}
}

func cosine(a, b []float32) float64 {
	var d, na, nb float64
	for i := range a {
		d += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	return d / (math.Sqrt(na) * math.Sqrt(nb))
}

func (s *memSem) Put(_ context.Context, tenant, _, scope string, emb []float32, e Entry, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, struct {
		tenant, scope string
		emb           []float32
		e             Entry
	}{tenant, scope, emb, e})
	return "k", nil
}
func (s *memSem) Nearest(_ context.Context, tenant, scope string, emb []float32) (*Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Match
	for _, r := range s.rows {
		if r.tenant != tenant || r.scope != scope {
			continue
		}
		if sim := cosine(emb, r.emb); best == nil || sim > best.Similarity {
			best = &Match{Entry: r.e, Similarity: sim}
		}
	}
	return best, nil
}

var hashEmb = EmbedFunc(func(_ context.Context, _, _, text string) ([]float32, error) {
	return mockllm.HashEmbedding(text, 128), nil
})

func route(cc config.CacheConfig) *router.Route { return &router.Route{Name: "faq", Cache: cc} }

var semOn = config.CacheConfig{Mode: "on", Exact: true, Semantic: true, Threshold: 0.8, TTL: time.Hour,
	Version: 1, EmbeddingRoute: "embed", MaxEntryBytes: 1 << 16}

func newCall(text string, rt *router.Route) *pipeline.Call {
	c := pipeline.NewCall(auth.Principal{TenantID: "t1"}, &api.ChatRequest{Model: "faq",
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: text}}}}, rt)
	c.Incoming = http.Header{}
	return c
}

func answer(c *pipeline.Call, text, finish string) {
	c.Response = &api.ChatResponse{ID: "up", Object: "chat.completion",
		Choices: []api.Choice{{Message: api.Message{Role: "assistant", Content: api.Content{Text: text}}, FinishReason: finish}},
		Usage:   &api.Usage{TotalTokens: 9}}
	c.CostMicros = 700
}

func TestMissStoreThenExactHit(t *testing.T) {
	x, sem := &memExact{m: map[string]Entry{}}, &memSem{}
	s := NewStage(x, sem, hashEmb, nil, nil)
	ctx := context.Background()

	c1 := newCall("how do I reset my password", route(semOn))
	handled, err := s.Before(ctx, c1)
	require.NoError(t, err)
	require.False(t, handled)
	answer(c1, "Settings > Security", "stop")
	s.After(ctx, c1)
	s.Wait()

	c2 := newCall("how do I reset my password", route(semOn))
	handled, _ = s.Before(ctx, c2)
	require.True(t, handled)
	require.Equal(t, "hit-exact", c2.CacheStatus)
	require.Equal(t, "Settings > Security", c2.Response.Choices[0].Message.Content.Text)
	require.Equal(t, "chatcmpl-"+c2.ID, c2.Response.ID)
	require.Equal(t, c1.ID, c2.Header.Get("X-ProofGate-Cache-Source"))
	require.EqualValues(t, 700, c2.Values["cache.saved_micros"])
	require.Equal(t, api.Usage{}, c2.Usage)
}

func TestSemanticHitAndMissByThreshold(t *testing.T) {
	x, sem := &memExact{m: map[string]Entry{}}, &memSem{}
	s := NewStage(x, sem, hashEmb, nil, nil)
	ctx := context.Background()
	c1 := newCall("how do I reset my password", route(semOn))
	_, _ = s.Before(ctx, c1)
	answer(c1, "Settings > Security", "stop")
	s.After(ctx, c1)
	s.Wait()

	c2 := newCall("how can I reset my password", route(semOn))
	handled, _ := s.Before(ctx, c2)
	require.True(t, handled)
	require.Equal(t, "hit-semantic", c2.CacheStatus)
	require.NotEmpty(t, c2.Header.Get("X-ProofGate-Cache-Similarity"))

	strict := semOn
	strict.Threshold = 0.99
	c3 := newCall("how can I reset my password", route(strict))
	handled, _ = s.Before(ctx, c3)
	require.False(t, handled)
	require.Equal(t, "miss", c3.CacheStatus)
}

func TestNotStoredWhenUnsafe(t *testing.T) {
	x, sem := &memExact{m: map[string]Entry{}}, &memSem{}
	s := NewStage(x, sem, hashEmb, nil, nil)
	ctx := context.Background()
	for _, finish := range []string{"length", "content_filter"} {
		c := newCall("q "+finish, route(semOn))
		_, _ = s.Before(ctx, c)
		answer(c, "partial", finish)
		s.After(ctx, c)
	}
	c := newCall("q err", route(semOn))
	_, _ = s.Before(ctx, c)
	answer(c, "x", "stop")
	c.Err = errors.New("boom")
	s.After(ctx, c)
	s.Wait()
	require.Empty(t, x.m)
	require.Empty(t, sem.rows)
}

func TestBypassAndErrorsAreSafe(t *testing.T) {
	var ops []string
	s := NewStage(&memExact{m: map[string]Entry{}, err: errors.New("redis down")}, &memSem{}, hashEmb,
		func(op string) { ops = append(ops, op) }, nil)
	c := newCall("q", route(semOn))
	c.Request.Tools = []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "f"}}}
	handled, err := s.Before(context.Background(), c)
	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, "bypass", c.CacheStatus)

	c = newCall("q", route(semOn))
	handled, err = s.Before(context.Background(), c)
	require.NoError(t, err, "a cache failure must never fail the request")
	require.False(t, handled)
	require.Contains(t, ops, "exact_get")
}

func TestPoolFullDrops(t *testing.T) {
	block := make(chan struct{})
	slow := &blockingExact{memExact: memExact{m: map[string]Entry{}}, block: block}
	drops := 0
	s := NewStage(slow, nil, nil, nil, func() { drops++ })
	exactOnly := config.CacheConfig{Mode: "on", Exact: true, Threshold: 0.9, TTL: time.Hour, Version: 1, MaxEntryBytes: 1 << 16}
	for i := 0; i < 65; i++ {
		c := newCall("q"+string(rune('a'+i%26))+string(rune('a'+i/26)), route(exactOnly))
		_, _ = s.Before(context.Background(), c)
		answer(c, "a", "stop")
		s.After(context.Background(), c)
	}
	require.Equal(t, 1, drops)
	close(block)
	s.Wait()
}

type blockingExact struct {
	memExact
	block chan struct{}
}

func (b *blockingExact) Put(ctx context.Context, t, k string, e Entry, ttl time.Duration) error {
	<-b.block
	return b.memExact.Put(ctx, t, k, e, ttl)
}

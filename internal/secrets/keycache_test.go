package secrets

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memCreds struct {
	m     map[string]Sealed
	reads int
	err   error
	delay time.Duration
}

func (c *memCreds) ActiveCredential(_ context.Context, p string) (Sealed, int, error) {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	c.reads++
	if c.err != nil {
		return Sealed{}, 0, c.err
	}
	s, ok := c.m[p]
	if !ok {
		return Sealed{}, 0, errors.New("not found")
	}
	return s, 1, nil
}

func TestKeyCache(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, _ := Seal(ctx, k, []byte("sk-1"), AAD("openai"))
	src := &memCreds{m: map[string]Sealed{"openai": s}}
	var registered []string
	now := time.Unix(0, 0)
	kc := NewKeyCache(src, k, time.Minute, func(v string) { registered = append(registered, v) })
	kc.now = func() time.Time { return now }
	kc.randFloat = func() float64 { return 0.5 } // 0 jitter

	for i := 0; i < 3; i++ {
		v, err := kc.Get(ctx, "openai")
		require.NoError(t, err)
		require.Equal(t, "sk-1", v)
	}
	require.Equal(t, 1, src.reads)
	require.Equal(t, []string{"sk-1"}, registered)

	now = now.Add(61 * time.Second)
	_, _ = kc.Get(ctx, "openai")
	require.Equal(t, 2, src.reads, "expired entries are re-read and re-decrypted")
}

func TestKeyCache_StaleGraceOnStoreFailure(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, _ := Seal(ctx, k, []byte("sk-stale-test"), AAD("anthropic"))
	src := &memCreds{m: map[string]Sealed{"anthropic": s}}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	kc := NewKeyCacheWithStaleGrace(src, k, 60*time.Second, 5*time.Minute, nil)
	kc.now = func() time.Time { return now }
	kc.randFloat = func() float64 { return 0.5 } // 0 jitter

	// Initial successful load
	v, err := kc.Get(ctx, "anthropic")
	require.NoError(t, err)
	require.Equal(t, "sk-stale-test", v)
	require.Equal(t, 1, src.reads)

	// Advance past TTL (60s), so entry is expired. Store now fails!
	now = now.Add(70 * time.Second)
	src.err = errors.New("postgres connection timeout")

	// Should serve stale value within 5m stale grace
	vStale, err := kc.Get(ctx, "anthropic")
	require.NoError(t, err, "should serve stale value during grace period")
	require.Equal(t, "sk-stale-test", vStale)
	require.Equal(t, 2, src.reads)

	// Advance past stale grace (now + 5m10s)
	now = now.Add(5 * time.Minute)
	_, err = kc.Get(ctx, "anthropic")
	require.Error(t, err, "past stale grace, should return store error")
	require.Contains(t, err.Error(), "postgres connection timeout")
}

func TestKeyCache_Singleflight(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, _ := Seal(ctx, k, []byte("sk-singleflight"), AAD("cohere"))

	var readCount atomic.Int32
	mockSrc := &singleflightMockSrc{
		onRead: func() (Sealed, error) {
			readCount.Add(1)
			time.Sleep(20 * time.Millisecond) // artificial delay to encourage concurrency
			return s, nil
		},
	}

	kc := NewKeyCache(mockSrc, k, time.Minute, nil)

	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)
	errs := make([]error, concurrency)
	vals := make([]string, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			vals[idx], errs[idx] = kc.Get(ctx, "cohere")
		}(i)
	}
	wg.Wait()

	for i := 0; i < concurrency; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, "sk-singleflight", vals[i])
	}
	require.Equal(t, int32(1), readCount.Load(), "singleflight must coalesce concurrent fetches into exactly 1 call")
}

func TestKeyCache_Jitter(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, _ := Seal(ctx, k, []byte("sk-jitter"), AAD("gemini"))
	src := &memCreds{m: map[string]Sealed{"gemini": s}}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	kc := NewKeyCache(src, k, 100*time.Second, nil)
	kc.now = func() time.Time { return now }

	// Test minimum jitter (rand = 0.0 -> -10% -> 90s)
	kc.randFloat = func() float64 { return 0.0 }
	_, err := kc.Get(ctx, "gemini")
	require.NoError(t, err)
	kc.mu.Lock()
	entry := kc.m["gemini"]
	kc.mu.Unlock()
	require.Equal(t, now.Add(90*time.Second), entry.expires)

	kc.Purge()

	// Test maximum jitter (rand = 1.0 -> +10% -> 110s)
	kc.randFloat = func() float64 { return 1.0 }
	_, err = kc.Get(ctx, "gemini")
	require.NoError(t, err)
	kc.mu.Lock()
	entry = kc.m["gemini"]
	kc.mu.Unlock()
	require.Equal(t, now.Add(110*time.Second), entry.expires)
}

type singleflightMockSrc struct {
	onRead func() (Sealed, error)
}

func (s *singleflightMockSrc) ActiveCredential(_ context.Context, _ string) (Sealed, int, error) {
	sealed, err := s.onRead()
	return sealed, 1, err
}

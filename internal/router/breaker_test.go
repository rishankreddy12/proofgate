package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBreakerLifecycle(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewBreakers(3, 30*time.Second, func() time.Time { return now })
	tg := Target{"p", "m"}
	for i := 0; i < 2; i++ {
		b.Failure(tg)
	}
	require.Equal(t, "closed", b.State(tg))
	b.Failure(tg)
	require.Equal(t, "open", b.State(tg))
	require.False(t, b.Allow(tg))

	now = now.Add(31 * time.Second)
	require.True(t, b.Allow(tg), "first call after cool-down is the probe")
	require.Equal(t, "half-open", b.State(tg))
	require.False(t, b.Allow(tg), "only one probe at a time")
	b.Failure(tg)
	require.Equal(t, "open", b.State(tg))

	now = now.Add(31 * time.Second)
	require.True(t, b.Allow(tg))
	b.Success(tg)
	require.Equal(t, "closed", b.State(tg))
	require.True(t, b.Allow(tg))
}

package analytics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatcherFlushesBySizeAndInterval(t *testing.T) {
	var mu sync.Mutex
	var batches [][]int
	flush := func(_ context.Context, b []int) error {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, append([]int(nil), b...))
		return nil
	}
	b := NewBatcher("t", 100, 3, 50*time.Millisecond, flush, nil)
	for i := 0; i < 4; i++ {
		require.True(t, b.Emit(i))
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(batches) == 2
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, []int{0, 1, 2}, batches[0])
	require.Equal(t, []int{3}, batches[1])
	require.NoError(t, b.Close(context.Background()))
}

func TestBatcherDropsWhenFull(t *testing.T) {
	block := make(chan struct{})
	drops := 0
	b := NewBatcher("t", 1, 1, time.Hour, func(context.Context, []int) error { <-block; return nil }, func() { drops++ })
	b.Emit(1) // taken by the flusher, which blocks
	time.Sleep(20 * time.Millisecond)
	b.Emit(2) // fills the channel
	require.False(t, b.Emit(3))
	require.Equal(t, 1, drops)
	close(block)
	require.NoError(t, b.Close(context.Background()))
}

func TestBatcherCloseFlushesQueue(t *testing.T) {
	var got []int
	b := NewBatcher("t", 100, 1000, time.Hour, func(_ context.Context, v []int) error { got = append(got, v...); return nil }, nil)
	b.Emit(7)
	b.Emit(8)
	require.NoError(t, b.Close(context.Background()))
	require.Equal(t, []int{7, 8}, got)
}

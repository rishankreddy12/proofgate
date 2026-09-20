package proof

import (
	"sync"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/cache"
	"github.com/stretchr/testify/require"
)

func TestShadowRecorderBufferingAndFlush(t *testing.T) {
	dropped := 0
	recorder := NewShadowRecorder(nil, 2, 5, 50*time.Millisecond, func() { dropped++ }, nil)

	// Emit through CacheHook
	hook := recorder.CacheHook()
	ok := recorder.Emit(ShadowRecord{ID: "r1"})
	require.True(t, ok)

	hook(cache.ShadowRecord{ID: "r2"})

	// Buffer is 2, next one should drop
	ok3 := recorder.Emit(ShadowRecord{ID: "r3"})
	require.False(t, ok3)
	require.Equal(t, 1, dropped)

	// Close drains and shuts down cleanly
	recorder.Close()
}

func TestShadowRecorderThreadSafety(t *testing.T) {
	recorder := NewShadowRecorder(nil, 1000, 10, 10*time.Millisecond, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				recorder.Emit(ShadowRecord{ID: "rec"})
			}
		}(i)
	}
	wg.Wait()
	recorder.Close()
}

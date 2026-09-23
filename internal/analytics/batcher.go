// Package analytics writes append-only analytics rows to ClickHouse without blocking requests.
package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Batcher[T any] struct {
	name     string
	ch       chan T
	maxBatch int
	interval time.Duration
	flush    func(context.Context, []T) error
	onDrop   func()
	done     chan struct{}
	once     sync.Once
	mu       sync.RWMutex
	closed   bool
	finalErr error
}

func NewBatcher[T any](name string, capacity, maxBatch int, interval time.Duration,
	flush func(context.Context, []T) error, onDrop func()) *Batcher[T] {
	if onDrop == nil {
		onDrop = func() {}
	}
	b := &Batcher[T]{name: name, ch: make(chan T, capacity), maxBatch: maxBatch, interval: interval,
		flush: flush, onDrop: onDrop, done: make(chan struct{})}
	go b.loop()
	return b
}

// Emit never blocks. It returns false if the queue is full or closed.
func (b *Batcher[T]) Emit(v T) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return false
	}
	select {
	case b.ch <- v:
		return true
	default:
		b.onDrop()
		return false
	}
}

func (b *Batcher[T]) write(buf []T) error {
	if len(buf) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.flush(ctx, buf); err != nil {
		slog.Error("analytics flush failed; rows lost", "table", b.name, "rows", len(buf), "err", err)
		return err
	}
	return nil
}

func (b *Batcher[T]) loop() {
	defer close(b.done)
	buf := make([]T, 0, b.maxBatch)
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case v, ok := <-b.ch:
			if !ok {
				b.finalErr = b.write(buf)
				return
			}
			buf = append(buf, v)
			if len(buf) >= b.maxBatch {
				_ = b.write(buf)
				buf = buf[:0]
			}
		case <-t.C:
			_ = b.write(buf)
			buf = buf[:0]
		}
	}
}

// Close stops accepting events and flushes everything already queued.
func (b *Batcher[T]) Close(ctx context.Context) error {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		close(b.ch)
		b.mu.Unlock()
	})
	select {
	case <-b.done:
		return b.finalErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

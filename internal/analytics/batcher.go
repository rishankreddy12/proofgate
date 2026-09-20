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

// Emit never blocks. It returns false and counts a drop when the queue is full.
func (b *Batcher[T]) Emit(v T) bool {
	select {
	case b.ch <- v:
		return true
	default:
		b.onDrop()
		return false
	}
}

func (b *Batcher[T]) write(buf []T) {
	if len(buf) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.flush(ctx, buf); err != nil {
		slog.Error("analytics flush failed; rows lost", "table", b.name, "rows", len(buf), "err", err)
	}
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
				b.write(buf)
				return
			}
			buf = append(buf, v)
			if len(buf) >= b.maxBatch {
				b.write(buf)
				buf = buf[:0]
			}
		case <-t.C:
			b.write(buf)
			buf = buf[:0]
		}
	}
}

// Close stops accepting events and flushes everything already queued.
func (b *Batcher[T]) Close(ctx context.Context) error {
	b.once.Do(func() { close(b.ch) })
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

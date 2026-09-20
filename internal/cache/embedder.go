package cache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"sync"
)

type Embedder interface {
	Embed(ctx context.Context, route, tenantID, text string) ([]float32, error)
}

type EmbedFunc func(ctx context.Context, route, tenantID, text string) ([]float32, error)

func (f EmbedFunc) Embed(ctx context.Context, route, tenantID, text string) ([]float32, error) {
	return f(ctx, route, tenantID, text)
}

type lruKey struct {
	route string
	sum   [32]byte
}

type lruItem struct {
	k lruKey
	v []float32
}

type lruEmbedder struct {
	inner Embedder
	size  int
	mu    sync.Mutex
	ll    *list.List
	m     map[lruKey]*list.Element
}

// NewLRUEmbedder caches embeddings in memory. Repeated questions (the common case for a cache) then
// cost one Redis round trip instead of an embedding call plus a Redis round trip.
func NewLRUEmbedder(inner Embedder, size int) Embedder {
	return &lruEmbedder{inner: inner, size: size, ll: list.New(), m: map[lruKey]*list.Element{}}
}

func (l *lruEmbedder) Embed(ctx context.Context, route, tenantID, text string) ([]float32, error) {
	k := lruKey{route: route, sum: sha256.Sum256([]byte(text))}
	l.mu.Lock()
	if el, ok := l.m[k]; ok {
		l.ll.MoveToFront(el)
		v := el.Value.(*lruItem).v
		l.mu.Unlock()
		return v, nil
	}
	l.mu.Unlock()
	v, err := l.inner.Embed(ctx, route, tenantID, text)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.m[k]; !ok {
		l.m[k] = l.ll.PushFront(&lruItem{k: k, v: v})
		if l.ll.Len() > l.size {
			old := l.ll.Back()
			l.ll.Remove(old)
			delete(l.m, old.Value.(*lruItem).k)
		}
	}
	return v, nil
}

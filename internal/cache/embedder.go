// Package cache provides enterprise-grade capabilities, configuration, and structural components for the cache subsystem.
package cache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"sync"
)

// Embedder abstracts the upstream provider API (e.g. text-embedding-3-small)
// used to project text queries into dense vector space for Semantic Caching.
type Embedder interface {
	Embed(ctx context.Context, route, tenantID, text string) ([]float32, error)
}

// EmbedFunc allows raw functions to satisfy the Embedder interface.
type EmbedFunc func(ctx context.Context, route, tenantID, text string) ([]float32, error)

// Embed executes the underlying embedding function.
func (f EmbedFunc) Embed(ctx context.Context, route, tenantID, text string) ([]float32, error) {
	return f(ctx, route, tenantID, text)
}

// lruKey uniquely identifies an embedding request to prevent redundant API calls.
type lruKey struct {
	route string
	sum   [32]byte
}

// lruItem encapsulates the dense vector payload for the LRU.
type lruItem struct {
	k lruKey
	v []float32
}

// lruEmbedder wraps a concrete Embedder with an in-memory Least Recently Used cache.
//
// Architecture: When Semantic Caching is enabled, the Gateway must convert every inbound
// query into a vector before it can query the RediSearch index. If the same query is asked
// frequently, the LRU bypasses the expensive remote LLM embedding call, reducing latency
// by ~100-500ms and saving fiat costs.
type lruEmbedder struct {
	inner Embedder
	size  int
	mu    sync.Mutex
	ll    *list.List
	m     map[lruKey]*list.Element
}

// NewLRUEmbedder initializes an in-memory LRU cache over an upstream embedding provider.
func NewLRUEmbedder(inner Embedder, size int) Embedder {
	return &lruEmbedder{inner: inner, size: size, ll: list.New(), m: map[lruKey]*list.Element{}}
}

// Embed checks the in-memory LRU for a previously computed vector of the text.
// On a cache miss, it calls the upstream Embedder and caches the resulting vector.
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

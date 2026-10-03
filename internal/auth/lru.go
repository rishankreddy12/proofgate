// Package auth provides enterprise-grade capabilities, configuration, and structural components for the auth subsystem.
package auth

import (
	"container/list"
	"sync"
	"time"
)

// lruItem represents an individual entry in the LRUCache.
type lruItem[V any] struct {
	key     string
	val     V
	expires time.Time
}

// LRUCache implements a generic, thread-safe Least Recently Used (LRU) cache
// featuring strict capacity bounds and Time-To-Live (TTL) expiration semantics.
//
// Concurrency Model: All operations are synchronized via a single sync.Mutex, guaranteeing
// thread-safety across concurrent goroutines in high-throughput HTTP handlers.
//
// Complexity:
// - Get: O(1) expected time.
// - Set: O(1) expected time (including eviction).
// - Remove/Purge: O(1) expected time.
type LRUCache[V any] struct {
	mu        sync.Mutex
	capacity  int
	ttl       time.Duration
	items     map[string]*list.Element
	evictList *list.List
}

// NewLRU instantiates a new LRUCache with bounded capacity and a uniform per-entry TTL.
// A ttl <= 0 disables temporal expiration entirely (entries are only evicted via capacity limits).
// Default capacity is implicitly bounded to 1,000 items if an invalid value is supplied.
func NewLRU[V any](capacity int, ttl time.Duration) *LRUCache[V] {
	if capacity <= 0 {
		capacity = 1000
	}
	return &LRUCache[V]{
		capacity:  capacity,
		ttl:       ttl,
		items:     make(map[string]*list.Element, capacity),
		evictList: list.New(),
	}
}

// Get retrieves the value associated with the specified key.
// It actively enforces TTL expiration on reads: if the entry exists but has surpassed its TTL,
// it is synchronously evicted and a miss is returned.
// A successful read promotes the element to the front of the eviction list (marking it most-recently-used).
func (c *LRUCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	item := el.Value.(*lruItem[V])
	if c.ttl > 0 && time.Now().After(item.expires) {
		c.evictElement(el) // Active expiration on read
		var zero V
		return zero, false
	}
	c.evictList.MoveToFront(el)
	return item.val, true
}

// Set inserts or completely overrides an entry in the LRU cache.
// Overriding an existing key promotes it to the front of the list and refreshes its TTL.
// If the insertion causes the cache to exceed its defined capacity, the least-recently-used
// elements are synchronously evicted until the constraint is satisfied.
func (c *LRUCache[V]) Set(key string, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expires time.Time
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}

	// Update path
	if el, ok := c.items[key]; ok {
		c.evictList.MoveToFront(el)
		item := el.Value.(*lruItem[V])
		item.val = val
		item.expires = expires
		return
	}

	// Eviction path (enforce capacity bounds)
	for c.evictList.Len() >= c.capacity {
		c.evictOldest()
	}

	// Insertion path
	item := &lruItem[V]{
		key:     key,
		val:     val,
		expires: expires,
	}
	el := c.evictList.PushFront(item)
	c.items[key] = el
}

// evictOldest removes the element at the back of the list (Least Recently Used).
func (c *LRUCache[V]) evictOldest() {
	el := c.evictList.Back()
	if el != nil {
		c.evictElement(el)
	}
}

// evictElement executes the raw memory deallocation from both the doubly-linked list
// and the underlying hash map index.
func (c *LRUCache[V]) evictElement(el *list.Element) {
	c.evictList.Remove(el)
	item := el.Value.(*lruItem[V])
	delete(c.items, item.key)
}

// Len returns the current number of items tracked in the cache.
func (c *LRUCache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}

// Remove explicitly targets a specific key for synchronous cache eviction.
func (c *LRUCache[V]) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.evictElement(el)
	}
}

// Purge acts as a global reset mechanism, synchronously dropping all map references
// and re-initializing the linked list. This allows the GC to sweep all underlying structs.
func (c *LRUCache[V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element, c.capacity)
	c.evictList.Init()
}

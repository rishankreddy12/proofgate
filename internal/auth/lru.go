package auth

import (
	"container/list"
	"sync"
	"time"
)

type lruItem[V any] struct {
	key     string
	val     V
	expires time.Time
}

// LRUCache is a generic, thread-safe LRU cache with optional TTL expiration.
type LRUCache[V any] struct {
	mu        sync.Mutex
	capacity  int
	ttl       time.Duration
	items     map[string]*list.Element
	evictList *list.List
}

// NewLRU creates a new LRUCache with bounded capacity and per-entry TTL.
// A ttl <= 0 means entries do not expire by time.
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

// Get returns the value for key if present and not expired.
// If the entry has expired, it is removed and (zero, false) is returned.
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
		c.evictElement(el)
		var zero V
		return zero, false
	}
	c.evictList.MoveToFront(el)
	return item.val, true
}

// Set adds or updates an entry in the LRU cache with current timestamp + TTL.
func (c *LRUCache[V]) Set(key string, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expires time.Time
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}

	if el, ok := c.items[key]; ok {
		c.evictList.MoveToFront(el)
		item := el.Value.(*lruItem[V])
		item.val = val
		item.expires = expires
		return
	}

	for c.evictList.Len() >= c.capacity {
		c.evictOldest()
	}

	item := &lruItem[V]{
		key:     key,
		val:     val,
		expires: expires,
	}
	el := c.evictList.PushFront(item)
	c.items[key] = el
}

func (c *LRUCache[V]) evictOldest() {
	el := c.evictList.Back()
	if el != nil {
		c.evictElement(el)
	}
}

func (c *LRUCache[V]) evictElement(el *list.Element) {
	c.evictList.Remove(el)
	item := el.Value.(*lruItem[V])
	delete(c.items, item.key)
}

// Len returns the number of items currently in the cache.
func (c *LRUCache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}

// Remove removes key from the cache if present.
func (c *LRUCache[V]) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.evictElement(el)
	}
}

// Purge evicts all entries from the cache.
func (c *LRUCache[V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element, c.capacity)
	c.evictList.Init()
}

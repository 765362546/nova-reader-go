// Package cache provides a small, thread-safe LRU cache used by the hot
// book/source paths to avoid unbounded in-memory maps.
package cache

import (
	"container/list"
	"sync"
)

type entry[K comparable, V any] struct {
	key   K
	value V
}

// LRU is a fixed-capacity least-recently-used cache. It is safe for concurrent
// use. When the capacity is exceeded the least recently used entry is evicted.
type LRU[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	items    map[K]*list.Element
}

// New creates an LRU holding at most capacity entries.
func New[K comparable, V any](capacity int) *LRU[K, V] {
	if capacity <= 0 {
		capacity = 1
	}
	return &LRU[K, V]{
		capacity: capacity,
		order:    list.New(),
		items:    make(map[K]*list.Element, capacity),
	}
}

// Get returns the cached value and marks it as recently used.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	e, ok := c.items[key]
	if !ok {
		return zero, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*entry[K, V]).value, true
}

// Put stores value under key, evicting the oldest entry when full.
func (c *LRU[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		e.Value.(*entry[K, V]).value = value
		c.order.MoveToFront(e)
		return
	}
	e := c.order.PushFront(&entry[K, V]{key: key, value: value})
	c.items[key] = e
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*entry[K, V]).key)
		}
	}
}

// Delete removes key from the cache.
func (c *LRU[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.Remove(e)
		delete(c.items, key)
	}
}

// DeleteWhere removes every entry whose key matches pred. It is intended for
// rare bulk invalidations (for example wiping one book's cached chapters).
func (c *LRU[K, V]) DeleteWhere(pred func(K) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.items {
		if pred(key) {
			c.order.Remove(e)
			delete(c.items, key)
		}
	}
}

// Len returns the number of cached entries.
func (c *LRU[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

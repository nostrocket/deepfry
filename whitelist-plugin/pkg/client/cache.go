package client

import (
	"container/list"
	"sync"
	"time"
)

// ttlCache is a bounded LRU cache with per-entry TTL. It caches whitelist
// decisions (true and false) so the plugin's stdin/stdout pipe — which
// serialises every event the relay accepts — does not block on a fresh
// HTTP round-trip per event.
//
// Negative results are cached too: a flood of non-whitelisted events would
// otherwise bypass the cache and keep the synchronous round-trip per event.
// Transient errors (network, non-2xx) are never cached so a momentary
// whitelist-server outage does not pin every pubkey to fail-closed for the
// full TTL.
type ttlCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	cap   int
	ll    *list.List
	items map[string]*list.Element
	now   func() time.Time
}

type cacheEntry struct {
	key       string
	value     bool
	expiresAt time.Time
}

func newTTLCache(size int, ttl time.Duration) *ttlCache {
	if size <= 0 {
		size = 1
	}
	return &ttlCache{
		ttl:   ttl,
		cap:   size,
		ll:    list.New(),
		items: make(map[string]*list.Element, size),
		now:   time.Now,
	}
}

func (c *ttlCache) Get(key string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return false, false
	}
	entry := el.Value.(*cacheEntry)
	if c.now().After(entry.expiresAt) {
		c.ll.Remove(el)
		delete(c.items, key)
		return false, false
	}
	c.ll.MoveToFront(el)
	return entry.value, true
}

func (c *ttlCache) Set(key string, value bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		entry := el.Value.(*cacheEntry)
		entry.value = value
		entry.expiresAt = c.now().Add(c.ttl)
		c.ll.MoveToFront(el)
		return
	}
	if c.ll.Len() >= c.cap {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheEntry).key)
		}
	}
	c.items[key] = c.ll.PushFront(&cacheEntry{
		key:       key,
		value:     value,
		expiresAt: c.now().Add(c.ttl),
	})
}

func (c *ttlCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// Package dedup provides a small, dependency-free bounded LRU used by the
// bloom plugin to remember recently-accepted event IDs so that duplicate
// copies of the same event (arriving from many upstream relays) can be
// rejected instead of re-accepted into strfry's writer queue.
//
// Concurrency model: NOT safe for concurrent use. LRU is designed for a
// single-goroutine caller — the bloom plugin's sequential stdin event loop
// (cmd/bloom/main.go's runEventLoop calls RouterHandler.Handle from exactly
// one goroutine). This mirrors the lock-free single-writer/many-reader
// contract documented in pkg/bloomgate/checker.go. Do NOT add a mutex; if a
// future caller needs concurrent access, that caller must synchronize
// externally.
package dedup

import "container/list"

// LRU is a bounded least-recently-used set of 32-byte keys (decoded Nostr
// event IDs). SeenOrAdd reports whether a key was already present, moving it
// to the front (most-recently-used) either way. When the set grows beyond
// capacity, the least-recently-used key is evicted.
//
// The zero value is not valid; use New.
type LRU struct {
	capacity int
	ll       *list.List
	items    map[[32]byte]*list.Element
}

// New creates an LRU bounded to capacity entries. If capacity <= 0 the
// returned LRU is disabled: SeenOrAdd always reports "new" (false) and never
// tracks or grows — this is the byte-identical-to-before-this-change path
// used when dedup_capacity is configured as 0 or negative.
func New(capacity int) *LRU {
	if capacity <= 0 {
		return &LRU{capacity: 0}
	}
	return &LRU{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[[32]byte]*list.Element, capacity),
	}
}

// SeenOrAdd reports whether key has been seen before. If key is already
// tracked, it is moved to the front (most-recently-used) and true is
// returned (duplicate). Otherwise key is inserted at the front and false is
// returned (new); if this insertion pushes the set beyond capacity, the
// least-recently-used key is evicted.
//
// When the LRU is disabled (capacity <= 0), SeenOrAdd is a no-op that always
// returns false.
func (l *LRU) SeenOrAdd(key [32]byte) bool {
	if l.capacity <= 0 {
		return false
	}

	if el, ok := l.items[key]; ok {
		l.ll.MoveToFront(el)
		return true
	}

	el := l.ll.PushFront(key)
	l.items[key] = el

	if l.ll.Len() > l.capacity {
		back := l.ll.Back()
		if back != nil {
			l.ll.Remove(back)
			delete(l.items, back.Value.([32]byte))
		}
	}

	return false
}

// Len reports the number of keys currently tracked. It never exceeds
// capacity.
func (l *LRU) Len() int {
	if l.capacity <= 0 {
		return 0
	}
	return l.ll.Len()
}

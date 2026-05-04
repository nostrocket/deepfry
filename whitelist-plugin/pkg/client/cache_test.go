package client

import (
	"testing"
	"time"
)

func TestTTLCache_HitAndMiss(t *testing.T) {
	c := newTTLCache(4, time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Fatal("empty cache returned hit")
	}
	c.Set("a", true)
	v, ok := c.Get("a")
	if !ok || !v {
		t.Fatalf("expected hit=true got hit=%v val=%v", ok, v)
	}
}

func TestTTLCache_NegativeIsCached(t *testing.T) {
	c := newTTLCache(4, time.Minute)
	c.Set("a", false)
	v, ok := c.Get("a")
	if !ok || v {
		t.Fatalf("expected cached false, got hit=%v val=%v", ok, v)
	}
}

func TestTTLCache_Expiry(t *testing.T) {
	c := newTTLCache(4, time.Minute)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }

	c.Set("a", true)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry should be live before TTL")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Fatal("entry should be expired after TTL")
	}
	if got := c.len(); got != 0 {
		t.Fatalf("expired entry not cleaned up: len=%d", got)
	}
}

func TestTTLCache_LRUEvictionOnOverflow(t *testing.T) {
	c := newTTLCache(2, time.Minute)
	c.Set("a", true)
	c.Set("b", true)
	// Touch "a" so "b" becomes the LRU.
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a missing")
	}
	c.Set("c", true) // evicts "b"

	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should still be present")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should be present")
	}
}

func TestTTLCache_OverwriteRefreshesTTL(t *testing.T) {
	c := newTTLCache(4, time.Minute)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }

	c.Set("a", true)
	now = now.Add(45 * time.Second)
	c.Set("a", false) // refresh
	now = now.Add(45 * time.Second)

	v, ok := c.Get("a")
	if !ok || v {
		t.Fatalf("expected refreshed false within TTL, got hit=%v val=%v", ok, v)
	}
}

package dedup

import "testing"

// keyAt builds a distinct [32]byte key by setting the last byte to i.
func keyAt(i byte) [32]byte {
	var k [32]byte
	k[31] = i
	return k
}

func TestLRU_DisabledOnZeroCapacity(t *testing.T) {
	l := New(0)
	k := keyAt(1)

	if dup := l.SeenOrAdd(k); dup {
		t.Fatalf("expected first SeenOrAdd on disabled LRU to report new (false), got dup")
	}
	if dup := l.SeenOrAdd(k); dup {
		t.Fatalf("expected disabled LRU to never report a dup, got dup=true")
	}
	if got := l.Len(); got != 0 {
		t.Fatalf("expected disabled LRU to never grow, Len()=%d", got)
	}
}

func TestLRU_DisabledOnNegativeCapacity(t *testing.T) {
	l := New(-5)
	k := keyAt(1)

	if dup := l.SeenOrAdd(k); dup {
		t.Fatalf("expected first SeenOrAdd on disabled LRU to report new (false), got dup")
	}
	if got := l.Len(); got != 0 {
		t.Fatalf("expected disabled LRU to never grow, Len()=%d", got)
	}
}

func TestLRU_FirstNewSecondDup(t *testing.T) {
	l := New(10)
	k := keyAt(1)

	if dup := l.SeenOrAdd(k); dup {
		t.Fatalf("expected first SeenOrAdd to report new (false), got dup")
	}
	if dup := l.SeenOrAdd(k); !dup {
		t.Fatalf("expected second SeenOrAdd of same key to report dup (true), got new")
	}
	if got := l.Len(); got != 1 {
		t.Fatalf("expected Len()=1, got %d", got)
	}
}

func TestLRU_EvictsOldestAtCapacity(t *testing.T) {
	const capacity = 3
	l := New(capacity)

	keys := []([32]byte){keyAt(1), keyAt(2), keyAt(3)}
	for _, k := range keys {
		if dup := l.SeenOrAdd(k); dup {
			t.Fatalf("expected new key %v to report false, got dup", k)
		}
	}
	if got := l.Len(); got != capacity {
		t.Fatalf("expected Len()=%d after filling to capacity, got %d", capacity, got)
	}

	// Insert one more distinct key -> oldest (keys[0]) must be evicted.
	overflow := keyAt(4)
	if dup := l.SeenOrAdd(overflow); dup {
		t.Fatalf("expected new overflow key to report false, got dup")
	}
	if got := l.Len(); got != capacity {
		t.Fatalf("expected Len() to stay at capacity %d after eviction, got %d", capacity, got)
	}

	// The most-recent keys (keys[1], keys[2], overflow) should still be
	// tracked as dups — only the oldest inserted key was evicted.
	if dup := l.SeenOrAdd(keys[1]); !dup {
		t.Fatalf("expected keys[1] to still report dup (true) after eviction of oldest, got new")
	}
	if dup := l.SeenOrAdd(keys[2]); !dup {
		t.Fatalf("expected keys[2] to still report dup (true) after eviction of oldest, got new")
	}
	if dup := l.SeenOrAdd(overflow); !dup {
		t.Fatalf("expected overflow key to still report dup (true), got new")
	}

	// The oldest key should have been forgotten: re-seeing it now reports
	// new. Checked last since SeenOrAdd re-touches keys and would otherwise
	// perturb the eviction order asserted above.
	if dup := l.SeenOrAdd(keys[0]); dup {
		t.Fatalf("expected evicted oldest key to report new (false) again, got dup")
	}
}

func TestLRU_TouchOnHitSurvivesEviction(t *testing.T) {
	const capacity = 3
	l := New(capacity)

	k1, k2, k3 := keyAt(1), keyAt(2), keyAt(3)
	l.SeenOrAdd(k1)
	l.SeenOrAdd(k2)
	l.SeenOrAdd(k3)

	// Touch k1 (re-see it) so it becomes most-recently-used, moving k2 to
	// become the least-recently-used entry.
	if dup := l.SeenOrAdd(k1); !dup {
		t.Fatalf("expected touching k1 to report dup (true), got new")
	}

	// Insert a new distinct key: without the touch, k1 would have been
	// evicted (it was inserted first); with the touch, k2 should be evicted
	// instead since it is now the least-recently-used.
	k4 := keyAt(4)
	if dup := l.SeenOrAdd(k4); dup {
		t.Fatalf("expected new key k4 to report false, got dup")
	}

	// k1 should have survived the eviction (touched to front).
	if dup := l.SeenOrAdd(k1); !dup {
		t.Fatalf("expected k1 to survive eviction after touch-on-hit, got new (evicted)")
	}

	// k2 should have been evicted (was least-recently-used after the touch).
	if dup := l.SeenOrAdd(k2); dup {
		t.Fatalf("expected k2 to have been evicted after touch-on-hit bumped k1, got dup")
	}
}

func TestLRU_LenNeverExceedsCapacity(t *testing.T) {
	const capacity = 5
	l := New(capacity)

	for i := byte(0); i < 20; i++ {
		l.SeenOrAdd(keyAt(i))
		if got := l.Len(); got > capacity {
			t.Fatalf("Len()=%d exceeded capacity %d after inserting key %d", got, capacity, i)
		}
	}
	if got := l.Len(); got != capacity {
		t.Fatalf("expected Len()=%d after inserting more than capacity distinct keys, got %d", capacity, got)
	}
}

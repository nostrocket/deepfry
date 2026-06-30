package bloomgate_test

import (
	"log"
	"os"
	"testing"
	"time"

	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/bloomgate"
	"whitelist-plugin/pkg/handler"
)

// Compile-time assertion: BloomChecker must satisfy handler.Checker.
var _ handler.Checker = (*bloomgate.BloomChecker)(nil)

// buildTestFilter builds a *bloom.Filter containing exactly the keys provided (as raw [32]byte).
func buildTestFilter(t *testing.T, keys ...[32]byte) *bloom.Filter {
	t.Helper()
	b := bloom.NewBuilder(uint(len(keys)+10), 0.01)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("bloom.Builder.Build: %v", err)
	}
	return f
}

// hexOf returns the 64-char lowercase hex string of a [32]byte key for use in IsWhitelisted.
func hexOf(k [32]byte) string {
	const hextable = "0123456789abcdef"
	var out [64]byte
	for i, b := range k {
		out[i*2] = hextable[b>>4]
		out[i*2+1] = hextable[b&0xf]
	}
	return string(out[:])
}

func logger() *log.Logger {
	return log.New(os.Stderr, "[test-bloom] ", 0)
}

// TestBloomCheckerQueryPassThrough: with a filter stored containing pubkey P,
// IsWhitelisted(hexOf(P)) returns (true,nil); for an absent pubkey Q returns (false,nil).
func TestBloomCheckerQueryPassThrough(t *testing.T) {
	var present [32]byte
	present[0] = 0xAA
	var absent [32]byte
	absent[0] = 0xBB

	f := buildTestFilter(t, present)
	c := bloomgate.NewBloomChecker(logger())
	c.Store(f)

	got, err := c.IsWhitelisted(hexOf(present))
	if err != nil {
		t.Fatalf("IsWhitelisted(present): unexpected error: %v", err)
	}
	if !got {
		t.Errorf("IsWhitelisted(present) = false; want true")
	}

	got, err = c.IsWhitelisted(hexOf(absent))
	if err != nil {
		t.Fatalf("IsWhitelisted(absent): unexpected error: %v", err)
	}
	if got {
		t.Errorf("IsWhitelisted(absent) = true; want false (key was not added)")
	}
}

// TestBloomCheckerBlockBeforeReady: calling IsWhitelisted before any filter is stored
// must block (not return) until a filter is provided from another goroutine.
func TestBloomCheckerBlockBeforeReady(t *testing.T) {
	var k [32]byte
	k[0] = 0x01

	c := bloomgate.NewBloomChecker(logger())

	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)

	// Launch a goroutine that will block until Store is called.
	go func() {
		ok, err := c.IsWhitelisted(hexOf(k))
		errCh <- err
		resultCh <- ok
	}()

	// Give the goroutine a moment to start and block.
	time.Sleep(30 * time.Millisecond)

	// Confirm it has NOT returned yet.
	select {
	case <-resultCh:
		t.Fatal("IsWhitelisted returned before Store was called; expected it to block")
	default:
		// good: still blocking
	}

	// Now unblock by storing a filter containing k.
	f := buildTestFilter(t, k)
	c.Store(f)

	// The goroutine should now unblock within a reasonable deadline.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("IsWhitelisted returned error after Store: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IsWhitelisted did not unblock after Store within 2s")
	}

	ok := <-resultCh
	if !ok {
		t.Errorf("IsWhitelisted(present key) = false after Store; want true")
	}
}

// TestBloomCheckerSubsequentCallsReturnImmediately: after Store, subsequent calls
// to IsWhitelisted return immediately (no blocking).
func TestBloomCheckerSubsequentCallsReturnImmediately(t *testing.T) {
	var k [32]byte
	k[0] = 0x02

	f := buildTestFilter(t, k)
	c := bloomgate.NewBloomChecker(logger())
	c.Store(f)

	// Call it a few times quickly; each must return immediately.
	for i := 0; i < 5; i++ {
		done := make(chan struct{})
		go func() {
			c.IsWhitelisted(hexOf(k)) //nolint:errcheck
			close(done)
		}()
		select {
		case <-done:
			// returned immediately — good
		case <-time.After(500 * time.Millisecond):
			t.Errorf("IsWhitelisted blocked on call %d after filter already stored", i+1)
		}
	}
}

// TestBloomCheckerStoreIdempotent: calling Store more than once must not panic,
// and the latest filter is the one queried.
func TestBloomCheckerStoreIdempotent(t *testing.T) {
	var first [32]byte
	first[0] = 0x10
	var second [32]byte
	second[0] = 0x20

	c := bloomgate.NewBloomChecker(logger())

	f1 := buildTestFilter(t, first)
	c.Store(f1) // first Store opens the ready gate

	f2 := buildTestFilter(t, second) // second filter, does NOT contain first
	c.Store(f2)                      // second Store must not panic

	// After the swap, 'second' is in-set and 'first' should not be (different filter).
	ok, err := c.IsWhitelisted(hexOf(second))
	if err != nil {
		t.Fatalf("IsWhitelisted(second): %v", err)
	}
	if !ok {
		t.Errorf("IsWhitelisted(second) = false after second Store; want true")
	}
}

// Package bloomgate provides BloomChecker — a handler.Checker implementation backed
// by an atomically-swappable *bloom.Filter with a cold-start "ready" gate.
//
// Concurrency model: single-writer / many-reader.
// The fetcher (Plan 02) is the only writer; it calls Store once per successful fetch.
// The event hot path (IsWhitelisted) reads the atomic pointer concurrently with zero locks.
//
// GATE-06 blocking contract: IsWhitelisted withholds its decision until the first filter
// is stored. When no filter has ever been stored it waits on the ready channel; it does
// NOT return (false, nil) and does NOT reject events. StrFry backpressures on the
// blocked plugin rather than admitting un-vetted events (fail-closed by withholding).
//
// HARD INVARIANT (D-02): this package never invokes the bitset global little-endian
// byte-order switch. All filter operations go through pkg/bloom boundaries.
package bloomgate

import (
	"log"
	"sync"
	"sync/atomic"

	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/handler"
)

// Compile-time assertion: BloomChecker must satisfy handler.Checker.
var _ handler.Checker = (*BloomChecker)(nil)

// BloomChecker queries an atomically-swappable *bloom.Filter on the per-event hot path.
// The zero value is not valid; use NewBloomChecker.
type BloomChecker struct {
	filter atomic.Pointer[bloom.Filter] // nil until first Store; single-writer/many-reader
	ready  chan struct{}                 // closed once the first filter is stored (GATE-06)
	once   sync.Once                    // guards the single close of ready
	logger *log.Logger
}

// NewBloomChecker initialises a BloomChecker. The ready gate is open only after the first
// successful Store call.
func NewBloomChecker(logger *log.Logger) *BloomChecker {
	return &BloomChecker{
		ready:  make(chan struct{}),
		logger: logger,
	}
}

// Store atomically swaps in a new filter and, on the first call, closes the ready channel
// so that any goroutines blocked in IsWhitelisted can proceed. Subsequent calls update the
// filter without closing ready again (sync.Once ensures the channel is closed at most once).
// Store may be called from any goroutine; the caller is responsible for ensuring only one
// goroutine calls Store at a time (single-writer contract).
func (c *BloomChecker) Store(f *bloom.Filter) {
	c.filter.Store(f)
	c.once.Do(func() {
		close(c.ready)
		if c.logger != nil {
			c.logger.Printf("[bloom-checker] ready gate opened; filter stored")
		}
	})
}

// IsWhitelisted reports whether pubkey is possibly present in the held bloom filter.
// It satisfies handler.Checker.
//
// If no filter has been stored yet (GATE-06), it blocks until Store is called from
// another goroutine. This withholds the decision — fail-closed by withholding — rather
// than returning (false, nil) which would reject the event, or panicking.
//
// Once a filter is held, it calls bloom.Filter.ContainsHex which owns the hex-decode
// boundary: bad length or non-hex input returns (false, nil) without error. A true result
// means "maybe-in-set → accept"; a false result means "not-in-set → reject" (D-12).
//
// No network code is present in this method. GATE-01/GATE-02 compliance: zero per-event HTTP.
func (c *BloomChecker) IsWhitelisted(pubkey string) (bool, error) {
	// Fast path: filter already loaded.
	if f := c.filter.Load(); f != nil {
		return f.ContainsHex(pubkey)
	}

	// Slow path: no filter yet — wait until the ready gate opens (D-06).
	<-c.ready

	// Re-load after the gate opens; Store guarantees a non-nil filter was set
	// before closing ready.
	f := c.filter.Load()
	return f.ContainsHex(pubkey)
}

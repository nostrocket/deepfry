// Package bloomgate fetcher provides BloomFetcher — a periodic conditional-GET service
// that fetches the bloom filter from the whitelist server's /bloom endpoint, parse-before-persists
// each valid 200 response, and atomically swaps the new filter into the BloomChecker.
//
// Lifecycle:
//   - Start(): loads disk-first (D-04), then launches the background ticker goroutine.
//   - Stop(): cancels context and waits for the goroutine.
//
// Wire contract consumed (Phase-2 D-06/D-07/D-08):
//   - 200: application/octet-stream DFBF body + ETag → parse, store, persist (D-07/D-08/D-09)
//   - 304: nothing changed — no swap, no disk write (D-09)
//   - 503: server has no filter yet — treat as transient, keep last good (D-08/D-10)
//
// HARD INVARIANT (D-02): this package never invokes the bitset global byte-order switch.
package bloomgate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/config"
)

// BloomFetcher performs periodic conditional-GET fetches against the whitelist server's
// /bloom endpoint, atomically swapping the BloomChecker's filter on a clean-parsed 200,
// and persisting the bytes to disk via temp+rename (D-08).
//
// The zero value is not valid; use NewBloomFetcher.
type BloomFetcher struct {
	checker    *BloomChecker  // single writer of checker.Store; many event-loop readers
	serverURL  string         // base URL of the whitelist server (no trailing slash)
	bloomPath  string         // ~/deepfry/bloom.dfbf — the persistence target
	interval   time.Duration  // how often to re-fetch
	retryCount int            // how many times to retry a failed fetch per cycle
	httpClient *http.Client   // shared transport for all fetches in this fetcher
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	logger     *log.Logger
}

// NewBloomFetcher constructs a BloomFetcher.
// cfg supplies serverURL, bloomPath, interval, timeout, and retryCount.
// The returned fetcher is idle until Start() is called.
func NewBloomFetcher(checker *BloomChecker, cfg *config.BloomConfig, logger *log.Logger) *BloomFetcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &BloomFetcher{
		checker:   checker,
		serverURL: cfg.ServerURL,
		bloomPath: cfg.BloomPath,
		interval:  cfg.BloomRefreshInterval,
		retryCount: cfg.RefreshRetryCount,
		httpClient: &http.Client{
			Timeout: cfg.BloomFetchTimeout,
		},
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
}

// Start loads the persisted bloom filter from disk first (D-04), making the checker
// ready immediately if the file is valid, then runs an initial synchronous fetch and
// launches the background ticker goroutine.
//
// Disk-first cold-start (D-04/D-05):
//   - If bloomPath holds a valid DFBF file, it is parsed and stored into the checker
//     immediately so the event loop unblocks before any network fetch.
//   - If the file is absent or corrupt, we continue — the initial fetch below will
//     attempt the server; if neither has a filter the checker stays un-ready (D-06).
func (f *BloomFetcher) Start() {
	// Phase 1: disk-first cold start.
	f.LoadDisk()

	// Phase 2: initial synchronous fetch from server.
	f.FetchOnce()

	// Phase 3: ticker goroutine for periodic refresh.
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		ticker := time.NewTicker(f.interval)
		defer ticker.Stop()
		for {
			select {
			case <-f.ctx.Done():
				return
			case <-ticker.C:
				f.FetchOnce()
			}
		}
	}()
}

// Stop cancels the fetcher context and waits for the goroutine to exit.
func (f *BloomFetcher) Stop() {
	f.cancel()
	f.wg.Wait()
}

// LoadDisk attempts to read and parse the persisted bloomPath file and, on success,
// calls checker.Store to make the checker ready immediately (D-04). It is exported
// for testability (disk-first cold-start test points the server at a closed port).
//
// A missing or corrupt file is silently ignored (D-05): the caller continues to
// attempt the server fetch.
func (f *BloomFetcher) LoadDisk() {
	file, err := os.Open(f.bloomPath)
	if err != nil {
		// Normal: no disk file yet.
		return
	}
	defer file.Close()

	filter, err := bloom.ReadFilter(file)
	if err != nil {
		f.logger.Printf("[bloom-fetcher] disk load failed (will fetch from server): %v", err)
		return
	}
	f.checker.Store(filter)
	f.logger.Printf("[bloom-fetcher] disk-first: loaded filter from %s", f.bloomPath)
}

// FetchOnce executes one conditional-GET cycle against serverURL/bloom with retry
// (bounded by retryCount, linear backoff per D-11). It is exported for testability.
//
// Decision tree per fetch attempt:
//   - 304: no-op (D-09 — current in-memory filter is already current).
//   - 503: treat as transient; keep last good, log (D-08/D-10).
//   - 200: parse body via bloom.ReadFilter (D-07 parse-before-persist); on success
//           call checker.Store and persist via temp+rename (D-08); on parse failure
//           discard and log (D-10, do NOT swap or write).
//   - other status / transport error: log and retry per D-11.
func (f *BloomFetcher) FetchOnce() {
	for attempt := 0; attempt <= f.retryCount; attempt++ {
		done, retry := f.doFetch()
		if done {
			return
		}
		if !retry {
			return
		}
		// Retryable error: check ctx first, then back off.
		if f.ctx.Err() != nil {
			f.logger.Printf("[bloom-fetcher] fetch cancelled")
			return
		}
		f.logger.Printf("[bloom-fetcher] fetch failed (attempt %d/%d); retrying", attempt+1, f.retryCount+1)
		if attempt < f.retryCount {
			select {
			case <-f.ctx.Done():
				f.logger.Printf("[bloom-fetcher] fetch cancelled during retry backoff")
				return
			case <-time.After(time.Second * time.Duration(attempt+1)):
			}
		}
	}
	f.logger.Printf("[bloom-fetcher] fetch failed after %d attempts; keeping last good filter", f.retryCount+1)
}

// doFetch performs a single conditional-GET attempt.
// Returns (done=true, retry=false) on 304 or successful 200.
// Returns (done=false, retry=true) on retryable errors (503, transport, unexpected status).
// Returns (done=true, retry=false) on parse failure after 200 (discard, no retry — body was delivered; retrying is unlikely to help).
func (f *BloomFetcher) doFetch() (done bool, retry bool) {
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.serverURL+"/bloom", nil)
	if err != nil {
		f.logger.Printf("[bloom-fetcher] build request: %v", err)
		return false, true
	}

	// Conditional GET: set If-None-Match to the current filter's ETag (D-09).
	if cur := f.checker.filter.Load(); cur != nil {
		req.Header.Set("If-None-Match", cur.ETag())
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		f.logger.Printf("[bloom-fetcher] transport error: %v", err)
		return false, true
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified: // 304
		// Nothing changed — keep current filter, no disk write (D-09).
		f.logger.Printf("[bloom-fetcher] 304 Not Modified — filter unchanged")
		return true, false

	case http.StatusServiceUnavailable: // 503
		// Server has no filter yet — treat as transient; keep last good (D-08/D-10).
		f.logger.Printf("[bloom-fetcher] 503 Service Unavailable — server loading, keeping last good filter")
		return false, true

	case http.StatusOK: // 200
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			f.logger.Printf("[bloom-fetcher] 200: read body error: %v", err)
			return false, true
		}

		// Parse BEFORE persist (D-07): validate magic + version + payloadLen.
		filter, err := bloom.ReadFilter(bytes.NewReader(body))
		if err != nil {
			// Corrupt/truncated body — discard, keep last good, do NOT touch disk (D-10).
			f.logger.Printf("[bloom-fetcher] 200: parse failed (discarding, keeping last good): %v", err)
			return true, false // done; retrying the same corrupt body won't help
		}

		// Atomic in-memory swap (D-07/GATE-03).
		f.checker.Store(filter)

		// Persist via temp+rename (D-08). A persist failure logs but does NOT undo the swap.
		if err := f.persist(body); err != nil {
			f.logger.Printf("[bloom-fetcher] persist error (filter already swapped in memory): %v", err)
		}

		f.logger.Printf("[bloom-fetcher] 200: filter stored and persisted to %s", f.bloomPath)
		return true, false

	default:
		f.logger.Printf("[bloom-fetcher] unexpected status %d", resp.StatusCode)
		return false, true
	}
}

// persist writes body bytes to bloomPath atomically via temp+rename (D-08).
// On success bloomPath is updated and the .tmp file is gone.
// On failure the .tmp file may remain; bloomPath is untouched.
func (f *BloomFetcher) persist(body []byte) error {
	tmpPath := f.bloomPath + ".tmp"

	tf, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp file %s: %w", tmpPath, err)
	}

	if _, err := tf.Write(body); err != nil {
		tf.Close()
		os.Remove(tmpPath) //nolint:errcheck
		return fmt.Errorf("write tmp file %s: %w", tmpPath, err)
	}

	if err := tf.Close(); err != nil {
		os.Remove(tmpPath) //nolint:errcheck
		return fmt.Errorf("close tmp file %s: %w", tmpPath, err)
	}

	// Atomic rename (same-filesystem assumption: bloomPath and .tmp are in the same dir).
	if err := os.Rename(tmpPath, f.bloomPath); err != nil {
		os.Remove(tmpPath) //nolint:errcheck
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, f.bloomPath, err)
	}

	return nil
}

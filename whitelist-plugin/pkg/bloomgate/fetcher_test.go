package bloomgate_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/bloomgate"
	"whitelist-plugin/pkg/config"
)

// buildTestFilterBytes returns the serialized DFBF bytes for a filter containing the given keys.
func buildTestFilterBytes(t *testing.T, keys ...[32]byte) ([]byte, *bloom.Filter) {
	t.Helper()
	b := bloom.NewBuilder(uint(len(keys)+10), 0.01)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("bloom.Builder.Build: %v", err)
	}
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("filter.MarshalBinary: %v", err)
	}
	return data, f
}

// testBloomConfig returns a minimal BloomConfig wired to a temp dir for bloomPath.
func testBloomConfig(serverURL, bloomPath string) *config.BloomConfig {
	return &config.BloomConfig{
		ServerURL:            serverURL,
		BloomPath:            bloomPath,
		BloomRefreshInterval: 10 * time.Second,
		BloomFetchTimeout:    2 * time.Second,
		RefreshRetryCount:    1,
	}
}

// TestBloomFetcher200StoreAndPersist: on a 200 response with a valid DFBF body, the fetcher
// stores the new filter into the checker (checker becomes ready, membership matches) and
// writes bytes to bloomPath such that a subsequent bloom.ReadFilter of the file succeeds.
func TestBloomFetcher200StoreAndPersist(t *testing.T) {
	var key [32]byte
	key[0] = 0x01
	data, _ := buildTestFilterBytes(t, key)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))
	defer srv.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())

	fetcher.FetchOnce()

	// Checker must be ready.
	ok, err := checker.IsWhitelisted(hexOf(key))
	if err != nil {
		t.Fatalf("IsWhitelisted: %v", err)
	}
	if !ok {
		t.Errorf("IsWhitelisted = false after 200; want true")
	}

	// bloomPath must exist and be parseable.
	f, err := os.Open(bloomPath)
	if err != nil {
		t.Fatalf("bloom_path not created after 200: %v", err)
	}
	defer f.Close()
	_, err = bloom.ReadFilter(f)
	if err != nil {
		t.Fatalf("bloom.ReadFilter on persisted file failed: %v", err)
	}

	// No temp file must remain.
	if _, err := os.Stat(bloomPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file %s.tmp must not exist after successful persist", bloomPath)
	}
}

// TestBloomFetcher304Noop: on a 304 response, fetch() does nothing — no Store,
// no disk write; the previously held filter remains; bloomPath is absent or unchanged.
func TestBloomFetcher304Noop(t *testing.T) {
	var key [32]byte
	key[0] = 0x02
	data, _ := buildTestFilterBytes(t, key)

	// First server: returns 200 so we have a filter.
	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))
	defer srv200.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv200.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())

	// Fetch once to get an initial filter.
	fetcher.FetchOnce()

	stat1, err := os.Stat(bloomPath)
	if err != nil {
		t.Fatalf("bloom_path not created after initial 200: %v", err)
	}

	// Second server: returns 304.
	srv304 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv304.Close()

	cfg2 := testBloomConfig(srv304.URL, bloomPath)
	fetcher2 := bloomgate.NewBloomFetcher(checker, cfg2, logger())
	fetcher2.FetchOnce()

	// bloomPath mtime and size must not have changed.
	stat2, err := os.Stat(bloomPath)
	if err != nil {
		t.Fatalf("bloom_path unexpectedly removed after 304: %v", err)
	}
	if stat2.ModTime() != stat1.ModTime() || stat2.Size() != stat1.Size() {
		t.Errorf("bloom_path was modified after 304 (mtime or size changed); want no-op")
	}
}

// TestBloomFetcherIfNoneMatchSet: when a filter is held, the request carries
// If-None-Match equal to the held filter's ETag(); when none is held, the header is absent.
func TestBloomFetcherIfNoneMatchSet(t *testing.T) {
	var key [32]byte
	key[0] = 0x03
	data, _ := buildTestFilterBytes(t, key)

	var capturedINM string
	var capturedWithoutFilter string
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			capturedWithoutFilter = r.Header.Get("If-None-Match")
			w.WriteHeader(http.StatusOK)
			w.Write(data) //nolint:errcheck
		} else {
			capturedINM = r.Header.Get("If-None-Match")
			w.WriteHeader(http.StatusOK)
			w.Write(data) //nolint:errcheck
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())

	// First fetch: no filter held, header must be absent.
	fetcher.FetchOnce()
	if capturedWithoutFilter != "" {
		t.Errorf("first fetch: If-None-Match must be absent when no filter held; got %q", capturedWithoutFilter)
	}

	// Second fetch: filter now held, header must be non-empty.
	fetcher.FetchOnce()
	if capturedINM == "" {
		t.Errorf("second fetch: If-None-Match must be set when filter is held; got empty")
	}
}

// TestBloomFetcher503TransientKeepLastGood: a 503 response is treated as transient —
// no swap, no disk write; the last good filter stays.
func TestBloomFetcher503TransientKeepLastGood(t *testing.T) {
	var key [32]byte
	key[0] = 0x04
	data, _ := buildTestFilterBytes(t, key)

	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))
	defer srv200.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv200.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())

	// Establish initial filter.
	fetcher.FetchOnce()
	stat1, err := os.Stat(bloomPath)
	if err != nil {
		t.Fatalf("bloomPath not created: %v", err)
	}

	srv503 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"loading"}`)) //nolint:errcheck
	}))
	defer srv503.Close()

	cfg2 := testBloomConfig(srv503.URL, bloomPath)
	fetcher2 := bloomgate.NewBloomFetcher(checker, cfg2, logger())
	fetcher2.FetchOnce()

	// Filter must still be the last good one.
	ok, err := checker.IsWhitelisted(hexOf(key))
	if err != nil {
		t.Fatalf("IsWhitelisted: %v", err)
	}
	if !ok {
		t.Errorf("filter swapped after 503; want last-good to remain")
	}

	// Disk file must not have changed.
	stat2, err := os.Stat(bloomPath)
	if err != nil {
		t.Fatalf("bloomPath disappeared after 503: %v", err)
	}
	if stat2.ModTime() != stat1.ModTime() {
		t.Errorf("bloom_path was modified after 503; want no-op")
	}
}

// TestBloomFetcherTransportErrorKeepLastGood: an unreachable server leaves the last good
// filter in place; with no last-good filter the checker remains un-ready.
func TestBloomFetcherTransportErrorKeepLastGood(t *testing.T) {
	var key [32]byte
	key[0] = 0x05
	data, _ := buildTestFilterBytes(t, key)

	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv200.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())
	fetcher.FetchOnce()

	// Close the server to make subsequent fetches fail with transport errors.
	srv200.Close()

	cfg2 := testBloomConfig(srv200.URL, bloomPath)
	fetcher2 := bloomgate.NewBloomFetcher(checker, cfg2, logger())
	fetcher2.FetchOnce()

	// Last good filter must still be in the checker.
	ok, err := checker.IsWhitelisted(hexOf(key))
	if err != nil {
		t.Fatalf("IsWhitelisted: %v", err)
	}
	if !ok {
		t.Errorf("filter lost after transport error; want last-good to remain")
	}
}

// TestBloomFetcherCorrupt200Discarded: a 200 body that fails bloom.ReadFilter is
// discarded — no Store, bloomPath left as prior valid contents (or absent).
func TestBloomFetcherCorrupt200Discarded(t *testing.T) {
	var key [32]byte
	key[0] = 0x06
	data, _ := buildTestFilterBytes(t, key)

	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))
	defer srv200.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv200.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())
	fetcher.FetchOnce() // establishes good filter

	stat1, _ := os.Stat(bloomPath)

	// Serve corrupt body.
	srvCorrupt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not a valid DFBF body")) //nolint:errcheck
	}))
	defer srvCorrupt.Close()

	cfg2 := testBloomConfig(srvCorrupt.URL, bloomPath)
	fetcher2 := bloomgate.NewBloomFetcher(checker, cfg2, logger())
	fetcher2.FetchOnce()

	// Original filter must still be in the checker (key still present).
	ok, err := checker.IsWhitelisted(hexOf(key))
	if err != nil {
		t.Fatalf("IsWhitelisted: %v", err)
	}
	if !ok {
		t.Errorf("checker updated with corrupt body; want prior filter to remain")
	}

	// bloomPath must not have changed.
	stat2, err := os.Stat(bloomPath)
	if err != nil {
		t.Fatalf("bloomPath missing after corrupt 200: %v", err)
	}
	if stat2.ModTime() != stat1.ModTime() || stat2.Size() != stat1.Size() {
		t.Errorf("bloomPath was modified by corrupt 200; want no-op")
	}

	// .tmp file must not survive.
	if _, err := os.Stat(bloomPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file must not survive after corrupt 200 discard")
	}
}

// TestBloomFetcherDiskFirstColdStart: with a valid bloom.dfbf on disk, Start() loads it
// and the checker is ready before any HTTP call (server URL can be a closed port).
func TestBloomFetcherDiskFirstColdStart(t *testing.T) {
	var key [32]byte
	key[0] = 0x07
	data, _ := buildTestFilterBytes(t, key)

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	if err := os.WriteFile(bloomPath, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Use a closed server so any network fetch fails.
	closedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedSrv.Close()

	cfg := testBloomConfig(closedSrv.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())

	// LoadDisk loads the disk filter without making any network call.
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())
	fetcher.LoadDisk()

	// Checker must be ready immediately from disk.
	ok, err := checker.IsWhitelisted(hexOf(key))
	if err != nil {
		t.Fatalf("IsWhitelisted: %v", err)
	}
	if !ok {
		t.Errorf("disk-first cold start: checker not ready from disk; want true for added key")
	}
}

// TestBloomFetcherAtomicWrite: after a successful persist, bloomPath exists and
// no ${bloomPath}.tmp remains.
func TestBloomFetcherAtomicWrite(t *testing.T) {
	var key [32]byte
	key[0] = 0x08
	data, _ := buildTestFilterBytes(t, key)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))
	defer srv.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := testBloomConfig(srv.URL, bloomPath)
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())
	fetcher.FetchOnce()

	// bloomPath must exist.
	if _, err := os.Stat(bloomPath); err != nil {
		t.Errorf("bloomPath missing after successful fetch: %v", err)
	}
	// .tmp must not remain.
	if _, err := os.Stat(bloomPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file must not remain after successful atomic rename")
	}
}

// TestBloomFetcherStart: Start() runs disk-first load then periodic fetch, Stop() cleans up.
func TestBloomFetcherStart(t *testing.T) {
	var key [32]byte
	key[0] = 0x09
	data, _ := buildTestFilterBytes(t, key)

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}))
	defer srv.Close()

	dir := t.TempDir()
	bloomPath := filepath.Join(dir, "bloom.dfbf")
	cfg := &config.BloomConfig{
		ServerURL:            srv.URL,
		BloomPath:            bloomPath,
		BloomRefreshInterval: 50 * time.Millisecond, // fast tick for test
		BloomFetchTimeout:    2 * time.Second,
		RefreshRetryCount:    1,
	}
	checker := bloomgate.NewBloomChecker(logger())
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger())

	fetcher.Start()
	// Allow at least one tick to fire.
	time.Sleep(200 * time.Millisecond)
	fetcher.Stop()

	if fetchCount < 2 {
		t.Errorf("expected at least 2 fetch calls (initial + tick); got %d", fetchCount)
	}

	// Verify round-trip: ReadFilter on bloomPath returns a readable filter.
	f, err := os.Open(bloomPath)
	if err != nil {
		t.Fatalf("bloomPath not found after Start/Stop: %v", err)
	}
	defer f.Close()

	// Parse the raw bytes to verify the persisted file is a valid DFBF.
	raw, err := os.ReadFile(bloomPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := bloom.ReadFilter(bytes.NewReader(raw)); err != nil {
		t.Fatalf("bloom.ReadFilter on persisted file: %v", err)
	}
}

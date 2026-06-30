package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/version"
	"whitelist-plugin/pkg/whitelist"
)

func makeKey(seed byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

func setupServer(keys [][32]byte, ready bool) (*WhitelistServer, *httptest.Server) {
	logger := log.New(os.Stderr, "[test] ", 0)
	wl := whitelist.NewWhiteList(keys)
	s := NewWhitelistServer(wl, ":0", false, logger)
	if ready {
		s.SetReady(len(keys))
	}
	ts := httptest.NewServer(s.Handler())
	return s, ts
}

func TestHandleCheck_Whitelisted(t *testing.T) {
	k := makeKey(0x01)
	hexKey := hex.EncodeToString(k[:])
	_, ts := setupServer([][32]byte{k}, true)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/check/" + hexKey)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !body.Whitelisted {
		t.Fatal("expected whitelisted=true")
	}
}

func TestHandleCheck_NotWhitelisted(t *testing.T) {
	k := makeKey(0x01)
	hexKey := hex.EncodeToString(k[:])
	_, ts := setupServer(nil, true) // empty whitelist
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/check/" + hexKey)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var body checkResponse
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Whitelisted {
		t.Fatal("expected whitelisted=false")
	}
}

func TestHandleCheck_InvalidPubkey(t *testing.T) {
	_, ts := setupServer(nil, true)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/check/not-a-valid-hex-key")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 even for invalid key, got %d", resp.StatusCode)
	}

	var body checkResponse
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Whitelisted {
		t.Fatal("expected whitelisted=false for invalid key")
	}
}

func TestHandleBulkCheck(t *testing.T) {
	known := makeKey(0x01)
	knownHex := hex.EncodeToString(known[:])
	unknown := makeKey(0x99)
	unknownHex := hex.EncodeToString(unknown[:])

	_, ts := setupServer([][32]byte{known}, true)
	defer ts.Close()

	reqBody, _ := json.Marshal(bulkCheckRequest{
		Pubkeys: []string{knownHex, unknownHex, "not-a-valid-hex-key", ""},
	})
	resp, err := http.Post(ts.URL+"/check", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body bulkCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if !body.Results[knownHex] {
		t.Errorf("expected %s whitelisted=true", knownHex)
	}
	if body.Results[unknownHex] {
		t.Errorf("expected %s whitelisted=false", unknownHex)
	}
	if body.Results["not-a-valid-hex-key"] {
		t.Error("expected invalid key whitelisted=false")
	}
	// Empty pubkey is skipped, not included in results.
	if _, ok := body.Results[""]; ok {
		t.Error("expected empty pubkey to be skipped")
	}
	if len(body.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(body.Results))
	}
}

func TestHandleBulkCheck_InvalidJSON(t *testing.T) {
	_, ts := setupServer(nil, true)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/check", "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleHealth_Ready(t *testing.T) {
	_, ts := setupServer(nil, true)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleHealth_NotReady(t *testing.T) {
	_, ts := setupServer(nil, false) // not ready
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestHandleStats(t *testing.T) {
	k := makeKey(0x01)
	s, ts := setupServer([][32]byte{k}, false)
	defer ts.Close()

	// Before ready — entries should be 0
	resp, err := http.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var body statsResponse
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Entries != 0 {
		t.Fatalf("expected 0 entries before ready, got %d", body.Entries)
	}

	// After ready
	s.SetReady(1)
	resp2, err := http.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()

	var body2 statsResponse
	json.NewDecoder(resp2.Body).Decode(&body2)
	if body2.Entries != 1 {
		t.Fatalf("expected 1 entry, got %d", body2.Entries)
	}
	if body2.LastRefresh == "" {
		t.Fatal("expected non-empty last_refresh")
	}
}

func TestHandleBloom_NotReady(t *testing.T) {
	_, ts := setupServer(nil, false)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/bloom")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before filter is built, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["status"] != "loading" {
		t.Fatalf("expected status=loading, got %q", body["status"])
	}
}

func TestHandleBloom_OK(t *testing.T) {
	s, ts := setupServer(nil, false)
	defer ts.Close()

	// Build a real filter over makeKey-derived keys
	keys := [][32]byte{makeKey(0xAA), makeKey(0xBB), makeKey(0xCC)}
	b := bloom.NewBuilder(uint(len(keys)), 1e-6)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if err := s.SwapFilter(f); err != nil {
		t.Fatalf("SwapFilter failed: %v", err)
	}

	resp, err := http.Get(ts.URL + "/bloom")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("expected Content-Type application/octet-stream, got %q", ct)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected non-empty ETag header")
	}
	if etag != f.ETag() {
		t.Fatalf("ETag mismatch: got %q, want %q", etag, f.ETag())
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	if len(bodyBytes) == 0 {
		t.Fatal("expected non-empty body")
	}

	// Round-trip through bloom.ReadFilter and verify Contains
	f2, err := bloom.ReadFilter(bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("ReadFilter failed: %v", err)
	}
	for _, k := range keys {
		if !f2.Contains(k) {
			t.Errorf("round-tripped filter missing key %x", k)
		}
	}
}

func TestHandleBloom_NotModified(t *testing.T) {
	s, ts := setupServer(nil, false)
	defer ts.Close()

	keys := [][32]byte{makeKey(0xDD)}
	b := bloom.NewBuilder(uint(len(keys)), 1e-6)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if err := s.SwapFilter(f); err != nil {
		t.Fatalf("SwapFilter failed: %v", err)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/bloom", nil)
	req.Header.Set("If-None-Match", f.ETag())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 for matching ETag, got %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if len(bodyBytes) != 0 {
		t.Fatalf("expected empty body for 304, got %d bytes", len(bodyBytes))
	}
	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Fatal("expected ETag header on 304")
	}
}

func TestHandleBloom_StaleMismatchedETag(t *testing.T) {
	s, ts := setupServer(nil, false)
	defer ts.Close()

	keys := [][32]byte{makeKey(0xEE)}
	b := bloom.NewBuilder(uint(len(keys)), 1e-6)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if err := s.SwapFilter(f); err != nil {
		t.Fatalf("SwapFilter failed: %v", err)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/bloom", nil)
	req.Header.Set("If-None-Match", `"staleetag0000000000000000000000000000000000000000000000000000000000"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for mismatched ETag, got %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if len(bodyBytes) == 0 {
		t.Fatal("expected non-empty body for 200 response")
	}
}

func TestSetStats_LiveValues(t *testing.T) {
	s, ts := setupServer(nil, false)
	defer ts.Close()

	// SetStats should NOT flip ready — health should still 503
	healthResp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 health before SetReady, got %d", healthResp.StatusCode)
	}

	// Call SetStats with known values
	now := time.Now().Truncate(time.Second)
	s.SetStats(42, now)

	// Verify /stats shape unchanged and values live
	resp, err := http.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatalf("stats request failed: %v", err)
	}
	defer resp.Body.Close()

	var body statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Entries != 42 {
		t.Fatalf("expected entries=42 after SetStats, got %d", body.Entries)
	}
	if body.LastRefresh == "" {
		t.Fatal("expected non-empty last_refresh after SetStats")
	}

	// Health still 503 — SetStats must NOT have called s.ready.Store(true)
	healthResp2, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health request 2 failed: %v", err)
	}
	healthResp2.Body.Close()
	if healthResp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 health after SetStats (not SetReady), got %d", healthResp2.StatusCode)
	}
}

func TestHandleVersion(t *testing.T) {
	_, ts := setupServer(nil, true)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/version")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body version.BuildInfo
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Version != version.Version || body.Commit != version.Commit || body.Built != version.Built {
		t.Fatalf("mismatch: got %+v, want %+v", body, version.Info())
	}
}

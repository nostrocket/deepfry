package server

import (
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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

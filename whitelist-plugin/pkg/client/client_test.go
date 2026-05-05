package client

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestIsWhitelisted_True(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(checkResponse{Whitelisted: true})
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)

	ok, err := c.IsWhitelisted("aabbccdd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestIsWhitelisted_False(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(checkResponse{Whitelisted: false})
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)

	ok, err := c.IsWhitelisted("aabbccdd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestIsWhitelisted_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)

	ok, err := c.IsWhitelisted("aabbccdd")
	if err == nil {
		t.Fatal("expected error on server 5xx")
	}
	if ok {
		t.Fatal("expected false on server error")
	}
}

func TestIsWhitelisted_ConnectionRefused(t *testing.T) {
	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient("http://127.0.0.1:1", 1*time.Second, logger)

	ok, err := c.IsWhitelisted("aabbccdd")
	if err == nil {
		t.Fatal("expected error on connection refused")
	}
	if ok {
		t.Fatal("expected false on connection refused")
	}
}

func TestIsWhitelisted_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)

	ok, err := c.IsWhitelisted("aabbccdd")
	if err == nil {
		t.Fatal("expected error on malformed json")
	}
	if ok {
		t.Fatal("expected false on malformed json")
	}
}

func TestIsWhitelisted_PubkeyInPath(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(checkResponse{Whitelisted: true})
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)
	if _, err := c.IsWhitelisted("deadbeef1234"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "/check/deadbeef1234"
	if capturedPath != expected {
		t.Fatalf("expected path %q, got %q", expected, capturedPath)
	}
}

func TestIsWhitelisted_CacheHitSkipsHTTP(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(checkResponse{Whitelisted: true})
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)

	for i := 0; i < 5; i++ {
		ok, err := c.IsWhitelisted("aabbccdd")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if !ok {
			t.Fatalf("call %d: expected true", i)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", calls)
	}
}

func TestIsWhitelisted_TransientErrorsNotCached(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(checkResponse{Whitelisted: true})
	}))
	defer ts.Close()

	logger := log.New(os.Stderr, "[test] ", 0)
	c := NewWhitelistClient(ts.URL, 2*time.Second, logger)

	ok, err := c.IsWhitelisted("aabbccdd")
	if err == nil {
		t.Fatal("expected error on first (5xx) call")
	}
	if ok {
		t.Fatal("expected fail-closed false on first (5xx) call")
	}
	ok, err = c.IsWhitelisted("aabbccdd")
	if err != nil {
		t.Fatalf("unexpected error after server recovers: %v", err)
	}
	if !ok {
		t.Fatal("expected true on retry after server recovers")
	}
	if calls != 2 {
		t.Fatalf("transient failure should not be cached: calls=%d", calls)
	}
}

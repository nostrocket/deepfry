package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/version"
	"whitelist-plugin/pkg/whitelist"
)

// bloomEntry holds the pre-serialized filter and its ETag, swapped atomically
// on each successful refresh. nil until the first rebuild (D-03, D-05).
type bloomEntry struct {
	etag  string // Filter.ETag() — pre-computed quoted hex string
	bytes []byte // Filter.MarshalBinary() output — cached once per generation
}

type WhitelistServer struct {
	whitelist     *whitelist.Whitelist
	addr          string
	logger        *log.Logger
	debug         bool
	ready         atomic.Bool
	entries       atomic.Int64
	lastRefresh   atomic.Pointer[time.Time]
	bloomSnapshot atomic.Pointer[bloomEntry] // separate from whitelist.list (D-03)
}

func NewWhitelistServer(wl *whitelist.Whitelist, addr string, debug bool, logger *log.Logger) *WhitelistServer {
	return &WhitelistServer{
		whitelist: wl,
		addr:      addr,
		debug:     debug,
		logger:    logger,
	}
}

// SetReady marks the server as ready to serve traffic (whitelist loaded).
func (s *WhitelistServer) SetReady(entries int) {
	s.entries.Store(int64(entries))
	now := time.Now()
	s.lastRefresh.Store(&now)
	s.ready.Store(true)
}

// SetStats updates entries and last_refresh live values without flipping readiness.
// Readiness is set once at startup by SetReady; SetStats keeps the stats values
// current after each subsequent Dgraph refresh (D-10).
func (s *WhitelistServer) SetStats(n int, t time.Time) {
	s.entries.Store(int64(n))
	s.lastRefresh.Store(&t)
}

// SwapFilter serializes f once into a bloomEntry and stores it atomically.
// Pre-serializing here means the handleBloom handler is alloc-free per request (D-05).
func (s *WhitelistServer) SwapFilter(f *bloom.Filter) error {
	b, err := f.MarshalBinary()
	if err != nil {
		return err
	}
	s.bloomSnapshot.Store(&bloomEntry{etag: f.ETag(), bytes: b})
	return nil
}

// Handler returns the HTTP handler for use in testing.
func (s *WhitelistServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /check/{pubkey}", s.handleCheck)
	mux.HandleFunc("POST /check", s.handleBulkCheck)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /bloom", s.handleBloom)
	return mux
}

func (s *WhitelistServer) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:    s.addr,
		Handler: s.Handler(),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Printf("Whitelist server listening on %s", s.addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// handleBloom serves the serialized bloom filter.
// - nil snapshot → 503 JSON {"status":"loading","detail":"bloom filter not yet built"} (D-08)
// - If-None-Match matches current ETag → 304 Not Modified, ETag header, empty body (D-07)
// - otherwise → 200 application/octet-stream, ETag header, Content-Length, cached bytes (D-06)
func (s *WhitelistServer) handleBloom(w http.ResponseWriter, r *http.Request) {
	snap := s.bloomSnapshot.Load()
	if snap == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "loading",
			"detail": "bloom filter not yet built",
		})
		return
	}
	// Conditional GET (D-07)
	if r.Header.Get("If-None-Match") == snap.etag {
		w.Header().Set("ETag", snap.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", snap.etag)
	w.Header().Set("Content-Length", strconv.Itoa(len(snap.bytes)))
	w.Write(snap.bytes)
}

type checkResponse struct {
	Whitelisted bool `json:"whitelisted"`
}

// maxBulkPubkeys caps a single bulk request to bound memory and work.
const maxBulkPubkeys = 100000

// maxBulkBodyBytes caps the request body size. A 64-char hex pubkey plus JSON
// quoting/comma is ~67 bytes; 100k of them plus envelope fits comfortably in 8 MiB.
const maxBulkBodyBytes = 8 << 20

type bulkCheckRequest struct {
	Pubkeys []string `json:"pubkeys"`
}

type bulkCheckResponse struct {
	Results map[string]bool `json:"results"`
}

type statsResponse struct {
	Entries     int64  `json:"entries"`
	LastRefresh string `json:"last_refresh"`
}

func (s *WhitelistServer) handleCheck(w http.ResponseWriter, r *http.Request) {
	pubkey := r.PathValue("pubkey")
	if pubkey == "" {
		http.Error(w, "missing pubkey", http.StatusBadRequest)
		return
	}

	// In-memory whitelist never returns a non-nil error; ignored for parity
	// with the Checker interface used by the plugin client.
	result, _ := s.whitelist.IsWhitelisted(pubkey)

	if s.debug {
		s.logger.Printf("CHECK %s → %v", pubkey, result)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(checkResponse{Whitelisted: result})
}

// handleBulkCheck checks many pubkeys in one request. Body:
//
//	{"pubkeys": ["<hex>", ...]}
//
// Response:
//
//	{"results": {"<hex>": true|false, ...}}
//
// Each pubkey maps to its own boolean, so duplicates collapse and the caller
// can look up any pubkey it sent. Unknown/invalid pubkeys simply map to false.
func (s *WhitelistServer) handleBulkCheck(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBulkBodyBytes)

	var req bulkCheckRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Pubkeys) > maxBulkPubkeys {
		http.Error(w, "too many pubkeys (max "+strconv.Itoa(maxBulkPubkeys)+")", http.StatusRequestEntityTooLarge)
		return
	}

	results := make(map[string]bool, len(req.Pubkeys))
	for _, pubkey := range req.Pubkeys {
		if pubkey == "" {
			continue
		}
		// In-memory whitelist never returns a non-nil error; ignored for parity
		// with the single-key handler.
		whitelisted, _ := s.whitelist.IsWhitelisted(pubkey)
		results[pubkey] = whitelisted
	}

	if s.debug {
		s.logger.Printf("BULK CHECK %d pubkeys", len(req.Pubkeys))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bulkCheckResponse{Results: results})
}

func (s *WhitelistServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "loading",
			"detail": "populating whitelist from dgraph",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *WhitelistServer) handleStats(w http.ResponseWriter, r *http.Request) {
	lastRefresh := s.lastRefresh.Load()
	refreshStr := ""
	if lastRefresh != nil {
		refreshStr = lastRefresh.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statsResponse{
		Entries:     s.entries.Load(),
		LastRefresh: refreshStr,
	})
}

func (s *WhitelistServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(version.Info())
}

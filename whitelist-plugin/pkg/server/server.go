package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
	"whitelist-plugin/pkg/version"
	"whitelist-plugin/pkg/whitelist"
)

type WhitelistServer struct {
	whitelist   *whitelist.Whitelist
	addr        string
	logger      *log.Logger
	debug       bool
	ready       atomic.Bool
	entries     atomic.Int64
	lastRefresh atomic.Pointer[time.Time]
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

// Handler returns the HTTP handler for use in testing.
func (s *WhitelistServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /check/{pubkey}", s.handleCheck)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /version", s.handleVersion)
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

type checkResponse struct {
	Whitelisted bool `json:"whitelisted"`
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

	result := s.whitelist.IsWhitelisted(pubkey)

	if s.debug {
		s.logger.Printf("CHECK %s → %v", pubkey, result)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(checkResponse{Whitelisted: result})
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

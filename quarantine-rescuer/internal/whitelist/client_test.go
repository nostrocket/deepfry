package whitelist

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClient_CheckHealth(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"ready", http.StatusOK, false},
		{"loading", http.StatusServiceUnavailable, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			c := NewClient(srv.URL, time.Second, newSilentLogger())
			err := c.CheckHealth(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckHealth err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestClient_CheckHealth_Unreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", 100*time.Millisecond, newSilentLogger())
	if err := c.CheckHealth(context.Background()); err == nil {
		t.Fatal("expected error from unreachable server")
	}
}

func TestClient_IsWhitelisted(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		status     int
		want       bool
		wantPubkey string
	}{
		{"whitelisted", `{"whitelisted":true}`, 200, true, "abc"},
		{"not_whitelisted", `{"whitelisted":false}`, 200, false, "def"},
		{"server_error", "boom", 500, false, "xxx"},
		{"bad_json", "{not json}", 200, false, "yyy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/check/") {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				if got := strings.TrimPrefix(r.URL.Path, "/check/"); got != tt.wantPubkey {
					t.Errorf("pubkey path = %q want %q", got, tt.wantPubkey)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			c := NewClient(srv.URL, time.Second, newSilentLogger())
			got := c.IsWhitelisted(context.Background(), tt.wantPubkey)
			if got != tt.want {
				t.Fatalf("IsWhitelisted = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClient_IsWhitelisted_FailsClosedOnNetworkError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", 50*time.Millisecond, newSilentLogger())
	if c.IsWhitelisted(context.Background(), "abc") {
		t.Fatal("expected fail-closed (false) on network error")
	}
}

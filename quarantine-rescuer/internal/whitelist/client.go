// Package whitelist is a thin HTTP client for the deepfry whitelist server.
//
// It mirrors whitelist-plugin/pkg/client to keep this module self-contained
// (existing deepfry subsystems are independent Go modules with no
// cross-imports). The endpoints — GET /check/{pubkey} and GET /health —
// are defined by whitelist-plugin/pkg/server and must stay in sync.
package whitelist

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Client struct {
	serverURL  string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(serverURL string, timeout time.Duration, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	// Default MaxIdleConnsPerHost (2) is too low for the burst of concurrent
	// /check requests this client makes — unpooled connections close after
	// each request and pile up in TIME_WAIT, eventually exhausting the
	// ephemeral port range for the (local, server) tuple and surfacing as
	// EADDRNOTAVAIL ("can't assign requested address") on connect.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 32
	transport.MaxConnsPerHost = 32
	return &Client{
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: timeout, Transport: transport},
		logger:     logger,
	}
}

type checkResponse struct {
	Whitelisted bool `json:"whitelisted"`
}

// CheckHealth verifies the whitelist server is reachable and ready.
func (c *Client) CheckHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach whitelist server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whitelist server at %s returned %d", c.serverURL, resp.StatusCode)
	}
	return nil
}

// IsWhitelisted returns true iff the server says the pubkey is on the whitelist.
// Returns false on any error (fail-closed) — the caller is expected to have
// already verified server reachability via CheckHealth, so transient false
// negatives during a run only affect those events, not the whole batch.
func (c *Client) IsWhitelisted(ctx context.Context, pubkey string) bool {
	url := fmt.Sprintf("%s/check/%s", c.serverURL, pubkey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		c.logger.Warn("whitelist check: build request failed", "pubkey", pubkey, "err", err)
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("whitelist check failed", "pubkey", pubkey, "err", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("whitelist check: non-200 response", "pubkey", pubkey, "status", resp.StatusCode)
		return false
	}
	var body checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.logger.Warn("whitelist check: decode failed", "pubkey", pubkey, "err", err)
		return false
	}
	return body.Whitelisted
}

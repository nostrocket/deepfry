package client

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type WhitelistClient struct {
	serverURL  string
	httpClient *http.Client
	logger     *log.Logger
}

func NewWhitelistClient(serverURL string, timeout time.Duration, logger *log.Logger) *WhitelistClient {
	return &WhitelistClient{
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

type checkResponse struct {
	Whitelisted bool `json:"whitelisted"`
}

// CheckHealth calls the server's /health endpoint and returns any error.
func (c *WhitelistClient) CheckHealth() error {
	resp, err := c.httpClient.Get(c.serverURL + "/health")
	if err != nil {
		return fmt.Errorf("cannot reach whitelist server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whitelist server at %s returned %d", c.serverURL, resp.StatusCode)
	}
	return nil
}

// IsWhitelisted calls the whitelist server to check a pubkey.
// Returns false on any error (fail closed).
func (c *WhitelistClient) IsWhitelisted(pubkey string) bool {
	url := fmt.Sprintf("%s/check/%s", c.serverURL, pubkey)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		c.logger.Printf("Whitelist check failed for %s: %v", pubkey, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Printf("Whitelist check returned %d for %s", resp.StatusCode, pubkey)
		return false
	}

	var body checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.logger.Printf("Whitelist check decode failed for %s: %v", pubkey, err)
		return false
	}

	return body.Whitelisted
}

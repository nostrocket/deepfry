package config

import (
	"fmt"
	"time"
)

func (c *Config) validate() error {
	if c.SourceRelayURL == "" {
		return fmt.Errorf("%s is required", KeySourceRelayURL)
	}
	if c.DeepFryRelayURL == "" {
		return fmt.Errorf("%s is required", KeyDeepFryRelayURL)
	}
	if c.NostrSecretKey == "" {
		return fmt.Errorf("%s is required", KeyNostrSecretKey)
	}

	// Validate sync start time format if provided
	if c.Sync.StartTime != "" {
		if _, err := time.Parse(time.RFC3339, c.Sync.StartTime); err != nil {
			return fmt.Errorf("%s must be in RFC3339 format (e.g., 2020-01-01T00:00:00Z): %w", KeySyncStartTime, err)
		}
	}

	return nil
}

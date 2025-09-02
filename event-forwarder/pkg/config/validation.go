package config

import "fmt"

func (c *Config) validate() error {
	if c.SourceRelayURL == "" {
		return fmt.Errorf("SOURCE_RELAY_URL is required")
	}
	if c.DeepFryRelayURL == "" {
		return fmt.Errorf("DEEPFRY_RELAY_URL is required")
	}
	if c.NostrSecretKey == "" {
		return fmt.Errorf("NOSTR_SYNC_SECKEY is required")
	}
	return nil
}

package config

import "fmt"

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
	return nil
}

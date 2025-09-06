package config

import (
	"event-forwarder/pkg/testutil"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		cfg := &Config{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected validation error for empty config, got nil")
		}
	})

	t.Run("missing source relay", func(t *testing.T) {
		cfg := &Config{
			SourceRelayURL:  "",
			DeepFryRelayURL: "wss://deepfry.relay",
			NostrSecretKey:  testutil.TestSK,
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected validation error for missing source relay, got nil")
		}
	})

	t.Run("missing deepfry relay", func(t *testing.T) {
		cfg := &Config{
			SourceRelayURL:  "wss://source.relay",
			DeepFryRelayURL: "",
			NostrSecretKey:  testutil.TestSK,
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected validation error for missing deepfry relay, got nil")
		}
	})

	t.Run("missing secret key", func(t *testing.T) {
		cfg := &Config{
			SourceRelayURL:  "wss://source.relay",
			DeepFryRelayURL: "wss://deepfry.relay",
			NostrSecretKey:  "",
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected validation error for missing secret key, got nil")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{
			SourceRelayURL:  "wss://source.relay",
			DeepFryRelayURL: "wss://deepfry.relay",
			NostrSecretKey:  testutil.TestSK,
		}
		err := cfg.validate()
		if err != nil {
			t.Fatalf("expected no error for valid config, got %v", err)
		}
	})
}

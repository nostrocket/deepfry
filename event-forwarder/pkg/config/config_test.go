package config

import (
	"event-forwarder/pkg/testutil"
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("valid env vars", func(t *testing.T) {
		os.Setenv("SOURCE_RELAY_URL", "wss://source.relay")
		os.Setenv("DEEPFRY_RELAY_URL", "wss://deepfry.relay")
		os.Setenv("NOSTR_SYNC_SECKEY", testutil.TestSKHex)
		defer func() {
			os.Unsetenv("SOURCE_RELAY_URL")
			os.Unsetenv("DEEPFRY_RELAY_URL")
			os.Unsetenv("NOSTR_SYNC_SECKEY")
		}()

		cfg, err := Load()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if cfg.SourceRelayURL != "wss://source.relay" {
			t.Errorf("expected SourceRelayURL 'wss://source.relay', got %s", cfg.SourceRelayURL)
		}
	})

	t.Run("missing env vars", func(t *testing.T) {
		os.Unsetenv("SOURCE_RELAY_URL")
		os.Unsetenv("DEEPFRY_RELAY_URL")
		os.Unsetenv("NOSTR_SYNC_SECKEY")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing env vars, got nil")
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		cfg := &Config{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected validation error for empty config, got nil")
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

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		value        string
		defaultValue string
		expected     string
	}{
		{"existing key", "TEST_KEY", "value", "default", "value"},
		{"missing key", "MISSING_KEY", "", "default", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != "" {
				os.Setenv(tt.key, tt.value)
				defer os.Unsetenv(tt.key)
			}
			result := getEnv(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		value        string
		defaultValue int
		expected     int
	}{
		{"existing key", "TEST_INT", "42", 10, 42},
		{"missing key", "MISSING_INT", "", 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != "" {
				os.Setenv(tt.key, tt.value)
				defer os.Unsetenv(tt.key)
			}
			result := getEnvInt(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestGetEnvInt_Invalid(t *testing.T) {
	os.Setenv("TEST_INT_INVALID", "not-a-number")
	defer os.Unsetenv("TEST_INT_INVALID")

	result := getEnvInt("TEST_INT_INVALID", 10)
	if result != 10 {
		t.Errorf("expected default 10 for invalid int, got %d", result)
	}
}

func TestGetEnvFloat(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		defaultValue float64
		expected     float64
	}{
		{"existing key", "3.14", 1.0, 3.14},
		{"missing key", "", 1.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_FLOAT"
			if tt.value != "" {
				os.Setenv(key, tt.value)
				defer os.Unsetenv(key)
			}
			result := getEnvFloat(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, result)
			}
		})
	}
}

func TestGetEnvFloat_Invalid(t *testing.T) {
	os.Setenv("TEST_FLOAT_INVALID", "not-a-number")
	defer os.Unsetenv("TEST_FLOAT_INVALID")

	result := getEnvFloat("TEST_FLOAT_INVALID", 1.0)
	if result != 1.0 {
		t.Errorf("expected default 1.0 for invalid float, got %f", result)
	}
}

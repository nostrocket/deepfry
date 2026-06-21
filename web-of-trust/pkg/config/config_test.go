package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// TestLoadConfig_EjectionThresholdDefaults verifies that with no config file and
// a temp HOME, LoadConfig returns the documented default thresholds: transport=10,
// filter_rejection=3, subscription_flap=5.
func TestLoadConfig_EjectionThresholdDefaults(t *testing.T) {
	viper.Reset()
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RelayEjectionThresholds.Transport != 10 {
		t.Fatalf("transport threshold default: want 10, got %d", cfg.RelayEjectionThresholds.Transport)
	}
	if cfg.RelayEjectionThresholds.FilterRej != 3 {
		t.Fatalf("filter_rejection threshold default: want 3, got %d", cfg.RelayEjectionThresholds.FilterRej)
	}
	if cfg.RelayEjectionThresholds.SubFlap != 5 {
		t.Fatalf("subscription_flap threshold default: want 5, got %d", cfg.RelayEjectionThresholds.SubFlap)
	}
}

// TestLoadConfig_EjectionThresholdGuard verifies that when a config file sets
// non-positive threshold values, LoadConfig corrects them to their hardcoded
// defaults (transport 10, filter_rejection 3, subscription_flap 5).
func TestLoadConfig_EjectionThresholdGuard(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Write a config file with zero/negative thresholds.
	configDir := tmpHome + "/deepfry"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := `relay_urls:
  - wss://relay.damus.io
relay_ejection_thresholds:
  transport: 0
  filter_rejection: -1
  subscription_flap: 0
`
	if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.RelayEjectionThresholds.Transport != 10 {
		t.Fatalf("transport guard: want 10, got %d", cfg.RelayEjectionThresholds.Transport)
	}
	if cfg.RelayEjectionThresholds.FilterRej != 3 {
		t.Fatalf("filter_rejection guard: want 3, got %d", cfg.RelayEjectionThresholds.FilterRej)
	}
	if cfg.RelayEjectionThresholds.SubFlap != 5 {
		t.Fatalf("subscription_flap guard: want 5, got %d", cfg.RelayEjectionThresholds.SubFlap)
	}
}

// TestLoadConfig_EjectedRelaysAbsent verifies that with no ejected_relays key
// in the config, the loaded EjectedRelays field is non-nil (usable) and empty.
func TestLoadConfig_EjectedRelaysAbsent(t *testing.T) {
	viper.Reset()
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	// Must not be nil (must be usable as a slice without nil-check).
	if cfg.EjectedRelays == nil {
		t.Fatal("EjectedRelays should be non-nil (empty slice), got nil")
	}
	if len(cfg.EjectedRelays) != 0 {
		t.Fatalf("EjectedRelays should be empty, got %v", cfg.EjectedRelays)
	}
}

func TestLoadConfig_ThroughputControlDefaults(t *testing.T) {
	viper.Reset()
	t.Setenv("HOME", t.TempDir())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.RelayFilterBatchSize != 100 {
		t.Fatalf("relay_filter_batch_size default: want 100, got %d", cfg.RelayFilterBatchSize)
	}
	if cfg.FrontierBatchSize != 100 {
		t.Fatalf("frontier_batch_size default: want 100, got %d", cfg.FrontierBatchSize)
	}
	if cfg.CountSampleInterval != 100 {
		t.Fatalf("count_sample_interval default: want 100, got %d", cfg.CountSampleInterval)
	}
}

func TestLoadConfig_ThroughputControlExplicitValues(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := tmpHome + "/deepfry"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := `relay_urls:
  - wss://relay.damus.io
relay_filter_batch_size: 100
frontier_batch_size: 500
count_sample_interval: 5
`
	if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.RelayFilterBatchSize != 100 {
		t.Fatalf("relay_filter_batch_size: want 100, got %d", cfg.RelayFilterBatchSize)
	}
	if cfg.FrontierBatchSize != 500 {
		t.Fatalf("frontier_batch_size: want 500, got %d", cfg.FrontierBatchSize)
	}
	if cfg.CountSampleInterval != 5 {
		t.Fatalf("count_sample_interval: want 5, got %d", cfg.CountSampleInterval)
	}
}

func TestLoadConfig_ThroughputControlGuard(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := tmpHome + "/deepfry"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := `relay_urls:
  - wss://relay.damus.io
relay_filter_batch_size: 250
frontier_batch_size: 0
count_sample_interval: -2
`
	if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.FrontierBatchSize != 250 {
		t.Fatalf("frontier_batch_size guard: want relay filter fallback 250, got %d", cfg.FrontierBatchSize)
	}
	if cfg.CountSampleInterval != 1 {
		t.Fatalf("count_sample_interval guard: want 1, got %d", cfg.CountSampleInterval)
	}
}

// TestEjectRelayURL_MovesToEjected verifies that EjectRelayURL removes the URL
// from relay_urls and appends it to ejected_relays, persisting to the YAML file.
func TestEjectRelayURL_MovesToEjected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Write an initial config with two relay URLs.
	configDir := tmpHome + "/deepfry"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := `relay_urls:
  - wss://a
  - wss://b
`
	if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	_, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if err := EjectRelayURL("wss://a"); err != nil {
		t.Fatalf("EjectRelayURL returned error: %v", err)
	}

	// Verify via viper state.
	relayURLs := viper.GetStringSlice("relay_urls")
	ejected := viper.GetStringSlice("ejected_relays")

	for _, u := range relayURLs {
		if u == "wss://a" {
			t.Error("wss://a should have been removed from relay_urls")
		}
	}
	found := false
	for _, u := range ejected {
		if u == "wss://a" {
			found = true
		}
	}
	if !found {
		t.Errorf("wss://a should appear in ejected_relays, got %v", ejected)
	}

	// Verify wss://b is still in relay_urls.
	foundB := false
	for _, u := range relayURLs {
		if u == "wss://b" {
			foundB = true
		}
	}
	if !foundB {
		t.Errorf("wss://b should remain in relay_urls, got %v", relayURLs)
	}

	// Also verify the on-disk YAML contains ejected_relays.
	data, err := os.ReadFile(configDir + "/web-of-trust.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]interface{}
	if err := yaml.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	ejectedOnDisk, ok := onDisk["ejected_relays"]
	if !ok {
		t.Fatalf("ejected_relays not found in on-disk YAML: %s", string(data))
	}
	ejectedList, ok := ejectedOnDisk.([]interface{})
	if !ok {
		t.Fatalf("ejected_relays is not a list: %T", ejectedOnDisk)
	}
	diskFound := false
	for _, u := range ejectedList {
		if u == "wss://a" {
			diskFound = true
		}
	}
	if !diskFound {
		t.Errorf("wss://a not found in ejected_relays on disk: %v", ejectedList)
	}
}

// TestEjectRelayURL_AppendsNotReplaces verifies that ejecting a second URL
// appends to ejected_relays without dropping the first ejected URL.
func TestEjectRelayURL_AppendsNotReplaces(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := tmpHome + "/deepfry"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := `relay_urls:
  - wss://a
  - wss://b
  - wss://c
`
	if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	_, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if err := EjectRelayURL("wss://a"); err != nil {
		t.Fatalf("first EjectRelayURL returned error: %v", err)
	}
	if err := EjectRelayURL("wss://b"); err != nil {
		t.Fatalf("second EjectRelayURL returned error: %v", err)
	}

	ejected := viper.GetStringSlice("ejected_relays")

	foundA := false
	foundB := false
	for _, u := range ejected {
		if u == "wss://a" {
			foundA = true
		}
		if u == "wss://b" {
			foundB = true
		}
	}
	if !foundA {
		t.Errorf("wss://a should be in ejected_relays after first ejection, got %v", ejected)
	}
	if !foundB {
		t.Errorf("wss://b should be in ejected_relays after second ejection, got %v", ejected)
	}
}

// TestEjectRelayURL_Idempotent verifies that ejecting the same URL twice does
// not create a duplicate entry in ejected_relays — ejection must be idempotent
// so the list does not grow unbounded across restarts or repeat calls.
func TestEjectRelayURL_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := tmpHome + "/deepfry"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := `relay_urls:
  - wss://a
  - wss://b
`
	if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	if _, err := LoadConfig(); err != nil {
		t.Fatal(err)
	}

	// Eject twice. The second call has nothing to remove from relay_urls and
	// the URL is already in ejected_relays, so it must be a no-op.
	if err := EjectRelayURL("wss://a"); err != nil {
		t.Fatalf("first EjectRelayURL returned error: %v", err)
	}
	if err := EjectRelayURL("wss://a"); err != nil {
		t.Fatalf("second EjectRelayURL returned error: %v", err)
	}

	ejected := viper.GetStringSlice("ejected_relays")
	count := 0
	for _, u := range ejected {
		if u == "wss://a" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("wss://a should appear exactly once in ejected_relays, got %d (%v)", count, ejected)
	}
}

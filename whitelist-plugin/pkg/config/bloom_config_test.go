package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoadBloomConfigDefaults verifies that LoadBloomConfig returns the documented
// five defaults when no whitelist.yaml is present.
// It isolates HOME to a temp dir so it never touches the real ~/deepfry.
func TestLoadBloomConfigDefaults(t *testing.T) {
	// Isolate HOME so ensureConfigDir resolves into a temp dir, never ~/deepfry.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg, err := LoadBloomConfig()
	if err != nil {
		t.Fatalf("LoadBloomConfig() returned unexpected error: %v", err)
	}

	// ServerURL default: "http://localhost:8081" (D-02)
	if cfg.ServerURL != "http://localhost:8081" {
		t.Errorf("ServerURL = %q; want %q", cfg.ServerURL, "http://localhost:8081")
	}

	// BloomRefreshInterval default: 6h (D-03)
	if cfg.BloomRefreshInterval != 6*time.Hour {
		t.Errorf("BloomRefreshInterval = %v; want %v", cfg.BloomRefreshInterval, 6*time.Hour)
	}

	// BloomFetchTimeout default: 30s (D-03)
	if cfg.BloomFetchTimeout != 30*time.Second {
		t.Errorf("BloomFetchTimeout = %v; want %v", cfg.BloomFetchTimeout, 30*time.Second)
	}

	// RefreshRetryCount default: 3 (D-03)
	if cfg.RefreshRetryCount != 3 {
		t.Errorf("RefreshRetryCount = %d; want %d", cfg.RefreshRetryCount, 3)
	}

	// BloomPath default: must end with "bloom.dfbf" and be under tmpHome/deepfry (D-03)
	wantSuffix := "bloom.dfbf"
	if !strings.HasSuffix(cfg.BloomPath, wantSuffix) {
		t.Errorf("BloomPath = %q; want suffix %q", cfg.BloomPath, wantSuffix)
	}
	wantDir := filepath.Join(tmpHome, "deepfry")
	if !strings.HasPrefix(cfg.BloomPath, wantDir) {
		t.Errorf("BloomPath = %q; want it to be under %q", cfg.BloomPath, wantDir)
	}
}

// TestLoadBloomConfigOverrides verifies that values written to whitelist.yaml are reflected.
func TestLoadBloomConfigOverrides(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Write a whitelist.yaml with overrides into the expected config dir.
	configDir := filepath.Join(tmpHome, "deepfry")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	yaml := `server_url: "http://myserver:9000"
bloom_refresh_interval: "10m"
bloom_fetch_timeout: "45s"
refresh_retry_count: 5
bloom_path: "/tmp/custom.dfbf"
`
	if err := os.WriteFile(filepath.Join(configDir, "whitelist.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadBloomConfig()
	if err != nil {
		t.Fatalf("LoadBloomConfig() returned unexpected error: %v", err)
	}

	if cfg.ServerURL != "http://myserver:9000" {
		t.Errorf("ServerURL = %q; want %q", cfg.ServerURL, "http://myserver:9000")
	}
	if cfg.BloomRefreshInterval != 10*time.Minute {
		t.Errorf("BloomRefreshInterval = %v; want %v", cfg.BloomRefreshInterval, 10*time.Minute)
	}
	if cfg.BloomFetchTimeout != 45*time.Second {
		t.Errorf("BloomFetchTimeout = %v; want %v", cfg.BloomFetchTimeout, 45*time.Second)
	}
	if cfg.RefreshRetryCount != 5 {
		t.Errorf("RefreshRetryCount = %d; want %d", cfg.RefreshRetryCount, 5)
	}
	if cfg.BloomPath != "/tmp/custom.dfbf" {
		t.Errorf("BloomPath = %q; want %q", cfg.BloomPath, "/tmp/custom.dfbf")
	}
}

// TestLoadBloomConfigQuarantineDefaults verifies that LoadBloomConfig, with no
// whitelist.yaml present, defaults the quarantine side-channel section to the same
// values the router uses (M24-QUARANTINE-BLOOM).
func TestLoadBloomConfigQuarantineDefaults(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg, err := LoadBloomConfig()
	if err != nil {
		t.Fatalf("LoadBloomConfig() returned unexpected error: %v", err)
	}

	if !cfg.Quarantine.Enabled {
		t.Errorf("Quarantine.Enabled = %v; want true", cfg.Quarantine.Enabled)
	}
	if cfg.Quarantine.RelayURL != "ws://strfry-quarantine:7778" {
		t.Errorf("Quarantine.RelayURL = %q; want %q", cfg.Quarantine.RelayURL, "ws://strfry-quarantine:7778")
	}
	if cfg.Quarantine.BufferSize != 10000 {
		t.Errorf("Quarantine.BufferSize = %d; want %d", cfg.Quarantine.BufferSize, 10000)
	}
	if cfg.Quarantine.PublishTimeout != 5*time.Second {
		t.Errorf("Quarantine.PublishTimeout = %v; want %v", cfg.Quarantine.PublishTimeout, 5*time.Second)
	}
	if cfg.Quarantine.MetricsInterval != 60*time.Second {
		t.Errorf("Quarantine.MetricsInterval = %v; want %v", cfg.Quarantine.MetricsInterval, 60*time.Second)
	}
}

// TestLoadBloomConfigQuarantineOverride verifies that a nested quarantine section in
// whitelist.yaml overrides the defaults, and that disabling it is honoured. It also
// asserts the existing bloom_ defaults are untouched by the presence of the section.
func TestLoadBloomConfigQuarantineOverride(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := filepath.Join(tmpHome, "deepfry")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	yaml := `quarantine:
  enabled: false
  relay_url: "ws://elsewhere:9999"
  buffer_size: 42
`
	if err := os.WriteFile(filepath.Join(configDir, "whitelist.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadBloomConfig()
	if err != nil {
		t.Fatalf("LoadBloomConfig() returned unexpected error: %v", err)
	}

	if cfg.Quarantine.Enabled {
		t.Errorf("Quarantine.Enabled = %v; want false (override)", cfg.Quarantine.Enabled)
	}
	if cfg.Quarantine.RelayURL != "ws://elsewhere:9999" {
		t.Errorf("Quarantine.RelayURL = %q; want %q", cfg.Quarantine.RelayURL, "ws://elsewhere:9999")
	}
	if cfg.Quarantine.BufferSize != 42 {
		t.Errorf("Quarantine.BufferSize = %d; want %d", cfg.Quarantine.BufferSize, 42)
	}
	// Unset quarantine keys still fall back to defaults.
	if cfg.Quarantine.PublishTimeout != 5*time.Second {
		t.Errorf("Quarantine.PublishTimeout = %v; want default %v", cfg.Quarantine.PublishTimeout, 5*time.Second)
	}

	// Existing bloom_ defaults are unchanged by the quarantine section.
	if cfg.ServerURL != "http://localhost:8081" {
		t.Errorf("ServerURL = %q; want %q", cfg.ServerURL, "http://localhost:8081")
	}
	if cfg.BloomRefreshInterval != 6*time.Hour {
		t.Errorf("BloomRefreshInterval = %v; want %v", cfg.BloomRefreshInterval, 6*time.Hour)
	}
	if cfg.RefreshRetryCount != 3 {
		t.Errorf("RefreshRetryCount = %d; want %d", cfg.RefreshRetryCount, 3)
	}
}

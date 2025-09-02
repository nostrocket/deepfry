package config

import (
	"event-forwarder/pkg/testutil"
	"flag"
	"os"
	"testing"
)

func TestEnvSource(t *testing.T) {
	envSource := &EnvSource{}

	t.Run("GetString", func(t *testing.T) {
		// Test existing value
		os.Setenv("TEST_STRING", "test_value")
		defer os.Unsetenv("TEST_STRING")

		value, found := envSource.GetString("TEST_STRING")
		if !found {
			t.Error("expected to find TEST_STRING")
		}
		if value != "test_value" {
			t.Errorf("expected 'test_value', got '%s'", value)
		}

		// Test missing value
		value, found = envSource.GetString("MISSING_STRING")
		if found {
			t.Error("expected not to find MISSING_STRING")
		}
		if value != "" {
			t.Errorf("expected empty string, got '%s'", value)
		}
	})

	t.Run("GetInt", func(t *testing.T) {
		// Test valid int
		os.Setenv("TEST_INT", "42")
		defer os.Unsetenv("TEST_INT")

		value, found := envSource.GetInt("TEST_INT")
		if !found {
			t.Error("expected to find TEST_INT")
		}
		if value != 42 {
			t.Errorf("expected 42, got %d", value)
		}

		// Test invalid int
		os.Setenv("TEST_INVALID_INT", "not_a_number")
		defer os.Unsetenv("TEST_INVALID_INT")

		value, found = envSource.GetInt("TEST_INVALID_INT")
		if found {
			t.Error("expected not to find valid int for TEST_INVALID_INT")
		}

		// Test missing int
		value, found = envSource.GetInt("MISSING_INT")
		if found {
			t.Error("expected not to find MISSING_INT")
		}
	})

	t.Run("GetFloat", func(t *testing.T) {
		// Test valid float
		os.Setenv("TEST_FLOAT", "3.14")
		defer os.Unsetenv("TEST_FLOAT")

		value, found := envSource.GetFloat("TEST_FLOAT")
		if !found {
			t.Error("expected to find TEST_FLOAT")
		}
		if value != 3.14 {
			t.Errorf("expected 3.14, got %f", value)
		}

		// Test invalid float
		os.Setenv("TEST_INVALID_FLOAT", "not_a_number")
		defer os.Unsetenv("TEST_INVALID_FLOAT")

		value, found = envSource.GetFloat("TEST_INVALID_FLOAT")
		if found {
			t.Error("expected not to find valid float for TEST_INVALID_FLOAT")
		}

		// Test missing float
		value, found = envSource.GetFloat("MISSING_FLOAT")
		if found {
			t.Error("expected not to find MISSING_FLOAT")
		}
	})
}

func TestFlagSource(t *testing.T) {
	flagSource := NewFlagSource()

	t.Run("GetString", func(t *testing.T) {
		// Test setting and getting string
		flagSource.Set("TEST_STRING", "flag_value")
		value, found := flagSource.GetString("TEST_STRING")
		if !found {
			t.Error("expected to find TEST_STRING")
		}
		if value != "flag_value" {
			t.Errorf("expected 'flag_value', got '%s'", value)
		}

		// Test empty string
		flagSource.Set("EMPTY_STRING", "")
		value, found = flagSource.GetString("EMPTY_STRING")
		if found {
			t.Error("expected not to find empty string")
		}

		// Test missing key
		value, found = flagSource.GetString("MISSING_STRING")
		if found {
			t.Error("expected not to find MISSING_STRING")
		}
	})

	t.Run("GetInt", func(t *testing.T) {
		// Test setting and getting int
		flagSource.Set("TEST_INT", 42)
		value, found := flagSource.GetInt("TEST_INT")
		if !found {
			t.Error("expected to find TEST_INT")
		}
		if value != 42 {
			t.Errorf("expected 42, got %d", value)
		}

		// Test wrong type
		flagSource.Set("WRONG_TYPE", "not_int")
		value, found = flagSource.GetInt("WRONG_TYPE")
		if found {
			t.Error("expected not to find int for wrong type")
		}

		// Test missing key
		value, found = flagSource.GetInt("MISSING_INT")
		if found {
			t.Error("expected not to find MISSING_INT")
		}
	})

	t.Run("GetFloat", func(t *testing.T) {
		// Test setting and getting float
		flagSource.Set("TEST_FLOAT", 3.14)
		value, found := flagSource.GetFloat("TEST_FLOAT")
		if !found {
			t.Error("expected to find TEST_FLOAT")
		}
		if value != 3.14 {
			t.Errorf("expected 3.14, got %f", value)
		}

		// Test wrong type
		flagSource.Set("WRONG_TYPE", "not_float")
		value, found = flagSource.GetFloat("WRONG_TYPE")
		if found {
			t.Error("expected not to find float for wrong type")
		}

		// Test missing key
		value, found = flagSource.GetFloat("MISSING_FLOAT")
		if found {
			t.Error("expected not to find MISSING_FLOAT")
		}
	})
}

func TestConfigResolver(t *testing.T) {
	t.Run("precedence order", func(t *testing.T) {
		// Set up environment
		os.Setenv("TEST_KEY", "env_value")
		os.Setenv("ENV_ONLY", "env_value")
		defer func() {
			os.Unsetenv("TEST_KEY")
			os.Unsetenv("ENV_ONLY")
		}()

		// Set up flag source with higher precedence
		flagSource := NewFlagSource()
		flagSource.Set("TEST_KEY", "flag_value")

		// Create resolver with flag source first (higher precedence)
		resolver := NewConfigResolver(flagSource, &EnvSource{})

		// Test string resolution - flag should take precedence
		value := resolver.ResolveString("TEST_KEY", "default")
		if value != "flag_value" {
			t.Errorf("expected 'flag_value', got '%s'", value)
		}

		// Test fallback to env
		value = resolver.ResolveString("ENV_ONLY", "default")
		if value != "env_value" {
			t.Errorf("expected 'env_value', got '%s'", value)
		}

		// Test default value
		value = resolver.ResolveString("MISSING_KEY", "default")
		if value != "default" {
			t.Errorf("expected 'default', got '%s'", value)
		}
	})

	t.Run("int resolution", func(t *testing.T) {
		flagSource := NewFlagSource()
		flagSource.Set("TEST_INT", 100)

		os.Setenv("TEST_INT", "50")
		defer os.Unsetenv("TEST_INT")

		resolver := NewConfigResolver(flagSource, &EnvSource{})

		// Flag should take precedence
		value := resolver.ResolveInt("TEST_INT", 1)
		if value != 100 {
			t.Errorf("expected 100, got %d", value)
		}

		// Test default
		value = resolver.ResolveInt("MISSING_INT", 42)
		if value != 42 {
			t.Errorf("expected 42, got %d", value)
		}
	})

	t.Run("float resolution", func(t *testing.T) {
		flagSource := NewFlagSource()
		flagSource.Set("TEST_FLOAT", 2.71)

		os.Setenv("TEST_FLOAT", "3.14")
		defer os.Unsetenv("TEST_FLOAT")

		resolver := NewConfigResolver(flagSource, &EnvSource{})

		// Flag should take precedence
		value := resolver.ResolveFloat("TEST_FLOAT", 1.0)
		if value != 2.71 {
			t.Errorf("expected 2.71, got %f", value)
		}

		// Test default
		value = resolver.ResolveFloat("MISSING_FLOAT", 1.0)
		if value != 1.0 {
			t.Errorf("expected 1.0, got %f", value)
		}
	})
}

func TestLoad(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Reset flag for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	t.Run("valid env vars", func(t *testing.T) {
		os.Setenv("SOURCE_RELAY_URL", "wss://source.relay")
		os.Setenv("DEEPFRY_RELAY_URL", "wss://deepfry.relay")
		os.Setenv("NOSTR_SYNC_SECKEY", testutil.TestSKHex)
		defer func() {
			os.Unsetenv("SOURCE_RELAY_URL")
			os.Unsetenv("DEEPFRY_RELAY_URL")
			os.Unsetenv("NOSTR_SYNC_SECKEY")
		}()

		// Set args to empty to avoid parsing flags
		os.Args = []string{"test"}

		cfg, err := Load()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if cfg.SourceRelayURL != "wss://source.relay" {
			t.Errorf("expected SourceRelayURL 'wss://source.relay', got %s", cfg.SourceRelayURL)
		}
		if cfg.DeepFryRelayURL != "wss://deepfry.relay" {
			t.Errorf("expected DeepFryRelayURL 'wss://deepfry.relay', got %s", cfg.DeepFryRelayURL)
		}
		if cfg.NostrSecretKey != testutil.TestSKHex {
			t.Errorf("expected NostrSecretKey '%s', got %s", testutil.TestSKHex, cfg.NostrSecretKey)
		}

		// Test default values
		if cfg.Sync.WindowSeconds != 5 {
			t.Errorf("expected WindowSeconds 5, got %d", cfg.Sync.WindowSeconds)
		}
		if cfg.Network.BackoffJitter != 0.2 {
			t.Errorf("expected BackoffJitter 0.2, got %f", cfg.Network.BackoffJitter)
		}
	})

	t.Run("CLI override env vars", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Setenv("SOURCE_RELAY_URL", "wss://env.relay")
		os.Setenv("DEEPFRY_RELAY_URL", "wss://deepfry.relay")
		os.Setenv("NOSTR_SYNC_SECKEY", testutil.TestSKHex)
		os.Setenv("SYNC_WINDOW_SECONDS", "10")
		defer func() {
			os.Unsetenv("SOURCE_RELAY_URL")
			os.Unsetenv("DEEPFRY_RELAY_URL")
			os.Unsetenv("NOSTR_SYNC_SECKEY")
			os.Unsetenv("SYNC_WINDOW_SECONDS")
		}()

		// Set CLI args to override some env vars
		os.Args = []string{"test", "--source-relay-url=wss://cli.relay", "--sync-window-seconds=20"}

		cfg, err := Load()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// CLI should override env
		if cfg.SourceRelayURL != "wss://cli.relay" {
			t.Errorf("expected CLI override 'wss://cli.relay', got %s", cfg.SourceRelayURL)
		}
		if cfg.Sync.WindowSeconds != 20 {
			t.Errorf("expected CLI override WindowSeconds 20, got %d", cfg.Sync.WindowSeconds)
		}

		// Env should be used where no CLI override
		if cfg.DeepFryRelayURL != "wss://deepfry.relay" {
			t.Errorf("expected env value 'wss://deepfry.relay', got %s", cfg.DeepFryRelayURL)
		}
	})

	t.Run("missing required env vars", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Unsetenv("SOURCE_RELAY_URL")
		os.Unsetenv("DEEPFRY_RELAY_URL")
		os.Unsetenv("NOSTR_SYNC_SECKEY")

		os.Args = []string{"test"}

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing env vars, got nil")
		}
	})

	t.Run("validation error", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Setenv("SOURCE_RELAY_URL", "wss://source.relay")
		os.Setenv("DEEPFRY_RELAY_URL", "") // Missing required field
		os.Setenv("NOSTR_SYNC_SECKEY", testutil.TestSKHex)
		defer func() {
			os.Unsetenv("SOURCE_RELAY_URL")
			os.Unsetenv("DEEPFRY_RELAY_URL")
			os.Unsetenv("NOSTR_SYNC_SECKEY")
		}()

		os.Args = []string{"test"}

		_, err := Load()
		if err == nil {
			t.Fatal("expected validation error, got nil")
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

func TestNewFlagSource(t *testing.T) {
	flagSource := NewFlagSource()
	if flagSource == nil {
		t.Fatal("expected non-nil FlagSource")
	}
	if flagSource.values == nil {
		t.Fatal("expected non-nil values map")
	}
}

func TestNewConfigResolver(t *testing.T) {
	flagSource := NewFlagSource()
	envSource := &EnvSource{}

	resolver := NewConfigResolver(flagSource, envSource)
	if resolver == nil {
		t.Fatal("expected non-nil ConfigResolver")
	}
	if len(resolver.sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(resolver.sources))
	}
}

// Benchmark tests for performance
func BenchmarkEnvSourceGetString(b *testing.B) {
	os.Setenv("BENCH_STRING", "test_value")
	defer os.Unsetenv("BENCH_STRING")

	envSource := &EnvSource{}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		envSource.GetString("BENCH_STRING")
	}
}

func BenchmarkFlagSourceGetString(b *testing.B) {
	flagSource := NewFlagSource()
	flagSource.Set("BENCH_STRING", "test_value")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		flagSource.GetString("BENCH_STRING")
	}
}

func BenchmarkConfigResolverResolveString(b *testing.B) {
	flagSource := NewFlagSource()
	flagSource.Set("BENCH_STRING", "flag_value")

	os.Setenv("BENCH_STRING", "env_value")
	defer os.Unsetenv("BENCH_STRING")

	resolver := NewConfigResolver(flagSource, &EnvSource{})
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resolver.ResolveString("BENCH_STRING", "default")
	}
}

func TestParseCLIFlags(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	t.Run("empty args", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Args = []string{"test"}
		flagSource, showHelp := parseCLIFlags()

		if showHelp {
			t.Error("expected showHelp to be false for empty args")
		}
		if flagSource == nil {
			t.Fatal("expected non-nil flagSource")
		}

		// Test that empty flag source returns no values
		if value, found := flagSource.GetString("SOURCE_RELAY_URL"); found {
			t.Errorf("expected no value for SOURCE_RELAY_URL, got '%s'", value)
		}
	})

	t.Run("with values", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Args = []string{"test", "--source-relay-url=wss://test.relay", "--sync-window-seconds=15"}
		flagSource, showHelp := parseCLIFlags()

		if showHelp {
			t.Error("expected showHelp to be false")
		}

		// Test string value
		if value, found := flagSource.GetString("SOURCE_RELAY_URL"); !found || value != "wss://test.relay" {
			t.Errorf("expected 'wss://test.relay', got '%s' (found: %v)", value, found)
		}

		// Test int value
		if value, found := flagSource.GetInt("SYNC_WINDOW_SECONDS"); !found || value != 15 {
			t.Errorf("expected 15, got %d (found: %v)", value, found)
		}
	})
}

func TestLoadWithInvalidCrypto(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Reset flag for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	os.Setenv("SOURCE_RELAY_URL", "wss://source.relay")
	os.Setenv("DEEPFRY_RELAY_URL", "wss://deepfry.relay")
	os.Setenv("NOSTR_SYNC_SECKEY", "invalid_key")
	defer func() {
		os.Unsetenv("SOURCE_RELAY_URL")
		os.Unsetenv("DEEPFRY_RELAY_URL")
		os.Unsetenv("NOSTR_SYNC_SECKEY")
	}()

	os.Args = []string{"test"}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid crypto key, got nil")
	}
}

func TestComplexConfigValues(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Reset flag for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// Test all configuration values with CLI override
	os.Setenv("SOURCE_RELAY_URL", "wss://env.source.relay")
	os.Setenv("DEEPFRY_RELAY_URL", "wss://env.deepfry.relay")
	os.Setenv("NOSTR_SYNC_SECKEY", testutil.TestSKHex)
	os.Setenv("SYNC_WINDOW_SECONDS", "10")
	os.Setenv("SYNC_MAX_BATCH", "500")
	os.Setenv("SYNC_MAX_CATCHUP_LAG_SECONDS", "15")
	os.Setenv("NETWORK_INITIAL_BACKOFF_SECONDS", "2")
	os.Setenv("NETWORK_MAX_BACKOFF_SECONDS", "60")
	os.Setenv("NETWORK_BACKOFF_JITTER", "0.3")
	os.Setenv("TIMEOUT_PUBLISH_SECONDS", "15")
	os.Setenv("TIMEOUT_SUBSCRIBE_SECONDS", "20")
	defer func() {
		os.Unsetenv("SOURCE_RELAY_URL")
		os.Unsetenv("DEEPFRY_RELAY_URL")
		os.Unsetenv("NOSTR_SYNC_SECKEY")
		os.Unsetenv("SYNC_WINDOW_SECONDS")
		os.Unsetenv("SYNC_MAX_BATCH")
		os.Unsetenv("SYNC_MAX_CATCHUP_LAG_SECONDS")
		os.Unsetenv("NETWORK_INITIAL_BACKOFF_SECONDS")
		os.Unsetenv("NETWORK_MAX_BACKOFF_SECONDS")
		os.Unsetenv("NETWORK_BACKOFF_JITTER")
		os.Unsetenv("TIMEOUT_PUBLISH_SECONDS")
		os.Unsetenv("TIMEOUT_SUBSCRIBE_SECONDS")
	}()

	// Override some values with CLI
	os.Args = []string{"test",
		"--source-relay-url=wss://cli.source.relay",
		"--sync-window-seconds=25",
		"--network-backoff-jitter=0.5",
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check CLI overrides
	if cfg.SourceRelayURL != "wss://cli.source.relay" {
		t.Errorf("expected CLI override 'wss://cli.source.relay', got %s", cfg.SourceRelayURL)
	}
	if cfg.Sync.WindowSeconds != 25 {
		t.Errorf("expected CLI override WindowSeconds 25, got %d", cfg.Sync.WindowSeconds)
	}
	if cfg.Network.BackoffJitter != 0.5 {
		t.Errorf("expected CLI override BackoffJitter 0.5, got %f", cfg.Network.BackoffJitter)
	}

	// Check env values where no CLI override
	if cfg.DeepFryRelayURL != "wss://env.deepfry.relay" {
		t.Errorf("expected env value 'wss://env.deepfry.relay', got %s", cfg.DeepFryRelayURL)
	}
	if cfg.Sync.MaxBatch != 500 {
		t.Errorf("expected env value MaxBatch 500, got %d", cfg.Sync.MaxBatch)
	}
	if cfg.Sync.MaxCatchupLagSeconds != 15 {
		t.Errorf("expected env value MaxCatchupLagSeconds 15, got %d", cfg.Sync.MaxCatchupLagSeconds)
	}
	if cfg.Network.InitialBackoffSeconds != 2 {
		t.Errorf("expected env value InitialBackoffSeconds 2, got %d", cfg.Network.InitialBackoffSeconds)
	}
	if cfg.Network.MaxBackoffSeconds != 60 {
		t.Errorf("expected env value MaxBackoffSeconds 60, got %d", cfg.Network.MaxBackoffSeconds)
	}
	if cfg.Timeouts.PublishSeconds != 15 {
		t.Errorf("expected env value PublishSeconds 15, got %d", cfg.Timeouts.PublishSeconds)
	}
	if cfg.Timeouts.SubscribeSeconds != 20 {
		t.Errorf("expected env value SubscribeSeconds 20, got %d", cfg.Timeouts.SubscribeSeconds)
	}

	// Check that crypto key pair is properly derived
	if cfg.NostrKeyPair.PrivateKeyHex == "" {
		t.Error("expected non-empty PrivateKeyHex")
	}
	if cfg.NostrKeyPair.PublicKeyHex == "" {
		t.Error("expected non-empty PublicKeyHex")
	}
	if cfg.NostrKeyPair.PrivateKeyBech32 == "" {
		t.Error("expected non-empty PrivateKeyBech32")
	}
	if cfg.NostrKeyPair.PublicKeyBech32 == "" {
		t.Error("expected non-empty PublicKeyBech32")
	}
}

func TestPrintUsage(t *testing.T) {
	// Test that printUsage doesn't panic - we can't easily test output without major refactoring
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("printUsage panicked: %v", r)
		}
	}()

	// We'll just test that the function exists and can be called
	// In a real application, you might want to capture stdout for testing
	printUsage()
}

func TestFlagSourceEdgeCases(t *testing.T) {
	flagSource := NewFlagSource()

	t.Run("zero values", func(t *testing.T) {
		// Test that zero values are not considered "found"
		flagSource.Set("ZERO_INT", 0)
		flagSource.Set("ZERO_FLOAT", 0.0)

		if value, found := flagSource.GetInt("ZERO_INT"); !found || value != 0 {
			t.Errorf("expected to find zero int, got %d (found: %v)", value, found)
		}

		if value, found := flagSource.GetFloat("ZERO_FLOAT"); !found || value != 0.0 {
			t.Errorf("expected to find zero float, got %f (found: %v)", value, found)
		}
	})

	t.Run("wrong types stored", func(t *testing.T) {
		// Store wrong types and ensure they're not found
		flagSource.Set("WRONG_INT", "string_value")
		flagSource.Set("WRONG_FLOAT", 123)
		flagSource.Set("WRONG_STRING", 456)

		if _, found := flagSource.GetInt("WRONG_INT"); found {
			t.Error("expected not to find int for string value")
		}

		if _, found := flagSource.GetFloat("WRONG_FLOAT"); found {
			t.Error("expected not to find float for int value")
		}

		if _, found := flagSource.GetString("WRONG_STRING"); found {
			t.Error("expected not to find string for int value")
		}
	})
}

func TestEnvSourceEdgeCases(t *testing.T) {
	envSource := &EnvSource{}

	t.Run("empty env var", func(t *testing.T) {
		os.Setenv("EMPTY_VAR", "")
		defer os.Unsetenv("EMPTY_VAR")

		if _, found := envSource.GetString("EMPTY_VAR"); found {
			t.Error("expected not to find empty env var")
		}
	})

	t.Run("env var with spaces", func(t *testing.T) {
		os.Setenv("SPACES_VAR", "  ")
		defer os.Unsetenv("SPACES_VAR")

		if value, found := envSource.GetString("SPACES_VAR"); !found || value != "  " {
			t.Errorf("expected to find spaces, got '%s' (found: %v)", value, found)
		}
	})
}

func TestConfigResolverEmptySources(t *testing.T) {
	resolver := NewConfigResolver()

	// All should return defaults when no sources
	if value := resolver.ResolveString("ANY_KEY", "default"); value != "default" {
		t.Errorf("expected 'default', got '%s'", value)
	}

	if value := resolver.ResolveInt("ANY_KEY", 42); value != 42 {
		t.Errorf("expected 42, got %d", value)
	}

	if value := resolver.ResolveFloat("ANY_KEY", 3.14); value != 3.14 {
		t.Errorf("expected 3.14, got %f", value)
	}
}

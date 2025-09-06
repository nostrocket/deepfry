package config

import (
	"os"
	"testing"
)

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

// Benchmark tests for performance
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

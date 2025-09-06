package config

import (
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

func TestNewFlagSource(t *testing.T) {
	flagSource := NewFlagSource()
	if flagSource == nil {
		t.Fatal("expected non-nil FlagSource")
	}
	if flagSource.values == nil {
		t.Fatal("expected non-nil values map")
	}
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

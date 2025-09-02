package config

import (
	"flag"
	"os"
	"testing"
)

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
		if value, found := flagSource.GetString(KeySourceRelayURL); found {
			t.Errorf("expected no value for %s, got '%s'", KeySourceRelayURL, value)
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
		if value, found := flagSource.GetString(KeySourceRelayURL); !found || value != "wss://test.relay" {
			t.Errorf("expected 'wss://test.relay', got '%s' (found: %v)", value, found)
		}

		// Test int value
		if value, found := flagSource.GetInt(KeySyncWindowSeconds); !found || value != 15 {
			t.Errorf("expected 15, got %d (found: %v)", value, found)
		}
	})
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

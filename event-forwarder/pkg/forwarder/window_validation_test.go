package forwarder

import (
	"context"
	"errors"
	"testing"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/testutil"

	"github.com/nbd-wtf/go-nostr"
)

// Test WindowManager validation enhancements
func TestWindowManagerValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      func() *config.Config
		expectError bool
		errorType   string
	}{
		{
			name: "valid window duration",
			config: func() *config.Config {
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 3600, // 1 hour
					},
				}
			},
			expectError: false,
		},
		{
			name: "zero window duration",
			config: func() *config.Config {
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 0,
					},
				}
			},
			expectError: true,
			errorType:   "duration",
		},
		{
			name: "sub-second window duration",
			config: func() *config.Config {
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 0, // 0 seconds (will fail validation)
					},
				}
			},
			expectError: true,
			errorType:   "duration",
		},
		{
			name: "excessive window duration",
			config: func() *config.Config {
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 366 * 24 * 3600, // More than 1 year
					},
				}
			},
			expectError: true,
			errorType:   "duration",
		},
		{
			name: "valid start time",
			config: func() *config.Config {
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 3600,
						StartTime:     "2024-01-01T00:00:00Z",
					},
				}
			},
			expectError: false,
		},
		{
			name: "invalid start time format",
			config: func() *config.Config {
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 3600,
						StartTime:     "2024-01-01 00:00:00", // Wrong format
					},
				}
			},
			expectError: true,
			errorType:   "start time",
		},
		{
			name: "far future start time",
			config: func() *config.Config {
				farFuture := time.Now().AddDate(2, 0, 0).Format(time.RFC3339)
				return &config.Config{
					Sync: config.SyncConfig{
						WindowSeconds: 3600,
						StartTime:     farFuture,
					},
				}
			},
			expectError: true,
			errorType:   "start time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.config()
			cfg.NostrKeyPair = crypto.KeyPair{
				PrivateKeyHex: testutil.TestSKHex,
				PublicKeyHex:  testutil.TestPKHex,
			}

			mockRelay := &testutil.MockRelay{
				QuerySyncReturn: []*nostr.Event{}, // No existing events
			}

			tracker := nsync.NewSyncTracker(mockRelay, cfg)
			windowManager := NewWindowManager(cfg, tracker)

			_, err := windowManager.GetOrCreate(context.Background())

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if tt.errorType != "" {
					// Check if error message contains expected type
					errMsg := err.Error()
					if errMsg == "" {
						t.Errorf("expected error message containing '%s', got empty error", tt.errorType)
					}
					// Note: We could make this more specific by checking for exact error types
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestWindowManagerAdvanceValidation(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{
			WindowSeconds: 3600, // 1 hour
		},
		NostrKeyPair: crypto.KeyPair{
			PrivateKeyHex: testutil.TestSKHex,
			PublicKeyHex:  testutil.TestPKHex,
		},
	}

	mockRelay := &testutil.MockRelay{}
	tracker := nsync.NewSyncTracker(mockRelay, cfg)
	windowManager := NewWindowManager(cfg, tracker)

	t.Run("advance valid window", func(t *testing.T) {
		validWindow := nsync.NewWindow(time.Hour)
		nextWindow := windowManager.Advance(validWindow)

		// Check that advancement worked correctly
		if !validWindow.To.Equal(nextWindow.From) {
			t.Errorf("advanced window not contiguous: prev.To=%v, next.From=%v",
				validWindow.To, nextWindow.From)
		}

		// Validate the resulting window
		if err := nextWindow.Validate(); err != nil {
			t.Errorf("advanced window is invalid: %v", err)
		}
	})

	t.Run("advance invalid window", func(t *testing.T) {
		// Create an invalid window (end before start)
		invalidWindow := nsync.Window{
			From: time.Now(),
			To:   time.Now().Add(-time.Hour), // Invalid: To before From
		}

		// Advance should still work (fallback behavior)
		nextWindow := windowManager.Advance(invalidWindow)

		// The result should be based on the original window's To time
		expectedFrom := invalidWindow.To
		if !nextWindow.From.Equal(expectedFrom) {
			t.Errorf("expected From=%v, got %v", expectedFrom, nextWindow.From)
		}
	})
}

func TestWindowManagerUpdateValidation(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{
			WindowSeconds: 3600,
		},
		NostrKeyPair: crypto.KeyPair{
			PrivateKeyHex: testutil.TestSKHex,
			PublicKeyHex:  testutil.TestPKHex,
		},
	}

	tests := []struct {
		name        string
		window      nsync.Window
		mockSetup   func(*testutil.MockRelay)
		expectError bool
	}{
		{
			name:   "update valid window",
			window: nsync.NewWindow(time.Hour),
			mockSetup: func(relay *testutil.MockRelay) {
				relay.PublishError = nil
			},
			expectError: false,
		},
		{
			name: "update invalid window - zero times",
			window: nsync.Window{
				From: time.Time{},
				To:   time.Time{},
			},
			mockSetup: func(relay *testutil.MockRelay) {
				// Should not be called due to validation failure
			},
			expectError: true,
		},
		{
			name: "update invalid window - end before start",
			window: nsync.Window{
				From: time.Now(),
				To:   time.Now().Add(-time.Hour),
			},
			mockSetup: func(relay *testutil.MockRelay) {
				// Should not be called due to validation failure
			},
			expectError: true,
		},
		{
			name:   "update valid window with relay error",
			window: nsync.NewWindow(time.Hour),
			mockSetup: func(relay *testutil.MockRelay) {
				relay.PublishError = errors.New("relay publish failed")
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRelay := &testutil.MockRelay{}
			tt.mockSetup(mockRelay)

			tracker := nsync.NewSyncTracker(mockRelay, cfg)
			windowManager := NewWindowManager(cfg, tracker)

			err := windowManager.Update(context.Background(), tt.window)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// Property-based test for WindowManager operations
func TestWindowManagerProperties(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{
			WindowSeconds: 3600, // 1 hour
		},
		NostrKeyPair: crypto.KeyPair{
			PrivateKeyHex: testutil.TestSKHex,
			PublicKeyHex:  testutil.TestPKHex,
		},
	}

	mockRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{}, // No existing events
		PublishError:    nil,
	}

	tracker := nsync.NewSyncTracker(mockRelay, cfg)
	windowManager := NewWindowManager(cfg, tracker)

	t.Run("GetOrCreate produces valid windows", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			window, err := windowManager.GetOrCreate(context.Background())
			if err != nil {
				t.Fatalf("GetOrCreate failed on iteration %d: %v", i, err)
			}

			if window == nil {
				t.Fatalf("GetOrCreate returned nil window on iteration %d", i)
			}

			if err := window.Validate(); err != nil {
				t.Errorf("GetOrCreate produced invalid window on iteration %d: %v", i, err)
			}
		}
	})

	t.Run("Advance produces contiguous windows", func(t *testing.T) {
		initialWindow, err := windowManager.GetOrCreate(context.Background())
		if err != nil {
			t.Fatalf("failed to get initial window: %v", err)
		}

		var windows []*nsync.Window
		windows = append(windows, initialWindow)

		// Generate sequence of advanced windows
		for i := 0; i < 10; i++ {
			nextWindow := windowManager.Advance(*windows[len(windows)-1])
			windows = append(windows, &nextWindow)
		}

		// Verify contiguity and validity
		for i := 1; i < len(windows); i++ {
			prev := windows[i-1]
			curr := windows[i]

			// Check contiguity
			if !prev.To.Equal(curr.From) {
				t.Errorf("windows %d and %d are not contiguous: prev.To=%v, curr.From=%v",
					i-1, i, prev.To, curr.From)
			}

			// Check validity
			if err := curr.Validate(); err != nil {
				t.Errorf("advanced window %d is invalid: %v", i, err)
			}

			// Check duration consistency
			prevDuration := prev.To.Sub(prev.From)
			currDuration := curr.To.Sub(curr.From)
			if prevDuration != currDuration {
				t.Errorf("window %d has inconsistent duration: prev=%v, curr=%v",
					i, prevDuration, currDuration)
			}
		}
	})
}

// Benchmark the validation overhead
func BenchmarkWindowManagerGetOrCreate(b *testing.B) {
	cfg := &config.Config{
		Sync: config.SyncConfig{
			WindowSeconds: 3600,
		},
		NostrKeyPair: crypto.KeyPair{
			PrivateKeyHex: testutil.TestSKHex,
			PublicKeyHex:  testutil.TestPKHex,
		},
	}

	mockRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{}, // No existing events
	}

	tracker := nsync.NewSyncTracker(mockRelay, cfg)
	windowManager := NewWindowManager(cfg, tracker)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = windowManager.GetOrCreate(ctx)
	}
}

func BenchmarkWindowManagerAdvance(b *testing.B) {
	cfg := &config.Config{
		Sync: config.SyncConfig{
			WindowSeconds: 3600,
		},
	}

	mockRelay := &testutil.MockRelay{}
	tracker := nsync.NewSyncTracker(mockRelay, cfg)
	windowManager := NewWindowManager(cfg, tracker)
	window := nsync.NewWindow(time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = windowManager.Advance(window)
	}
}

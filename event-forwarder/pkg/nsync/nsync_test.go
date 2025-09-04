package nsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"event-forwarder/pkg/testutil"

	"github.com/nbd-wtf/go-nostr"
)

var testKeyPair = crypto.KeyPair{
	PrivateKeyHex:    testutil.TestSKHex,
	PrivateKeyBech32: testutil.TestSK,
	PublicKeyHex:     testutil.TestPKHex,
	PublicKeyBech32:  testutil.TestPK,
}

// Reuse testutil.MockRelay for relay mocking to keep tests DRY

func TestNewSyncTracker(t *testing.T) {
	mockRelay := &testutil.MockRelay{}
	cfg := &config.Config{
		SourceRelayURL: "wss://source.relay",
		NostrSecretKey: testutil.TestSK,
		NostrKeyPair:   testKeyPair,
	}

	tracker := NewSyncTracker(mockRelay, cfg)

	if tracker.relay != mockRelay {
		t.Errorf("expected relay to be set, got %v", tracker.relay)
	}
	if tracker.keyPair.PrivateKeyBech32 != testutil.TestSK {
		t.Errorf("expected secretKey %s, got %s", testutil.TestSK, tracker.keyPair.PrivateKeyBech32)
	}
	if tracker.keyPair.PublicKeyBech32 != testutil.TestPK {
		t.Errorf("expected publicKey %s, got %s", testutil.TestPK, tracker.keyPair.PublicKeyBech32)
	}
	if tracker.sourceURL != "wss://source.relay" {
		t.Errorf("expected sourceURL 'wss://source.relay', got %s", tracker.sourceURL)
	}
}

func TestGetLastWindow(t *testing.T) {
	tests := []struct {
		name        string
		mockEvents  []*nostr.Event
		mockError   error
		expected    *Window
		expectError bool
	}{
		{
			name: "success with event",
			mockEvents: []*nostr.Event{
				{
					Tags: nostr.Tags{
						{"d", "wss://source.relay"},
						{"from", "1640995200"},
						{"to", "1640998800"},
					},
				},
			},
			expected: &Window{
				From: time.Unix(1640995200, 0),
				To:   time.Unix(1640998800, 0),
			},
			expectError: false,
		},
		{
			name:        "no events",
			mockEvents:  []*nostr.Event{},
			expected:    nil,
			expectError: false,
		},
		{
			name:        "query error",
			mockEvents:  nil,
			mockError:   errors.New("query failed"),
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRelay := &testutil.MockRelay{
				QuerySyncReturn: tt.mockEvents,
				QuerySyncError:  tt.mockError,
			}

			cfg := &config.Config{SourceRelayURL: "wss://source.relay"}
			tracker := NewSyncTracker(mockRelay, cfg)

			window, err := tracker.GetLastWindow(context.Background())

			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.expected == nil && window != nil {
				t.Errorf("expected nil window, got %v", window)
			}
			if tt.expected != nil && window == nil {
				t.Fatal("expected window, got nil")
			}
			if tt.expected != nil && window != nil {
				if !window.From.Equal(tt.expected.From) || !window.To.Equal(tt.expected.To) {
					t.Errorf("expected window %v, got %v", tt.expected, window)
				}
			}
		})
	}
}

func TestUpdateWindow(t *testing.T) {
	tests := []struct {
		name        string
		mockError   error
		expectError bool
	}{
		{
			name:        "success",
			mockError:   nil,
			expectError: false,
		},
		{
			name:        "publish error",
			mockError:   errors.New("publish failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRelay := &testutil.MockRelay{
				PublishError: tt.mockError,
			}

			cfg := &config.Config{
				SourceRelayURL: "wss://source.relay",
				NostrSecretKey: testutil.TestSK,
				NostrKeyPair:   testKeyPair,
			}
			tracker := NewSyncTracker(mockRelay, cfg)

			window := Window{From: time.Now(), To: time.Now().Add(time.Hour)}
			err := tracker.UpdateWindow(context.Background(), window)

			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestParseWindow(t *testing.T) {
	tests := []struct {
		name        string
		event       *nostr.Event
		expected    *Window
		expectError bool
	}{
		{
			name: "valid event",
			event: &nostr.Event{
				Tags: nostr.Tags{
					{"d", "wss://source.relay"},
					{"from", "1640995200"},
					{"to", "1640998800"},
				},
			},
			expected: &Window{
				From: time.Unix(1640995200, 0),
				To:   time.Unix(1640998800, 0),
			},
			expectError: false,
		},
		{
			name: "missing from tag",
			event: &nostr.Event{
				Tags: nostr.Tags{
					{"d", "wss://source.relay"},
					{"to", "1640998800"},
				},
			},
			expected:    nil,
			expectError: true,
		},
		{
			name: "missing to tag",
			event: &nostr.Event{
				Tags: nostr.Tags{
					{"d", "wss://source.relay"},
					{"from", "1640995200"},
				},
			},
			expected:    nil,
			expectError: true,
		},
		{
			name: "invalid from timestamp",
			event: &nostr.Event{
				Tags: nostr.Tags{
					{"d", "wss://source.relay"},
					{"from", "invalid"},
					{"to", "1640998800"},
				},
			},
			expected:    nil,
			expectError: true,
		},
		{
			name: "invalid to timestamp",
			event: &nostr.Event{
				Tags: nostr.Tags{
					{"d", "wss://source.relay"},
					{"from", "1640995200"},
					{"to", "invalid"},
				},
			},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRelay := &testutil.MockRelay{}
			cfg := &config.Config{SourceRelayURL: "wss://source.relay"}
			tracker := NewSyncTracker(mockRelay, cfg)

			window, err := tracker.parseWindow(tt.event)

			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.expected == nil && window != nil {
				t.Errorf("expected nil window, got %v", window)
			}
			if tt.expected != nil && window == nil {
				t.Fatal("expected window, got nil")
			}
			if tt.expected != nil && window != nil {
				if !window.From.Equal(tt.expected.From) || !window.To.Equal(tt.expected.To) {
					t.Errorf("expected window %v, got %v", tt.expected, window)
				}
			}
		})
	}
}

func TestWindow_Next(t *testing.T) {
	window := Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}
	duration := time.Hour

	nextWindow := window.Next(duration)

	expectedFrom := window.To
	expectedTo := window.To.Add(duration)

	if !nextWindow.From.Equal(expectedFrom) {
		t.Errorf("expected From %v, got %v", expectedFrom, nextWindow.From)
	}
	if !nextWindow.To.Equal(expectedTo) {
		t.Errorf("expected To %v, got %v", expectedTo, nextWindow.To)
	}
}

func TestNewWindow(t *testing.T) {
	duration := time.Hour
	window := NewWindow(duration)

	now := time.Now().UTC()
	windowStart := now.Truncate(duration)

	if !window.From.Equal(windowStart) {
		t.Errorf("expected From %v, got %v", windowStart, window.From)
	}
	if !window.To.Equal(windowStart.Add(duration)) {
		t.Errorf("expected To %v, got %v", windowStart.Add(duration), window.To)
	}
}

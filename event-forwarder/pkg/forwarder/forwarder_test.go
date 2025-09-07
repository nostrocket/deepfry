package forwarder

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/testutil"

	nostr "github.com/nbd-wtf/go-nostr"
)

// Shared helpers used by other *_test.go in this package
var testKeyPair = crypto.KeyPair{
	PrivateKeyHex:    testutil.TestSKHex,
	PrivateKeyBech32: testutil.TestSK,
	PublicKeyHex:     testutil.TestPKHex,
	PublicKeyBech32:  testutil.TestPK,
}

func createTestConfig() *config.Config {
	return &config.Config{
		SourceRelayURL:  "wss://source.relay",
		DeepFryRelayURL: "wss://deepfry.relay",
		NostrSecretKey:  testutil.TestSK,
		NostrKeyPair:    testKeyPair,
		Sync: config.SyncConfig{
			WindowSeconds:        5,
			MaxBatch:             1000,
			MaxCatchupLagSeconds: 10,
		},
		Network: config.NetworkConfig{
			InitialBackoffSeconds: 1,
			MaxBackoffSeconds:     30,
			BackoffJitter:         0.2,
		},
		Timeouts: config.TimeoutConfig{
			PublishSeconds:   10,
			SubscribeSeconds: 10,
		},
	}
}

func createTestLogger() *log.Logger                     { return log.New(os.Stdout, "[TEST] ", 0) }
func createNoopTelemetry() telemetry.TelemetryPublisher { return telemetry.NewNoopPublisher() }

// ---- Orchestrator-focused, lean tests ----

func TestNew_InitializesConnMgrAndTelemetry(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	cap := testutil.NewCapturingPublisher()
	f := New(cfg, logger, cap)
	if f.connMgr == nil {
		t.Fatalf("expected connMgr to be initialized")
	}
	if f.tsink == nil {
		t.Fatalf("expected telemetry sink to be initialized")
	}
	if f.currentSyncMode != SyncModeWindowed {
		t.Fatalf("expected initial mode %q, got %q", SyncModeWindowed, f.currentSyncMode)
	}
}

func TestNewWithRelays_SetsSyncTrackerAndMode(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	src := &testutil.MockRelay{}
	dst := &testutil.MockRelay{}
	f := NewWithRelays(cfg, logger, src, dst, createNoopTelemetry())
	if f.sourceRelay != src || f.deepfryRelay != dst {
		t.Fatalf("expected injected relays to be set")
	}
	if f.syncTracker == nil {
		t.Fatalf("expected syncTracker to be initialized")
	}
	if f.currentSyncMode != SyncModeWindowed {
		t.Fatalf("expected initial mode %q", SyncModeWindowed)
	}
}

func TestSyncLoop_ContextCancellation(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	src := &testutil.MockRelay{}
	dst := &testutil.MockRelay{}
	f := NewWithRelays(cfg, logger, src, dst, createNoopTelemetry())

	start := nsync.Window{From: time.Unix(1640995200, 0), To: time.Unix(1640998800, 0)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := f.syncLoop(ctx, &start); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestProcessRealtimeEvent_SuccessAndError(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	src := &testutil.MockRelay{}
	dst := &testutil.MockRelay{}
	f := NewWithRelays(cfg, logger, src, dst, createNoopTelemetry())

	// success
	if err := f.processRealtimeEvent(context.Background(), &nostr.Event{ID: "x", Kind: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dst.PublishCalls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(dst.PublishCalls))
	}

	// error
	dst.PublishError = errors.New("boom")
	if err := f.processRealtimeEvent(context.Background(), &nostr.Event{ID: "y", Kind: 1}); err == nil {
		t.Fatalf("expected error when publish fails")
	}
}

func TestUpdateRealtimeWindow_UsesWindowManager(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	f := NewWithRelays(cfg, logger, &testutil.MockRelay{}, &testutil.MockRelay{}, createNoopTelemetry())
	f.currentWindow = &nsync.Window{From: time.Now().Add(-5 * time.Minute).UTC(), To: time.Now().UTC()}

	// stub window manager
	called := false
	f.winMgr = &stubWindowMgr{updateFn: func(ctx context.Context, w nsync.Window) error {
		called = true
		return nil
	}}

	if err := f.updateRealtimeWindow(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected WindowManager.Update to be called")
	}
	if f.currentWindow == nil || time.Since(f.currentWindow.To) > 2*time.Second {
		t.Fatalf("expected currentWindow to update near now, got %+v", f.currentWindow)
	}
}

func TestUpdateRealtimeWindow_Error(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	f := NewWithRelays(cfg, logger, &testutil.MockRelay{}, &testutil.MockRelay{}, createNoopTelemetry())
	f.currentWindow = &nsync.Window{From: time.Now().Add(-5 * time.Minute).UTC(), To: time.Now().UTC()}
	f.winMgr = &stubWindowMgr{updateFn: func(ctx context.Context, w nsync.Window) error { return errors.New("fail") }}
	if err := f.updateRealtimeWindow(context.Background()); err == nil {
		t.Fatalf("expected error from window update")
	}
}

func TestGetOrCreateWindow_UsesWindowManager(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	f := NewWithRelays(cfg, logger, &testutil.MockRelay{}, &testutil.MockRelay{}, createNoopTelemetry())
	want := nsync.NewWindowFromStart(time.Unix(1_700_000_000, 0).UTC(), 5*time.Second)
	f.winMgr = &stubWindowMgr{window: &want}
	got, err := f.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == nil || !got.From.Equal(want.From) || !got.To.Equal(want.To) {
		t.Fatalf("expected window from WindowManager, got %+v", got)
	}
}

func TestGetOrCreateWindow_InlineStartTime(t *testing.T) {
	cfg := createTestConfig()
	// remove winMgr usage by creating via NewWithRelays then nil it
	logger := createTestLogger()
	f := NewWithRelays(cfg, logger, &testutil.MockRelay{}, &testutil.MockRelay{}, createNoopTelemetry())
	f.winMgr = nil
	// set explicit start time
	start := time.Unix(1_700_000_000, 0).UTC()
	cfg.Sync.StartTime = start.Format(time.RFC3339)
	got, err := f.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == nil || !got.From.Equal(start) {
		t.Fatalf("expected from=%v, got %+v", start, got)
	}
}

// ---- minimal stubs used in these tests ----

type stubWindowMgr struct {
	window   *nsync.Window
	getErr   error
	updateFn func(ctx context.Context, w nsync.Window) error
}

func (s *stubWindowMgr) GetOrCreate(ctx context.Context) (*nsync.Window, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.window != nil {
		return s.window, nil
	}
	w := nsync.NewWindow(5 * time.Second)
	return &w, nil
}
func (s *stubWindowMgr) Advance(w nsync.Window) nsync.Window { return w }
func (s *stubWindowMgr) Update(ctx context.Context, w nsync.Window) error {
	if s.updateFn != nil {
		return s.updateFn(ctx, w)
	}
	return nil
}

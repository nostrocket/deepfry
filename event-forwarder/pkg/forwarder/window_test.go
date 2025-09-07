package forwarder

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/testutil"

	nostr "github.com/nbd-wtf/go-nostr"
)

func TestWindowManager_GetOrCreate_UsesStartTime(t *testing.T) {
	// Use fixed time to avoid flakiness
	startTime := time.Unix(1_700_000_000, 0).UTC()
	start := startTime.Format(time.RFC3339)
	cfg := &config.Config{Sync: config.SyncConfig{StartTime: start, WindowSeconds: 600}}
	wm := NewWindowManager(cfg, nsync.NewSyncTracker(&testutil.MockRelay{}, cfg)).(*windowManagerImpl)
	w, err := wm.GetOrCreate(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// With StartTime set, the window should start exactly at the provided start time
	if !w.From.Equal(startTime) {
		t.Fatalf("expected window.From %v; got %v", startTime, w.From)
	}
}

func TestWindowManager_GetOrCreate_FromLastWindow(t *testing.T) {
	cfg := &config.Config{Sync: config.SyncConfig{WindowSeconds: 600}, SourceRelayURL: "wss://src"}
	// populate deterministic keypair since SyncTracker filters by author
	cfg.NostrKeyPair.PrivateKeyHex = testutil.TestSKHex
	cfg.NostrKeyPair.PublicKeyHex = testutil.TestPKHex
	// Use deterministic last window aligned to second precision
	base := time.Unix(1_700_000_000, 0).UTC().Add(-1 * time.Hour)
	last := nsync.NewWindowFromStart(base, 10*time.Minute)
	relay := &testutil.MockRelay{}
	// Build a fake sync event to be returned by QuerySync
	ev := &nostr.Event{Tags: nostr.Tags{{"d", cfg.SourceRelayURL}, {"from", strconv.FormatInt(last.From.Unix(), 10)}, {"to", strconv.FormatInt(last.To.Unix(), 10)}}}
	relay.QuerySyncReturn = []*nostr.Event{ev}
	wm := NewWindowManager(cfg, nsync.NewSyncTracker(relay, cfg)).(*windowManagerImpl)
	w, err := wm.GetOrCreate(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Should be Next(duration) of last
	expected := last.Next(10 * time.Minute)
	// Compare at second-level precision to account for truncation in parseWindow()
	if w.From.Unix() != expected.From.Unix() || w.To.Unix() != expected.To.Unix() {
		t.Fatalf("expected next window %v; got %v", expected, *w)
	}
}

func TestWindowManager_Update_DelegatesAndErrors(t *testing.T) {
	cfg := &config.Config{Sync: config.SyncConfig{WindowSeconds: 300}, SourceRelayURL: "wss://src"}
	// Provide key material to allow signing in tracker.UpdateWindow
	cfg.NostrKeyPair.PrivateKeyHex = testutil.TestSKHex
	cfg.NostrKeyPair.PublicKeyHex = testutil.TestPKHex
	relay := &testutil.MockRelay{}
	trk := nsync.NewSyncTracker(relay, cfg)
	wm := NewWindowManager(cfg, trk).(*windowManagerImpl)
	// Use fixed time to ensure deterministic signing and tags
	fixed := time.Unix(1_700_000_000, 0).UTC().Add(-30 * time.Minute)
	w := nsync.NewWindowFromStart(fixed, 5*time.Minute)
	// Inject publish error via mock relay
	relay.PublishError = errors.New("boom")
	if err := wm.Update(context.Background(), w); err == nil {
		t.Fatalf("expected error from tracker.UpdateWindow")
	}
	relay.PublishError = nil
	if err := wm.Update(context.Background(), w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

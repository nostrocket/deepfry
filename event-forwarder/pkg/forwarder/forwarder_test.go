package forwarder

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/relay"
	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/testutil"

	"github.com/nbd-wtf/go-nostr"
)

var testKeyPair = crypto.KeyPair{
	PrivateKeyHex:    testutil.TestSKHex,
	PrivateKeyBech32: testutil.TestSK,
	PublicKeyHex:     testutil.TestPKHex,
	PublicKeyBech32:  testutil.TestPK,
}

// Use shared testutil.MockRelay for relay mocking to reduce duplication

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

func createTestLogger() *log.Logger {
	return log.New(os.Stdout, "[TEST] ", log.LstdFlags)
}

func createNoopTelemetry() telemetry.TelemetryPublisher {
	return telemetry.NewNoopPublisher()
}

func TestNew(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	forwarder := New(cfg, logger, createNoopTelemetry())

	if forwarder.cfg != cfg {
		t.Errorf("expected cfg to be %v, got %v", cfg, forwarder.cfg)
	}
	if forwarder.logger != logger {
		t.Errorf("expected logger to be %v, got %v", logger, forwarder.logger)
	}
	if forwarder.sourceRelay != nil {
		t.Errorf("expected sourceRelay to be nil, got %v", forwarder.sourceRelay)
	}
	if forwarder.deepfryRelay != nil {
		t.Errorf("expected deepfryRelay to be nil, got %v", forwarder.deepfryRelay)
	}
	if forwarder.syncTracker != nil {
		t.Errorf("expected syncTracker to be nil, got %v", forwarder.syncTracker)
	}
}

func TestNewWithRelays(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	sourceRelay := &testutil.MockRelay{}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	if forwarder.cfg != cfg {
		t.Errorf("expected cfg to be %v, got %v", cfg, forwarder.cfg)
	}
	if forwarder.logger != logger {
		t.Errorf("expected logger to be %v, got %v", logger, forwarder.logger)
	}
	if forwarder.sourceRelay != sourceRelay {
		t.Errorf("expected sourceRelay to be %v, got %v", sourceRelay, forwarder.sourceRelay)
	}
	if forwarder.deepfryRelay != deepfryRelay {
		t.Errorf("expected deepfryRelay to be %v, got %v", deepfryRelay, forwarder.deepfryRelay)
	}
	if forwarder.syncTracker == nil {
		t.Error("expected syncTracker to be initialized")
	}
}

func TestCloseRelays(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	sourceRelay := &testutil.MockRelay{}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())
	forwarder.closeRelays()

	if !sourceRelay.CloseCalled {
		t.Error("expected sourceRelay.Close() to be called")
	}
	if !deepfryRelay.CloseCalled {
		t.Error("expected deepfryRelay.Close() to be called")
	}
}

func TestCloseRelays_NilRelays(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	forwarder := New(cfg, logger, createNoopTelemetry())
	// Should not panic when relays are nil
	forwarder.closeRelays()
}

func TestGetOrCreateWindow_NoLastWindow(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	deepfryRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{}, // No existing sync events
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	window, err := forwarder.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if window == nil {
		t.Fatal("expected window to be created")
	}

	duration := time.Duration(cfg.Sync.WindowSeconds) * time.Second
	now := time.Now().UTC()
	expectedStart := now.Truncate(duration)

	if window.From.Before(expectedStart.Add(-time.Second)) || window.From.After(expectedStart.Add(time.Second)) {
		t.Errorf("expected window.From to be around %v, got %v", expectedStart, window.From)
	}
	if window.To.Before(expectedStart.Add(duration).Add(-time.Second)) || window.To.After(expectedStart.Add(duration).Add(time.Second)) {
		t.Errorf("expected window.To to be around %v, got %v", expectedStart.Add(duration), window.To)
	}
}

func TestGetOrCreateWindow_WithLastWindow(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	lastWindowTo := time.Unix(1640998800, 0)

	deepfryRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{
			{
				Tags: nostr.Tags{
					{"d", cfg.SourceRelayURL},
					{"from", "1640995200"},
					{"to", "1640998800"},
				},
			},
		},
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	window, err := forwarder.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if window == nil {
		t.Fatal("expected window to be created")
	}

	// Should get next window after the last one
	if !window.From.Equal(lastWindowTo) {
		t.Errorf("expected window.From to be %v, got %v", lastWindowTo, window.From)
	}

	expectedTo := lastWindowTo.Add(time.Duration(cfg.Sync.WindowSeconds) * time.Second)
	if !window.To.Equal(expectedTo) {
		t.Errorf("expected window.To to be %v, got %v", expectedTo, window.To)
	}
}

func TestGetOrCreateWindow_QueryError(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	deepfryRelay := &testutil.MockRelay{
		QuerySyncError: errors.New("query failed"),
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	_, err := forwarder.getOrCreateWindow(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSyncWindow_Success(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	// Mock events to be returned by source relay
	mockEvents := []*nostr.Event{
		{ID: "event1", Content: "test content 1"},
		{ID: "event2", Content: "test content 2"},
	}

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: mockEvents,
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify source relay was queried with correct filter
	if len(sourceRelay.QueryEventsCalls) != 1 {
		t.Fatalf("expected 1 QueryEvents call, got %d", len(sourceRelay.QueryEventsCalls))
	}

	filter := sourceRelay.QueryEventsCalls[0]
	if filter.Since == nil || filter.Since.Time() != window.From {
		t.Errorf("expected filter.Since to be %v, got %v", window.From, filter.Since)
	}
	if filter.Until == nil || filter.Until.Time() != window.To {
		t.Errorf("expected filter.Until to be %v, got %v", window.To, filter.Until)
	}
	if filter.Limit != cfg.Sync.MaxBatch {
		t.Errorf("expected filter.Limit to be %d, got %d", cfg.Sync.MaxBatch, filter.Limit)
	}

	// Verify events were published to deepfry relay (2 events + 1 sync event)
	expectedPublishCalls := len(mockEvents) + 1 // +1 for sync event
	if len(deepfryRelay.PublishCalls) != expectedPublishCalls {
		t.Fatalf("expected %d Publish calls, got %d", expectedPublishCalls, len(deepfryRelay.PublishCalls))
	}

	// Verify the first events are the forwarded events
	for i, publishedEvent := range deepfryRelay.PublishCalls[:len(mockEvents)] {
		if publishedEvent.ID != mockEvents[i].ID {
			t.Errorf("expected published event ID %s, got %s", mockEvents[i].ID, publishedEvent.ID)
		}
	}

	// Verify sync window was updated (last event should be sync event)
	lastEvent := deepfryRelay.PublishCalls[len(deepfryRelay.PublishCalls)-1]
	if lastEvent.Kind != nsync.SyncEventKind {
		t.Errorf("expected last published event to be sync event (kind %d), got kind %d", nsync.SyncEventKind, lastEvent.Kind)
	}
}

func TestSyncWindow_QueryError(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QueryEventsError: errors.New("query failed"),
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sourceRelay.QueryEventsError) {
		t.Errorf("expected error to wrap query error, got %v", err)
	}
}

func TestSyncWindow_PublishError_NoPanicAndContinues(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	mockEvents := []*nostr.Event{
		{ID: "event1", Content: "test content 1"},
	}

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: mockEvents,
	}
	// Use a relay that fails on regular events but succeeds on sync events
	deepfryRelay := &ConditionalErrorRelay{
		PublishCalls:      []nostr.Event{},
		EventPublishError: errors.New("publish failed"),
	}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())
	// Prevent real reconnect attempts
	forwarder.connMgr = &fakeConnMgr{src: sourceRelay, df: deepfryRelay}
	// Force the update path to use SyncTracker directly to publish the sync event on the same relay
	forwarder.syncTracker = nsync.NewSyncTracker(deepfryRelay, cfg)
	forwarder.winMgr = nil

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	// New behavior: do not panic on event publish error; continue and still attempt sync update (which succeeds)
	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Must have published a sync event regardless of individual event publish failures
	foundSync := false
	for _, ev := range deepfryRelay.PublishCalls {
		if ev.Kind == nsync.SyncEventKind {
			foundSync = true
			break
		}
	}
	if !foundSync {
		t.Fatalf("expected a sync event (kind %d) to be published; got %d publish calls: %+v", nsync.SyncEventKind, len(deepfryRelay.PublishCalls), deepfryRelay.PublishCalls)
	}
}

func TestSyncWindow_EventPublishError_SyncSucceeds(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	mockEvents := []*nostr.Event{
		{ID: "event1", Content: "test content 1"},
	}

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: mockEvents,
	}

	// Custom mock that fails on regular events but succeeds on sync events
	deepfryRelay := &ConditionalErrorRelay{
		PublishCalls:      []nostr.Event{},
		EventPublishError: errors.New("event publish failed"),
	}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())
	// Ensure sync update goes through the SyncTracker using the same relay
	forwarder.syncTracker = nsync.NewSyncTracker(deepfryRelay, cfg)
	forwarder.winMgr = nil

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	// Should succeed when sync event publishes successfully
	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Must include a sync event publish
	foundSync := false
	for _, ev := range deepfryRelay.PublishCalls {
		if ev.Kind == nsync.SyncEventKind {
			foundSync = true
			break
		}
	}
	if !foundSync {
		t.Fatalf("expected a sync event (kind %d) to be published; got %d publish calls: %+v", nsync.SyncEventKind, len(deepfryRelay.PublishCalls), deepfryRelay.PublishCalls)
	}
}

// ConditionalErrorRelay fails on regular events but succeeds on sync events
type ConditionalErrorRelay struct {
	PublishCalls      []nostr.Event
	EventPublishError error
	CloseCalled       bool
}

func (r *ConditionalErrorRelay) QuerySync(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error) {
	return []*nostr.Event{}, nil
}

func (r *ConditionalErrorRelay) QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
	ch := make(chan *nostr.Event)
	close(ch) // Return empty channel
	return ch, nil
}

func (r *ConditionalErrorRelay) Subscribe(ctx context.Context, filters nostr.Filters, opts ...nostr.SubscriptionOption) (*nostr.Subscription, error) {
	events := make(chan *nostr.Event)
	eose := make(chan struct{})
	closed := make(chan string)

	// Create a mock subscription that immediately closes
	sub := &nostr.Subscription{
		Events:            events,
		EndOfStoredEvents: eose,
		ClosedReason:      closed,
	}

	// Close immediately
	go func() {
		close(events)
		close(eose)
	}()

	return sub, nil
}

func (r *ConditionalErrorRelay) Publish(ctx context.Context, event nostr.Event) error {
	r.PublishCalls = append(r.PublishCalls, event)
	if event.Kind == nsync.SyncEventKind {
		return nil // Sync events succeed
	}
	return r.EventPublishError // Regular events fail
}

func (r *ConditionalErrorRelay) Close() error {
	r.CloseCalled = true
	return nil
}

// fakeConnMgr is a no-op ConnectionManager used to avoid real network operations in unit tests.
type fakeConnMgr struct {
	src relay.Relay
	df  relay.Relay
}

func (f *fakeConnMgr) Connect(ctx context.Context) error   { return nil }
func (f *fakeConnMgr) Reconnect(ctx context.Context) error { return nil }
func (f *fakeConnMgr) Close()                              {}
func (f *fakeConnMgr) Source() relay.Relay                 { return f.src }
func (f *fakeConnMgr) Deepfry() relay.Relay                { return f.df }

// stubWindowMgr implements WindowManager for tests with controllable errors
type stubWindowMgr struct {
	window    *nsync.Window
	getErr    error
	updateErr error
	tracker   *nsync.SyncTracker
}

func (s *stubWindowMgr) GetOrCreate(ctx context.Context) (*nsync.Window, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.window != nil {
		return s.window, nil
	}
	w := nsync.NewWindowFromStart(time.Unix(1640995200, 0).UTC(), 5*time.Second)
	return &w, nil
}

func (s *stubWindowMgr) Advance(w nsync.Window) nsync.Window { return w }
func (s *stubWindowMgr) Update(ctx context.Context, w nsync.Window) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	if s.tracker != nil {
		// Delegate to real tracker to publish the sync event so tests can observe it
		return s.tracker.UpdateWindow(ctx, w)
	}
	return nil
}

func TestSyncLoop_ContextCancellation(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	sourceRelay := &testutil.MockRelay{}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	startWindow := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	// Create a context that cancels quickly
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := forwarder.syncLoop(ctx, &startWindow)
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestConnectRelays_SourceRelayError(t *testing.T) {
	cfg := createTestConfig()
	cfg.SourceRelayURL = "invalid://url"
	logger := createTestLogger()

	forwarder := New(cfg, logger, createNoopTelemetry())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Expect a panic: use defer+recover to assert it occurred
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected connectRelays to panic, but it did not")
		}
	}()

	// This call should panic
	forwarder.connectRelays(ctx)
}

func TestConnectRelays_DeepfryRelayError(t *testing.T) {
	cfg := createTestConfig()
	cfg.DeepFryRelayURL = "invalid://url"
	logger := createTestLogger()

	forwarder := New(cfg, logger, createNoopTelemetry())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Expect a panic: use defer+recover to assert it occurred
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected connectRelays to panic, but it did not")
		}
	}()

	// This call should panic
	forwarder.connectRelays(ctx)
}

func TestStart_GetWindowError(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	deepfryRelay := &testutil.MockRelay{
		QuerySyncError: errors.New("failed to get window"),
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	// For NewWithRelays, Start still calls connectRelays which will fail
	// So we test getOrCreateWindow directly instead
	_, err := forwarder.getOrCreateWindow(context.Background())
	if err == nil {
		t.Fatal("expected error due to window retrieval failure")
	}
	if !strings.Contains(err.Error(), "failed to get window") {
		t.Errorf("expected window retrieval error, got %v", err)
	}
}

func TestGetOrCreateWindow_NextWindow(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	lastWindowFrom := time.Unix(1640995200, 0)
	lastWindowTo := time.Unix(1640998800, 0)

	deepfryRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{
			{
				Tags: nostr.Tags{
					{"d", cfg.SourceRelayURL},
					{"from", strconv.FormatInt(lastWindowFrom.Unix(), 10)},
					{"to", strconv.FormatInt(lastWindowTo.Unix(), 10)},
				},
			},
		},
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	window, err := forwarder.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should start from the end of the last window
	if !window.From.Equal(lastWindowTo) {
		t.Errorf("expected window.From to be %v, got %v", lastWindowTo, window.From)
	}

	expectedTo := lastWindowTo.Add(time.Duration(cfg.Sync.WindowSeconds) * time.Second)
	if !window.To.Equal(expectedTo) {
		t.Errorf("expected window.To to be %v, got %v", expectedTo, window.To)
	}
}

func TestSyncWindow_LargeEventBatch(t *testing.T) {
	cfg := createTestConfig()
	cfg.Sync.MaxBatch = 2
	logger := createTestLogger()

	// Create exactly MaxBatch events
	mockEvents := []*nostr.Event{
		{ID: "event1", Content: "content1"},
		{ID: "event2", Content: "content2"},
	}

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: mockEvents,
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should publish all events + sync event
	expectedPublishCalls := len(mockEvents) + 1
	if len(deepfryRelay.PublishCalls) != expectedPublishCalls {
		t.Fatalf("expected %d Publish calls, got %d", expectedPublishCalls, len(deepfryRelay.PublishCalls))
	}
}

func TestSyncWindow_NilEvent(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{nil}, // Should not cause panic
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	// Should handle nil events gracefully (though this shouldn't happen in practice)
	// The test will show if we properly handle edge cases
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("syncWindow panicked on nil event: %v", r)
		}
	}()

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCloseRelays_PartialFailure(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		CloseError: errors.New("source close failed"),
	}
	deepfryRelay := &testutil.MockRelay{} // This one succeeds

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	// Should call both Close methods despite first failure
	forwarder.closeRelays()

	if !sourceRelay.CloseCalled {
		t.Error("expected sourceRelay.Close() to be called")
	}
	if !deepfryRelay.CloseCalled {
		t.Error("expected deepfryRelay.Close() to be called")
	}
}

func TestSyncWindow_TimestampConversion(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{},
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	// Use specific timestamps to verify conversion
	window := nsync.Window{
		From: time.Unix(1640995200, 0), // 2022-01-01 08:00:00 UTC
		To:   time.Unix(1640998800, 0), // 2022-01-01 09:00:00 UTC
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the filter had correct timestamps
	if len(sourceRelay.QueryEventsCalls) != 1 {
		t.Fatalf("expected 1 QueryEvents call, got %d", len(sourceRelay.QueryEventsCalls))
	}

	filter := sourceRelay.QueryEventsCalls[0]
	if filter.Since == nil || filter.Since.Time().Unix() != window.From.Unix() {
		t.Errorf("expected filter.Since %v, got %v", window.From.Unix(), filter.Since.Time().Unix())
	}
	if filter.Until == nil || filter.Until.Time().Unix() != window.To.Unix() {
		t.Errorf("expected filter.Until %v, got %v", window.To.Unix(), filter.Until.Time().Unix())
	}
}

// Comprehensive integration-style test that exercises multiple code paths
func TestSyncWindow_CompleteFlow(t *testing.T) {
	cfg := createTestConfig()
	cfg.Sync.MaxBatch = 5
	logger := createTestLogger()

	// Set up events to be returned by source relay
	events := []*nostr.Event{
		{
			ID:      "event1",
			Content: "test content 1",
			Kind:    1,
		},
		{
			ID:      "event2",
			Content: "test content 2",
			Kind:    1,
		},
		{
			ID:      "event3",
			Content: "test content 3",
			Kind:    1,
		},
	}

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: events,
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify all events were published (events + sync)
	expectedCalls := len(events) + 1 // +1 for sync event
	if len(deepfryRelay.PublishCalls) != expectedCalls {
		t.Fatalf("expected %d publish calls, got %d", expectedCalls, len(deepfryRelay.PublishCalls))
	}

	// Verify all regular events were published first
	for i, event := range events {
		publishedEvent := deepfryRelay.PublishCalls[i]
		if publishedEvent.ID != event.ID {
			t.Errorf("expected event %d to have ID %s, got %s", i, event.ID, publishedEvent.ID)
		}
		if publishedEvent.Content != event.Content {
			t.Errorf("expected event %d to have content %s, got %s", i, event.Content, publishedEvent.Content)
		}
	}

	// Verify sync event was published last
	syncEvent := deepfryRelay.PublishCalls[len(deepfryRelay.PublishCalls)-1]
	if syncEvent.Kind != nsync.SyncEventKind {
		t.Errorf("expected sync event kind %d, got %d", nsync.SyncEventKind, syncEvent.Kind)
	}

	// Verify sync event has correct tags
	var foundFromTag, foundToTag bool
	for _, tag := range syncEvent.Tags {
		if len(tag) >= 2 {
			switch tag[0] {
			case "from":
				expectedFrom := strconv.FormatInt(window.From.Unix(), 10)
				if tag[1] != expectedFrom {
					t.Errorf("expected from tag %s, got %s", expectedFrom, tag[1])
				}
				foundFromTag = true
			case "to":
				expectedTo := strconv.FormatInt(window.To.Unix(), 10)
				if tag[1] != expectedTo {
					t.Errorf("expected to tag %s, got %s", expectedTo, tag[1])
				}
				foundToTag = true
			}
		}
	}
	if !foundFromTag {
		t.Error("sync event missing 'from' tag")
	}
	if !foundToTag {
		t.Error("sync event missing 'to' tag")
	}
}

func TestStart_SuccessfulSync(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{}, // No existing sync events
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	// Mock the syncLoop by calling getOrCreateWindow directly
	window, err := forwarder.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if window == nil {
		t.Fatal("expected window to be created")
	}

	// Should have queried for last window
	if len(deepfryRelay.QuerySyncCalls) == 0 {
		t.Error("expected at least one QuerySync call to find last window")
	}
}

func TestGetOrCreateWindow_InvalidSyncEvent(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	// Return a sync event with invalid tags
	deepfryRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{
			{
				Tags: nostr.Tags{
					{"d", cfg.SourceRelayURL},
					{"from", "invalid"},
					{"to", "also-invalid"},
				},
			},
		},
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	// Should return error when sync event has invalid timestamps
	_, err := forwarder.getOrCreateWindow(context.Background())
	if err == nil {
		t.Fatal("expected error due to invalid sync event")
	}
	if !strings.Contains(err.Error(), "invalid syntax") {
		t.Errorf("expected invalid syntax error, got %v", err)
	}
}

func TestGetOrCreateWindow_MissingSyncEventTags(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	// Return a sync event with missing required tags
	deepfryRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{
			{
				Tags: nostr.Tags{
					{"d", cfg.SourceRelayURL},
					// Missing "from" and "to" tags
				},
			},
		},
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	// Should return error when sync event has missing tags
	_, err := forwarder.getOrCreateWindow(context.Background())
	if err == nil {
		t.Fatal("expected error due to missing sync event tags")
	}
	if !strings.Contains(err.Error(), "missing from/to tags") {
		t.Errorf("expected missing tags error, got %v", err)
	}
}

func TestGetOrCreateWindow_EmptyResponse(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()
	deepfryRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{}, // No sync events
	}

	forwarder := NewWithRelays(cfg, logger, &testutil.MockRelay{}, deepfryRelay, createNoopTelemetry())

	window, err := forwarder.getOrCreateWindow(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if window == nil {
		t.Fatal("expected window to be created")
	}

	// Should create a new current window
	duration := time.Duration(cfg.Sync.WindowSeconds) * time.Second
	now := time.Now().UTC()
	expectedStart := now.Truncate(duration)

	if window.From.Before(expectedStart.Add(-time.Second)) || window.From.After(expectedStart.Add(time.Second)) {
		t.Errorf("expected window.From to be around %v, got %v", expectedStart, window.From)
	}
}

func TestSyncWindow_EmptyEvents(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{}, // No events to sync
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should still call UpdateWindow even with no events
	if len(deepfryRelay.PublishCalls) != 1 {
		t.Fatalf("expected 1 Publish call (sync event), got %d", len(deepfryRelay.PublishCalls))
	}

	// Should be a sync event
	syncEvent := deepfryRelay.PublishCalls[0]
	if syncEvent.Kind != nsync.SyncEventKind {
		t.Errorf("expected sync event kind %d, got %d", nsync.SyncEventKind, syncEvent.Kind)
	}
}

func TestSyncWindow_MaxBatchLimit(t *testing.T) {
	cfg := createTestConfig()
	cfg.Sync.MaxBatch = 2 // Set small batch size
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QuerySyncReturn: []*nostr.Event{
			{ID: "event1"},
			{ID: "event2"},
			{ID: "event3"}, // This should not be returned due to limit
		},
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	err := forwarder.syncWindow(context.Background(), window)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify filter had correct limit
	if len(sourceRelay.QueryEventsCalls) != 1 {
		t.Fatalf("expected 1 QueryEvents call, got %d", len(sourceRelay.QueryEventsCalls))
	}

	filter := sourceRelay.QueryEventsCalls[0]
	if filter.Limit != cfg.Sync.MaxBatch {
		t.Errorf("expected filter limit %d, got %d", cfg.Sync.MaxBatch, filter.Limit)
	}
}

func TestCloseRelays_WithErrors(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		CloseError: errors.New("source close failed"),
	}
	deepfryRelay := &testutil.MockRelay{
		CloseError: errors.New("deepfry close failed"),
	}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	// Should not panic even if Close returns errors
	forwarder.closeRelays()

	if !sourceRelay.CloseCalled {
		t.Error("expected sourceRelay.Close() to be called")
	}
	if !deepfryRelay.CloseCalled {
		t.Error("expected deepfryRelay.Close() to be called")
	}
}

func TestSyncWindow_ContextCancellation(t *testing.T) {
	cfg := createTestConfig()
	logger := createTestLogger()

	sourceRelay := &testutil.MockRelay{
		QueryEventsError: context.Canceled,
	}
	deepfryRelay := &testutil.MockRelay{}

	forwarder := NewWithRelays(cfg, logger, sourceRelay, deepfryRelay, createNoopTelemetry())

	window := nsync.Window{
		From: time.Unix(1640995200, 0),
		To:   time.Unix(1640998800, 0),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := forwarder.syncWindow(ctx, window)
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}

package forwarder

import (
	"strings"
	"testing"

	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/testutil"

	"github.com/nbd-wtf/go-nostr"
)

// fakeRelayConn wraps a testutil.MockRelay to satisfy methods used by connection manager when reconnecting via Close()
type fakeRelayConn struct{ testutil.MockRelay }

func TestConnectionManager_Close_EmitsTelemetry(t *testing.T) {
	cap := testutil.NewCapturingPublisher()
	emitted := func(ev telemetry.TelemetryEvent) { cap.Publish(ev) }
	impl := &connectionManagerImpl{cfgSourceURL: "wss://src", cfgDeepfryURL: "wss://dst", telemetryEmit: emitted}
	// Pretend we connected by assigning mock relays
	impl.source = &fakeRelayConn{}
	impl.deepfry = &fakeRelayConn{}

	impl.Close()
	events := cap.Snapshot()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 telemetry events, got %d", len(events))
	}
	// Ensure the last two are connection status changes, in any order
	foundSource := false
	foundDeepfry := false
	for _, e := range events {
		if e.EventType() == "connection_status_changed" {
			c := e.(telemetry.ConnectionStatusChanged)
			if strings.Contains(c.RelayURL, "source") && !c.Connected {
				foundSource = true
			}
			if strings.Contains(c.RelayURL, "deepfry") && !c.Connected {
				foundDeepfry = true
			}
		}
	}
	if !foundSource || !foundDeepfry {
		t.Fatalf("expected disconnect telemetry for both relays; got: %#v", events)
	}
}

// Sanity test for safeCloseSubscription used in realtime strategy
func TestSafeCloseSubscription_NoPanic(t *testing.T) {
	sub := &nostr.Subscription{Events: make(chan *nostr.Event), EndOfStoredEvents: make(chan struct{}), ClosedReason: make(chan string, 1)}
	// Close twice should not panic due to recover
	safeCloseSubscription(sub)
	safeCloseSubscription(sub)
}

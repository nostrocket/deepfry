package forwarder

import (
	"log"
	"os"
	"testing"
	"time"

	"event-forwarder/pkg/nsync"
)

func TestShouldSwitchToRealtime(t *testing.T) {
	f := &Forwarder{logger: log.New(os.Stdout, "", 0)}
	now := time.Now().UTC()
	// Far-old window: To way before now
	old := nsync.Window{From: now.Add(-2 * time.Hour), To: now.Add(-110 * time.Minute)}
	if f.shouldSwitchToRealtime(old) {
		t.Fatalf("should not switch for far-old window")
	}
	// Near-now window: To within tolerance
	near := nsync.Window{From: now.Add(-2 * time.Minute), To: now.Add(-2 * time.Second)}
	if !f.shouldSwitchToRealtime(near) {
		t.Fatalf("expected to switch for near-now window")
	}
}

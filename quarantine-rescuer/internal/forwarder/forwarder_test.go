package forwarder

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"quarantine-rescuer/internal/exporter"
)

func newSilentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestForward_AllFailWhenRelayUnreachable confirms that when no relay is
// reachable, every event is reported as failed (so the deleter never gets
// the chance to remove them from quarantine — fail-closed).
func TestForward_AllFailWhenRelayUnreachable(t *testing.T) {
	f := New("ws://127.0.0.1:1", 2, 200*time.Millisecond, newSilentLogger())

	in := map[string][]exporter.RawEvent{
		"p1": {
			{ID: "a", PubKey: "p1", CreatedAt: 100, Raw: validEvent("a", "p1", 100)},
			{ID: "b", PubKey: "p1", CreatedAt: 200, Raw: validEvent("b", "p1", 200)},
		},
		"p2": {
			{ID: "c", PubKey: "p2", CreatedAt: 50, Raw: validEvent("c", "p2", 50)},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := f.Forward(ctx, in)

	if len(res.SuccessIDs) != 0 {
		t.Errorf("SuccessIDs = %v, want none", res.SuccessIDs)
	}
	if len(res.FailedIDs) != 3 {
		t.Errorf("FailedIDs = %v (len %d), want 3", res.FailedIDs, len(res.FailedIDs))
	}
}

func TestForward_EmptyInput(t *testing.T) {
	f := New("ws://127.0.0.1:1", 2, 200*time.Millisecond, newSilentLogger())
	res := f.Forward(context.Background(), map[string][]exporter.RawEvent{})
	if len(res.SuccessIDs) != 0 || len(res.FailedIDs) != 0 {
		t.Errorf("expected no work on empty input; got success=%v failed=%v", res.SuccessIDs, res.FailedIDs)
	}
}

func TestForward_DefaultsApplied(t *testing.T) {
	// Constructing with zeros should fall back to defaults; smoke test only.
	f := New("ws://127.0.0.1:1", 0, 0, nil)
	if f.workers != 4 {
		t.Errorf("workers = %d, want 4", f.workers)
	}
	if f.publishTimeout != DefaultPublishTimeout {
		t.Errorf("publishTimeout = %v, want %v", f.publishTimeout, DefaultPublishTimeout)
	}
	if f.logger == nil {
		t.Error("logger must default to slog.Default()")
	}
}

// validEvent builds a minimal go-nostr-decodable JSON event. It does not
// have a real signature, but Forward only round-trips the JSON; signature
// verification happens on the receiving relay.
func validEvent(id, pubkey string, createdAt int64) []byte {
	return []byte(`{"id":"` + id + `","pubkey":"` + pubkey + `","created_at":` +
		itoa(createdAt) + `,"kind":1,"tags":[],"content":"x","sig":"00"}`)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

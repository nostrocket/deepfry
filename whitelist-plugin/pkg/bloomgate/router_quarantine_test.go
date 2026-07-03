package bloomgate_test

import (
	"encoding/json"
	"testing"

	"whitelist-plugin/pkg/bloomgate"
	"whitelist-plugin/pkg/handler"

	"github.com/nbd-wtf/go-nostr"
)

// bloomFakeEnqueuer records events handed to the quarantine publisher. It implements
// handler.EventEnqueuer so the RouterHandler can be exercised with a real BloomChecker
// but without opening a WebSocket to a live quarantine relay.
type bloomFakeEnqueuer struct {
	events []nostr.Event
}

func (f *bloomFakeEnqueuer) Enqueue(evt nostr.Event) bool {
	f.events = append(f.events, evt)
	return true
}

func wrapBloomEvent(t *testing.T, evt nostr.Event) handler.RouterInputMsg {
	t.Helper()
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return handler.RouterInputMsg{
		Type:       "new",
		Event:      raw,
		ReceivedAt: 1700000000,
		SourceType: handler.SourceTypeIP4,
		SourceInfo: "127.0.0.1",
	}
}

// TestRouterHandlerWithBloomChecker proves the RouterHandler is genuinely
// checker-agnostic on the bloom path (M24-QUARANTINE-BLOOM): a real BloomChecker,
// backed by a filter that contains a known pubkey, drives the quarantine side-channel:
//   - in-set pubkey  → accept, no enqueue
//   - out-of-set kind-1 pubkey → reject + fire-and-forget enqueue
//
// This lives in bloomgate_test (external) to avoid the import cycle that would arise
// if package handler imported bloomgate.
func TestRouterHandlerWithBloomChecker(t *testing.T) {
	// A single known pubkey is in the filter.
	var known [32]byte
	known[0] = 0xBE
	known[31] = 0xEF
	knownHex := hexOf(known)

	checker := bloomgate.NewBloomChecker(logger())
	checker.Store(buildTestFilter(t, known)) // opens the ready gate

	enq := &bloomFakeEnqueuer{}
	h := handler.NewRouterHandler(checker, enq, true, logger())

	// In-set pubkey → accept, no quarantine enqueue.
	accepted, err := h.Handle(wrapBloomEvent(t, nostr.Event{ID: "e-known", PubKey: knownHex, Kind: 1, Content: "hi"}))
	if err != nil {
		t.Fatalf("unexpected error on in-set event: %v", err)
	}
	if accepted.Action != handler.ActionAccept {
		t.Fatalf("in-set pubkey: expected accept, got %+v", accepted)
	}
	if len(enq.events) != 0 {
		t.Fatalf("in-set pubkey: expected no enqueue, got %d", len(enq.events))
	}

	// Out-of-set kind-1 pubkey → reject + enqueue to quarantine.
	var stranger [32]byte
	stranger[0] = 0x11
	stranger[31] = 0x22
	strangerHex := hexOf(stranger)

	rejected, err := h.Handle(wrapBloomEvent(t, nostr.Event{ID: "e-stranger", PubKey: strangerHex, Kind: 1, Content: "hi"}))
	if err != nil {
		t.Fatalf("unexpected error on out-of-set event: %v", err)
	}
	if rejected.Action != handler.ActionReject {
		t.Fatalf("out-of-set pubkey: expected reject, got %+v", rejected)
	}
	if len(enq.events) != 1 || enq.events[0].ID != "e-stranger" {
		t.Fatalf("out-of-set kind-1: expected one enqueue of e-stranger, got %+v", enq.events)
	}
}

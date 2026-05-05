package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// fakeChecker implements Checker with a fixed whitelist set and an optional
// transient error to simulate whitelist-server failures.
type fakeChecker struct {
	allow map[string]bool
	err   error
}

func (f *fakeChecker) IsWhitelisted(pk string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.allow[pk], nil
}

// fakeEnqueuer records the events that were handed off to the publisher.
type fakeEnqueuer struct {
	events []nostr.Event
	full   bool
}

func (f *fakeEnqueuer) Enqueue(evt nostr.Event) bool {
	if f.full {
		return false
	}
	f.events = append(f.events, evt)
	return true
}

func wrapEvent(t *testing.T, evt nostr.Event) RouterInputMsg {
	t.Helper()
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return RouterInputMsg{
		Type:       "new",
		Event:      raw,
		ReceivedAt: 1700000000,
		SourceType: SourceTypeIP4,
		SourceInfo: "127.0.0.1",
	}
}

func baseEvt(id, pubkey string, kind int) nostr.Event {
	return nostr.Event{
		ID:      id,
		PubKey:  pubkey,
		Kind:    kind,
		Content: "hi",
	}
}

func TestRouterHandler_WhitelistedAccept(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{"pk-ok": true}}
	enq := &fakeEnqueuer{}
	var buf bytes.Buffer
	h := NewRouterHandler(checker, enq, true, log.New(&buf, "", 0))

	out, err := h.Handle(wrapEvent(t, baseEvt("e1", "pk-ok", 1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != ActionAccept || out.Id != "e1" {
		t.Fatalf("expected accept for e1, got %+v", out)
	}
	if len(enq.events) != 0 {
		t.Fatalf("expected no quarantine enqueue, got %d", len(enq.events))
	}
	if !strings.Contains(buf.String(), "decision=accept") {
		t.Fatalf("log missing decision=accept: %q", buf.String())
	}
}

func TestRouterHandler_NonWhitelistedHeuristicPass(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{}}
	enq := &fakeEnqueuer{}
	h := NewRouterHandler(checker, enq, true, log.New(&bytes.Buffer{}, "", 0))

	out, err := h.Handle(wrapEvent(t, baseEvt("e2", "pk-stranger", 1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != ActionReject || out.Msg != string(RejectReasonNotInWoT) {
		t.Fatalf("expected reject not-in-wot, got %+v", out)
	}
	if len(enq.events) != 1 || enq.events[0].ID != "e2" {
		t.Fatalf("expected quarantine enqueue of e2, got %+v", enq.events)
	}
}

func TestRouterHandler_NonWhitelistedHeuristicDrop(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{}}
	enq := &fakeEnqueuer{}
	h := NewRouterHandler(checker, enq, true, log.New(&bytes.Buffer{}, "", 0))

	// Kind 7 (reaction) is not in the allowlist.
	out, err := h.Handle(wrapEvent(t, baseEvt("e3", "pk-stranger", 7)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != ActionReject {
		t.Fatalf("expected reject, got %+v", out)
	}
	if len(enq.events) != 0 {
		t.Fatalf("expected no enqueue for disallowed kind, got %+v", enq.events)
	}
}

func TestRouterHandler_QuarantineDisabledStillRejects(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{}}
	enq := &fakeEnqueuer{}
	h := NewRouterHandler(checker, enq, false, log.New(&bytes.Buffer{}, "", 0))

	out, err := h.Handle(wrapEvent(t, baseEvt("e4", "pk-stranger", 1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != ActionReject {
		t.Fatalf("expected reject, got %+v", out)
	}
	if len(enq.events) != 0 {
		t.Fatalf("expected no enqueue when quarantine disabled, got %+v", enq.events)
	}
}

func TestRouterHandler_MalformedEvent(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{}}
	enq := &fakeEnqueuer{}
	h := NewRouterHandler(checker, enq, true, log.New(&bytes.Buffer{}, "", 0))

	// No event JSON at all.
	out, err := h.Handle(RouterInputMsg{Type: "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != ActionReject || out.Msg != string(RejectReasonMalformed) {
		t.Fatalf("expected malformed reject, got %+v", out)
	}
	if len(enq.events) != 0 {
		t.Fatalf("expected no enqueue for malformed input, got %+v", enq.events)
	}
}

func TestRouterHandler_CheckerError(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{}, err: errors.New("server unreachable")}
	enq := &fakeEnqueuer{}
	var buf bytes.Buffer
	h := NewRouterHandler(checker, enq, true, log.New(&buf, "", 0))

	out, err := h.Handle(wrapEvent(t, baseEvt("e6", "pk-stranger", 1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != ActionReject {
		t.Fatalf("expected reject, got %+v", out)
	}
	if out.Msg != string(RejectReasonCheckFailed) {
		t.Fatalf("expected msg %q, got %q", RejectReasonCheckFailed, out.Msg)
	}
	if out.Id != "e6" {
		t.Fatalf("expected id 'e6', got %q", out.Id)
	}
	if len(enq.events) != 0 {
		t.Fatalf("expected no quarantine enqueue on check failure, got %+v", enq.events)
	}
	logged := buf.String()
	if !strings.Contains(logged, "reason=check_failed") {
		t.Fatalf("expected reason=check_failed in log, got %q", logged)
	}
	if !strings.Contains(logged, "server unreachable") {
		t.Fatalf("expected underlying error in log, got %q", logged)
	}
}

func TestRouterHandler_QueueFullLogs(t *testing.T) {
	checker := &fakeChecker{allow: map[string]bool{}}
	enq := &fakeEnqueuer{full: true}
	var buf bytes.Buffer
	h := NewRouterHandler(checker, enq, true, log.New(&buf, "", 0))

	out, _ := h.Handle(wrapEvent(t, baseEvt("e5", "pk-stranger", 1)))
	if out.Action != ActionReject {
		t.Fatalf("expected reject, got %+v", out)
	}
	if !strings.Contains(buf.String(), "queue_full") {
		t.Fatalf("expected queue_full log, got %q", buf.String())
	}
}

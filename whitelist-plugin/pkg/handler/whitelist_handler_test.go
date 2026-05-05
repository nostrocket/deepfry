package handler

import (
	"bytes"
	"encoding/hex"
	"errors"
	"log"
	"testing"

	"whitelist-plugin/pkg/whitelist"
)

func makeKey(seed byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// errChecker implements Checker and always returns an error, simulating an
// unreachable whitelist server.
type errChecker struct{ err error }

func (e *errChecker) IsWhitelisted(string) (bool, error) { return false, e.err }

func TestWhitelistHandler_Handle(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "test: ", 0)

	t.Run("accept when whitelisted", func(t *testing.T) {
		buf.Reset()
		k := makeKey(0x01)
		hexKey := hex.EncodeToString(k[:])
		wl := whitelist.NewWhiteList([][32]byte{k})
		h := NewWhitelistHandler(wl, logger)

		input := InputMsg{Event: Event{ID: "evt-accept", Pubkey: hexKey}}
		out, err := h.Handle(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Action != ActionAccept {
			t.Fatalf("expected ActionAccept, got %v", out.Action)
		}
		if out.Id != "evt-accept" {
			t.Fatalf("expected id 'evt-accept', got %q", out.Id)
		}
		if out.Msg != "" {
			t.Fatalf("expected empty msg for accept, got %q", out.Msg)
		}
		if !bytes.Contains(buf.Bytes(), []byte("decision=accept")) {
			t.Errorf("expected decision=accept log, got %q", buf.String())
		}
	})

	t.Run("reject when not whitelisted", func(t *testing.T) {
		buf.Reset()
		k := makeKey(0x02)
		hexKey := hex.EncodeToString(k[:])
		wl := whitelist.NewWhiteList(nil) // empty whitelist
		h := NewWhitelistHandler(wl, logger)

		input := InputMsg{Event: Event{ID: "evt-reject", Pubkey: hexKey}}
		out, err := h.Handle(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Action != ActionReject {
			t.Fatalf("expected ActionReject, got %v", out.Action)
		}
		if out.Id != "evt-reject" {
			t.Fatalf("expected id 'evt-reject', got %q", out.Id)
		}
		if out.Msg != string(RejectReasonNotInWoT) {
			t.Fatalf("expected msg %q, got %q", RejectReasonNotInWoT, out.Msg)
		}
		if !bytes.Contains(buf.Bytes(), []byte("reason=not_in_wot")) {
			t.Errorf("expected reason=not_in_wot log, got %q", buf.String())
		}
	})

	t.Run("reject as malformed when event has no id and no pubkey", func(t *testing.T) {
		buf.Reset()
		wl := whitelist.NewWhiteList(nil)
		h := NewWhitelistHandler(wl, logger)

		input := InputMsg{Event: Event{}}
		out, err := h.Handle(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Action != ActionReject {
			t.Fatalf("expected ActionReject for malformed input, got %v", out.Action)
		}
		// RejectMalformed() leaves id empty.
		if out.Id != "" {
			t.Fatalf("expected empty id for malformed input, got %q", out.Id)
		}
		if out.Msg != string(RejectReasonMalformed) {
			t.Fatalf("expected msg %q, got %q", RejectReasonMalformed, out.Msg)
		}
		if !bytes.Contains(buf.Bytes(), []byte("reason=malformed")) {
			t.Errorf("expected reason=malformed log, got %q", buf.String())
		}
	})

	t.Run("reject as not-in-wot when only pubkey is missing", func(t *testing.T) {
		// An event with an id but no pubkey still parses (ParseEvent allows it
		// when at least one field is present), so it falls through to the
		// whitelist check with an empty pubkey and is rejected as not-in-WoT.
		buf.Reset()
		wl := whitelist.NewWhiteList(nil)
		h := NewWhitelistHandler(wl, logger)

		input := InputMsg{Event: Event{ID: "evt-nopub"}}
		out, err := h.Handle(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Action != ActionReject {
			t.Fatalf("expected ActionReject when pubkey missing, got %v", out.Action)
		}
		if out.Id != "evt-nopub" {
			t.Fatalf("expected id 'evt-nopub', got %q", out.Id)
		}
		if out.Msg != string(RejectReasonNotInWoT) {
			t.Fatalf("expected msg %q, got %q", RejectReasonNotInWoT, out.Msg)
		}
	})

	t.Run("reject as check_failed when checker errors", func(t *testing.T) {
		buf.Reset()
		k := makeKey(0x03)
		hexKey := hex.EncodeToString(k[:])
		h := NewWhitelistHandler(&errChecker{err: errors.New("server unreachable")}, logger)

		input := InputMsg{Event: Event{ID: "evt-checkfail", Pubkey: hexKey}}
		out, err := h.Handle(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Action != ActionReject {
			t.Fatalf("expected ActionReject on check failure, got %v", out.Action)
		}
		if out.Id != "evt-checkfail" {
			t.Fatalf("expected id 'evt-checkfail', got %q", out.Id)
		}
		if out.Msg != string(RejectReasonCheckFailed) {
			t.Fatalf("expected msg %q, got %q", RejectReasonCheckFailed, out.Msg)
		}
		logged := buf.String()
		if !bytes.Contains([]byte(logged), []byte("reason=check_failed")) {
			t.Errorf("expected reason=check_failed log, got %q", logged)
		}
		if !bytes.Contains([]byte(logged), []byte("server unreachable")) {
			t.Errorf("expected underlying error in log, got %q", logged)
		}
	})
}

package handler

import (
	"bytes"
	"encoding/hex"
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
		if buf.Len() == 0 || !bytes.Contains(buf.Bytes(), []byte("Handling event ID:")) {
			t.Errorf("expected logger to contain handling message, got %q", buf.String())
		}
	})

	t.Run("reject when not whitelisted", func(t *testing.T) {
		buf.Reset()
		// different key not in whitelist
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
	})

	t.Run("reject on invalid json", func(t *testing.T) {
		buf.Reset()
		wl := whitelist.NewWhiteList(nil)
		h := NewWhitelistHandler(wl, logger)

		input := InputMsg{Event: Event{}}
		out, err := h.Handle(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Action != ActionReject {
			t.Fatalf("expected ActionReject for invalid json, got %v", out.Action)
		}
		// parseEvent couldn't extract an id -> expect empty id
		if out.Id != "" {
			t.Fatalf("expected empty id for invalid json, got %q", out.Id)
		}
		if out.Msg != string(RejectReasonNotInWoT) {
			t.Fatalf("expected msg %q, got %q", RejectReasonNotInWoT, out.Msg)
		}
	})

	t.Run("reject when pubkey missing", func(t *testing.T) {
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
}

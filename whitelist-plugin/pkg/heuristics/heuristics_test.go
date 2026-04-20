package heuristics

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func baseEvent() nostr.Event {
	return nostr.Event{
		ID:      "eventid",
		PubKey:  "pubkey",
		Kind:    1,
		Content: "hello",
	}
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(e *nostr.Event)
		wantKeep   bool
		wantReason string
	}{
		{
			name:     "kind 0 accepted",
			mutate:   func(e *nostr.Event) { e.Kind = 0 },
			wantKeep: true,
		},
		{
			name:     "kind 1 accepted",
			mutate:   func(e *nostr.Event) { e.Kind = 1 },
			wantKeep: true,
		},
		{
			name:     "kind 3 accepted",
			mutate:   func(e *nostr.Event) { e.Kind = 3 },
			wantKeep: true,
		},
		{
			name:       "kind 7 rejected",
			mutate:     func(e *nostr.Event) { e.Kind = 7 },
			wantKeep:   false,
			wantReason: ReasonKindNotAllowed,
		},
		{
			name:       "kind 4 (DM) rejected",
			mutate:     func(e *nostr.Event) { e.Kind = 4 },
			wantKeep:   false,
			wantReason: ReasonKindNotAllowed,
		},
		{
			name: "oversized content rejected",
			mutate: func(e *nostr.Event) {
				e.Content = strings.Repeat("x", MaxContentBytes+1)
			},
			wantKeep:   false,
			wantReason: ReasonContentTooLarge,
		},
		{
			name: "content at limit accepted",
			mutate: func(e *nostr.Event) {
				e.Content = strings.Repeat("x", MaxContentBytes)
			},
			wantKeep: true,
		},
		{
			name:       "missing id rejected",
			mutate:     func(e *nostr.Event) { e.ID = "" },
			wantKeep:   false,
			wantReason: ReasonMissingRequiredField,
		},
		{
			name:       "missing pubkey rejected",
			mutate:     func(e *nostr.Event) { e.PubKey = "" },
			wantKeep:   false,
			wantReason: ReasonMissingRequiredField,
		},
		{
			name:     "empty content allowed",
			mutate:   func(e *nostr.Event) { e.Content = "" },
			wantKeep: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evt := baseEvent()
			tc.mutate(&evt)
			got := Filter(evt)
			if got.Keep != tc.wantKeep {
				t.Fatalf("Keep: got %v, want %v (reason=%q)", got.Keep, tc.wantKeep, got.Reason)
			}
			if !tc.wantKeep && got.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", got.Reason, tc.wantReason)
			}
			if tc.wantKeep && got.Reason != "" {
				t.Fatalf("expected empty reason on keep, got %q", got.Reason)
			}
		})
	}
}

// Package heuristics implements the MVP pre-quarantine garbage gate.
// It is NOT a spam classifier — its only job is to drop obvious junk so the
// quarantine LMDB does not fill with noise. See quarantine/SPEC.md §6.3.
package heuristics

import "github.com/nbd-wtf/go-nostr"

// MaxContentBytes is the upper bound on evt.Content length for MVP.
// Chosen to comfortably cover kind 0 / 1 / 3 payloads without room for abuse.
const MaxContentBytes = 256 * 1024

// Reason codes surfaced in logs and metrics.
const (
	ReasonKindNotAllowed       = "kind_not_allowed"
	ReasonContentTooLarge      = "content_too_large"
	ReasonMissingRequiredField = "missing_required_fields"
)

// Result communicates the filter decision plus a reason for dropped events.
type Result struct {
	Keep   bool
	Reason string
}

// keep returns an accept result.
func keep() Result { return Result{Keep: true} }

// drop returns a reject result with the supplied reason.
func drop(reason string) Result { return Result{Keep: false, Reason: reason} }

// allowedKinds is the MVP kind allowlist. See quarantine/SPEC.md §6.3 for rationale:
// 0 = profile metadata, 1 = short text note, 3 = contacts/follow list.
var allowedKinds = map[int]struct{}{
	0: {},
	1: {},
	3: {},
}

// Filter applies the MVP garbage gate to a Nostr event.
func Filter(evt nostr.Event) Result {
	if evt.ID == "" || evt.PubKey == "" {
		return drop(ReasonMissingRequiredField)
	}
	if _, ok := allowedKinds[evt.Kind]; !ok {
		return drop(ReasonKindNotAllowed)
	}
	if len(evt.Content) > MaxContentBytes {
		return drop(ReasonContentTooLarge)
	}
	return keep()
}

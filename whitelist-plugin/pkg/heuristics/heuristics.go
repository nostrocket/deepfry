// Package heuristics implements the MVP pre-quarantine garbage gate.
// It is NOT a spam classifier — its only job is to drop obvious junk so the
// quarantine LMDB does not fill with noise. See quarantine/SPEC.md §6.3.
package heuristics

import "github.com/nbd-wtf/go-nostr"

// MaxContentBytes is the 64 KB (65536-byte) upper bound on len(evt.Content).
// It is aligned to the quarantine relay's maxEventSize (config/strfry/strfry-quarantine.conf)
// so oversized events are dropped at this gate rather than forwarded and then rejected by the
// relay — a rejection closes the WebSocket with a 1006 that churns the fire-and-forget
// publisher's reconnect loop. Dropping them here eliminates that churn.
//
// Caveat: the relay's maxEventSize bounds the TOTAL serialized event size (id, pubkey, sig,
// kind, tags, content, JSON envelope), whereas MaxContentBytes bounds only len(evt.Content).
// So an event with near-limit content plus large tags could still marginally exceed the relay
// limit. This is acceptable: it removes the large-content offenders responsible for essentially
// all observed rejections; the residual near-boundary case is negligible.
const MaxContentBytes = 65536

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

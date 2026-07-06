package handler

import (
	"encoding/hex"
	"log"

	"whitelist-plugin/pkg/heuristics"

	"github.com/nbd-wtf/go-nostr"
)

// EventEnqueuer is the subset of *quarantine.Publisher the handler depends on.
// Kept as an interface so tests can inject a fake without importing the package.
type EventEnqueuer interface {
	Enqueue(evt nostr.Event) bool
}

// Deduper reports whether a decoded 32-byte event ID has been seen before,
// recording it if not. Kept as an interface (rather than importing
// pkg/dedup directly) so tests can inject a fake and so this package stays
// free of a dependency on the concrete LRU implementation.
type Deduper interface {
	SeenOrAdd(key [32]byte) bool
}

// dupLogSampleN controls how often a duplicate-accept summary line is
// emitted: one line per this many suppressed duplicates, rather than one
// line per duplicate. At tens of dups/sec, per-dup logging would recreate a
// past disk-fill incident.
const dupLogSampleN = 1000

// RouterHandler implements the event routing logic described in quarantine/SPEC.md §6.5:
//  1. Whitelisted pubkey → Accept (event lands in main StrFry).
//  2. Non-whitelisted pubkey → run heuristics; if the event passes, fire-and-forget
//     to the quarantine relay; always Reject so mainline rejects the write.
type RouterHandler struct {
	checker           Checker
	publisher         EventEnqueuer
	logger            *log.Logger
	quarantineEnabled bool

	// dedup and dupCount support the optional accepted-ID dedup gate
	// (WithDedup). Handle is called from a single goroutine (cmd/bloom's
	// sequential stdin event loop), so dupCount is a plain counter with no
	// atomics needed.
	dedup    Deduper
	dupCount uint64
}

func NewRouterHandler(checker Checker, publisher EventEnqueuer, quarantineEnabled bool, logger *log.Logger) *RouterHandler {
	return &RouterHandler{
		checker:           checker,
		publisher:         publisher,
		logger:            logger,
		quarantineEnabled: quarantineEnabled,
	}
}

// WithDedup attaches a Deduper that gates the accept branch of Handle: a
// duplicate of an already-accepted event ID is rejected instead of
// re-accepted. It is opt-in (cmd/router and existing tests never call it, so
// their behavior is byte-identical to before this change). Returns h for
// fluent construction.
func (h *RouterHandler) WithDedup(d Deduper) *RouterHandler {
	h.dedup = d
	return h
}

// decodeEventID decodes a 64-char hex event ID into a 32-byte array. It
// returns false (without error) for anything that isn't exactly 64 hex
// chars, so a malformed/unexpected ID silently skips dedup rather than
// erroring the whole request.
func decodeEventID(id string) ([32]byte, bool) {
	var key [32]byte
	if len(id) != 64 {
		return key, false
	}
	decoded, err := hex.DecodeString(id)
	if err != nil {
		return key, false
	}
	copy(key[:], decoded)
	return key, true
}

// Handle applies the routing decision. Called once per stdin line.
func (h *RouterHandler) Handle(input RouterInputMsg) (OutputMsg, error) {
	evt, err := input.ParseFullEvent()
	if err != nil {
		if h.logger != nil {
			h.logger.Printf("malformed event: %v", err)
		}
		return RejectMalformed(), nil
	}

	ok, checkErr := h.checker.IsWhitelisted(evt.PubKey)
	if checkErr != nil {
		h.log("decision=reject id=%s pubkey=%s reason=check_failed err=%v", evt.ID, pubkeyPrefix(evt.PubKey), checkErr)
		return Reject(evt.ID, RejectReasonCheckFailed), nil
	}
	if ok {
		if h.dedup != nil {
			if key, decodable := decodeEventID(evt.ID); decodable && h.dedup.SeenOrAdd(key) {
				h.dupCount++
				if h.dupCount%dupLogSampleN == 0 {
					h.log("dedup: suppressed %d duplicate accepts (empty-msg rejects)", h.dupCount)
				}
				return RejectDuplicate(evt.ID), nil
			}
		}
		h.log("decision=accept id=%s pubkey=%s", evt.ID, pubkeyPrefix(evt.PubKey))
		return Accept(evt.ID), nil
	}

	if h.quarantineEnabled && h.publisher != nil {
		if res := heuristics.Filter(evt); res.Keep {
			if h.publisher.Enqueue(evt) {
				h.log("decision=reject id=%s pubkey=%s reason=not_in_wot quarantined=y", evt.ID, pubkeyPrefix(evt.PubKey))
			} else {
				h.log("decision=reject id=%s pubkey=%s reason=not_in_wot quarantined=n cause=queue_full", evt.ID, pubkeyPrefix(evt.PubKey))
			}
		} else {
			h.log("decision=reject id=%s pubkey=%s reason=not_in_wot quarantined=n cause=%s", evt.ID, pubkeyPrefix(evt.PubKey), res.Reason)
		}
	} else {
		h.log("decision=reject id=%s pubkey=%s reason=not_in_wot quarantined=n cause=quarantine_disabled", evt.ID, pubkeyPrefix(evt.PubKey))
	}

	return Reject(evt.ID, RejectReasonNotInWoT), nil
}

func (h *RouterHandler) log(format string, args ...any) {
	if h.logger != nil {
		h.logger.Printf(format, args...)
	}
}

// pubkeyPrefix returns the first 8 chars of a hex pubkey for log lines.
// We never log full event content; pubkey prefix is enough to correlate events.
func pubkeyPrefix(pk string) string {
	if len(pk) <= 8 {
		return pk
	}
	return pk[:8]
}

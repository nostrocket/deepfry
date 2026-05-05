package handler

import (
	"log"

	"whitelist-plugin/pkg/heuristics"

	"github.com/nbd-wtf/go-nostr"
)

// EventEnqueuer is the subset of *quarantine.Publisher the handler depends on.
// Kept as an interface so tests can inject a fake without importing the package.
type EventEnqueuer interface {
	Enqueue(evt nostr.Event) bool
}

// RouterHandler implements the event routing logic described in quarantine/SPEC.md §6.5:
//  1. Whitelisted pubkey → Accept (event lands in main StrFry).
//  2. Non-whitelisted pubkey → run heuristics; if the event passes, fire-and-forget
//     to the quarantine relay; always Reject so mainline rejects the write.
type RouterHandler struct {
	checker           Checker
	publisher         EventEnqueuer
	logger            *log.Logger
	quarantineEnabled bool
}

func NewRouterHandler(checker Checker, publisher EventEnqueuer, quarantineEnabled bool, logger *log.Logger) *RouterHandler {
	return &RouterHandler{
		checker:           checker,
		publisher:         publisher,
		logger:            logger,
		quarantineEnabled: quarantineEnabled,
	}
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

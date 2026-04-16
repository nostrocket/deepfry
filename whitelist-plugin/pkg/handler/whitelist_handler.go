package handler

import (
	"log"
)

type WhitelistHandler struct {
	checker Checker
	logger  *log.Logger
}

func NewWhitelistHandler(checker Checker, logger *log.Logger) *WhitelistHandler {
	return &WhitelistHandler{checker: checker, logger: logger}
}

func (h *WhitelistHandler) Handle(input InputMsg) (OutputMsg, error) {
	// Extract event ID and pubkey from the event
	eventId, pubkey, err := input.ParseEvent()

	if err != nil {
		return Reject(eventId, RejectReasonNotInWoT), nil
	}

	if h.logger != nil {
		h.logger.Printf("Handling event ID: %s", eventId)
	}

	// Check whitelist
	if h.checker.IsWhitelisted(pubkey) {
		return Accept(eventId), nil
	}

	return Reject(eventId, RejectReasonNotInWoT), nil
}

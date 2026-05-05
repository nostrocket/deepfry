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
	eventId, pubkey, err := input.ParseEvent()
	if err != nil {
		h.log("decision=reject reason=malformed err=%v", err)
		return RejectMalformed(), nil
	}

	ok, checkErr := h.checker.IsWhitelisted(pubkey)
	if checkErr != nil {
		h.log("decision=reject id=%s pubkey=%s reason=check_failed err=%v", eventId, pubkeyPrefix(pubkey), checkErr)
		return Reject(eventId, RejectReasonCheckFailed), nil
	}
	if ok {
		h.log("decision=accept id=%s pubkey=%s", eventId, pubkeyPrefix(pubkey))
		return Accept(eventId), nil
	}
	h.log("decision=reject id=%s pubkey=%s reason=not_in_wot", eventId, pubkeyPrefix(pubkey))
	return Reject(eventId, RejectReasonNotInWoT), nil
}

func (h *WhitelistHandler) log(format string, args ...any) {
	if h.logger != nil {
		h.logger.Printf(format, args...)
	}
}

package handler

import (
	"log"
	"whitelist-plugin/pkg/whitelist"
)

type WhitelistHandler struct {
	whitelist *whitelist.Whitelist
	logger    *log.Logger
}

func NewWhitelistHandler(wl *whitelist.Whitelist, logger *log.Logger) *WhitelistHandler {
	return &WhitelistHandler{whitelist: wl, logger: logger}
}

func (h *WhitelistHandler) Handle(input InputMsg) (OutputMsg, error) {
	// Extract event ID and pubkey from the event JSON
	eventId, pubkey, err := input.ParseEvent()

	if err != nil {
		return Reject(eventId, RejectReasonNotInWoT), nil
	}

	// Check whitelist
	if h.whitelist.IsWhitelisted(pubkey) {
		return Accept(eventId), nil
	}

	return Reject(eventId, RejectReasonNotInWoT), nil
}

// func (h *WhitelistHandler) parseEvent(eventJSON string) (id, pubkey string, err error) {
// 	var event struct {
// 		ID     string `json:"id"`
// 		Pubkey string `json:"pubkey"`
// 	}

// 	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
// 		return "", "", err
// 	}

// 	return event.ID, event.Pubkey, nil
// }

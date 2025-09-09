package handler

import (
	"encoding/json"
	"fmt"
)

// EventType represents the type of event (currently always "new").
const EventType = "new"

// SourceType represents the channel source of the event.
type SourceType string

// Allowed SourceType values.
const (
	SourceTypeIP4    SourceType = "IP4"
	SourceTypeIP6    SourceType = "IP6"
	SourceTypeImport SourceType = "Import"
	SourceTypeStream SourceType = "Stream"
	SourceTypeSync   SourceType = "Sync"
	SourceTypeStored SourceType = "Stored"
)

// Action represents the output action for the event.
type Action string

// Allowed Action values.
const (
	ActionAccept       Action = "accept"
	ActionReject       Action = "reject"
	ActionShadowReject Action = "shadowReject"
)

// RejectReason represents the reason for rejecting an event.
type RejectReason string

// Allowed RejectReason values.
const (
	RejectReasonNotInWoT  RejectReason = "rejected: not in web of trust"
	RejectReasonMalformed RejectReason = "rejected: malformed event"
	RejectReasonInternal  RejectReason = "rejected: internal error"
)

type Event struct {
	ID     string `json:"id"`
	Pubkey string `json:"pubkey"`
}

// InputMsg represents the input message structure for the Strfry plugin.
// This is received as JSONL from the relay for new events.
type InputMsg struct {
	Type       string     `json:"type"`       // Currently always "new" (use EventType constant)
	Event      Event      `json:"event"`      // The event object posted by the client (we only deserialize id and pubkey)
	ReceivedAt int64      `json:"receivedAt"` // Unix timestamp of when the event was received by the relay
	SourceType SourceType `json:"sourceType"` // Channel source: one of the SourceType constants
	SourceInfo string     `json:"sourceInfo"` // Specifics of the source, e.g., an IP address
}

// OutputMsg represents the output message structure for the Strfry plugin.
// This is sent as JSONL in response to input events.
type OutputMsg struct {
	Id     string `json:"id"`     // Event ID from the input event.id field
	Action Action `json:"action"` // One of the Action constants
	Msg    string `json:"msg"`    // NIP-20 response message (e.g., ["OK", "<event_id>", true, ""] or error); only used for "reject"
}

// SerializeInputMsg serializes an InputMsg to minified JSONL (JSON + newline).
func SerializeInputMsg(msg InputMsg) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize InputMsg: %w", err)
	}
	return append(data, '\n'), nil
}

// DeserializeInputMsg deserializes a JSONL line to an InputMsg.
func DeserializeInputMsg(data []byte) (InputMsg, error) {
	var msg InputMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return msg, fmt.Errorf("failed to deserialize InputMsg: %w", err)
	}
	return msg, nil
}

// SerializeOutputMsg serializes an OutputMsg to minified JSONL (JSON + newline).
func SerializeOutputMsg(msg OutputMsg) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize OutputMsg: %w", err)
	}
	return append(data, '\n'), nil
}

// DeserializeOutputMsg deserializes a JSONL line to an OutputMsg.
func DeserializeOutputMsg(data []byte) (OutputMsg, error) {
	var msg OutputMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return msg, fmt.Errorf("failed to deserialize OutputMsg: %w", err)
	}
	return msg, nil
}

// Accept creates an OutputMsg for accepting an event.
func Accept(eventId string) OutputMsg {
	return OutputMsg{
		Id:     eventId,
		Action: ActionAccept,
		Msg:    "",
	}
}

// Reject creates an OutputMsg for rejecting an event with a specified reason.
func Reject(eventId string, reason RejectReason) OutputMsg {
	return OutputMsg{
		Id:     eventId,
		Action: ActionReject,
		Msg:    string(reason),
	}
}

func RejectMalformed() OutputMsg {
	return OutputMsg{
		Id:     "",
		Action: ActionReject,
		Msg:    string(RejectReasonMalformed),
	}
}

func RejectInternal(eventId string) OutputMsg {
	return OutputMsg{
		Id:     eventId,
		Action: ActionReject,
		Msg:    string(RejectReasonInternal),
	}
}

func RejectInternalWithError(eventId string, err error) OutputMsg {
	return OutputMsg{
		Id:     eventId,
		Action: ActionReject,
		Msg:    fmt.Sprintf("%s: %v", string(RejectReasonInternal), err),
	}
}

func (i *InputMsg) ParseEvent() (id string, pubKey string, err error) {
	// With Event as a structured field, simply return the values and validate
	if i.Event.ID == "" && i.Event.Pubkey == "" {
		return "", "", fmt.Errorf("missing event fields: id and pubkey")
	}
	return i.Event.ID, i.Event.Pubkey, nil
}

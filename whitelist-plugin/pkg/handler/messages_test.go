package handler

import (
	"fmt"
	"reflect"
	"testing"
)

var validCases = []struct {
	name string
	msg  string
}{
	{name: "valid message", msg: `{"type":"new","event":"{\"id\":\"eventid\",\"pubkey\":\"pubkey\",\"content\":\"content\"}","receivedAt":1625079600,"sourceType":"ip4","sourceInfo":"source info"}`},
	{name: "missing fields", msg: `{"type": "new"}`},
	{name: "actual message", msg: `{"event":{"content":"{\"peerId\":\"15ZgsH1Hupauf6IhCvcj\"}","created_at":1757318487,"id":"7d9d3f82ed97eac48f22549d9223e939dbda56c5818dbd06e6227694323ef4a1","kind":22712,"pubkey":"7eb315fcec3ff6b2205d0b5c846c33713ec70f1a8ba5f8612c2db12b95ae62a9","sig":"49aabcf459882d78c669686a4f23f60f4d144ba7ff10c20f672fcb344459cc71a49040365808edb568ad68bfff0aee75023c270310c794cb64c0bf4ede3dc9c9","tags":[["x","2n4y1851603c6q1w35616g6t3o6i1j552543313p"]]},"receivedAt":1757318499,"sourceInfo":"172.20.0.1","sourceType":"IP4","type":"new"}`},
}

var invalidCases = []struct {
	name string
	msg  string
}{
	{name: "malformed JSON", msg: `{"type": "new", "event": "content", "receivedAt": notanumber}`},
	{name: "incomplete JSON", msg: `{"type": "new", "event": "content"`},
	{name: "wrong type", msg: `["type", "new"]`},
	{name: "empty string", msg: ``},
	{name: "not JSON", msg: `Just a plain string`},
	{name: "number", msg: `12345`},
	{name: "boolean", msg: `true`},
	{name: "array", msg: `["new", "event"]`},
	{name: "object with wrong types", msg: `{"type": 123, "event": false, "receivedAt": "string"}`},
	{name: "extra comma", msg: `{"type": "new", "event": "content",}`},
	{name: "invalid unicode", msg: `{"type": "new", "event": "content\uZZZZ"}`},
	{name: "very large number", msg: `{"type": "new", "event": "content", "receivedAt": 12345678901234567890}`},
	{name: "float instead of int", msg: `{"type": "new", "event": "content", "receivedAt": 1625079600.123}`},
	{name: "boolean instead of string", msg: `{"type": true, "event": "content", "receivedAt": 1625079600}`},
}

func TestValidInputMessageDeserialization(t *testing.T) {

	for _, test := range validCases {
		t.Run(fmt.Sprintf("%s is serialised", test.name), func(t *testing.T) {
			got, err := DeserializeInputMsg([]byte(test.msg))
			if err != nil {
				t.Errorf("failed to deserialize InputMsg: %v", err)
				return
			}
			if got.Type != "new" {
				t.Errorf("expected Type 'new', got '%s'", got.Type)
			}
		})
	}
}

func TestInvalidInputMessageDeserialization(t *testing.T) {
	for _, test := range invalidCases {
		t.Run(fmt.Sprintf("%s is not serialised", test.name), func(t *testing.T) {
			_, err := DeserializeInputMsg([]byte(test.msg))
			if err == nil {
				t.Errorf("expected error when deserializing invalid message, got nil")
			}
		})
	}
}

// TestSerializeInputMsg tests serialization of InputMsg to JSONL.
func TestSerializeInputMsg(t *testing.T) {
	tests := []struct {
		name    string
		input   InputMsg
		wantErr bool
	}{
		{
			name: "valid input",
			input: InputMsg{
				Type:       EventType,
				Event:      `{"id":"test","pubkey":"pub"}`,
				ReceivedAt: 1234567890,
				SourceType: SourceTypeIP4,
				SourceInfo: "127.0.0.1",
			},
			wantErr: false,
		},
		{
			name:    "empty input",
			input:   InputMsg{},
			wantErr: false,
		},
		// Add more cases if needed for edge coverage
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SerializeInputMsg(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("SerializeInputMsg() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) == 0 {
				t.Errorf("SerializeInputMsg() returned empty data")
			}
			// Verify ends with newline
			if !tt.wantErr && got[len(got)-1] != '\n' {
				t.Errorf("SerializeInputMsg() does not end with newline")
			}
		})
	}
}

// TestDeserializeInputMsg tests deserialization with validation.
func TestDeserializeInputMsg(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    InputMsg
		wantErr bool
	}{
		{
			name:  "valid input",
			input: []byte(`{"type":"new","event":"test","receivedAt":123,"sourceType":"IP4","sourceInfo":"info"}`),
			want: InputMsg{
				Type:       EventType,
				Event:      "test",
				ReceivedAt: 123,
				SourceType: SourceTypeIP4,
				SourceInfo: "info",
			},
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			input:   []byte(`{"type": "new", "event": "test"`),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeserializeInputMsg(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeserializeInputMsg() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DeserializeInputMsg() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSerializeOutputMsg tests serialization of OutputMsg to JSONL.
func TestSerializeOutputMsg(t *testing.T) {
	tests := []struct {
		name    string
		input   OutputMsg
		wantErr bool
	}{
		{
			name: "accept action",
			input: OutputMsg{
				Id:     "test-id",
				Action: ActionAccept,
				Msg:    "",
			},
			wantErr: false,
		},
		{
			name: "reject action with msg",
			input: OutputMsg{
				Id:     "test-id",
				Action: ActionReject,
				Msg:    `["OK", "test-id", false, "error"]`,
			},
			wantErr: false,
		},
		{
			name: "shadowReject action",
			input: OutputMsg{
				Id:     "test-id",
				Action: ActionShadowReject,
				Msg:    "",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SerializeOutputMsg(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("SerializeOutputMsg() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) == 0 {
				t.Errorf("SerializeOutputMsg() returned empty data")
			}
			if !tt.wantErr && got[len(got)-1] != '\n' {
				t.Errorf("SerializeOutputMsg() does not end with newline")
			}
		})
	}
}

// TestDeserializeOutputMsg tests deserialization of OutputMsg.
func TestDeserializeOutputMsg(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    OutputMsg
		wantErr bool
	}{
		{
			name:  "valid output",
			input: []byte(`{"id":"test-id","action":"accept","msg":""}`),
			want: OutputMsg{
				Id:     "test-id",
				Action: ActionAccept,
				Msg:    "",
			},
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			input:   []byte(`{"id": "test-id", "action": "accept"`),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeserializeOutputMsg(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeserializeOutputMsg() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DeserializeOutputMsg() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConstants tests that constants are defined correctly.
func TestConstants(t *testing.T) {
	if EventType != "new" {
		t.Errorf("EventType = %v, want 'new'", EventType)
	}
	if SourceTypeIP4 != "IP4" {
		t.Errorf("SourceTypeIP4 = %v, want 'IP4'", SourceTypeIP4)
	}
	if ActionAccept != "accept" {
		t.Errorf("ActionAccept = %v, want 'accept'", ActionAccept)
	}
	// Test all constants similarly
}

// TestAccept tests the Accept function.
func TestAccept(t *testing.T) {
	eventId := "test-event-id"
	got := Accept(eventId)
	want := OutputMsg{
		Id:     eventId,
		Action: ActionAccept,
		Msg:    "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Accept() = %v, want %v", got, want)
	}
}

// TestReject tests the Reject function.
func TestReject(t *testing.T) {
	tests := []struct {
		name     string
		eventId  string
		reason   RejectReason
		expected OutputMsg
	}{
		{
			name:    "reject with default reason",
			eventId: "test-event-id",
			reason:  RejectReasonNotInWoT,
			expected: OutputMsg{
				Id:     "test-event-id",
				Action: ActionReject,
				Msg:    string(RejectReasonNotInWoT),
			},
		},
		// Add more test cases if you add more RejectReason constants
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Reject(tt.eventId, tt.reason)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Reject() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestRejectReasonConstants tests that RejectReason constants are defined correctly.
func TestRejectReasonConstants(t *testing.T) {
	if RejectReasonNotInWoT != "rejected: not in web of trust" {
		t.Errorf("RejectReasonNotInWoT = %v, want 'rejected: not in web of trust'", RejectReasonNotInWoT)
	}
	// Add tests for additional constants as they are added
}

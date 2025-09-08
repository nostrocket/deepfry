package handler

import (
	"bytes"
	"testing"
)

func TestJSONLIOAdapter_Input_Valid(t *testing.T) {
	adapter := NewJSONLIOAdapter(&bytes.Buffer{})
	input := []byte(`{"type":"new","event":"{\"id\":\"abc\",\"pubkey\":\"def\"}","receivedAt":123,"sourceType":"IP4","sourceInfo":"info"}`)

	msg, err := adapter.Input(input)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if msg.Type != "new" {
		t.Errorf("expected type 'new', got %q", msg.Type)
	}
}

func TestJSONLIOAdapter_Input_Invalid(t *testing.T) {
	adapter := NewJSONLIOAdapter(&bytes.Buffer{})
	input := []byte(`not a json`)

	_, err := adapter.Input(input)
	if err == nil {
		t.Error("expected error for invalid input, got nil")
	}
}

func TestJSONLIOAdapter_Output(t *testing.T) {
	adapter := NewJSONLIOAdapter(&bytes.Buffer{})
	outMsg := OutputMsg{
		Id:     "abc",
		Action: ActionAccept,
		Msg:    "",
	}
	data, err := adapter.Output(outMsg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("expected output to end with newline")
	}
}

func TestJSONLIOAdapter_Flush(t *testing.T) {
	var buf bytes.Buffer
	adapter := NewJSONLIOAdapter(&buf)
	err := adapter.Flush()
	if err != nil {
		t.Errorf("expected no error from Flush, got %v", err)
	}
}

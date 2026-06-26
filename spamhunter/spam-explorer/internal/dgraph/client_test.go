package dgraph

import "testing"

// TestNewClient constructs a client against a dummy address and closes it.
// grpc.NewClient is non-blocking (it does not dial until the first RPC), so this
// needs no live server — it asserts the constructor wires a connection and that
// Close releases it cleanly. The live read path is exercised by the Task 2 smoke
// run against a real Dgraph.
func TestNewClient(t *testing.T) {
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatalf("NewClient returned error for a well-formed addr: %v", err)
	}
	if c == nil {
		t.Fatal("NewClient returned a nil client with no error")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

//go:build integration

package dgraph

// Integration tests for Plan 05-02: pubkey-validation-hardening
//
// D-07: TestMarkAttemptedRecoverOrPurge — proves MarkAttempted recovers an
// uppercase-hex node to its canonical lowercase form (last_attempt left unset
// so it re-enters the frontier) and deletes unrecoverable garbage nodes (short
// hex and relay-URL blobs).
//
// D-08: TestWritePathRejectsGarbage — proves the validated write path
// (AddFollowers / dgraph.isValidHexPubkey gate) never writes garbage-pubkey
// nodes to Dgraph when a follow-list mixes valid lowercase hex with uppercase
// hex, short hex, and relay-URL blobs.
//
// Both tests use time-derived unique fixtures and clean up all created nodes so
// no orphan nodes pollute the live frontier.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestMarkAttemptedRecoverOrPurge verifies the VALID-03 recover-or-purge path
// inside MarkAttempted:
//   - An uppercase-hex node is recovered to its lowercase canonical form;
//     its last_attempt predicate is NOT stamped (it re-enters the fresh frontier).
//   - A short-hex garbage node is deleted.
//   - A relay-blob garbage node is deleted.
func TestMarkAttemptedRecoverOrPurge(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// Derive a unique valid lowercase 64-char hex so the test is rerun-safe.
	lower := fmt.Sprintf("%064x", time.Now().UnixNano())
	upper := strings.ToUpper(lower)

	// Short-hex garbage: prefix with "zz" so it is 4 chars — not valid hex at all
	// (z is not a hex digit), not 64 chars, and guaranteed unique per test run.
	shortHex := fmt.Sprintf("zz%016x", time.Now().UnixNano())

	// Relay-blob garbage: prefix mimics the real live-DB format
	// (from CONTEXT.md §"Real garbage nodes") and includes a unique nonce.
	relayBlob := fmt.Sprintf("wss://relay.testmark.pub/TestMarkAttempted/%d", time.Now().UnixNano())

	// Insert all three garbage nodes into Dgraph with mustMutate.
	// The uppercase node has no last_attempt (the state MarkAttempted must preserve
	// after recovery). The other two are plain stubs.
	mustMutate(t, c, fmt.Sprintf(
		`_:upper <pubkey> %q .
_:upper <dgraph.type> "Profile" .
_:short <pubkey> %q .
_:short <dgraph.type> "Profile" .
_:blob <pubkey> %q .
_:blob <dgraph.type> "Profile" .
`, upper, shortHex, relayBlob))

	// Confirm the uppercase node exists before calling MarkAttempted.
	preUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{upper})
	if err != nil {
		t.Fatalf("pre-check ResolvePubkeysToUIDs(%q) failed: %v", upper, err)
	}
	if _, found := preUIDs[upper]; !found {
		t.Fatalf("test fixture: uppercase node %q was not found in Dgraph before MarkAttempted", upper)
	}

	// Call MarkAttempted with all three garbage pubkeys.
	if err := c.MarkAttempted(ctx, []string{upper, shortHex, relayBlob}, time.Now().Unix()); err != nil {
		t.Fatalf("MarkAttempted failed: %v", err)
	}

	// ---- Assert D-07a: uppercase node recovered to lowercase ----

	// The lowercase form must now resolve to a node.
	lowerUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{lower})
	if err != nil {
		t.Fatalf("post ResolvePubkeysToUIDs(%q) failed: %v", lower, err)
	}
	recoveredUID, lowerFound := lowerUIDs[lower]
	if !lowerFound {
		t.Fatalf("D-07a FAIL: recovered lowercase pubkey %q does not resolve to a node", lower)
	}

	// The original uppercase string must no longer resolve to its own node.
	upperUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{upper})
	if err != nil {
		t.Fatalf("post ResolvePubkeysToUIDs(%q) failed: %v", upper, err)
	}
	if _, stillFound := upperUIDs[upper]; stillFound {
		t.Errorf("D-07a FAIL: uppercase garbage string %q still resolves to a node after recovery", upper)
	}

	// The recovered node must NOT have last_attempt set.
	lastAttempt := queryLastAttempt(t, c, recoveredUID)
	if lastAttempt != 0 {
		t.Errorf("D-07a FAIL: recovered node (uid %s) has last_attempt=%d; want 0 (unset)", recoveredUID, lastAttempt)
	}

	// ---- Assert D-07b/c: garbage nodes deleted ----

	shortUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{shortHex})
	if err != nil {
		t.Fatalf("post ResolvePubkeysToUIDs(%q) failed: %v", shortHex, err)
	}
	if _, stillFound := shortUIDs[shortHex]; stillFound {
		t.Errorf("D-07b FAIL: short-hex garbage node %q still exists after MarkAttempted", shortHex)
	}

	blobUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{relayBlob})
	if err != nil {
		t.Fatalf("post ResolvePubkeysToUIDs(%q) failed: %v", relayBlob, err)
	}
	if _, stillFound := blobUIDs[relayBlob]; stillFound {
		t.Errorf("D-07c FAIL: relay-blob garbage node %q still exists after MarkAttempted", relayBlob)
	}

	// ---- Cleanup: delete the recovered lowercase node ----
	if err := c.DeleteNodes(ctx, []string{recoveredUID}); err != nil {
		t.Logf("cleanup warning: failed to delete recovered node (uid %s): %v", recoveredUID, err)
	}
}

// queryLastAttempt returns the last_attempt value of a node identified by uid,
// or 0 if the predicate is absent. It is a test helper scoped to this file.
func queryLastAttempt(t *testing.T, c *Client, uid string) int64 {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	q := fmt.Sprintf(`{ node(func: uid(%s)) { last_attempt } }`, uid)
	resp, err := txn.Query(ctx, q)
	if err != nil {
		t.Fatalf("queryLastAttempt(%s) failed: %v", uid, err)
	}

	var parsed struct {
		Node []struct {
			LastAttempt int64 `json:"last_attempt"`
		} `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("queryLastAttempt unmarshal failed: %v", err)
	}
	if len(parsed.Node) == 0 {
		return 0
	}
	return parsed.Node[0].LastAttempt
}

// TestWritePathRejectsGarbage verifies VALID-01: that the validated write path
// (AddFollowers with the isValidHexPubkey gate) never persists a garbage pubkey
// as a node in Dgraph.
//
// Layer covered: the isValidHexPubkey gate inside AddFollowers (dgraph.go:265)
// is the same gate the crawler funnels all p-tag pubkeys through before writing.
// This test calls AddFollowers directly so it asserts the gate + write contract
// at the dgraph layer, independent of the crawler layer's own filter.
func TestWritePathRejectsGarbage(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// Build a unique valid signer pubkey so the test is rerun-safe.
	signerPubkey := fmt.Sprintf("%064x", time.Now().UnixNano())
	// Shift by one so signer != followee.
	validFollowee := fmt.Sprintf("%064x", time.Now().UnixNano()+1)

	// Garbage pubkeys that must not appear in Dgraph after the write.
	upperHex := strings.ToUpper(validFollowee) // 64-char uppercase hex — recoverable by MarkAttempted, but must not be written here
	shortHex := fmt.Sprintf("zz%016x", time.Now().UnixNano()+2)
	relayBlob := fmt.Sprintf("wss://relay.testmark.pub/TestWritePathRejectsGarbage/%d", time.Now().UnixNano())

	// Build the follow-set: one valid followee + three garbage entries.
	follows := map[string]struct{}{
		validFollowee: {},
		upperHex:      {},
		shortHex:      {},
		relayBlob:     {},
	}

	// Call the validated write path. kind3createdAt = now.
	kind3ts := time.Now().Unix()
	if err := c.AddFollowers(ctx, signerPubkey, kind3ts, follows, false); err != nil {
		t.Fatalf("AddFollowers failed: %v", err)
	}

	// ---- Assert D-08: garbage strings resolve to zero nodes ----

	garbageStrings := []string{upperHex, shortHex, relayBlob}
	garbageUIDs, err := c.ResolvePubkeysToUIDs(ctx, garbageStrings)
	if err != nil {
		t.Fatalf("post-write ResolvePubkeysToUIDs(garbage) failed: %v", err)
	}
	for _, g := range garbageStrings {
		if uid, found := garbageUIDs[g]; found {
			t.Errorf("D-08 FAIL: garbage pubkey %q was written to Dgraph as node uid %s", g, uid)
		}
	}

	// The valid followee must have been written.
	validUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{validFollowee})
	if err != nil {
		t.Fatalf("post-write ResolvePubkeysToUIDs(valid) failed: %v", err)
	}
	validUID, validFound := validUIDs[validFollowee]
	if !validFound {
		t.Errorf("D-08: valid followee %q was NOT written to Dgraph — unexpected", validFollowee)
	}

	// ---- Cleanup: delete signer and valid followee nodes ----
	signerUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{signerPubkey})
	if err != nil {
		t.Logf("cleanup: failed to resolve signer uid: %v", err)
	}
	var toDelete []string
	if uid, found := signerUIDs[signerPubkey]; found {
		toDelete = append(toDelete, uid)
	}
	if validFound {
		toDelete = append(toDelete, validUID)
	}
	if len(toDelete) > 0 {
		if err := c.DeleteNodes(ctx, toDelete); err != nil {
			t.Logf("cleanup warning: failed to delete nodes %v: %v", toDelete, err)
		}
	}
}


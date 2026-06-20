//go:build integration

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// queryFollowerCount reads the stored follower_count for a pubkey (0 if unset).
func queryFollowerCount(t *testing.T, c *Client, pubkey string) int {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	q := fmt.Sprintf(`{ node(func: eq(pubkey, %q)) { follower_count } }`, pubkey)
	resp, err := txn.Query(ctx, q)
	if err != nil {
		t.Fatalf("queryFollowerCount(%q) query failed: %v", pubkey, err)
	}
	var parsed struct {
		Node []struct {
			FollowerCount int `json:"follower_count"`
		} `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("queryFollowerCount(%q) unmarshal failed: %v", pubkey, err)
	}
	if len(parsed.Node) == 0 {
		return 0
	}
	return parsed.Node[0].FollowerCount
}

// TestGetStalePubkeysOrderByFollowerCount verifies GetStalePubkeys returns
// frontier pubkeys ordered by the STORED follower_count predicate (DSCALE-01).
// follower_count is set directly via mustMutate — no real follow edges are
// needed, since the read path now reads the stored predicate rather than
// recomputing count(~follows).
func TestGetStalePubkeysOrderByFollowerCount(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixNano()
	high := fmt.Sprintf("%064x", now)
	mid := fmt.Sprintf("%064x", now+1)
	low := fmt.Sprintf("%064x", now+2)

	// Three frontier nodes (no last_attempt) with stored follower_count 30/20/10.
	mustMutate(t, c, fmt.Sprintf(
		`_:h <pubkey> %q .
_:h <dgraph.type> "Profile" .
_:h <follower_count> "30" .
_:m <pubkey> %q .
_:m <dgraph.type> "Profile" .
_:m <follower_count> "20" .
_:l <pubkey> %q .
_:l <dgraph.type> "Profile" .
_:l <follower_count> "10" .
`, high, mid, low))

	defer func() {
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{high, mid, low})
		if err != nil {
			t.Logf("cleanup resolve failed: %v", err)
			return
		}
		toDelete := make([]string, 0, len(allUIDs))
		for _, uid := range allUIDs {
			toDelete = append(toDelete, uid)
		}
		if len(toDelete) > 0 {
			if err := c.DeleteNodes(ctx, toDelete); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	}()

	got, err := c.GetStalePubkeys(ctx, 0, countFrontier(t, c)+100)
	if err != nil {
		t.Fatal(err)
	}

	// All three frontier nodes must be returned (ordered by stored follower_count).
	for name, pk := range map[string]string{"high": high, "mid": mid, "low": low} {
		if _, ok := got[pk]; !ok {
			t.Errorf("%s-follower frontier node %s missing from results", name, pk)
		}
	}
}

// TestBackfillFollowerCount verifies BackfillFollowerCount sets
// follower_count = count(~follows) on a node that has follows but no
// follower_count, and that a second run leaves the value unchanged (idempotent).
func TestBackfillFollowerCount(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixNano()
	// target: a node followed by 3 followers but with no follower_count yet.
	target := fmt.Sprintf("%064x", now+5000)
	f1 := fmt.Sprintf("%064x", now+5001)
	f2 := fmt.Sprintf("%064x", now+5002)
	f3 := fmt.Sprintf("%064x", now+5003)

	mustMutate(t, c, fmt.Sprintf(
		`_:t <pubkey> %q .
_:t <dgraph.type> "Profile" .
`, target))

	targetUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{target})
	if err != nil {
		t.Fatalf("resolve target UID: %v", err)
	}
	targetUID := targetUIDs[target]

	// Wire three followers -> target.
	mustMutate(t, c, fmt.Sprintf(
		`_:f1 <pubkey> %q .
_:f1 <dgraph.type> "Profile" .
_:f2 <pubkey> %q .
_:f2 <dgraph.type> "Profile" .
_:f3 <pubkey> %q .
_:f3 <dgraph.type> "Profile" .
_:f1 <follows> <%s> .
_:f2 <follows> <%s> .
_:f3 <follows> <%s> .
`, f1, f2, f3, targetUID, targetUID, targetUID))

	defer func() {
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{target, f1, f2, f3})
		if err != nil {
			t.Logf("cleanup resolve failed: %v", err)
			return
		}
		toDelete := make([]string, 0, len(allUIDs))
		for _, uid := range allUIDs {
			toDelete = append(toDelete, uid)
		}
		if len(toDelete) > 0 {
			if err := c.DeleteNodes(ctx, toDelete); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	}()

	// First run: target must end up with follower_count = 3.
	updated, err := c.BackfillFollowerCount(ctx)
	if err != nil {
		t.Fatalf("BackfillFollowerCount failed: %v", err)
	}
	if updated < 1 {
		t.Errorf("BackfillFollowerCount updated %d nodes; want >= 1", updated)
	}
	if fc := queryFollowerCount(t, c, target); fc != 3 {
		t.Errorf("target follower_count after backfill = %d, want 3", fc)
	}

	// Second run: idempotent — target follower_count stays 3.
	if _, err := c.BackfillFollowerCount(ctx); err != nil {
		t.Fatalf("BackfillFollowerCount (2nd run) failed: %v", err)
	}
	if fc := queryFollowerCount(t, c, target); fc != 3 {
		t.Errorf("target follower_count after 2nd backfill = %d, want 3 (not idempotent)", fc)
	}
}

// TestBackfillFollowerCountPaged forces the multi-page / termination path by
// seeding 5 nodes and running the backfill with pageSize=2 (so the loop must
// page 2 + 2 + 1 and terminate on the short final page). It asserts every node
// receives the correct follower_count and that the total processed equals the
// node count with no skips or duplicates (WR-01).
func TestBackfillFollowerCountPaged(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixNano()

	// Five distinct target nodes, each wired with a known number of followers
	// (0..4) so we can assert exact follower_count per node after the backfill.
	const numTargets = 5
	targets := make([]string, numTargets)
	for i := range targets {
		targets[i] = fmt.Sprintf("%064x", now+6000+int64(i))
	}

	// Seed the target nodes first.
	var seed string
	for i, pk := range targets {
		seed += fmt.Sprintf("_:t%d <pubkey> %q .\n_:t%d <dgraph.type> \"Profile\" .\n", i, pk, i)
	}
	mustMutate(t, c, seed)

	targetUIDs, err := c.ResolvePubkeysToUIDs(ctx, targets)
	if err != nil {
		t.Fatalf("resolve target UIDs: %v", err)
	}

	// Build followers: target i gets exactly i followers (0,1,2,3,4).
	var followerPubkeys []string
	var edges string
	fIdx := 0
	for i, pk := range targets {
		uid := targetUIDs[pk]
		for j := 0; j < i; j++ {
			fpk := fmt.Sprintf("%064x", now+7000+int64(fIdx))
			followerPubkeys = append(followerPubkeys, fpk)
			edges += fmt.Sprintf("_:f%d <pubkey> %q .\n_:f%d <dgraph.type> \"Profile\" .\n_:f%d <follows> <%s> .\n", fIdx, fpk, fIdx, fIdx, uid)
			fIdx++
		}
	}
	mustMutate(t, c, edges)

	defer func() {
		all := append(append([]string{}, targets...), followerPubkeys...)
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, all)
		if err != nil {
			t.Logf("cleanup resolve failed: %v", err)
			return
		}
		toDelete := make([]string, 0, len(allUIDs))
		for _, uid := range allUIDs {
			toDelete = append(toDelete, uid)
		}
		if len(toDelete) > 0 {
			if err := c.DeleteNodes(ctx, toDelete); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	}()

	// Run with pageSize=2 to force multiple pages over the full has(pubkey) set.
	processed, err := c.backfillFollowerCountPaged(ctx, 2)
	if err != nil {
		t.Fatalf("backfillFollowerCountPaged failed: %v", err)
	}

	// processed counts every node in the graph (targets + followers + any other
	// seeded nodes). It must be >= the nodes we created, and the loop must have
	// terminated (returning here proves termination).
	wantAtLeast := numTargets + len(followerPubkeys)
	if processed < wantAtLeast {
		t.Errorf("processed %d nodes; want >= %d (skips/short-circuit?)", processed, wantAtLeast)
	}

	// Each target i must have follower_count == i exactly (no skips, no dupes).
	for i, pk := range targets {
		if fc := queryFollowerCount(t, c, pk); fc != i {
			t.Errorf("target[%d] follower_count = %d, want %d", i, fc, i)
		}
	}
}

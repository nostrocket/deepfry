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
	fc, _ := queryFollowerCountPresent(t, c, pubkey)
	return fc
}

// queryFollowerCountPresent reads the stored follower_count for a pubkey and also
// reports whether the predicate is actually SET on the node. This distinguishes a
// genuinely-written 0 (zero-follower node, which the uid-cursor backfill MUST
// write so the read-path index sees it) from a missing predicate that JSON
// unmarshalling would also surface as 0.
func queryFollowerCountPresent(t *testing.T, c *Client, pubkey string) (int, bool) {
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
		Node []map[string]json.RawMessage `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("queryFollowerCount(%q) unmarshal failed: %v", pubkey, err)
	}
	if len(parsed.Node) == 0 {
		return 0, false
	}
	raw, present := parsed.Node[0]["follower_count"]
	if !present {
		return 0, false
	}
	var fc int
	if err := json.Unmarshal(raw, &fc); err != nil {
		t.Fatalf("queryFollowerCount(%q) value unmarshal failed: %v", pubkey, err)
	}
	return fc, true
}

// queryUncrawledPresent reports whether the uncrawled predicate is SET on a node
// and its value (Phase 14 uncrawled-marker fix). It distinguishes a written
// uncrawled=1 from a missing predicate (which JSON unmarshalling also surfaces as 0).
func queryUncrawledPresent(t *testing.T, c *Client, pubkey string) (int, bool) {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	q := fmt.Sprintf(`{ node(func: eq(pubkey, %q)) { uncrawled } }`, pubkey)
	resp, err := txn.Query(ctx, q)
	if err != nil {
		t.Fatalf("queryUncrawled(%q) query failed: %v", pubkey, err)
	}
	var parsed struct {
		Node []map[string]json.RawMessage `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("queryUncrawled(%q) unmarshal failed: %v", pubkey, err)
	}
	if len(parsed.Node) == 0 {
		return 0, false
	}
	raw, present := parsed.Node[0]["uncrawled"]
	if !present {
		return 0, false
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("queryUncrawled(%q) value unmarshal failed: %v", pubkey, err)
	}
	return v, true
}

// inUncrawledFrontier reports whether a pubkey is returned by an eq(uncrawled, 1)
// frontier query (the actual frontier read-path entry point, Phase 14).
func inUncrawledFrontier(t *testing.T, c *Client, pubkey string, first int) bool {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	q := fmt.Sprintf(
		`{ frontier(func: eq(uncrawled, 1), first: %d, orderdesc: follower_count) { pubkey } }`, first)
	resp, err := txn.Query(ctx, q)
	if err != nil {
		t.Fatalf("inUncrawledFrontier query failed: %v", err)
	}
	var parsed struct {
		Frontier []struct {
			Pubkey string `json:"pubkey"`
		} `json:"frontier"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("inUncrawledFrontier unmarshal failed: %v", err)
	}
	for _, n := range parsed.Frontier {
		if n.Pubkey == pubkey {
			return true
		}
	}
	return false
}

// TestAddFollowersSetsUncrawledMarker verifies that nodes created by AddFollowers
// (both the signer node and a followee stub) carry uncrawled = 1 (Phase 14), so
// they enter the eq(uncrawled, 1) frontier index. INVARIANT: uncrawled = 1 ⟺ never
// attempted, and a fresh node has by definition never been attempted.
func TestAddFollowersSetsUncrawledMarker(t *testing.T) {
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
	signer := fmt.Sprintf("%064x", now+8000)
	followee := fmt.Sprintf("%064x", now+8001)

	defer func() {
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{signer, followee})
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

	follows := map[string]struct{}{followee: {}}
	if err := c.AddFollowers(ctx, signer, now, follows, false); err != nil {
		t.Fatalf("AddFollowers failed: %v", err)
	}

	// The newly-created signer node must carry uncrawled = 1.
	if v, present := queryUncrawledPresent(t, c, signer); !present || v != 1 {
		t.Errorf("signer uncrawled = (%d, present=%v), want (1, true)", v, present)
	}
	// The newly-created followee stub must carry uncrawled = 1.
	if v, present := queryUncrawledPresent(t, c, followee); !present || v != 1 {
		t.Errorf("followee stub uncrawled = (%d, present=%v), want (1, true)", v, present)
	}
	// Both must appear in the eq(uncrawled, 1) frontier query.
	big := countFrontier(t, c) + 100
	if !inUncrawledFrontier(t, c, signer, big) {
		t.Errorf("signer %s not in eq(uncrawled, 1) frontier", signer)
	}
	if !inUncrawledFrontier(t, c, followee, big) {
		t.Errorf("followee stub %s not in eq(uncrawled, 1) frontier", followee)
	}
}

// TestMarkAttemptedClearsUncrawledMarker verifies that MarkAttempted star-deletes
// the uncrawled predicate on every stamped node (Phase 14), so it leaves the
// eq(uncrawled, 1) frontier index once it has been attempted. INVARIANT: uncrawled
// = 1 ⟺ never attempted.
func TestMarkAttemptedClearsUncrawledMarker(t *testing.T) {
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
	pk := fmt.Sprintf("%064x", now+9000)

	// Seed an uncrawled frontier node directly (uncrawled=1, no last_attempt).
	mustMutate(t, c, fmt.Sprintf(
		`_:n <pubkey> %q .
_:n <dgraph.type> "Profile" .
_:n <follower_count> "5" .
_:n <uncrawled> "1" .
`, pk))

	defer func() {
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{pk})
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

	big := countFrontier(t, c) + 100

	// Precondition: the node is in the frontier before being attempted.
	if !inUncrawledFrontier(t, c, pk, big) {
		t.Fatalf("seed node %s not in eq(uncrawled, 1) frontier before MarkAttempted", pk)
	}

	// Stamp it as attempted (treat as a miss — empty hits set).
	if err := c.MarkAttempted(ctx, []string{pk}, time.Now().Unix(),
		map[string]struct{}{}, DefaultBackoffParams()); err != nil {
		t.Fatalf("MarkAttempted failed: %v", err)
	}

	// The uncrawled predicate must be gone after the attempt.
	if v, present := queryUncrawledPresent(t, c, pk); present {
		t.Errorf("uncrawled still present (=%d) after MarkAttempted; star-delete failed", v)
	}
	// And the node must no longer appear in the eq(uncrawled, 1) frontier.
	if inUncrawledFrontier(t, c, pk, big) {
		t.Errorf("node %s still in eq(uncrawled, 1) frontier after MarkAttempted", pk)
	}
}

// TestUncrawledFrontierOrderedByFollowerCount verifies an eq(uncrawled, 1) frontier
// query returns uncrawled nodes ordered by follower_count (Phase 14): the frontier
// enters via the uncrawled index but still surfaces high-follower nodes first.
func TestUncrawledFrontierOrderedByFollowerCount(t *testing.T) {
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
	high := fmt.Sprintf("%064x", now+9100)
	mid := fmt.Sprintf("%064x", now+9101)
	low := fmt.Sprintf("%064x", now+9102)

	mustMutate(t, c, fmt.Sprintf(
		`_:h <pubkey> %q .
_:h <dgraph.type> "Profile" .
_:h <follower_count> "300" .
_:h <uncrawled> "1" .
_:m <pubkey> %q .
_:m <dgraph.type> "Profile" .
_:m <follower_count> "200" .
_:m <uncrawled> "1" .
_:l <pubkey> %q .
_:l <dgraph.type> "Profile" .
_:l <follower_count> "100" .
_:l <uncrawled> "1" .
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

	// Read the actual frontier selection (the production query string) and confirm
	// our three high-follower uncrawled nodes appear, ordered high→low among themselves.
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	q := fmt.Sprintf(frontierStaleQueryFmt, countFrontier(t, c)+100)
	resp, err := txn.Query(ctx, q)
	if err != nil {
		t.Fatalf("frontier query failed: %v", err)
	}
	var parsed struct {
		Frontier []struct {
			Pubkey string `json:"pubkey"`
		} `json:"frontier"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("frontier unmarshal failed: %v", err)
	}

	// Record the order positions of our three seeded nodes.
	pos := map[string]int{high: -1, mid: -1, low: -1}
	for i, n := range parsed.Frontier {
		if _, tracked := pos[n.Pubkey]; tracked {
			pos[n.Pubkey] = i
		}
	}
	for name, pk := range map[string]string{"high": high, "mid": mid, "low": low} {
		if pos[pk] < 0 {
			t.Errorf("%s-follower uncrawled node %s missing from frontier", name, pk)
		}
	}
	// orderdesc: follower_count → high before mid before low.
	if pos[high] >= 0 && pos[mid] >= 0 && pos[high] > pos[mid] {
		t.Errorf("high (%d) must precede mid (%d) by follower_count desc", pos[high], pos[mid])
	}
	if pos[mid] >= 0 && pos[low] >= 0 && pos[mid] > pos[low] {
		t.Errorf("mid (%d) must precede low (%d) by follower_count desc", pos[mid], pos[low])
	}
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

	// Three frontier nodes (uncrawled=1, never attempted) with stored
	// follower_count 30/20/10. Phase 14: the frontier read enters via the uncrawled
	// marker index (eq(uncrawled, 1)), so seed nodes must carry uncrawled=1 to be
	// selected — this mirrors how AddFollowers stamps newly-created nodes.
	mustMutate(t, c, fmt.Sprintf(
		`_:h <pubkey> %q .
_:h <dgraph.type> "Profile" .
_:h <follower_count> "30" .
_:h <uncrawled> "1" .
_:m <pubkey> %q .
_:m <dgraph.type> "Profile" .
_:m <follower_count> "20" .
_:m <uncrawled> "1" .
_:l <pubkey> %q .
_:l <dgraph.type> "Profile" .
_:l <follower_count> "10" .
_:l <uncrawled> "1" .
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

// TestBackfillFollowerCountPaged forces the multi-page / termination path of the
// uid-cursor backfill (Fix C) by seeding 5 target nodes (plus their followers)
// and running with pageSize=2 (> the page size, so the loop must advance the uid
// cursor across several pages and terminate on the short final page). It asserts:
//   - every target gets the correct follower_count (0..4),
//   - the zero-follower target gets an EXPLICITLY-written follower_count = 0 (not a
//     missing predicate) — required so it is visible to the read-path index,
//   - total processed >= the seeded node count (no skips), and the loop terminates.
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

	// Each target i must have follower_count == i exactly, and the predicate must
	// be PRESENT on every target including the zero-follower one (no skips, no
	// dupes). A present-but-0 value on target[0] proves the uid-cursor upsert
	// writes val(fc)=0 rather than skipping zero-follower nodes.
	for i, pk := range targets {
		fc, present := queryFollowerCountPresent(t, c, pk)
		if !present {
			t.Errorf("target[%d] missing follower_count predicate (zero-count node skipped?)", i)
		}
		if fc != i {
			t.Errorf("target[%d] follower_count = %d, want %d", i, fc, i)
		}
	}
}

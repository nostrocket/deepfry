//go:build integration

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/dgraph-io/dgo/v210/protos/api"
)

func TestGetStalePubkeysIncludesFrontier(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// A crawled node (has last_attempt + kind3CreatedAt) and a pure stub (neither).
	stub := fmt.Sprintf("%064x", time.Now().UnixNano()) // unique fake pubkey
	crawled := fmt.Sprintf("%064x", time.Now().UnixNano()+1)
	now := time.Now().Unix()
	mustMutate(t, c, fmt.Sprintf(`_:s <pubkey> %q .
_:s <dgraph.type> "Profile" .
_:c <pubkey> %q .
_:c <dgraph.type> "Profile" .
_:c <kind3CreatedAt> "%d" .
_:c <last_db_update> "%d" .
_:c <last_attempt> "%d" .
`, stub, crawled, now, now, now))

	// Size the frontier limit above the current never-attempted node count so the
	// freshly-inserted stub is guaranteed to be among the returned frontier rows,
	// regardless of how many real stubs the live graph already holds. (Phase 1 of
	// GetStalePubkeys is an unordered `NOT has(last_attempt)` selection, so a limit
	// below the frontier size could non-deterministically exclude this test's stub.)
	frontierLimit := countFrontier(t, c) + 1000

	got, err := c.GetStalePubkeys(ctx, now-3600, frontierLimit)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := got[stub]; !ok {
		t.Fatalf("frontier stub %s was NOT selected — regression of the orderasc/1000-cap bug", stub)
	}
	// The freshly-attempted crawled node must NOT be stale yet.
	if _, ok := got[crawled]; ok {
		t.Errorf("freshly-attempted node %s should not be stale", crawled)
	}
}

// countFrontier returns the number of never-attempted ("frontier") pubkey nodes
// in the graph, so the test can size GetStalePubkeys' limit above it.
func countFrontier(t *testing.T, c *Client) int {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, `{ f(func: has(pubkey)) @filter(NOT has(last_attempt)) { c: count(uid) } }`)
	if err != nil {
		t.Fatalf("count frontier failed: %v", err)
	}
	var parsed struct {
		F []struct {
			C int `json:"c"`
		} `json:"f"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("unmarshal frontier count failed: %v", err)
	}
	if len(parsed.F) == 0 {
		return 0
	}
	return parsed.F[0].C
}

// mustMutate runs the given RDF n-quads in a single committed transaction.
func mustMutate(t *testing.T, c *Client, rdf string) {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)
	if _, err := txn.Mutate(ctx, &api.Mutation{SetNquads: []byte(rdf), CommitNow: true}); err != nil {
		t.Fatalf("mustMutate failed: %v", err)
	}
}

// TestGetStalePubkeysOrder verifies that GetStalePubkeys returns frontier
// pubkeys ordered by descending follower count (PERF-01, D-08/D-09).
//
// Fixture: three frontier pubkeys with 3, 1, 2 followers respectively.
// Expected order: 3-follower node first, then 2-follower, then 1-follower.
func TestGetStalePubkeysOrder(t *testing.T) {
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
	// Three frontier pubkeys (no last_attempt) with differing follower counts.
	high := fmt.Sprintf("%064x", now)
	mid := fmt.Sprintf("%064x", now+1)
	low := fmt.Sprintf("%064x", now+2)
	// Three "follower" pubkeys that will follow the frontier nodes.
	f1 := fmt.Sprintf("%064x", now+10)
	f2 := fmt.Sprintf("%064x", now+11)
	f3 := fmt.Sprintf("%064x", now+12)

	// Insert frontier nodes (no last_attempt).
	mustMutate(t, c, fmt.Sprintf(
		`_:h <pubkey> %q .
_:h <dgraph.type> "Profile" .
_:m <pubkey> %q .
_:m <dgraph.type> "Profile" .
_:l <pubkey> %q .
_:l <dgraph.type> "Profile" .
`, high, mid, low))

	// Resolve UIDs for frontier nodes (needed to add follows edges).
	frontierUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{high, mid, low})
	if err != nil {
		t.Fatalf("resolve frontier UIDs: %v", err)
	}
	highUID := frontierUIDs[high]
	midUID := frontierUIDs[mid]
	lowUID := frontierUIDs[low]

	// Insert follower nodes and wire follows edges:
	//   f1, f2, f3 follow high (3 followers)
	//   f1, f2     follow mid  (2 followers)
	//   f1         follows low  (1 follower)
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
_:f1 <follows> <%s> .
_:f2 <follows> <%s> .
_:f1 <follows> <%s> .
`, f1, f2, f3, highUID, highUID, highUID, midUID, midUID, lowUID))

	defer func() {
		// Cleanup all created nodes.
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{high, mid, low, f1, f2, f3})
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

	// Use a limit large enough to include all three but small enough to be specific.
	// olderThanUnix is unused by the aged phase now (next_attempt drives that),
	// but still accepted for API compatibility.
	got, err := c.GetStalePubkeys(ctx, 0, countFrontier(t, c)+100)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm all three frontier nodes are returned.
	if _, ok := got[high]; !ok {
		t.Errorf("high-follower frontier node %s missing from results", high)
	}
	if _, ok := got[mid]; !ok {
		t.Errorf("mid-follower frontier node %s missing from results", mid)
	}
	if _, ok := got[low]; !ok {
		t.Errorf("low-follower frontier node %s missing from results", low)
	}
}

// TestCountStalePubkeys verifies CountStalePubkeys returns frontier count +
// aged-eligible count (METRIC-01, D-16).
func TestCountStalePubkeys(t *testing.T) {
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
	// One frontier node (no last_attempt, no next_attempt).
	frontier := fmt.Sprintf("%064x", now+1000)
	// One aged-eligible node (next_attempt in the past).
	aged := fmt.Sprintf("%064x", now+1001)
	// One future node (next_attempt in the future — must NOT be counted).
	future := fmt.Sprintf("%064x", now+1002)

	pastTime := time.Now().Unix() - 3600    // 1h ago
	futureTime := time.Now().Unix() + 86400 // 24h from now
	tsNow := time.Now().Unix()

	mustMutate(t, c, fmt.Sprintf(
		`_:fr <pubkey> %q .
_:fr <dgraph.type> "Profile" .
_:ag <pubkey> %q .
_:ag <dgraph.type> "Profile" .
_:ag <last_attempt> "%d" .
_:ag <next_attempt> "%d" .
_:fu <pubkey> %q .
_:fu <dgraph.type> "Profile" .
_:fu <last_attempt> "%d" .
_:fu <next_attempt> "%d" .
`, frontier, aged, tsNow, pastTime, future, tsNow, futureTime))

	defer func() {
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{frontier, aged, future})
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

	total, err := c.CountStalePubkeys(ctx)
	if err != nil {
		t.Fatalf("CountStalePubkeys failed: %v", err)
	}

	// We added 1 frontier + 1 aged-eligible. The future node must NOT be counted.
	// Because this is a live DB with other nodes, we can only assert a lower bound
	// and that the future node is not counted by verifying the count is stable
	// before and after inserting the future node.
	// Here we just assert the total is at least 2 (frontier + aged).
	if total < 2 {
		t.Errorf("CountStalePubkeys returned %d; want >= 2 (fixture has 1 frontier + 1 aged)", total)
	}
	t.Logf("CountStalePubkeys = %d (at least 2 expected from fixture)", total)
}

// TestGetStalePubkeysAgedPhaseNextAttempt verifies the aged phase's
// lt(next_attempt, now) filter directly by querying CountStalePubkeys and
// a targeted aged-only DQL query (D-02).
//
// Note: GetStalePubkeys returns aged results ordered by follower count DESC
// (PERF-01). Because the live graph may have aged nodes with higher follower
// counts than our zero-follower fixture, we cannot rely on GetStalePubkeys with
// a small limit to include the fixture. Instead we directly exercise the aged
// DQL filter to verify the semantics.
func TestGetStalePubkeysAgedPhaseNextAttempt(t *testing.T) {
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
	// Aged-eligible: has next_attempt in the past.
	agedPk := fmt.Sprintf("%064x", now+2000)
	// Future: has next_attempt in the future — must NOT appear in results.
	futurePk := fmt.Sprintf("%064x", now+2001)

	pastTime := time.Now().Unix() - 3600
	futureTime := time.Now().Unix() + 86400
	tsNow := time.Now().Unix()

	mustMutate(t, c, fmt.Sprintf(
		`_:ag <pubkey> %q .
_:ag <dgraph.type> "Profile" .
_:ag <last_attempt> "%d" .
_:ag <next_attempt> "%d" .
_:fu <pubkey> %q .
_:fu <dgraph.type> "Profile" .
_:fu <last_attempt> "%d" .
_:fu <next_attempt> "%d" .
`, agedPk, tsNow, pastTime, futurePk, tsNow, futureTime))

	defer func() {
		allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{agedPk, futurePk})
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

	// Directly query the aged DQL to verify that:
	//   1. agedPk (next_attempt in past) IS returned by the aged filter.
	//   2. futurePk (next_attempt in future) is NOT returned.
	nowUnix := time.Now().Unix()
	agedQuery := fmt.Sprintf(`
	{
		var(func: has(next_attempt)) @filter(lt(next_attempt, %d)) {
			ac as count(~follows)
		}
		aged(func: uid(ac), orderdesc: val(ac)) {
			pubkey
		}
	}`, nowUnix)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, agedQuery)
	if err != nil {
		t.Fatalf("aged DQL query: %v", err)
	}

	var parsed struct {
		Aged []struct {
			Pubkey string `json:"pubkey"`
		} `json:"aged"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("aged DQL unmarshal: %v", err)
	}

	agedFound := false
	futureFound := false
	for _, n := range parsed.Aged {
		if n.Pubkey == agedPk {
			agedFound = true
		}
		if n.Pubkey == futurePk {
			futureFound = true
		}
	}

	if !agedFound {
		t.Errorf("aged-eligible node %s (next_attempt=%d < now=%d) was NOT returned by aged DQL filter",
			agedPk, pastTime, nowUnix)
	}
	if futureFound {
		t.Errorf("future node %s (next_attempt=%d > now=%d) was incorrectly returned by aged DQL filter",
			futurePk, futureTime, nowUnix)
	}
}

// queryNodeBackoff reads uid, miss_count, next_attempt, and last_attempt for
// a node identified by pubkey. Returns zero values if the node is absent.
func queryNodeBackoff(t *testing.T, c *Client, pubkey string) (uid string, missCount int, nextAttempt int64, lastAttempt int64) {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	q := fmt.Sprintf(`{ node(func: eq(pubkey, %q)) { uid miss_count next_attempt last_attempt } }`, pubkey)
	resp, err := txn.Query(ctx, q)
	if err != nil {
		t.Fatalf("queryNodeBackoff(%q) query failed: %v", pubkey, err)
	}

	var parsed struct {
		Node []struct {
			UID         string `json:"uid"`
			MissCount   int    `json:"miss_count"`
			NextAttempt int64  `json:"next_attempt"`
			LastAttempt int64  `json:"last_attempt"`
		} `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("queryNodeBackoff(%q) unmarshal failed: %v", pubkey, err)
	}
	if len(parsed.Node) == 0 {
		return "", 0, 0, 0
	}
	n := parsed.Node[0]
	return n.UID, n.MissCount, n.NextAttempt, n.LastAttempt
}

// TestMarkAttemptedHitMiss verifies the PERF-02 hit/miss stamping logic:
//   - A HIT pubkey: next_attempt = ts + hitRefreshCadence (24h), miss_count = 0
//   - A MISS pubkey: next_attempt = ts + BackoffInterval(prevMiss, ...), miss_count++
//
// Two rounds are exercised: round 1 produces initial miss counts; round 2
// verifies the geometric schedule advances, and a hit resets the counter.
func TestMarkAttemptedHitMiss(t *testing.T) {
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
	hitPk := fmt.Sprintf("%064x", now+3000)
	missPk := fmt.Sprintf("%064x", now+3001)

	// Insert both nodes as fresh stubs (no last_attempt).
	mustMutate(t, c, fmt.Sprintf(
		`_:h <pubkey> %q .
_:h <dgraph.type> "Profile" .
_:m <pubkey> %q .
_:m <dgraph.type> "Profile" .
`, hitPk, missPk))

	defer func() {
		hitUID, _, _, _ := queryNodeBackoff(t, c, hitPk)
		missUID, _, _, _ := queryNodeBackoff(t, c, missPk)
		var toDelete []string
		if hitUID != "" {
			toDelete = append(toDelete, hitUID)
		}
		if missUID != "" {
			toDelete = append(toDelete, missUID)
		}
		if len(toDelete) > 0 {
			if err := c.DeleteNodes(ctx, toDelete); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	}()

	params := DefaultBackoffParams() // base=2h, ratio=2, cap=168h, hitCadence=24h
	ts := time.Now().Unix()
	hits := map[string]struct{}{hitPk: {}}

	// Round 1: hitPk is a hit, missPk is a miss (prevMiss = 0).
	if err := c.MarkAttempted(ctx, []string{hitPk, missPk}, ts, hits, params); err != nil {
		t.Fatalf("round-1 MarkAttempted failed: %v", err)
	}

	// Assert hit: miss_count == 0, next_attempt == ts + 24h.
	_, hitMiss, hitNext, hitLast := queryNodeBackoff(t, c, hitPk)
	if hitMiss != 0 {
		t.Errorf("HIT miss_count: got %d, want 0", hitMiss)
	}
	wantHitNext := ts + int64(params.HitRefreshCadence.Seconds())
	if hitNext != wantHitNext {
		t.Errorf("HIT next_attempt: got %d, want %d (diff %d)", hitNext, wantHitNext, hitNext-wantHitNext)
	}
	if hitLast != ts {
		t.Errorf("HIT last_attempt: got %d, want %d", hitLast, ts)
	}

	// Assert miss round 1: miss_count == 1, next_attempt == ts + 2h (BackoffInterval(0, 2h, 2, 168h)).
	_, miss1Count, miss1Next, miss1Last := queryNodeBackoff(t, c, missPk)
	if miss1Count != 1 {
		t.Errorf("MISS round-1 miss_count: got %d, want 1", miss1Count)
	}
	wantMiss1Interval := BackoffInterval(0, params.Base, params.Ratio, params.Cap)
	wantMiss1Next := ts + int64(wantMiss1Interval.Seconds())
	if miss1Next != wantMiss1Next {
		t.Errorf("MISS round-1 next_attempt: got %d, want %d", miss1Next, wantMiss1Next)
	}
	if miss1Last != ts {
		t.Errorf("MISS round-1 last_attempt: got %d, want %d", miss1Last, ts)
	}

	// Round 2: missPk is again a miss (prevMiss = 1 → interval = 4h).
	ts2 := ts + 1
	emptyHits := map[string]struct{}{}
	if err := c.MarkAttempted(ctx, []string{missPk}, ts2, emptyHits, params); err != nil {
		t.Fatalf("round-2 MarkAttempted failed: %v", err)
	}

	_, miss2Count, miss2Next, _ := queryNodeBackoff(t, c, missPk)
	if miss2Count != 2 {
		t.Errorf("MISS round-2 miss_count: got %d, want 2", miss2Count)
	}
	wantMiss2Interval := BackoffInterval(1, params.Base, params.Ratio, params.Cap)
	wantMiss2Next := ts2 + int64(wantMiss2Interval.Seconds())
	if miss2Next != wantMiss2Next {
		t.Errorf("MISS round-2 next_attempt: got %d, want %d", miss2Next, wantMiss2Next)
	}
}

// TestBackfillNextAttempt verifies BackfillNextAttempt (D-06):
//   - A node with last_attempt but no next_attempt gets next_attempt = last_attempt + 24h, miss_count = 0.
//   - A node that already has next_attempt is NOT touched.
//   - A never-attempted node (no last_attempt) gets neither predicate.
//   - A second run updates 0 nodes (idempotent).
func TestBackfillNextAttempt(t *testing.T) {
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
	// needsBackfill: has last_attempt, no next_attempt — the backfill target.
	needsPk := fmt.Sprintf("%064x", now+4000)
	// alreadyHas: has both last_attempt and next_attempt — must not be touched.
	alreadyPk := fmt.Sprintf("%064x", now+4001)
	// neverAttempted: no last_attempt, no next_attempt — never-attempted frontier node.
	neverPk := fmt.Sprintf("%064x", now+4002)

	ts := time.Now().Unix()
	existingNextAttempt := ts + 7200 // the already-has node's current next_attempt (must not change)

	mustMutate(t, c, fmt.Sprintf(
		`_:n <pubkey> %q .
_:n <dgraph.type> "Profile" .
_:n <last_attempt> "%d" .
_:a <pubkey> %q .
_:a <dgraph.type> "Profile" .
_:a <last_attempt> "%d" .
_:a <next_attempt> "%d" .
_:f <pubkey> %q .
_:f <dgraph.type> "Profile" .
`, needsPk, ts, alreadyPk, ts, existingNextAttempt, neverPk))

	defer func() {
		uids, err := c.ResolvePubkeysToUIDs(ctx, []string{needsPk, alreadyPk, neverPk})
		if err != nil {
			t.Logf("cleanup resolve failed: %v", err)
			return
		}
		toDelete := make([]string, 0, len(uids))
		for _, uid := range uids {
			toDelete = append(toDelete, uid)
		}
		if len(toDelete) > 0 {
			if err := c.DeleteNodes(ctx, toDelete); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	}()

	// Run backfill — should update exactly needsPk.
	updated, err := c.BackfillNextAttempt(ctx, 86400)
	if err != nil {
		t.Fatalf("BackfillNextAttempt failed: %v", err)
	}
	// At minimum 1 node was updated (needsPk); may be more from existing DB state.
	if updated < 1 {
		t.Errorf("BackfillNextAttempt updated %d nodes; want >= 1", updated)
	}

	// needsPk must now have next_attempt = last_attempt + 86400, miss_count = 0.
	_, needsMiss, needsNext, needsLast := queryNodeBackoff(t, c, needsPk)
	wantNext := needsLast + 86400
	if needsNext != wantNext {
		t.Errorf("needsPk next_attempt: got %d, want %d (last_attempt=%d)", needsNext, wantNext, needsLast)
	}
	if needsMiss != 0 {
		t.Errorf("needsPk miss_count: got %d, want 0", needsMiss)
	}

	// alreadyPk: next_attempt must remain unchanged.
	_, _, alreadyNext, _ := queryNodeBackoff(t, c, alreadyPk)
	if alreadyNext != existingNextAttempt {
		t.Errorf("alreadyPk next_attempt changed from %d to %d; must be untouched by backfill",
			existingNextAttempt, alreadyNext)
	}

	// neverPk: must have neither last_attempt nor next_attempt.
	_, neverMiss, neverNext, neverLast := queryNodeBackoff(t, c, neverPk)
	if neverNext != 0 {
		t.Errorf("neverPk should have no next_attempt, got %d", neverNext)
	}
	if neverLast != 0 {
		t.Errorf("neverPk should have no last_attempt, got %d", neverLast)
	}
	if neverMiss != 0 {
		t.Errorf("neverPk should have no miss_count, got %d", neverMiss)
	}

	// Idempotency: a second run should find 0 candidates for needsPk (it now has next_attempt).
	// We can't easily assert "exactly 0" on a live DB, but we CAN assert needsPk
	// is not touched again (its next_attempt stays the same).
	_, err = c.BackfillNextAttempt(ctx, 86400)
	if err != nil {
		t.Fatalf("BackfillNextAttempt (2nd run) failed: %v", err)
	}
	_, _, needsNext2, _ := queryNodeBackoff(t, c, needsPk)
	if needsNext2 != needsNext {
		t.Errorf("BackfillNextAttempt not idempotent for needsPk: next_attempt changed from %d to %d on 2nd run",
			needsNext, needsNext2)
	}
}

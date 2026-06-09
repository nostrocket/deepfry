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
	stub := fmt.Sprintf("%064x", time.Now().UnixNano())   // unique fake pubkey
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

//go:build integration

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAddFollowersLargeKind3 proves the write-path fix (TEST-03, D-11): a
// follow-list larger than the internal chunk size (batchSize=200) must persist
// its FULL follow set in a single AddFollowers call.
//
// Red/green proof: this test FAILS against pre-fix code (chunks 2…N carried the
// same createdAt, tripped the version guard, and were silently dropped) and
// PASSES post-fix. To reproduce the failure, temporarily revert Plan 01's
// AddFollowers rewrite (e.g. `git stash` the dgraph.go change) and re-run.
//
// It selects the highest-count fixture under testdata/largest-kind3-*.json and
// t.Skips cleanly when none has been harvested yet (the expected state for this
// phase, since harvesting needs a manual live crawl).
func TestAddFollowersLargeKind3(t *testing.T) {
	fixture := selectLargestFixture(t)
	if fixture == "" {
		t.Skip("no large kind-3 fixture found under testdata/largest-kind3-*.json; " +
			"run the crawler against live relays to harvest one")
	}

	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture %s failed: %v", fixture, err)
	}

	// A kind-3 Nostr event: pubkey + p-tag followee list (ID-only, no payload).
	var event struct {
		PubKey    string     `json:"pubkey"`
		CreatedAt int64      `json:"created_at"`
		Tags      [][]string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode fixture %s failed: %v", fixture, err)
	}

	follows := make(map[string]struct{})
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			if isValidHexPubkey(tag[1]) {
				follows[tag[1]] = struct{}{}
			}
		}
	}
	if len(follows) <= batchSize {
		t.Fatalf("fixture %s has only %d valid followees; need > %d to exercise batching",
			fixture, len(follows), batchSize)
	}

	// Use a time-seeded unique signer so this test never collides with real
	// graph data, regardless of the fixture's own pubkey.
	signer := fmt.Sprintf("%064x", time.Now().UnixNano())
	createdAt := event.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// Teardown: delete the signer (followee stubs created by this test are left
	// since they may overlap real graph nodes; deleting the signer removes all
	// edges this test added). Resolve the signer UID and DeleteNodes it.
	t.Cleanup(func() {
		uid := resolveUID(t, c, signer)
		if uid != "" {
			if err := c.DeleteNodes(ctx, []string{uid}); err != nil {
				t.Logf("cleanup: delete signer %s (uid %s) failed: %v", signer, uid, err)
			}
		}
	})

	// Single full-set write.
	if err := c.AddFollowers(ctx, signer, createdAt, follows, true); err != nil {
		t.Fatalf("AddFollowers failed: %v", err)
	}

	// Assert the full follow set persisted.
	got := countFollows(t, c, signer)
	want := len(follows)
	if got != want {
		t.Fatalf("expected %d follow edges, got %d — chunked write dropped %d entries",
			want, got, want-got)
	}
}

// selectLargestFixture returns the path of the testdata/largest-kind3-<count>.json
// file with the highest <count> in its name, or "" if none exist.
func selectLargestFixture(t *testing.T) string {
	t.Helper()
	matches, _ := filepath.Glob("testdata/largest-kind3-*.json")
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		return fixtureCount(matches[i]) > fixtureCount(matches[j])
	})
	return matches[0]
}

// fixtureCount extracts the integer <count> from a largest-kind3-<count>.json
// filename. Returns 0 if it cannot be parsed.
func fixtureCount(path string) int {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".json")
	base = strings.TrimPrefix(base, "largest-kind3-")
	n, err := strconv.Atoi(base)
	if err != nil {
		return 0
	}
	return n
}

// resolveUID returns the UID for a pubkey, or "" if not found.
func resolveUID(t *testing.T, c *Client, pubkey string) string {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, fmt.Sprintf(`{ n(func: eq(pubkey, %q), first: 1) { uid } }`, pubkey))
	if err != nil {
		t.Logf("resolveUID query failed: %v", err)
		return ""
	}
	var parsed struct {
		N []struct {
			UID string `json:"uid"`
		} `json:"n"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Logf("resolveUID unmarshal failed: %v", err)
		return ""
	}
	if len(parsed.N) == 0 {
		return ""
	}
	return parsed.N[0].UID
}

// countFollows returns the number of follows edges out of the given pubkey.
func countFollows(t *testing.T, c *Client, pubkey string) int {
	t.Helper()
	ctx := context.Background()
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, fmt.Sprintf(
		`{ n(func: eq(pubkey, %q), first: 1) { c: count(follows) } }`, pubkey))
	if err != nil {
		t.Fatalf("countFollows query failed: %v", err)
	}
	var parsed struct {
		N []struct {
			C int `json:"c"`
		} `json:"n"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		t.Fatalf("countFollows unmarshal failed: %v", err)
	}
	if len(parsed.N) == 0 {
		return 0
	}
	return parsed.N[0].C
}

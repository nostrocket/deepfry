package dgraph

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestFollowerCountDelta pins the pure follow-set delta math that drives
// follower_count maintenance in AddFollowers (Phase 14, DSCALE-03). It runs
// under `make test` / `-short` with no Dgraph dependency — followerCountDelta is
// the unit-test seam, exactly like chunkSlice.
//
// Note: the backfill pagination logic is NOT separable from Dgraph I/O (the uid
// cursor advances off live query results), so it is covered by the integration
// test in dgraph_follower_count_integration_test.go rather than faked here.
func TestFollowerCountDelta(t *testing.T) {
	// existingMap builds a pubkey->uid map from a slice of pubkeys (uids are
	// arbitrary for the delta math).
	existingMap := func(pks []string) map[string]string {
		m := make(map[string]string, len(pks))
		for _, pk := range pks {
			m[pk] = "0x1" // arbitrary uid, unused by the set math
		}
		return m
	}
	updatedSet := func(pks []string) map[string]struct{} {
		m := make(map[string]struct{}, len(pks))
		for _, pk := range pks {
			m[pk] = struct{}{}
		}
		return m
	}

	cases := []struct {
		name        string
		existing    []string
		updated     []string
		wantAdded   []string
		wantRemoved []string
	}{
		{"all new", nil, []string{"a", "b"}, []string{"a", "b"}, nil},
		{"all removed", []string{"a", "b"}, nil, nil, []string{"a", "b"}},
		{"disjoint", []string{"a"}, []string{"b"}, []string{"b"}, []string{"a"}},
		{"unchanged", []string{"a", "b"}, []string{"a", "b"}, nil, nil},
		{"mixed", []string{"a", "b"}, []string{"b", "c"}, []string{"c"}, []string{"a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			added, removed := followerCountDelta(existingMap(tc.existing), updatedSet(tc.updated))

			// followerCountDelta returns sorted slices; compare directly.
			if !reflect.DeepEqual(added, tc.wantAdded) {
				t.Errorf("added = %v, want %v", added, tc.wantAdded)
			}
			if !reflect.DeepEqual(removed, tc.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, tc.wantRemoved)
			}
		})
	}
}

// TestGetStalePubkeysQueryEntersViaFollowerCountIndex pins the entry-point
// invariants of both GetStalePubkeys selection blocks WITHOUT a live Dgraph.
//
//   - FRONTIER (Phase 14 uncrawled-marker fix): enters via the uncrawled int index
//     (func: eq(uncrawled, 1)) ordered desc by follower_count, and must NOT carry the
//     absent-predicate @filter(NOT has(last_attempt)) (which full-scanned the 1.38M
//     follower_count index, ~25s). eq(uncrawled, 1) IS the never-attempted set.
//   - AGED (live-verification Fix A, unchanged): enters via the follower_count int
//     index (func: ge(follower_count, 0)) ordered desc, restricting by
//     lt(next_attempt, now) as a @filter, and must NOT root on has(next_attempt).
func TestGetStalePubkeysQueryEntersViaFollowerCountIndex(t *testing.T) {
	frontier := fmt.Sprintf(frontierStaleQueryFmt, 100)
	aged := fmt.Sprintf(agedStaleQueryFmt, 100, time.Now().Unix())

	// FRONTIER: enters via the uncrawled marker index, ordered by follower_count.
	if !strings.Contains(frontier, "func: eq(uncrawled, 1)") {
		t.Errorf("frontier query must enter via the uncrawled int index "+
			"(func: eq(uncrawled, 1)); got:\n%s", frontier)
	}
	if !strings.Contains(frontier, "orderdesc: follower_count") {
		t.Errorf("frontier query must order by orderdesc: follower_count; got:\n%s", frontier)
	}
	if strings.Contains(frontier, "NOT has(last_attempt)") {
		t.Errorf("frontier query must DROP the @filter(NOT has(last_attempt)) "+
			"absent-predicate scan; eq(uncrawled, 1) is the never-attempted set; got:\n%s", frontier)
	}
	if strings.Contains(frontier, "func: has(pubkey)") {
		t.Errorf("frontier query must NOT root on func: has(pubkey) (forces full sort); got:\n%s", frontier)
	}

	// AGED: enters via the follower_count int index, restricts by next_attempt filter.
	if !strings.Contains(aged, "func: ge(follower_count, 0)") {
		t.Errorf("aged query must enter via the follower_count int index "+
			"(func: ge(follower_count, 0)); got:\n%s", aged)
	}
	if !strings.Contains(aged, "orderdesc: follower_count") {
		t.Errorf("aged query must order by orderdesc: follower_count; got:\n%s", aged)
	}
	if !strings.Contains(aged, "@filter(lt(next_attempt,") {
		t.Errorf("aged query must keep lt(next_attempt, now) as a @filter; got:\n%s", aged)
	}
	if strings.Contains(aged, "func: has(next_attempt)") {
		t.Errorf("aged query must NOT root on func: has(next_attempt) (forces full sort); got:\n%s", aged)
	}
}

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

// TestGetStalePubkeysQueryEntersViaFollowerCountIndex pins the live-verification
// Fix A invariant WITHOUT a live Dgraph: both GetStalePubkeys selection blocks
// must enter through the follower_count int index (func: ge(follower_count, 0))
// ordered desc, and must NOT root on has(pubkey) / has(next_attempt) (which made
// Dgraph full-sort the whole set, ~150x slower on the production graph).
func TestGetStalePubkeysQueryEntersViaFollowerCountIndex(t *testing.T) {
	frontier := fmt.Sprintf(frontierStaleQueryFmt, 100)
	aged := fmt.Sprintf(agedStaleQueryFmt, 100, time.Now().Unix())

	for name, q := range map[string]string{"frontier": frontier, "aged": aged} {
		if !strings.Contains(q, "func: ge(follower_count, 0)") {
			t.Errorf("%s query must enter via the follower_count int index "+
				"(func: ge(follower_count, 0)); got:\n%s", name, q)
		}
		if !strings.Contains(q, "orderdesc: follower_count") {
			t.Errorf("%s query must order by orderdesc: follower_count; got:\n%s", name, q)
		}
		if strings.Contains(q, "func: has(pubkey)") {
			t.Errorf("%s query must NOT root on func: has(pubkey) (forces full sort); got:\n%s", name, q)
		}
		if strings.Contains(q, "func: has(next_attempt)") {
			t.Errorf("%s query must NOT root on func: has(next_attempt) (forces full sort); got:\n%s", name, q)
		}
	}

	// The aged block still restricts by next_attempt — as a @filter, not the root.
	if !strings.Contains(aged, "@filter(lt(next_attempt,") {
		t.Errorf("aged query must keep lt(next_attempt, now) as a @filter; got:\n%s", aged)
	}
	// The frontier block still restricts to never-attempted nodes — as a @filter.
	if !strings.Contains(frontier, "@filter(NOT has(last_attempt))") {
		t.Errorf("frontier query must keep NOT has(last_attempt) as a @filter; got:\n%s", frontier)
	}
}

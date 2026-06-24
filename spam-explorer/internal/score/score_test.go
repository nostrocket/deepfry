package score

import (
	"reflect"
	"testing"
)

func TestScore_Upstream(t *testing.T) {
	// seed(0) -> a(1) ; a(1) -> b(2) ; seed(0) -> b(2)
	// b's valid followers: seed (0<2) and a (1<2) => 2.
	// a's valid followers: seed (0<1) => 1.
	levels := map[string]int{"seed": 0, "a": 1, "b": 2}
	adjacency := map[string][]string{
		"seed": {"a", "b"},
		"a":    {"b"},
	}
	got := Score(levels, adjacency)
	want := map[string]int{"a": 1, "b": 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Score = %v, want %v", got, want)
	}
}

func TestScore_ExcludesSameLevelAndDeeper(t *testing.T) {
	// x(1) and y(1) follow each other (same level) and both follow z(1) (same level).
	// w(2) follows z(1) (deeper follower of a shallower node => not counted for z).
	// Only strictly-upstream followers count.
	levels := map[string]int{"seed": 0, "x": 1, "y": 1, "z": 1, "w": 2}
	adjacency := map[string][]string{
		"seed": {"x", "y", "z"}, // seed (0) upstream of x,y,z (1) => each +1
		"x":    {"y", "z"},      // same level => not counted
		"y":    {"x", "z"},      // same level => not counted
		"w":    {"z"},           // deeper (2) following shallower (1) => not counted
	}
	got := Score(levels, adjacency)
	want := map[string]int{"x": 1, "y": 1, "z": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Score = %v, want %v (same-level and deeper followers excluded)", got, want)
	}
	if _, ok := got["w"]; ok {
		t.Errorf("w should have no score (nobody upstream follows it)")
	}
}

func TestScore_SkipsTargetBeyondCap(t *testing.T) {
	// d is present as a follow target but absent from levels (dropped by --max-level).
	// It must never be scored (D-04). The follower 'a' is leveled.
	levels := map[string]int{"seed": 0, "a": 1}
	adjacency := map[string][]string{
		"seed": {"a"},
		"a":    {"d"}, // d not in levels => skipped
	}
	got := Score(levels, adjacency)
	want := map[string]int{"a": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Score = %v, want %v (target beyond cap skipped)", got, want)
	}
}

func TestScore_SkipsUnleveledFollower(t *testing.T) {
	// A follower that is itself not in levels must be skipped defensively.
	levels := map[string]int{"seed": 0, "a": 1}
	adjacency := map[string][]string{
		"seed":    {"a"},
		"ghost":   {"a"}, // ghost not leveled => skipped, must not credit a twice
		"unknown": {"seed"},
	}
	got := Score(levels, adjacency)
	want := map[string]int{"a": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Score = %v, want %v (unleveled follower skipped)", got, want)
	}
}

// TestScore_EveryNonSeedNodeHasParent is the structural invariant: in a BFS
// tree, every non-seed leveled node has at least one strictly-upstream follower
// (its discovery parent), so vfc >= 1 for every non-seed node.
func TestScore_EveryNonSeedNodeHasParent(t *testing.T) {
	levels := map[string]int{"seed": 0, "a": 1, "b": 1, "c": 2, "d": 2}
	adjacency := map[string][]string{
		"seed": {"a", "b"}, // parents of a, b
		"a":    {"c"},      // parent of c
		"b":    {"d"},      // parent of d
	}
	got := Score(levels, adjacency)
	for uid, lvl := range levels {
		if lvl == 0 {
			continue // seed
		}
		if got[uid] < 1 {
			t.Errorf("non-seed node %q (level %d) has vfc %d, want >= 1", uid, lvl, got[uid])
		}
	}
}

package bfs

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// fakeExpander serves hand-built frontiers from a static graph keyed by UID.
// It implements the FrontierExpander shape with no live Dgraph.
type fakeExpander struct {
	graph   map[string]FrontierResult
	calls   int
	queried [][]string
}

func (f *fakeExpander) Expand(_ context.Context, uids []string) ([]FrontierResult, error) {
	f.calls++
	queried := append([]string(nil), uids...)
	f.queried = append(f.queried, queried)
	out := make([]FrontierResult, 0, len(uids))
	for _, u := range uids {
		if r, ok := f.graph[u]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// node is a tiny helper to build a FrontierResult from a uid/pubkey and its
// followee uids (pubkey for followees is "pk-"+uid).
func node(uid string, followees ...string) FrontierResult {
	r := FrontierResult{UID: uid, Pubkey: "pk-" + uid}
	for _, fe := range followees {
		r.Follows = append(r.Follows, FollowEdge{UID: fe, Pubkey: "pk-" + fe})
	}
	return r
}

func TestLevel_ShortestHop(t *testing.T) {
	// seed -> a -> b ; seed -> b (b reachable at level 1 directly and level 2 via a)
	g := map[string]FrontierResult{
		"seed": node("seed", "a", "b"),
		"a":    node("a", "b"),
		"b":    node("b"),
	}
	fe := &fakeExpander{graph: g}

	levels, adjacency, pubkeys, err := Level(context.Background(), "seed", fe.Expand, 4)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}

	wantLevels := map[string]int{"seed": 0, "a": 1, "b": 1}
	if !reflect.DeepEqual(levels, wantLevels) {
		t.Errorf("levels = %v, want %v", levels, wantLevels)
	}
	// b must be level 1 (first-reached via seed), never re-leveled to 2.
	if levels["b"] != 1 {
		t.Errorf("b level = %d, want 1 (first-reached wins)", levels["b"])
	}
	// adjacency records every materialized follows edge.
	if got := adjacency["seed"]; !equalSet(got, []string{"a", "b"}) {
		t.Errorf("adjacency[seed] = %v, want {a,b}", got)
	}
	if got := adjacency["a"]; !equalSet(got, []string{"b"}) {
		t.Errorf("adjacency[a] = %v, want {b}", got)
	}
	if pubkeys["a"] != "pk-a" {
		t.Errorf("pubkeys[a] = %q, want pk-a", pubkeys["a"])
	}
}

func TestLevel_DiamondFirstReachedWins(t *testing.T) {
	// seed -> a, seed -> c ; a -> d ; c -> d ; d reachable at level 2 from both.
	g := map[string]FrontierResult{
		"seed": node("seed", "a", "c"),
		"a":    node("a", "d"),
		"c":    node("c", "d"),
		"d":    node("d"),
	}
	fe := &fakeExpander{graph: g}
	levels, _, _, err := Level(context.Background(), "seed", fe.Expand, 4)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	want := map[string]int{"seed": 0, "a": 1, "c": 1, "d": 2}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("levels = %v, want %v", levels, want)
	}
}

func TestLevel_CycleTerminates(t *testing.T) {
	// A -> B -> A (a 2-cycle reachable from seed). Must terminate.
	g := map[string]FrontierResult{
		"seed": node("seed", "A"),
		"A":    node("A", "B"),
		"B":    node("B", "A"),
	}
	fe := &fakeExpander{graph: g}
	levels, _, _, err := Level(context.Background(), "seed", fe.Expand, 10)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	want := map[string]int{"seed": 0, "A": 1, "B": 2}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("levels = %v, want %v", levels, want)
	}
	// A is leveled exactly once (1), never re-leveled by the B->A back edge.
	if levels["A"] != 1 {
		t.Errorf("A level = %d, want 1 (no re-level via cycle)", levels["A"])
	}
}

func TestLevel_MaxLevelCapDropsDeeper(t *testing.T) {
	// chain seed -> a -> b -> c -> d ; cap at 2 must drop c (level 3) and d.
	g := map[string]FrontierResult{
		"seed": node("seed", "a"),
		"a":    node("a", "b"),
		"b":    node("b", "c"),
		"c":    node("c", "d"),
		"d":    node("d"),
	}
	fe := &fakeExpander{graph: g}
	levels, adjacency, _, err := Level(context.Background(), "seed", fe.Expand, 2)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	want := map[string]int{"seed": 0, "a": 1, "b": 2}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("levels = %v, want %v (cap=2 drops level-3 c and beyond)", levels, want)
	}
	// b is at the cap (level 2) so it is never expanded; its edge b->c is not materialized.
	if _, expanded := adjacency["b"]; expanded {
		t.Errorf("adjacency[b] present = true, want false (b at cap is not expanded)")
	}
}

func TestLevel_NoCapWhenMaxLevelNonPositive(t *testing.T) {
	// maxLevel <= 0 means "no cap" — walk the whole reachable chain.
	g := map[string]FrontierResult{
		"seed": node("seed", "a"),
		"a":    node("a", "b"),
		"b":    node("b", "c"),
		"c":    node("c"),
	}
	fe := &fakeExpander{graph: g}
	levels, _, _, err := Level(context.Background(), "seed", fe.Expand, 0)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	want := map[string]int{"seed": 0, "a": 1, "b": 2, "c": 3}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("levels = %v, want %v (maxLevel<=0 means no cap)", levels, want)
	}
}

// equalSet compares two string slices as sets (order-independent).
func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	return reflect.DeepEqual(ac, bc)
}

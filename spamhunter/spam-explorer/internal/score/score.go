// Package score computes the valid-follower count by inverting the in-memory
// follows adjacency materialized during BFS (D-02). It is PURE: it issues no
// Dgraph query, performs no I/O, and never references the stored follower_count
// predicate (RESEARCH Pitfall 5 — follower_count has a floor of 1 and cannot
// discriminate; this tool's whole point is the new valid_follower_count metric).
//
// Correctness proof (D-02 + D-04 — preserve verbatim; do NOT "fix" this by
// adding ~follows reverse-edge queries):
//
// A valid follower F of target T is defined as a follower with
// level(F) < level(T). By the BFS leveling rule, every node at level L was
// discovered by expanding the follows edges of nodes at level L-1 or shallower.
// Therefore, for any edge F -> T where level(F) < level(T), the node F was
// expanded BEFORE T's level completed, and at that moment its follows set —
// which includes T — was read and recorded into the in-memory adjacency.
// Consequently the materialized follows adjacency contains EVERY edge that could
// ever satisfy the valid-follower predicate. Inverting it in memory (F -> T
// becomes a tally on T whenever level(F) < level(T)) yields the exact
// valid_follower_count. Edges from same-level or deeper followers are simply not
// counted (strict <), satisfying SCORE-02.
//
// The --max-level M cap (D-03) cannot break this: a scored node T has
// level(T) <= M, so all its valid followers have level(F) < level(T) <= M and
// were expanded before the cap took effect; only level-(M+1) discoveries are
// dropped, and those are never scored. No ~follows query is needed under any
// Phase-1 configuration.
package score

// Score inverts the materialized follows adjacency and counts, for each target
// T, the followers F with level(F) < level(T) (SCORE-01). Same-level and deeper
// followers are discarded by the strict < comparison (SCORE-02). A target absent
// from levels (dropped beyond the --max-level cap) is skipped and never scored
// (D-04). A follower absent from levels is skipped defensively.
//
//   - levels:    uid -> BFS level (0 = seed).
//   - adjacency: follower-uid -> []followee-uid (materialized follows edges).
//
// Returns uid -> valid_follower_count. No Dgraph access, no I/O.
func Score(levels map[string]int, adjacency map[string][]string) map[string]int {
	vfc := make(map[string]int, len(levels))
	for follower, followees := range adjacency {
		lf, ok := levels[follower]
		if !ok {
			continue // follower never leveled (shouldn't happen for materialized edges)
		}
		for _, target := range followees {
			lt, ok := levels[target]
			if !ok {
				continue // target beyond the cap; never scored (D-04)
			}
			if lf < lt { // strictly upstream — SCORE-01; same/deeper discarded — SCORE-02
				vfc[target]++
			}
		}
	}
	return vfc
}

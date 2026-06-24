// Package bfs performs pure, in-memory frontier BFS leveling over an injected
// expander. It assigns every reachable node a level equal to its shortest
// follow-hop distance from the seed (LEVEL-01) and records every materialized
// follows edge so the scoring pass can invert the adjacency in memory (D-02).
//
// This package is PURE: it never touches Dgraph. The frontier expander is
// injected as a function value (FrontierExpander), so tests feed a fake graph
// with no live server, and the only I/O-bearing dependency points
// internal/dgraph -> bfs at call time, never bfs -> internal/dgraph at compile
// time.
package bfs

import "context"

// FollowEdge is one outgoing follows edge: the followee's UID and pubkey.
// Both fields are carried because BFS keys on UID while output emits pubkey
// (the UID-vs-pubkey discipline, RESEARCH Pitfall 2).
type FollowEdge struct {
	UID    string `json:"uid"`
	Pubkey string `json:"pubkey"`
}

// FrontierResult is one node's expansion: its own UID/pubkey plus every
// follows edge. The wire layer (internal/dgraph, Plan 02) produces these from a
// `frontier(func: uid(...)) { uid pubkey follows { uid pubkey } }` query; tests
// hand-build them.
type FrontierResult struct {
	UID     string       `json:"uid"`
	Pubkey  string       `json:"pubkey"`
	Follows []FollowEdge `json:"follows"`
}

// FrontierExpander expands one BFS frontier: given the UIDs at the current
// level, it returns each one's FrontierResult (its follows edges). It is the
// injection seam that keeps bfs pure — internal/dgraph.Client.ExpandFrontier
// satisfies this shape at call time.
type FrontierExpander func(ctx context.Context, uids []string) ([]FrontierResult, error)

// Level drives the expander level-by-level from seedUID and returns:
//   - levels: uid -> shortest follow-hop distance from the seed (seed = 0).
//   - adjacency: follower-uid -> []followee-uid for every materialized edge.
//   - pubkeys: uid -> pubkey for every node observed (frontier roots + followees).
//
// Leveling rules (LEVEL-01, Pitfall 3):
//   - The seed is level 0 and forms the initial frontier.
//   - A node enters levels exactly once, at first discovery — the shallowest
//     level wins, which is the FIFO BFS invariant. A node already in levels is
//     never re-leveled and never re-enqueued, so cycles (A->B->A) terminate.
//   - Only followees absent from levels join the next frontier.
//
// Termination: the loop stops when the next frontier is empty OR the next level
// would exceed maxLevel. maxLevel <= 0 means "no cap" (walk the whole reachable
// component) — the D-03 Phase-1 bounding cap is opt-in via a positive value.
func Level(ctx context.Context, seedUID string, expand FrontierExpander, maxLevel int) (levels map[string]int, adjacency map[string][]string, pubkeys map[string]string, err error) {
	levels = map[string]int{seedUID: 0}
	adjacency = map[string][]string{}
	pubkeys = map[string]string{}

	frontier := []string{seedUID}
	currentLevel := 0

	for len(frontier) > 0 {
		// Respect the cap: do not expand past maxLevel (the nodes AT maxLevel
		// are leveled but their outgoing edges are not materialized, which is
		// exactly what D-04 relies on — only level-(M+1) discoveries are dropped).
		if maxLevel > 0 && currentLevel >= maxLevel {
			break
		}

		results, expandErr := expand(ctx, frontier)
		if expandErr != nil {
			return nil, nil, nil, expandErr
		}

		nextLevel := currentLevel + 1
		var next []string
		for _, r := range results {
			if r.Pubkey != "" {
				pubkeys[r.UID] = r.Pubkey
			}
			for _, edge := range r.Follows {
				adjacency[r.UID] = append(adjacency[r.UID], edge.UID)
				if edge.Pubkey != "" {
					pubkeys[edge.UID] = edge.Pubkey
				}
				if _, seen := levels[edge.UID]; !seen {
					levels[edge.UID] = nextLevel
					next = append(next, edge.UID)
				}
			}
		}

		frontier = next
		currentLevel = nextLevel
	}

	return levels, adjacency, pubkeys, nil
}

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"spam-explorer/internal/bfs"
)

// frontierQuery builds the one-round-trip frontier-expansion DQL for a set of
// UIDs (D-01). It roots on `func: uid(<csv>)` over the comma-joined frontier and
// selects, for every node, its own uid+pubkey plus a nested `follows { uid pubkey }`
// block — BOTH fields at every level (Pitfall 2: BFS keys on UID, output emits
// pubkey).
//
// PHASE 2 PAGINATION SEAM: this is the single place where pagination drops in.
// Phase 2 will split a large `uids` set into batches (first:/after: or chunked
// uid(...) blocks) INSIDE ExpandFrontier and merge the results — bfs.go never
// changes. This is the explicit reason D-01 chose frontier-expansion over
// @recurse. Extracted as a pure helper so the query shape is unit-testable
// offline.
func frontierQuery(uids []string) string {
	return fmt.Sprintf(`{
		frontier(func: uid(%s)) {
			uid
			pubkey
			follows { uid pubkey }
		}
	}`, strings.Join(uids, ", "))
}

// ExpandFrontier expands one BFS frontier in a single read-only round-trip: given
// the UIDs at the current level it returns each one's follows edges as
// bfs.FrontierResult (INGEST-03, D-01). The return type is bfs.FrontierResult so
// this method satisfies bfs.FrontierExpander directly — main injects
// client.ExpandFrontier into bfs.Level with no adapter, keeping the dependency
// pointing dgraph -> bfs (never bfs -> dgraph).
func (c *Client) ExpandFrontier(ctx context.Context, uids []string) ([]bfs.FrontierResult, error) {
	query := frontierQuery(uids)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("expand frontier failed: %w", err)
	}

	var result struct {
		Frontier []bfs.FrontierResult `json:"frontier"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal frontier failed: %w", err)
	}
	return result.Frontier, nil
}

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
)

// resolveSeedQuery builds the seed-resolution DQL. The seed pubkey is
// interpolated with %q (== strconv.Quote): it is wrapped in double quotes and
// any embedded quotes/backslashes are escaped, so an injection-shaped --seed
// cannot break out of the eq() argument (T-01-02 mitigation). NEVER use raw %s
// here. Full hex-format validation is Phase 3 (CLI-02); Phase 1 just quotes.
//
// Extracted as a pure helper so the query shape is unit-testable without a live
// Dgraph (mirrors web-of-trust's package-constant query-assertion pattern).
func resolveSeedQuery(seed string) string {
	return fmt.Sprintf(`{ node(func: eq(pubkey, %q), first: 1) { uid pubkey } }`, seed)
}

// ResolveSeed looks up the internal UID for a seed pubkey via an eq(pubkey, ...)
// read-only query (CLI-01 input path). It returns a clear error if the seed is
// absent from the graph (missing-seed guard, Pitfall 4) so main can exit with a
// signal instead of silently producing an empty file.
//
// Dgraph traverses on internal UIDs, not pubkeys — this UID anchors the BFS.
func (c *Client) ResolveSeed(ctx context.Context, seed string) (string, error) {
	query := resolveSeedQuery(seed)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return "", fmt.Errorf("resolve seed failed: %w", err)
	}

	var result struct {
		Node []struct {
			UID    string `json:"uid"`
			Pubkey string `json:"pubkey"`
		} `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return "", fmt.Errorf("unmarshal seed failed: %w", err)
	}
	if len(result.Node) == 0 {
		return "", fmt.Errorf("seed pubkey %q not found in graph", seed)
	}
	return result.Node[0].UID, nil
}

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Helpers for the clusterscan CLI: read-only queries that locate spam clusters
// by graph shape. Trust flows out from seed pubkeys along `follows` edges; a
// node joins the trusted set once at least K already-trusted accounts follow it.
// Accounts that never join, yet still touch the trusted set through one or two
// edges ("weak bridges"), are the entry points to candidate spam clusters.
//
// All graph computation stays inside DQL (counts, filters, uid() sets, math());
// the caller only does set bookkeeping. Every query uses a read-only txn.

// WeakBridge is a non-trusted account that touches the trusted set through a
// small number of edges. It is the suspected entry point to a spam cluster.
type WeakBridge struct {
	UID              string `json:"uid"`
	Pubkey           string `json:"pubkey"`
	Kind3CreatedAt   int64  `json:"kind3CreatedAt"`
	TrustedFollowers int    `json:"trusted_followers"` // trusted accounts that follow this node
	TrustedFollowees int    `json:"trusted_followees"` // trusted accounts this node follows
	Weight           int    `json:"weight"`            // total edges crossing into the trusted set
}

// ClusterNode is a single member of the sub-graph hanging off a bridge.
type ClusterNode struct {
	UID    string `json:"uid"`
	Pubkey string `json:"pubkey"`
}

// uidList renders a slice of Dgraph UIDs (e.g. "0x1a") as a comma-separated
// argument for uid(...).
func uidList(uids []string) string {
	return strings.Join(uids, ",")
}

// ResolvePubkeysToUIDs looks up the Dgraph UID for each given pubkey. Pubkeys
// not present in the graph are simply omitted from the result map.
func (c *Client) ResolvePubkeysToUIDs(
	ctx context.Context,
	pubkeys []string,
) (map[string]string, error) {
	if len(pubkeys) == 0 {
		return map[string]string{}, nil
	}

	quoted := make([]string, len(pubkeys))
	for i, pk := range pubkeys {
		quoted[i] = strconv.Quote(pk)
	}
	query := fmt.Sprintf(`
	{
		nodes(func: eq(pubkey, [%s])) {
			uid
			pubkey
		}
	}`, strings.Join(quoted, ", "))

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("resolve pubkeys failed: %w", err)
	}

	var result struct {
		Nodes []struct {
			UID    string `json:"uid"`
			Pubkey string `json:"pubkey"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal resolved pubkeys failed: %w", err)
	}

	out := make(map[string]string, len(result.Nodes))
	for _, n := range result.Nodes {
		out[n.Pubkey] = n.UID
	}
	return out, nil
}

// ExpandTrustedSet runs one round of trust propagation: it returns the UIDs of
// every node, not already trusted, that is followed by at least k members of
// the current trusted set. Callers loop until this returns no new UIDs.
func (c *Client) ExpandTrustedSet(
	ctx context.Context,
	trustedUIDs []string,
	k int,
) ([]string, error) {
	if len(trustedUIDs) == 0 {
		return nil, nil
	}

	query := fmt.Sprintf(`
	{
		trusted as var(func: uid(%s))
		cand as var(func: has(pubkey)) @filter(NOT uid(trusted)) {
			e as count(~follows @filter(uid(trusted)))
		}
		qualified(func: uid(cand)) @filter(ge(val(e), %d)) {
			uid
		}
	}`, uidList(trustedUIDs), k)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("expand trusted set failed: %w", err)
	}

	var result struct {
		Qualified []struct {
			UID string `json:"uid"`
		} `json:"qualified"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal expanded set failed: %w", err)
	}

	uids := make([]string, len(result.Qualified))
	for i, n := range result.Qualified {
		uids[i] = n.UID
	}
	return uids, nil
}

// GetWeakBridges returns up to `limit` non-trusted accounts whose number of
// edges crossing into the trusted set is between 1 and maxWeight (inclusive).
// The boolean return is true when the limit was hit (results were truncated),
// so the caller can warn rather than silently under-report.
func (c *Client) GetWeakBridges(
	ctx context.Context,
	trustedUIDs []string,
	maxWeight int,
	limit int,
) ([]WeakBridge, bool, error) {
	if len(trustedUIDs) == 0 {
		return nil, false, nil
	}

	query := fmt.Sprintf(`
	{
		trusted as var(func: uid(%s))
		cand as var(func: has(pubkey)) @filter(NOT uid(trusted)) {
			tf as count(~follows @filter(uid(trusted)))
			tg as count(follows @filter(uid(trusted)))
			w as math(tf + tg)
		}
		bridges(func: uid(cand), first: %d, orderasc: pubkey)
		@filter(ge(val(w), 1) AND le(val(w), %d)) {
			uid
			pubkey
			kind3CreatedAt
			trusted_followers: val(tf)
			trusted_followees: val(tg)
			weight: val(w)
		}
	}`, uidList(trustedUIDs), limit, maxWeight)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, false, fmt.Errorf("query weak bridges failed: %w", err)
	}

	var result struct {
		Bridges []WeakBridge `json:"bridges"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, false, fmt.Errorf("unmarshal weak bridges failed: %w", err)
	}

	return result.Bridges, len(result.Bridges) >= limit, nil
}

// Degree holds the out-degree (follows) and in-degree (~follows) of a node.
type Degree struct {
	Follows   int `json:"follows_count"`
	Followers int `json:"followers_count"`
}

// DegreesForUIDs returns the follows / followers counts for each given UID.
// Callers should batch large UID sets to stay under gRPC message limits.
func (c *Client) DegreesForUIDs(ctx context.Context, uids []string) ([]Degree, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	query := fmt.Sprintf(`
	{
		nodes(func: uid(%s)) {
			follows_count: count(follows)
			followers_count: count(~follows)
		}
	}`, uidList(uids))

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query degrees failed: %w", err)
	}

	var result struct {
		Nodes []Degree `json:"nodes"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal degrees failed: %w", err)
	}
	return result.Nodes, nil
}

// recurseNode mirrors the nested shape returned by an @recurse query over the
// follows predicate.
type recurseNode struct {
	UID     string        `json:"uid"`
	Pubkey  string        `json:"pubkey"`
	Follows []recurseNode `json:"follows"`
}

// ClusterBeneath walks the follows edges below a bridge node up to `depth` hops
// and returns the distinct set of nodes reachable from it (excluding the bridge
// itself). The caller intersects this set with the non-trusted population to
// size the suspected spam cluster.
func (c *Client) ClusterBeneath(
	ctx context.Context,
	bridgeUID string,
	depth int,
) ([]ClusterNode, error) {
	query := fmt.Sprintf(`
	{
		sub(func: uid(%s)) @recurse(depth: %d, loop: false) {
			uid
			pubkey
			follows
		}
	}`, bridgeUID, depth)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query cluster beneath %s failed: %w", bridgeUID, err)
	}

	var result struct {
		Sub []recurseNode `json:"sub"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal cluster beneath failed: %w", err)
	}
	if len(result.Sub) == 0 {
		return nil, nil
	}

	// Flatten the recurse tree, skipping the root and de-duplicating by UID.
	seen := map[string]struct{}{bridgeUID: {}}
	var members []ClusterNode
	var walk func(nodes []recurseNode)
	walk = func(nodes []recurseNode) {
		for _, n := range nodes {
			if _, dup := seen[n.UID]; !dup {
				seen[n.UID] = struct{}{}
				members = append(members, ClusterNode{UID: n.UID, Pubkey: n.Pubkey})
			}
			walk(n.Follows)
		}
	}
	walk(result.Sub[0].Follows)
	return members, nil
}

package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Abstraction layer over Dgraph to store a Nostr Web-of-Trust (kind 3) graph.
// Guarantees uniqueness of pubkeys using @upsert schema and upsert blocks.
//
// Schema used:
//   pubkey: string @index(exact) @upsert .
//   kind3CreatedAt: int .
//   last_db_update: int .
//   follows: uid @reverse .

// Client wraps a dgo.Dgraph instance.
type Client struct {
	dg   *dgo.Dgraph
	conn *grpc.ClientConn
}

// NewClient creates a new Client connected to the given dgraph gRPC address (eg "localhost:9080").
func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		dg:   dgo.NewDgraphClient(api.NewDgraphClient(conn)),
		conn: conn,
	}, nil
}

// Close closes the gRPC connection, call this with defer.
func (c *Client) Close() error {
	return c.conn.Close()
}

// EnsureSchema sets the schema needed for this module.
func (c *Client) EnsureSchema(ctx context.Context) error {
	schema := `pubkey: string @index(exact) @upsert @unique .
kind3CreatedAt: int @index(int) .
last_db_update: int @index(int) .
follows: [uid] @reverse .`
	return c.dg.Alter(ctx, &api.Operation{Schema: schema})
}

// AddFollowers adds multiple follows edges from a single follower to multiple followees.
// For kind 3 events, this completely replaces the user's follow list (replaceable event behavior).
func (c *Client) AddFollowers(ctx context.Context, signerPubkey string, kind3createdAt int64, follows map[string]struct{}, debug bool) error {
	// Create a longer timeout context for this specific operation
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if debug {
		log.Printf("DEBUG: Starting AddFollowers for pubkey %s with %d follows", signerPubkey, len(follows))
	}
	start := time.Now()

	lastUpdate := time.Now().Unix()

	txn := c.dg.NewTxn()
	defer txn.Discard(queryCtx)

	// Step 1: Get follower and existing follows - include kind3CreatedAt to avoid separate query
	followerQuery := fmt.Sprintf(`
	{
		follower(func: eq(pubkey, %q), first: 1) {
			uid
			kind3CreatedAt
			follows { 
				uid
				pubkey 
			}
		}
	}`, signerPubkey)

	resp, err := txn.Query(queryCtx, followerQuery)
	if err != nil {
		return fmt.Errorf("query follower failed: %w", err)
	}
	if debug {
		log.Printf("DEBUG: Initial follower query completed in %v", time.Since(start))
	}

	var result struct {
		Follower []struct {
			UID            string `json:"uid"`
			Kind3CreatedAt int64  `json:"kind3CreatedAt"`
			Follows        []struct {
				UID    string `json:"uid"`
				Pubkey string `json:"pubkey"`
			} `json:"follows"`
		} `json:"follower"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return fmt.Errorf("unmarshal follower failed: %w", err)
	}

	// Create/update follower
	var followerUID string
	existingFollows := make(map[string]string) // pubkey -> uid

	if len(result.Follower) == 0 {
		// Create new follower
		followerNQuads := fmt.Sprintf(`
			_:follower <pubkey> %q .
			_:follower <kind3CreatedAt> "%d" .
			_:follower <last_db_update> "%d" .
		`, signerPubkey, kind3createdAt, lastUpdate)

		mu := &api.Mutation{
			SetNquads: []byte(followerNQuads),
			CommitNow: false,
		}
		assigned, err := txn.Mutate(queryCtx, mu)
		if err != nil {
			return fmt.Errorf("create follower failed: %w", err)
		}
		followerUID = assigned.Uids["follower"]
	} else {
		// Update existing follower - check if this is newer than existing
		existingKind3CreatedAt := result.Follower[0].Kind3CreatedAt
		if kind3createdAt <= existingKind3CreatedAt {
			// Skip update - existing event is newer or same age
			return nil
		}

		followerUID = result.Follower[0].UID

		// Track existing follows for deletion
		for _, f := range result.Follower[0].Follows {
			existingFollows[f.Pubkey] = f.UID
		}
	}

	// Always update timestamps regardless of whether there are new follows
	updateNQuads := fmt.Sprintf(`
		<%s> <kind3CreatedAt> "%d" .
		<%s> <last_db_update> "%d" .
	`, followerUID, kind3createdAt, followerUID, lastUpdate)

	mu := &api.Mutation{
		SetNquads: []byte(updateNQuads),
		CommitNow: false,
	}
	if _, err := txn.Mutate(queryCtx, mu); err != nil {
		return fmt.Errorf("update follower timestamps failed: %w", err)
	}

	// Step 2: Remove all existing follows (kind 3 is replaceable)
	if len(existingFollows) > 0 {
		var delNQuads string
		for _, uid := range existingFollows {
			delNQuads += fmt.Sprintf("<%s> <follows> <%s> .\n", followerUID, uid)
		}
		mu := &api.Mutation{
			DelNquads: []byte(delNQuads),
			CommitNow: false,
		}
		if _, err := txn.Mutate(queryCtx, mu); err != nil {
			return fmt.Errorf("remove existing follows failed: %w", err)
		}
	}

	// Step 3: Bulk query all followees at once
	if len(follows) > 0 {
		followeeList := make([]string, 0, len(follows))
		for followee := range follows {
			followeeList = append(followeeList, followee)
		}

		// Build single query for all followees
		var queryParts []string
		for i, followee := range followeeList {
			queryParts = append(queryParts, fmt.Sprintf(`followee_%d(func: eq(pubkey, %q)) { uid }`, i, followee))
		}

		bulkQuery := fmt.Sprintf("{ %s }", fmt.Sprintf(strings.Join(queryParts, "\n")))

		bulkResp, err := txn.Query(queryCtx, bulkQuery)
		if err != nil {
			return fmt.Errorf("bulk query followees failed: %w", err)
		}

		// Parse bulk results
		var bulkResult map[string][]struct {
			UID string `json:"uid"`
		}
		if err := json.Unmarshal(bulkResp.Json, &bulkResult); err != nil {
			return fmt.Errorf("unmarshal bulk followees failed: %w", err)
		}

		// Step 4: Create missing followees in bulk
		var createNQuads string
		followeeUIDs := make([]string, len(followeeList))

		for i, followee := range followeeList {
			key := fmt.Sprintf("followee_%d", i)
			if nodes, exists := bulkResult[key]; exists && len(nodes) > 0 {
				// Followee exists
				followeeUIDs[i] = nodes[0].UID
			} else {
				// Need to create followee
				blankNodeID := fmt.Sprintf("new_followee_%d", i)
				createNQuads += fmt.Sprintf("_:%s <pubkey> %q .\n", blankNodeID, followee)
				followeeUIDs[i] = "_:" + blankNodeID
			}
		}

		// Create missing followees if any
		if createNQuads != "" {
			mu := &api.Mutation{
				SetNquads: []byte(createNQuads),
				CommitNow: false,
			}
			assigned, err := txn.Mutate(queryCtx, mu)
			if err != nil {
				return fmt.Errorf("create missing followees failed: %w", err)
			}

			// Replace blank node references with actual UIDs
			for i, uid := range followeeUIDs {
				if strings.HasPrefix(uid, "_:") {
					blankNodeID := uid[2:] // Remove "_:" prefix
					if actualUID, exists := assigned.Uids[blankNodeID]; exists {
						followeeUIDs[i] = actualUID
					}
				}
			}
		}

		// Step 5: Create all follow edges in bulk
		var edgeNQuads string
		for _, followeeUID := range followeeUIDs {
			if followeeUID != "" && !strings.HasPrefix(followeeUID, "_:") {
				edgeNQuads += fmt.Sprintf("<%s> <follows> <%s> .\n", followerUID, followeeUID)
			}
		}

		if edgeNQuads != "" {
			mu := &api.Mutation{
				SetNquads: []byte(edgeNQuads),
				CommitNow: false,
			}
			if _, err := txn.Mutate(queryCtx, mu); err != nil {
				return fmt.Errorf("create follow edges failed: %w", err)
			}
		}
	}

	// Commit all changes
	if err := txn.Commit(queryCtx); err != nil {
		return fmt.Errorf("commit transaction failed: %w", err)
	}

	if debug {
		log.Printf("DEBUG: AddFollowers completed successfully in %v for pubkey %s", time.Since(start), signerPubkey)
	}
	return nil
}

// RemoveFollower removes the follows edge from follower -> followee.
// The follower's timestamps are updated to reflect the removal.
// Returns error if parameters are empty or timestamps are invalid.
func (c *Client) RemoveFollower(ctx context.Context, signerPubkey string, kind3createdAt int64, followee string) error {
	if signerPubkey == "" {
		return fmt.Errorf("signerPubkey must be specified (non-empty)")
	}
	if followee == "" {
		return fmt.Errorf("followee must be specified (non-empty)")
	}
	if kind3createdAt == 0 {
		return fmt.Errorf("kind3createdAt must be specified (non-zero)")
	}

	lastUpdate := time.Now().Unix()

	// Query to find the nodes
	q := `query {
		f as var(func: eq(pubkey, "` + signerPubkey + `"))
		e as var(func: eq(pubkey, "` + followee + `"))
	}`

	// Update the follower's timestamp
	setNquads := `
		uid(f) <pubkey> "` + signerPubkey + `" .
		uid(f) <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
		uid(f) <last_db_update> "` + fmt.Sprintf("%d", lastUpdate) + `" .`

	// Delete the edge
	delNquads := `uid(f) <follows> uid(e) .`

	setMu := &api.Mutation{SetNquads: []byte(setNquads)}
	delMu := &api.Mutation{DelNquads: []byte(delNquads)}

	req := &api.Request{
		Query:     q,
		Mutations: []*api.Mutation{setMu, delMu},
		CommitNow: true,
	}

	_, err := c.dg.NewTxn().Do(ctx, req)
	return err
}

// RemovePubKeyIfNoFollowers checks if the pubkey has any followers (~follows).
// If there are zero followers, it deletes the node. Returns (deleted bool, error).
func (c *Client) RemovePubKeyIfNoFollowers(ctx context.Context, pubkey string) (bool, error) {
	q := `query Check($pubkey: string) {
  node(func: eq(pubkey, $pubkey)) {
	uid
	followers: ~follows { uid }
  }
}`

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	req := &api.Request{
		Query: q,
		Vars:  map[string]string{"$pubkey": pubkey},
	}
	resp, err := txn.Do(ctx, req)
	if err != nil {
		return false, err
	}

	type queryResp struct {
		Node []struct {
			UID       string   `json:"uid"`
			Followers []string `json:"followers"`
		} `json:"node"`
	}

	var qr queryResp
	if err := json.Unmarshal(resp.Json, &qr); err != nil {
		return false, err
	}
	if len(qr.Node) == 0 {
		return false, nil // nothing to delete
	}

	n := qr.Node[0]
	if len(n.Followers) > 0 {
		return false, nil // still has followers
	}

	del := fmt.Sprintf(`<%s> * * .`, n.UID)
	mu := &api.Mutation{DelNquads: []byte(del)}
	_, err = txn.Mutate(ctx, mu)
	if err != nil {
		return false, err
	}

	err = txn.Commit(ctx)
	if err != nil {
		return false, err
	}

	return true, nil
}

// GetStalePubkeys returns pubkeys with last_db_update older than the given threshold,
// or pubkeys that don't have last_db_update set at all.
// If olderThanUnix is not provided, defaults to 24 hours ago.
// Results are sorted by age, with least recently updated first.
func (c *Client) GetStalePubkeys(ctx context.Context, olderThanUnix int64) ([]string, error) {
	query := fmt.Sprintf(`
	{
		stale(func: has(pubkey), orderasc: last_db_update) @filter(NOT has(last_db_update) OR lt(last_db_update, %d)) {
			pubkey
		}
	}`, olderThanUnix)

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query stale pubkeys failed: %w", err)
	}

	var result struct {
		Stale []struct {
			Pubkey string `json:"pubkey"`
		} `json:"stale"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal stale pubkeys failed: %w", err)
	}

	pubkeys := make([]string, len(result.Stale))
	for i, node := range result.Stale {
		pubkeys[i] = node.Pubkey
	}

	return pubkeys, nil
}

// CountPubkeys returns the total number of pubkeys in the graph.
func (c *Client) CountPubkeys(ctx context.Context) (int, error) {
	query := `
	{
		count(func: has(pubkey)) {
			count(uid)
		}
	}`

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query pubkey count failed: %w", err)
	}

	var result struct {
		Count []struct {
			Count int `json:"count"`
		} `json:"count"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return 0, fmt.Errorf("unmarshal pubkey count failed: %w", err)
	}

	if len(result.Count) == 0 {
		return 0, nil
	}

	return result.Count[0].Count, nil
}

// GetKind3CreatedAt returns the kind3CreatedAt unix timestamp for the given pubkey.
// Returns 0 if the pubkey doesn't exist or has no kind3CreatedAt value.
func (c *Client) GetKind3CreatedAt(ctx context.Context, pubkey string) (int64, error) {
	// Create a shorter timeout context for this specific operation
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `query GetKind3($pubkey: string) {
		pubkey_node(func: eq(pubkey, $pubkey), first: 1) {
			kind3CreatedAt
		}
	}`

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(queryCtx)

	req := &api.Request{
		Query: query,
		Vars:  map[string]string{"$pubkey": pubkey},
	}

	resp, err := txn.Do(queryCtx, req)
	if err != nil {
		return 0, fmt.Errorf("query kind3CreatedAt failed: %w", err)
	}

	var result struct {
		PubkeyNode []struct {
			Kind3CreatedAt int64 `json:"kind3CreatedAt"`
		} `json:"pubkey_node"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return 0, fmt.Errorf("unmarshal kind3CreatedAt failed: %w", err)
	}

	if len(result.PubkeyNode) == 0 {
		return 0, nil // pubkey doesn't exist
	}

	return result.PubkeyNode[0].Kind3CreatedAt, nil
}

// GetPubkeysWithMinFollowers returns a map of pubkeys that have at least the specified number of followers.
// The map uses pubkey as key with empty struct as value for memory-efficient set operations.
func (c *Client) GetPubkeysWithMinFollowers(ctx context.Context, minFollowers int) (map[string]struct{}, error) {
	query := fmt.Sprintf(`
	{
		popular(func: has(pubkey)) @filter(ge(count(~follows), %d)) {
			pubkey
		}
	}`, minFollowers)

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query popular pubkeys failed: %w", err)
	}

	var result struct {
		Popular []struct {
			Pubkey string `json:"pubkey"`
		} `json:"popular"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal popular pubkeys failed: %w", err)
	}

	pubkeys := make(map[string]struct{}, len(result.Popular))
	for _, node := range result.Popular {
		pubkeys[node.Pubkey] = struct{}{}
	}

	return pubkeys, nil
}

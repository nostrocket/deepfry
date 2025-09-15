package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
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
kind3CreatedAt: int .
last_db_update: int .
follows: [uid] @reverse .`
	return c.dg.Alter(ctx, &api.Operation{Schema: schema})
}

// AddFollowers adds multiple follows edges from a single follower to multiple followees.
// For kind 3 events, this completely replaces the user's follow list (replaceable event behavior).
func (c *Client) AddFollowers(ctx context.Context, signerPubkey string, kind3createdAt int64, follows map[string]struct{}) error {
	if kind3createdAt == 0 {
		return fmt.Errorf("kind3createdAt must be specified (non-zero)")
	}
	if len(follows) == 0 {
		return nil // nothing to do
	}

	// Filter out self-follows and empty keys, convert to slice
	var validFollows []string
	for followee := range follows {
		if followee == signerPubkey {
			continue // skip self-follows silently
		}
		if followee == "" {
			continue // skip empty follows silently
		}
		validFollows = append(validFollows, followee)
	}

	if len(validFollows) == 0 {
		return nil // nothing to do after filtering
	}

	lastUpdate := time.Now().Unix()

	// First, get follower and existing follows
	query := fmt.Sprintf(`
	{
		follower(func: eq(pubkey, %q)) {
			uid
			follows { 
				uid
				pubkey 
			}
		}
	}`, signerPubkey)

	txn := c.dg.NewTxn()
	resp, err := txn.Query(ctx, query)
	if err != nil {
		txn.Discard(ctx)
		return fmt.Errorf("query follower failed: %w", err)
	}

	// Parse query results
	var result struct {
		Follower []struct {
			UID     string `json:"uid"`
			Follows []struct {
				UID    string `json:"uid"`
				Pubkey string `json:"pubkey"`
			} `json:"follows"`
		} `json:"follower"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		txn.Discard(ctx)
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
		assigned, err := txn.Mutate(ctx, mu)
		if err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("create follower failed: %w", err)
		}
		followerUID = assigned.Uids["follower"]
	} else {
		// Update existing follower
		followerUID = result.Follower[0].UID

		// Track existing follows for deletion
		for _, f := range result.Follower[0].Follows {
			existingFollows[f.Pubkey] = f.UID
		}

		updateNQuads := fmt.Sprintf(`
			<%s> <kind3CreatedAt> "%d" .
			<%s> <last_db_update> "%d" .
		`, followerUID, kind3createdAt, followerUID, lastUpdate)

		mu := &api.Mutation{
			SetNquads: []byte(updateNQuads),
			CommitNow: false,
		}
		if _, err := txn.Mutate(ctx, mu); err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("update follower failed: %w", err)
		}
	}

	// Step 1: Remove all existing follows (kind 3 is replaceable)
	for _, uid := range existingFollows {
		delNQuads := fmt.Sprintf(`<%s> <follows> <%s> .`, followerUID, uid)
		mu := &api.Mutation{
			DelNquads: []byte(delNQuads),
			CommitNow: false,
		}
		if _, err := txn.Mutate(ctx, mu); err != nil {
			continue
		}
	}

	// Step 2: Add new follows
	for i, followee := range validFollows {
		// Find or create followee
		followeeQuery := fmt.Sprintf(`
		{
			followee(func: eq(pubkey, %q)) {
				uid
			}
		}`, followee)

		fresp, err := txn.Query(ctx, followeeQuery)
		if err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("query followee %d failed: %w", i, err)
		}

		var followeeResult struct {
			Followee []struct {
				UID string `json:"uid"`
			} `json:"followee"`
		}
		if err := json.Unmarshal(fresp.Json, &followeeResult); err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("unmarshal followee %d failed: %w", i, err)
		}

		// Create/get followee
		var followeeUID string
		if len(followeeResult.Followee) == 0 {
			// Create new followee
			followeeNQuads := fmt.Sprintf(`
				_:followee%d <pubkey> %q .
			`, i, followee)

			mu := &api.Mutation{
				SetNquads: []byte(followeeNQuads),
				CommitNow: false,
			}
			assigned, err := txn.Mutate(ctx, mu)
			if err != nil {
				txn.Discard(ctx)
				return fmt.Errorf("create followee %d failed: %w", i, err)
			}
			followeeUID = assigned.Uids[fmt.Sprintf("followee%d", i)]
		} else {
			followeeUID = followeeResult.Followee[0].UID
		}

		// Create the follows edge
		edgeNQuads := fmt.Sprintf(`
			<%s> <follows> <%s> .
		`, followerUID, followeeUID)

		mu := &api.Mutation{
			SetNquads: []byte(edgeNQuads),
			CommitNow: false,
		}
		if _, err := txn.Mutate(ctx, mu); err != nil {
			continue
		}
	}

	// Commit all changes
	if err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction failed: %w", err)
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

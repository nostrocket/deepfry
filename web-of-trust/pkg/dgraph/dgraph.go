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
//   last_db_update: datetime .
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
	schema := `pubkey: string @index(exact) @upsert .
kind3CreatedAt: int .
last_db_update: datetime .
follows: [uid] @reverse .`
	return c.dg.Alter(ctx, &api.Operation{Schema: schema})
}

// AddFollower creates a follows edge from follower -> followee.
// Both nodes are upserted so duplicates are impossible.
// The follower's timestamps are updated, the followee node is created if it doesn't exist.
// Returns error if follower timestamps are not specified (zero values).
func (c *Client) AddFollower(ctx context.Context, signerPubkey string, kind3createdAt int64, followee string) error {
	if kind3createdAt == 0 {
		return fmt.Errorf("kind3createdAt must be specified (non-zero)")
	}

	lastUpdate := time.Now()

	// First, check and get UIDs for both pubkeys
	query := fmt.Sprintf(`
	{
		follower(func: eq(pubkey, %q)) {
			uid
		}
		followee(func: eq(pubkey, %q)) {
			uid
		}
	}`, signerPubkey, followee)

	txn := c.dg.NewTxn()
	resp, err := txn.Query(ctx, query)
	if err != nil {
		txn.Discard(ctx)
		return fmt.Errorf("query failed: %w", err)
	}

	// Parse query results
	var result struct {
		Follower []struct {
			UID string `json:"uid"`
		} `json:"follower"`
		Followee []struct {
			UID string `json:"uid"`
		} `json:"followee"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		txn.Discard(ctx)
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	// Create/update follower
	var followerUID string
	if len(result.Follower) == 0 {
		// Create new follower
		followerNQuads := fmt.Sprintf(`
			_:follower <pubkey> %q .
			_:follower <kind3CreatedAt> "%d" .
			_:follower <last_db_update> %q .
		`, signerPubkey, kind3createdAt, lastUpdate.Format(time.RFC3339))

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
		updateNQuads := fmt.Sprintf(`
			<%s> <kind3CreatedAt> "%d" .
			<%s> <last_db_update> %q .
		`, followerUID, kind3createdAt, followerUID, lastUpdate.Format(time.RFC3339))

		mu := &api.Mutation{
			SetNquads: []byte(updateNQuads),
			CommitNow: false,
		}
		if _, err := txn.Mutate(ctx, mu); err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("update follower failed: %w", err)
		}
	}

	// Create/get followee
	var followeeUID string
	if len(result.Followee) == 0 {
		// Create new followee
		followeeNQuads := fmt.Sprintf(`
			_:followee <pubkey> %q .
		`, followee)

		mu := &api.Mutation{
			SetNquads: []byte(followeeNQuads),
			CommitNow: false,
		}
		assigned, err := txn.Mutate(ctx, mu)
		if err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("create followee failed: %w", err)
		}
		followeeUID = assigned.Uids["followee"]
	} else {
		followeeUID = result.Followee[0].UID
	}

	// Create the follows edge
	edgeNQuads := fmt.Sprintf(`
		<%s> <follows> <%s> .
	`, followerUID, followeeUID)

	mu := &api.Mutation{
		SetNquads: []byte(edgeNQuads),
		CommitNow: true,
	}
	if _, err := txn.Mutate(ctx, mu); err != nil {
		txn.Discard(ctx)
		return fmt.Errorf("add edge failed: %w", err)
	}

	return nil
}

// AddFollowersBatch adds multiple follows edges from a single follower to multiple followees.
// This is more efficient than calling AddFollower repeatedly.
func (c *Client) AddFollowersBatch(ctx context.Context, signerPubkey string, kind3createdAt int64, followees []string) error {
	if kind3createdAt == 0 {
		return fmt.Errorf("kind3createdAt must be specified (non-zero)")
	}
	if len(followees) == 0 {
		return nil // nothing to do
	}

	lastUpdate := time.Now()

	// First, ensure follower exists and get its UID
	query := fmt.Sprintf(`
	{
		follower(func: eq(pubkey, %q)) {
			uid
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
			UID string `json:"uid"`
		} `json:"follower"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		txn.Discard(ctx)
		return fmt.Errorf("unmarshal follower failed: %w", err)
	}

	// Create/update follower
	var followerUID string
	if len(result.Follower) == 0 {
		// Create new follower
		followerNQuads := fmt.Sprintf(`
			_:follower <pubkey> %q .
			_:follower <kind3CreatedAt> "%d" .
			_:follower <last_db_update> %q .
		`, signerPubkey, kind3createdAt, lastUpdate.Format(time.RFC3339))

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
		updateNQuads := fmt.Sprintf(`
			<%s> <kind3CreatedAt> "%d" .
			<%s> <last_db_update> %q .
		`, followerUID, kind3createdAt, followerUID, lastUpdate.Format(time.RFC3339))

		mu := &api.Mutation{
			SetNquads: []byte(updateNQuads),
			CommitNow: false,
		}
		if _, err := txn.Mutate(ctx, mu); err != nil {
			txn.Discard(ctx)
			return fmt.Errorf("update follower failed: %w", err)
		}
	}

	// Process each followee
	for i, followee := range followees {
		// Find existing followee
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
			txn.Discard(ctx)
			return fmt.Errorf("add edge %d failed: %w", i, err)
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

	lastUpdate := time.Now()

	// Query to find the nodes
	q := `query {
		f as var(func: eq(pubkey, "` + signerPubkey + `"))
		e as var(func: eq(pubkey, "` + followee + `"))
	}`

	// Update the follower's timestamp
	setNquads := `
		uid(f) <pubkey> "` + signerPubkey + `" .
		uid(f) <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
		uid(f) <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`

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

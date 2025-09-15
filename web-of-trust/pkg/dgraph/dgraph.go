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
follows: uid @reverse .`
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

	// First transaction: check if nodes exist
	checkQuery := `{
		q1(func: eq(pubkey, "` + signerPubkey + `")) {
			uid
		}
		q2(func: eq(pubkey, "` + followee + `")) {
			uid
		}
	}`

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, checkQuery)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	type QueryResp struct {
		Q1 []struct {
			UID string `json:"uid"`
		} `json:"q1"`
		Q2 []struct {
			UID string `json:"uid"`
		} `json:"q2"`
	}
	var qr QueryResp
	if err := json.Unmarshal(resp.Json, &qr); err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	// Commit the read transaction
	if err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit read failed: %w", err)
	}

	// Second transaction: create/update nodes
	txn2 := c.dg.NewTxn()
	defer txn2.Discard(ctx)

	var followerUID, followeeUID string

	// Handle follower node
	if len(qr.Q1) == 0 {
		// Create new follower node
		mu := &api.Mutation{
			SetNquads: []byte(`_:new <pubkey> "` + signerPubkey + `" .
_:new <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
_:new <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`),
		}
		assigned, err := txn2.Mutate(ctx, mu)
		if err != nil {
			return fmt.Errorf("create follower failed: %w", err)
		}
		followerUID = assigned.Uids["new"]
	} else {
		// Update existing follower
		followerUID = qr.Q1[0].UID
		mu := &api.Mutation{
			SetNquads: []byte(`<` + followerUID + `> <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
<` + followerUID + `> <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`),
		}
		if _, err := txn2.Mutate(ctx, mu); err != nil {
			return fmt.Errorf("update follower failed: %w", err)
		}
	}

	// Handle followee node
	if len(qr.Q2) == 0 {
		// Create new followee node
		mu := &api.Mutation{
			SetNquads: []byte(`_:new2 <pubkey> "` + followee + `" .`),
		}
		assigned, err := txn2.Mutate(ctx, mu)
		if err != nil {
			return fmt.Errorf("create followee failed: %w", err)
		}
		followeeUID = assigned.Uids["new2"]
	} else {
		followeeUID = qr.Q2[0].UID
	}

	// Add the follows edge
	mu := &api.Mutation{
		SetNquads: []byte(`<` + followerUID + `> <follows> <` + followeeUID + `> .`),
	}
	if _, err := txn2.Mutate(ctx, mu); err != nil {
		return fmt.Errorf("add edge failed: %w", err)
	}

	if err := txn2.Commit(ctx); err != nil {
		return fmt.Errorf("commit mutations failed: %w", err)
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

	// Build query to find existing nodes
	query := `{
		follower(func: eq(pubkey, "` + signerPubkey + `")) {
			uid
		}`

	for i, followee := range followees {
		query += fmt.Sprintf(`
		followee%d(func: eq(pubkey, "%s")) {
			uid
		}`, i, followee)
	}
	query += "\n}"

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	// Parse to check which nodes exist
	var result map[string][]map[string]interface{}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	// Commit the read
	if err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit read failed: %w", err)
	}

	// New transaction for mutations
	txn2 := c.dg.NewTxn()
	defer txn2.Discard(ctx)

	var followerUID string

	// Handle follower node
	if followerNodes, ok := result["follower"]; !ok || len(followerNodes) == 0 {
		// Create new follower
		mu := &api.Mutation{
			SetNquads: []byte(`_:follower <pubkey> "` + signerPubkey + `" .
_:follower <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
_:follower <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`),
		}
		assigned, err := txn2.Mutate(ctx, mu)
		if err != nil {
			return fmt.Errorf("create follower failed: %w", err)
		}
		followerUID = assigned.Uids["follower"]
	} else {
		// Update existing follower
		followerUID = followerNodes[0]["uid"].(string)
		mu := &api.Mutation{
			SetNquads: []byte(`<` + followerUID + `> <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
<` + followerUID + `> <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`),
		}
		if _, err := txn2.Mutate(ctx, mu); err != nil {
			return fmt.Errorf("update follower failed: %w", err)
		}
	}

	// Handle followee nodes and edges
	for i, followee := range followees {
		followeeKey := fmt.Sprintf("followee%d", i)
		var followeeUID string

		if followeeNodes, ok := result[followeeKey]; ok && len(followeeNodes) > 0 {
			// Followee exists
			followeeUID = followeeNodes[0]["uid"].(string)
		} else {
			// Create followee
			blankNode := fmt.Sprintf("_:followee%d", i)
			mu := &api.Mutation{
				SetNquads: []byte(blankNode + ` <pubkey> "` + followee + `" .`),
			}
			assigned, err := txn2.Mutate(ctx, mu)
			if err != nil {
				return fmt.Errorf("create followee %d failed: %w", i, err)
			}
			followeeUID = assigned.Uids[fmt.Sprintf("followee%d", i)]
		}

		// Add edge
		mu := &api.Mutation{
			SetNquads: []byte(`<` + followerUID + `> <follows> <` + followeeUID + `> .`),
		}
		if _, err := txn2.Mutate(ctx, mu); err != nil {
			return fmt.Errorf("add edge %d failed: %w", i, err)
		}
	}

	if err := txn2.Commit(ctx); err != nil {
		return fmt.Errorf("commit mutations failed: %w", err)
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

	q := `query {
  follower as var(func: eq(pubkey, "` + signerPubkey + `"))
  followee as var(func: eq(pubkey, "` + followee + `"))
}`

	nquads := `
  uid(follower) <pubkey> "` + signerPubkey + `" .
  uid(follower) <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
  uid(follower) <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`

	delNquads := `uid(follower) <follows> uid(followee) .`

	setMu := &api.Mutation{SetNquads: []byte(nquads)}
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

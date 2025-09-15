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

	// Use proper upsert block syntax with blank nodes
	upsertQuery := `
	query {
		q1(func: eq(pubkey, "` + signerPubkey + `")) {
			v1 as uid
		}
		q2(func: eq(pubkey, "` + followee + `")) {
			v2 as uid
		}
	}`

	// Use conditional mutation to create nodes only if they don't exist
	mutation := `
	mutation {
		set {
			uid(v1) <pubkey> "` + signerPubkey + `" .
			uid(v1) <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
			uid(v1) <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .
			uid(v2) <pubkey> "` + followee + `" .
			uid(v1) <follows> uid(v2) .
		}
	}`

	// Execute as an upsert operation
	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, upsertQuery)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	// Parse response to check if nodes exist
	type QueryResp struct {
		Q1 []struct {
			V1 string `json:"uid"`
		} `json:"q1"`
		Q2 []struct {
			V2 string `json:"uid"`
		} `json:"q2"`
	}
	var qr QueryResp
	if err := json.Unmarshal(resp.Json, &qr); err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	// Build mutation based on whether nodes exist
	var nquads string
	if len(qr.Q1) == 0 {
		// Follower doesn't exist, create new blank node
		nquads = `_:follower <pubkey> "` + signerPubkey + `" .
_:follower <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
_:follower <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`
	} else {
		// Follower exists, update it
		nquads = `<` + qr.Q1[0].V1 + `> <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
<` + qr.Q1[0].V1 + `> <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`
	}

	if len(qr.Q2) == 0 {
		// Followee doesn't exist, create new blank node
		nquads += `
_:followee <pubkey> "` + followee + `" .`
		if len(qr.Q1) == 0 {
			nquads += `
_:follower <follows> _:followee .`
		} else {
			nquads += `
<` + qr.Q1[0].V1 + `> <follows> _:followee .`
		}
	} else {
		// Followee exists, just add edge
		if len(qr.Q1) == 0 {
			nquads += `
_:follower <follows> <` + qr.Q2[0].V2 + `> .`
		} else {
			nquads += `
<` + qr.Q1[0].V1 + `> <follows> <` + qr.Q2[0].V2 + `> .`
		}
	}

	mu := &api.Mutation{SetNquads: []byte(nquads)}
	if _, err := txn.Mutate(ctx, mu); err != nil {
		return fmt.Errorf("mutation failed: %w", err)
	}

	if err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
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
	query := `query {
		follower(func: eq(pubkey, "` + signerPubkey + `")) {
			v as uid
		}`

	for i, followee := range followees {
		query += fmt.Sprintf(`
		followee%d(func: eq(pubkey, "%s")) {
			f%d as uid
		}`, i, followee, i)
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

	// Build mutations
	var nquads string

	// Handle follower node
	if followerNodes, ok := result["follower"]; !ok || len(followerNodes) == 0 {
		// Create new follower
		nquads = `_:follower <pubkey> "` + signerPubkey + `" .
_:follower <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
_:follower <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`

		// Add follows edges
		for i, followee := range followees {
			followeeKey := fmt.Sprintf("followee%d", i)
			if followeeNodes, ok := result[followeeKey]; ok && len(followeeNodes) > 0 {
				// Followee exists
				if uid, ok := followeeNodes[0]["uid"].(string); ok {
					nquads += fmt.Sprintf("\n_:follower <follows> <%s> .", uid)
				}
			} else {
				// Create followee
				nquads += fmt.Sprintf(`
_:followee%d <pubkey> "%s" .
_:follower <follows> _:followee%d .`, i, followee, i)
			}
		}
	} else {
		// Update existing follower
		followerUID := followerNodes[0]["uid"].(string)
		nquads = fmt.Sprintf(`<%s> <kind3CreatedAt> "%d" .
<%s> <last_db_update> "%s" .`, followerUID, kind3createdAt, followerUID, lastUpdate.Format(time.RFC3339))

		// Add follows edges
		for i, followee := range followees {
			followeeKey := fmt.Sprintf("followee%d", i)
			if followeeNodes, ok := result[followeeKey]; ok && len(followeeNodes) > 0 {
				// Followee exists
				if uid, ok := followeeNodes[0]["uid"].(string); ok {
					nquads += fmt.Sprintf("\n<%s> <follows> <%s> .", followerUID, uid)
				}
			} else {
				// Create followee
				nquads += fmt.Sprintf(`
_:followee%d <pubkey> "%s" .
<%s> <follows> _:followee%d .`, i, followee, followerUID, i)
			}
		}
	}

	mu := &api.Mutation{SetNquads: []byte(nquads)}
	if _, err := txn.Mutate(ctx, mu); err != nil {
		return fmt.Errorf("mutation failed: %w", err)
	}

	if err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
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

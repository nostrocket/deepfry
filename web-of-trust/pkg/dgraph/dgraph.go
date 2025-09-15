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
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
func (c *Client) AddFollower(ctx context.Context, signerPubkey string, kind3createdAt int64, follower string) error {
	if kind3createdAt == 0 {
		return fmt.Errorf("kind3createdAt must be specified (non-zero)")
	}

	lastUpdate := time.Now()

	q := `query($follower: string, $followee: string) {
  follower as var(func: eq(pubkey, $follower))
  followee as var(func: eq(pubkey, $followee))
}`

	nquads := `
  uid(follower) <pubkey> val(follower) .
  uid(follower) <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
  uid(follower) <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .
  uid(followee) <pubkey> val(followee) .
  uid(follower) <follows> uid(followee) .`

	mu := &api.Mutation{SetNquads: []byte(nquads)}
	req := &api.Request{
		Query: q,
		Vars: map[string]string{
			"$follower": signerPubkey,
			"$followee": follower,
		},
		Mutations: []*api.Mutation{mu},
		CommitNow: true,
	}
	_, err := c.dg.NewTxn().Do(ctx, req)
	return err
}

// RemoveFollower removes the follows edge from follower -> followee.
// The follower's timestamps are updated to reflect the removal.
// Returns error if parameters are empty or timestamps are invalid.
func (c *Client) RemoveFollower(ctx context.Context, signerPubkey string, kind3createdAt int64, follower string) error {
	if signerPubkey == "" {
		return fmt.Errorf("signerPubkey must be specified (non-empty)")
	}
	if follower == "" {
		return fmt.Errorf("follower must be specified (non-empty)")
	}
	if kind3createdAt == 0 {
		return fmt.Errorf("kind3createdAt must be specified (non-zero)")
	}

	lastUpdate := time.Now()

	q := `query($follower: string, $followee: string) {
  follower as var(func: eq(pubkey, $follower))
  followee as var(func: eq(pubkey, $followee))
}`

	nquads := `
  uid(follower) <pubkey> val(follower) .
  uid(follower) <kind3CreatedAt> "` + fmt.Sprintf("%d", kind3createdAt) + `" .
  uid(follower) <last_db_update> "` + lastUpdate.Format(time.RFC3339) + `" .`

	delNquads := `uid(follower) <follows> uid(followee) .`

	setMu := &api.Mutation{SetNquads: []byte(nquads)}
	delMu := &api.Mutation{DelNquads: []byte(delNquads)}
	req := &api.Request{
		Query: q,
		Vars: map[string]string{
			"$follower": signerPubkey,
			"$followee": follower,
		},
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

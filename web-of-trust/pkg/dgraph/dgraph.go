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
	"google.golang.org/grpc/status"
)

// Abstraction layer over Dgraph to store a Nostr Web-of-Trust (kind 3) graph.
// Guarantees uniqueness of pubkeys using upsert schema and upsert blocks.
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

// NewClient creates a new Client connected to the given dgraph gRPC address
// (eg "localhost:9080").
func NewClient(addr string) (*Client, error) {
	// Set up gRPC connection options with better timeouts
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),                   // Make connection establishment blocking
		grpc.WithTimeout(10 * time.Second), // Connection timeout
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(100*1024*1024), // 100MB max message size
			grpc.MaxCallSendMsgSize(100*1024*1024),
		),
	}

	// Establish connection with improved options
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Dgraph: %w", err)
	}

	client := &Client{
		dg:   dgo.NewDgraphClient(api.NewDgraphClient(conn)),
		conn: conn,
	}

	// Perform initial health check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	healthy, diagnostics := client.CheckConnectionHealth(ctx)
	if !healthy {
		conn.Close()
		diagJSON, _ := json.Marshal(diagnostics)
		return nil, fmt.Errorf("Dgraph connection health check failed: %s", string(diagJSON))
	}

	return client, nil
}

// CheckConnectionHealth verifies if the connection to Dgraph is healthy
// and returns detailed diagnostics about the connection state.
func (c *Client) CheckConnectionHealth(ctx context.Context) (bool, map[string]interface{}) {
	diagnostics := make(map[string]interface{})

	// Check gRPC connection state
	connState := c.conn.GetState()
	diagnostics["grpc_conn_state"] = connState.String()

	// Try a simple ping query with a short timeout
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	startTime := time.Now()
	pingQuery := "{ ping(func: uid(0x1)) { uid } }"

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(pingCtx)

	_, err := txn.Query(pingCtx, pingQuery)
	pingDuration := time.Since(startTime)

	diagnostics["ping_duration_ms"] = pingDuration.Milliseconds()
	diagnostics["ping_success"] = (err == nil)

	if err != nil {
		errStatus, ok := status.FromError(err)
		if ok {
			diagnostics["ping_error_code"] = errStatus.Code().String()
			diagnostics["ping_error_message"] = errStatus.Message()
		} else {
			diagnostics["ping_error"] = err.Error()
		}
	}

	return err == nil, diagnostics
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
follows: [uid] @reverse .

type Profile {
  pubkey
  follows
  kind3CreatedAt
  last_db_update
}`
	return c.dg.Alter(ctx, &api.Operation{Schema: schema})
}

// AddFollowers adds multiple follows edges from a single follower to multiple
// followees. For kind 3 events, this completely replaces the user's follow list
// (replaceable event behavior).
func (c *Client) AddFollowers(
	ctx context.Context,
	signerPubkey string,
	kind3createdAt int64,
	follows map[string]struct{},
	debug bool,
) error {
	// Use the provided context directly - no additional timeout needed
	// The caller should manage appropriate timeouts
	queryCtx := ctx

	if debug {
		log.Printf("DEBUG: Starting AddFollowers for pubkey %s with %d follows",
			signerPubkey, len(follows))
	}
	start := time.Now()

	lastUpdate := time.Now().Unix()

	txn := c.dg.NewTxn()
	defer txn.Discard(queryCtx)

	// Step 1: Get follower and existing follows - include kind3CreatedAt to
	// avoid separate query
	followerQueryStartTime := time.Now()
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

	log.Printf("DEBUG: Starting follower query for pubkey %s at %v",
		signerPubkey, followerQueryStartTime.Format(time.RFC3339))

	// Try with retry mechanism
	resp, err := c.queryWithRetries(queryCtx, txn, followerQuery, 3, debug)
	if err != nil {
		duration := time.Since(followerQueryStartTime)

		// Get more details about the gRPC error
		errStatus, ok := status.FromError(err)
		var errDetails string
		if ok {
			errDetails = fmt.Sprintf("gRPC status code: %v, message: %v",
				errStatus.Code(), errStatus.Message())
		} else {
			errDetails = fmt.Sprintf("raw error: %v", err)
		}

		// Check connection state
		connState := c.conn.GetState().String()
		log.Printf("ERROR: Initial follower query failed after %v: %v (%s). Connection state: %s",
			duration, err, errDetails, connState)

		return fmt.Errorf("query follower failed: %w", err)
	}
	followerQueryDuration := time.Since(followerQueryStartTime)
	if debug {
		log.Printf("DEBUG: Initial follower query completed in %v (%v since start)",
			followerQueryDuration, time.Since(start))
	}

	followerUnmarshalStartTime := time.Now()
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
	if debug {
		log.Printf("DEBUG: Unmarshalled follower data in %v",
			time.Since(followerUnmarshalStartTime))
	}

	// Create/update follower
	followerProcessStartTime := time.Now()
	var followerUID string
	existingFollows := make(map[string]string) // pubkey -> uid

	if len(result.Follower) == 0 {
		// Create new follower
		createStartTime := time.Now()
		followerNQuads := fmt.Sprintf(`
			_:follower <pubkey> %q .
			_:follower <dgraph.type> "Profile" .
			_:follower <kind3CreatedAt> "%d" .
			_:follower <last_db_update> "%d" .
		`, signerPubkey, kind3createdAt, lastUpdate)

		mu := &api.Mutation{
			SetNquads: []byte(followerNQuads),
			CommitNow: false,
		}
		assigned, err := c.mutateWithRetries(queryCtx, txn, mu, 3, debug)
		if err != nil {
			log.Printf("ERROR: Create follower mutation failed: %v", err)
			return fmt.Errorf("create follower failed: %w", err)
		}
		followerUID = assigned.Uids["follower"]
		if debug {
			log.Printf("DEBUG: Created new follower in %v",
				time.Since(createStartTime))
		}
	} else {
		// Update existing follower - check if this is newer than existing
		existingKind3CreatedAt := result.Follower[0].Kind3CreatedAt
		if kind3createdAt <= existingKind3CreatedAt {
			// Skip update - existing event is newer or same age
			if debug {
				log.Printf("DEBUG: Skipped update - existing event is newer or same age (%d <= %d)",
					kind3createdAt, existingKind3CreatedAt)
			}
			return nil
		}

		followerUID = result.Follower[0].UID

		// Track existing follows for deletion
		for _, f := range result.Follower[0].Follows {
			existingFollows[f.Pubkey] = f.UID
		}
		if debug {
			log.Printf("DEBUG: Found existing follower with %d follows in %v",
				len(existingFollows), time.Since(followerProcessStartTime))
		}
	}

	// Always update timestamps regardless of whether there are new follows
	updateStartTime := time.Now()
	updateNQuads := fmt.Sprintf(`
		<%s> <kind3CreatedAt> "%d" .
		<%s> <last_db_update> "%d" .
	`, followerUID, kind3createdAt, followerUID, lastUpdate)

	mu := &api.Mutation{
		SetNquads: []byte(updateNQuads),
		CommitNow: false,
	}
	if _, err := c.mutateWithRetries(queryCtx, txn, mu, 3, debug); err != nil {
		log.Printf("ERROR: Update follower timestamps failed: %v", err)
		return fmt.Errorf("update follower timestamps failed: %w", err)
	}
	if debug {
		log.Printf("DEBUG: Update follower timestamps completed in %v",
			time.Since(updateStartTime))
	}

	// Step 2: Remove all existing follows (kind 3 is replaceable)
	if len(existingFollows) > 0 {
		removeStartTime := time.Now()
		var delNQuads string
		for _, uid := range existingFollows {
			delNQuads += fmt.Sprintf("<%s> <follows> <%s> .\n",
				followerUID, uid)
		}
		mu := &api.Mutation{
			DelNquads: []byte(delNQuads),
			CommitNow: false,
		}
		if _, err := c.mutateWithRetries(queryCtx, txn, mu, 3, debug); err != nil {
			log.Printf("ERROR: Remove existing follows failed: %v", err)
			return fmt.Errorf("remove existing follows failed: %w", err)
		}
		if debug {
			log.Printf("DEBUG: Removed %d existing follows in %v",
				len(existingFollows), time.Since(removeStartTime))
		}
	}

	// Step 3: Bulk query all followees at once
	if len(follows) > 0 {
		followeeList := make([]string, 0, len(follows))
		for followee := range follows {
			followeeList = append(followeeList, followee)
		}

		// Build single query for all followees
		bulkQueryStartTime := time.Now()
		var queryParts []string
		for i, followee := range followeeList {
			part := fmt.Sprintf(
				`followee_%d(func: eq(pubkey, %q)) { uid }`,
				i,
				followee,
			)
			queryParts = append(queryParts, part)
		}

		joinedParts := strings.Join(queryParts, "\n")
		bulkQuery := fmt.Sprintf("{ %s }", joinedParts)

		if debug {
			log.Printf("DEBUG: Built bulk query with %d parts in %v",
				len(queryParts), time.Since(bulkQueryStartTime))
		}

		// Execute the bulk query with retries
		bulkQueryExecStartTime := time.Now()

		if debug {
			log.Printf("DEBUG: Executing bulk query for %d followees", len(follows))
		}

		bulkResp, err := c.queryWithRetries(queryCtx, txn, bulkQuery, 3, debug)
		if err != nil {
			errStatus, ok := status.FromError(err)
			var errDetails string
			if ok {
				errDetails = fmt.Sprintf("gRPC status code: %v, message: %v",
					errStatus.Code(), errStatus.Message())
			} else {
				errDetails = fmt.Sprintf("raw error: %v", err)
			}

			log.Printf("ERROR: Bulk query failed after %v: %v (%s)",
				time.Since(bulkQueryExecStartTime), err, errDetails)

			return fmt.Errorf("bulk query followees failed: %w", err)
		}
		if debug {
			log.Printf("DEBUG: Executed bulk query for %d followees in %v",
				len(follows), time.Since(bulkQueryExecStartTime))
		}

		// Parse bulk results
		unmarshalStartTime := time.Now()
		var bulkResult map[string][]struct {
			UID string `json:"uid"`
		}
		if err := json.Unmarshal(bulkResp.Json, &bulkResult); err != nil {
			return fmt.Errorf("unmarshal bulk followees failed: %w", err)
		}
		if debug {
			log.Printf("DEBUG: Unmarshalled bulk query results in %v",
				time.Since(unmarshalStartTime))
		}

		// Step 4: Create missing followees in bulk
		prepFolloweeStartTime := time.Now()
		var createNQuads string
		followeeUIDs := make([]string, len(followeeList))
		var missingCount int

		for i, followee := range followeeList {
			key := fmt.Sprintf("followee_%d", i)
			if nodes, exists := bulkResult[key]; exists && len(nodes) > 0 {
				// Followee exists
				followeeUIDs[i] = nodes[0].UID
			} else {
				// Need to create followee
				missingCount++
				blankNodeID := fmt.Sprintf("new_followee_%d", i)
				createNQuads += fmt.Sprintf("_:%s <pubkey> %q .\n",
					blankNodeID, followee)
				createNQuads += fmt.Sprintf(
					"_:%s <dgraph.type> \"Profile\" .\n", blankNodeID)
				followeeUIDs[i] = "_:" + blankNodeID
			}
		}
		if debug {
			log.Printf("DEBUG: Prepared %d missing followees (out of %d total) in %v",
				missingCount, len(followeeList), time.Since(prepFolloweeStartTime))
		}

		// Create missing followees if any
		if createNQuads != "" {
			createFolloweesStartTime := time.Now()
			mu := &api.Mutation{
				SetNquads: []byte(createNQuads),
				CommitNow: false,
			}
			assigned, err := c.mutateWithRetries(queryCtx, txn, mu, 3, debug)
			if err != nil {
				log.Printf("ERROR: Create missing followees failed: %v", err)
				return fmt.Errorf("create missing followees failed: %w", err)
			}
			if debug {
				log.Printf("DEBUG: Created %d missing followees in %v",
					missingCount, time.Since(createFolloweesStartTime))
			}

			// Replace blank node references with actual UIDs
			for i, uid := range followeeUIDs {
				if strings.HasPrefix(uid, "_:") {
					// Remove "_:" prefix
					blankNodeID := uid[2:]
					if actualUID, exists := assigned.Uids[blankNodeID]; exists {
						followeeUIDs[i] = actualUID
					}
				}
			}
		}

		// Step 5: Create all follow edges in bulk
		edgeStartTime := time.Now()
		var edgeNQuads string
		var edgeCount int
		for _, followeeUID := range followeeUIDs {
			if followeeUID != "" && !strings.HasPrefix(followeeUID, "_:") {
				edgeCount++
				edgeNQuads += fmt.Sprintf("<%s> <follows> <%s> .\n",
					followerUID, followeeUID)
			}
		}

		if edgeNQuads != "" {
			edgeMutateStartTime := time.Now()
			mu := &api.Mutation{
				SetNquads: []byte(edgeNQuads),
				CommitNow: false,
			}
			if _, err := c.mutateWithRetries(queryCtx, txn, mu, 3, debug); err != nil {
				log.Printf("ERROR: Create follow edges failed: %v", err)
				return fmt.Errorf("create follow edges failed: %w", err)
			}
			if debug {
				log.Printf("DEBUG: Created %d follow edges in %v (prep: %v, mutation: %v)",
					edgeCount,
					time.Since(edgeStartTime),
					edgeMutateStartTime.Sub(edgeStartTime),
					time.Since(edgeMutateStartTime))
			}
		}
	}

	// Commit all changes with retries
	commitStartTime := time.Now()
	log.Printf("DEBUG: Starting transaction commit for pubkey %s with %d follows",
		signerPubkey, len(follows))

	// Log connection state before commit attempt
	if debug {
		connState := c.conn.GetState().String()
		log.Printf("DEBUG: gRPC connection state before commit: %s", connState)
	}

	if err := c.commitWithRetries(queryCtx, txn, 3, debug); err != nil {
		// Get more details about the gRPC error
		errStatus, ok := status.FromError(err)
		var errDetails string
		if ok {
			errDetails = fmt.Sprintf("gRPC status code: %v, message: %v",
				errStatus.Code(), errStatus.Message())
		} else {
			errDetails = fmt.Sprintf("raw error: %v", err)
		}

		// Check connection state
		connState := c.conn.GetState().String()
		log.Printf("ERROR: Transaction commit failed after %v: %v (%s). Connection state: %s",
			time.Since(commitStartTime), err, errDetails, connState)

		return fmt.Errorf("commit transaction failed: %w", err)
	}

	if debug {
		log.Printf("DEBUG: Transaction commit completed in %v",
			time.Since(commitStartTime))
	}

	totalDuration := time.Since(start)

	// Always log timing for slow operations, regardless of debug flag
	if totalDuration > 5*time.Second || debug {
		logTimings := map[string]interface{}{
			"pubkey":         signerPubkey,
			"follows_count":  len(follows),
			"total_duration": totalDuration.String(),
			"component":      "dgraph-client",
			"operation":      "AddFollowers",
		}
		timingsJSON, _ := json.Marshal(logTimings)
		log.Printf("TIMING_METRICS: %s", string(timingsJSON))
	}

	if debug {
		log.Printf(
			"DEBUG: AddFollowers completed successfully in %v for pubkey %s with %d follows",
			totalDuration,
			signerPubkey,
			len(follows),
		)
	}
	return nil
}

// RemoveFollower removes the follows edge from follower -> followee.
// The follower's timestamps are updated to reflect the removal.
// Returns error if parameters are empty or timestamps are invalid.
func (c *Client) RemoveFollower(
	ctx context.Context,
	signerPubkey string,
	kind3createdAt int64,
	followee string,
) error {
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
		uid(f) <dgraph.type> "Profile" .
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
func (c *Client) RemovePubKeyIfNoFollowers(
	ctx context.Context,
	pubkey string,
) (bool, error) {
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

// GetStalePubkeys returns pubkeys with last_db_update older than the given
// threshold, or pubkeys that don't have last_db_update set at all.
// If olderThanUnix is not provided, defaults to 24 hours ago.
// Results are sorted by age, with least recently updated first.
// Returns a map of pubkey to kind3CreatedAt timestamp.
func (c *Client) GetStalePubkeys(
	ctx context.Context,
	olderThanUnix int64,
) (map[string]int64, error) {
	query := fmt.Sprintf(`
	{
		stale(func: has(pubkey), orderasc: last_db_update) 
		@filter(NOT has(last_db_update) OR lt(last_db_update, %d)) {
			pubkey
			kind3CreatedAt
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
			Pubkey         string `json:"pubkey"`
			Kind3CreatedAt int64  `json:"kind3CreatedAt"`
		} `json:"stale"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal stale pubkeys failed: %w", err)
	}

	pubkeyMap := make(map[string]int64, len(result.Stale))
	for _, node := range result.Stale {
		pubkeyMap[node.Pubkey] = node.Kind3CreatedAt
	}

	return pubkeyMap, nil
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

// GetKind3CreatedAt returns the kind3CreatedAt unix timestamp for the given
// pubkey. Returns 0 if the pubkey doesn't exist or has no kind3CreatedAt value.
func (c *Client) GetKind3CreatedAt(
	ctx context.Context,
	pubkey string,
) (int64, error) {
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

// GetPubkeysWithMinFollowers returns a map of pubkeys that have at least the
// specified number of followers. The map uses pubkey as key with empty struct
// as value for memory-efficient set operations.
func (c *Client) GetPubkeysWithMinFollowers(
	ctx context.Context,
	minFollowers int,
) (map[string]struct{}, error) {
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

// GetPubkeysWithMinFollowersPaginated returns pubkeys with at least the
// specified number of followers using pagination to avoid gRPC message size
// limits. Calls the provided callback function for each batch of pubkeys.
func (c *Client) GetPubkeysWithMinFollowersPaginated(
	ctx context.Context,
	minFollowers int,
	batchSize int,
	callback func([]string) error,
) error {
	offset := 0

	for {
		query := fmt.Sprintf(`
		{
			popular(func: has(pubkey), first: %d, offset: %d) 
			@filter(ge(count(~follows), %d)) {
				pubkey
			}
		}`, batchSize, offset, minFollowers)

		txn := c.dg.NewTxn()
		resp, err := txn.Query(ctx, query)
		txn.Discard(ctx)

		if err != nil {
			return fmt.Errorf("query popular pubkeys failed: %w", err)
		}

		var result struct {
			Popular []struct {
				Pubkey string `json:"pubkey"`
			} `json:"popular"`
		}

		if err := json.Unmarshal(resp.Json, &result); err != nil {
			return fmt.Errorf("unmarshal popular pubkeys failed: %w", err)
		}

		// If no results, we're done
		if len(result.Popular) == 0 {
			break
		}

		// Extract pubkeys from this batch
		batch := make([]string, len(result.Popular))
		for i, node := range result.Popular {
			batch[i] = node.Pubkey
		}

		// Call the callback with this batch
		if err := callback(batch); err != nil {
			return fmt.Errorf("callback error: %w", err)
		}

		// If we got fewer results than batch size, we're done
		if len(result.Popular) < batchSize {
			break
		}

		offset += batchSize
	}

	return nil
}

// TouchLastDBUpdate updates only the last_db_update field for a given pubkey
// to the current time. It only performs the update if the pubkey exists and
// has a non-zero kind3CreatedAt value.
// Returns true if the update was performed, false if skipped (no pubkey or zero kind3CreatedAt).
func (c *Client) TouchLastDBUpdate(
	ctx context.Context,
	pubkey string,
) (bool, error) {
	if pubkey == "" {
		return false, fmt.Errorf("pubkey must be specified (non-empty)")
	}

	// Create a timeout context for this operation
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// First query to check if the pubkey exists and has a valid kind3CreatedAt
	query := `query GetNode($pubkey: string) {
		node(func: eq(pubkey, $pubkey), first: 1) {
			uid
			kind3CreatedAt
		}
	}`

	txn := c.dg.NewTxn()
	defer txn.Discard(queryCtx)

	req := &api.Request{
		Query: query,
		Vars:  map[string]string{"$pubkey": pubkey},
	}

	resp, err := txn.Do(queryCtx, req)
	if err != nil {
		return false, fmt.Errorf("query pubkey failed: %w", err)
	}

	var result struct {
		Node []struct {
			UID            string `json:"uid"`
			Kind3CreatedAt int64  `json:"kind3CreatedAt"`
		} `json:"node"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return false, fmt.Errorf("unmarshal pubkey query failed: %w", err)
	}

	// Check if pubkey exists and has a non-zero kind3CreatedAt
	if len(result.Node) == 0 || result.Node[0].Kind3CreatedAt == 0 {
		return false, nil // Skip update
	}

	// Update the last_db_update timestamp
	lastUpdate := time.Now().Unix()
	nquads := fmt.Sprintf(`
		<%s> <last_db_update> "%d" .
	`, result.Node[0].UID, lastUpdate)

	mu := &api.Mutation{
		SetNquads: []byte(nquads),
		CommitNow: true,
	}

	_, err = txn.Mutate(queryCtx, mu)
	if err != nil {
		return false, fmt.Errorf("update last_db_update failed: %w", err)
	}

	return true, nil
}

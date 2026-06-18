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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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

// NewClient creates a new Client connected to the given dgraph gRPC address
// (eg "localhost:9080").
func NewClient(addr string) (*Client, error) {
	// A frontier-first GetStalePubkeys query with a large `first:` can return a
	// response well over gRPC's default 4MB receive cap when the graph holds
	// hundreds of thousands of stub nodes. Raise the max receive size so large
	// selection batches do not fail with ResourceExhausted.
	const maxRecvMsgSize = 256 << 20 // 256 MiB
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
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
// Phase 8 adds next_attempt and miss_count predicates (D-01, additive only).
func (c *Client) EnsureSchema(ctx context.Context) error {
	schema := `pubkey: string @index(exact) @upsert @unique .
kind3CreatedAt: int @index(int) .
last_db_update: int @index(int) .
last_attempt: int @index(int) .
next_attempt: int @index(int) .
miss_count: int .
follows: [uid] @reverse .

type Profile {
  pubkey
  follows
  kind3CreatedAt
  last_db_update
  last_attempt
  next_attempt
  miss_count
}`
	return c.dg.Alter(ctx, &api.Operation{Schema: schema})
}

// Internal batching/timeout tuning for AddFollowers. These are the single
// tuning point for the unified write path:
//   - batchSize keeps each followee-resolution query string and each mutation
//     under the ~4MB gRPC message cap (at ~10k followees the *query string*
//     alone exceeds 4MB if issued in one shot, so the query is batched too).
//   - the deadline scales with follow-list size so a large list neither fails
//     prematurely nor hangs unbounded.
const (
	baseTimeout     = 30 * time.Second
	perBatchTimeout = 5 * time.Second
	batchSize       = 200
)

// IsTransientError classifies Dgraph/gRPC failures that are worth scheduling
// for a later attempt. ResourceExhausted remains fatal: oversized gRPC messages
// are structural for the payload and indefinite retry would livelock.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

// FollowUpdateError adds AddFollowers progress context while preserving the
// underlying Dgraph/gRPC error for errors.As/errors.Is/status.FromError callers.
type FollowUpdateError struct {
	Pubkey      string
	FollowCount int
	Phase       string
	ChunkIndex  int
	ChunkTotal  int
	Elapsed     time.Duration
	RetryCount  int
	Outcome     string
	Underlying  error
}

func (e *FollowUpdateError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf(
		"follow update pubkey=%s follows=%d phase=%s chunk=%d/%d elapsed=%s retry_count=%d outcome=%s: %v",
		e.Pubkey,
		e.FollowCount,
		e.Phase,
		e.ChunkIndex,
		e.ChunkTotal,
		e.Elapsed,
		e.RetryCount,
		e.Outcome,
		e.Underlying,
	)
}

func (e *FollowUpdateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Underlying
}

type followUpdateProgress struct {
	pubkey          string
	followCount     int
	totalChunks     int
	completedChunks int
	currentPhase    string
	currentChunk    int
	started         time.Time
	retryCount      int
}

func newFollowUpdateProgress(pubkey string, followCount int, totalChunks int) *followUpdateProgress {
	if totalChunks < 1 {
		totalChunks = 1
	}
	return &followUpdateProgress{
		pubkey:      pubkey,
		followCount: followCount,
		totalChunks: totalChunks,
		started:     time.Now(),
	}
}

func (p *followUpdateProgress) beginChunk(phase string, chunkIndex int) {
	p.currentPhase = phase
	if chunkIndex <= 0 {
		chunkIndex = p.completedChunks + 1
	}
	p.currentChunk = chunkIndex
	if p.currentChunk > p.totalChunks {
		p.totalChunks = p.currentChunk
	}
}

func (p *followUpdateProgress) completeChunk() {
	if p.currentChunk > p.completedChunks {
		p.completedChunks = p.currentChunk
	}
}

func (p *followUpdateProgress) finish(outcome string, err error) *FollowUpdateError {
	if err == nil {
		return nil
	}
	chunk := p.currentChunk
	if chunk == 0 {
		chunk = p.completedChunks
	}
	if chunk == 0 {
		chunk = 1
	}
	return &FollowUpdateError{
		Pubkey:      p.pubkey,
		FollowCount: p.followCount,
		Phase:       p.currentPhase,
		ChunkIndex:  chunk,
		ChunkTotal:  p.totalChunks,
		Elapsed:     time.Since(p.started),
		RetryCount:  p.retryCount,
		Outcome:     outcome,
		Underlying:  err,
	}
}

func withWindowTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, perBatchTimeout)
}

// chunkSlice splits items into consecutive windows of at most size elements.
// It is a pure helper (no Dgraph dependency) so it can be unit-tested as the
// seam for the internal batching in AddFollowers. It never returns a nil or
// empty trailing chunk: an empty input yields zero chunks.
func chunkSlice(items []string, size int) [][]string {
	if size <= 0 || len(items) == 0 {
		return nil
	}
	n := (len(items) + size - 1) / size
	chunks := make([][]string, 0, n)
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}
	return chunks
}

// AddFollowers adds multiple follows edges from a single follower to multiple
// followees. For kind 3 events, this completely replaces the user's follow list
// (replaceable event behavior).
//
// This is the single write path for an entire kind-3 event: it receives the
// FULL follow set, runs the version guard and remove-all-existing-follows
// exactly once, and batches only the internal followee-resolution query and
// edge mutations (in batchSize windows) to stay under the gRPC message cap. The
// whole operation is one all-or-nothing transaction.
func (c *Client) AddFollowers(
	ctx context.Context,
	signerPubkey string,
	kind3createdAt int64,
	follows map[string]struct{},
	debug bool,
) error {
	progress := newFollowUpdateProgress(signerPubkey, len(follows), 1)
	logFinish := func(outcome string, elapsed time.Duration) {
		log.Printf(
			"follow_update pubkey=%s follows=%d chunk=%d/%d elapsed=%s retry_count=%d outcome=%s",
			progress.pubkey,
			progress.followCount,
			progress.currentChunk,
			progress.totalChunks,
			elapsed,
			progress.retryCount,
			outcome,
		)
	}
	fail := func(format string, args ...interface{}) error {
		err := fmt.Errorf(format, args...)
		outcome := "fatal_error"
		if IsTransientError(err) {
			outcome = "transient_error"
		}
		wrapped := progress.finish(outcome, err)
		logFinish(outcome, wrapped.Elapsed)
		return wrapped
	}

	// Validation gate (D-08/D-09): an invalid signer is rejected outright and
	// nothing is written, so a malformed pubkey never reaches an nquad.
	if !isValidHexPubkey(signerPubkey) {
		return fail("invalid signer pubkey %q: must be 64 hex chars", signerPubkey)
	}

	// Size-scaled timeout (D-07): one live context for the whole operation, so
	// there is nothing per-batch to leak. The deadline grows with the number of
	// batches the follow set will require.
	batches := (len(follows) + batchSize - 1) / batchSize
	deadline := baseTimeout + time.Duration(batches)*perBatchTimeout
	queryCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	if debug {
		log.Printf("DEBUG: Starting AddFollowers for pubkey %s with %d follows",
			signerPubkey, len(follows))
	}

	lastUpdate := time.Now().Unix()

	txn := c.dg.NewTxn()
	defer txn.Discard(queryCtx)

	// Step 1: Get follower and existing follows - include kind3CreatedAt to
	// avoid separate query
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

	progress.beginChunk("query_follower", 1)
	windowCtx, windowCancel := withWindowTimeout(queryCtx)
	resp, err := txn.Query(windowCtx, followerQuery)
	windowCancel()
	if err != nil {
		return fail("query follower failed: %w", err)
	}
	progress.completeChunk()
	if debug {
		log.Printf("DEBUG: Initial follower query completed in %v",
			time.Since(progress.started))
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
		return fail("unmarshal follower failed: %w", err)
	}

	// Create/update follower
	var followerUID string
	existingFollows := make(map[string]string) // pubkey -> uid

	followeeList := make([]string, 0, len(follows))
	for followee := range follows {
		if !isValidHexPubkey(followee) {
			log.Printf("WARN: skipping invalid followee pubkey %q for signer %s",
				followee, signerPubkey)
			continue
		}
		followeeList = append(followeeList, followee)
	}
	followeeChunks := chunkSlice(followeeList, batchSize)
	// Upper-bound total before resolving missing followees. Missing-followee
	// creation shares the same window count as followee resolution, and edge
	// mutation windows are capped by the same valid followee count.
	progress.totalChunks = 2 + len(followeeChunks)*3 + 1 // follower + timestamp + windows + commit

	if len(result.Follower) == 0 {
		// Create new follower
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
		progress.beginChunk("create_follower", progress.completedChunks+1)
		windowCtx, windowCancel := withWindowTimeout(queryCtx)
		assigned, err := txn.Mutate(windowCtx, mu)
		windowCancel()
		if err != nil {
			return fail("create follower failed: %w", err)
		}
		progress.completeChunk()
		followerUID = assigned.Uids["follower"]
		log.Printf("New pubkey added to graph (signer): %s", signerPubkey)
	} else {
		// Update existing follower - check if this is newer than existing
		existingKind3CreatedAt := result.Follower[0].Kind3CreatedAt
		if kind3createdAt <= existingKind3CreatedAt {
			// Skip update - existing event is newer or same age
			if debug {
				progress.beginChunk("skipped_old_event", progress.completedChunks+1)
				progress.completeChunk()
				logFinish("success", time.Since(progress.started))
			}
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
	progress.beginChunk("update_follower_timestamps", progress.completedChunks+1)
	windowCtx, windowCancel = withWindowTimeout(queryCtx)
	_, err = txn.Mutate(windowCtx, mu)
	windowCancel()
	if err != nil {
		return fail("update follower timestamps failed: %w", err)
	}
	progress.completeChunk()

	// Step 2: Remove all existing follows (kind 3 is replaceable)
	if len(existingFollows) > 0 {
		progress.totalChunks++
		var delNQuads string
		for _, uid := range existingFollows {
			delNQuads += fmt.Sprintf("<%s> <follows> <%s> .\n",
				followerUID, uid)
		}
		mu := &api.Mutation{
			DelNquads: []byte(delNQuads),
			CommitNow: false,
		}
		progress.beginChunk("remove_existing_follows", progress.completedChunks+1)
		windowCtx, windowCancel = withWindowTimeout(queryCtx)
		_, err = txn.Mutate(windowCtx, mu)
		windowCancel()
		if err != nil {
			return fail("remove existing follows failed: %w", err)
		}
		progress.completeChunk()
	}

	// Step 3: Resolve followees and create follow edges, batched in batchSize
	// windows. Both the resolution QUERY STRING and the mutations are batched:
	// at ~10k followees a single query string would itself blow past the ~4MB
	// gRPC cap (D-06). Invalid followees are skipped and logged so one bad entry
	// never aborts the rest of the list (D-09).
	if len(followeeList) > 0 {
		var allEdgeNQuads strings.Builder
		for _, window := range followeeChunks {
			// Build the followee-resolution query for just this window.
			queryParts := make([]string, 0, len(window))
			for i, followee := range window {
				queryParts = append(queryParts, fmt.Sprintf(
					`followee_%d(func: eq(pubkey, %q)) { uid }`,
					i,
					followee,
				))
			}
			bulkQuery := fmt.Sprintf("{ %s }", strings.Join(queryParts, "\n"))

			progress.beginChunk("resolve_followees", progress.completedChunks+1)
			windowCtx, windowCancel = withWindowTimeout(queryCtx)
			bulkResp, err := txn.Query(windowCtx, bulkQuery)
			windowCancel()
			if err != nil {
				return fail("bulk query followees failed: %w", err)
			}
			progress.completeChunk()

			var bulkResult map[string][]struct {
				UID string `json:"uid"`
			}
			if err := json.Unmarshal(bulkResp.Json, &bulkResult); err != nil {
				return fail("unmarshal bulk followees failed: %w", err)
			}

			// Stage stub creation for missing followees in this window.
			var createNQuads string
			followeeUIDs := make([]string, len(window))
			for i, followee := range window {
				key := fmt.Sprintf("followee_%d", i)
				if nodes, exists := bulkResult[key]; exists && len(nodes) > 0 {
					followeeUIDs[i] = nodes[0].UID
				} else {
					blankNodeID := fmt.Sprintf("new_followee_%d", i)
					createNQuads += fmt.Sprintf("_:%s <pubkey> %q .\n",
						blankNodeID, followee)
					createNQuads += fmt.Sprintf(
						"_:%s <dgraph.type> \"Profile\" .\n", blankNodeID)
					followeeUIDs[i] = "_:" + blankNodeID
					if debug {
						log.Printf("New pubkey added to graph (stub): %s", followee)
					}
				}
			}

			// Create missing followees in this window, if any.
			if createNQuads != "" {
				mu := &api.Mutation{
					SetNquads: []byte(createNQuads),
					CommitNow: false,
				}
				progress.beginChunk("create_missing_followees", progress.completedChunks+1)
				windowCtx, windowCancel = withWindowTimeout(queryCtx)
				assigned, err := txn.Mutate(windowCtx, mu)
				windowCancel()
				if err != nil {
					return fail("create missing followees failed: %w", err)
				}
				progress.completeChunk()
				for i, uid := range followeeUIDs {
					if strings.HasPrefix(uid, "_:") {
						blankNodeID := uid[2:]
						if actualUID, exists := assigned.Uids[blankNodeID]; exists {
							followeeUIDs[i] = actualUID
						}
					}
				}
			} else {
				progress.beginChunk("create_missing_followees", progress.completedChunks+1)
				progress.completeChunk()
			}

			// Accumulate follow edges for this window.
			for _, followeeUID := range followeeUIDs {
				if followeeUID != "" && !strings.HasPrefix(followeeUID, "_:") {
					allEdgeNQuads.WriteString(fmt.Sprintf("<%s> <follows> <%s> .\n",
						followerUID, followeeUID))
				}
			}
		}

		// Create all follow edges, batched in batchSize windows so the edge
		// mutation also stays under the gRPC cap on huge follow lists.
		edgeLines := strings.Split(strings.TrimRight(allEdgeNQuads.String(), "\n"), "\n")
		if len(edgeLines) == 1 && edgeLines[0] == "" {
			edgeLines = nil
		}
		for _, edgeWindow := range chunkSlice(edgeLines, batchSize) {
			edgeNQuads := strings.Join(edgeWindow, "\n") + "\n"
			mu := &api.Mutation{
				SetNquads: []byte(edgeNQuads),
				CommitNow: false,
			}
			progress.beginChunk("create_follow_edges", progress.completedChunks+1)
			windowCtx, windowCancel = withWindowTimeout(queryCtx)
			_, err = txn.Mutate(windowCtx, mu)
			windowCancel()
			if err != nil {
				return fail("create follow edges failed: %w", err)
			}
			progress.completeChunk()
		}
	}

	// Commit all changes
	progress.beginChunk("commit_transaction", progress.completedChunks+1)
	windowCtx, windowCancel = withWindowTimeout(queryCtx)
	err = txn.Commit(windowCtx)
	windowCancel()
	if err != nil {
		return fail("commit transaction failed: %w", err)
	}
	progress.completeChunk()

	elapsed := time.Since(progress.started)
	if debug || elapsed > baseTimeout || len(followeeList) > batchSize {
		logFinish("success", elapsed)
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

// GetStalePubkeys returns up to `limit` pubkeys that need (re)crawling, as a map
// of pubkey -> kind3CreatedAt. It prioritises the uncrawled frontier (pubkeys
// never attempted) and only then tops up with previously-attempted pubkeys
// whose next_attempt timestamp is in the past.
//
// Both phases are ordered by descending follower count (PERF-01, D-08).
// D-09 verification: `orderdesc: count(~follows)` in the `func:` line is NOT
// supported by Dgraph v25 (rejects "Expected val(). Got count() with order.").
// The workaround uses a `var` block to compute `count(~follows)` into an
// aggregation variable `fc`, then uses `val(fc)` as the orderdesc key — this
// is semantically equivalent and verified working on the production graph.
// D-10 (stored follower_count predicate) is NOT needed since the val() pattern
// achieves the same ordering with a computed aggregate.
//
// Large-frontier sort-cap evidence (HARD-04/WR-05): 08-REVIEW.md WR-05 raises
// the concern that `orderdesc: val(fc)` over a large frontier set may be capped
// at 1000 rows by Dgraph before the explicit `first:` limit is applied,
// potentially re-introducing the historical stub-starvation bug for very large
// frontiers. This concern was resolved via the D-09 human checkpoint in Phase 8:
// on the production graph (100k+ frontier nodes) `first: N` was verified to be
// honored together with `orderdesc: val(fc)` — the top-N nodes by follower count
// were returned, not a pre-truncated subset capped at 1000. The D-09 checkpoint
// is the standing live-verified evidence for this guarantee (08-REVIEW.md WR-05).
// The integration test TestGetStalePubkeysOrder exercises correctness at small
// scale; the >1000-row regime is covered by the D-09 live verification.
//
// Phase 8 change: the aged phase now keys on next_attempt (D-02) instead of
// last_attempt. The olderThanUnix parameter is kept for API compatibility but
// is no longer used by the aged phase — it is deprecated and ignored.
//
// IMPORTANT: never-attempted nodes are selected by an explicit `NOT has(last_attempt)`
// query with an explicit `first:`. Do NOT use `orderasc: last_attempt` to surface
// them — missing-value nodes sort last and Dgraph caps an unbounded sorted query at
// 1000 rows, which is the bug this function previously had (it returned only
// already-crawled accounts and never a single stub).
func (c *Client) GetStalePubkeys(
	ctx context.Context,
	olderThanUnix int64, // deprecated as of Phase 8; aged phase now uses lt(next_attempt, now)
	limit int,
) (map[string]int64, error) {
	out := make(map[string]int64, limit)

	// Phase 1: the uncrawled frontier — pubkeys we have never attempted.
	// Ordered by descending follower count (PERF-01) via val() aggregation (D-09 fix).
	frontierQuery := fmt.Sprintf(`
	{
		var(func: has(pubkey)) @filter(NOT has(last_attempt)) {
			fc as count(~follows)
		}
		frontier(func: uid(fc), first: %d, orderdesc: val(fc)) {
			pubkey
			kind3CreatedAt
		}
	}`, limit)
	if err := c.collectStale(ctx, frontierQuery, "frontier", out); err != nil {
		return nil, err
	}

	// Phase 2: top up with previously-attempted pubkeys whose next_attempt
	// is in the past (D-02). Ordered by descending follower count (PERF-01).
	if remaining := limit - len(out); remaining > 0 {
		nowUnix := time.Now().Unix()
		agedQuery := fmt.Sprintf(`
		{
			var(func: has(next_attempt)) @filter(lt(next_attempt, %d)) {
				ac as count(~follows)
			}
			aged(func: uid(ac), first: %d, orderdesc: val(ac)) {
				pubkey
				kind3CreatedAt
			}
		}`, nowUnix, remaining)
		if err := c.collectStale(ctx, agedQuery, "aged", out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// collectStale runs a stale-selection query whose root block is named `block`
// and merges its {pubkey -> kind3CreatedAt} rows into out.
func (c *Client) collectStale(
	ctx context.Context,
	query, block string,
	out map[string]int64,
) error {
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query stale pubkeys (%s) failed: %w", block, err)
	}

	var parsed map[string][]struct {
		Pubkey         string `json:"pubkey"`
		Kind3CreatedAt int64  `json:"kind3CreatedAt"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		return fmt.Errorf("unmarshal stale pubkeys (%s) failed: %w", block, err)
	}
	for _, n := range parsed[block] {
		out[n.Pubkey] = n.Kind3CreatedAt
	}
	return nil
}

// MarkAttempted stamps last_attempt = ts on every given pubkey and applies
// hit/miss backoff stamping per PERF-02 (D-03/D-04). It records that the
// crawler tried to fetch the pubkey's kind-3 so that un-fetchable pubkeys age
// out via next_attempt instead of being retried every loop.
//
// hits: set of pubkeys that returned a kind-3 event this batch (D-05).
//   - HIT (pubkey in hits): next_attempt = ts + params.HitRefreshCadence, miss_count = 0 (D-03)
//   - MISS (pubkey not in hits): next_attempt = ts + BackoffInterval(currentMiss, ...), miss_count++ (D-04)
//
// The VALID-03 recover-or-purge gate (Phase 5) is preserved intact: invalid
// pubkeys are recovered (uppercase→lowercase) or purged before reaching the
// stamp path. Recovered nodes are NOT stamped (they re-enter the frontier).
//
// params carries the config-driven backoff values so pkg/dgraph does not need
// to import pkg/config (avoids import cycle). Use DefaultBackoffParams() if
// config is not yet wired.
func (c *Client) MarkAttempted(
	ctx context.Context,
	pubkeys []string,
	ts int64,
	hits map[string]struct{},
	params BackoffParams,
) error {
	if len(pubkeys) == 0 {
		return nil
	}

	// Validation gate with inline recover-or-purge (VALID-03).
	// For each invalid pubkey, attempt to recover it (uppercase→lowercase) or
	// purge it (unrecoverable garbage) so it stops re-entering the stale
	// frontier. Valid pubkeys are collected into the `valid` slice for the
	// hit/miss stamp path.
	valid := make([]string, 0, len(pubkeys))
	for _, pk := range pubkeys {
		if isValidHexPubkey(pk) {
			valid = append(valid, pk)
			continue
		}

		lower := strings.ToLower(pk)
		if isValidHexPubkey(lower) {
			// RECOVERABLE: pk is uppercase/mixed-case 64-char hex.
			// Resolve the garbage node's UID then either update it in place
			// (if the lowercase form doesn't already exist) or purge it
			// (if a separate lowercase node already exists).
			garbageUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{pk})
			if err != nil {
				log.Printf("WARN: recover pubkey %q — UID lookup failed: %v", pk, err)
				continue
			}
			garbageUID, found := garbageUIDs[pk]
			if !found {
				// No node in Dgraph for this garbage string — nothing to fix.
				continue
			}

			// Check whether the corrected lowercase form already exists.
			lowerUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{lower})
			if err != nil {
				log.Printf("WARN: recover pubkey %q — lowercase UID lookup failed: %v", pk, err)
				continue
			}
			if _, lowerExists := lowerUIDs[lower]; lowerExists {
				// Duplicate: the canonical lowercase node already exists.
				// Purge the uppercase garbage node.
				if err := c.DeleteNodes(ctx, []string{garbageUID}); err != nil {
					log.Printf("WARN: purge duplicate uppercase node %q (uid %s) failed: %v", pk, garbageUID, err)
				} else {
					log.Printf("INFO: purged duplicate uppercase pubkey node %q (uid %s)", pk, garbageUID)
				}
			} else {
				// No lowercase duplicate — update the pubkey field in place.
				//
				// NOTE (HARD-02): The recovery txn and the hit/miss stamp txn
				// below are INDEPENDENT operations: recovery deliberately does NOT
				// stamp last_attempt/next_attempt so the corrected node re-enters
				// the fresh frontier and is crawled under its canonical pubkey.
				// MarkAttempted is retry-safe: recovery is idempotent (re-resolving
				// an already-corrected pubkey finds the lowercase form and takes the
				// duplicate/no-op path above), and stamping is an upsert.
				//
				// IMPORTANT: txn.Discard(ctx) is called INLINE after Mutate (not
				// via defer) because this branch runs inside a for-loop — a defer
				// inside a loop fires at function return, not at iteration end,
				// accumulating undiscarded txns until MarkAttempted returns (WR-02).
				txn := c.dg.NewTxn()
				mu := &api.Mutation{
					SetNquads: []byte(fmt.Sprintf("<%s> <pubkey> %q .\n", garbageUID, lower)),
					CommitNow: true,
				}
				_, mutErr := txn.Mutate(ctx, mu)
				txn.Discard(ctx) // always inline — closes both success and error paths
				if mutErr != nil {
					log.Printf("WARN: recover uppercase pubkey %q (uid %s) to %q failed: %v", pk, garbageUID, lower, mutErr)
				} else {
					log.Printf("INFO: recovered uppercase pubkey %q (uid %s) → %q", pk, garbageUID, lower)
				}
			}
		} else {
			// UNRECOVERABLE: short hex ("f1", "cbdc", "de"), relay-URL blobs,
			// or other garbage that is not valid hex even when lowercased.
			garbageUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{pk})
			if err != nil {
				log.Printf("WARN: purge unrecoverable pubkey %q — UID lookup failed: %v", pk, err)
				continue
			}
			uid, found := garbageUIDs[pk]
			if !found {
				// Not in Dgraph — nothing to delete.
				continue
			}
			if err := c.DeleteNodes(ctx, []string{uid}); err != nil {
				log.Printf("WARN: purge unrecoverable pubkey %q (uid %s) failed: %v", pk, uid, err)
			} else {
				log.Printf("INFO: purged unrecoverable pubkey %q (uid %s)", pk, uid)
			}
		}
	}
	if len(valid) == 0 {
		return nil
	}

	// Resolve UIDs and current miss_count in a single companion query so we
	// can apply the geometric backoff schedule per pubkey (D-04).
	nodeInfo, err := c.resolveUIDsWithMissCount(ctx, valid)
	if err != nil {
		return fmt.Errorf("resolve pubkeys for mark-attempted failed: %w", err)
	}

	var nquads strings.Builder
	for _, n := range nodeInfo {
		// Stamp last_attempt for all valid pubkeys (preserves existing aging
		// semantics and truthfulness of the field).
		nquads.WriteString(fmt.Sprintf("<%s> <last_attempt> \"%d\" .\n", n.UID, ts))

		if _, isHit := hits[n.Pubkey]; isHit {
			// HIT (D-03): pubkey returned a kind-3 event — reset backoff.
			nquads.WriteString(fmt.Sprintf("<%s> <next_attempt> \"%d\" .\n",
				n.UID, ts+int64(params.HitRefreshCadence.Seconds())))
			nquads.WriteString(fmt.Sprintf("<%s> <miss_count> \"0\" .\n", n.UID))
		} else {
			// MISS (D-04): no event returned — advance backoff geometrically.
			interval := BackoffInterval(n.MissCount, params.Base, params.Ratio, params.Cap)
			nquads.WriteString(fmt.Sprintf("<%s> <next_attempt> \"%d\" .\n",
				n.UID, ts+int64(interval.Seconds())))
			nquads.WriteString(fmt.Sprintf("<%s> <miss_count> \"%d\" .\n",
				n.UID, n.MissCount+1))
		}
	}
	if nquads.Len() == 0 {
		return nil
	}

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)
	mu := &api.Mutation{SetNquads: []byte(nquads.String()), CommitNow: true}
	if _, err := txn.Mutate(ctx, mu); err != nil {
		return fmt.Errorf("mark attempted failed: %w", err)
	}
	return nil
}

// pubkeyNode is a compact result row for resolveUIDsWithMissCount.
type pubkeyNode struct {
	UID       string
	Pubkey    string
	MissCount int
}

// resolveUIDsWithMissCount queries uid, pubkey, and miss_count for each
// given pubkey in a single read-only transaction. Pubkeys not found in
// Dgraph are omitted from the result.
func (c *Client) resolveUIDsWithMissCount(
	ctx context.Context,
	pubkeys []string,
) ([]pubkeyNode, error) {
	if len(pubkeys) == 0 {
		return nil, nil
	}

	quoted := make([]string, len(pubkeys))
	for i, pk := range pubkeys {
		quoted[i] = fmt.Sprintf("%q", pk)
	}
	query := fmt.Sprintf(`
	{
		nodes(func: eq(pubkey, [%s])) {
			uid
			pubkey
			miss_count
		}
	}`, strings.Join(quoted, ", "))

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("resolve pubkeys with miss_count failed: %w", err)
	}

	var result struct {
		Nodes []struct {
			UID       string `json:"uid"`
			Pubkey    string `json:"pubkey"`
			MissCount int    `json:"miss_count"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal pubkeys with miss_count failed: %w", err)
	}

	out := make([]pubkeyNode, len(result.Nodes))
	for i, n := range result.Nodes {
		out[i] = pubkeyNode{
			UID:       n.UID,
			Pubkey:    n.Pubkey,
			MissCount: n.MissCount,
		}
	}
	return out, nil
}

// BackfillNextAttempt seeds next_attempt and miss_count for existing attempted
// nodes that predate the Phase 8 PERF-02 predicates (D-06).
//
// For each node with last_attempt set but no next_attempt, it writes:
//
//	next_attempt = last_attempt + hitRefreshCadence (seconds)
//	miss_count   = 0
//
// The operation is paginated: it queries and commits in batchSize windows so
// that a large legacy frontier (>gRPC message cap) is backfilled incrementally
// without a single oversized query or mutation (HARD-01/WR-03). Because the
// @filter(NOT has(next_attempt)) predicate shrinks the result set as rows are
// stamped, the loop re-queries offset 0 each window — committed rows drop out
// of the filter, so the next offset:0 page is always the next un-stamped batch.
//
// The operation is idempotent: a second run finds zero candidates (nodes with
// next_attempt already set are excluded from the query). Returns the total count
// of nodes updated across all windows.
func (c *Client) BackfillNextAttempt(ctx context.Context, hitRefreshCadence int64) (int, error) {
	type nodeRow struct {
		UID         string `json:"uid"`
		LastAttempt int64  `json:"last_attempt"`
	}

	total := 0
	for {
		// Query one page of nodes that have last_attempt but no next_attempt.
		// Always offset:0 — each committed window removes its rows from the
		// filtered set, so the next iteration starts from a fresh offset:0 page.
		query := fmt.Sprintf(`
		{
			nodes(func: has(last_attempt), first: %d, offset: 0) @filter(NOT has(next_attempt)) {
				uid
				last_attempt
			}
		}`, batchSize)

		txn := c.dg.NewReadOnlyTxn()
		resp, err := txn.Query(ctx, query)
		txn.Discard(ctx) // inline discard — not deferred — so it fires every iteration (HARD-01)
		if err != nil {
			return total, fmt.Errorf("backfill query failed: %w", err)
		}

		var result struct {
			Nodes []nodeRow `json:"nodes"`
		}
		if err := json.Unmarshal(resp.Json, &result); err != nil {
			return total, fmt.Errorf("backfill unmarshal failed: %w", err)
		}
		if len(result.Nodes) == 0 {
			// No more un-stamped nodes — backfill complete.
			break
		}

		// Build nquads for this page: next_attempt = last_attempt + hitRefreshCadence,
		// miss_count = 0.
		var nquads strings.Builder
		for _, n := range result.Nodes {
			nquads.WriteString(fmt.Sprintf("<%s> <next_attempt> \"%d\" .\n",
				n.UID, n.LastAttempt+hitRefreshCadence))
			nquads.WriteString(fmt.Sprintf("<%s> <miss_count> \"0\" .\n", n.UID))
		}

		// Commit this page's stamps as a separate CommitNow mutation (per-window
		// discipline mirrors AddFollowers chunking — avoids unbounded mutation).
		muTxn := c.dg.NewTxn()
		mu := &api.Mutation{SetNquads: []byte(nquads.String()), CommitNow: true}
		_, mutErr := muTxn.Mutate(ctx, mu)
		muTxn.Discard(ctx) // inline discard — not deferred — so it fires every iteration
		if mutErr != nil {
			return total, fmt.Errorf("backfill mutation failed: %w", mutErr)
		}

		total += len(result.Nodes)
	}

	return total, nil
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

// CountStalePubkeys returns the total number of pubkeys that are eligible for
// crawling: frontier (never-attempted) plus aged-eligible (next_attempt in the
// past). This matches the selection semantics of GetStalePubkeys so that
// staleRemaining is honest (METRIC-01, D-16).
//
// Uses a single read-only transaction with two named count blocks.
func (c *Client) CountStalePubkeys(ctx context.Context) (int, error) {
	nowUnix := time.Now().Unix()
	query := fmt.Sprintf(`
	{
		frontier(func: has(pubkey)) @filter(NOT has(last_attempt)) {
			count(uid)
		}
		aged(func: has(next_attempt)) @filter(lt(next_attempt, %d)) {
			count(uid)
		}
	}`, nowUnix)

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("count stale pubkeys failed: %w", err)
	}

	var result struct {
		Frontier []struct {
			Count int `json:"count"`
		} `json:"frontier"`
		Aged []struct {
			Count int `json:"count"`
		} `json:"aged"`
	}

	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return 0, fmt.Errorf("unmarshal stale pubkey count failed: %w", err)
	}

	var frontierCount, agedCount int
	if len(result.Frontier) > 0 {
		frontierCount = result.Frontier[0].Count
	}
	if len(result.Aged) > 0 {
		agedCount = result.Aged[0].Count
	}
	return frontierCount + agedCount, nil
}

// GetKind3CreatedAt returns the kind3CreatedAt unix timestamp for the given
// pubkey. Returns 0 if the pubkey doesn't exist or has no kind3CreatedAt value.
func (c *Client) GetKind3CreatedAt(
	ctx context.Context,
	pubkey string,
) (int64, error) {
	query := `query GetKind3($pubkey: string) {
		pubkey_node(func: eq(pubkey, $pubkey), first: 1) {
			kind3CreatedAt
		}
	}`

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	req := &api.Request{
		Query: query,
		Vars:  map[string]string{"$pubkey": pubkey},
	}

	resp, err := txn.Do(ctx, req)
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

// PubkeyNode represents a Dgraph node with its pubkey metadata.
type PubkeyNode struct {
	UID            string `json:"uid"`
	Pubkey         string `json:"pubkey"`
	Kind3CreatedAt int64  `json:"kind3CreatedAt"`
	LastDBUpdate   int64  `json:"last_db_update"`
}

// GetAllPubkeysPaginated iterates over all pubkey nodes in batches,
// calling the callback with each batch of PubkeyNode results.
func (c *Client) GetAllPubkeysPaginated(
	ctx context.Context,
	batchSize int,
	callback func([]PubkeyNode) error,
) error {
	offset := 0

	for {
		query := fmt.Sprintf(`
		{
			nodes(func: has(pubkey), first: %d, offset: %d, orderasc: pubkey) {
				uid
				pubkey
				kind3CreatedAt
				last_db_update
			}
		}`, batchSize, offset)

		txn := c.dg.NewReadOnlyTxn()
		resp, err := txn.Query(ctx, query)
		txn.Discard(ctx)

		if err != nil {
			return fmt.Errorf("query all pubkeys failed: %w", err)
		}

		var result struct {
			Nodes []PubkeyNode `json:"nodes"`
		}

		if err := json.Unmarshal(resp.Json, &result); err != nil {
			return fmt.Errorf("unmarshal all pubkeys failed: %w", err)
		}

		if len(result.Nodes) == 0 {
			break
		}

		if err := callback(result.Nodes); err != nil {
			return fmt.Errorf("callback error: %w", err)
		}

		if len(result.Nodes) < batchSize {
			break
		}

		offset += batchSize
	}

	return nil
}

// DeleteNodes deletes multiple nodes by UID, removing all predicates and edges.
func (c *Client) DeleteNodes(ctx context.Context, uids []string) error {
	if len(uids) == 0 {
		return nil
	}

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)

	var nquads string
	for _, uid := range uids {
		nquads += fmt.Sprintf("<%s> * * .\n", uid)
	}

	mu := &api.Mutation{
		DelNquads: []byte(nquads),
	}
	if _, err := txn.Mutate(ctx, mu); err != nil {
		return fmt.Errorf("delete nodes failed: %w", err)
	}

	return txn.Commit(ctx)
}

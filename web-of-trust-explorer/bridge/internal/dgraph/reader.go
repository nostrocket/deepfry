// Package dgraph reads the whole DeepFry follow-graph from Dgraph over dgo
// gRPC, READ-ONLY, and produces the dense uint32 SoA representation the rest of
// the bridge consumes. It is a port + compose of the proven web-of-trust read
// pattern (pkg/dgraph/dgraph.go): NewReadOnlyTxn + after:-cursor paging +
// MaxCallRecvMsgSize(256MiB) + inline txn.Discard, with the hex->uint32 remap
// (a port of src/transport/GraphTransport.ts createHexRemap) appended.
//
// Data-separation rule (deepfry/CLAUDE.md): canonical events live only in
// StrFry; the bridge NEVER mutates Dgraph. There are deliberately zero
// write/schema calls in this package — only NewReadOnlyTxn() reads.
package dgraph

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// maxRecvMsgSize raises gRPC's default 4 MiB receive cap so a large follows
// page does not fail with ResourceExhausted (RESEARCH Pitfall 2 / WoT NewClient).
const maxRecvMsgSize = 256 << 20 // 256 MiB

// defaultPageSize is the server-controlled `first: N` window. The client cannot
// set this (threat T-01.1-03/DoS): the bridge owns paging, bounding per-page
// memory and gRPC response size.
const defaultPageSize = 50000

// GraphData is the dense SoA result of a full read. Edges are flat [src,tgt]
// uint32 pairs (length EdgeCount*2). The per-node attribute slices are all
// indexed by the dense node index assigned by the remap. Pubkeys are packed
// 32-byte binary (NOT 64-char hex) so the wire pubkey table is half the size
// (RESEARCH Pattern 3); a node with no decodable pubkey gets 32 zero bytes.
type GraphData struct {
	Edges          []uint32 // [src0,tgt0, src1,tgt1, ...]
	InDeg          []uint32 // filled by prep.Degrees (followers)
	OutDeg         []uint32 // filled by prep.Degrees (follows)
	Community      []uint32 // filled by prep.Louvain
	Kind3CreatedAt []int32  // unix seconds, i32 (Assumption A3)
	LastDbUpdate   []int32  // unix seconds, i32
	Pubkeys        []byte   // packed 32 bytes per node index (length NodeCount*32)
	NodeCount      uint32
	EdgeCount      uint32
}

// remap is a dense, stable, collision-free hex->uint32 map. First sighting of a
// hex uid assigns the next dense index (0,1,2,...); repeats return the same
// index. Identical semantics to src/transport/GraphTransport.ts createHexRemap,
// ported to Go (RESEARCH Pattern 2). We remap on the Dgraph uid for edge
// compactness and ship a parallel index->pubkey table for the tooltip (D-04).
type remap struct {
	m   map[string]uint32
	hex []string
}

func newRemap() *remap {
	return &remap{m: make(map[string]uint32)}
}

// indexOf returns the dense index for a hex uid, assigning a new one on first
// sighting.
func (r *remap) indexOf(h string) uint32 {
	if i, ok := r.m[h]; ok {
		return i
	}
	i := uint32(len(r.m))
	r.m[h] = i
	r.hex = append(r.hex, h)
	return i
}

// size is the number of distinct uids seen so far.
func (r *remap) size() uint32 { return uint32(len(r.m)) }

// dqlNode is the slice of the DQL response we read per page.
type dqlNode struct {
	UID            string      `json:"uid"`
	Follows        []dqlFollow `json:"follows"`
	Pubkey         string      `json:"pubkey"`
	Kind3CreatedAt *int64      `json:"kind3CreatedAt"`
	LastDbUpdate   *int64      `json:"last_db_update"`
}

type dqlFollow struct {
	UID string `json:"uid"`
}

// Reader holds the read-only Dgraph gRPC client.
type Reader struct {
	dg   *dgo.Dgraph
	conn *grpc.ClientConn
}

// NewReader connects to the Dgraph gRPC address (e.g. "localhost:9080") with an
// insecure transport (local, no auth — matches web-of-trust) and the raised
// receive cap.
func NewReader(addr string) (*Reader, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial dgraph %q: %w", addr, err)
	}
	return &Reader{
		dg:   dgo.NewDgraphClient(api.NewDgraphClient(conn)),
		conn: conn,
	}, nil
}

// Close closes the gRPC connection.
func (r *Reader) Close() error {
	if r.conn == nil {
		return nil
	}
	return r.conn.Close()
}

// nodeAttrs accumulates per-node attributes keyed by dense index while paging.
// We append by index as nodes are first seen; an edge target seen before its
// own source page still gets a slot via the remap, so we resize lazily.
type nodeAttrs struct {
	kind3 map[uint32]int32
	lastU map[uint32]int32
	pk    map[uint32][32]byte
}

func newNodeAttrs() *nodeAttrs {
	return &nodeAttrs{
		kind3: make(map[uint32]int32),
		lastU: make(map[uint32]int32),
		pk:    make(map[uint32][32]byte),
	}
}

// ReadAll pages the whole follows graph READ-ONLY via after:-cursor paging
// (NEVER offset — RESEARCH Pitfall / WoT measured offset ~100 nodes/min),
// remaps hex uid->dense uint32, captures each node's packed 32-byte pubkey and
// the two timestamps, and returns the dense GraphData. Degrees/community are
// left zero here; main fills them via the prep package.
//
// txn.Discard is called INLINE every iteration (never deferred inside the loop —
// RESEARCH Pitfall 6 / WoT HARD-01), so read-only transactions do not leak
// across pages.
func (r *Reader) ReadAll(ctx context.Context) (*GraphData, error) {
	rm := newRemap()
	attrs := newNodeAttrs()
	var edges []uint32

	cursor := "0x0" // uid cursor; `after: 0x0` starts before the first node.
	for {
		// Only nodes with a `follows` predicate are edge sources. Leaf nodes
		// (followed but following no one) are still discovered as edge
		// *targets* via the remap when they appear inside follows{uid}.
		query := fmt.Sprintf(`{
			q(func: has(follows), first: %d, after: %s) {
				uid
				pubkey
				kind3CreatedAt
				last_db_update
				follows { uid }
			}
		}`, defaultPageSize, cursor)

		txn := r.dg.NewReadOnlyTxn()
		resp, err := txn.Query(ctx, query)
		txn.Discard(ctx) // inline discard — fires every iteration (HARD-01)
		if err != nil {
			return nil, fmt.Errorf("read-all query (cursor %s) failed: %w", cursor, err)
		}

		var page struct {
			Q []dqlNode `json:"q"`
		}
		if err := json.Unmarshal(resp.Json, &page); err != nil {
			return nil, fmt.Errorf("read-all unmarshal failed: %w", err)
		}
		if len(page.Q) == 0 {
			break
		}

		for i := range page.Q {
			n := &page.Q[i]
			src := rm.indexOf(n.UID)
			recordAttrs(attrs, src, n)
			for _, f := range n.Follows {
				tgt := rm.indexOf(f.UID)
				edges = append(edges, src, tgt)
			}
		}

		// Advance the cursor to the last uid of this page.
		cursor = page.Q[len(page.Q)-1].UID

		// A short page means the has(follows) set is exhausted.
		if len(page.Q) < defaultPageSize {
			break
		}
	}

	nodeCount := rm.size()
	edgeCount := uint32(len(edges) / 2)

	gd := &GraphData{
		Edges:          edges,
		InDeg:          make([]uint32, nodeCount),
		OutDeg:         make([]uint32, nodeCount),
		Community:      make([]uint32, nodeCount),
		Kind3CreatedAt: make([]int32, nodeCount),
		LastDbUpdate:   make([]int32, nodeCount),
		Pubkeys:        make([]byte, int(nodeCount)*32),
		NodeCount:      nodeCount,
		EdgeCount:      edgeCount,
	}

	// Flatten the lazily-keyed attribute maps into the dense slices. Targets
	// discovered only as follows{uid} (no own page row) keep zero attrs.
	for idx, v := range attrs.kind3 {
		gd.Kind3CreatedAt[idx] = v
	}
	for idx, v := range attrs.lastU {
		gd.LastDbUpdate[idx] = v
	}
	for idx, pk := range attrs.pk {
		copy(gd.Pubkeys[int(idx)*32:int(idx)*32+32], pk[:])
	}

	return gd, nil
}

// recordAttrs stores a node's pubkey (decoded to 32 packed bytes) and
// timestamps at its dense index. A 64-char hex pubkey decodes to exactly 32
// bytes; anything that fails to decode leaves 32 zero bytes (no panic).
func recordAttrs(attrs *nodeAttrs, idx uint32, n *dqlNode) {
	if n.Kind3CreatedAt != nil {
		attrs.kind3[idx] = clampToI32(*n.Kind3CreatedAt)
	}
	if n.LastDbUpdate != nil {
		attrs.lastU[idx] = clampToI32(*n.LastDbUpdate)
	}
	if len(n.Pubkey) == 64 {
		var b [32]byte
		if _, err := hex.Decode(b[:], []byte(n.Pubkey)); err == nil {
			attrs.pk[idx] = b
		}
	}
}

// clampToI32 narrows a unix-second timestamp to int32 (Assumption A3: fits
// until 2038 for this dev tool), saturating rather than wrapping on overflow.
func clampToI32(v int64) int32 {
	const maxI32 = int64(^uint32(0) >> 1) // 2147483647
	const minI32 = -maxI32 - 1
	if v > maxI32 {
		return int32(maxI32)
	}
	if v < minI32 {
		return int32(minI32)
	}
	return int32(v)
}

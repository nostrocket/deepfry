// Package dgraph is the ONLY tier in spam-explorer that talks to the wire. It
// holds a minimal, read-only dgo/v210 gRPC client (D-06): connect + read-only
// query helpers only — no Alter, no mutation, no schema. Seed resolution and
// frontier expansion (the Phase 2 pagination seam) live alongside the client so
// all Dgraph I/O is isolated here; internal/bfs, internal/score, internal/output
// stay pure.
package dgraph

import (
	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps a read-only dgo.Dgraph over a single gRPC connection. It exposes
// only NewClient/Close plus the read-query helpers (ResolveSeed, ExpandFrontier).
// There is deliberately NO mutation/Alter/schema path (D-06, threat T-01-04).
type Client struct {
	dg   *dgo.Dgraph
	conn *grpc.ClientConn
}

// NewClient dials the Dgraph gRPC endpoint (e.g. "localhost:9080") and returns a
// read-only client. grpc.NewClient is non-blocking — it establishes the channel
// lazily on the first RPC — so this returns immediately without a live server.
//
// The raised MaxCallRecvMsgSize is LOAD-BEARING: a single frontier response can
// carry many thousands of follows edges and exceed gRPC's default 4 MiB receive
// cap, failing with ResourceExhausted. 256 MiB matches the web-of-trust sibling
// (Pitfall 1, threat T-01-SC headroom).
func NewClient(addr string) (*Client, error) {
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

// Close closes the underlying gRPC connection. Call with defer.
func (c *Client) Close() error {
	return c.conn.Close()
}

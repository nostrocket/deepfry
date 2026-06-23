// Command bridge is the standalone WoT-Explorer Go bridge (D-01/D-02). It reads
// the whole DeepFry follow-graph from Dgraph over dgo gRPC (READ-ONLY), runs the
// server-side data-prep pass (in/out-degree, Louvain community IDs), and serves
// the result as a little-endian binary frame over HTTP chunked transfer at
// GET /graph.bin — removing the browser JSON.parse wall that FAILED the Phase 1
// verdict (PERF-01).
//
// The HTTP server and wire encoding are wired in Task 3; this entrypoint owns
// flag parsing and the Dgraph read.
package main

import (
	"context"
	"flag"
	"log"

	"wot-explorer-bridge/internal/dgraph"
)

func main() {
	dgraphAddr := flag.String("dgraph", "localhost:9080", "Dgraph gRPC address")
	// Bind loopback only (threat T-01.1-01/Information Disclosure): never
	// 0.0.0.0, which would expose the Dgraph read-all on the LAN.
	listenAddr := flag.String("listen", "127.0.0.1:8081", "HTTP listen address (loopback only)")
	flag.Parse()

	_ = *listenAddr // HTTP server is wired in Task 3.

	reader, err := dgraph.NewReader(*dgraphAddr)
	if err != nil {
		log.Fatalf("connect dgraph: %v", err)
	}
	defer reader.Close()

	ctx := context.Background()
	gd, err := reader.ReadAll(ctx)
	if err != nil {
		log.Fatalf("read-all: %v", err)
	}
	log.Printf("read graph: %d nodes, %d edges", gd.NodeCount, gd.EdgeCount)
}

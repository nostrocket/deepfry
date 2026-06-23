// Command bridge is the standalone WoT-Explorer Go bridge (D-01/D-02). It reads
// the whole DeepFry follow-graph from Dgraph over dgo gRPC (READ-ONLY), runs the
// server-side data-prep pass (in/out-degree, Louvain community IDs), and serves
// the result as a little-endian binary frame over HTTP chunked transfer at
// GET /graph.bin — removing the browser JSON.parse wall that FAILED the Phase 1
// verdict (PERF-01).
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"wot-explorer-bridge/internal/dgraph"
	"wot-explorer-bridge/internal/prep"
	"wot-explorer-bridge/internal/wire"
)

func main() {
	dgraphAddr := flag.String("dgraph", "localhost:9080", "Dgraph gRPC address")
	// Bind loopback only (threat T-01.1-01/Information Disclosure): never
	// 0.0.0.0, which would expose the Dgraph read-all on the LAN.
	listenAddr := flag.String("listen", "127.0.0.1:8081", "HTTP listen address (loopback only)")
	flag.Parse()

	reader, err := dgraph.NewReader(*dgraphAddr)
	if err != nil {
		log.Fatalf("connect dgraph: %v", err)
	}
	defer reader.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/graph.bin", graphHandler(reader))

	log.Printf("bridge listening on http://%s/graph.bin (dgraph %s)", *listenAddr, *dgraphAddr)
	srv := &http.Server{
		Addr:    *listenAddr,
		Handler: mux,
		// HTTP/1.1 (ListenAndServe default) so Transfer-Encoding: chunked is
		// applied when no Content-Length is set (RESEARCH Pattern 4).
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("http server: %v", err)
	}
}

// graphHandler reads the whole graph, runs the prep pass, and streams the
// encoded binary frame section-by-section with a Flush after each so the
// browser's byte counter advances visibly (D-09).
//
// Server-side memory note (Claude's-discretion item): the GraphData (remap-built
// edge list + attribute arrays) is held only for the duration of this request;
// it goes out of scope and is GC'd once the response returns.
func graphHandler(reader *dgraph.Reader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		gd, err := reader.ReadAll(r.Context())
		if err != nil {
			log.Printf("read-all: %v", err)
			http.Error(w, "read failed", http.StatusBadGateway)
			return
		}

		// Server-side data-prep pass over the dense edge list.
		gd.InDeg, gd.OutDeg = prep.Degrees(gd.Edges, gd.NodeCount)
		gd.Community = prep.Louvain(gd.Edges, gd.NodeCount)

		w.Header().Set("Content-Type", "application/octet-stream")
		// REQUIRED — the COEP require-corp Vite page silently blocks the fetch
		// without this (RESEARCH Pitfall 3).
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		// CORS for the Vite dev origin; `*` acceptable for a localhost dev tool.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		// Deliberately NO Content-Length → Go auto-applies chunked transfer.

		flusher, _ := w.(http.Flusher)
		for _, s := range wire.Sections(gd) {
			if _, err := w.Write(s.Bytes); err != nil {
				log.Printf("write section %s: %v", s.Name, err)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		log.Printf("served graph.bin: %d nodes, %d edges", gd.NodeCount, gd.EdgeCount)
	}
}

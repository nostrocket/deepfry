// Command clusterscan finds suspected spam clusters in the Web-of-Trust graph
// by graph shape alone. It computes a trusted set by propagating trust out from
// seed pubkeys (a node joins once K trusted accounts follow it), then reports
// "weak bridges": non-trusted accounts that touch the trusted set through only a
// few edges yet have a large cluster of non-trusted accounts hanging beneath
// them. It is strictly read-only and writes a timestamped CSV + JSON report to
// the working directory.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/dgraph"
)

// bridgeReport is one ranked row of the output.
type bridgeReport struct {
	Pubkey           string               `json:"pubkey"`
	Weight           int                  `json:"weight"`
	TrustedFollowers int                  `json:"trusted_followers"`
	TrustedFollowees int                  `json:"trusted_followees"`
	ClusterSize      int                  `json:"cluster_size"`
	Score            float64              `json:"score"`
	Kind3CreatedAt   int64                `json:"kind3_created_at"`
	Members          []dgraph.ClusterNode `json:"members,omitempty"`
}

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	k := flag.Int("k", cfg.TrustK, "endorsements from the trusted set required to join it")
	depth := flag.Int("depth", cfg.ClusterDepth, "follows-hops to walk when sizing a cluster")
	maxWeight := flag.Int("max-bridge-weight", cfg.MaxBridgeWeight, "max edges crossing into trusted for a node to count as a weak bridge")
	minCluster := flag.Int("min-cluster-size", cfg.MinClusterSize, "ignore bridges whose cluster is smaller than this")
	bridgeLimit := flag.Int("bridge-limit", 10000, "max weak bridges to fetch (truncation is reported, not silent)")
	outDir := flag.String("out", ".", "directory to write the report files into")
	withMembers := flag.Bool("members", false, "include per-cluster member pubkeys in the JSON report")
	stats := flag.Bool("stats", false, "after building the trusted set, print its follow-count distribution and exit (calibration)")
	flag.Parse()

	ctx := context.Background()

	client, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create Dgraph client: %v", err)
	}
	defer client.Close()

	// --- Phase 0: resolve seed pubkeys to UIDs ---
	seedUIDs, err := client.ResolvePubkeysToUIDs(ctx, cfg.SeedPubkeys)
	if err != nil {
		log.Fatalf("Failed to resolve seed pubkeys: %v", err)
	}
	if len(seedUIDs) == 0 {
		log.Fatalf("None of the %d configured seed pubkeys exist in the graph; cannot anchor trust", len(cfg.SeedPubkeys))
	}
	if len(seedUIDs) < len(cfg.SeedPubkeys) {
		log.Printf("WARNING: only %d of %d seed pubkeys found in the graph", len(seedUIDs), len(cfg.SeedPubkeys))
	}

	// trusted maps UID -> struct{} for fast membership tests.
	trusted := make(map[string]struct{}, len(seedUIDs))
	for _, uid := range seedUIDs {
		trusted[uid] = struct{}{}
	}
	log.Printf("Seeded trusted set with %d pubkeys (K=%d)", len(trusted), *k)

	// --- Phase 1: trust closure ---
	for round := 1; ; round++ {
		newUIDs, err := client.ExpandTrustedSet(ctx, keysOf(trusted), *k)
		if err != nil {
			log.Fatalf("Trust propagation round %d failed: %v", round, err)
		}
		added := 0
		for _, uid := range newUIDs {
			if _, ok := trusted[uid]; !ok {
				trusted[uid] = struct{}{}
				added++
			}
		}
		log.Printf("Round %d: +%d trusted (total %d)", round, added, len(trusted))
		if added == 0 {
			break
		}
	}

	// --- Calibration: report the trusted set's degree distribution and stop ---
	if *stats {
		if err := printTrustedStats(ctx, client, keysOf(trusted)); err != nil {
			log.Fatalf("Failed to compute stats: %v", err)
		}
		return
	}

	// --- Phase 2: weak bridges ---
	bridges, truncated, err := client.GetWeakBridges(ctx, keysOf(trusted), *maxWeight, *bridgeLimit)
	if err != nil {
		log.Fatalf("Failed to fetch weak bridges: %v", err)
	}
	if truncated {
		log.Printf("WARNING: weak-bridge results truncated at the --bridge-limit of %d; some bridges are not reported", *bridgeLimit)
	}
	log.Printf("Found %d weak bridges (weight 1..%d)", len(bridges), *maxWeight)

	// --- Phase 3: size the cluster beneath each bridge ---
	reports := make([]bridgeReport, 0, len(bridges))
	for _, b := range bridges {
		members, err := client.ClusterBeneath(ctx, b.UID, *depth)
		if err != nil {
			log.Printf("WARNING: skipping bridge %s: %v", b.Pubkey, err)
			continue
		}

		// Keep only non-trusted members; a trusted node beneath the bridge is
		// part of the main graph, not the suspected cluster.
		cluster := members[:0:0]
		for _, m := range members {
			if _, ok := trusted[m.UID]; !ok {
				cluster = append(cluster, m)
			}
		}
		if len(cluster) < *minCluster {
			continue
		}

		r := bridgeReport{
			Pubkey:           b.Pubkey,
			Weight:           b.Weight,
			TrustedFollowers: b.TrustedFollowers,
			TrustedFollowees: b.TrustedFollowees,
			ClusterSize:      len(cluster),
			Score:            float64(len(cluster)) / float64(b.Weight),
			Kind3CreatedAt:   b.Kind3CreatedAt,
		}
		if *withMembers {
			r.Members = cluster
		}
		reports = append(reports, r)
	}

	// Strongest signal first: a large cluster reached through the thinnest link.
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Score != reports[j].Score {
			return reports[i].Score > reports[j].Score
		}
		return reports[i].ClusterSize > reports[j].ClusterSize
	})

	if err := writeReports(*outDir, reports); err != nil {
		log.Fatalf("Failed to write report: %v", err)
	}
	log.Printf("Done: %d suspected spam clusters (>= %d members)", len(reports), *minCluster)
}

// printTrustedStats fetches the follows/followers counts for the whole trusted
// set (in batches) and prints the distribution. follows==0 nodes are reported
// separately because they are dominated by un-crawled accounts, not real
// behaviour, so they must not be read as a low-degree signal.
func printTrustedStats(ctx context.Context, client *dgraph.Client, uids []string) error {
	const batch = 5000
	follows := make([]int, 0, len(uids))
	followers := make([]int, 0, len(uids))
	for i := 0; i < len(uids); i += batch {
		end := i + batch
		if end > len(uids) {
			end = len(uids)
		}
		degs, err := client.DegreesForUIDs(ctx, uids[i:end])
		if err != nil {
			return err
		}
		for _, d := range degs {
			follows = append(follows, d.Follows)
			followers = append(followers, d.Followers)
		}
	}

	crawled := make([]int, 0, len(follows))
	for _, f := range follows {
		if f > 0 {
			crawled = append(crawled, f)
		}
	}

	log.Printf("Trusted set: %d nodes", len(follows))
	log.Printf("  with crawled follow-list (follows>0): %d (%.1f%%)  follows==0: %d",
		len(crawled), 100*float64(len(crawled))/float64(len(follows)), len(follows)-len(crawled))
	printDist("follows  (all)    ", follows)
	printDist("follows  (crawled)", crawled)
	printDist("followers(all)    ", followers)
	return nil
}

// printDist prints mean / median / percentile summary of an integer sample.
func printDist(label string, xs []int) {
	if len(xs) == 0 {
		log.Printf("  %s: (empty)", label)
		return
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	sum := 0
	for _, v := range s {
		sum += v
	}
	pct := func(p float64) int { return s[int(float64(len(s)-1)*p/100)] }
	log.Printf("  %s n=%-6d mean %-7.1f median %-5d p25 %-5d p75 %-5d p90 %-6d p99 %-6d max %d",
		label, len(s), float64(sum)/float64(len(s)), pct(50), pct(25), pct(75), pct(90), pct(99), s[len(s)-1])
}

// keysOf returns the keys of a UID set as a slice.
func keysOf(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// writeReports emits a ranked CSV summary and a JSON file (which carries the
// optional member lists) into dir, both with a shared timestamp.
func writeReports(dir string, reports []bridgeReport) error {
	timestamp := time.Now().Format("20060102_150405")

	csvPath := filepath.Join(dir, fmt.Sprintf("spam_clusters_%s.csv", timestamp))
	file, err := os.Create(csvPath)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer file.Close()

	w := csv.NewWriter(file)
	header := []string{"rank", "bridge_pubkey", "weight", "trusted_followers", "trusted_followees", "cluster_size", "score", "kind3_created_at"}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for i, r := range reports {
		row := []string{
			strconv.Itoa(i + 1),
			r.Pubkey,
			strconv.Itoa(r.Weight),
			strconv.Itoa(r.TrustedFollowers),
			strconv.Itoa(r.TrustedFollowees),
			strconv.Itoa(r.ClusterSize),
			strconv.FormatFloat(r.Score, 'f', 2, 64),
			strconv.FormatInt(r.Kind3CreatedAt, 10),
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}

	jsonPath := filepath.Join(dir, fmt.Sprintf("spam_clusters_%s.json", timestamp))
	jf, err := os.Create(jsonPath)
	if err != nil {
		return fmt.Errorf("create json: %w", err)
	}
	defer jf.Close()
	enc := json.NewEncoder(jf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(reports); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}

	absCSV, _ := filepath.Abs(csvPath)
	absJSON, _ := filepath.Abs(jsonPath)
	log.Printf("Report written:\n  %s\n  %s", absCSV, absJSON)
	return nil
}

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"web-of-trust/pkg/dgraph"
)

var validPubkey = regexp.MustCompile(`^[0-9a-f]{64}$`)

func main() {
	dgraphAddr := flag.String("dgraph-addr", "localhost:9080", "Dgraph gRPC address")
	purge := flag.Bool("purge", false, "Delete invalid and duplicate pubkey nodes")
	verbose := flag.Bool("v", false, "Print details of each bad entry")
	flag.Parse()

	ctx := context.Background()

	client, err := dgraph.NewClient(*dgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create Dgraph client: %v", err)
	}
	defer client.Close()

	totalPubkeys, err := client.CountPubkeys(ctx)
	if err != nil {
		log.Fatalf("Failed to count pubkeys: %v", err)
	}

	fmt.Println("=== Web of Trust Health Check ===")
	fmt.Printf("Dgraph: %s\n", *dgraphAddr)
	fmt.Printf("Total pubkeys: %d\n", totalPubkeys)

	// Single pass: collect invalid and duplicate pubkeys
	var invalidNodes []dgraph.PubkeyNode
	seen := map[string][]dgraph.PubkeyNode{}
	scanned := 0

	err = client.GetAllPubkeysPaginated(ctx, 5000, func(batch []dgraph.PubkeyNode) error {
		for _, node := range batch {
			if !validPubkey.MatchString(node.Pubkey) {
				invalidNodes = append(invalidNodes, node)
			}
			seen[node.Pubkey] = append(seen[node.Pubkey], node)
		}
		scanned += len(batch)
		fmt.Printf("\rScanning... %d pubkeys", scanned)
		return nil
	})
	fmt.Println()
	if err != nil {
		log.Fatalf("Failed to scan pubkeys: %v", err)
	}

	// Find duplicate groups
	type duplicateGroup struct {
		pubkey string
		nodes  []dgraph.PubkeyNode
	}
	var duplicates []duplicateGroup
	extraDuplicateNodes := 0
	for pubkey, nodes := range seen {
		if len(nodes) > 1 {
			duplicates = append(duplicates, duplicateGroup{pubkey: pubkey, nodes: nodes})
			extraDuplicateNodes += len(nodes) - 1
		}
	}

	// Report invalid pubkeys
	fmt.Println("\n--- Invalid Pubkeys ---")
	if len(invalidNodes) == 0 {
		fmt.Println("None found")
	} else {
		fmt.Printf("Found %d invalid pubkeys\n", len(invalidNodes))
		if *verbose {
			for _, node := range invalidNodes {
				reason := describeInvalid(node.Pubkey)
				fmt.Printf("  UID %-10s pubkey=%-20q %s\n", node.UID, truncate(node.Pubkey, 20), reason)
			}
		}
	}

	// Report duplicates
	fmt.Println("\n--- Duplicate Pubkeys ---")
	if len(duplicates) == 0 {
		fmt.Println("None found")
	} else {
		fmt.Printf("Found %d duplicate groups (%d extra nodes)\n", len(duplicates), extraDuplicateNodes)
		if *verbose {
			for _, group := range duplicates {
				fmt.Printf("  pubkey: %s\n", truncate(group.pubkey, 20))
				ranked := rankNodes(group.nodes)
				for i, node := range ranked {
					marker := "[DELETE]"
					if i == 0 {
						marker = "[KEEP]"
					}
					fmt.Printf("    UID %-10s kind3CreatedAt=%-12d last_db_update=%-12d %s\n",
						node.UID, node.Kind3CreatedAt, node.LastDBUpdate, marker)
				}
			}
		}
	}

	// Summary
	totalToPurge := len(invalidNodes) + extraDuplicateNodes
	fmt.Println("\n=== Summary ===")
	fmt.Printf("Invalid:    %d nodes\n", len(invalidNodes))
	fmt.Printf("Duplicates: %d extra nodes (%d groups)\n", extraDuplicateNodes, len(duplicates))
	fmt.Printf("Total:      %d nodes to purge\n", totalToPurge)

	if totalToPurge == 0 {
		fmt.Println("\nDatabase is clean.")
		return
	}

	if !*purge {
		fmt.Println("\nUse -purge to delete these entries.")
		return
	}

	// Collect UIDs to delete
	var uidsToDelete []string
	for _, node := range invalidNodes {
		uidsToDelete = append(uidsToDelete, node.UID)
	}
	for _, group := range duplicates {
		ranked := rankNodes(group.nodes)
		for _, node := range ranked[1:] { // skip the keeper
			uidsToDelete = append(uidsToDelete, node.UID)
		}
	}

	fmt.Printf("\nWill delete %d nodes. Stop the crawler before purging to avoid race conditions.\n", len(uidsToDelete))
	fmt.Print("Continue? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(input)) != "y" {
		fmt.Println("Aborted.")
		return
	}

	if err := client.DeleteNodes(ctx, uidsToDelete); err != nil {
		log.Fatalf("Failed to delete nodes: %v", err)
	}
	fmt.Printf("Deleted %d nodes.\n", len(uidsToDelete))
}

// rankNodes sorts nodes so the best candidate to keep is first.
// Priority: highest kind3CreatedAt, then highest last_db_update, then lowest UID.
func rankNodes(nodes []dgraph.PubkeyNode) []dgraph.PubkeyNode {
	ranked := make([]dgraph.PubkeyNode, len(nodes))
	copy(ranked, nodes)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Kind3CreatedAt != ranked[j].Kind3CreatedAt {
			return ranked[i].Kind3CreatedAt > ranked[j].Kind3CreatedAt
		}
		if ranked[i].LastDBUpdate != ranked[j].LastDBUpdate {
			return ranked[i].LastDBUpdate > ranked[j].LastDBUpdate
		}
		return ranked[i].UID < ranked[j].UID
	})
	return ranked
}

func describeInvalid(pubkey string) string {
	if pubkey == "" {
		return "(empty)"
	}
	if len(pubkey) != 64 {
		return fmt.Sprintf("(length %d)", len(pubkey))
	}
	return "(non-hex characters)"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

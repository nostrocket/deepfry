package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// GraphQLRepository queries Dgraph's GraphQL endpoint to retrieve whitelisted pubkeys.
// It fetches all Profile pubkeys from Dgraph, merges them with hardcoded keys,
// deduplicates, and returns the combined list.
type GraphQLRepository struct {
	endpoint   string
	httpClient *http.Client
	pageSize   int
	logger     *log.Logger
}

// NewGraphQLRepository creates a new GraphQLRepository.
// Dgraph endpoint can be configured via DGRAPH_GRAPHQL_URL environment variable.
// Defaults to http://dgraph:8080/graphql
func NewGraphQLRepository(endpoint string, pageSize int, logger *log.Logger) *GraphQLRepository {
	// Configure HTTP transport for connection reuse and pooling
	transport := &http.Transport{
		MaxIdleConns:        10,               // Max idle connections across all hosts
		MaxIdleConnsPerHost: 2,                // Max idle connections per host
		IdleConnTimeout:     90 * time.Second, // Keep connections alive longer
		DisableCompression:  false,            // Enable compression for smaller payloads
	}

	return &GraphQLRepository{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		pageSize: pageSize,
		logger:   logger,
	}
}

// GetAll retrieves all whitelisted pubkeys from Dgraph and merges with hardcoded keys.
// Returns deduplicated list of pubkeys as [32]byte arrays.
func (r *GraphQLRepository) GetAll() ([][32]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Fetch all pubkeys from Dgraph
	dgraphPubkeys, err := r.fetchAllPubkeysFromDgraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pubkeys from Dgraph: %w", err)
	}

	// Get hardcoded pubkeys for known forwarders and admins
	hardcodedPubkeys := getHardcodedPubkeys()

	// Merge and deduplicate
	allPubkeys := mergePubkeys(dgraphPubkeys, hardcodedPubkeys)

	// Convert to [32]byte arrays with pre-allocated capacity
	keys := make([][32]byte, 0, len(allPubkeys))
	for i, hexStr := range allPubkeys {
		k, err := hexTo32ByteArray(hexStr)
		if err != nil {
			r.logger.Printf("invalid pubkey from Dgraph or hardcoded list: %d %d %s", len(allPubkeys), i, hexStr)
			continue
		}
		keys = append(keys, k)
	}

	return keys, nil
}

// fetchAllPubkeysFromDgraph paginates through all Profile records in Dgraph
// and returns their pubkeys as hex strings.
func (r *GraphQLRepository) fetchAllPubkeysFromDgraph(ctx context.Context) ([]string, error) {
	// Pre-allocate with estimated capacity to reduce reallocations
	// Start with 2x pageSize as a reasonable minimum
	allPubkeys := make([]string, 0, r.pageSize*2)
	offset := 0

	for {
		// Context cancellation is checked automatically by http.Request
		// No need for explicit select statement here

		// Fetch page
		pubkeys, err := r.fetchPubkeysPage(ctx, offset, r.pageSize)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch page at offset %d: %w", offset, err)
		}

		// No more results
		if len(pubkeys) == 0 {
			break
		}

		// log a message to say how many pubkeys were fetched in this page
		r.logger.Printf("Fetched %d pubkeys from Dgraph at offset %d\n", len(pubkeys), offset)

		allPubkeys = append(allPubkeys, pubkeys...)

		// If we got fewer results than page size, we're done
		if len(pubkeys) < r.pageSize {
			break
		}

		offset += r.pageSize
	}

	return allPubkeys, nil
}

// fetchPubkeysPage fetches a single page of pubkeys from Dgraph GraphQL endpoint.
func (r *GraphQLRepository) fetchPubkeysPage(ctx context.Context, offset, limit int) ([]string, error) {
	// GraphQL query (constant, could be moved to package level for micro-optimization)
	query := `
		query QueryProfiles($offset: Int!, $first: Int!) {
			queryProfile(offset: $offset, first: $first) {
				pubkey
			}
		}
	`

	// Build request body using structs to avoid map allocations
	reqBody := struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}{
		Query: query,
		Variables: map[string]interface{}{
			"offset": offset,
			"first":  limit,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body once
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code after reading (better error messages)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data struct {
			QueryProfile []struct {
				Pubkey string `json:"pubkey"`
			} `json:"queryProfile"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Check for GraphQL errors
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", response.Errors[0].Message)
	}

	// Extract pubkeys
	pubkeys := make([]string, 0, len(response.Data.QueryProfile))
	for _, profile := range response.Data.QueryProfile {
		if profile.Pubkey != "" {
			pubkeys = append(pubkeys, profile.Pubkey)
		}
	}

	return pubkeys, nil
}

// getHardcodedPubkeys returns a list of hardcoded pubkeys for known forwarders and admins.
// These are always included in the whitelist regardless of Dgraph state.
func getHardcodedPubkeys() []string {
	return []string{
		"f6b07746e51d757fce1a030ef6fbe5dae6805df857f26ddce4e414bc3f983c4d", // live event forwarder
		"de6a2fe67d4407511f23d5d8f8dbfd29967b9a345cfed912fdfedf7fbabf570d", // history forwarder
		"d91191e30e00444b942c0e82cad470b32af171764c2275bee0bd99377efd4075", // gsov
		"a0dda882fb89732b04793a2c989435fcd89ee559e81291074450edbd9b15621b", // rocketdog8
		"ba1838441e720ee91360d38321a19cbf8596e6540cfa045c9c5d429f1a2b9e3a", // macro88
	}
}

// mergePubkeys merges two slices of pubkeys and deduplicates them.
// Optimized to pre-size the map based on expected total keys.
func mergePubkeys(dgraphKeys, hardcodedKeys []string) []string {
	// Pre-size map with expected total to reduce rehashing
	expectedSize := len(dgraphKeys) + len(hardcodedKeys)
	seen := make(map[string]struct{}, expectedSize)
	result := make([]string, 0, expectedSize)

	// Add dgraph keys
	for _, key := range dgraphKeys {
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}

	// Add hardcoded keys
	for _, key := range hardcodedKeys {
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}

	return result
}

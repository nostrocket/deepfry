package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// GraphQLRepository retrieves whitelisted pubkeys from Dgraph. It fetches all
// Profile pubkeys, merges them with hardcoded keys, deduplicates, and returns
// the combined list.
//
// Pagination uses Dgraph's DQL endpoint (/query) with uid-cursor pagination
// (func: type(Profile), after: <lastUID>) rather than GraphQL offset pagination.
// Offset pagination is O(n^2) over a large, ordered result set — at ~1.5M
// profiles deep pages take minutes and the full load never completes within
// queryTimeout. The uid cursor seeks directly, so every page is ~constant time.
type GraphQLRepository struct {
	endpoint     string // GraphQL endpoint (kept for reference/config parity)
	dqlEndpoint  string // DQL /query endpoint, derived from endpoint
	httpClient   *http.Client
	pageSize     int
	queryTimeout time.Duration
	logger       *log.Logger
}

// NewGraphQLRepository creates a new GraphQLRepository. The endpoint is the
// Dgraph GraphQL URL (e.g. http://host:8080/graphql); the DQL /query URL used
// for pagination is derived from it.
func NewGraphQLRepository(endpoint string, pageSize int, logger *log.Logger, httpTimeout, idleConnTimeout, queryTimeout time.Duration) *GraphQLRepository {
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     idleConnTimeout,
		DisableCompression:  false,
	}

	return &GraphQLRepository{
		endpoint:    endpoint,
		dqlEndpoint: deriveDQLEndpoint(endpoint),
		httpClient: &http.Client{
			Timeout:   httpTimeout,
			Transport: transport,
		},
		pageSize:     pageSize,
		queryTimeout: queryTimeout,
		logger:       logger,
	}
}

// deriveDQLEndpoint maps a Dgraph GraphQL URL to its DQL /query URL.
// http://host:8080/graphql -> http://host:8080/query
func deriveDQLEndpoint(graphqlURL string) string {
	if strings.HasSuffix(graphqlURL, "/query") {
		return graphqlURL
	}
	if strings.HasSuffix(graphqlURL, "/graphql") {
		return strings.TrimSuffix(graphqlURL, "/graphql") + "/query"
	}
	return strings.TrimRight(graphqlURL, "/") + "/query"
}

// GetAll retrieves all whitelisted pubkeys from Dgraph and merges with hardcoded keys.
// Returns deduplicated list of pubkeys as [32]byte arrays.
func (r *GraphQLRepository) GetAll(ctx context.Context) ([][32]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
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
// using a uid cursor and returns their pubkeys as hex strings.
func (r *GraphQLRepository) fetchAllPubkeysFromDgraph(ctx context.Context) ([]string, error) {
	// Pre-allocate with estimated capacity to reduce reallocations
	// Start with 2x pageSize as a reasonable minimum
	allPubkeys := make([]string, 0, r.pageSize*2)
	after := "" // empty cursor => start from the beginning

	for {
		// Context cancellation is checked automatically by http.Request
		pubkeys, lastUID, rowCount, err := r.fetchPubkeysPage(ctx, after, r.pageSize)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch page after uid %q: %w", after, err)
		}

		// No more results
		if rowCount == 0 {
			break
		}

		r.logger.Printf("Fetched %d pubkeys from Dgraph after uid %q\n", len(pubkeys), after)

		allPubkeys = append(allPubkeys, pubkeys...)

		// A short page (fewer rows than requested) means we've reached the end.
		// Use the page's row count, not len(pubkeys), since rows without a
		// pubkey value are skipped from the result but still fill the page.
		if rowCount < r.pageSize {
			break
		}

		// Defensive guard against an infinite loop if the cursor fails to advance.
		if lastUID == "" || lastUID == after {
			break
		}
		after = lastUID
	}

	return allPubkeys, nil
}

// fetchPubkeysPage fetches a single page of pubkeys from Dgraph's DQL /query
// endpoint, seeking past the given uid cursor. It returns the pubkeys found on
// the page and the uid of the last row (the cursor for the next page); an empty
// lastUID signals the end of pagination.
func (r *GraphQLRepository) fetchPubkeysPage(ctx context.Context, after string, limit int) ([]string, string, int, error) {
	// DQL query with uid-cursor pagination. The cursor (after) is a Dgraph-issued
	// uid (e.g. "0x140000"), so it is trusted and safe to inline.
	cursor := ""
	if after != "" {
		cursor = fmt.Sprintf(", after: %s", after)
	}
	query := fmt.Sprintf(`{ q(func: type(Profile), first: %d%s) { uid pubkey } }`, limit, cursor)

	req, err := http.NewRequestWithContext(ctx, "POST", r.dqlEndpoint, bytes.NewBufferString(query))
	if err != nil {
		return nil, "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/dql")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", 0, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data struct {
			Q []struct {
				UID    string `json:"uid"`
				Pubkey string `json:"pubkey"`
			} `json:"q"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, "", 0, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response.Errors) > 0 {
		return nil, "", 0, fmt.Errorf("DQL error: %s", response.Errors[0].Message)
	}

	rows := response.Data.Q
	if len(rows) == 0 {
		return nil, "", 0, nil
	}

	pubkeys := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Pubkey != "" {
			pubkeys = append(pubkeys, row.Pubkey)
		}
	}

	return pubkeys, rows[len(rows)-1].UID, len(rows), nil
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

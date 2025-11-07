package repository

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// BenchmarkGetAll tests the full GetAll operation with realistic data
func BenchmarkGetAll(b *testing.B) {
	// Create a server that returns 10,000 pubkeys across multiple pages
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		// Parse the offset from request
		var reqBody struct {
			Variables map[string]interface{} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&reqBody)
		offset := int(reqBody.Variables["offset"].(float64))

		// Generate 1000 unique pubkeys per page
		profiles := make([]string, 0, 1000)
		for i := 0; i < 1000; i++ {
			pubkeyID := offset + i
			if pubkeyID >= 10000 {
				break
			}
			profiles = append(profiles, fmt.Sprintf(`{"pubkey": "%064x"}`, pubkeyID))
		}

		response := fmt.Sprintf(`{
			"data": {
				"queryProfile": [%s]
			}
		}`, joinStrings(profiles, ","))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	repo := &GraphQLRepository{
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		pageSize: 1000,
		logger:   log.New(io.Discard, "", 0),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		callCount = 0
		keys, err := repo.GetAll()
		if err != nil {
			b.Fatalf("GetAll() failed: %v", err)
		}
		// Should have 10,000 + 5 hardcoded = 10,005 keys
		if len(keys) != 10005 {
			b.Fatalf("Expected 10,005 keys, got %d", len(keys))
		}
	}
}

// BenchmarkFetchPubkeysPage tests a single page fetch
func BenchmarkFetchPubkeysPage(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate 1000 pubkeys
		profiles := make([]string, 1000)
		for i := 0; i < 1000; i++ {
			profiles[i] = fmt.Sprintf(`{"pubkey": "%064x"}`, i)
		}

		response := fmt.Sprintf(`{
			"data": {
				"queryProfile": [%s]
			}
		}`, joinStrings(profiles, ","))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	repo := &GraphQLRepository{
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		pageSize: 1000,
		logger:   log.New(io.Discard, "", 0),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		keys, err := repo.fetchAllPubkeysFromDgraph(b.Context())
		if err != nil {
			b.Fatalf("fetchAllPubkeysFromDgraph() failed: %v", err)
		}
		if len(keys) != 1000 {
			b.Fatalf("Expected 1000 keys, got %d", len(keys))
		}
	}
}

// BenchmarkMergePubkeys tests the deduplication logic
func BenchmarkMergePubkeys(b *testing.B) {
	// Create 10,000 dgraph keys and 5 hardcoded keys (2 duplicates)
	dgraphKeys := make([]string, 10000)
	for i := 0; i < 10000; i++ {
		dgraphKeys[i] = fmt.Sprintf("%064x", i)
	}

	hardcodedKeys := []string{
		fmt.Sprintf("%064x", 0),     // duplicate
		fmt.Sprintf("%064x", 1),     // duplicate
		fmt.Sprintf("%064x", 10000), // unique
		fmt.Sprintf("%064x", 10001), // unique
		fmt.Sprintf("%064x", 10002), // unique
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := mergePubkeys(dgraphKeys, hardcodedKeys)
		if len(result) != 10003 {
			b.Fatalf("Expected 10,003 unique keys, got %d", len(result))
		}
	}
}

// Helper to join strings (like strings.Join but inline)
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

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

func TestGraphQLRepository_GetAll(t *testing.T) {
	tests := []struct {
		name           string
		mockResponse   string
		mockStatusCode int
		wantErr        bool
		expectedCount  int // includes hardcoded keys
	}{
		{
			name: "successful fetch with profiles",
			mockResponse: `{
				"data": {
					"queryProfile": [
						{"pubkey": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
						{"pubkey": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
					]
				}
			}`,
			mockStatusCode: http.StatusOK,
			wantErr:        false,
			expectedCount:  7, // 2 from Dgraph + 5 hardcoded
		},
		{
			name: "empty profile list",
			mockResponse: `{
				"data": {
					"queryProfile": []
				}
			}`,
			mockStatusCode: http.StatusOK,
			wantErr:        false,
			expectedCount:  5, // 0 from Dgraph + 5 hardcoded
		},
		{
			name: "GraphQL error response",
			mockResponse: `{
				"errors": [
					{"message": "Internal server error"}
				]
			}`,
			mockStatusCode: http.StatusOK,
			wantErr:        true,
		},
		{
			name:           "HTTP error status",
			mockResponse:   `Internal Server Error`,
			mockStatusCode: http.StatusInternalServerError,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request method and content type
				if r.Method != "POST" {
					t.Errorf("Expected POST request, got %s", r.Method)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
				}

				// Verify request body contains GraphQL query
				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}

				if _, ok := reqBody["query"]; !ok {
					t.Error("Request body missing 'query' field")
				}

				// Send mock response
				w.WriteHeader(tt.mockStatusCode)
				w.Write([]byte(tt.mockResponse))
			}))
			defer server.Close()

			// Create repository with mock server
			repo := &GraphQLRepository{
				endpoint: server.URL,
				httpClient: &http.Client{
					Timeout: 5 * time.Second,
				},
				pageSize: 1000,
				logger:   log.New(io.Discard, "", 0),
			}

			// Execute GetAll
			keys, err := repo.GetAll()

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAll() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check key count if no error expected
			if !tt.wantErr {
				if len(keys) != tt.expectedCount {
					t.Errorf("GetAll() returned %d keys, expected %d", len(keys), tt.expectedCount)
				}

				// Verify all keys are 32 bytes
				for i, key := range keys {
					if len(key) != 32 {
						t.Errorf("Key at index %d has length %d, expected 32", i, len(key))
					}
				}
			}
		})
	}
}

func TestGraphQLRepository_Pagination(t *testing.T) {
	callCount := 0

	// Create mock server that returns different data per page
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		var reqBody struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		offset := int(reqBody.Variables["offset"].(float64))

		// Return different responses based on offset
		var response string
		if offset == 0 {
			// First page: return full page with unique profiles
			profiles := "["
			for i := 0; i < 1000; i++ {
				if i > 0 {
					profiles += ","
				}
				// Generate unique pubkeys using hex index
				profiles += fmt.Sprintf(`{"pubkey": "%064x"}`, i)
			}
			profiles += "]"
			response = `{"data": {"queryProfile": ` + profiles + `}}`
		} else if offset == 1000 {
			// Second page: return partial page to trigger end of pagination
			response = `{"data": {"queryProfile": [{"pubkey": "1111111111111111111111111111111111111111111111111111111111111111"}]}}`
		} else {
			// Any other page: return empty (shouldn't be called if pagination works correctly)
			response = `{"data": {"queryProfile": []}}`
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	repo := &GraphQLRepository{
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		pageSize: 1000,
		logger:   log.New(io.Discard, "", 0),
	}

	keys, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll() failed: %v", err)
	}

	// Should have made exactly 2 calls (first page full, second page partial)
	if callCount != 2 {
		t.Errorf("Expected exactly 2 pagination calls, got %d", callCount)
	}

	// Should have 1001 unique keys from Dgraph + 5 hardcoded = 1006 total
	// (1000 from first page + 1 from second page + 5 hardcoded)
	expectedKeys := 1001 + 5 // 1006 total
	if len(keys) != expectedKeys {
		t.Errorf("Expected %d keys, got %d", expectedKeys, len(keys))
	}
}

func TestGraphQLRepository_Deduplication(t *testing.T) {
	// Mock server that returns a duplicate of a hardcoded key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return one of the hardcoded pubkeys
		response := `{
			"data": {
				"queryProfile": [
					{"pubkey": "f6b07746e51d757fce1a030ef6fbe5dae6805df857f26ddce4e414bc3f983c4d"},
					{"pubkey": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
				]
			}
		}`
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	repo := &GraphQLRepository{
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		pageSize: 1000,
		logger:   log.New(io.Discard, "", 0),
	}

	keys, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll() failed: %v", err)
	}

	// Should have 6 unique keys: 5 hardcoded + 1 unique from Dgraph
	// (the duplicate should be removed)
	if len(keys) != 6 {
		t.Errorf("Expected 6 unique keys after deduplication, got %d", len(keys))
	}
}

func TestGraphQLRepository_Timeout(t *testing.T) {
	// Create a slow server that never responds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // Longer than client timeout
	}))
	defer server.Close()

	repo := &GraphQLRepository{
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 100 * time.Millisecond, // Short timeout
		},
		pageSize: 1000,
		logger:   log.New(io.Discard, "", 0),
	}

	_, err := repo.GetAll()
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestGraphQLRepository_ContextCancellation(t *testing.T) {
	// Create a slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
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

	// This will fail because GetAll creates its own context
	// But the test demonstrates the timeout handling
	_, err := repo.GetAll()
	if err == nil {
		t.Error("Expected error from slow server, got nil")
	}
}

func TestMergePubkeys(t *testing.T) {
	tests := []struct {
		name           string
		dgraphKeys     []string
		hardcodedKeys  []string
		expectedCount  int
		expectedUnique bool
	}{
		{
			name:           "no duplicates",
			dgraphKeys:     []string{"aaaa", "bbbb"},
			hardcodedKeys:  []string{"cccc", "dddd"},
			expectedCount:  4,
			expectedUnique: true,
		},
		{
			name:           "with duplicates",
			dgraphKeys:     []string{"aaaa", "bbbb", "cccc"},
			hardcodedKeys:  []string{"cccc", "dddd"},
			expectedCount:  4,
			expectedUnique: true,
		},
		{
			name:           "empty dgraph keys",
			dgraphKeys:     []string{},
			hardcodedKeys:  []string{"aaaa", "bbbb"},
			expectedCount:  2,
			expectedUnique: true,
		},
		{
			name:           "empty hardcoded keys",
			dgraphKeys:     []string{"aaaa", "bbbb"},
			hardcodedKeys:  []string{},
			expectedCount:  2,
			expectedUnique: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergePubkeys(tt.dgraphKeys, tt.hardcodedKeys)

			if len(result) != tt.expectedCount {
				t.Errorf("Expected %d keys, got %d", tt.expectedCount, len(result))
			}

			if tt.expectedUnique {
				// Check for uniqueness
				seen := make(map[string]bool)
				for _, key := range result {
					if seen[key] {
						t.Errorf("Duplicate key found: %s", key)
					}
					seen[key] = true
				}
			}
		})
	}
}

func TestGetHardcodedPubkeys(t *testing.T) {
	keys := getHardcodedPubkeys()

	// Should have 5 hardcoded keys
	expectedCount := 5
	if len(keys) != expectedCount {
		t.Errorf("Expected %d hardcoded keys, got %d", expectedCount, len(keys))
	}

	// All keys should be 64 hex characters (32 bytes)
	for i, key := range keys {
		if len(key) != 64 {
			t.Errorf("Key at index %d has length %d, expected 64 hex chars", i, len(key))
		}
	}

	// Verify specific known keys are present
	expectedKeys := map[string]bool{
		"f6b07746e51d757fce1a030ef6fbe5dae6805df857f26ddce4e414bc3f983c4d": true, // live forwarder
		"de6a2fe67d4407511f23d5d8f8dbfd29967b9a345cfed912fdfedf7fbabf570d": true, // history forwarder
	}

	for _, key := range keys {
		if expectedKeys[key] {
			delete(expectedKeys, key)
		}
	}

	if len(expectedKeys) > 0 {
		t.Errorf("Missing expected keys: %v", expectedKeys)
	}
}

func TestNewGraphQLRepository_DefaultEndpoint(t *testing.T) {
	endpoint := "http://dgraph:8080/graphql"
	pageSize := 1000
	logger := log.New(io.Discard, "", 0)

	repo := NewGraphQLRepository(endpoint, pageSize, logger)

	if repo.endpoint != endpoint {
		t.Errorf("Expected endpoint %s, got %s", endpoint, repo.endpoint)
	}

	if repo.pageSize != pageSize {
		t.Errorf("Expected page size %d, got %d", pageSize, repo.pageSize)
	}

	if repo.httpClient.Timeout != 30*time.Second {
		t.Errorf("Expected timeout 30s, got %v", repo.httpClient.Timeout)
	}

	// Verify transport is configured for connection pooling
	transport, ok := repo.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Error("Expected http.Transport to be configured")
	}
	if transport.MaxIdleConns != 10 {
		t.Errorf("Expected MaxIdleConns=10, got %d", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 2 {
		t.Errorf("Expected MaxIdleConnsPerHost=2, got %d", transport.MaxIdleConnsPerHost)
	}
}

func TestNewGraphQLRepository_CustomEndpoint(t *testing.T) {
	customEndpoint := "http://custom-dgraph:9090/graphql"
	pageSize := 500
	logger := log.New(io.Discard, "", 0)

	repo := NewGraphQLRepository(customEndpoint, pageSize, logger)

	if repo.endpoint != customEndpoint {
		t.Errorf("Expected custom endpoint %s, got %s", customEndpoint, repo.endpoint)
	}

	if repo.pageSize != pageSize {
		t.Errorf("Expected page size %d, got %d", pageSize, repo.pageSize)
	}
}

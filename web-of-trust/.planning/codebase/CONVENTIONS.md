# Coding Conventions

**Analysis Date:** 2026-06-09

## Language & Version

**Go:** 1.24.1+

The module uses standard Go conventions throughout.

## Naming Patterns

**Files:**
- Lowercase with underscores: `clusterscan.go`, `chunks.go`, `main.go`
- Package follows directory name: `package dgraph` in `pkg/dgraph/`
- Receiver methods grouped with their type in same file

**Functions:**
- PascalCase for exported functions: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`
- camelCase for unexported functions: `normalizeSeedPubkeys()`, `cleanSubscribeError()`
- Methods: receiver as single letter lowercase: `(c *Client)`, `(e *subscriptionError)`
- Constructor pattern: `func New(cfg Config) (*Crawler, error)`

**Variables:**
- camelCase: `signerPubkey`, `followeeList`, `dgClient`, `relayState`
- Map keys as pubkey strings or UIDs: `pubkeys map[string]struct{}`
- Booleans prefixed with state: `alive`, `deleted`, `valid`
- Timestamps with suffix: `kind3CreatedAt`, `lastUpdate`, `olderThanUnix`

**Types & Structs:**
- PascalCase for all types: `Client`, `Crawler`, `Config`, `PubkeyNode`, `WeakBridge`
- Exported struct fields: PascalCase: `RelayURLs`, `DgraphAddr`, `Timeout`
- Struct tags for config mapping: `mapstructure:"relay_urls"` (snake_case in YAML)
- JSON tags for Dgraph unmarshalling: `json:"pubkey"`, `json:"kind3CreatedAt"`
- Unexported struct fields: camelCase: `dg`, `conn`, `relays`, `dgClient`

**Constants:**
- UPPERCASE_WITH_UNDERSCORES: `maxBackoff`, `initialBackoff`, `maxConsecutiveFailures`
- Magic numbers extracted as named constants at package/function level

**Interfaces:**
- No large interface definitions found; composition over interfaces
- Error interface implementation: `Error() string`, `Unwrap() error`

## Code Style

**Formatting:**
- `go fmt` enforced via Makefile (`make fmt`)
- Standard Go formatting rules: 8-space indentation (tabs)
- Line length: no strict limit observed, but functions kept readable

**Linting:**
- `golangci-lint` optional (non-failing via Makefile)
- Makefile targets: `make lint`, `make lint-fix`
- Tool gracefully handles absence (doesn't break build)

**Import Organization:**
1. Standard library imports (fmt, context, log, etc.)
2. Blank line separator
3. Internal module imports (web-of-trust/pkg/...)
4. Blank line separator
5. External third-party imports (github.com/..., google.golang.org/...)

**Example from `pkg/crawler/crawler.go`:**
```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
)
```

## Error Handling

**Pattern: `fmt.Errorf` with `%w` for error wrapping:**
- All errors wrapped with context using `%w` verb for error chain
- Error message explains the operation that failed
- Example: `fmt.Errorf("query follower failed: %w", err)`

**Custom error types (for categorization):**
- `subscriptionError` - subscription-related failures on relay
- `transportError` - connection/transport failures
- Both implement `Error()` and `Unwrap()` methods

**Error propagation:**
- Errors returned immediately to caller with context added
- No silent error suppression except explicit cases (e.g., config file not found)
- Named return values used in some methods: `([]string, error)`, `(bool, error)`

**Example from `pkg/config/config.go` (lines 33-44):**
```go
func LoadConfig() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, "deepfry")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", configDir, err)
		}
	}
	// ...
}
```

## Logging

**Framework:** Standard library `log` package (not custom logger)

**Logging levels (by prefix convention):**
- `log.Printf("Connected to relay: %s", url)` - Info level
- `log.Printf("WARN: Failed to connect: %v", err)` - Warning level
- `log.Printf("DEBUG: Starting AddFollowers for pubkey %s...", pubkey)` - Debug when debug flag enabled
- `log.Fatalf("Failed to create crawler: %v", err)` - Fatal errors in main()

**When to log:**
- Connection lifecycle events: connect, disconnect, reconnect
- Major state changes: relay dead, retry scheduled, chunk processed
- Configuration loaded/saved
- Processing milestones: pubkeys processed, batch completed
- Debug output guarded by `if c.debug { log.Printf(...) }`
- No logging of raw secrets (env vars, private keys)

**Example from `pkg/crawler/crawler.go` (line 85-98):**
```go
for _, url := range cfg.RelayURLs {
	relay, err := nostr.RelayConnect(context.Background(), url)
	if err != nil {
		log.Printf("WARN: Failed to connect to relay %s, removing from config: %v", url, err)
		if cfg.OnConnectFail != nil {
			cfg.OnConnectFail(url)
		}
		continue
	}
	// ...
	if cfg.Debug {
		log.Printf("Connected to relay: %s", url)
	}
}
```

## Comments

**Module-level comments:**
- Explain purpose at top of file above package declaration
- Describe schema/structure for complex packages (e.g., Dgraph package)

**Function comments:**
- All exported functions have comment starting with function name
- Describe parameters, return values, and side effects
- Example from `pkg/dgraph/dgraph.go` (lines 32-33):
  ```go
  // NewClient creates a new Client connected to the given dgraph gRPC address
  // (eg "localhost:9080").
  ```

**Inline comments:**
- Minimal; code is self-documenting with clear names
- Used for non-obvious logic: why a step is needed, workaround for external limitation
- Example from `pkg/dgraph/dgraph.go` (line 87): `// viper.SafeWriteConfigAs does not write SetDefault values...`

## Function Design

**Size:** Functions keep logic focused; longest is `AddFollowers()` at ~230 lines due to complex Dgraph transaction management

**Parameters:**
- `ctx context.Context` always first parameter (Go convention)
- Configuration struct passed as value: `cfg Config`
- Callback functions used for pagination: `callback func([]string) error`
- Debug flag passed explicitly: `debug bool`

**Return Values:**
- Error always last return value
- Boolean for optional success: `(deleted bool, error)`
- Map for bulk results: `map[string]int64`
- Explicit nil for not found (not error): `func() (int64, error)` returns `(0, nil)` if not exists

**Example from `pkg/dgraph/dgraph.go` (lines 430-433):**
```go
func (c *Client) GetStalePubkeys(
	ctx context.Context,
	olderThanUnix int64,
) (map[string]int64, error) {
	// ...
}
```

## Module Design

**Package structure:**
- `pkg/config` - configuration loading and persistence
- `pkg/dgraph` - Dgraph client and schema operations
- `pkg/crawler` - relay connection and event processing

**Exports:**
- Public APIs: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`
- Config struct exported with public fields for mapstructure unmarshalling
- Helper types exported when needed: `PubkeyNode`, `WeakBridge`

**No barrel files** - each package imports directly from subpackage

**Dgraph client methods:**
- Constructor: `NewClient(addr string) (*Client, error)`
- Lifecycle: `Close() error`
- Schema setup: `EnsureSchema(ctx context.Context) error`
- Write operations: `AddFollowers()`, `RemoveFollower()`, `DeleteNodes()`
- Read operations: `GetStalePubkeys()`, `CountPubkeys()`, `GetKind3CreatedAt()`
- Paginated reads: `GetPubkeysWithMinFollowersPaginated()`, `GetAllPubkeysPaginated()`
- Graph analysis (clusterscan): `ResolvePubkeysToUIDs()`, `ExpandTrustedSet()`, `GetWeakBridges()`

**Config persistence:**
- YAML file at `~/deepfry/web-of-trust.yaml`
- Viper library handles loading/saving
- Defaults provided if file missing
- Functions to update: `SaveForwardRelayURL()`, `RemoveRelayURL()`

## Concurrency

**Patterns used:**
- `sync.Mutex` for protecting shared state: `dbUpdateMutex`
- `sync.WaitGroup` for waiting on goroutines: signal handling in main
- `atomic.Int32` for concurrent counter access: `failures` in relay state
- Context-based cancellation: `context.WithCancel()` for graceful shutdown
- No global state; all state in struct fields or function-local variables

## Dependencies & Integrations

**Key external packages:**
- `github.com/dgraph-io/dgo/v210` - Dgraph gRPC client
- `github.com/nbd-wtf/go-nostr` - Nostr relay communication
- `github.com/spf13/viper` - YAML config loading
- `google.golang.org/grpc` - gRPC transport

**No vendor directory** - relies on go mod cache

---

*Convention analysis: 2026-06-09*

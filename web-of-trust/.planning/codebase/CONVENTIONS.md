# Coding Conventions

**Analysis Date:** 2026-06-09

## Naming Patterns

**Files:**
- Lowercase, single word or noun: `crawler.go`, `chunks.go`, `dgraph.go`, `config.go`, `clusterscan.go`, `main.go`
- No underscores in non-test filenames. Test files use the `_test.go` suffix and may carry a descriptive segment: `dgraph_stale_test.go`
- One package per directory; package name matches directory: `package dgraph` in `pkg/dgraph/`, `package crawler` in `pkg/crawler/`
- Entry points always `package main` in `cmd/<app>/main.go`

**Functions:**
- Exported functions/methods use PascalCase: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`, `MarkAttempted()`, `FetchAndUpdateFollows()` (`pkg/dgraph/dgraph.go`, `pkg/crawler/crawler.go`)
- Unexported functions/methods use camelCase: `collectStale()`, `markRelayDead()`, `processFollowsInChunks()`, `updateFollowsFromEvent()` (`pkg/dgraph/dgraph.go:482`, `pkg/crawler/crawler.go:170`)
- Constructor pattern: `New(cfg Config) (*Crawler, error)` for the primary type (`pkg/crawler/crawler.go:67`); `NewClient(addr string) (*Client, error)` for the Dgraph client (`pkg/dgraph/dgraph.go:34`)
- Exported function doc comments start with the function name: `// NewClient creates...`, `// GetStalePubkeys returns...`

**Variables:**
- camelCase for locals and unexported fields: `signerPubkey`, `followeeList`, `dgClient`, `relayState`, `lastUpdate`, `queryCtx` (`pkg/dgraph/dgraph.go`)
- Single-letter lowercase receivers tied to the type: `(c *Client)`, `(c *Crawler)`, `(e *subscriptionError)`
- Map-as-set idiom uses `map[string]struct{}` for pubkey/UID sets: `follows map[string]struct{}` (`pkg/dgraph/dgraph.go:84`)
- Boolean flags carry state names: `alive`, `deleted`, `debug`, `valid`
- Timestamp variables/fields carry a time suffix or `At`: `kind3CreatedAt`, `lastUpdate`, `last_db_update`, `last_attempt`, `olderThanUnix`, `retryAt`

**Types:**
- PascalCase for all type names: `Client`, `Crawler`, `Config`, `PubkeyNode`, `relayState`, `subscriptionError`
- Exported types when consumed across packages (`Client`, `Config`, `PubkeyNode`); unexported when package-internal (`relayState`, `subscriptionError`, `transportError`)
- Exported struct fields are PascalCase; unexported fields are camelCase (`pkg/crawler/crawler.go:48-65`)

**Constants:**
- Package- or function-level named constants for magic numbers/durations: `initialBackoff`, `maxBackoff`, `maxConsecutiveFailures` (`pkg/crawler/crawler.go:33-37`); `maxRecvMsgSize = 256 << 20` (`pkg/dgraph/dgraph.go:39`)
- camelCase const names (not SCREAMING_CASE), declared in `const ( ... )` blocks

**Struct tags:**
- `mapstructure:"snake_case"` for viper config unmarshalling: `RelayURLs []string mapstructure:"relay_urls"` (`pkg/config/config.go:16`)
- `json:"..."` tags on inline result structs for Dgraph response decoding: `json:"pubkey"`, `json:"kind3CreatedAt"`, `json:"uid"` (`pkg/dgraph/dgraph.go:126-133`)

## Code Style

**Formatting:**
- `go fmt` is the enforced formatter, run via `make fmt` (Makefile target)
- Standard gofmt rules: tab indentation, gofmt import grouping
- `make all` runs `tidy fmt vet test build` in sequence — fmt and vet are part of the canonical workflow
- No strict line-length limit; long `fmt.Sprintf` calls are wrapped across lines with arguments aligned (`pkg/dgraph/dgraph.go:217-223`)

**Linting:**
- `golangci-lint` via `make lint` / `make lint-fix`
- Lint is **non-failing**: the Makefile guards with `command -v golangci-lint` and prints a warning instead of failing if the tool is missing or reports issues
- No `.golangci.yml` config file present in this module — default linters apply when the tool is installed
- `go vet` via `make vet` is part of the standard pipeline

## Import Organization

**Order (gofmt-grouped, blank-line separated):**
1. Standard library: `context`, `encoding/json`, `fmt`, `log`, `os`, `strings`, `sync`, `time`
2. Internal module packages: `web-of-trust/pkg/config`, `web-of-trust/pkg/crawler`, `web-of-trust/pkg/dgraph`
3. Third-party packages: `github.com/dgraph-io/dgo/v210`, `github.com/nbd-wtf/go-nostr`, `github.com/spf13/viper`, `google.golang.org/grpc`

Example (`pkg/crawler/crawler.go:3-17`): stdlib block, then `web-of-trust/pkg/dgraph`, then `github.com/nbd-wtf/go-nostr`.

**Path Aliases:**
- Module path is `web-of-trust` (declared in `go.mod`); internal imports are absolute under that path (`web-of-trust/pkg/...`)
- No import aliasing observed

## Error Handling

**Patterns:**
- Errors are wrapped with `%w` and a description of the failed operation: `fmt.Errorf("query follower failed: %w", err)` (`pkg/dgraph/dgraph.go:118`). This is the dominant pattern across `pkg/dgraph`.
- Error is always the last return value: `(*Client, error)`, `(bool, error)`, `(map[string]int64, error)`
- Errors are returned immediately to the caller with added context; no silent suppression except documented cases (config file not found is logged, not fatal — `pkg/config/config.go:80-99`)
- Input-validation errors use plain `fmt.Errorf` with a clear message: `fmt.Errorf("signerPubkey must be specified (non-empty)")` (`pkg/dgraph/dgraph.go:333`)
- Custom error types implement `Error() string` and `Unwrap() error`: `subscriptionError`, `transportError` (`pkg/crawler/crawler.go:19-31`)
- Typed errors are matched with `errors.As` in a type switch to classify relay failures (`pkg/crawler/crawler.go:422-428`)
- "Not found" is signalled with a zero value + nil error, never an error: `return 0, nil` when a pubkey is absent (`pkg/dgraph/dgraph.go:608-612`); `return false, nil` when nothing to delete (`pkg/dgraph/dgraph.go:409-411`)
- In `main()`, unrecoverable startup errors use `log.Fatalf(...)` (`cmd/crawler/main.go:44`, `:50`, `:88`)

## Logging

**Framework:** Standard library `log` package (`log.Printf`, `log.Fatalf`). No structured-logging library.

**Patterns:**
- Info level: `log.Printf("Connected to relay: %s", url)` (`pkg/crawler/crawler.go:98`)
- Warnings prefixed with `WARN:`: `log.Printf("WARN: Failed to connect to relay %s...", ...)` (`pkg/crawler/crawler.go:88`)
- Debug output prefixed with `DEBUG:` and guarded by a `debug` flag: `if debug { log.Printf("DEBUG: Starting AddFollowers...") }` (`pkg/dgraph/dgraph.go:91-94`)
- Metrics emitted as JSON-encoded lines prefixed with `METRICS:` / `DEBUG_METRICS:` (`pkg/crawler/crawler.go:573`, `:591`)
- Fatal startup failures use `log.Fatalf` in `main()` only
- Connection lifecycle (connect/disconnect/reconnect/dead/retry), config load/save, and processing milestones are all logged
- Never log raw secrets (env vars, private keys) — enforced by convention per project CLAUDE.md

## Comments

**When to Comment:**
- File-level purpose comment above the package declaration for non-trivial packages, including a schema sketch for `pkg/dgraph` (`pkg/dgraph/dgraph.go:17-24`)
- Non-obvious logic and external-tool workarounds get an inline `why` comment: `// viper.SafeWriteConfigAs does not write SetDefault values...` (`pkg/config/config.go:87`)
- Regression-guarding rationale documented inline where a past bug informs the code: the `GetStalePubkeys` doc comment explains why `orderasc: last_attempt` must NOT be used (`pkg/dgraph/dgraph.go:438-442`)

**GoDoc:**
- Every exported function, method, and type has a doc comment beginning with its identifier name
- Comments describe parameters, return values, and side effects (e.g., `RemoveFollower`, `RemovePubKeyIfNoFollowers`, `TouchLastDBUpdate`)
- Config struct fields carry trailing inline comments explaining their meaning (`pkg/config/config.go:25-29`)

## Function Design

- `ctx context.Context` is always the first parameter (`AddFollowers`, `GetStalePubkeys`, `MarkAttempted`, etc.)
- Configuration passed as a value struct: `New(cfg Config)`, `cfg Config`
- Callback functions used for paginated/streamed results: `callback func([]string) error`, `callback func([]PubkeyNode) error` (`pkg/dgraph/dgraph.go:662`, `:805`)
- Debug behaviour passed explicitly as a `debug bool` parameter or struct field, not a global
- Multiple return values favour `(value, error)`; booleans for optional success: `(deleted bool, error)` (`pkg/dgraph/dgraph.go:375`)
- Bulk results returned as maps: `map[string]int64` (pubkey -> timestamp)
- Functions managing a transaction `defer txn.Discard(ctx)` immediately after creating the txn, then `Commit` explicitly at the end (`pkg/dgraph/dgraph.go:99-100`, `:309`)

## Module Design

**Package layout:**
- `pkg/config` — configuration loading/persistence (leaf package, viper-backed)
- `pkg/dgraph` — Dgraph client, schema, and all graph queries/mutations
- `pkg/crawler` — relay pool, kind-3 subscription, event validation, chunked writes
- `cmd/<app>` — thin `main` entry points wiring config + packages together

**Dependency direction:** `cmd/*` → `pkg/crawler` → `pkg/dgraph`; `pkg/config` is a leaf. No circular imports.

**Exports:**
- Public APIs are method sets on exported structs: `NewClient()` returns `*Client`, callers use methods on it
- Lifecycle methods follow Go convention: `Close() error`, `EnsureSchema(ctx) error`
- Helper result types exported only when crossing package boundaries: `PubkeyNode`, and (in `clusterscan.go`) `WeakBridge`
- No barrel/aggregator files; each `.go` file groups a type and its receiver methods

## Concurrency

- `sync.Mutex` guards shared mutable state (`dbUpdateMutex` in `Crawler`) (`pkg/crawler/crawler.go:54`)
- `sync.WaitGroup` for goroutine lifecycle, e.g. the signal handler in `main` (`cmd/crawler/main.go:30-39`)
- `atomic.Int32` for lock-free counters (`relayState.failures`) (`pkg/crawler/crawler.go:45`)
- Context-based cancellation for graceful shutdown: `context.WithCancel` in `main`, cancelled on SIGINT/SIGTERM (`cmd/crawler/main.go:22-39`)
- No package-level global mutable state; all state lives in struct fields or function locals

## Validation Conventions

- Pubkeys validated with `nostr.GetPublicKey()` before use; invalid pubkeys are logged and skipped, never fatal (`pkg/crawler/crawler.go:266`, `:507`)
- Event signatures verified with `event.CheckSignature()` before processing; failures logged as `WARN` and skipped (`pkg/crawler/crawler.go:375`)
- Follow-list p-tags parsed, de-duplicated, and invalid entries dropped before writing
- Config validated after unmarshal (e.g. at least one relay URL required) (`pkg/config/config.go:107-109`)

---

*Convention analysis: 2026-06-09*

# Codebase Structure

**Analysis Date:** 2026-06-09

## Directory Layout

```
web-of-trust/
├── cmd/                      # CLI entry points (one main package per binary)
│   ├── crawler/              # Main crawler loop binary
│   │   └── main.go
│   ├── clusterscan/          # Spam-cluster detection CLI
│   │   └── main.go
│   ├── discover-relays/      # NIP-65 / nostr.watch relay discovery CLI
│   │   └── main.go
│   ├── healthcheck/          # Invalid/duplicate pubkey scanner CLI
│   │   └── main.go
│   └── pubkeys/              # Popular-pubkey CSV exporter CLI
│       └── main.go
├── pkg/                      # Shared libraries
│   ├── config/               # YAML config load/persist (leaf package)
│   │   └── config.go
│   ├── crawler/              # Relay pool + kind-3 subscription + writes
│   │   ├── crawler.go
│   │   └── chunks.go
│   └── dgraph/               # Dgraph client: writes, stale selection, analysis
│       ├── dgraph.go
│       ├── clusterscan.go
│       └── dgraph_stale_test.go   # integration test (//go:build integration)
├── queries/                  # Ad-hoc DQL query references
│   └── explore.dql
├── bin/                      # Built binaries (Makefile output; not committed)
├── Makefile                  # Per-binary build/test/lint targets
├── go.mod / go.sum           # Module definition + lockfile (module: web-of-trust)
├── README.md
├── CLAUDE.md                 # Module guidance
└── *.csv                     # Generated exports (e.g. popular_pubkeys_*.csv)
```

## Directory Purposes

**`cmd/`:**
- Purpose: One `main` package per executable. Thin wiring over `pkg/` libraries.
- Contains: Flag parsing, config loading, run loops, report writers.
- Key files: `cmd/crawler/main.go` (primary), `cmd/clusterscan/main.go`.

**`pkg/config/`:**
- Purpose: Load/persist `~/deepfry/web-of-trust.yaml`; supply defaults; npub→hex.
- Contains: `Config` struct (`mapstructure` tags), `LoadConfig`, mutation helpers.
- Key files: `pkg/config/config.go`.

**`pkg/crawler/`:**
- Purpose: Relay connection pool, kind-3 subscription, event validation/forwarding, chunked writes.
- Contains: `Crawler`, `relayState`, error types, `processFollowsInChunks`.
- Key files: `pkg/crawler/crawler.go`, `pkg/crawler/chunks.go`.

**`pkg/dgraph/`:**
- Purpose: All Dgraph access. `dgraph.go` = crawler writes + stale selection; `clusterscan.go` = read-only analysis.
- Contains: `Client`, `PubkeyNode`, `WeakBridge`, `ClusterNode`, `Degree`.
- Key files: `pkg/dgraph/dgraph.go`, `pkg/dgraph/clusterscan.go`.

**`queries/`:**
- Purpose: Reference DQL snippets for manual exploration in Ratel.
- Contains: `explore.dql`. Not compiled.

## Key File Locations

**Entry Points:**
- `cmd/crawler/main.go`: Crawl loop, seed init, final report.
- `cmd/clusterscan/main.go`: Trust closure + weak-bridge reporting.
- `cmd/discover-relays/main.go`: Relay discovery and latency testing.
- `cmd/healthcheck/main.go`: Pubkey integrity scan + optional purge.
- `cmd/pubkeys/main.go`: Pubkey CSV export.

**Configuration:**
- `pkg/config/config.go`: `Config` struct and viper defaults.
- `~/deepfry/web-of-trust.yaml`: Live config (never edit for testing — use a temp HOME).

**Core Logic:**
- `pkg/crawler/crawler.go`: `FetchAndUpdateFollows`, `queryRelay`, relay lifecycle.
- `pkg/dgraph/dgraph.go`: `AddFollowers`, `GetStalePubkeys`, `MarkAttempted`, schema.
- `pkg/dgraph/clusterscan.go`: `ExpandTrustedSet`, `GetWeakBridges`, `ClusterBeneath`.

**Testing:**
- `pkg/dgraph/dgraph_stale_test.go`: Integration regression test, gated by `//go:build integration`.

**Build:**
- `Makefile`: `build-crawler`, `build-pubkeys`, `build-discover-relays`, `build-healthcheck`, `build-clusterscan`; version injected via ldflags into `web-of-trust/pkg/version`.

## Naming Conventions

**Files:**
- Lowercase, no separators or underscores only: `crawler.go`, `chunks.go`, `clusterscan.go`, `main.go`.
- Tests: `<subject>_test.go` (e.g. `dgraph_stale_test.go`).

**Directories:**
- Lowercase; package name matches directory (`package dgraph` in `pkg/dgraph/`).
- Hyphenated for multi-word command dirs: `cmd/discover-relays/`.

**Identifiers:**
- Exported funcs/types PascalCase (`NewClient`, `GetStalePubkeys`, `WeakBridge`).
- Unexported funcs/fields camelCase (`normalizeSeedPubkeys`, `dbUpdateMutex`).
- Receivers single lowercase letter (`(c *Client)`, `(c *Crawler)`).
- Config struct fields use `mapstructure:"snake_case"` tags; Dgraph JSON via `json:"..."` tags.

## Where to Add New Code

**New crawler behavior (relay handling, event processing):**
- Implementation: `pkg/crawler/crawler.go` (or a new file in `pkg/crawler/` for a cohesive concern, like `chunks.go`).
- Driven from: `cmd/crawler/main.go`.

**New Dgraph query or mutation:**
- Write-path / crawl-selection methods: add to `pkg/dgraph/dgraph.go` as a `*Client` method.
- Read-only analysis methods: add to `pkg/dgraph/clusterscan.go`.
- Add a JSON-tagged result struct beside the method; keep all DQL in the method body.

**New CLI tool:**
- Create `cmd/<tool-name>/main.go` (hyphenated dir, `package main`).
- Add `APP_<NAME>` and a `build-<tool>` target to the `Makefile` and include it in `build`.

**New config field:**
- Add to `Config` in `pkg/config/config.go` with a `mapstructure` tag.
- Add a matching `viper.SetDefault(...)` in `LoadConfig`.

**Utilities / shared helpers:**
- Prefer a method on the relevant `*Client`/`*Crawler` type; otherwise an unexported helper in the same package. No `pkg/util` catch-all exists.

**Tests:**
- Co-locate as `<file>_test.go` in the same package.
- Integration tests that need live Dgraph must start with `//go:build integration` and run via `make test-integration`. There is no unit-test suite yet; `make test` runs `-short`.

## Special Directories

**`bin/`:**
- Purpose: Compiled binaries from `make build`.
- Generated: Yes. Committed: No.

**`queries/`:**
- Purpose: Reference DQL for manual Ratel exploration.
- Generated: No. Committed: Yes.

**Root `*.csv`:**
- Purpose: Generated exports from `pubkeys`/`clusterscan` runs (timestamped).
- Generated: Yes. Committed: incidentally present; treat as artifacts.

**`~/deepfry/`:**
- Purpose: Live config home (outside repo). Never delete/overwrite; use a temp HOME for tests.

---

*Structure analysis: 2026-06-09*

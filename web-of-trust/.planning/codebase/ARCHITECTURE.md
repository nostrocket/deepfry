<!-- refreshed: 2026-06-09 -->
# Architecture

**Analysis Date:** 2026-06-09

## System Overview

```text
┌─────────────────────────────────────────────────────────────┐
│                       CLI Entry Points (cmd/)                │
├──────────────┬──────────────┬──────────────┬────────────────┤
│   crawler    │ clusterscan  │   pubkeys    │  healthcheck   │
│ `cmd/crawler`│`cmd/cluster..`│ `cmd/pubkeys`│`cmd/healthc..` │
│              │              │              │ discover-relays│
└──────┬───────┴──────┬───────┴──────┬───────┴───────┬────────┘
       │              │              │               │
       ▼              ▼              ▼               ▼
┌──────────────────┐  ┌──────────────────────────────────────┐
│  crawler package │  │            config package            │
│ `pkg/crawler/`   │  │ `pkg/config/config.go`               │
│ relay pool,      │  │ viper YAML @ ~/deepfry/web-of-trust   │
│ kind-3 subscribe │  └──────────────────────────────────────┘
└────────┬─────────┘
         │ (all entry points use the dgraph client)
         ▼
┌─────────────────────────────────────────────────────────────┐
│                   dgraph package (data layer)               │
│  `pkg/dgraph/dgraph.go`      — crawler writes + stale reads  │
│  `pkg/dgraph/clusterscan.go` — read-only graph analysis      │
└──────────────────────────────┬──────────────────────────────┘
                               │ gRPC (dgo/v210)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│  Dgraph (localhost:9080)   — ID-only Profile follow-graph    │
└─────────────────────────────────────────────────────────────┘
         ▲                                          │
         │ kind-3 events (NIP-01 WebSocket)         │ optional forward
         │                                          ▼
┌──────────────────┐                     ┌──────────────────────┐
│  Nostr Relays    │                     │  Forward Relay        │
│  (wss://...)     │                     │  (e.g. StrFry 7777)   │
└──────────────────┘                     └──────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| Crawler main | Main loop: get stale pubkeys, fetch follows, mark attempted, report stats | `cmd/crawler/main.go` |
| Crawler package | Relay pool, kind-3 subscription, event validation, event forwarding, chunked writes | `pkg/crawler/crawler.go` |
| Chunks | Splits follow-lists >10k into 200-item batches | `pkg/crawler/chunks.go` |
| Dgraph client | Upsert pubkeys, follows edges, stale-frontier selection, timestamp management | `pkg/dgraph/dgraph.go` |
| Clusterscan queries | Trust propagation, weak-bridge detection, cluster sizing (read-only DQL) | `pkg/dgraph/clusterscan.go` |
| Clusterscan CLI | Build trusted set, rank spam clusters, write CSV/JSON | `cmd/clusterscan/main.go` |
| Config | Load/persist `~/deepfry/web-of-trust.yaml`, npub→hex normalization | `pkg/config/config.go` |
| Discover relays | nostr.watch API + NIP-65 discovery, latency testing, config update | `cmd/discover-relays/main.go` |
| Pubkeys exporter | Export pubkeys with ≥1 follower to timestamped CSV | `cmd/pubkeys/main.go` |
| Healthcheck | Scan for invalid/duplicate pubkey nodes, optional `-purge` | `cmd/healthcheck/main.go` |

## Pattern Overview

**Overall:** Layered Go module — thin CLI mains over two shared packages (`crawler`, `dgraph`) and a leaf `config` package. Single binary per command, each its own `main` under `cmd/`.

**Key Characteristics:**
- **Stale-frontier crawl model:** the crawler never re-walks the whole graph; it asks Dgraph for the uncrawled frontier, then tops up with aged-out pubkeys (`GetStalePubkeys`, `pkg/dgraph/dgraph.go:443`).
- **Upsert-based dedup:** pubkey uniqueness enforced via Dgraph `@unique @upsert` schema; all writes are idempotent (`EnsureSchema`, `pkg/dgraph/dgraph.go:60`).
- **ID-only graph:** Dgraph stores pubkey relationships only; event payloads stay in StrFry. No event bodies persisted.
- **Concurrent fan-out, serialized write:** relays queried concurrently; Dgraph writes serialized under `dbUpdateMutex` (`pkg/crawler/crawler.go:350`).
- **Read-only analysis:** clusterscan uses only read-only txns; all graph math (counts, `uid()` sets, `math()`, `@recurse`) stays inside DQL.

## Layers

**CLI / Entry layer:**
- Purpose: Parse flags, load config, wire and drive a package; signal handling and final reporting.
- Location: `cmd/crawler/`, `cmd/clusterscan/`, `cmd/pubkeys/`, `cmd/healthcheck/`, `cmd/discover-relays/`
- Contains: `main()`, flag parsing, batch loops, CSV/JSON report writers.
- Depends on: `pkg/config`, `pkg/crawler`, `pkg/dgraph`.
- Used by: User (binary execution from `bin/`).

**Crawler layer:**
- Purpose: Manage relay connection pool; subscribe to kind-3 events; validate signatures; forward events; coordinate writes.
- Location: `pkg/crawler/`
- Contains: `Crawler` struct, `relayState` machines, `FetchAndUpdateFollows`, `queryRelay`, `processFollowsInChunks`.
- Depends on: `pkg/dgraph`, `github.com/nbd-wtf/go-nostr`.
- Used by: `cmd/crawler/main.go`.

**Data layer:**
- Purpose: Encapsulate all Dgraph mutations and queries; enforce pubkey uniqueness; provide crawler writes and read-only analysis queries.
- Location: `pkg/dgraph/`
- Contains: `Client` struct; `dgraph.go` (writes + stale selection) and `clusterscan.go` (read-only analysis).
- Depends on: `github.com/dgraph-io/dgo/v210`, `google.golang.org/grpc`.
- Used by: Crawler and every CLI entry point.

**Config layer (leaf):**
- Purpose: Load YAML config with defaults; persist runtime mutations (relay add/remove, forward relay).
- Location: `pkg/config/config.go`
- Contains: `Config` struct, `LoadConfig`, `SaveForwardRelayURL`, `RemoveRelayURL`, `normalizeSeedPubkeys`.
- Depends on: `github.com/spf13/viper`, `go-nostr/nip19`.
- Used by: All entry points (no dependency on crawler/dgraph; leaf).

## Data Flow

### Primary Request Path (Crawler Loop)

1. Load config from `~/deepfry/web-of-trust.yaml`; create Dgraph client (`cmd/crawler/main.go:42`).
2. `EnsureSchema` and connect relays in `crawler.New` (`pkg/crawler/crawler.go:67`).
3. `GetStalePubkeys(ctx, now-threshold, 500)` selects the uncrawled frontier, then aged-out pubkeys (`cmd/crawler/main.go:110` → `pkg/dgraph/dgraph.go:443`).
4. If DB empty, inject seed pubkey (`cmd/crawler/main.go:123`).
5. `ReconnectRelays` revives dead relays past their backoff (`pkg/crawler/crawler.go:201`).
6. `FetchAndUpdateFollows` fans out a kind-3 filter to all alive relays concurrently, collects events on a channel (`pkg/crawler/crawler.go:260`).
7. Per event: dedup by ID, `CheckSignature`, forward to forward-relay, skip if `created_at` ≤ stored `kind3CreatedAt` (else `TouchLastDBUpdate`), parse `p` tags (`pkg/crawler/crawler.go:349`).
8. `updateFollowsFromEvent` → `AddFollowers` (or chunked for >10k) replaces the follow list under txn (`pkg/crawler/crawler.go:497` → `pkg/dgraph/dgraph.go:80`).
9. `MarkAttempted` stamps `last_attempt` on the whole batch so un-fetchable pubkeys age out (`cmd/crawler/main.go:157` → `pkg/dgraph/dgraph.go:512`).
10. Loop until `ctx` cancelled (SIGINT/SIGTERM) or no stale pubkeys remain; print final report (`cmd/crawler/main.go:178`).

### Clusterscan Path (Spam Detection)

1. Resolve configured seed pubkeys to UIDs (`cmd/clusterscan/main.go:64` → `ResolvePubkeysToUIDs`).
2. Trust closure: loop `ExpandTrustedSet` (node joins when ≥K trusted accounts follow it) until no new nodes (`cmd/clusterscan/main.go:83`).
3. `GetWeakBridges` finds non-trusted nodes with 1..maxWeight edges crossing into trusted (`pkg/dgraph/clusterscan.go:141`).
4. `ClusterBeneath` walks `follows` up to `depth` hops per bridge; non-trusted members counted (`pkg/dgraph/clusterscan.go:237`).
5. Rank by `clusterSize / weight`; write timestamped CSV + JSON (`cmd/clusterscan/main.go:156`).

**State Management:**
- Dgraph is source-of-truth for all pubkeys and follows.
- `kind3CreatedAt` stores the event's `created_at` for version checking (newer-wins).
- `last_attempt` tracks crawl attempts (drives stale-frontier aging); `last_db_update` records the last write.
- Relay connection state (alive, failures, backoff, retryAt) is in-memory only; lost on restart.

## Key Abstractions

**Crawler (`pkg/crawler.Crawler`):**
- Purpose: Owns the relay pool and coordinates follow-list fetches.
- Examples: `cmd/crawler/main.go` calls `FetchAndUpdateFollows`, `ReconnectRelays`.
- Pattern: Per-relay state machine with exponential backoff; subscription-based event collection; concurrent reads, mutex-serialized writes.

**Dgraph Client (`pkg/dgraph.Client`):**
- Purpose: Single access point for all graph queries and mutations.
- Examples: Every entry point calls `dgraph.NewClient(addr)` then methods on `*Client`.
- Pattern: Upsert writes wrapped in txns; read-only txns for selection/analysis; pagination helpers (`GetAllPubkeysPaginated`, `GetPubkeysWithMinFollowersPaginated`) for large result sets.

**Config (`pkg/config.Config`):**
- Purpose: Centralize environment config; persist runtime relay changes.
- Examples: `LoadConfig` reads YAML; `RemoveRelayURL` rewrites it on relay failure.
- Pattern: Viper-backed with `SetDefault`; supports hex and npub via `nip19.Decode`.

## Entry Points

**Crawler:**
- Location: `cmd/crawler/main.go`
- Triggers: `./bin/crawler`; SIGINT/SIGTERM for graceful shutdown.
- Responsibilities: Loop `GetStalePubkeys` → `FetchAndUpdateFollows` → `MarkAttempted`; report stats.

**Clusterscan:**
- Location: `cmd/clusterscan/main.go`
- Triggers: `./bin/clusterscan [flags]` (`-k`, `-depth`, `-max-bridge-weight`, `-min-cluster-size`, `-stats`, `-members`).
- Responsibilities: Build trusted set, find weak bridges, size clusters, write CSV/JSON.

**Discover Relays:**
- Location: `cmd/discover-relays/main.go`
- Triggers: `./bin/discover-relays [flags]` (`-count`, `-max-test`, `-replace`, `-dry-run`).
- Responsibilities: Pull relays from nostr.watch API / NIP-65, latency-test, update config.

**Pubkeys Exporter:**
- Location: `cmd/pubkeys/main.go`
- Triggers: `./bin/pubkeys`.
- Responsibilities: Export pubkeys with ≥1 follower to timestamped CSV.

**Healthcheck:**
- Location: `cmd/healthcheck/main.go`
- Triggers: `./bin/healthcheck [-purge] [-v] [-dgraph-addr]`.
- Responsibilities: Scan for invalid-format and duplicate pubkey nodes; optionally delete.

## Architectural Constraints

- **Threading:** Single-threaded main loop in crawler; concurrent per-relay subscription goroutines in `FetchAndUpdateFollows`; all Dgraph writes serialized under `dbUpdateMutex` (`pkg/crawler/crawler.go:54`).
- **Global state:** Relay pool (`Crawler.relays`) held in-memory and mutated in place during reconnect/dead-marking; `viper` holds global config state used by `SaveForwardRelayURL`/`RemoveRelayURL`.
- **Circular imports:** None. `cmd/*` → `pkg/crawler`, `pkg/dgraph`, `pkg/config`; `pkg/crawler` → `pkg/dgraph`; `pkg/config` is a leaf.
- **Dgraph uniqueness:** `pubkey` marked `@unique @upsert`; at most one node per pubkey across all writes (`pkg/dgraph/dgraph.go:61`).
- **Message size limits:** gRPC `MaxCallRecvMsgSize` raised to 256 MiB for large frontier reads (`pkg/dgraph/dgraph.go:39`); follow-lists >10k chunked into 200-item batches (`pkg/crawler/chunks.go:20`).
- **Stale-feed invariants:** never-attempted nodes selected via explicit `NOT has(last_attempt)` with explicit `first:` — never via `orderasc: last_attempt` (which caps at 1000 and starves stubs). Documented at `pkg/dgraph/dgraph.go:438`.

## Anti-Patterns

### Sorting the stale frontier by `last_attempt`

**What happens:** Using `orderasc: last_attempt` to surface never-crawled stubs.
**Why it's wrong:** Missing-value nodes sort last and Dgraph caps an unbounded sorted query at 1000 rows, so the crawler returns only already-crawled accounts and never expands the web of trust.
**Do this instead:** Select the frontier with an explicit `@filter(NOT has(last_attempt))` block and explicit `first:`, then top up with aged nodes (`GetStalePubkeys`, `pkg/dgraph/dgraph.go:443`).

### Large follow-lists in a single mutation

**What happens:** Writing a >10k-entry follow list in one `AddFollowers` call.
**Why it's wrong:** A single bulk query/mutation can exceed gRPC limits and time out.
**Do this instead:** Route lists >10k through `processFollowsInChunks` (200-item batches) — `pkg/crawler/crawler.go:533`.

### Treating relay errors as fatal

**What happens:** Aborting the crawl when a relay subscription or transport fails.
**Why it's wrong:** Relays are flaky; one dead relay should not stop the whole crawl.
**Do this instead:** Mark the relay dead with backoff and continue with the rest; remove after `maxConsecutiveFailures` (`markRelayDead`, `pkg/crawler/crawler.go:170`).

## Error Handling

**Strategy:** Wrap-and-propagate with `%w`; relay-level failures degrade gracefully while DB/config failures are fatal at the entry point.

**Patterns:**
- Relay errors classified as `subscriptionError` vs `transportError`; transport failures mark the relay dead (`pkg/crawler/crawler.go:419`).
- Dgraph errors wrapped with operation context and propagated to the main loop, which logs and breaks.
- Config load errors are fatal (`log.Fatalf`); invalid pubkeys in events are logged and skipped.
- Relay-timeout (`context.DeadlineExceeded`) is non-fatal — already-received events are still processed (`pkg/crawler/crawler.go:333`).

## Cross-Cutting Concerns

**Logging:** Standard `log` package. Levels by prefix (`WARN:`, `DEBUG:`), structured `METRICS:`/`RELAY_ERROR:` JSON lines; debug output guarded by `c.debug`. No raw secrets logged.
**Validation:** Pubkeys validated with `nostr.GetPublicKey` (hex, 64 chars); events verified with `event.CheckSignature()` before processing; `p`-tags de-duped.
**Authentication:** None within this module — relays are public WebSocket endpoints; trust is derived from the follow-graph, not from auth.

---

*Architecture analysis: 2026-06-09*

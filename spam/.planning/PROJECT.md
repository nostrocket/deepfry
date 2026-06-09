# deepfry — Queryable Endpoint over strfry's LMDB

## What This Is

deepfry is a read-only middleware service that exposes a [strfry](https://github.com/hoytech/strfry) Nostr relay's LMDB database as a standard, directly queryable GraphQL endpoint. It lets consumers run rich queries the Nostr `REQ` protocol cannot express — e.g. *"latest 20 kind-1 events per pubkey"* — without going through the strfry relay process. It is built for operators of the DeepFry/Humble Horse stack who run strfry and want analytic/feed queries over its event data.

## Core Value

Serve correct, rich queries over strfry's events **without ever depending on strfry's in-memory custom LMDB comparators or writing to strfry's database** — by reading only the well-defined `EventPayload` sub-DB and maintaining an independent derived index.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Read-only decoder library: open strfry env `MDB_RDONLY`, decode `EventPayload` (`0x00` raw + `0x01` zstd-dictionary), gate on `Meta.dbVersion == 3`, assert endianness matches host
- [ ] Full importer: windowed `levId` scan of `EventPayload` → derived SQLite store (short read txns per window)
- [ ] Incremental tailer: `MDB_SET_RANGE` from `lastLevId+1` to catch new inserts
- [ ] On-demand / periodic full reconciler: diff derived `lev_id` set against `EventPayload` to drop deleted events; change probe via `negentropyModificationCounter` + filesystem watch
- [ ] Replaceable & parameterized-replaceable collapse + expiration filtering in the derived store (match relay semantics)
- [ ] GraphQL API: `events()`, `latestPerAuthor()`, `stats`, with pagination and hard limit ceilings
- [ ] Version/endianness hard gate that refuses to run (loudly) on mismatch
- [ ] Docker subsystem + docker-compose integration, co-located with strfry, read-only mount of `strfry-db`

### Out of Scope

- Approach B (embed strfry/golpe code) — too tightly version-locked; only revisit if exact live replaceable/deletion semantics are required
- Approach C (shell out to `strfry scan`/`export`) — prototype-only, not the production path
- Reading/range-scanning any `Event__*` index sub-DB — relies on golpe's in-memory comparators; silently wrong from a foreign reader
- Any write transaction against strfry's environment — strfry is the sole writer; a second writer corrupts the env
- Live subscriptions / push — request/response only for v1
- Mutations / acting as a relay — deepfry is a query surface, not a relay
- Cross-architecture / big-endian support — deepfry assumed co-located with strfry on the same little-endian arch (assert, don't byte-swap) for v1
- Re-verifying event signatures on the hot path — strfry already validated on ingest

## Context

- Part of the **DeepFry** modular backend stack for Humble Horse (surrounds a stock, unmodified strfry relay with sidecar services). deepfry follows the same "never fork strfry" principle: it reads strfry's LMDB directly but treats the on-disk format as a private API.
- Target strfry DB version: **3** (`src/constants.h: CURR_DB_VERSION = 3`). Format is strfry-internal and may change between releases with no compatibility guarantee — must pin and CI-test against a specific strfry commit with a fixture DB.
- Full spec with verified on-disk encodings, required LMDB operations, and caveats lives in `spec.md` (source of truth for implementation details).
- The broader deepfry stack is written in Go; **deepfry (this service) is intentionally Rust** — a deliberate divergence chosen for this subsystem.
- Existing infrastructure: strfry at `ws://localhost:7777` with LMDB at `./strfry-db/` (default), 10 TiB sparse `mapsize`. Config convention: per-subsystem config under `~/deepfry/`.

## Constraints

- **Tech stack**: Rust — needs LMDB read bindings, zstd (dictionary decompression), SQLite, and a GraphQL server. Diverges from the Go stack by deliberate choice.
- **Correctness**: MUST NOT open or range-scan any `Event__*` sub-DB (custom comparators not present in a foreign process). Read only `EventPayload` (required) and optionally `Meta`/`Event`/`CompressionDictionary`.
- **Safety**: read-only LMDB access only (`MDB_RDONLY`); never open a write txn. Keep read txns short (scan in bounded `levId` windows) so strfry can reclaim pages and the DB file doesn't grow unbounded.
- **Compatibility**: hard version gate — refuse to run if `Meta.dbVersion != 3`. Assert `Meta.endianness` matches host; refuse on mismatch (co-located assumption).
- **Deployment**: packaged as its own Docker subsystem with a docker-compose service, co-located with strfry, mounting `strfry-db` read-only.
- **Compression**: must handle `0x01` zstd-dictionary payloads (appears after strfry offline compaction) even though default deployments are `0x00` uncompressed.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Approach A (derived store; read only `EventPayload`) | Avoids strfry's in-memory custom comparators entirely; never writes to / corrupts strfry; survives restarts & compactions | — Pending |
| Derived store = SQLite | Single-node, simplest; fits the windowed-query workload; spec's v1 default | — Pending |
| Query API = GraphQL | Self-documenting, flexible filtering; expresses `latestPerAuthor` cleanly | — Pending |
| Request/response only (no subscriptions) for v1 | Keeps v1 simple; derived store is eventually-consistent | — Pending |
| Co-located, same arch (assert endianness) | Avoids byte-swap/cross-arch complexity; matches deployment reality | — Pending |
| Language = Rust | Deliberate divergence from the Go stack for this subsystem | — Pending |
| Deletion reconcile cadence = hours / on-demand | Deletions are rare; tolerant staleness window keeps scan load on strfry low | — Pending |
| Packaged as Docker subsystem + compose, read-only `strfry-db` mount | Matches existing deepfry infra conventions | — Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-09 after initialization*

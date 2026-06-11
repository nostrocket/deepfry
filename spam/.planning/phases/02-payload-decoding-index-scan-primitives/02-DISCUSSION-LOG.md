# Phase 2: Payload Decoding & Index Scan Primitives - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-11
**Phase:** 2-Payload Decoding & Index Scan Primitives
**Areas discussed:** Decoder output shape, Scan primitive contract, Dictionary cache, Malformed payload policy

---

## Decoder output shape

### Q1 — Primary decoder output

| Option | Description | Selected |
|--------|-------------|----------|
| Typed struct + raw bytes | Deserialize NostrEvent AND retain decoded JSON bytes | ✓ |
| Typed struct only | NostrEvent only; re-serialize from struct when needed | |
| Raw JSON passthrough | Return decoded bytes verbatim; parse fields lazily | |

**User's choice:** Typed struct + raw bytes.

### Q2 — Deserialization strictness + tags typing

| Option | Description | Selected |
|--------|-------------|----------|
| Lenient, tags as Vec<Vec<String>> | 7 known fields required, unknowns ignored; tags = Vec<Vec<String>> | ✓ |
| Strict, reject unknowns | deny_unknown_fields | |
| tags as raw JSON Value | Keep tags as serde_json::Value, parse per-tag on demand | |

**User's choice:** Lenient, tags as `Vec<Vec<String>>`.

---

## Scan primitive contract

### Q1 — Per-entry yield

| Option | Description | Selected |
|--------|-------------|----------|
| (key bytes, levId) pairs | No hydration; Phase 3 hydrates + builds cursors | ✓ |
| levIds only | Just ordered levIds (Phase 1 style) | |
| Fully hydrated events | Scan + lookup + decode in one call | |

**User's choice:** `(key bytes, levId)` pairs.

### Q2 — Bounding & positioning

**Initial AskUserQuestion was paused** — user asked for an explanation of what
"scan bounding and positioning" means and how Phase 1's existing code does it.
Provided a walkthrough of `scan_lev_ids_for_index` (full, unpositioned) and
`seek_first_ge_lev_id` (MDB_SET_RANGE, single entry) in `src/lmdb/indexes.rs`, and
the three knobs Phase 2 adds (count limit, start key, direction).

**User's choice:** `limit + start-key + direction (forward/reverse)`.
**Notes:** User added a hard requirement: **`limit = 0` must mean "no limit"** (unbounded scan).

### Q3 — Reconciling `limit = 0` with LMDB-09 (short txns)

| Option | Description | Selected |
|--------|-------------|----------|
| Internal windowing | Unbounded scan in bounded batches; close+reopen txn, resume by key | ✓ |
| Single open txn | One read txn for the whole scan (violates LMDB-09) | |
| Caller streams pages | Primitive always returns one bounded page + cursor; caller loops | |

**User's choice:** Internal windowing.

---

## Dictionary cache

### Q1 — zstd dictionary loading/caching

| Option | Description | Selected |
|--------|-------------|----------|
| Lazy load + cache by dictId | Build/cache DecoderDictionary on first 0x01 with that dictId | ✓ |
| Eager load all at startup | Pre-build every DDict at startup | |
| No cache | Load + digest dict on every 0x01 decode | |

**User's choice:** Lazy load + cache by `dictId` (cache must be concurrency-safe for Phase 4).

---

## Malformed payload policy

### Q1 — Single payload decode failure

| Option | Description | Selected |
|--------|-------------|----------|
| Skip + log (warn), count it | Drop bad event, warn with levId + reason, track skipped-count | ✓ |
| Fail the whole query | Any decode failure aborts the query | |
| Return per-event error marker | Yield Result per event; caller surfaces partial errors | |

**User's choice:** Skip + log (warn), count it.

---

## Claude's Discretion

- Internal window batch size for `limit = 0` scans (default ± optional config knob).
- Concurrency primitive for the dictionary cache.
- Decode error-type design (`thiserror`), module layout, exact scan-primitive signatures.
- Whether the scan primitive owns its `RoTxn` or borrows one (recommended: owns it).
- Whether to do startup `0x01` sampling/log now or defer to Phase 5.

## Deferred Ideas

- Deletion reconciliation / staleness window (spec §6.5/§6.6) — Phase 3+.
- NIP-40 expiration filtering (spec §6.7) — Phase 3.
- Startup `0x01` sampling + richer drift surfacing — Phase 5 (OPS).
- Doc-sync: stale `rusqlite`/SQLite wording + amended LMDB-05 wording in CLAUDE.md — non-code cleanup.

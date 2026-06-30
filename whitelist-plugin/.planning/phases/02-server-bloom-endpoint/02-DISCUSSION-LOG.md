# Phase 2: Server Bloom Endpoint - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-30
**Phase:** 2-Server Bloom Endpoint
**Areas discussed:** Rebuild hook, Swap coupling, Serving, Config, Stats staleness, Not-ready response

---

## Rebuild Hook & Ownership

| Option | Description | Selected |
|--------|-------------|----------|
| Refresher callback | Add `onRefresh(keys)` to `WhitelistRefresher`; server registers it to rebuild + swap the filter. Separates concerns, single trigger point. | ✓ |
| Whitelist owns bloom | Move the filter into `Whitelist`; `UpdateKeys` rebuilds the bloom. Tightest coupling, build cost inside the swap. | |
| Server polls generation | Server-side goroutine rebuilds on detected change. Decoupled but adds a second timer + change detection. | |

**User's choice:** Refresher callback
**Notes:** Callback fires only after a successful `UpdateKeys`; failed refresh keeps the prior filter. Naturally became the place to also fix `/stats` (see below).

---

## Swap Coupling

| Option | Description | Selected |
|--------|-------------|----------|
| Two independent pointers | Leave `wl.list` as-is; add a separate `atomic.Pointer[Filter]`. Simplest, untouched `/check` path; eventually consistent. | ✓ |
| One combined snapshot | `{map, filter}` behind one atomic pointer; always same generation, but refactors proven `Whitelist` internals. | |

**User's choice:** Two independent pointers
**Notes:** Brief map/filter generation skew is acceptable; both reads stay lock-free.

---

## /bloom Serving

| Option | Description | Selected |
|--------|-------------|----------|
| Cache bytes per generation | Pre-serialize DFBF bytes on swap; each request writes cached bytes + Content-Length. Alloc-free handler. | ✓ |
| WriteTo per request | Call `Filter.WriteTo(w)` per request. Simpler state, slightly more CPU; fine at ~6h polling. | |

**User's choice:** Cache bytes per generation
**Notes:** `Content-Type: application/octet-stream`, `ETag` from `Filter.ETag()`, `If-None-Match` → 304.

---

## Config Surface (SRV-04)

| Option | Description | Selected |
|--------|-------------|----------|
| Single `fp_rate` key | Add `bloom_fp_rate` (default 1e-6); size via `NewBuilder(wl.Len(), fp)`. Minimal, matches SRV-04. | ✓ |
| fp_rate + capacity floor | Add `bloom_min_capacity` too, to avoid undersizing a tiny whitelist. More knobs. | |

**User's choice:** Single `bloom_fp_rate` key
**Notes:** Whitelist is ~1.5M keys in practice — no floor needed.

---

## Stats Staleness

| Option | Description | Selected |
|--------|-------------|----------|
| Fix stats too | `onRefresh` callback also updates `entries`/`last_refresh` each cycle, fixing latent staleness. Shape unchanged. | ✓ |
| Leave stats untouched | Strictly honor "exactly as before"; staleness stays out of scope. | |

**User's choice:** Fix stats too
**Notes:** JSON shape unchanged; values become accurate per-refresh. Flagged as a sanctioned deviation from ROADMAP success-criterion 5 wording (documented in CONTEXT D-10).

---

## Not-Ready Response

| Option | Description | Selected |
|--------|-------------|----------|
| 503 like /health | Return 503 + `{status:loading}` until first filter exists; plugin treats 503 as "use persisted". | ✓ |
| Serve empty filter | Always serve a valid (empty) filter; never errors, but rejects everyone until first build. | |

**User's choice:** 503 like /health
**Notes:** Mirrors existing `/health` loading behavior; defines the Phase 3 plugin fallback contract.

---

## Claude's Discretion

- Exact method/field names (`SetOnRefresh`, `SwapFilter`, `SetStats`) and whether cached bytes live in a `{filter, bytes}` struct vs a parallel pointer — provided `whitelist.list` stays untouched.
- Whether `n` for `NewBuilder` is captured before/after `UpdateKeys` (same count either way).
- Mux registration style for `GET /bloom` (match existing `Handler()` pattern).

## Deferred Ideas

- Per-cycle filter metrics (generation age, bloom size) — already GATE-F2 (v2/future).
- gzip/`Accept-Encoding` on `/bloom` — out of scope unless later needed.
- Capacity floor / extra sizing knobs — rejected for this phase.

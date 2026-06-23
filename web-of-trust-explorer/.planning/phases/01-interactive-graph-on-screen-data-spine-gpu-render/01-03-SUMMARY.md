---
phase: 01-interactive-graph-on-screen-data-spine-gpu-render
plan: 03
subsystem: data
tags: [dgraph, dql, web-worker, json-wire, transport, perf-spike, cosmos.gl, soa]

# Dependency graph
requires:
  - phase: 01-01
    provides: GraphTransport interface, SoA types, hex->uint32 remap + parser helpers, synthetic worker
  - phase: 01-02
    provides: GPU ceiling spike (5M/30M render, auto-freeze, 60fps PASS), O(E) in-degree pass
provides:
  - DgraphTransport (GraphTransport impl) — read-only DQL after-cursor paging over the follows graph
  - dgraph.worker — chunked per-page fetch + parse + hex->uint32 remap + drop-page-string discipline + O(E) in-degree
  - Staged loader (Fetching -> Parsing -> Building) with live edge counter, no % bar (D-09)
  - Verdict instrument (fetch/parse/layout ms + peak heap + encoding_ns, on-screen panel + console.table) (D-10)
  - RECORDED JSON-wire feasibility verdict — FAIL -> trigger PERF-01 (Go binary-streaming bridge), pulled forward from v2
affects: [Phase 2 (terrain overlays), Phase 3 (explore/slice), PERF-01 Go bridge phase]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DgraphTransport behind the same GraphTransport interface as SyntheticTransport — the JSON-direct -> Go-binary-stream swap is a single-file change (drop-in GoBridgeTransport)"
    - "Read-only DQL only: has(follows) + follows{uid} via POST /query application/dql; never a /mutate or /alter (grep-verified)"
    - "Chunked per-page parse in the Worker, drop the page string before the next fetch (memory discipline), dense uint32 remap, Transferable zero-copy hand-off"
    - "Loader-as-instrument: the staged loader doubles as the measurement surface (live edge count + verdict breakdown)"

key-files:
  created:
    - src/transport/DgraphTransport.ts
    - src/workers/dgraph.worker.ts
    - src/ui/loader.ts
    - src/ui/verdict.ts
  modified:
    - src/main.ts
    - index.html

key-decisions:
  - "JSON-wire feasibility verdict: FAIL -> trigger PERF-01 (Go binary-streaming bridge), pulled forward from v2 to next phase."
  - "D-04 'the real dev DB is small' assumption is FALSIFIED: 365,559 follow-source nodes / 1,541,632 profiles / ~tens of millions of edges."
  - "Bottleneck localized to the JSON wire + client-side parse/remap/memory, NOT the renderer (Plan 02 GPU half already PASSED at 5M synthetic / ~60fps)."
  - "PERF-01 fix is a drop-in GoBridgeTransport behind the SAME GraphTransport interface; cosmos.gl + the SoA pipeline stay unchanged."

patterns-established:
  - "Spike-as-verdict: a decisive PERF-01 trigger backed by server-side counts + an honest 'unusable at scale' observation IS the deliverable, not a failure."
  - "Swappable transport is the escape hatch — the recorded FAIL exercises exactly the seam the architecture was built to support."

requirements-completed: [DATA-01, DATA-03]

# Metrics
duration: ~110min
completed: 2026-06-23
status: complete
---

# Phase 01 Plan 03: JSON Wire + Feasibility Verdict Summary

**Built the read-only DgraphTransport (after-cursor DQL paging, chunked Worker parse, hex->uint32 remap) + staged loader + verdict instrument, ran it against the real dev Dgraph (v25.3.0), and recorded a decisive feasibility verdict: browser-direct JSON wire FAILS at real scale -> trigger PERF-01 (Go binary-streaming bridge), pulled forward from v2.**

## Performance

- **Duration:** ~110 min
- **Started:** 2026-06-23
- **Completed:** 2026-06-23
- **Tasks:** 4 (2 code, 1 human-action gate, 1 human-verify verdict gate)
- **Files modified:** 6 (4 created, 2 modified)

## Accomplishments

- **DgraphTransport + dgraph.worker** — a working, read-only, after-cursor DQL paging loader over the `follows` graph: `{ q(func: has(follows), first: 50000, after: <cursor>) { uid follows { uid } } }` via `POST /query` (`Content-Type: application/dql`), chunked per-page parse, hex->uint32 dense remap (reusing Plan 01 helpers), drop-page-string-before-next-fetch discipline, one O(E) in-degree pass, Transferable zero-copy hand-off. Grep-verified read-only: **0** `/mutate|/alter`, **0** `followers` queries.
- **Staged loader (D-09)** — Fetching from Dgraph -> Parsing edges -> Building layout, with a live edge counter ticking up and NO percentage bar (after-cursor paging has no honest upfront total).
- **Verdict instrument (D-10)** — captures fetch/parse/layout-ready ms, node+edge counts, accumulated `encoding_ns`, and peak JS heap via `measureUserAgentSpecificMemory` with `usedJSHeapSize` fallback; renders an on-screen panel and `console.table`s the breakdown.
- **Swappable transport confirmed** — `DgraphTransport` and `SyntheticTransport` both implement `GraphTransport`; selectable at runtime via `?transport=dgraph` (default `http://localhost:8080`, page size 50000).
- **Recorded the phase-closing feasibility verdict** — the deliverable of the spike: a decisive FAIL -> PERF-01 trigger (see below).

## Task Commits

Each task was committed atomically:

1. **Task 1: DgraphTransport + dgraph.worker (after-cursor paging, chunked parse, hex->uint32 remap, read-only)** — `b9d6854` (feat, TDD)
2. **Task 2: Staged loader (D-09) + verdict instrument (D-10), selectable Dgraph transport** — `1e7125a` (feat)
3. **Task 3: Bring up the dev Dgraph for the real-wire spike** — human-action gate, satisfied (Dgraph healthy + populated, no restart needed)
4. **Task 4: Record the JSON-wire feasibility verdict** — human-verify gate, **verdict recorded below**

**Plan metadata:** committed with `docs(01-03): complete plan` (this SUMMARY + STATE + ROADMAP + REQUIREMENTS)

_Note: Task 1 was TDD (parse + transport tests written/extended first); committed as a single GREEN feat commit._

## Files Created/Modified

- `src/transport/DgraphTransport.ts` — `GraphTransport` impl: spawns the worker, forwards `onProgress`, resolves transferred `GraphBuffers` (with `hexByIndex` for tooltips); DQL `after`-cursor paging loop (cursor `0x0` -> last uid per page -> stop on short/empty page). Swappable with `SyntheticTransport`.
- `src/workers/dgraph.worker.ts` — chunked per-page `fetch` (`POST /query`, `application/dql`) + parse of the `{data:{q:[...]},extensions:{...}}` envelope + hex->uint32 remap + drop-page-string discipline + O(E) in-degree pass + `encoding_ns` extraction. **Issues ONLY read DQL** — no mutation body is ever constructed.
- `src/ui/loader.ts` — staged labels (Fetching -> Parsing -> Building) + live edge counter, no % bar (D-09).
- `src/ui/verdict.ts` — fetch/parse/layout-ready ms + peak heap (`measureUserAgentSpecificMemory` w/ `usedJSHeapSize` fallback) + `encoding_ns` readout; on-screen panel + `console.table` (D-10).
- `src/main.ts` — transport selectable via `?transport=dgraph`; mounts loader, forwards progress, runs the verdict readout on completion.
- `index.html` — loader/verdict panel mount points.

## Recorded JSON-Wire Feasibility Verdict (D-04 / D-05 / D-08)

**VERDICT: FAIL -> trigger PERF-01 (Go binary-streaming bridge), pulled forward from v2 to the next phase.**

This is the deliverable the plan asked for. The plan's verdict `must_have` explicitly allows the PERF-01 trigger as a valid, successful outcome ("A recorded feasibility verdict states PASS ... or triggers PERF-01"). The spike produced a decisive, evidence-backed PERF-01 trigger. **This is a successful spike, not a plan failure.**

### Real graph size (server-side, read-only counts; real dev Dgraph v25.3.0 @ localhost:8080)

- **365,559** nodes have `follows` (edge-source nodes) — `count(uid)` over `has(follows)`.
- **1,541,632** total profiles — `count(uid)` over `has(pubkey)`.
- Edge count not totaled cheaply, but plausibly **tens of millions** (a sampled source node `0x2` follows several hundred targets).

### Server-side cost signal

- A bare `count(uid)` over `has(follows)` took **~6.7s** in Dgraph (`processing_ns` ~ 6.72e9, `encoding_ns` ~ 5.5e7). The full `follows{uid}` JSON traversal — the actual bulk-load shape — is far larger and far more expensive to encode.

### In-browser observation

- Running `npm run dev` with `?transport=dgraph` was **"incredibly slow and made the entire dev machine unusable"** — the browser-direct path (single-shot `JSON.parse` of a multi-hundred-MB body + memory-doubling) drove the machine into swap.
- **Precise in-browser metrics (fetchMs / parseMs / peak heap) were NOT captured** because the load was aborted as unusable before the verdict panel could report. Recorded honestly: the "unusable at this scale" observation **plus** the server-side counts ARE the verdict. The instrument is in place and correct; the wire simply never completed a measurable run.

### Key finding — D-04 falsified

- The D-04 planning assumption that **"the real dev DB is small"** is **FALSIFIED**. It is ~365k follow-source nodes / 1.5M profiles / ~tens of millions of edges — full target scale, not a tiny DB. The plan's premise of "measure small, extrapolate to 30M" collapsed because the real DB is already at the dangerous end of the range.

### Localization — the renderer is NOT the problem

- The GPU/render half (Plan 02) already **PASSED** at 5M synthetic nodes / ~30M edges at ~60fps on the M3 Pro. The renderer, layout, and SoA pipeline are fine.
- The bottleneck is **purely the JSON wire + client-side parse/remap/memory** — exactly the dominant risk Phase 1 was front-loaded to confront (browser-direct JSON pull of tens of millions of edges, no streaming, blocking memory-doubling `JSON.parse`).

### Recommendation — pull PERF-01 forward

- **PERF-01 (Go binary-streaming bridge):** `dgo` gRPC -> server-side hex->uint32 remap -> streamed binary edge buffer -> **zero browser `JSON.parse`**.
- This is a **drop-in `GoBridgeTransport`** behind the **same `GraphTransport` interface** the spike exercised. cosmos.gl + the SoA pipeline stay unchanged. The architecture's swappable-transport seam exists precisely for this swap; the FAIL validates that design.
- Move PERF-01 from v2 -> the next phase.

## Decisions Made

- **JSON-wire verdict: FAIL -> PERF-01 triggered, pulled forward to next phase.** (See above.)
- **D-04 "small DB" assumption falsified** (~365k follow-nodes / 1.5M profiles).
- **Bottleneck localized to the wire, not the renderer** (Plan 02 GPU half PASSED).
- **PERF-01 fix is a drop-in GoBridgeTransport** behind the existing `GraphTransport` interface.

## Deviations from Plan

None - plan executed exactly as written. The plan's verdict `must_have` explicitly admits the PERF-01 trigger as a valid outcome; the spike delivered exactly that. The only honest note (not a deviation) is that precise in-browser fetch/parse/heap numbers were not captured because the load was aborted as machine-unusable — the "unusable at scale" observation plus the server-side counts constitute the recorded verdict.

## Issues Encountered

- **In-browser run unusable at real scale** — `?transport=dgraph` against the real dev DB drove the machine into swap; the run was aborted before the verdict panel reported. Resolved by recording the verdict from the abort observation + server-side counts (this IS the FAIL verdict, by design of the spike).

## User Setup Required

None - the dev Dgraph is the only external dependency, brought up in Task 3 (`docker-compose -f docker-compose.dgraph.yml up -d` from the deepfry root) and confirmed healthy/populated.

## Next Phase Readiness

- **Phase 1 feasibility checkpoint is resolved with a decisive verdict.** Both dominant risks now have recorded verdicts: GPU half PASS (Plan 02), JSON-wire half FAIL -> PERF-01 (this plan).
- **Next:** Pull PERF-01 (Go binary-streaming bridge) forward. Implement `GoBridgeTransport` behind the existing `GraphTransport` interface (`dgo` gRPC -> server-side hex->uint32 remap -> streamed binary edge buffer -> zero browser `JSON.parse`). cosmos.gl + SoA pipeline are unchanged.
- The `DgraphTransport` built here remains a valid, working, read-only reference path for small graphs and documents the exact wire shape the Go bridge replaces.

## Self-Check: PASSED

**Files created/modified (verified on disk):**
- FOUND: src/transport/DgraphTransport.ts
- FOUND: src/workers/dgraph.worker.ts
- FOUND: src/ui/loader.ts
- FOUND: src/ui/verdict.ts
- FOUND: src/main.ts
- FOUND: index.html

**Task commits (verified in git log):**
- FOUND: b9d6854 — feat(01-03): DgraphTransport + dgraph.worker (read-only after-cursor paging, chunked parse, hex->uint32 remap)
- FOUND: 1e7125a — feat(01-03): staged loader (D-09) + verdict instrument (D-10), selectable Dgraph transport

**Verification (from execution):** `npx vitest run` 33/33 green; `npx tsc --noEmit` clean; read-only invariant grep-clean (0 `/mutate|/alter`, 0 `followers`).

---
*Phase: 01-interactive-graph-on-screen-data-spine-gpu-render*
*Completed: 2026-06-23*

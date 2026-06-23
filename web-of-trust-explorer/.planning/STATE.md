---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_phase: 01.1
current_phase_name: go-binary-streaming-bridge-perf-01
status: executing
stopped_at: Phase 01.1 context gathered
last_updated: "2026-06-23T06:51:27.805Z"
last_activity: 2026-06-23
last_activity_desc: Phase 01.1 execution started
progress:
  total_phases: 4
  completed_phases: 1
  total_plans: 6
  completed_plans: 3
  percent: 25
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-22)

**Core value:** Smooth 60fps interaction with the whole follow-graph at once, so a developer can see its terrain — hubs, clusters, bridges, dense vs sparse regions.
**Current focus:** Phase 01.1 — go-binary-streaming-bridge-perf-01

## Current Position

Phase: 01.1 (go-binary-streaming-bridge-perf-01) — EXECUTING
Plan: 1 of 3
Status: Executing Phase 01.1
Last activity: 2026-06-23 — Phase 01.1 execution started

Progress: [██████████] 100%

## Performance Metrics

**Velocity:**

- Total plans completed: 3
- Average duration: - min
- Total execution time: 0.0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01 | 3 | - | - |

**Recent Trend:**

- Last 5 plans: none yet
- Trend: -

*Updated after each plan completion*
| Phase 01 P01 | 18 | 5 tasks | 16 files |
| Phase 01 P02 | 6 | 4 tasks | 8 files |
| Phase 01 P03 | 110 | 4 tasks | 6 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Roadmap]: cosmos.gl (`@cosmos.gl/graph`, WebGL2) is the GPU layout+render engine; data source behind a swappable transport interface so JSON-direct → Go-binary-stream is a one-file swap.
- [Roadmap]: Remap hex pubkeys → dense uint32 at load; structure-of-arrays typed buffers; query only `follows` (derive followers/in-degree client-side to avoid `@hasInverse` double-count); DQL `after`-cursor paging (not offset).
- [Roadmap]: Topology-static / style-dynamic — overlays rewrite only style buffers; filters hide/dim and NEVER re-layout; analytics run one-shot in a Web Worker.
- [Roadmap]: Go binary-streaming bridge (PERF-01) deferred to v2; gated on the Phase 1 feasibility verdict against synthetic full-scale data.
- [Phase 1]: Pinned vite@^7.3.5 — Vite 8 Rolldown can't resolve cosmos.gl's CJS dep gl-bench; Vite 7 (Rollup+esbuild) handles the interop.
- [Phase 1]: cosmos.gl 3.0.0 needs render() (not create()+start()) to start the draw loop and allocate the hover-picking FBO.
- [Phase 1]: @cosmos.gl/graph@3.0.0 confirmed legitimate (pre-install human gate) and working in Chrome (render human gate).
- [Phase 1 / 01-02]: **GPU-half feasibility verdict — 60fps PASS.** WebGL2/cosmos.gl held ~60fps under pan/zoom/hover at 5M nodes / ~30M edges on the reference machine (M3 Pro / Chrome, D-06/D-07); layout auto-settled + auto-froze without user action (D-11/D-12). **Resolves Open Question 1 in favor of WebGL2 — WebGPU compute-shader escalation (Phase-N) NOT triggered.** (Qualitative-but-confirmed headline PASS; no precise per-axis FPS numbers recorded.)
- [Phase 1 / 01-02]: In-degree derived in one O(E) pass (src/graph/generator.ts computeInDegree → Uint32Array, sum === edgeCount) in the worker pre-Float32, transferred zero-copy; no followers query, no per-node objects (D-08).
- [Phase 1 / 01-03]: **JSON-wire feasibility verdict — FAIL → trigger PERF-01 (Go binary-streaming bridge), pulled forward from v2 to next phase.** Browser-direct DQL JSON load against the real dev Dgraph (v25.3.0) drove the dev machine into swap and was unusable; the single-shot memory-doubling JSON.parse of a multi-hundred-MB body is the wall. Precise in-browser fetch/parse/heap numbers NOT captured (load aborted as unusable) — the "unusable at scale" observation + server-side counts ARE the verdict. (A successful, decisive spike outcome — the plan's verdict must_have explicitly admits the PERF-01 trigger.)
- [Phase 1 / 01-03]: **D-04 "the real dev DB is small" assumption FALSIFIED.** Server-side read-only counts: 365,559 follow-source nodes (has(follows)); 1,541,632 total profiles (has(pubkey)); ~tens of millions of edges. A bare count(uid) over has(follows) took ~6.7s in Dgraph (processing_ns ≈ 6.72e9). Full target scale, not a tiny DB.
- [Phase 1 / 01-03]: **Bottleneck localized to the JSON wire + client-side parse/remap/memory, NOT the renderer** (Plan 02 GPU half PASSED at 5M synthetic / ~60fps). PERF-01 fix is a drop-in GoBridgeTransport behind the SAME GraphTransport interface (dgo gRPC → server-side hex→uint32 remap → streamed binary edge buffer → zero browser JSON.parse); cosmos.gl + SoA pipeline stay unchanged.
- [Phase 1 / 01-03]: DgraphTransport built read-only and grep-verified (0 /mutate|/alter, 0 followers query) — after-cursor DQL paging over has(follows)+follows{uid}, chunked Worker parse, hex→uint32 remap, drop-page-string discipline, Transferable zero-copy; swappable with SyntheticTransport behind GraphTransport.

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- [Phase 1]: ~~Dominant risk is browser-direct JSON pull of tens of millions of edges (no streaming, blocking JSON.parse)~~ — RESOLVED in 01-03 with a decisive verdict: browser-direct JSON wire FAILS at real scale (machine driven into swap, unusable) → PERF-01 (Go binary-streaming bridge) triggered and pulled forward from v2. The architecture's swappable GraphTransport seam exists precisely for this swap (drop-in GoBridgeTransport).
- [Phase 1]: ~~cosmos.gl has a stated GPU simulation-space ceiling that may not fit several million nodes~~ — RESOLVED in 01-02: WebGL2 held ~60fps at 5M/30M on the M3 Pro and auto-settled+froze; Open Question 1 closed in favor of WebGL2, no WebGPU escalation.

- [Phase 01.1 — OPEN BLOCKER, 2026-06-23]: Bridge built and committed (Waves 1–3, through commit 8e0e51c). **Server/wire half = PASS** (live, real dev Dgraph): streamed 1,526,983 nodes / 26,328,822 edges as a 290 MB binary frame (magic WOTB v1), HTTP 200, fetch+compute+stream ≈ 126 s, client peak RSS 5 MB, no swap; server-side array Louvain ran to completion at full scale (1,239 communities — risk A2 resolved). Headless decode+buildGraphBuffers against the real frame PASS (all 26M link indices in-bounds, build 49 ms). **In-browser half = FAIL → user-reported measured browser heap ≈ 3 GB, which degrades the whole dev machine.** This is NOT the JSON-parse wall (that's gone) — it is excess in-browser memory the transport/middleware should not require.
  - **User architectural verdict (2026-06-23):** "3 GB is far too high and not necessary if the middleware was correctly architected." User STOPPED autonomous mode here to own the memory architecture decision before any fix is implemented. Do NOT just patch the transport — revisit the architecture.
  - **Known avoidable waste in current GoBridgeTransport (our code, not cosmos.gl):** (a) the full 290 MB frame stays pinned for the session because the small attribute arrays are returned as VIEWS into it; (b) links are duplicated — uint32 in the pinned frame + a separate 210 MB float32 copy; (c) eager hexByIndex builds ~1.5M strings upfront (≈150–200 MB + a multi-second main-thread freeze); (d) chunks[]+frame held simultaneously → ~580 MB transient receive peak. Together ≈ 700 MB+ of avoidable transport-side memory — but that alone does NOT explain 3 GB.
  - **Likely dominant term = cosmos.gl's own internal edge structures at 26M edges** (force-layout adjacency, sorted both directions, degree textures) — engine-inherent, not fixed by the bridge. Open architectural question the user is deciding: whether "correctly architected middleware" means (i) eliminate the transport-side duplication above (~700 MB win, keeps live layout), and/or (ii) a deeper change — e.g. ship a reduced/LOD or server-pre-laid-out graph so the browser RENDERS rather than runs the force sim (cf. the deck.gl render-only "Alternatives Considered" path), trading the live-layout bet for a much smaller browser footprint. NOT YET DECIDED.
  - **Env note:** DeepFry whitelist-server occupies :8081 (the bridge default). Live run used `bridge -listen 127.0.0.1:8082` + an UNCOMMITTED vite.config.ts edit pointing `server.proxy['/graph.bin']` → :8082. On resume: commit that port change, free 8081, or pass `-listen 127.0.0.1:8081` to match the committed config.
  - **PERF-01 live verdict status: PENDING** — server/wire PASS recorded; in-browser PASS blocked on the memory-architecture decision above. 01.1-03-SUMMARY.md remains `pending-human-verification`.
- [NEXT]: PERF-01 (Go binary-streaming bridge) is now the priority for the next phase — pulled forward from v2 by the 01-03 FAIL verdict. Implement GoBridgeTransport behind the existing GraphTransport interface; cosmos.gl + SoA pipeline unchanged.

### Roadmap Evolution

- Phase 01.1 inserted after Phase 1: Go binary-streaming bridge (PERF-01) pulled forward — browser-direct JSON wire FAILed Phase 1 feasibility verdict at real scale (URGENT)

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| Performance | PERF-01 Go binary-streaming bridge (escape hatch, transport-only) | **PULLED FORWARD (no longer deferred)** — 01-03 JSON-wire verdict FAIL triggered it; now the next-phase priority | Pulled forward 2026-06-23 (01-03 verdict) |

## Session Continuity

Last session: 2026-06-23T06:29:50.073Z
Stopped at: Phase 01.1 context gathered
Resume file: .planning/phases/01.1-go-binary-streaming-bridge-perf-01/01.1-CONTEXT.md

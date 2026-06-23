---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_phase: 01
current_phase_name: interactive-graph-on-screen-data-spine-gpu-render
status: executing
stopped_at: Completed 01-02-PLAN.md (GPU ceiling spike — 60fps verdict PASS)
last_updated: "2026-06-23T04:16:20.000Z"
last_activity: 2026-06-23
last_activity_desc: Plan 01-02 complete (5M/30M GPU render, auto-freeze, 60fps PASS — Open Question 1 resolved)
progress:
  total_phases: 3
  completed_phases: 0
  total_plans: 3
  completed_plans: 2
  percent: 67
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-22)

**Core value:** Smooth 60fps interaction with the whole follow-graph at once, so a developer can see its terrain — hubs, clusters, bridges, dense vs sparse regions.
**Current focus:** Phase 01 — interactive-graph-on-screen-data-spine-gpu-render

## Current Position

Phase: 01 (interactive-graph-on-screen-data-spine-gpu-render) — EXECUTING
Plan: 3 of 3
Status: Plan 01-02 complete (GPU ceiling spike, 60fps verdict PASS); ready to execute Plan 03 (JSON wire + feasibility verdict)
Last activity: 2026-06-23 — Plan 01-02 complete (5M/30M BA render, O(E) in-degree, auto-freeze, Run/Pause+Fit+tooltip, 60fps PASS, vitest 28/28)

Progress: [███████░░░] 67%

## Performance Metrics

**Velocity:**

- Total plans completed: 0
- Average duration: - min
- Total execution time: 0.0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**

- Last 5 plans: none yet
- Trend: -

*Updated after each plan completion*
| Phase 01 P01 | 18 | 5 tasks | 16 files |
| Phase 01 P02 | 6 | 4 tasks | 8 files |

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

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- [Phase 1]: Dominant risk is browser-direct JSON pull of tens of millions of edges (no streaming, blocking JSON.parse). Phase 1 must validate against synthetic ~5M-node/~30M-edge data, not the dev DB, and record a load-time verdict.
- [Phase 1]: ~~cosmos.gl has a stated GPU simulation-space ceiling that may not fit several million nodes~~ — RESOLVED in 01-02: WebGL2 held ~60fps at 5M/30M on the M3 Pro and auto-settled+froze; Open Question 1 closed in favor of WebGL2, no WebGPU escalation. (The JSON-wire / peak-heap memory verdict — DATA-03 full — is still pending Plan 03.)

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| Performance | PERF-01 Go binary-streaming bridge (escape hatch, transport-only) | Deferred to v2 — gated on Phase 1 verdict | Roadmap (2026-06-22) |

## Session Continuity

Last session: 2026-06-23T04:16:20.000Z
Stopped at: Completed 01-02-PLAN.md (GPU ceiling spike — 60fps verdict PASS, Open Question 1 resolved)
Resume file: None

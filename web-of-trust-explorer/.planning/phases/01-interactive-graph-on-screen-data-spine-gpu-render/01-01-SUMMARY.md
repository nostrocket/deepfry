---
phase: 01-interactive-graph-on-screen-data-spine-gpu-render
plan: 01
subsystem: ui
tags: [vite, typescript, cosmos.gl, webgl2, web-worker, vitest, barabasi-albert, soa-buffers, dgraph-dql]

# Dependency graph
requires: []
provides:
  - "Vite + TypeScript walking-skeleton app that renders a small synthetic graph on the GPU via @cosmos.gl/graph 3.0.0 and pans/zooms"
  - "Swappable GraphTransport interface (load(onProgress) -> GraphBuffers) — the only data path in main.ts"
  - "SoA typed-buffer shapes (GraphBuffers) + Float32 index-precision guard MAX_NODE_INDEX = 16_777_216"
  - "Barabasi-Albert synthetic generator (mulberry32-seeded, m=6, parameterized by nodeCount) in a Web Worker with zero-copy Transferable hand-off"
  - "SyntheticTransport (small ~1000-node variant) driving the BA worker"
  - "hex->uint32 dense remap helper + DQL-envelope parser (pure, exported) for Plan 03's DgraphTransport to reuse"
  - "cosmos.gl render adapter (src/graph/cosmos.ts) exposing the Graph instance for Plan 02 to attach run/pause/fit/hover"
  - "COOP same-origin + COEP require-corp Vite headers (cross-origin isolation for Plan 03 memory measurement)"
  - "Wave-0 vitest CPU-pipeline suite: remap, generator, parse, transport, precision (21/21 green)"
affects: [01-02-PLAN, 01-03-PLAN, "Phase 2 Terrain Overlays", "Phase 3 Explore & Slice"]

# Tech tracking
tech-stack:
  added:
    - "@cosmos.gl/graph@3.0.0 (GPU force-layout + render engine; OpenJS rename of @cosmograph/cosmos)"
    - "vite@^7.3.5 (pinned — see Deviation 1)"
    - "vitest (dev; CPU-pipeline unit tests)"
    - "typescript 5.x (vanilla-ts template)"
  patterns:
    - "Swappable transport: all data reaches main.ts only through the GraphTransport interface (synthetic now, Dgraph in Plan 03 — one-file swap)"
    - "Structure-of-arrays typed buffers (Float32Array positions, Uint32/Float32 links) — no per-node heap objects"
    - "Float32 precision guard: assert nodeCount < MAX_NODE_INDEX (2^24) before building any Float32 link view"
    - "Worker -> main zero-copy hand-off via Transferable ArrayBuffers"
    - "cosmos.gl owns the <canvas> imperatively; app shell is read-only on the render loop (no React)"

key-files:
  created:
    - "package.json — pins @cosmos.gl/graph@3.0.0, vite@^7.3.5, vitest"
    - "vite.config.ts — COOP same-origin + COEP require-corp server headers"
    - "vitest.config.ts — test config aligned to Vite surface"
    - "tsconfig.json, index.html — scaffold + full-viewport #graph div"
    - "src/types.ts — GraphBuffers shape + MAX_NODE_INDEX = 16_777_216"
    - "src/transport/GraphTransport.ts — GraphTransport / GraphBuffers / LoadProgress interfaces"
    - "src/transport/SyntheticTransport.ts — small-graph GraphTransport impl driving the worker"
    - "src/workers/synthetic.worker.ts — BA generator, mulberry32 PRNG, Transferable post"
    - "src/graph/generator.ts — BA generator + hex->uint32 remap + DQL parser (pure, exported)"
    - "src/graph/cosmos.ts — @cosmos.gl/graph render adapter (setPointPositions/setLinks/render)"
    - "src/main.ts — wires SyntheticTransport -> cosmos adapter via the GraphTransport interface only"
    - "tests/remap.test.ts, tests/generator.test.ts, tests/parse.test.ts, tests/transport.test.ts, tests/precision.test.ts"
  modified: []

key-decisions:
  - "Pinned vite@^7.3.5 instead of the scaffold-default ^8.x — Vite 8's Rolldown bundler fails to resolve cosmos.gl's CJS dep gl-bench (no 'default' export) in both build and dep-prebundle; Vite 7 (Rollup+esbuild) handles the CJS interop. No source change; closer to RESEARCH's Vite-6.x intent."
  - "Used cosmos.gl render() to start the draw loop, NOT the RESEARCH-specified create()+start(). The installed 3.0.0 source requires render() to start the draw loop AND allocate the hover-picking framebuffer; create()+start() left a blank canvas and crashed on hover."
  - "@cosmos.gl/graph@3.0.0 confirmed legitimate (human pre-install gate, Task 1) and confirmed working in Chrome (human render gate, Task 5)."

patterns-established:
  - "Swappable GraphTransport: data path is interface-only in main.ts; transports are interchangeable implementations."
  - "SoA typed buffers with a Float32 2^24 mantissa ceiling guard enforced before any Float32 index view."
  - "Zero-copy Worker hand-off: generator writes into typed arrays, posts ArrayBuffers as Transferables."
  - "cosmos.gl render adapter exposes the Graph instance so later plans attach controls without touching the render loop."

requirements-completed: [REND-01, REND-03, DATA-03]

# Metrics
duration: 18min
completed: 2026-06-23
status: complete
---

# Phase 1 Plan 01: Walking Skeleton (Data Spine + GPU Render) Summary

**Vite+TS app that pushes a Barabási–Albert synthetic graph through a swappable GraphTransport into SoA Float32 buffers and renders it on the GPU via @cosmos.gl/graph 3.0.0 with working pan/zoom — the end-to-end render spine, proven small, with a green CPU-pipeline test suite.**

## Performance

- **Duration:** ~18 min (commit span; plus two blocking human-verify gates)
- **Started:** 2026-06-23T11:44:17+08:00 (first task commit)
- **Completed:** 2026-06-23T12:01:56+08:00 (render fix) + human render-gate approval
- **Tasks:** 5 (2 human-verify checkpoints, 3 auto)
- **Files modified:** 16 created

## Accomplishments

- Proved the full render spine end-to-end: synthetic Dgraph-shaped data → GraphTransport → SoA typed buffers → single GPU cosmos.gl render the user can pan and zoom (human-confirmed in Chrome, no console errors, `self.crossOriginIsolated === true`).
- Established the swappable `GraphTransport` interface as the single data path in `main.ts` — Plan 03's `DgraphTransport` drops in with no main.ts change.
- Laid the SoA typed-buffer foundation with the Float32 24-bit mantissa precision guard (`MAX_NODE_INDEX = 16_777_216`) enforced before any Float32 link view.
- Shipped the reusable `hex->uint32` dense remap + DQL-envelope parser as pure exported functions (Plan 03's dgraph worker imports them) plus a seeded BA generator that scales by `nodeCount` (Plan 02 stresses it to 5M/30M).
- Stood up the Wave-0 vitest CPU-pipeline suite: 21/21 green across 5 files (remap dense/stable/collision-free, generator exact-N/skewed-degree/deterministic, parser edge-pairs, transport conformance, precision guard throws above 2^24).

## Task Commits

1. **Task 1: Pre-install legitimacy gate for @cosmos.gl/graph@3.0.0** — human-approved before install (no commit; checkpoint:human-verify, gate=blocking-human)
2. **Task 2: Scaffold Vite+TS, COOP/COEP, SoA types, RED tests** — `056cf7e` (test)
3. **Task 3: GraphTransport + BA worker + SyntheticTransport + remap/parser (GREEN)** — `6bb2633` (feat)
4. **Task 4: cosmos.gl render adapter + main.ts wiring + index.html** — `d07c381` (feat)
   - Render-start fix: `157df20` (fix) — render() instead of create()+start()
5. **Task 5: Confirm render + pan/zoom in Chrome** — human-approved (no commit; checkpoint:human-verify, gate=blocking)

**Plan metadata:** (this docs commit)

## Files Created/Modified

- `package.json` — pins `@cosmos.gl/graph@3.0.0`, `vite@^7.3.5`, vitest
- `vite.config.ts` — COOP same-origin + COEP require-corp server headers; Vite-native worker support
- `vitest.config.ts` — test config aligned to the Vite surface
- `tsconfig.json`, `index.html` — scaffold + full-viewport `#graph` div
- `src/types.ts` — `GraphBuffers` shape + `MAX_NODE_INDEX = 16_777_216`
- `src/transport/GraphTransport.ts` — `GraphTransport` / `GraphBuffers` / `LoadProgress` interfaces
- `src/transport/SyntheticTransport.ts` — small-graph (~1000-node) transport driving the worker
- `src/workers/synthetic.worker.ts` — BA generator, mulberry32 PRNG, Transferable zero-copy post
- `src/graph/generator.ts` — BA generator + hex->uint32 remap + DQL parser (pure, exported)
- `src/graph/cosmos.ts` — `@cosmos.gl/graph` render adapter (setPointPositions/setLinks/render); exposes the Graph instance
- `src/main.ts` — wires SyntheticTransport → cosmos adapter through the GraphTransport interface only
- `tests/{remap,generator,parse,transport,precision}.test.ts` — Wave-0 CPU-pipeline suite

## Requirements Satisfied

- **REND-01** (whole graph as a single GPU node-link map) — **partial**: proven on a small synthetic graph; the at-scale (5M-node) single-map render is Plan 02.
- **REND-03** (pan/zoom/hover at 60fps) — **partial**: pan and zoom proven smooth on the small graph (human-confirmed); hover and the 60fps-at-scale verdict are Plan 02. No 60fps-at-scale claim is made here.
- **DATA-03** (render at target scale without exhausting memory) — **foundation only**: the SoA typed-buffer model + hex->uint32 dense remap + Float32 precision guard are in place; the at-scale memory verdict (5M/30M) is Plan 02/03.

## Decisions Made

- **Pinned `vite@^7.3.5`** rather than the scaffold-default ^8.x (see Deviation 1).
- **Used cosmos.gl `render()`** to start the draw loop instead of RESEARCH's `create()`+`start()` (see Deviation 2).
- **Confirmed `@cosmos.gl/graph@3.0.0` legitimate and working** via the two human gates (pre-install, post-render).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pinned vite@^7.3.5 (scaffold defaulted to ^8.x)**
- **Found during:** Task 4 (render adapter + dev-server wiring)
- **Issue:** Vite 8's Rolldown bundler failed to resolve cosmos.gl's CJS dependency `gl-bench` (`"default"` not exported) in both the production build and the dev dependency-prebundle, breaking both `npm run build` and `npm run dev`.
- **Fix:** Pinned `vite@^7.3.5` (Rollup + esbuild), which handles the CJS interop correctly. No source change. This matches RESEARCH's Vite-6.x intent more closely than the bleeding-edge 8.x default.
- **Files modified:** package.json, package-lock.json
- **Verification:** `npm run dev` serves; `npx tsc --noEmit` clean; human-confirmed render in Chrome.
- **Committed in:** `d07c381` (Task 4 commit)

**2. [Rule 1 - Bug] cosmos.gl render() not create()+start()**
- **Found during:** Task 5 (human render gate) — surfaced as a blank canvas + a crash on hover-picking
- **Issue:** Task 4/RESEARCH specified `create()` + `start()` (a MEDIUM-confidence API). The installed `@cosmos.gl/graph@3.0.0` source requires `render()` to (a) start the GPU draw loop and (b) allocate the hover-picking framebuffer. With create()+start() the canvas drew nothing and hover crashed. Installed source overrode RESEARCH.
- **Fix:** Replaced `create()` + `start()` with `render()` in the cosmos adapter.
- **Files modified:** src/graph/cosmos.ts
- **Verification:** Human confirmed the graph renders, pans, and zooms in Chrome with a clean console.
- **Committed in:** `157df20` (fix commit)

---

**Total deviations:** 2 auto-fixed (1 blocking toolchain pin, 1 render-start bug)
**Impact on plan:** Both were necessary to make the skeleton actually render; no scope creep. Deviation 2's root cause (RESEARCH's API confidence) is now resolved against the installed source — Plan 02 attaches run/pause/fit/hover on the verified `render()`-started Graph instance.

## Issues Encountered

- The cosmos.gl 3.0.0 init API differed from RESEARCH (create/start vs render); resolved by reading the installed package source and switching to `render()`. Documented as Deviation 2 so Plan 02 builds on the verified call.

## User Setup Required

None — no external service configuration required (this plan issues no Dgraph requests; synthetic data only).

## Known Stubs

None that block the plan goal. SyntheticTransport is intentionally small (~1000 nodes) per scope; Plan 02 scales the same generator to 5M/30M. The cosmos adapter intentionally omits Run/Pause, auto-freeze, fit, and hover — those are explicitly Plan 02 scope (the adapter exposes the Graph instance for them).

## Next Phase Readiness

Ready for **Plan 02 (GPU ceiling spike)**: scale the BA worker to 5M/30M, add auto-freeze + Run/Pause + Fit + hover on the exposed cosmos Graph instance, and record the 60fps verdict.
Ready for **Plan 03 (JSON wire + verdict)**: add `DgraphTransport` (after-cursor paging, chunked parse) reusing the exported `remap` + DQL parser, plus the staged loader, verdict instrument, and recorded feasibility verdict. COOP/COEP isolation is already in place for Plan 03's memory measurement.

No blockers.

## Self-Check: PASSED

All claimed key files exist on disk; all four task/fix commits (056cf7e, 6bb2633, d07c381, 157df20) found in git history.

---
*Phase: 01-interactive-graph-on-screen-data-spine-gpu-render*
*Completed: 2026-06-23*

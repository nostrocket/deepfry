---
phase: 01-interactive-graph-on-screen-data-spine-gpu-render
plan: 02
subsystem: ui
tags: [cosmos.gl, webgl2, barabasi-albert, force-layout, typed-arrays, soa, web-worker, vite, typescript]

# Dependency graph
requires:
  - phase: 01-01
    provides: "Vite+TS spine, GraphTransport interface + SoA typed-array types, CosmosAdapter (render loop, pan/zoom), small synthetic generator + vitest CPU-pipeline scaffold"
provides:
  - "5M-node / ~30M-edge Barabási–Albert synthetic generator into zero-copy SoA typed arrays (BA edge-copying, mulberry32 seeded PRNG, m=6)"
  - "O(E) in-degree counting pass (generateBA → computeInDegree) producing a Uint32Array (sum === edgeCount), no followers query, no per-node objects"
  - "Auto-freeze motion sampler (fixed ~10k-node subset, 500ms windows, 3 sub-threshold windows → graph.pause() + onSettled)"
  - "CosmosAdapter run/pause/fit + hover-index wiring (unpause/pause/fitView/onPointMouseOver/onPointMouseOut) against @cosmos.gl/graph 3.0.0"
  - "Vanilla-TS control panel: Run/Pause toggle (flips to Run on auto-freeze) + Fit button + cursor-tracking tooltip"
  - "Recorded GPU-half feasibility verdict: 60fps PASS at 5M/30M on M3 Pro / Chrome — Open Question 1 resolved in favor of WebGL2"
affects: [Phase 1 Plan 03 (JSON wire + feasibility verdict), Phase 2 (terrain overlays — degree/community), Phase 3 (explore/slice)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "In-degree derived from the edge buffer in a single O(E) pass into a Uint32Array (computeInDegree), computed in-worker pre-Float32 and transferred zero-copy — never query followers, never build per-node objects (D-08)"
    - "Motion sampler reads only a FIXED ~10k-node subset of getPointPositions() per tick (never all 5M) to detect settle without stalling the render loop (RESEARCH Pitfall 5)"
    - "Reversible auto-freeze via graph.pause()/unpause() (not stop()), so Run/Pause and auto-freeze share one toggle"
    - "cosmos owns the canvas imperatively; the vanilla-TS shell drives only a thin control panel and never re-renders the canvas (Pattern 2)"

key-files:
  created:
    - src/graph/autofreeze.ts
    - src/ui/controls.ts
  modified:
    - src/graph/generator.ts
    - src/workers/synthetic.worker.ts
    - src/transport/SyntheticTransport.ts
    - src/graph/cosmos.ts
    - src/main.ts
    - index.html

key-decisions:
  - "GPU-half feasibility verdict: 60fps PASS — WebGL2/cosmos.gl held ~60fps during pan, zoom, and hover at the 5M-node / ~30M-edge target on the reference machine (M3 Pro / Chrome). Resolves Open Question 1; the WebGPU compute-shader escalation (Phase-N) is NOT triggered."
  - "Layout auto-settled into terrain and the auto-freeze sampler paused it without user action (Run flipped to Run) — D-11 + D-12 confirmed on real at-scale render."
  - "In-degree lives in src/graph/generator.ts as computeInDegree() (single O(E) pass over links → Uint32Array, sum === edgeCount), invoked by the worker before the Float32 hand-off; no followers query, no per-node objects."
  - "Auto-freeze uses pause() (reversible) not stop(), and samples a fixed ~10k-node subset — never all 5M positions per tick."

patterns-established:
  - "O(E) in-degree pass in the worker, transferred as a Uint32Array alongside positions/links (zero-copy)"
  - "Fixed-subset motion sampling for settle detection at million scale"
  - "Single Run/Pause toggle shared between user control and auto-freeze, via reversible pause()/unpause()"

requirements-completed: [REND-02, REND-03, REND-04]

# Metrics
duration: ~6min
completed: 2026-06-23
status: complete
---

# Phase 1 Plan 02: GPU Ceiling Spike Summary

**WebGL2/cosmos.gl renders a 5M-node / ~30M-edge Barabási–Albert synthetic graph as one global GPU map that auto-settles, auto-freezes, and holds ~60fps under pan/zoom/hover on the M3 Pro — resolving the GPU half of the phase feasibility verdict.**

## Performance

- **Duration:** ~6 min (first task commit 12:14:00 → final task commit 12:16:20 +0800)
- **Started:** 2026-06-23T04:14:00Z (Task 1 commit)
- **Completed:** 2026-06-23 (Task 4 human-verify approved)
- **Tasks:** 4 (3 implementation + 1 human-verify checkpoint)
- **Files modified:** 8 (2 created, 6 modified)

## Accomplishments

- Scaled the synthetic transport to the fixed 5M-node / ~30M-edge power-law target (BA edge-copying, m=6, mulberry32 seeded PRNG) into zero-copy SoA typed arrays — `SyntheticTransport` defaults to `nodeCount = 5_000_000`.
- Added `computeInDegree()` — a single O(E) pass over the edge buffer producing a `Uint32Array` of length `nodeCount` (verified `sum === edgeCount`), computed in the worker before the Float32 hand-off and transferred zero-copy. No `followers` query, no per-node objects (D-08). Float32 precision guard retained (5M < 2^24).
- Built `autofreeze.ts`: samples a fixed deterministic ~10k-node subset of `getPointPositions()` every 500ms, and after 3 consecutive sub-threshold windows calls `graph.pause()` and fires `onSettled` — never sampling all 5M positions (RESEARCH Pitfall 5).
- Extended the `CosmosAdapter` with `run()` = unpause, `pause()` = pause (reversible), `fit()` = `fitView(250, 0.1)`, and hover-index emit via `onPointMouseOver`/`onPointMouseOut` — API names verified against installed `@cosmos.gl/graph` 3.0.0 types.
- Built the vanilla-TS control panel: a single Run/Pause toggle (flips to "Run" when auto-freeze fires), a Fit/Reset button, and a cursor-tracking tooltip showing the node `#index`; `main.ts` mounts controls + starts the sampler after load + routes the hover index.
- **Recorded the GPU-half feasibility verdict (see below): 60fps PASS.**

## Recorded 60fps Verdict (GPU half of the phase feasibility verdict)

Observed by the human on the reference machine (M3 Pro / Chrome — D-06/D-07) at the 5M-node / ~30M-edge synthetic scale:

- **60fps:** **PASS** — held ~60fps during pan, zoom, and hover (frames ≤ ~16.7ms). WebGL2/cosmos.gl sustains the interaction model at the 5M/30M target. _(Qualitative-but-confirmed headline PASS; exact per-axis FPS counters and generate-to-first-paint time were not separately recorded beyond "held ~60fps" — no precise numbers invented.)_
- **Auto-settle + auto-freeze:** **YES** — the hairball settled into terrain and the auto-freeze sampler paused the layout without user action (Run flipped to "Run"). D-11 + D-12 confirmed.
- **Fit/Reset:** returns the view to the whole map (D-13 / REND-04).
- **Resolution:** This RESOLVES phase **Open Question 1** in favor of the current WebGL2 stack. The **WebGPU compute-shader escalation (Phase-N) is NOT triggered.**

The JSON-wire half of the feasibility verdict (browser-direct load time, peak-heap memory verdict) remains for Plan 03.

## Task Commits

Each implementation task was committed atomically:

1. **Task 1: Scale BA generator to 5M/30M + O(E) in-degree pass** — `4380277` (feat)
2. **Task 2: Auto-freeze sampler + run/pause/fit/hover wiring (D-11/12/13/14)** — `801c9c8` (feat)
3. **Task 3: Control panel (Run/Pause + Fit + tooltip) wired in main.ts (D-12/13/14)** — `9456933` (feat)
4. **Task 4: Record 60fps verdict (human-verify checkpoint)** — approved by human (verdict above)

**Plan metadata:** _(this docs commit)_

## Files Created/Modified

- `src/graph/generator.ts` *(modified)* — added `computeInDegree()` single O(E) pass → `Uint32Array` (sum === edgeCount); BA generator parameterized for mid-scale tests.
- `src/workers/synthetic.worker.ts` *(modified)* — invokes `generateBA` + `computeInDegree` at 5M scale, transfers positions/links/in-degree zero-copy.
- `src/transport/SyntheticTransport.ts` *(modified)* — defaults `nodeCount = 5_000_000`, `m = 6`.
- `src/graph/autofreeze.ts` *(created)* — fixed ~10k-node subset motion sampler → `graph.pause()` + `onSettled` after 3 sub-threshold windows; start/stop API.
- `src/graph/cosmos.ts` *(modified)* — `CosmosAdapter` exposes `run()`/`pause()`/`fit()` (fitView 250,0.1) and hover-index emit via `onPointMouseOver`/`onPointMouseOut`.
- `src/ui/controls.ts` *(created)* — Run/Pause toggle (flips to "Run" on auto-freeze), Fit button, cursor-tracking `#index` tooltip.
- `src/main.ts` *(modified)* — mounts controls, starts the autofreeze sampler after load, routes hover index to the tooltip.
- `index.html` *(modified)* — controls container + tooltip element alongside `#graph`.

## Decisions Made

- **In-degree pass placement:** `computeInDegree()` lives in `src/graph/generator.ts` (not inline in the worker) so it is unit-testable; the worker imports and runs it pre-Float32. Single O(E) loop incrementing `inDegree[tgt]`; asserted `sum === edgeCount`.
- **Reversible freeze:** used `pause()`/`unpause()` (not `stop()`) per RESEARCH, so the single Run/Pause toggle serves both user control and auto-freeze.
- **Sampler scope:** fixed ~10k-node subset only, never all 5M positions per tick.

## Deviations from Plan

None — plan executed exactly as written. Notably, all `@cosmos.gl/graph` 3.0.0 API names (`unpause`, `pause`, `fitView`, `onPointMouseOver`, `onPointMouseOut`) were verified against the installed type definitions before use, so no API deviations were required this time (unlike the version-pinning friction in Plan 01).

## Issues Encountered

None during planned work. The expected multi-second generate-before-first-paint at 5M scale (no loader yet — that is Plan 03) was anticipated by the plan and is not an issue.

## Known Stubs

None — all delivered surface is wired (generator → worker → transport → cosmos render → controls/tooltip). The only deferred surface is the staged loader and the JSON-wire transport, which are explicitly Plan 03's scope.

## User Setup Required

None — no external service configuration required (synthetic in-browser data, no Dgraph/Go seeder this plan).

## Next Phase Readiness

- **GPU half of the feasibility verdict is in and PASSES** — the WebGL2 stack is validated at the 5M/30M target; no WebGPU escalation.
- Ready for **Plan 03**: DgraphTransport (after-cursor paging), chunked parse + remap, staged loader, and the JSON-wire / peak-heap feasibility verdict (the second half that, combined with this PASS, completes the phase verdict).
- DATA-03 remains **Partial**: the GPU-half render at 5M without exhausting memory was observed, but the full at-scale memory/peak-heap verdict is instrumented in Plan 03.

## Self-Check: PASSED

**Files created/modified (verified on disk):**
- FOUND: src/graph/generator.ts (computeInDegree at line 140)
- FOUND: src/workers/synthetic.worker.ts (imports computeInDegree; invoked line 54)
- FOUND: src/transport/SyntheticTransport.ts (nodeCount = 5_000_000 line 25)
- FOUND: src/graph/autofreeze.ts
- FOUND: src/graph/cosmos.ts
- FOUND: src/ui/controls.ts
- FOUND: src/main.ts
- FOUND: index.html

**Commits (verified in git log):**
- FOUND: 4380277 (Task 1)
- FOUND: 801c9c8 (Task 2)
- FOUND: 9456933 (Task 3)

**Verification (from execution):** `npx vitest run` 28/28 green (5 files); `npx tsc --noEmit` clean.

---
*Phase: 01-interactive-graph-on-screen-data-spine-gpu-render*
*Completed: 2026-06-23*

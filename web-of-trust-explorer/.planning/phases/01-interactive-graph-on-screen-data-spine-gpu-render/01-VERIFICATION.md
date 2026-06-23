---
phase: 01-interactive-graph-on-screen-data-spine-gpu-render
verified: 2026-06-23T14:05:00Z
status: passed
score: 5/5 success criteria verified
behavior_unverified: 0
overrides_applied: 0
re_verification: # none — initial verification
human_verification: []
---

# Phase 1: Interactive Graph On Screen (Data Spine + GPU Render) Verification Report

**Phase Goal:** A developer opens the app, the whole follow-graph bulk-loads from Dgraph into memory with a visible progress state, settles into spatial structure via live GPU force layout, and can be panned/zoomed/hovered as one global map at 60fps — proven against synthetic ~5M-node / ~30M-edge power-law data, not just the tiny dev DB. Phase 1 ends with an explicit feasibility checkpoint that decides whether the deferred Go binary-streaming bridge (PERF-01, v2) must be pulled forward.
**Verified:** 2026-06-23T14:05:00Z
**Status:** passed
**Re-verification:** No — initial verification
**Mode:** mvp (de-risking spike phase)

## Framing

This is a DE-RISKING SPIKE phase. Its goal explicitly includes "an explicit feasibility checkpoint that decides whether PERF-01 must be pulled forward." The two dominant risks were tested by deliberately separate means:

- **GPU ceiling** — synthetic 5M-node / 30M-edge power-law graph (Plan 02). Verdict: **PASS, ~60fps**, human-verified on the M3 Pro / Chrome reference machine.
- **JSON wire** — real dev Dgraph (Plan 03). Verdict: **FAIL → trigger PERF-01**, human-observed unusable (swap) plus server-side counts (365,559 follow-source nodes / 1,541,632 profiles / ~tens of millions of edges).

The recorded **FAIL → PERF-01 trigger is the INTENDED, SUCCESSFUL outcome** of the feasibility checkpoint — the goal asks for a decision, and a decisive, evidence-backed PERF-01 trigger is that decision. It is scored as "the requirement's verdict was delivered," not "the requirement failed to build." REND-01 (single-map render at real Dgraph scale) and DATA-03 (at-scale memory) carry explicit forward-references to PERF-01.

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth (Success Criterion) | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Opening the app bulk-loads the entire graph once from Dgraph with a visible loading/progress indicator, querying only `follows` (followers/in-degree derived client-side) via `after`-cursor paging | ✓ VERIFIED | `dgraphPaging.ts:37` builds `{ q(func: has(follows), first: N, after: cursor) { uid follows { uid } } }`; paging loop `dgraphPaging.ts:82-102` (cursor `0x0` → last uid → stop on short/empty page); `dgraph.worker.ts:74` POSTs `application/dql` to `/query`; `loader.ts:15-17` shows Fetching→Parsing→Building + live edge counter; in-degree derived O(E) in worker (`generator.ts:140`), **0 `followers` queries** (grep-clean). Ran against real dev Dgraph. |
| 2 | The whole graph renders as a single global GPU node-link map and settles via a live force-directed layout the user can run, pause, and freeze | ✓ VERIFIED | `cosmos.ts:75` `enableSimulation:true` auto-starts layout on `render()` (line 96, D-11); `run()`=`unpause()`, `pause()`=`pause()` (lines 99-100); `autofreeze.ts` samples a fixed ~10k subset every 500ms (`getPointPositions`, line 109) → `graph.pause()` after 3 sub-threshold windows (lines 127-133); `controls.ts` Run/Pause toggle flips to "Run" on auto-freeze. Human-verified auto-settle+freeze at 5M/30M (Plan 02 gate, REND-02). |
| 3 | Pan, zoom, and hover hold 60fps on a synthetic ~5M-node / ~30M-edge power-law graph, with hex pubkeys remapped to dense uint32 in SoA typed buffers (no per-node heap objects) | ✓ VERIFIED | `SyntheticTransport.ts:25` defaults `nodeCount=5_000_000, m=6`; `synthetic.worker.ts:41,54,70` generates BA graph + O(E) in-degree, zero-copy Transferable post; SoA Float32/Uint32 buffers, no per-node objects; hex→uint32 dense remap (unit-tested). **60fps PASS human-verified** on M3 Pro/Chrome during pan/zoom/hover (Plan 02 blocking gate, REND-03). Behavior-dependent truth carried by the human-verify checkpoint. |
| 4 | A single fit-to-screen / reset returns the view to the whole map, and the app loads target-scale data without exhausting browser memory | ✓ VERIFIED | Fit: `cosmos.ts:101` `fit()`=`fitView(250,0.1)`, wired to Fit button in `controls.ts`; human-verified returns to whole map (REND-04). At-scale memory: GPU half observed rendering 5M/30M without exhausting memory (Plan 02); browser-direct JSON wire at real scale exhausts memory → **measured NOT viable, mitigation carried by PERF-01** (the spike's deliverable, see Framing). |
| 5 | The data source sits behind a swappable transport interface, and the phase ends with a recorded feasibility verdict on browser-direct JSON load time (pass, or trigger the deferred Go bridge PERF-01) | ✓ VERIFIED | `GraphTransport.ts` interface; both `SyntheticTransport` (`:15 implements GraphTransport`) and `DgraphTransport` (`:25 implements GraphTransport`) satisfy it; `main.ts` selects via `?transport=dgraph`, reaching data only through the interface. **Recorded verdict: GPU half PASS, JSON-wire half FAIL → PERF-01 triggered/pulled forward** (01-03-SUMMARY + ROADMAP feasibility verdict). The "or triggers PERF-01" branch of the criterion is satisfied. |

**Score:** 5/5 truths verified (0 present, behavior-unverified)

The two behavior-dependent truths (#2 settle/freeze, #3 60fps interaction) are runtime invariants that grep cannot prove. Both were exercised through the plans' **blocking `checkpoint:human-verify` gates** (Plan 01 Task 5, Plan 02 Task 4, Plan 03 Task 4) and recorded as observed PASS by the human on the reference machine. They are therefore VERIFIED via the human-gate evidence the spike was designed around, not left PRESENT_BEHAVIOR_UNVERIFIED.

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `vite.config.ts` | COOP same-origin + COEP require-corp headers | ✓ VERIFIED | Both headers set (lines confirmed); enables `measureUserAgentSpecificMemory`. |
| `src/types.ts` | SoA shapes + Float32 precision guard | ✓ VERIFIED | `MAX_NODE_INDEX = 16_777_216` (line 21); `GraphBuffers` shape. |
| `src/transport/GraphTransport.ts` | Swappable transport interface | ✓ VERIFIED | `GraphTransport`/`GraphBuffers`/`LoadProgress`; sole data path in main.ts. |
| `src/transport/SyntheticTransport.ts` | 5M BA transport over worker | ✓ VERIFIED | `implements GraphTransport`, `nodeCount=5_000_000`, spawns synthetic.worker. |
| `src/transport/DgraphTransport.ts` | Real-wire transport | ✓ VERIFIED | `implements GraphTransport`, spawns dgraph.worker, swappable. |
| `src/transport/dgraphPaging.ts` | After-cursor paging core (unit-tested) | ✓ VERIFIED | `buildDqlPageQuery` + paging/termination loop, read-only DQL only. |
| `src/workers/synthetic.worker.ts` | BA generator + in-degree, zero-copy | ✓ VERIFIED | `generateBA`+`computeInDegree`, Transferable post (line 70). |
| `src/workers/dgraph.worker.ts` | Chunked fetch+parse+remap, read-only | ✓ VERIFIED | `application/dql` POST `/query`; 0 mutate/alter, 0 followers (grep-clean). |
| `src/graph/cosmos.ts` | render() + run/pause/fit/hover | ✓ VERIFIED | `@cosmos.gl/graph` render(), fitView, onPointMouseOver, unpause/pause. |
| `src/graph/autofreeze.ts` | Motion-sample settle detector | ✓ VERIFIED | 10k-subset 500ms sampler → pause() after 3 windows. |
| `src/graph/generator.ts` | BA gen + remap + parser + computeInDegree | ✓ VERIFIED | `computeInDegree` real O(E) pass (lines 140-151). |
| `src/ui/controls.ts` | Run/Pause toggle + Fit + tooltip | ✓ VERIFIED | All three present and wired to adapter. |
| `src/ui/loader.ts` | Staged labels + live edge count, no % bar | ✓ VERIFIED | Fetching/Parsing/Building + edge counter. |
| `src/ui/verdict.ts` | fetch/parse/layout ms + peak heap | ✓ VERIFIED | `measureUserAgentSpecificMemory` w/ `usedJSHeapSize` fallback + `encoding_ns`. |
| `package.json` | `@cosmos.gl/graph` pinned 3.0.0 | ✓ VERIFIED | `"@cosmos.gl/graph": "3.0.0"` (exact pin). |

### Key Link Verification

| From | To | Via | Status |
| --- | --- | --- | --- |
| `main.ts` | `GraphTransport` | selects Synthetic/Dgraph via interface only (`main.ts:27-32`) | ✓ WIRED |
| `main.ts` | `cosmos.ts` | hands GraphBuffers to adapter, render() | ✓ WIRED |
| `SyntheticTransport` | `synthetic.worker.ts` | `new Worker(new URL('../workers/synthetic.worker.ts'…))` | ✓ WIRED |
| `DgraphTransport` | `dgraph.worker.ts` | spawns worker, forwards onProgress | ✓ WIRED |
| `dgraph.worker.ts` | Dgraph :8080 | POST `/query` `application/dql` after-cursor | ✓ WIRED |
| `controls.ts` | `cosmos.ts` | Run/Pause→unpause/pause, Fit→fitView | ✓ WIRED |
| `autofreeze.ts` | `cosmos.ts` | getPointPositions → pause() on settle | ✓ WIRED |
| `cosmos.ts` | `controls.ts` | onPointMouseOver → tooltip | ✓ WIRED |
| `verdict.ts` | `main.ts` | verdict readout surfaced after load | ✓ WIRED |

### Read-Only Invariant (DeepFry data-separation rule)

| Check | Result |
| --- | --- |
| `/mutate` or `/alter` in dgraph code | ✗ none — grep-clean (PASS) |
| `set {` / `delete {` mutation bodies | ✗ none — grep-clean (PASS) |
| `followers` query (must derive client-side) | ✗ none — grep-clean (PASS); in-degree from O(E) edge pass |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| --- | --- | --- | --- |
| Type-check clean | `npx tsc --noEmit` | exit 0 | ✓ PASS |
| CPU-pipeline test suite | `npx vitest run` | 33/33 passed (5 files) | ✓ PASS |
| Read-only invariant | grep `/mutate\|/alter\|followers` in dgraph code | 0 matches | ✓ PASS |

The two runtime invariants (60fps interaction, auto-settle/freeze) require a GPU + real render loop and cannot be exercised by a 10s spot-check; they were covered by the plans' blocking human-verify gates (recorded PASS).

### Requirements Coverage

| Requirement | Source Plan(s) | Status | Evidence |
| --- | --- | --- | --- |
| DATA-01 | 01-03 | ✓ SATISFIED | DgraphTransport after-cursor DQL paging over `has(follows)`+`follows{uid}`, chunked parse + remap, staged loader; bulk-load built and ran against real dev Dgraph. (Wire too heavy at real scale → mitigation = PERF-01.) |
| DATA-03 | 01-01, 01-02, 01-03 | ✓ SATISFIED (verdict delivered) | SoA buffers + hex→uint32 remap + Float32 guard (01-01); GPU half renders 5M/30M without exhausting memory (01-02); JSON-wire at real scale measured NOT viable → recorded FAIL-by-design-trigger, mitigation carried by PERF-01. The at-scale memory requirement's verdict is the deliverable. |
| REND-01 | 01-01, 01-02 | ✓ SATISFIED (partial, forward-ref) | Single global GPU node-link map proven at synthetic 5M-node scale (01-02). Real-Dgraph render at scale is gated behind PERF-01 (browser-direct wire is the limiter, not the renderer). Carries explicit PERF-01 forward-reference. |
| REND-02 | 01-02 | ✓ SATISFIED | Live GPU layout auto-starts, Run/Pause toggle, auto-freeze on settle — human-confirmed at 5M/30M. |
| REND-03 | 01-01, 01-02 | ✓ SATISFIED | Pan/zoom/hover ~60fps at 5M/30M on M3 Pro — recorded verdict PASS. |
| REND-04 | 01-02 | ✓ SATISFIED | Single Fit/Reset → `fitView` returns to whole map. |

All 6 declared requirement IDs (DATA-01, DATA-03, REND-01, REND-02, REND-03, REND-04) are accounted for and map to plan frontmatter. No orphaned requirements: REQUIREMENTS.md maps exactly these 6 to Phase 1, and all 6 appear in plan `requirements` fields. REND-01 and DATA-03 carry explicit forward-references to PERF-01 (consistent with the spike's recorded verdict), which is the intended deliverable shape — not a gap.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| — | — | TBD/FIXME/XXX | — | None found in src/ |
| — | — | TODO/HACK/PLACEHOLDER | — | None found in src/ |
| — | — | stub returns / empty handlers | — | None found in src/ (non-test) |

No debt markers, no stub handlers, no placeholder implementations. The deferred surface (Go bridge / PERF-01) is a recorded, intentional next-phase decision, not an in-code stub.

### Human Verification Required

None outstanding. The phase's behavior-dependent gates (render+pan/zoom, 60fps at 5M/30M with auto-settle+freeze, real-wire load attempt → verdict) were all exercised and recorded PASS / verdict via the plans' blocking `checkpoint:human-verify` gates during execution (Plan 01 Task 5, Plan 02 Task 4, Plan 03 Task 4). No verification items were deferred to end-of-phase.

### Gaps Summary

No gaps. The phase goal — open the app, bulk-load behind a visible progress state, settle via live GPU layout, pan/zoom/hover the whole map at 60fps proven at synthetic 5M/30M scale, and END WITH AN EXPLICIT FEASIBILITY CHECKPOINT deciding PERF-01 — is achieved in the codebase:

- The full render spine (transport → SoA buffers → cosmos.gl GPU render → controls) exists, is wired, and is substantive (tsc clean, 33/33 tests green).
- The swappable `GraphTransport` seam is real, with both transports implementing it; `main.ts` reaches data only through the interface.
- The read-only invariant holds (0 mutate/alter, 0 followers — grep-clean).
- The GPU half was human-verified PASS at 5M/30M ~60fps with auto-settle/freeze.
- The JSON-wire half produced the decisive, evidence-backed **FAIL → PERF-01 trigger** that the goal's feasibility checkpoint demanded — server-side counts (365,559 follow-nodes / 1,541,632 profiles) falsified the small-DB assumption and the browser-direct load was observed unusable. PERF-01 (drop-in `GoBridgeTransport` behind the same interface) is pulled forward from v2.

REND-01 (real-Dgraph render at scale) and DATA-03 (at-scale browser-direct memory) correctly carry forward-references to PERF-01 — this is the spike's intended output, not unfinished work. The phase delivered the goal: de-risk both halves and decide PERF-01.

---

_Verified: 2026-06-23T14:05:00Z_
_Verifier: Claude (gsd-verifier)_

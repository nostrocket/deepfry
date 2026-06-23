# Phase 1: Interactive Graph On Screen (Data Spine + GPU Render) - Research

**Researched:** 2026-06-23
**Domain:** GPU graph visualization (cosmos.gl/WebGL2), Dgraph DQL bulk-load, in-browser power-law synthesis, JS memory budgeting
**Confidence:** HIGH on the engine API (verified against shipped `.d.ts`), MEDIUM on at-scale perf/memory feasibility (the open spike risk this phase exists to resolve)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Synthetic Data & Validation Strategy**
- **D-01:** The at-scale test graph is generated **in-browser** (in a Worker), not via Dgraph or a Go seeder. This **only** proves the GPU side — produces no real JSON wire.
- **D-02:** Generator is **fixed at the 5M-node / 30M-edge target** (no scale knob).
- **D-03:** Power-law **shape parameters** are left to the researcher/planner (see § Synthetic Power-Law Generator below).
- **D-04:** The browser-direct **load-time feasibility verdict is measured separately against the real dev Dgraph at its current real size**, then extrapolated to target scale. Two risks, two means: synthetic = GPU ceiling, real Dgraph = JSON wire.

**Feasibility Verdict Bar**
- **D-05:** **PASS = ≤ ~30s** for session-start bulk load (query → parse → buffers ready), extrapolated to target scale. Worse triggers pulling PERF-01 (Go bridge) forward.
- **D-06:** Reference machine: **MacBook Pro, Apple M3 Pro — 11-core CPU (5P+6E), 14-core GPU, 18 GB unified memory, macOS 26.3 / Metal 4.**
- **D-07:** Reference browser: **Chrome.**
- **D-08:** ⚠️ **The 18 GB unified-memory pool is a co-equal risk to framerate.** Treat peak JS heap as a first-class verdict metric; design parse/buffer-fill to avoid `JSON.parse` doubling.

**Loading / Progress UX**
- **D-09:** Loader shows **staged labels** (`Fetching from Dgraph…` → `Parsing edges…` → `Building layout…`) with a **live count ticking up** ("12.3M / ~30M edges"). No % bar (cursor paging has no honest upfront total).
- **D-10:** On completion, surface **on-screen breakdown + console log**: fetch ms / parse ms / layout-ready ms / node+edge count / peak JS heap.

**Layout Controls & Default Behavior**
- **D-11:** Force layout **auto-starts settling immediately on load** (the "it's alive" moment).
- **D-12:** Controls = single **Run/Pause toggle + auto-freeze when settled** (motion below threshold → stop iterating). "Settled" threshold is researcher/planner discretion (see § Auto-Freeze Threshold).
- **D-13:** **Fit-to-screen / reset** as a single action returning view to whole map (REND-04).

**Hover Behavior**
- **D-14:** Hover = **visual highlight + minimal tooltip** showing just raw node index or hex pubkey. Proves hit-testing at 60fps without bleeding Phase 3's detail panel in.

### Claude's Discretion
- Exact cosmos.gl npm package name/version — **RESOLVED below: `@cosmos.gl/graph@3.0.0`.**
- Power-law generator parameters (D-03), "settled" motion threshold (D-12), chunk/page size for paged load and Worker parse.
- Shape of the swappable transport interface (only that one must exist).
- Edge rendering approach at 30M edges (whether/how edges draw vs nodes-only at extreme zoom-out) — planner's call within 60fps constraint.

### Deferred Ideas (OUT OF SCOPE)
- Adjustable synthetic-data scale knob (declined per D-02).
- Go generator → seed Dgraph / static fixture for at-scale load testing (declined per D-01; natural escalation if D-04 extrapolation proves too uncertain).
- Three explicit Run/Pause/Freeze controls (declined for toggle + auto-freeze, D-12).
- Degree/community overlays → Phase 2; search/fly-to, neighborhood highlight, full node-detail panel, time filter, refresh → Phase 3. PERF-01 Go bridge → v2 (gated by this phase's verdict).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DATA-01 | Entire follow-graph bulk-loads from Dgraph once, with visible loading/progress state | § Dgraph DQL Bulk-Load (`after`-cursor paging shape), § Staged Loader / Verdict Instrument |
| DATA-03 | Loads/renders at target scale (millions of nodes, tens of millions of edges) without exhausting browser memory | § Memory & Parse Budgeting (D-08), § Dense hex→uint32 Remap, § Synthetic Power-Law Generator |
| REND-01 | Whole graph as single global GPU node-link map | § cosmos.gl 3.0.0 — Init & Data Input (`setPointPositions`/`setLinks`) |
| REND-02 | Nodes settle via live GPU force layout; run/pause/freeze | § cosmos.gl — Simulation Control (`start`/`pause`/`unpause`/`stop`/`step`), § Auto-Freeze Threshold |
| REND-03 | Pan, zoom, hover across full graph at 60fps | § cosmos.gl — Interaction & Hit-Testing (`onPointMouseOver`, `findPointsInRect`), § 60fps validation |
| REND-04 | Fit-to-screen / reset in one action | § cosmos.gl — Viewport (`fitView`) |

DATA-02 (refresh) and the explore/filter/overlay requirements are explicitly Phase 2/3 — out of scope here.
</phase_requirements>

## Summary

The engine bet (cosmos.gl, WebGL2, graphology, Vite 6 + TS 5, vanilla/Svelte shell) is settled in `.claude/CLAUDE.md` and is **not re-litigated here**. This research nails down the four implementation-ready unknowns the planner needs: (1) the **exact cosmos.gl 3.0.0 API** for feeding structure-of-arrays typed buffers and driving the simulation — verified directly against the package's shipped `.d.ts` type definitions; (2) the **Dgraph DQL `after`-cursor query shape** for an ID-only `follows` dump; (3) a **Barabási–Albert / linear-preferential-attachment generator** that writes 5M nodes / 30M edges straight into typed arrays with no per-node heap objects; and (4) the **memory & parse discipline** (D-08) that keeps the load inside the 18 GB pool.

The single biggest finding: **cosmos.gl 3.0.0 is real, current, and exactly the right API shape** — `setPointPositions(Float32Array)` + `setLinks(Float32Array)` are literally the SoA buffers this phase mandates, and `start/pause/unpause/stop/step` map 1:1 onto the D-11/D-12 run/pause/auto-freeze control model. The remaining risk is **not** the API — it is whether WebGL2 layout holds 60fps and whether the parse stays under budget at the real production scale. That is precisely what the two spikes (synthetic GPU ceiling + real-Dgraph wire verdict) exist to answer.

**Primary recommendation:** Scaffold Vite + TS, wrap the Dgraph dump in a Worker behind a `GraphTransport` interface, build the BA generator as a second Worker, fill `Uint32Array` link buffers + `Float32Array` position buffers in-Worker, hand them to a single `@cosmos.gl/graph@3.0.0` instance with `fitViewOnInit: true` and `enableSimulation` on, auto-freeze via `getPointPositions()` motion sampling, and make the loader double as the verdict instrument.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Bulk-load `follows` from Dgraph | Web Worker (fetch + parse) | Browser main (UI progress) | Keeps blocking `JSON.parse` off the render loop (D-08) |
| Synthetic 5M/30M generation | Web Worker | — | CPU-heavy; must never touch main thread; D-01 |
| hex→uint32 remap | Web Worker | — | Built during parse, in the same pass, into typed arrays |
| SoA typed buffers (positions, links) | Web Worker → transferred | GPU (cosmos.gl) | `Transferable` ArrayBuffers cross the worker boundary zero-copy |
| Force simulation | GPU (cosmos.gl/WebGL2 shaders) | — | The core perf bet; runs in fragment/vertex shaders |
| Render (nodes + links) | GPU (cosmos.gl/luma.gl) | — | Single `<canvas>`, owned imperatively |
| Pan / zoom / hover / fit | GPU + d3-zoom (bundled in cosmos.gl) | Browser main (event capture) | cosmos.gl owns the transform; shell only reads hover index |
| Control panel (Run/Pause, Fit, loader, verdict readout) | Browser main (vanilla/Svelte) | — | Thin shell; never in the render loop |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `@cosmos.gl/graph` | `3.0.0` | GPU force-layout **and** render engine | `[VERIFIED: npm registry]` — published 2026-06-17, 17.9k weekly downloads, repo `github.com/cosmosgl/graph`, no postinstall. The OpenJS rename of `@cosmograph/cosmos` (legacy is `2.0.0-beta.20`). `[VERIFIED: shipped .d.ts]` SoA API + GPU sim. |
| `typescript` | `5.x` | Language | cosmos.gl ships its own `.d.ts`; typed-array correctness is the main bug surface `[CITED: .claude/CLAUDE.md]` |
| `vite` | `6.x` | Dev server + bundler | Native ESM, instant HMR, zero-config TS + Web Workers `[CITED: .claude/CLAUDE.md]` |
| App shell | Vanilla TS (recommended) or Svelte 5 | Control panel only | cosmos.gl owns the canvas imperatively; keep framework out of render loop `[CITED: .claude/CLAUDE.md]` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `comlink` | `4.4.2` | Worker RPC ergonomics | `[VERIFIED: npm registry]` Wrap the Dgraph-fetch and generator Workers so `JSON.parse`/generation never blocks UI. Optional — raw `postMessage` + `Transferable` also works and is lower-overhead. |
| `vitest` | (with Vite 6) | Unit tests | Test the Dgraph→buffer transform, hex→uint32 remap, and BA generator output shape (not the GPU) |

**Deferred to Phase 2 (do NOT install in Phase 1):** `graphology`, `graphology-communities-louvain`, `graphology-metrics`, `d3-scale`, `d3-scale-chromatic`. These serve degree/community overlays (OVER-01/02), which are Phase 2. Phase 1 derives in-degree client-side with a trivial O(E) counting pass over the edge buffer — no graphology needed. **Deferred to Phase 3:** `nostr-tools` (npub search, EXPL-01).

> **Scope guard for the planner:** the `.claude/CLAUDE.md` stack table lists the full v1 dependency set. Phase 1 installs only the Core row + `comlink`/`vitest`. graphology/d3/nostr-tools are correct choices but belong to later phases — installing them now is premature.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `@cosmos.gl/graph` SoA + Worker JSON | Thin Go binary-stream bridge (PERF-01) | The committed escape hatch — only if D-05 verdict fails (>~30s). Deferred to v2. |
| In-Worker raw `postMessage`+`Transferable` | `comlink` | comlink is ergonomic but adds a proxy layer; for one-shot bulk transfer of ArrayBuffers, raw transfer is leaner. Either is fine. |
| Vanilla TS shell | Svelte 5 (runes) | Svelte only if you want reactive controls without hand-wiring; adds build weight. Avoid React (fights imperative canvas). |

**Installation:**
```bash
npm create vite@latest wot-graph-explorer -- --template vanilla-ts
cd wot-graph-explorer
npm install @cosmos.gl/graph@3.0.0
npm install -D vitest
# optional worker RPC:
npm install comlink@4.4.2
```

**Version verification (performed this session):**
- `npm view @cosmos.gl/graph version` → `3.0.0` (latest dist-tag); published `2026-06-17`. dist-tags: `latest: 3.0.0`, `beta: 3.1.0-beta.1`, `rc: 2.6.2-rc.0`. **Pin `3.0.0`** (do not float to `3.1.0-beta`).
- `npm view @cosmograph/cosmos version` → `2.0.0-beta.20` (the legacy name — confirm you are NOT importing this).
- `comlink` → `4.4.2`; `vitest` ships with Vite 6.

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `@cosmos.gl/graph` | npm | published 2026-06-17 (3.0.0) | 17.9k/wk | github.com/cosmosgl/graph | SUS (`too-new`) | **Approved with note** — false-positive "too-new": this is a genuine recent OpenJS rename/release of an established engine (legacy `@cosmograph/cosmos`). No postinstall. Planner adds one `checkpoint:human-verify` before first install per protocol. |
| `comlink` | npm | mature | high | github.com/GoogleChromeLabs/comlink | OK | Approved |
| `vitest` | npm | mature | very high | github.com/vitest-dev/vitest | OK | Approved |
| `nostr-tools` | npm | patch published 2026-06-22 | 761k/wk | github.com/nbd-wtf/nostr-tools | SUS (`too-new`) | **NOT in Phase 1** (Phase 3). False positive — a fresh patch of a 761k/wk-download package. |
| graphology + plugins | npm | mature | 94k–1.2M/wk | github.com/graphology/graphology | OK | **NOT in Phase 1** (Phase 2) |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** `@cosmos.gl/graph` (Phase 1 install — planner inserts `checkpoint:human-verify` before the install task, per package-legitimacy protocol; the "too-new" flag is a date heuristic, mitigated by the verified repo/downloads/no-postinstall signals). `nostr-tools` is out of Phase 1 scope.

## Architecture Patterns

### System Architecture Diagram

```
                          ┌─────────────────── MAIN THREAD (UI) ───────────────────┐
                          │                                                         │
  [Dgraph :8080] ──fetch──┼──> data flows IN via one of two transports             │
  (DQL /query, gzip)      │         │                                               │
                          │         ▼                                               │
                          │   GraphTransport interface (swappable)                  │
                          │   ├── DgraphTransport  (real wire — JSON verdict)       │
                          │   └── SyntheticTransport (BA generator — GPU verdict)   │
                          │         │ both run in a WEB WORKER                       │
                          │         ▼                                               │
   ┌── WORKER ───────────────────────────────────────────────────┐                │
   │  fetch chunk (after-cursor) ──> parse chunk ──> hex→uint32   │  progress msgs │
   │  remap (Map<hex,u32>) ──> append to growable Uint32Array     │ ───(count)───► │ staged loader
   │  link buffer + random-seed Float32Array positions            │                │ (D-09/D-10)
   │  (one O(E) pass also tallies in-degree for later)            │                │
   └──────────────────────────┬───────────────────────────────────┘                │
                              │ Transferable ArrayBuffers (zero-copy)               │
                              ▼                                                      │
                    ┌──────── GPU (cosmos.gl / WebGL2) ────────┐                    │
                    │ setPointPositions(Float32Array x,y…)     │  onPointMouseOver  │
                    │ setLinks(Float32Array src,tgt…)          │ ──(index)────────► │ tooltip (D-14)
                    │ create(); start(alpha)  ── live force ── │                    │
                    │ fitView()  ◄── Fit/Reset button (D-13)   │ ◄──(Run/Pause)──── │ control panel
                    │ pause()/unpause()/stop() ◄── auto-freeze │                    │
                    └──────────────────────────────────────────┘                    │
                          └──────────────────────────────────────────────────────────┘
```

Trace of the primary use case: app opens → SyntheticTransport (or DgraphTransport) generates/fetches into typed buffers in a Worker → buffers transferred to main → cosmos.gl `setPointPositions`/`setLinks`/`create`/`start` → hairball settles → user pans/zooms/hovers, hits Fit, toggles Run/Pause; motion-sampler auto-`pause()`s when settled.

### Recommended Project Structure
```
src/
├── main.ts                  # app entry: wires shell + graph
├── transport/
│   ├── GraphTransport.ts     # the swappable interface (D + discretion)
│   ├── DgraphTransport.ts    # real wire — DQL after-cursor paging
│   └── SyntheticTransport.ts # BA generator (drives the Worker)
├── workers/
│   ├── dgraph.worker.ts      # fetch + parse + hex→uint32 remap
│   └── synthetic.worker.ts   # Barabási–Albert into typed arrays
├── graph/
│   ├── cosmos.ts             # cosmos.gl init, config, control wiring
│   └── autofreeze.ts         # motion-sampling settle detector (D-12)
├── ui/
│   ├── loader.ts             # staged labels + live counter (D-09)
│   └── verdict.ts            # fetch/parse/layout ms + peak heap (D-10)
└── types.ts                  # GraphBuffers { positions, links, nodeCount, edgeCount }
```

### Pattern 1: Swappable Transport Interface
**What:** A single TS interface both the real Dgraph loader and the synthetic generator implement, so JSON-direct → Go-binary-stream is a one-file swap later (PERF-01).
**When to use:** Always — required by the phase.
**Example:**
```typescript
// src/transport/GraphTransport.ts  [ASSUMED — interface shape is researcher's design per D discretion]
export interface GraphBuffers {
  positions: Float32Array;   // [x0,y0, x1,y1, …]  length = nodeCount*2
  links: Float32Array;       // [s0,t0, s1,t1, …]  length = edgeCount*2  (cosmos.gl wants Float32Array)
  nodeCount: number;
  edgeCount: number;
  hexByIndex?: string[];     // uint32 → hex, for hover tooltip (D-14); omit for synthetic
}
export interface LoadProgress { stage: 'fetch'|'parse'|'layout'; edgesSoFar: number; }
export interface GraphTransport {
  load(onProgress: (p: LoadProgress) => void): Promise<GraphBuffers>;
}
```

### Pattern 2: cosmos.gl owns the canvas; shell is read-only on it
**What:** The vanilla/Svelte shell never re-renders the canvas. It only (a) calls `graph.start()/pause()/fitView()` on button clicks and (b) reads the hover index out of `onPointMouseOver` to position a DOM tooltip.
**Why:** Re-render frameworks fight imperative WebGL; keeping the shell thin is the whole reason React is excluded.

### Anti-Patterns to Avoid
- **`JSON.parse` of the full payload on the main thread** → multi-second freeze + memory-doubling OOM. Parse in the Worker, chunked. `[CITED: .claude/CLAUDE.md]`
- **Per-node heap objects (`{id, x, y}[]`)** → blows the 18 GB budget at 5M nodes. SoA typed arrays only (the phase mandates this, and cosmos.gl's API requires it anyway).
- **`Array<number>` for links/positions** → boxed doubles, ~8 bytes + overhead each; use `Float32Array`/`Uint32Array`.
- **Floating to `3.1.0-beta`** → pin `3.0.0` (latest stable).
- **GraphQL `/graphql` for the whole-graph dump** → chatty typed responses, larger payloads. Use DQL `/query`. `[CITED: .claude/CLAUDE.md]`

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| GPU force layout | Custom WebGL/WebGPU compute shaders | cosmos.gl `start()` | The entire engine bet; from-scratch is a Phase-N optimization only `[CITED: .claude/CLAUDE.md]` |
| Pan/zoom transform | Manual matrix math | cosmos.gl (bundles d3-zoom) | `fitView`, drag, pinch all built-in |
| Spatial hit-testing for hover | Quadtree over 5M points on CPU | cosmos.gl `onPointMouseOver` / `findPointsInRect` | GPU-side picking; `[VERIFIED: shipped .d.ts]` |
| Fit-to-view bounds math | Compute bounding box, build transform | cosmos.gl `fitView(duration, padding)` | One call (REND-04 / D-13) |
| Worker RPC plumbing | Hand-rolled `postMessage` correlation | `comlink` (optional) | Only if you want ergonomics; raw transfer is fine for one-shot |

**Key insight:** Phase 1 is almost entirely *wiring an existing engine correctly* + *two measurement spikes*. The only genuinely custom code is the transport interface, the BA generator, the hex→uint32 remap, and the auto-freeze sampler — all small, testable, CPU-side, and Worker-resident.

## Synthetic Power-Law Generator (resolves D-03)

**Goal:** 5,000,000 nodes / 30,000,000 directed edges (avg out-degree 6), heavy hub skew, written straight into `Uint32Array`/`Float32Array` with **zero per-node objects**.

**Recommended algorithm: linear-preferential-attachment (Barabási–Albert variant).** `[ASSUMED — standard graph-theory algorithm; parameters are researcher's specification per D-03]`

- Each new node attaches `m = 6` out-edges (→ ~30M edges for 5M nodes). BA degree distribution is power-law with exponent **γ ≈ 3** — realistic hub skew (a few mega-hubs, long tail of leaves), which matches a follow-graph's shape qualitatively.
- **Efficient O(E) sampling without scanning a degree array:** keep a flat "target pool" `Uint32Array` where each node id appears once per in-edge it has received (the classic BA *edge-copying* trick). To add node `i`'s `m` edges, pick `m` targets by indexing random positions in the pool, then append `i` to the pool `m` times. This makes attachment probability proportional to current degree in O(1) per edge, no cumulative-sum rebuild.
- **Memory of the generator itself:** target pool grows to `2 * edgeCount` entries (each edge contributes one endpoint twice over the run) ≈ 60M × 4 bytes = **240 MB** `Uint32Array`. Link buffer = 30M × 2 × 4 = **240 MB**. Positions = 5M × 2 × 4 = **40 MB**. All flat typed arrays, all transferable.
- **Tunables to expose to the planner (not the user — D-02 fixes scale):** `m` (out-degree, default 6), an optional `m0` seed clique (e.g. 10 fully-connected seed nodes), and an optional fitness/aging tweak if a steeper hub skew is wanted. Default BA (`m=6`, γ≈3) is the recommended starting shape.
- **Positions:** seed each node at a random point in `spaceSize` (`Float32Array`, `Math.random()*4096`). cosmos.gl rescales unless `dontRescale` is passed; random seed is standard for force layout.
- **Determinism:** use a seeded PRNG (e.g. mulberry32) so the spike is reproducible across runs.

**Why not configuration-model:** BA is simpler, produces the hub skew directly, and the edge-copying variant is the fastest O(E) approach that stays in typed arrays. A configuration model needs a pre-specified degree sequence and a matching pass — more code, same realism. BA is the pragmatic pick.

## Dgraph DQL Bulk-Load (resolves the wire path for DATA-01)

**Endpoint:** `POST http://localhost:8080/query`, header `Content-Type: application/dql`, body = DQL query string. `[CITED: .claude/CLAUDE.md "How the Browser Talks to Dgraph"]`

**`after`-cursor paging semantics** `[CITED: docs.dgraph.io/dql/query/pagination + dgraph.io/docs/query-language/pagination]`:
- Syntax on a func or predicate: `func(first: N, after: <uid>)`. `after` takes a **uid** (the uid of the last node in the previous page), and skipping is **O(1)** — strictly preferred over `offset`.
- First page: `after: 0x0` (or omit). Subsequent pages: `after: <last uid returned>`.
- Both endpoints speak **JSON only, no streaming** — each page is one JSON body. Request gzip via `Accept-Encoding: gzip` (the browser sets this automatically on `fetch`; Dgraph honors it). `[CITED: .claude/CLAUDE.md]`

**ID-only `follows` dump (paged over the node set):** `[ASSUMED — query shape is researcher-composed against the documented Profile schema; verify against live dev Dgraph in the spike]`
```graphql
# Content-Type: application/dql  →  POST /query
# $cursor starts at 0x0; bump to the last uid of each page.
{
  q(func: has(follows), first: 50000, after: <cursor>) {
    uid
    follows { uid }
  }
}
```
- `has(follows)` enumerates every node that has at least one follow edge; `first/after` pages the **outer node set** (the stable, uid-ordered axis), so the cursor is the last node's uid each page. Inner `follows { uid }` returns that node's out-edges. Nodes with no `follows` are leaves you discover as edge *targets* anyway.
- This returns **only uids** — the most compact ID-only shape (DQL lets you ask for just `uid`, unlike GraphQL's named-field overhead). `[CITED: .claude/CLAUDE.md]`
- **Followers/in-degree is derived client-side** (per the locked roadmap decision) — do NOT query the `@hasInverse` `followers` edge (avoids double-counting + halves the payload). One O(E) counting pass over the parsed edge list yields in-degree.

**Response shape to parse** `[CITED: Dgraph raw-HTTP docs via .claude/CLAUDE.md]`:
```json
{
  "data": { "q": [ { "uid": "0x1", "follows": [ {"uid":"0x2"}, {"uid":"0x5"} ] }, … ] },
  "extensions": { "server_latency": { "encoding_ns": 1234567, "processing_ns": … } }
}
```
- **`extensions.server_latency.encoding_ns` is the server-side encode cost** — log it; it is part of the verdict (Dgraph can take multiple seconds to encode a huge result). `[CITED: .claude/CLAUDE.md]`
- **Page size (discretion):** start at `first: 50000` outer nodes/page (≈300k edges/page at avg degree 6). Tune in the spike — bigger pages = fewer round-trips but larger per-page parse spikes (the memory-doubling risk lives here, D-08).

> **D-04 reminder:** the dev Dgraph is small. Measure fetch/parse/encoding on it at its real size, record edges/ms throughput, and **extrapolate linearly to 30M edges** for the verdict. This proves the wire; the synthetic generator proves the GPU. They are deliberately separate.

## Memory & Parse Budgeting (D-08 — co-equal risk)

**The wall:** `JSON.parse` is single-shot, blocking, and roughly memory-doubling (parsed JS objects + the source string co-resident). At 30M edges a naive single-string parse on the main thread risks both a multi-second freeze and exceeding the 18 GB pool. `[CITED: .claude/CLAUDE.md]`

**Discipline (all in the Worker):**
1. **Chunked parse, never one big string.** Parse each `after`-page's JSON body independently, extract edges into the growing typed buffer, then **drop the page string** so the GC reclaims it before the next page. Peak transient ≈ one page, not the whole graph.
2. **hex→uint32 remap during parse** (`Map<string, number>`). First sighting of a hex uid → assign next dense index. Edges stored as `[srcIdx, tgtIdx]` `Uint32Array`. This is the dense remap the phase mandates and it also shrinks the buffer (4-byte ints vs ~64-byte hex strings).
3. **Growable typed buffers.** Pre-size if you can estimate (Dgraph `count()` first), else grow geometrically (1.5–2× `Uint32Array` reallocation) — accept transient doubling at the growth step only, or use a chunk-list then a single concat at the end.
4. **cosmos.gl link buffer is `Float32Array`** (verified — `setLinks(links: Float32Array)`). Build the remap in `Uint32Array` for exactness, then produce the `Float32Array` view at hand-off. Note: Float32 mantissa is 24-bit → exact integers only up to 2^24 ≈ 16.7M. **5M node indices fit; do NOT exceed ~16.7M distinct indices in a Float32 link buffer.** At 5M this is safe; flag it if scale ever grows.
5. **Transfer, don't copy.** Pass the `ArrayBuffer`s to the main thread as `Transferable` (zero-copy) — no second resident copy.

**Peak-heap measurement (D-10 verdict metric):** `[CITED: web — MDN measureUserAgentSpecificMemory; performance.memory]`
- `performance.measureUserAgentSpecificMemory()` — most accurate cross-agent JS heap (async, requires cross-origin isolation: `Cross-Origin-Opener-Policy: same-origin` + `Cross-Origin-Embedder-Policy: require-corp` headers in Vite dev config). This is the recommended metric.
- `performance.memory.usedJSHeapSize` — Chrome-only, synchronous, coarse, but zero-setup; fine as a fallback since the reference browser is Chrome (D-07).
- Sample peak at: end of parse, after buffer build, after cosmos.gl `create()`. Log all three (D-10).

## cosmos.gl 3.0.0 — Verified API (the heart of REND-01..04)

All signatures below are `[VERIFIED: shipped .d.ts in @cosmos.gl/graph@3.0.0]` (extracted from `package/dist/index.d.ts` and `config.d.ts`).

### Init & Data Input
```typescript
// Source: @cosmos.gl/graph@3.0.0 dist/index.d.ts + README
import { Graph } from '@cosmos.gl/graph';

const div = document.querySelector('#graph') as HTMLDivElement;
const graph = new Graph(div, {
  spaceSize: 8192,            // default 4096; raise for 5M nodes to reduce overlap
  simulationFriction: 0.85,   // verified default
  simulationDecay: 5000,      // verified default (5e3)
  simulationGravity: 0.25,    // verified default
  simulationRepulsion: 1,     // verified default
  enableSimulation: true,     // run on load (D-11)
  fitViewOnInit: true,        // auto-fit on first layout (REND-04 baseline)
  fitViewDelay: 1000,         // ms before the initial fit
  fitViewPadding: 0.3,
  enableDrag: true,
  // hover (D-14):
  onPointMouseOver: (index, pos, ev, isHighlighted, isOutlined) => showTooltip(index),
  onPointMouseOut: () => hideTooltip(),
});

// Structure-of-arrays buffers (exactly what this phase produces):
graph.setPointPositions(new Float32Array(/* [x0,y0,x1,y1,…] len = nodeCount*2 */));
graph.setLinks(new Float32Array(/* [s0,t0,s1,t1,…] len = edgeCount*2 */));
graph.create();   // apply pending data
graph.start();    // begin GPU force simulation (D-11 auto-start)
```

### Simulation Control (REND-02, D-11/D-12)
`[VERIFIED: shipped .d.ts]` — public methods on the `Graph` instance:
- `start(alpha?: number)` — begin/reheat simulation; `alpha` 0–1 = initial energy. "Only controls simulation state, not rendering."
- `pause()` — stop running but **preserve state** (progress, alpha). → Run/Pause toggle "pause" half (D-12).
- `unpause()` — resume a paused simulation. → Run/Pause toggle "run" half.
- `stop()` — stop **and reset** simulation state. → use for "freeze" if you want to free the sim entirely after settle.
- `step()` — run exactly one simulation step manually, even while paused.
- `render()` / `create()` — apply pending data / trigger a draw without restarting the sim.
- `destroy()` — tear down the instance.

**Mapping to D-12 (Run/Pause + auto-freeze):** Run/Pause button toggles `unpause()`/`pause()`. Auto-freeze = call `pause()` (cheap, reversible) when settled; if you want to fully release GPU sim cost, `stop()`. `pause()` is recommended for freeze since the user can still nudge Run.

### Auto-Freeze Threshold (resolves D-12)
`[ASSUMED — threshold value is researcher's recommendation; tune in the spike]`
- cosmos.gl has no built-in "settled" event in the verified API surface, so **sample motion on the main thread**: periodically (e.g. every 500 ms via `setInterval` or on a rAF tick) call `getPointPositions()` (verified method), compare to the previous sample, compute mean per-node displacement.
- **Recommended threshold:** when mean displacement per node drops below **~0.5 in `spaceSize` units per 500 ms window** for **3 consecutive windows**, call `graph.pause()` and surface "settled". This is a starting value — the spike should confirm it produces a visually stable terrain at 5M nodes and tune as needed.
- Cheaper alternative: sample only a random ~10k-node subset's positions for the displacement metric (5M position reads every 500 ms is itself work).

### Viewport / Fit (REND-04, D-13)
`[VERIFIED: shipped .d.ts]`
- `fitView(duration?: number, padding?: number, enableSimulation?: boolean)` — fit whole graph to screen. **This is the single Fit/Reset action.**
- `fitViewByPointIndices(indices, duration?, padding?)`, `fitViewByPointPositions(positions, …)` — fit to a subset (Phase 3 fly-to; not needed now).
- `zoomToPointByIndex(...)` — single-point zoom (Phase 3).
- Config: `fitViewOnInit`, `fitViewDelay`, `fitViewPadding`, `fitViewDuration` for the initial auto-fit.

### Interaction & Hit-Testing (REND-03, D-14)
`[VERIFIED: shipped .d.ts config callbacks]`
- `onPointMouseOver?: (index, pointPosition, event, isHighlighted, isOutlined) => void` — fires on hover with the **point index** (this is the hover hook for D-14 tooltip).
- `onPointMouseOut?: (event) => void`.
- `onClick?: (index|undefined, pointPosition|undefined, event) => void` and `onPointClick?: (index, pointPosition, event) => void`.
- `onMouseMove?: (index|undefined, pointPosition|undefined, event) => void`.
- `findPointsInRect(rect: [[x,y],[x,y]]): number[]`, `findPointsInPolygon(...)` — rectangular/polygon selection (Phase 3 lasso; not needed now).
- Highlight via config: `highlightedPointIndices`, `outlinedPointIndices` (set + `render()`), or rely on the `isHighlighted` flag in the callback for D-14's visual highlight.
- Pan/zoom (drag, wheel, pinch) are built in via the bundled d3-zoom; no wiring needed beyond `enableDrag`.

### Tooltip (D-14)
On `onPointMouseOver(index, …)`, position a DOM tooltip at the mouse event coords showing `hexByIndex[index]` (real load) or the raw `index` (synthetic, no hex). Use `spaceToScreenPosition(...)` (verified method) if you want to anchor to the node rather than the cursor.

## Common Pitfalls

### Pitfall 1: Float32 link buffer integer-precision ceiling
**What goes wrong:** node indices > 2^24 (16.7M) lose precision in the `Float32Array` cosmos.gl requires for links → wrong edges.
**Why:** Float32 has a 24-bit mantissa.
**How to avoid:** Build/remap in `Uint32Array`; only ≤16.7M distinct indices. At 5M nodes you are safe (5M < 16.7M). **Add an assertion** `nodeCount < 16_777_216` before producing the Float32 link view.
**Warning signs:** edges connecting to wrong/duplicate nodes only at high indices.

### Pitfall 2: Per-page parse memory spike (D-08)
**What goes wrong:** A large `first:` page produces a multi-hundred-MB JSON string; parsing it doubles transiently and can spike past the 18 GB pool when combined with the growing buffer.
**How to avoid:** Moderate page size (~50k nodes), drop each page string immediately after extracting edges, sample peak heap per page. If a page spikes, halve the page size.
**Warning signs:** `usedJSHeapSize` jumps then drops sharply each page; tab "Aw, Snap" at large pages.

### Pitfall 3: Importing the wrong/legacy package
**What goes wrong:** `@cosmograph/cosmos` (legacy, `2.0.0-beta.20`) has the old `setData` API, not `setPointPositions`/`setLinks`.
**How to avoid:** Import `@cosmos.gl/graph@3.0.0` exactly. Verify `package.json` after install.

### Pitfall 4: cross-origin isolation missing → no accurate memory metric
**What goes wrong:** `measureUserAgentSpecificMemory()` throws/returns nothing without COOP+COEP headers.
**How to avoid:** Add COOP/COEP headers to Vite dev server config; or fall back to Chrome's `performance.memory.usedJSHeapSize` (D-07 is Chrome).

### Pitfall 5: Running the motion-sampler over all 5M positions
**What goes wrong:** Reading 5M positions every 500 ms to detect settle is itself a perf drain competing with the 60fps budget.
**How to avoid:** Sample a fixed random subset (~10k nodes) for the displacement metric.

## Code Examples

### Worker → main zero-copy buffer hand-off
```typescript
// synthetic.worker.ts  [ASSUMED — standard Worker transfer pattern]
const { positions, links, nodeCount, edgeCount } = generateBA(5_000_000, 6);
// transfer the underlying ArrayBuffers (zero-copy):
self.postMessage(
  { positions, links, nodeCount, edgeCount },
  [positions.buffer, links.buffer]
);
```

### Verdict readout (D-10)
```typescript
// verdict.ts  [ASSUMED — composed from MDN performance APIs]
const t0 = performance.now();
/* …fetch… */ const fetchMs = performance.now() - t0;
/* …parse+build… */ const parseMs = performance.now() - t0 - fetchMs;
/* after graph.create() + first settle */ const layoutReadyMs = performance.now() - t0;
const peak = await (performance as any).measureUserAgentSpecificMemory?.()
  ?? { bytes: (performance as any).memory?.usedJSHeapSize };
console.table({ fetchMs, parseMs, layoutReadyMs, nodeCount, edgeCount, peakHeapBytes: peak.bytes });
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `@cosmograph/cosmos` (regl, `setData`) | `@cosmos.gl/graph` 3.x (luma.gl/WebGL2, `setPointPositions`/`setLinks`) | v3 / OpenJS rename, 2026-06-17 | Package name + data API both changed; use the new ones |
| `JSON.parse` whole payload on main | Chunked Worker parse + Transferable | (project decision) | Avoids freeze + OOM at 30M edges |

**Deprecated/outdated:**
- `@cosmograph/cosmos` name and `setData()` — superseded by `@cosmos.gl/graph` and `setPointPositions`+`setLinks`.

## Validation Architecture

> `workflow.nyquist_validation` config not present in this repo; treated as enabled. This phase's "tests" are dominated by the two feasibility spikes (the verdict) plus pure-function unit tests for the data pipeline. The GPU/60fps and at-scale memory facets are **manual-instrumented spikes**, not automatable assertions.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (ships with Vite 6) |
| Config file | none yet — Wave 0 creates `vitest.config.ts` (can share `vite.config.ts`) |
| Quick run command | `npx vitest run --reporter=dot` |
| Full suite command | `npx vitest run` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DATA-03 | hex→uint32 remap is dense, stable, collision-free | unit | `npx vitest run tests/remap.test.ts` | ❌ Wave 0 |
| DATA-03 | BA generator yields exactly N nodes / ~M edges, power-law degree, no per-node objects | unit | `npx vitest run tests/generator.test.ts` | ❌ Wave 0 |
| DATA-01 | DQL response parser turns the documented JSON shape into correct edge pairs | unit | `npx vitest run tests/parse.test.ts` | ❌ Wave 0 |
| DATA-01 | transport interface: Synthetic + Dgraph both return `GraphBuffers` | unit | `npx vitest run tests/transport.test.ts` | ❌ Wave 0 |
| DATA-03 | Float32 index-precision guard asserts `nodeCount < 2^24` | unit | `npx vitest run tests/precision.test.ts` | ❌ Wave 0 |
| REND-01..04 | render/layout/pan/zoom/hover/fit | **manual-only spike** | n/a — verified via Chrome DevTools Performance (60fps trace) + visual | ❌ (manual) |
| DATA-01/03 | feasibility verdict (fetch/parse/layout ms, peak heap) | **manual-instrumented** | recorded from D-10 readout against real dev Dgraph + synthetic | ❌ (spike) |

**Manual-only justification:** GPU frame rate, visual settle, and at-scale memory cannot be asserted headlessly with fidelity; they are the point of the spike. The verdict readout (D-10) is the recorded artifact. Everything CPU-side (remap, generator, parser, transport, precision guard) IS unit-tested.

### Sampling Rate
- **Per task commit:** `npx vitest run --reporter=dot` (CPU pipeline tests; sub-second).
- **Per wave merge:** `npx vitest run` (full suite).
- **Phase gate:** full suite green + recorded feasibility verdict (PASS ≤~30s extrapolated, D-05) before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `vitest.config.ts` (or extend `vite.config.ts`) — framework config
- [ ] `tests/remap.test.ts` — DATA-03 (hex→uint32)
- [ ] `tests/generator.test.ts` — DATA-03 (BA generator)
- [ ] `tests/parse.test.ts` — DATA-01 (DQL JSON → edges)
- [ ] `tests/transport.test.ts` — DATA-01 (interface conformance)
- [ ] `tests/precision.test.ts` — DATA-03 (Float32 index guard)
- [ ] Framework install: `npm install -D vitest`

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Node + npm | Vite scaffold, build | ✓ (npm verified live this session) | npm reachable | — |
| Dgraph @ :8080 | DATA-01 real-wire verdict (D-04) | ✗ (not probed — not running by default; `docker-compose -f docker-compose.dgraph.yml up -d` from deepfry root) | — | Synthetic transport (D-01) proves GPU side regardless; real-wire verdict needs Dgraph up |
| Chrome | D-07 reference browser, DevTools Performance, `chrome://gpu` | (assumed present, dev machine) | — | — |
| WebGL2 | cosmos.gl render+sim | (universal in Chrome on M3) | — | — |
| `measureUserAgentSpecificMemory` | D-10 peak-heap metric | needs COOP/COEP headers in Vite | — | `performance.memory.usedJSHeapSize` (Chrome) |

**Missing dependencies with no fallback:** none that block — the synthetic spike (the GPU ceiling, the harder risk) needs no external infra (D-01).
**Missing dependencies with fallback:** Dgraph must be brought up (`docker-compose.dgraph.yml`) for the real-wire verdict (D-04); until then the JSON-wire half of the verdict is blocked, but the GPU half proceeds.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | BA `m=6`, γ≈3 is a "realistic" follow-graph shape | Synthetic Generator | Spike proves GPU ceiling against a shape that doesn't match real WoT skew; low risk — it's a stress shape, and D-04 measures real data separately |
| A2 | DQL query `q(func: has(follows), first:N, after:<uid>){uid follows{uid}}` returns the intended ID-only edge dump on the live schema | Dgraph Bulk-Load | Wrong query → empty/incorrect dump; **must verify against live dev Dgraph in the spike** before trusting the verdict |
| A3 | Auto-freeze threshold ≈0.5 units/500ms × 3 windows | Auto-Freeze | Too tight → never freezes; too loose → freezes mid-settle. Tune in spike (D-12 explicitly allows). |
| A4 | Page size `first: 50000` nodes is a safe parse-memory chunk | Memory Budgeting | Too big → memory spike (D-08); halve if `usedJSHeapSize` spikes. Tune in spike. |
| A5 | Transport interface shape (`GraphBuffers`/`GraphTransport`) | Pattern 1 | Pure design; researcher's discretion per CONTEXT. No external dependency. |
| A6 | `extensions.server_latency.encoding_ns` present on raw-HTTP DQL responses | Dgraph response shape | If absent, derive server time another way; verdict still works from client-side fetch/parse ms. |

**Note:** the cosmos.gl 3.0.0 API claims are NOT assumptions — they are verified against the package's shipped `.d.ts` and are tagged `[VERIFIED: shipped .d.ts]` throughout.

## Open Questions

1. **Does WebGL2 layout hold 60fps at 5M nodes / 30M edges on the M3 Pro?**
   - What we know: cosmos.gl claims 1M+ interactively; M3 Pro has a 14-core GPU; engine API is correct.
   - What's unclear: independent 5M-node live-layout benchmarks are scarce (flagged in `.claude/CLAUDE.md` open questions).
   - Recommendation: this IS the synthetic spike (D-01/D-02). Measure FPS via Chrome DevTools Performance; if it fails, the documented Phase-N WebGPU compute-shader layout is the escalation (not Phase 1's job).

2. **Does the real-Dgraph JSON wire extrapolate to ≤~30s at 30M edges (D-05)?**
   - What we know: no streaming, blocking parse, multi-second server encode possible.
   - What's unclear: real dev-DB size and edges/ms throughput until measured.
   - Recommendation: the D-04 real-wire spike. If extrapolation >~30s → trigger PERF-01 (Go bridge) per D-05.

3. **Does peak JS heap stay under the 18 GB pool during parse+buffer build (D-08)?**
   - What we know: chunked parse + transferable + uint32 remap are the mitigations.
   - What's unclear: actual peak until measured with `measureUserAgentSpecificMemory`.
   - Recommendation: instrument per-page peak; if it approaches the pool, shrink page size or move to the Go binary stream early.

## Sources

### Primary (HIGH confidence)
- `@cosmos.gl/graph@3.0.0` shipped type definitions (`dist/index.d.ts`, `dist/config.d.ts`) — extracted this session via `npm pack`; verified `setPointPositions`, `setLinks`, `create`, `start(alpha)`, `pause`, `unpause`, `stop`, `step`, `render`, `destroy`, `fitView`, `findPointsInRect`, `getPointPositions`, `getSampledLinks`, `spaceToScreenPosition`, `onPointMouseOver`/`onPointMouseOut`/`onClick`/`onPointClick`, config defaults (`spaceSize:4096`, `simulationDecay:5e3`, `simulationFriction:0.85`, `simulationGravity:0.25`, `simulationRepulsion:1`).
- npm registry (`npm view`) — `@cosmos.gl/graph` 3.0.0 latest (pub 2026-06-17), legacy `@cosmograph/cosmos` 2.0.0-beta.20, downloads 17.9k/wk, repo, no postinstall; `comlink` 4.4.2.
- `.claude/CLAUDE.md` (pre-researched stack + "How the Browser Talks to Dgraph" + WebGL2-vs-WebGPU verdict) — the engine bet, treated as canonical HOW spec.

### Secondary (MEDIUM confidence)
- cosmos.gl GitHub README + 2.0 migration notes (`github.com/cosmosgl/graph`) — `setData`→`setPointPositions`/`setLinks` Float32Array format, config props.
- Dgraph docs — pagination (`docs.dgraph.io/dql/query/pagination`, `dgraph.io/docs/query-language/pagination`): `after: <uid>` O(1), `first: N` semantics.

### Tertiary (LOW confidence)
- WebSearch on DQL response shape / raw-HTTP `extensions.server_latency` — corroborated by `.claude/CLAUDE.md`; verify query against live dev Dgraph in spike.
- BA generator parameters — standard graph theory (training knowledge); a stress shape, not a measured fit.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions verified on npm, cosmos.gl API verified against shipped types.
- cosmos.gl API (REND-01..04 wiring): HIGH — read from `.d.ts`.
- Dgraph query shape: MEDIUM — paging semantics cited from docs; exact query must be confirmed against live schema (A2).
- Synthetic generator / thresholds / page sizes: MEDIUM — sound defaults, explicitly tunable in the spike per CONTEXT discretion.
- At-scale 60fps + memory feasibility: the open question this phase resolves — not pre-claimable.

**Research date:** 2026-06-23
**Valid until:** 2026-07-07 (cosmos.gl is fast-moving — currently 3.0.0 stable with 3.1.0-beta already out; re-verify version before a delayed install).

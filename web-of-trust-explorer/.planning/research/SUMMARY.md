# Project Research Summary

**Project:** WoT Graph Explorer
**Domain:** Browser-based GPU graph explorer — single global node-link map of a directed Web-of-Trust follow-graph (500k–5M+ nodes, tens of millions of directed edges) loaded from Dgraph, with live GPU force layout and 60fps pan/zoom/hover
**Researched:** 2026-06-22
**Confidence:** MEDIUM-HIGH

## Executive Summary

This is a local, read-only developer tool whose single core value is **smooth 60fps interaction with the whole follow-graph at once** so a developer can see its terrain — hubs, clusters, bridges, dense vs sparse regions. At the target scale (500k–5M+ nodes, tens of millions of directed edges, laid out and rendered live in one browser tab) the field of viable approaches collapses to essentially one engine. All four research tracks converged: **cosmos.gl** (`@cosmos.gl/graph`, the OpenJS-Foundation continuation of `@cosmograph/cosmos`) is the only maintained library that runs the force simulation **and** renders on the GPU at million-node scale. Everything classic (D3-force, Cytoscape, vis-network, Sigma-CPU) chokes by ~50k nodes; deck.gl renders fast but cannot lay out live. The engine choice is therefore not a toss-up — it is cosmos.gl on WebGL2, with a custom WebGPU compute-shader layout flagged only as a later, measurement-gated optimization for the 5M ceiling.

The dominant risk is **not** the renderer. It is the **browser-direct JSON pull of tens of millions of edges from Dgraph**: the wire has no streaming, JSON balloons (10M+ edges ≈ 100–250 MB compact, far more naively), and `JSON.parse` is single-shot and blocking. The named, committed escape hatch is a **thin Go binary-streaming bridge** behind a transport interface — designed-for now, built only when measured load time demands it. Two further hard rules emerge from research: (1) **remap 64-char hex pubkeys to dense uint32 indices at load** (~30× smaller edges, GPU-uploadable, O(1) attribute lookups), keeping a structure-of-arrays typed-buffer model throughout the hot path; and (2) **topology is static, style is dynamic** — overlays (degree sizing, community color, time filters) rewrite only style typed arrays and filter by alpha/dim, never removing nodes or re-running layout.

The mitigation that ties everything together: a **mandatory feasibility spike against SYNTHETIC ~5M-node / ~30M-edge power-law data before committing the architecture.** The docker-compose dev DB holds only a tiny subset and hides every edge-scale risk (wire size, parse blocking, GPU layout-space ceiling, WebGL index limits). Validate load time, layout convergence, and 60fps against real-scale synthetic data first; assume nothing from vendor "1M+" marketing copy.

## Key Findings

### Recommended Stack

cosmos.gl on WebGL2 is the engine; the rest of the stack is a deliberately lean single-page tool. Vite + TypeScript for the build, vanilla TS or Svelte 5 for a thin control-panel shell (React is explicitly avoided — its re-render model fights an imperative WebGL canvas). graphology hosts the in-memory model and its algorithm ecosystem (degree, Louvain), with analytics run in a Web Worker via comlink. Data comes from Dgraph's **DQL `/query` endpoint** (not GraphQL) for the most compact ID-only edge projection.

**Core technologies:**
- **cosmos.gl** (`@cosmos.gl/graph` 3.x): GPU force-layout **and** render engine — the whole map. The only maintained engine doing both at million-node scale; live layout is its core feature (no precompute needed), matching the project constraint exactly.
- **TypeScript 5.x + Vite 6.x**: TS-native engine, instant HMR, native ESM, zero SSR/routing need. Lean single-page tooling.
- **graphology 0.25.x + graphology-communities-louvain / -metrics**: in-memory graph data model and the host for degree and Louvain community detection; feeds cosmos.gl typed position/link arrays.
- **nostr-tools 2.x + comlink 4.x**: npub↔hex (NIP-19) decode for search; ergonomic Web Worker RPC so JSON parse and Louvain never block the render loop.
- **Dgraph DQL `/query`** (`application/dql`): compact integer-adjacency edge projection; reserve `/graphql` for single-pubkey search only.

### Expected Features

The product is **legibility of terrain, not analytics.** Every feature is judged by whether it helps a developer see and move across the graph's shape at 60fps. Cost splits into per-frame work (must be GPU or O(visible) — pan/zoom/hover/sizing/coloring) and one-shot async work (degree, community, components — run once in a Worker, never blocking the UI).

**Must have (table stakes):**
- Pan / zoom / hover at 60fps on the full graph — the core value
- Bulk-load whole graph once, operate in-memory — foundation for everything (also the biggest risk)
- Live GPU force-directed layout — produces the spatial terrain
- Degree-based node sizing & coloring — the primary hub-revealing encoding (in/out degree distinct for a directed graph)
- Community detection + community coloring — primary region-revealing encoding (Louvain in a Worker, async one-shot)
- Search (hex/npub) → fly-to + 1-hop neighborhood highlight
- Activity/freshness time filtering (`kind3CreatedAt` / `last_db_update`) via hide/dim, never re-layout
- Refresh (re-pull from Dgraph) — explicit button, not automatic
- Hover tooltip / click-to-focus + fit-to-screen — cheap baseline completeness, do not omit

**Should have (competitive):**
- Connected-components coloring/isolate — "is this one blob or many islands?" (union-find, Worker)
- Degree-distribution histogram + brush-to-filter — quantifies the power-law tail, doubles as a filter
- Box/lasso select → isolate cluster; k-core/coreness coloring (peels periphery to expose the trust-core); directionality cues (encode via node in/out degree + edge color, not per-edge arrowheads)
- Local layout cache — frame as a cache of the last live layout, not an offline pipeline

**Defer (v2+):**
- Density/heatmap overlay — only if the hairball proves unreadable
- Approximate PageRank / sampled betweenness — degree + k-core may answer "importance" well enough
- Minimap — navigation nice-to-have, not terrain-critical
- Thin Go streaming bridge — escape hatch, built only if v1 JSON load is unacceptable

**Anti-features (explicitly out):** editing/writing data (violates DeepFry data-separation rule), auth/multi-user, sharing/saved views, server-side rendering/hosting, trust-score computation, full-graph edge bundling, exhaustive betweenness centrality, live continuous re-querying, per-node event-content panels (event payloads do not live in Dgraph).

### Architecture Approach

A strict one-way pipeline: data leaves Dgraph once per session through a **swappable transport seam**, is normalized into dense typed-array columns with a `pubkey->uint32` remap, handed to the cosmos.gl wrapper for live GPU layout + render, and overlays mutate **only style buffers, never topology.** No backend service in v1 — the browser talks to Dgraph directly. The optional Go bridge is a drop-in replacement for the loader's transport and nothing else.

**Major components:**
1. **transport adapter** — issues data requests, abstracts the wire protocol; the single seam where the Go bridge later plugs in (`fetch()` POST to `:8080/query` in v1; binary/gzip stream in v2).
2. **data-loader** — pages the full node + edge list out of Dgraph (cursor `after`, nodes-first then edges), builds the dense `pubkey->uint32` map, emits flat typed arrays; owns retry/progress.
3. **graph-store** — canonical in-memory model: `Float32Array`/`Uint32Array` columns for nodes (idx, pubkey, timestamps, degree, community) and edges (src/dst idx) + the pk->idx map. Framework-agnostic; movable into a Worker.
4. **render/layout engine wrapper** — thin façade over cosmos.gl (`setPointPositions`/`setLinks`/`setPointColors`/`setPointSizes`, camera, pick/hover); cosmos.gl never imported outside this module.
5. **overlays/analytics** — degree, community, filter masks → style typed arrays; topology read-only.
6. **UI controls + search** — thin DOM/Svelte shell; npub/hex decode → map lookup → fly-to.

Three load-bearing patterns: **(1) integer-index node remapping** (dense ids, nodes-first load); **(2) cursor `after` paging** for bulk export (O(1) per page vs offset's O(N)); **(3) topology-static / style-dynamic buffers** (recolor = one texture update, filter by alpha).

### Critical Pitfalls

1. **Hex pubkeys over the wire and into GPU buffers** — 64-char strings can't go in a typed array and bloat edges ~30×. Establish a `uint32` index space at load, discard hex from the hot path, keep one pk->idx map for search/display only.
2. **Pulling the whole graph in one Dgraph query** — multi-GB blob times out / OOMs; `offset` paging is O(N) and degrades super-linearly. Use `after`-cursor paging in 50k–200k batches, nodes and edges in separate flat passes; treat browser-direct as on probation with the Go bridge as the escape hatch.
3. **`@hasInverse` double-counting** — `followers` is the materialized reverse of `follows`; querying both pulls every edge twice. Query **only `follows`**; derive in-degree/followers client-side.
4. **CPU/SVG/Canvas force layout** — looks fine at 5k, a slideshow at 500k. Commit to GPU layout-and-render from the first rendering phase; a CPU MVP is throwaway, not a stepping stone.
5. **GPU layout simulation-space ceiling at 5M** — cosmos.gl's bounded sim-space may not fit several million nodes ("they may not fit at all"). Validate against actual max N in a spike; wire a graceful degradation (node sampling or one-time precomputed snapshot) to a measured threshold; pin the engine version.
6. **Object-per-node/edge model** — tens of millions of heap objects OOM the tab and cause GC pauses that drop frames. Structure-of-arrays typed buffers, zero per-frame allocation in render/layout/pick loops.
7. **Validating only against the docker-compose dev DB** — small data hides every edge risk. Generate synthetic 5M/30M power-law data and benchmark load + layout + fps in the first feasibility spike; warm Dgraph's cold cache before timing.

(Also: WebGL 32-bit index limits — require WebGL2; CPU per-mousemove picking — use GPU picking + frame throttle; client-side centrality OOM — degree client-side, community in a Worker, skip exact betweenness; losing directedness — track in/out degree separately; over-scoping into polish before a full-scale 60fps demo exists.)

## Implications for Roadmap

Research is unanimous on a coarse build order: a data spine first, the core-value render second, overlays/analytics third, explorer affordances fourth, and a transport-only optimization gated last. A **feasibility spike against synthetic full-scale data is a mandatory precondition** to committing the architecture and should front-load Phase 1 (or be a brief pre-phase). Suggested 4–5 phases:

### Phase 1: Feasibility spike + transport + bulk load + remap (the data spine)
**Rationale:** Nothing can render until data is in dense typed arrays, and the architecture must not be committed until validated against real-scale data. The spike de-risks the dominant wire/layout risks before they bake in.
**Delivers:** synthetic 5M/30M power-law dataset + benchmark; transport seam (HTTP/DQL impl) with `after`-cursor paging; nodes-first → edges load building the `pubkey->uint32` remap into typed-array columns in the store; loading progress.
**Addresses:** Bulk-load whole graph in-memory (table stakes).
**Avoids:** Pitfalls 1 (hex in buffers), 2 (un-paginated query), 3 (`@hasInverse` double edges), 6 (object model), 12 (dev-DB-only testing). Ends with an explicit checkpoint: is browser-direct JSON load fast enough, or trigger the Go bridge (Phase 5)?

### Phase 2: GPU render + live layout (the core value)
**Rationale:** The render IS the product; land it second so it can be validated early against the Phase-1 synthetic data.
**Delivers:** cosmos.gl engine wrapper fed the store's positions/links; GPU force sim with run/pause; pan/zoom/hover/pick at 60fps; fit-to-screen; hover tooltip.
**Uses:** cosmos.gl 3.x (WebGL2), the engine façade.
**Implements:** render/layout engine wrapper component; topology-static buffer pattern.
**Avoids:** Pitfalls 4 (CPU layout), 5 (GPU layout-space ceiling — validate here, wire fallback to a threshold), 7 (WebGL index limits — capability check at init), 8 (GPU picking).

### Phase 3: Overlays + analytics (degree, community, color/size)
**Rationale:** Layers on top of a working render without touching topology; the second core terrain question after "can I move."
**Delivers:** degree pass (in/out distinct) → size buffer; Louvain community detection in a Web Worker → color buffer; connected components optional; recolor/resize via style buffers without re-upload.
**Uses:** graphology + graphology-communities-louvain/-metrics, comlink Worker.
**Implements:** overlays/analytics component; style-dynamic buffer pattern.
**Avoids:** Pitfalls 9 (client-side analytics OOM — Worker, degree cheap), 10 (directedness lost — track both degrees).

### Phase 4: Search + time filters + refresh
**Rationale:** The explorer affordances; depend on the map (Phase 2 camera), the store (Phase 1 map/columns), and the buffer pattern (Phase 3).
**Delivers:** npub/hex (NIP-19) search → fly-to + 1-hop neighborhood highlight; `kind3CreatedAt`/`last_db_update` range filters via alpha/visibility buffers (never re-layout); refresh re-runs the load + recomputes degree/community.
**Uses:** nostr-tools NIP-19 decode.
**Avoids:** filtering must never re-layout (hard architectural rule); npub-input search miss; fly-to-into-the-void (highlight neighborhood); refresh must rebuild the index space cleanly without leaking old buffers.

### Phase 5 (deferred, threshold-gated): Go binary-streaming bridge
**Rationale:** A transport-only optimization, built only if Phase-1's measured load time at real scale demands it. The clean transport seam makes it additive — no changes to Phases 2–4.
**Delivers:** `bridge.ts` against the existing transport interface; a thin Go service streaming gzip'd binary edge ints (length-prefixed `Int32Array` or Arrow IPC) over a `ReadableStream`; browser fills GPU buffers with zero JSON parsing.
**Avoids:** the JSON parse-blocking cliff at 5M (Recovery for Pitfall 2).

### Phase Ordering Rationale
- **Dependencies run strictly left->right:** data spine -> render -> overlays -> explorer affordances -> optional bridge. Each phase is independently demonstrable.
- **Risk is front-loaded:** the spike + load phase confronts the two load-bearing risks (the wire, the frame ceiling) before architecture is committed, so a pivot to the Go bridge or layout-sampling fallback is cheap rather than a rewrite.
- **The transport seam keeps the bridge removable:** browser-direct ships in v1; the bridge is purely additive behind the interface — deliberately last and gated on measurement.
- **Scope guard across all phases:** no cosmetic/polish work merged before a full-scale 60fps demo exists (Pitfall 11).

### Research Flags

Phases likely needing deeper research during planning (`/gsd-plan-phase --research-phase <N>`):
- **Phase 1:** the exact current npm name/version of cosmos.gl (recent `@cosmograph/cosmos` -> `@cosmos.gl/graph` rename) needs confirming at install; synthetic power-law generator design and realistic Dgraph dump shape/size against real data need verification — this is the highest-uncertainty area.
- **Phase 2:** cosmos.gl's exact API surface (verified across sources, not hands-on) and its real 5M-node layout-space behavior / 60fps on the target GPU need hands-on validation; the WebGPU fallback path is unscoped.

Phases with standard patterns (likely skip research-phase):
- **Phase 3:** graphology degree/Louvain in a Worker are well-documented; degree is trivial O(E).
- **Phase 4:** NIP-19 decode, alpha-buffer filtering, and fly-to are established patterns once the buffer model from Phase 3 exists.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | MEDIUM-HIGH | Engine choice (cosmos.gl) is decisive and corroborated; exact npm identifier/version is recent and MEDIUM. Browser-direct JSON feasibility at 5M is the genuine edge. |
| Features | MEDIUM | Feature canon cross-checked against Gephi/sigma.js/Cosmograph; million-node client-side timing estimates are extrapolations from published JS benchmarks (LOW where noted). |
| Architecture | MEDIUM | Dgraph pagination/`@hasInverse` semantics HIGH; cosmos.gl API verified across sources but not a hands-on prototype (MEDIUM). |
| Pitfalls | MEDIUM-HIGH | Cosmos limits, WebGL 32-bit index, Dgraph `@hasInverse` storage, offset-pagination behavior verified against primary sources; scale-threshold numbers are engineering estimates. |

**Overall confidence:** MEDIUM-HIGH on approach and engine; MEDIUM on upper-scale (5M + live layout + browser-direct JSON) feasibility, which has a committed fallback.

### Gaps to Address

- **Real 5M-node 60fps on the target GPU** — vendor claims "1M+, not the limit," but independent 5M live-layout benchmarks are scarce, and cosmos.gl has a stated simulation-space ceiling. *Handle in the Phase-1 spike against synthetic full-scale data; wire layout-sampling/precompute fallback to a measured node-count threshold.*
- **Browser-direct JSON wall at tens of millions of edges** — the single biggest risk. *Prototype the actual Dgraph dump size/time against real data in Phase 1; the Go binary-stream bridge (Phase 5) is the committed escape hatch behind the transport seam.*
- **Exact cosmos.gl npm name/version** — confirm on npm before pinning (recent OpenJS rename). *Resolve at Phase-1/Phase-2 install.*
- **Louvain runtime at 5M nodes** — seconds-to-tens-of-seconds extrapolated, not measured. *Validate in the Phase-3 Worker; if quality/latency hurt, escalate community detection to Leiden in the Go bridge.*

## Sources

### Primary (HIGH confidence)
- Dgraph DQL pagination docs + issue #5807 — `after` (UID) O(1) vs `offset` O(N), offset scalability limits
- Dgraph `@hasInverse` discussions — `followers` materialized as full reverse of `follows` (two predicates)
- WebGL 32-bit index spec / `OES_element_index_uint` / WebGL2 registry — 65535 -> 4.29B index ceiling
- DeepFry `CLAUDE.md` + `.planning/PROJECT.md` — Dgraph schema, scale target, load model, browser-direct decision + escape hatch

### Secondary (MEDIUM confidence)
- OpenJS Foundation "Introducing cosmos.gl" + cosmosgl/graph README + npm `@cosmos.gl/graph` — GPU force layout+render, 1M+ claim, WebGL2/luma.gl, stated simulation-space limit
- Dgraph raw HTTP `/query` docs — `application/dql`, JSON `{data,extensions}`, no streaming, `respFormat=rdf` larger
- Nightingale DVS "How to Visualize a Graph with a Million Nodes" — Cosmograph as the million-node browser tool; CPU chokes ~100k
- Sigma.js v3 release notes + perf issues #239/#967 — WebGL layout ceiling ~50k edges
- deck.gl performance docs — ScatterplotLayer ~1M @60fps; no force layout
- npm graphology-communities-louvain — 50k/994k-edge ~938ms benchmark; directed-modularity support
- Gephi / Gephi Lite tutorials — canonical exploration feature set
- WebGPU support / WebGL-vs-WebGPU 2026 articles — ~83% support, ~150x compute-shader speedup, no iOS Safari

### Tertiary (LOW confidence)
- Million-node browser community-detection timing — extrapolated from published smaller benchmarks; validate in Worker
- Library-comparison blog posts (Cylynx, weber-stephen Medium) — layout-ceiling figures corroborating Sigma/canvas limits
- Edge-bundling / density-map cost papers — supports deferring edge bundling in favor of density overlay

---
*Research completed: 2026-06-22*
*Ready for roadmap: yes*

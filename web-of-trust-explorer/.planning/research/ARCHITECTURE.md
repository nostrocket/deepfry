# Architecture Research

**Domain:** Browser-based GPU explorer for a large directed Web-of-Trust follow-graph stored in Dgraph
**Researched:** 2026-06-22
**Confidence:** MEDIUM (Dgraph pagination semantics HIGH; cosmos.gl API MEDIUM — verified across multiple sources but not a hands-on prototype)

## Standard Architecture

A single-page browser app with a strict one-way pipeline: data leaves Dgraph once per session, is normalized into dense typed-array columns in the browser, handed to a GPU graph engine for live layout + render, and overlays mutate only style buffers (never topology). There is no backend service in v1 — the browser talks to Dgraph directly over HTTP. The optional Go bridge is a drop-in replacement for the *loader's transport*, nothing else.

### System Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                            Dgraph (read-only)                          │
│   HTTP/GraphQL :8080   ·   DQL /query :8080   ·   gRPC :9080           │
│   Profile { pubkey @id, follows @hasInverse(followers), kind3CreatedAt,│
│             last_db_update }                                           │
└───────────────────────────────┬────────────────────────────────────────┘
                                 │  (v1: paged DQL JSON over fetch())
                  ┌──────────────┴───────────────┐
                  │   [SEAM] Transport adapter    │  ← swap point for Go bridge
                  └──────────────┬───────────────┘
                                 ▼
┌──────────────────────────────────────────────────────────────────────┐
│                          BROWSER (single page app)                     │
│  ┌─────────────┐   ┌──────────────┐   ┌────────────────────────────┐   │
│  │ data-loader │──▶│ graph-store  │──▶│  render/layout engine       │   │
│  │ (paged pull,│   │ (typed-array │   │  wrapper (cosmos.gl)         │   │
│  │  remap)     │   │  columns +   │   │  - setPointPositions         │   │
│  └─────────────┘   │  pk→idx map) │   │  - setLinks                  │   │
│        │           └──────┬───────┘   │  - GPU force sim @60fps      │   │
│        │                  │           │  - pan/zoom/hover/pick       │   │
│        │                  ▼           └──────────────┬─────────────┘   │
│        │           ┌──────────────┐                  │                  │
│        │           │ overlays /   │  writes only     │ render()         │
│        │           │ analytics    │  style buffers ─▶│ (no re-upload of │
│        │           │ (degree,     │  setPointColors  │  positions/links)│
│        │           │  community,  │  setPointSizes   │                  │
│        │           │  filters)    │                  │                  │
│        │           └──────┬───────┘                  │                  │
│        ▼                  ▼                           ▼                  │
│  ┌──────────────────────────────────────────────────────────────────┐ │
│  │              UI controls + search (pubkey hex/npub → idx)          │ │
│  └──────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Typical Implementation |
|-----------|----------------|------------------------|
| **transport adapter** | Issues data requests, abstracts wire protocol. The single seam where the Go bridge later plugs in. | `fetch()` POST to `:8080/query` (DQL) or `/graphql` in v1; same interface backed by a binary/gzip stream in v2 |
| **data-loader** | Pages the full node + edge list out of Dgraph, builds the dense `pubkey → uint32 index` map, emits flat typed arrays. Owns retry/progress. | Async generator over cursor pages (`first:N, after:lastUID`); JS `Map<string,number>` for remap |
| **graph-store** | Canonical in-memory model: column arrays for nodes (idx, pubkey, kind3CreatedAt, last_db_update, degree, community) and edges (src idx, dst idx). Source of truth that overlays read and engine consumes. | `Float32Array`/`Uint32Array` columns + the pk→idx `Map`; plain object, framework-agnostic |
| **render/layout engine wrapper** | Thin façade over cosmos.gl. Receives positions/links once; exposes recolor/resize/filter mutations, camera (fly-to), and pick/hover events. Isolates the rest of the app from the engine's API. | `@cosmos.gl/graph` (`setPointPositions`, `setLinks`, `setPointColors`, `setPointSizes`); wrapper class hides it |
| **overlays / analytics** | Computes derived per-node attributes (degree sizing, community detection) and filter masks; writes them into style typed arrays. Never touches topology. | CPU passes over store columns; produces `Float32Array` color/size buffers fed to the wrapper |
| **UI controls** | Sliders/toggles for degree sizing, community palette, time filters (`kind3CreatedAt`, `last_db_update`), refresh action. | Lightweight DOM / Svelte / React — small surface |
| **search** | pubkey (hex or npub) → node index via the store's map; triggers camera fly-to + neighborhood highlight. | npub decode (bech32) → hex → `Map` lookup → wrapper.flyTo(idx) |

## Recommended Project Structure

```
src/
├── transport/          # the swappable seam
│   ├── dgraph-http.ts  # v1: paged DQL/GraphQL over fetch()
│   ├── bridge.ts       # v2 placeholder: binary/gzip stream client (Go bridge)
│   └── types.ts        # GraphPage / RawEdge interface both transports satisfy
├── loader/
│   ├── load-graph.ts   # orchestrates paging, progress events, assembles arrays
│   └── remap.ts        # pubkey(string) -> dense uint32 index
├── store/
│   ├── graph-store.ts  # typed-array columns + pk->idx map; the in-memory model
│   └── columns.ts      # buffer allocation/growth helpers
├── engine/
│   ├── cosmos-wrapper.ts  # façade over @cosmos.gl/graph
│   └── camera.ts          # fly-to / fit / zoom helpers
├── overlays/
│   ├── degree.ts       # degree -> size buffer
│   ├── community.ts    # community detect -> color buffer
│   └── filters.ts      # time-range masks -> alpha/visibility buffer
├── search/
│   └── find-pubkey.ts  # npub/hex decode + lookup + highlight
├── ui/                 # controls, panels, status/progress
└── main.ts             # wires loader -> store -> engine -> overlays -> ui
```

### Structure Rationale

- **`transport/` isolated from `loader/`:** the loader speaks an interface (`GraphPage` stream), not HTTP. Adding the Go bridge means adding one file and a config flag — no churn in loader/store/engine. This is the documented escape hatch made concrete.
- **`store/` framework-agnostic:** the store is plain typed arrays so it survives a UI-framework swap and can be moved into a Web Worker later without rewrite.
- **`engine/` is a façade, not the engine:** all cosmos.gl-specific calls live behind one wrapper, so a future engine swap (e.g. a WebGPU successor) touches one module.
- **`overlays/` write buffers, never topology:** enforces the "recolor without re-upload" rule structurally.

## Architectural Patterns

### Pattern 1: Integer-index node remapping (dense ids)

**What:** As nodes stream in, assign each pubkey a sequential `uint32` index the first time it is seen. Store the `pubkey → index` map; emit edges as `(srcIndex, dstIndex)` pairs, not pubkey strings.
**When to use:** Always, at this scale. cosmos.gl `setLinks` consumes integer point indices, and dense ids let you address every per-node attribute as `array[index]`.
**Trade-offs:** One `Map<string,number>` of N pubkeys in memory (~tens of MB at 5M), in exchange for ~30× smaller edge payload (4-byte int vs ~64-char hex pubkey) and O(1) attribute lookup. Edges referencing a pubkey not yet seen require either a two-pass load (nodes first, then edges) or lazy index allocation — prefer **nodes-first, then edges** so every edge endpoint is guaranteed to resolve.

**Example:**
```typescript
const pkToIdx = new Map<string, number>();
function idx(pk: string): number {
  let i = pkToIdx.get(pk);
  if (i === undefined) { i = pkToIdx.size; pkToIdx.set(pk, i); }
  return i;
}
// edges become a flat Uint32Array: [src0,dst0, src1,dst1, ...]
```

### Pattern 2: Cursor (UID `after`) paging for bulk export

**What:** Walk the whole graph in fixed-size pages using DQL `first:N, after:<lastUID>` rather than `offset`. `after` is O(1) per page; `offset` is O(N) and collapses past a few hundred thousand rows.
**When to use:** The bulk-export load. Page nodes first (cheap projection: `uid, pubkey, kind3CreatedAt, last_db_update`), then page the `follows` edges. Keep page size moderate (e.g. 50k–200k entities) to bound per-response JSON memory and allow incremental progress + buffer growth.
**Trade-offs:** More round-trips than one giant query, but bounded memory and a progress bar; avoids a single multi-GB JSON response that would OOM the tab. JSON-over-HTTP remains the wire bottleneck — this pattern makes it *survivable*, the Go bridge later makes it *fast*.

**Example:**
```graphql
# page of nodes; repeat with after:<lastUidOfPage> until empty
{ q(func: type(Profile), first: 100000, after: 0x0) {
    uid pubkey kind3CreatedAt last_db_update
} }
```
For edges, project the `follows` predicate (UID-only) per page; never request reverse `followers` (redundant — it is the inverse of `follows`).

### Pattern 3: Topology-static / style-dynamic buffers

**What:** Positions and links are uploaded once. Overlays (degree size, community color, time filters) only rewrite the *style* typed arrays — `setPointColors`, `setPointSizes`, link width/color — then call `render()`. cosmos.gl maps each typed array straight to a WebGL texture, so a recolor is a single texture update, not a topology re-upload.
**When to use:** Every overlay and filter interaction. This is what keeps 60fps while toggling colorings on millions of nodes.
**Trade-offs:** Requires keeping derived attributes as parallel index-aligned arrays in the store. Filtering by hiding (alpha → 0) keeps indices stable and cheap; physically removing nodes would invalidate the index space and force a re-upload — avoid it.

```typescript
// recolor by community without touching positions/links
engine.setPointColors(communityColorBuffer); // Float32Array rgba, len = 4*N
engine.render();
```

## Data Flow

### Load Flow (session start + refresh)

```
[Refresh / session start]
   ↓
transport.streamNodes() ──pages──▶ loader: assign dense idx, fill node columns
   ↓
transport.streamEdges() ──pages──▶ loader: remap (src,dst) → idx pairs → edge array
   ↓
graph-store populated (typed-array columns + pk→idx map)
   ↓
engine.setPointPositions(initial) ; engine.setLinks(edges) ; engine.start()  // GPU force sim
   ↓
overlays compute degree/community → style buffers → engine.setPointColors/Sizes → render()
```

### Interaction Flow (steady state, 60fps)

```
[pan/zoom]        → engine camera (GPU)            → render          (no store touch)
[hover/pick]      → engine pick event → node idx   → store lookup    → UI tooltip
[overlay toggle]  → overlays recompute style buffer → engine.setPointColors/Sizes → render
[time filter]     → filters → alpha/visibility buffer → engine.render
[search pubkey]   → decode npub→hex → map→idx → engine.flyTo(idx) + highlight buffer
```

### State Management

```
graph-store (typed arrays + pk→idx map)   ← single source of truth
   ↑ read                    ↑ read
overlays/analytics        search/UI
   │ write style buffers      │ camera/highlight
   ▼                          ▼
engine wrapper (owns GPU buffers; topology immutable per load)
```

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| ~500k nodes / few M edges | v1 as described works: paged DQL JSON, main-thread load, cosmos.gl live layout. Load is a few seconds. |
| ~1–2M nodes / ~10M edges | JSON parse + remap on the main thread starts to jank the UI. Move loader + remap into a **Web Worker**, transfer buffers to main thread via `postMessage` transferables. Still v1 transport. |
| ~5M+ nodes / tens of M edges | JSON-over-HTTP transfer time dominates load. Introduce the **Go bridge** (binary edge stream + gzip) behind the transport seam; the int-index arrays arrive nearly ready-to-upload. GPU render itself remains the engine's job and is already proven at 1M+. |

### Scaling Priorities

1. **First bottleneck — JSON transfer/parse on load.** Mitigate in order: smaller projection (UID-only edges) → Web Worker parse → Go binary bridge. The transport seam means each step is additive, not a rewrite.
2. **Second bottleneck — GPU memory / layout convergence at 5M nodes.** Tune cosmos.gl simulation params (decay, repulsion, link strength), cap iterations, and allow pausing the sim once settled so interaction frees the GPU.

## Anti-Patterns

### Anti-Pattern 1: Keeping pubkey strings as the node key in hot paths
**What people do:** Index per-node attributes and links by hex pubkey string.
**Why it's wrong:** 64-char keys bloat the edge payload ~30×, defeat typed-array columns, and force string hashing in the render loop.
**Do this instead:** Remap to dense `uint32` indices at load; keep one `pubkey→idx` map for search/tooltips only.

### Anti-Pattern 2: Re-uploading positions/links to recolor or filter
**What people do:** Call the engine's full `setData`/topology setters whenever an overlay changes.
**Why it's wrong:** Re-uploading millions of links per interaction destroys 60fps.
**Do this instead:** Mutate only the style typed arrays (`setPointColors`/`setPointSizes`) and `render()`; filter by alpha, not by removing nodes.

### Anti-Pattern 3: `offset`-based paging or one giant query for bulk export
**What people do:** `first:N, offset:K` walking, or a single unpaged query for the whole graph.
**Why it's wrong:** `offset` is O(N) per page and degrades to a crawl; one giant JSON response OOMs the tab.
**Do this instead:** Cursor paging with `after:<lastUID>` (O(1)), bounded page size, incremental buffer growth.

### Anti-Pattern 4: Building the Go bridge into the core before it's needed
**What people do:** Stand up the binary service in v1 "to be safe."
**Why it's wrong:** Adds a new service to a tool whose whole premise is browser-direct simplicity; premature.
**Do this instead:** Ship the transport seam (interface + HTTP impl). Add `bridge.ts` only when measured load time on real data demands it.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| Dgraph `:8080` `/query` (DQL) | POST DQL, paged with `first`/`after`; UID-only edge projection | Preferred for bulk export — most control over shape and cursoring |
| Dgraph `:8080` `/graphql` | GraphQL queries against the `Profile` type | Viable but less compact; DQL gives tighter projections |
| Dgraph `:9080` gRPC | Reserved for the future Go bridge (Go dgo client) | Browser can't use gRPC directly; this is the bridge's data path |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| transport ↔ loader | async iterator of `GraphPage` | The seam: HTTP today, binary bridge later, loader unchanged |
| loader ↔ store | hands over typed-array columns + pk→idx map | One-shot per load/refresh |
| store ↔ overlays | direct read of columns; overlays write style buffers | Topology read-only to overlays |
| engine wrapper ↔ everything | wrapper API only (positions/links/colors/sizes/camera/pick) | cosmos.gl never imported outside the wrapper |

## Suggested Build Order (for a coarse 3–5 phase roadmap)

Dependencies run left→right; each phase is independently demonstrable.

1. **Phase A — Transport + bulk load + remap (the data spine).**
   Build the transport seam (HTTP/DQL impl), paged `after`-cursor export, and the pubkey→index remap producing typed-array node/edge columns in the store. *Demo:* counts and arrays in console/devtools. Depends on: nothing. Unblocks everything.
2. **Phase B — GPU render + live layout (the core value).**
   Engine wrapper over cosmos.gl: feed the store's positions/links, run GPU force sim, wire pan/zoom/hover/pick. *Demo:* the whole graph moving at 60fps. Depends on: A.
3. **Phase C — Overlays + analytics (degree, community, color/size).**
   Compute degree and community, write style buffers, recolor/resize without re-upload. *Demo:* hubs sized, regions colored. Depends on: B (engine wrapper exposes style setters).
4. **Phase D — Search + time filters + refresh.**
   npub/hex search → fly-to/highlight; `kind3CreatedAt`/`last_db_update` range filters via alpha buffers; refresh re-runs the load. *Demo:* find a pubkey, slice by time. Depends on: A (map, columns) + B (camera) + C (buffer pattern).
5. **Phase E (optional / deferred) — Go binary-streaming bridge.**
   Only if measured load time at real scale demands it. Implement `bridge.ts` against the existing transport interface; Go service streams gzip'd binary edge ints over the wire. *Demo:* faster cold load on the full graph. Depends on: A's seam being clean — no changes to B/C/D.

**Ordering rationale:** the data spine (A) must exist before anything can render; render (B) is the product's core value and should land second so it can be validated early against real data; analytics (C) and the explorer affordances (D) layer on top of a working render without touching topology; the bridge (E) is a transport-only optimization gated behind a measurement, deliberately last and removable-without-rework.

## Sources

- [Dgraph DQL Pagination — official docs](https://docs.dgraph.io/dql/query/pagination) — `after` (UID) O(1) vs `offset` O(N). (MEDIUM, corroborated)
- [Dgraph Pagination](https://dgraph.io/docs/query-language/pagination/) and [issue #5807](https://github.com/dgraph-io/dgraph/issues/5807) — offset scalability limits. (MEDIUM)
- [cosmos.gl / cosmosgl/graph (GitHub)](https://github.com/cosmosgl/graph) and [v2 migration notes](https://github.com/cosmosgl/graph) — `setPointPositions`/`setLinks`/`setPointColors` Float32Array API, index-based links, GPU-only compute. (MEDIUM)
- [@cosmos.gl/graph (npm)](https://www.npmjs.com/package/@cosmos.gl/graph) — current package, OpenJS Foundation. (MEDIUM)
- [Introducing cosmos.gl — OpenJS Foundation](https://openjsf.org/blog/introducing-cosmos-gl) — 1M+ nodes/links real-time claim. (MEDIUM)
- [The Best Libraries to Render Large Force-Directed Graphs on the Web](https://weber-stephen.medium.com/the-best-libraries-and-methods-to-render-large-network-graphs-on-the-web-d122ece2f4dc) and [Cylynx comparison](https://www.cylynx.io/blog/a-comparison-of-javascript-graph-network-visualisation-libraries/) — Sigma.js ~50k-edge layout ceiling, canvas ~5k limit, cosmos.gl/KeyLines at the top. (LOW–MEDIUM)

---
*Architecture research for: browser GPU explorer of a Dgraph-backed directed follow-graph*
*Researched: 2026-06-22*

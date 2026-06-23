# Roadmap: WoT Graph Explorer

## Overview

This roadmap delivers a local, read-only GPU graph explorer that bulk-loads DeepFry's
entire Web-of-Trust follow-graph into the browser and renders it as one interactive global
map at 60fps. The journey is three vertical MVP slices, each end-to-end usable on its own:
**Phase 1** confronts the two dominant risks (the JSON wire and the GPU layout ceiling) by
putting a real, full-scale graph on screen — bulk-loaded, dense-remapped, GPU-laid-out, and
pannable at 60fps against synthetic ~5M-node / ~30M-edge data. **Phase 2** layers the terrain
encodings (degree sizing/coloring, community coloring) on top of the working render via a
one-shot Web Worker, touching only style buffers. **Phase 3** adds the explorer affordances —
hex/npub search with fly-to, neighborhood highlight, node detail, time-range filtering, and
refresh — turning a map you can look at into a map you can interrogate. Risk is front-loaded:
Phase 1 ends with an explicit feasibility checkpoint that decides whether the deferred Go
binary-streaming bridge (PERF-01, v2) must be pulled forward.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Interactive Graph On Screen (Data Spine + GPU Render)** - Bulk-load, dense-remap, GPU-lay-out, and pan/zoom/hover the full graph at 60fps, validated against synthetic full-scale data (completed 2026-06-23)
- [ ] **Phase 2: Terrain Overlays (Degree + Community)** - Size/color nodes by in/out degree and color by detected community via a one-shot Worker, mutating only style buffers
- [ ] **Phase 3: Explore & Slice (Search, Detail, Time Filter, Refresh)** - Search hex/npub with fly-to + neighborhood highlight, inspect node detail, slice by activity time, and refresh from Dgraph

## Phase Details

### Phase 1: Interactive Graph On Screen (Data Spine + GPU Render)

**Goal**: A developer opens the app, the whole follow-graph bulk-loads from Dgraph into memory with a visible progress state, settles into spatial structure via live GPU force layout, and can be panned/zoomed/hovered as one global map at 60fps — proven against synthetic ~5M-node / ~30M-edge power-law data, not just the tiny dev DB.
**Mode:** mvp
**Depends on**: Nothing (first phase)
**Requirements**: DATA-01, DATA-03, REND-01, REND-02, REND-03, REND-04
**Success Criteria** (what must be TRUE):

  1. Opening the app bulk-loads the entire graph once from Dgraph with a visible loading/progress indicator, querying only `follows` (followers/in-degree derived client-side) via `after`-cursor paging
  2. The whole graph renders as a single global GPU node-link map and settles via a live force-directed layout the user can run, pause, and freeze
  3. Pan, zoom, and hover hold 60fps on a synthetic ~5M-node / ~30M-edge power-law graph, with hex pubkeys remapped to dense uint32 indices in structure-of-arrays typed buffers (no per-node heap objects)
  4. A single fit-to-screen / reset action returns the view to the whole map, and the app loads target-scale data without exhausting browser memory
  5. The data source sits behind a swappable transport interface, and the phase ends with a recorded feasibility verdict on browser-direct JSON load time (pass, or trigger the deferred Go bridge PERF-01)

**Plans**: 3/3 plans complete
Plans:
**Wave 1**

- [x] 01-01-PLAN.md — Walking Skeleton: scaffold Vite+TS, GraphTransport interface + SoA types, small synthetic render + pan/zoom, vitest CPU-pipeline scaffold

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 01-02-PLAN.md — GPU ceiling spike: 5M/30M BA generator, auto-freeze, Run/Pause + Fit + hover, recorded 60fps verdict

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 01-03-PLAN.md — JSON wire + verdict: DgraphTransport after-cursor paging, chunked parse + remap, staged loader + verdict instrument, recorded feasibility verdict

**Feasibility verdict (phase-closing):** GPU half PASS (01-02, 5M/30M ~60fps); JSON-wire half **FAIL → PERF-01 triggered**. Browser-direct JSON load against the real dev Dgraph (365k follow-nodes / 1.5M profiles / ~tens of millions of edges — D-04 small-DB assumption falsified) made the dev machine unusable (swap). **The deferred Go binary-streaming bridge (PERF-01) is pulled forward from v2 to the next phase** as a drop-in `GoBridgeTransport` behind the existing `GraphTransport` interface; cosmos.gl + SoA pipeline unchanged.

**UI hint**: yes

### Phase 01.1: Go Binary-Streaming Bridge (PERF-01) (INSERTED)

**Goal:** [Urgent work - to be planned]
**Requirements**: TBD
**Depends on:** Phase 1
**Plans:** 0 plans

Plans:

- [ ] TBD (run /gsd-plan-phase 01.1 to break down)

### Phase 2: Terrain Overlays (Degree + Community)

**Goal**: On the working render, a developer can see where the hubs and regions are — nodes sized and colored by degree (in-degree/followers distinct from out-degree/follows) and colored by detected community — all computed once off the main thread and applied without re-running layout.
**Mode:** mvp
**Depends on**: Phase 1
**Requirements**: OVER-01, OVER-02
**Success Criteria** (what must be TRUE):

  1. Nodes are sized and colored by degree so hubs and influencers visibly stand out, with in-degree (followers) and out-degree (follows) tracked and encodable distinctly
  2. Nodes are colored by detected community (Louvain) so the graph's regions/clusters are visually distinct
  3. Degree and community are computed one-shot in a Web Worker so the analytics pass never blocks or stalls the 60fps render loop
  4. Switching or recomputing an overlay updates only style buffers (color/size typed arrays) and never re-runs the force layout or mutates topology

**Plans**: TBD
**UI hint**: yes

### Phase 3: Explore & Slice (Search, Detail, Time Filter, Refresh)

**Goal**: A developer can interrogate the map — find a specific pubkey and fly to it, see its neighborhood in context, read its graph-local details, slice the graph by activity/freshness over time, and pull a fresh copy from Dgraph — all without ever breaking the laid-out terrain or the 60fps interaction.
**Mode:** mvp
**Depends on**: Phase 2
**Requirements**: EXPL-01, EXPL-02, EXPL-03, FILT-01, DATA-02
**Success Criteria** (what must be TRUE):

  1. A developer searches a pubkey by hex or npub (NIP-19 decoded to the 32-byte hex `@id`) and the view flies to and highlights that node
  2. Selecting a node highlights its 1-hop neighborhood (follows and followers) and dims the rest of the graph
  3. Hovering or clicking a node shows its details — npub-formatted pubkey, in/out degree, community, and activity timestamps
  4. A time-range control filters by `kind3CreatedAt` and `last_db_update` applied as hide/dim (via alpha/visibility buffers), never re-running the layout
  5. An explicit Refresh re-pulls the current graph from Dgraph and recomputes degree/community, rebuilding the index space cleanly without leaking old buffers

**Plans**: TBD
**UI hint**: yes

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Interactive Graph On Screen | 3/3 | Complete    | 2026-06-23 |
| 2. Terrain Overlays | 0/TBD | Not started | - |
| 3. Explore & Slice | 0/TBD | Not started | - |

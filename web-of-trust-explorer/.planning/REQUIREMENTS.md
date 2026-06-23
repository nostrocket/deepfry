# Requirements: WoT Graph Explorer

**Defined:** 2026-06-22
**Core Value:** Smooth 60fps interaction with the whole follow-graph at once, so a developer can see its terrain — hubs, clusters, bridges, dense vs sparse regions.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Data

- [x] **DATA-01**: User opens the app and the entire follow-graph bulk-loads from Dgraph into the browser once, with a visible loading/progress state _(complete in 01-03: DgraphTransport read-only after-cursor DQL paging over `has(follows)`+`follows{uid}` via POST /query `application/dql`, chunked Worker parse + hex→uint32 remap, staged loader (Fetching→Parsing→Building) with live edge count; the bulk-load capability exists and ran against the real dev Dgraph. NOTE: at real scale browser-direct JSON load is too heavy to be usable — the load path is correct, the wire transport is the limiter → addressed by PERF-01)_
- [ ] **DATA-02**: User can refresh to re-pull the current graph state from Dgraph on demand (explicit, not automatic)
- [x] **DATA-03**: The graph loads and renders at target scale (hundreds of thousands to millions of nodes, tens of millions of edges) without exhausting browser memory _(foundation laid in 01-01: SoA typed buffers + hex→uint32 dense remap + Float32 2^24 precision guard; 01-02: GPU half PASSED — 5M/30M renders without exhausting memory. **01-03 verdict: browser-direct JSON wire is NOT viable at real scale.** Chunked-parse + dense remap + drop-page-string memory discipline + Transferable were all implemented, but the single-shot memory-doubling JSON.parse of the real dev DB (365k follow-nodes / 1.5M profiles / ~tens of millions of edges) drove the machine into swap and was unusable. This is a recorded FAIL-by-design-trigger of the JSON wire, NOT a missing implementation → the at-scale memory/load requirement is **carried by PERF-01** (Go binary-streaming bridge, pulled forward from v2; drop-in GoBridgeTransport behind GraphTransport, zero browser JSON.parse))_

### Render

- [x] **REND-01**: User sees the whole graph as a single global node-link map rendered on the GPU _(partial in 01-01: proven on a small synthetic graph via @cosmos.gl/graph; single-map render at 5M-node scale is Plan 02)_
- [x] **REND-02**: Nodes settle into spatial structure via a live GPU force-directed layout; user can run, pause, and freeze the layout _(complete in 01-02: layout auto-starts, Run/Pause toggle, auto-freeze on settle — confirmed at 5M/30M scale)_
- [x] **REND-03**: User can pan, zoom, and hover across the full graph at 60fps _(complete in 01-02: pan/zoom/hover held ~60fps at 5M/30M on the M3 Pro — recorded verdict PASS; supersedes the 01-01 small-scale Partial)_
- [x] **REND-04**: User can fit-to-screen / reset the view to the whole map in one action _(complete in 01-02: Fit/Reset button → fitView returns to whole map)_

### Overlay

- [ ] **OVER-01**: User sees nodes sized and colored by degree (distinguishing in-degree/followers from out-degree/follows) so hubs and influencers stand out
- [ ] **OVER-02**: User sees nodes colored by detected community so the graph's regions/clusters are visually distinct

### Explore

- [ ] **EXPL-01**: User can search for a pubkey by hex or npub (NIP-19) and the view flies to and highlights that node
- [ ] **EXPL-02**: When a node is selected, its 1-hop neighborhood (follows and followers) is highlighted and the rest of the graph is dimmed
- [ ] **EXPL-03**: User can hover or click a node to see its details — npub-formatted pubkey, in/out degree, community, and activity timestamps

### Filter

- [ ] **FILT-01**: User can filter/slice the graph by `kind3CreatedAt` and `last_db_update` via a time-range control, with filtering applied as hide/dim (never re-layout)

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Analytics

- **ANLY-01**: Connected-components coloring / isolate ("is this one graph or many islands?")
- **ANLY-02**: Degree-distribution histogram panel with brush-to-filter
- **ANLY-03**: k-core / coreness coloring to expose the dense trust-core
- **ANLY-04**: Approximate PageRank / sampled betweenness influence coloring

### Interaction

- **INTR-01**: Box / lasso select → isolate or hide a region's induced subgraph
- **INTR-02**: Directionality cues on edges (color/gradient by direction)
- **INTR-03**: Minimap / overview-while-zoomed navigation aid
- **INTR-04**: Local layout cache (persist last settled positions in IndexedDB to speed reload)

### Performance

- **PERF-01**: Thin Go binary-streaming bridge between browser and Dgraph (escape hatch if v1 JSON load time is unacceptable) — **TRIGGERED & PULLED FORWARD by the 01-03 JSON-wire verdict (FAIL at real scale); now the next-phase priority, drop-in GoBridgeTransport behind GraphTransport (dgo gRPC → server-side hex→uint32 remap → streamed binary edge buffer → zero browser JSON.parse)**
- **PERF-02**: Density / heatmap overlay for dense-vs-sparse terrain when the node-link hairball is unreadable

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Editing / writing graph data | Violates DeepFry data-separation rule; canonical data lives in StrFry/Dgraph and is never mutated here |
| Accounts / auth / multi-user | Single-developer local tool; auth adds backend + security surface for zero value |
| Sharing / saved views / collaboration | Requires hosting + persistence; out of scope for a local tool (screenshot / local cache suffice) |
| Server-side rendering / hosted deployment | Contradicts the GPU-in-browser core; adds infra and hardening |
| Trust-score editing / computation | This tool visualizes terrain shape; scoring is a separate DeepFry subsystem |
| Edge bundling (full graph) | Needs heavy precompute / tile-pyramids; incompatible with live in-browser layout. Use density overlay instead |
| Exhaustive betweenness centrality | O(N·E) — infeasible client-side at millions of nodes; would freeze the tab |
| Live continuous re-querying | Tens of millions of edges per refresh is not viable continuously; explicit Refresh instead |
| Per-node event-content panels | Event payloads do not live in Dgraph (ID-only graph); only graph-local attributes are shown |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| DATA-01 | Phase 1 | Complete (01-03: DgraphTransport read-only after-cursor DQL paging + chunked parse + remap + staged loader; bulk-load capability built and ran. Wire too heavy at real scale → PERF-01) |
| DATA-02 | Phase 3 | Pending |
| DATA-03 | Phase 1 | Carried by PERF-01 (01-01: SoA buffers + remap + precision guard; 01-02: GPU half PASS — 5M/30M renders without exhausting memory; 01-03 verdict: browser-direct JSON wire NOT viable at real scale — FAIL-by-design-trigger, memory discipline implemented but transport is the limiter → Go binary-streaming bridge PERF-01 pulled forward) |
| REND-01 | Phase 1 | Partial (01-01: small synthetic GPU render; 01-02: single global GPU map rendered at 5M-node scale) |
| REND-02 | Phase 1 | Complete (01-02: live GPU layout auto-starts, Run/Pause, auto-freeze on settle) |
| REND-03 | Phase 1 | Complete (01-02: pan/zoom/hover ~60fps at 5M/30M — recorded verdict PASS) |
| REND-04 | Phase 1 | Complete (01-02: Fit/Reset returns to whole map) |
| OVER-01 | Phase 2 | Pending |
| OVER-02 | Phase 2 | Pending |
| EXPL-01 | Phase 3 | Pending |
| EXPL-02 | Phase 3 | Pending |
| EXPL-03 | Phase 3 | Pending |
| FILT-01 | Phase 3 | Pending |

**Coverage:**

- v1 requirements: 13 total
- Mapped to phases: 13 ✓
- Unmapped: 0 ✓

---
*Requirements defined: 2026-06-22*
*Last updated: 2026-06-23 — Plan 01-03 (JSON-wire spike): DATA-01 Complete (DgraphTransport bulk-load built and ran); DATA-03 carried by PERF-01 — JSON-wire verdict FAIL at real scale (365k follow-nodes / 1.5M profiles), Go binary-streaming bridge PERF-01 pulled forward from v2. Phase 1 feasibility checkpoint resolved: GPU half PASS, JSON-wire half FAIL → PERF-01.*

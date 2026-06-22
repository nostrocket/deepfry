# Requirements: WoT Graph Explorer

**Defined:** 2026-06-22
**Core Value:** Smooth 60fps interaction with the whole follow-graph at once, so a developer can see its terrain — hubs, clusters, bridges, dense vs sparse regions.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Data

- [ ] **DATA-01**: User opens the app and the entire follow-graph bulk-loads from Dgraph into the browser once, with a visible loading/progress state
- [ ] **DATA-02**: User can refresh to re-pull the current graph state from Dgraph on demand (explicit, not automatic)
- [ ] **DATA-03**: The graph loads and renders at target scale (hundreds of thousands to millions of nodes, tens of millions of edges) without exhausting browser memory

### Render

- [ ] **REND-01**: User sees the whole graph as a single global node-link map rendered on the GPU
- [ ] **REND-02**: Nodes settle into spatial structure via a live GPU force-directed layout; user can run, pause, and freeze the layout
- [ ] **REND-03**: User can pan, zoom, and hover across the full graph at 60fps
- [ ] **REND-04**: User can fit-to-screen / reset the view to the whole map in one action

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

- **PERF-01**: Thin Go binary-streaming bridge between browser and Dgraph (escape hatch if v1 JSON load time is unacceptable)
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
| DATA-01 | Phase 1 | Pending |
| DATA-02 | Phase 3 | Pending |
| DATA-03 | Phase 1 | Pending |
| REND-01 | Phase 1 | Pending |
| REND-02 | Phase 1 | Pending |
| REND-03 | Phase 1 | Pending |
| REND-04 | Phase 1 | Pending |
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
*Last updated: 2026-06-22 after roadmap creation (traceability mapped)*

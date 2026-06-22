# Feature Research

**Domain:** Browser-based GPU graph-exploration tool ("understand the terrain" of a large directed Web-of-Trust follow-graph)
**Researched:** 2026-06-22
**Confidence:** MEDIUM (feature canon cross-checked against Gephi / sigma.js / Cosmograph; client-side scaling estimates are extrapolations from published JS benchmarks, marked LOW where noted)

## Framing

The whole product is **legibility of terrain**, not analytics. Every feature below is judged by one question: *does it help a developer see and reason about the shape of the follow-graph (hubs, clusters, bridges, dense vs sparse, components) while moving fluidly across it?* Anything that does not serve that goal is a differentiator at best, an anti-feature at worst.

The dominant constraint is scale: **500k–5M+ nodes, tens of millions of directed edges, computed and rendered live in one browser tab at 60fps.** This is the lens for every complexity/feasibility call. Two cost buckets matter:

- **Per-frame / interactive** (must be GPU or O(visible)): pan, zoom, hover, sizing, coloring, highlight. These run 60×/sec and cannot touch the whole graph in JS.
- **One-shot / async** (run once on load or on demand, off the main thread): community detection, degree computation, connected components. These can be O(N+E) in a Web Worker; the UI must not block.

The visualization engine (Cosmograph `@cosmograph/cosmos`, already chosen in PROJECT.md) supplies the GPU layout/render and most interaction primitives. It does **not** supply community detection, edge bundling, or rich analytics — those are ours to add or omit.

## Feature Landscape

### Table Stakes (The Map Is Useless Without These)

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Pan / zoom / hover at 60fps on the full graph | This *is* the core value; terrain is only legible when you can move across it fluidly | MEDIUM | Engine-native (GPU). The rest of the app must not steal main-thread time and stall this. [PROJECT core value] |
| Bulk-load whole graph once, operate in-memory | Continuous re-querying at this scale is infeasible; one load + manual refresh | MEDIUM | Load-time is the risk (millions of edges over JSON wire). Show a loading state. Direct browser→Dgraph per PROJECT.md. |
| Live GPU force-directed layout | Without spatial structure there is no "terrain" — nodes must settle into clusters/hubs spatially | MEDIUM | Engine-native (cosmos). Expose run/pause and basic force params so the user can let it settle then freeze. |
| Degree-based node sizing & coloring | The single most direct hub-revealing encoding; "where are the influencers" is the first terrain question | LOW | Degree computed once in a Worker (O(N+E)), fed to engine as point size/color-by-value. In/out degree distinct for a directed follow-graph. |
| Community detection + community coloring | Reveals the graph's *regions* — the second core terrain question. Color-by-community is what makes clusters pop visually | MEDIUM-HIGH | graphology Louvain, run once in a Worker. At 1M–5M nodes expect multi-second to tens-of-seconds; treat as async one-shot, not interactive. [LOW confidence on exact runtime at 5M] |
| Search / locate a pubkey + fly-to + highlight | A directed lookup ("where is *this* key and who's around it") is a baseline expectation | MEDIUM | Must accept hex **and** npub (NIP-19 decode → 32-byte hex, the Dgraph `@id`). Fly-to + highlight node + 1-hop neighborhood. |
| Hover tooltip / click-to-focus | Identify what a node is and pull its details without leaving the map | LOW | Engine hover/click events; show pubkey (npub-formatted), in/out degree, community id, activity timestamps. |
| Fit-to-screen / reset view | After flying somewhere you must be able to get back to the whole map in one action | LOW | Engine-native `fitView`. |
| Neighborhood highlight on selection | Seeing a node's follows/followers in context is the atomic exploration act | MEDIUM | Highlight 1-hop (directed: separate follows vs followers visually) and dim the rest. |
| Activity / freshness filtering (time slider) | The user explicitly wants to slice by `kind3CreatedAt` / `last_db_update` to see fresh vs stale regions | MEDIUM | Range slider over the two int timestamps. Filtering = hide/dim, not re-layout (re-layout per tick would kill fps). |
| Refresh (re-pull from Dgraph) | A dev tool against changing data needs a "get the current state" action | LOW | Re-run the bulk load + recompute degree/community. Explicit button, not automatic. |

### Differentiators (Make Terrain Dramatically More Legible)

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Directionality cues on edges | It's a *directed* follow-graph; showing who-follows-whom (arrows / gradient / asymmetric color) distinguishes hubs (followed) from sprayers (following) | MEDIUM | cosmos draws straight-line edges; per-edge arrowheads at this scale are expensive. Cheaper: color/gradient edges by direction, or encode in/out degree on nodes instead of per-edge arrows. |
| Connected-components view | Instantly answers "is the graph one blob or many islands?" — color/isolate by component | MEDIUM | O(N+E) union-find in a Worker, one-shot. Cheap and high-signal for terrain. Pairs with community coloring as a toggle. |
| Degree-distribution / histogram panel | Quantifies the hub structure (power-law tail) and drives filter thresholds; doubles as a filter control | LOW-MEDIUM | Computed from the degree pass already done. Cosmograph-style linked histograms (brush a range → filter the map) are very high value for terrain. |
| Box / lasso select → isolate / hide | "Grab that cluster and look at just it" is the power-user terrain move | MEDIUM | Engine selection + a hide/isolate mode. Isolate = filter to selection's induced subgraph (no re-layout needed). |
| Minimap / overview-while-zoomed | At 5M nodes it's easy to get lost; a minimap shows where you are in the global map | MEDIUM | Not always engine-native; may need a second low-detail render or a static density thumbnail. |
| Density / heatmap overlay | Reveals dense vs sparse regions without resolving individual nodes — pure terrain signal | MEDIUM-HIGH | Smoothed 2D histogram of node/edge positions (cheaper than edge bundling). Good fallback when individual edges are an unreadable hairball. |
| k-core / coreness coloring | Peels the periphery to expose the dense trust-core — very on-theme for a Web-of-Trust | MEDIUM | O(N+E) in a Worker, one-shot, cheaper than centrality. Strong terrain signal; good v1.x add. |
| Approximate PageRank / influence coloring | "Who is structurally important" beyond raw degree | MEDIUM | Power-iteration is O(E) per iter; tractable as an async one-shot at this scale. A differentiator, not table stakes. |
| Adjustable layout/force params live | Let the user tune clustering tightness to make structure readable | LOW | Engine exposes simulation params; expose a small control panel. |
| Save/restore layout positions locally | Re-running force layout every session is slow at 5M nodes; caching settled positions (localStorage/IndexedDB) speeds reload | MEDIUM | Tension with PROJECT's "no precomputed snapshot" decision — frame as a *local cache of the last live layout*, not an offline pipeline. Revisit only if layout time hurts. |

### Anti-Features (Explicitly Out of Scope for a Local Read-Only Explorer)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Editing / writing graph data | "Fix this follow / annotate this node" | Violates DeepFry's data-separation rule; canonical data lives in StrFry/Dgraph and must never be mutated here | Read-only. Surface the pubkey so the user can act elsewhere. |
| Accounts / auth / multi-user | "Log in, see who else is exploring" | It's a single-developer local tool; auth adds a backend, sessions, security surface for zero value | None — runs against local dev Dgraph. |
| Sharing / saved views / collaboration | "Send my colleague this view" | Requires hosting + persistence + a product surface; out of scope per PROJECT.md | Screenshot. Local layout cache (a differentiator) covers personal re-use. |
| Server-side rendering / hosted deployment | "Make it a web app" | Contradicts the GPU-in-browser core; adds infra, rate-limiting, security | Local tool only; browser does all rendering. |
| Trust-score editing / computation | "Compute and tune trust scores here" | This tool is about *terrain shape*, not scoring; scoring is a separate DeepFry concern | Leave scoring to the trust subsystem; here, only *visualize* structure. |
| Edge bundling (full graph) | "Reduce the hairball" | Quality bundling needs heavy precompute / tile-pyramids / multi-server backends (GraphMaps, Cornac); incompatible with live in-browser layout | Density/heatmap overlay (cheaper) + degree/community coloring to reduce visual load. |
| Exhaustive betweenness centrality | "Find the true bridges" | O(N·E) — infeasible client-side at millions of nodes; would freeze the tab | Approximate via k-core / community-bridge detection, or sampled betweenness, as a differentiator. |
| Live continuous re-querying | "Always show latest data" | Tens of millions of edges per refresh is not viable continuously | Explicit Refresh button (table stakes). |
| Per-node detail panels with event content | "Show this user's posts" | Event payloads do not live in Dgraph (ID-only graph); fetching from StrFry per node breaks the in-memory model | Show only graph-local attributes (degree, community, timestamps, pubkey). |

## Feature Dependencies

```
Bulk-load whole graph (in-memory)
    ├──requires──> Direct browser→Dgraph query path
    └──enables───> everything below

GPU force layout
    └──requires──> Bulk-load
                       └──enables──> Pan/Zoom/Hover (60fps)   [core value]

Degree pass (Worker, O(N+E))
    ├──enables──> Degree sizing & coloring        [table stakes]
    ├──enables──> Degree-distribution histogram    [differentiator]
    └──enables──> Histogram-brush filtering

Community detection (Louvain, Worker, async)
    └──enables──> Community coloring               [table stakes]

Connected components (union-find, Worker)
    └──enables──> Component coloring / isolate     [differentiator]

Search (hex/npub) ──requires──> NIP-19 decode ──> fly-to + neighborhood highlight
                                                        └──requires──> selection/highlight primitive

Box/lasso select ──requires──> selection primitive
    └──enables──> Isolate/hide                      [differentiator]

Activity time slider ──requires──> kind3CreatedAt/last_db_update on nodes
    └──implemented-as──> hide/dim filter (NOT re-layout)

k-core / PageRank ──share──> the Worker analytics pass (run once, conflict-free with community/components)
```

### Dependency Notes

- **Everything depends on bulk-load + in-memory model.** This is the first phase; it also carries the biggest risk (JSON wire size). The "thin Go bridge streaming binary" escape hatch in PROJECT.md is the mitigation if load time is unacceptable.
- **One shared Worker analytics pass** can compute degree, connected components, k-core, and Louvain in sequence off the main thread on load. Sequencing them avoids contention and keeps the UI responsive. PageRank can join the same pass.
- **Filtering must never trigger re-layout.** Time-slider, degree-brush, and isolate all work by hide/dim on already-laid-out nodes. Re-running force layout per filter change would destroy the 60fps core value. This is a hard architectural rule, not a preference.
- **Search depends on NIP-19 decoding.** npub→hex is a small, well-defined library function; the Dgraph `@id` is the 32-byte hex pubkey, so all internal lookups key on hex.
- **Directionality cues conflict with raw edge rendering at scale.** Per-edge arrowheads on tens of millions of straight-line edges are too expensive; encode direction on nodes (in/out degree) or via edge color/gradient instead.

## MVP Definition

### Launch With (v1) — already the PROJECT.md Active set

- [ ] Bulk-load whole graph from Dgraph into memory — foundation for everything
- [ ] GPU render of full graph as one global map — the product
- [ ] Pan / zoom / hover at 60fps — the core value
- [ ] Live GPU force layout — produces the terrain to look at
- [ ] Degree-based node sizing & coloring — primary hub-revealing encoding
- [ ] Community detection + community coloring — primary region-revealing encoding
- [ ] Search (hex/npub) → fly-to + neighborhood highlight — directed lookup
- [ ] Activity/freshness time filtering — fresh-vs-stale terrain slicing
- [ ] Refresh from Dgraph — current-state action
- [ ] Hover tooltip / click-to-focus + fit-to-screen — baseline interaction completeness (implied; cheap, do not omit)

### Add After Validation (v1.x)

- [ ] Connected-components coloring/isolate — trigger: user asks "is this one graph or many?"
- [ ] Degree-distribution histogram + brush-to-filter — trigger: thresholding by hand gets tedious
- [ ] Box/lasso select → isolate cluster — trigger: user wants to study one region in isolation
- [ ] k-core / coreness coloring — trigger: degree+community isn't enough to find the trust-core
- [ ] Directionality cues — trigger: follows-vs-followers distinction matters for interpretation
- [ ] Local layout cache — trigger: per-session layout time becomes painful at full scale

### Future Consideration (v2+)

- [ ] Density/heatmap overlay — defer until the hairball is proven unreadable at full zoom-out
- [ ] Approximate PageRank / sampled betweenness — defer; degree+k-core may answer "importance" well enough
- [ ] Minimap — defer; nice-to-have navigation aid, not terrain-critical
- [ ] Thin Go streaming bridge — defer unless v1 JSON load time is unacceptable (escape hatch, not a feature)

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Pan/zoom/hover 60fps | HIGH | MEDIUM | P1 |
| Bulk-load in-memory | HIGH | MEDIUM | P1 |
| Live GPU force layout | HIGH | MEDIUM | P1 |
| Degree sizing & coloring | HIGH | LOW | P1 |
| Community detection + coloring | HIGH | MEDIUM-HIGH | P1 |
| Search hex/npub + fly-to + highlight | HIGH | MEDIUM | P1 |
| Activity/time filtering | HIGH | MEDIUM | P1 |
| Hover/click-to-focus + fit-to-screen | MEDIUM | LOW | P1 |
| Refresh | MEDIUM | LOW | P1 |
| Connected components | MEDIUM | MEDIUM | P2 |
| Degree histogram + brush filter | HIGH | LOW-MEDIUM | P2 |
| Box/lasso isolate | MEDIUM | MEDIUM | P2 |
| k-core coloring | MEDIUM | MEDIUM | P2 |
| Directionality cues | MEDIUM | MEDIUM | P2 |
| Local layout cache | MEDIUM | MEDIUM | P2/P3 |
| Density/heatmap overlay | MEDIUM | MEDIUM-HIGH | P3 |
| PageRank / sampled betweenness | LOW-MEDIUM | MEDIUM | P3 |
| Minimap | LOW | MEDIUM | P3 |

## Competitor Feature Analysis

| Feature | Gephi / Gephi Lite | sigma.js | Cosmograph / cosmos | Our Approach |
|---------|--------------------|----------|---------------------|--------------|
| Render scale | desktop, ~100k–1M (CPU/OpenGL) | thousands–millions (WebGL) | millions (GPU) | Cosmograph cosmos — millions, live layout |
| Live force layout | yes (ForceAtlas2) | layout external | yes (GPU) | GPU live (cosmos) |
| Degree sizing/coloring | yes (Appearance pane) | manual | point size/color by value | one Worker degree pass → engine encoding |
| Community coloring | yes (modularity class) | external | external | graphology Louvain in Worker |
| Filtering | degree-range slider, filter stack | manual | filtering + linked histograms | hide/dim filters + histogram brush (v1.x) |
| Search/locate | yes | manual | yes | hex/npub NIP-19 + fly-to + highlight |
| Time dimension | dynamic graphs | no | Timeline | time slider over kind3CreatedAt/last_db_update |
| Edge bundling | plugins | no | no (straight edges) | NOT building — density overlay instead |
| Directed cues | arrows (small graphs) | arrows | straight edges | node in/out degree + edge color, not arrowheads |

## Sources

- [Gephi Quickstart / Tutorials](https://gephi.org/quickstart/), [NULab Gephi Tutorial](https://cssh.northeastern.edu/nulab/gephi-tutorial/), [Gephi Lite blog](https://gephi.wordpress.com/2022/11/15/gephi-lite/) — canonical exploration feature set (MEDIUM)
- [sigma.js](https://github.com/jacomyal/sigma.js/), [sigmajs.org](https://www.sigmajs.org/) — WebGL large-graph rendering and interactions (MEDIUM)
- [Cosmograph cosmos docs](https://cosmograph.app/docs/) — GPU layout, filtering+histograms, Timeline, straight-line edges (MEDIUM, partial doc coverage)
- [graphology-communities-louvain](https://www.npmjs.com/package/graphology-communities-louvain), [Graphology docs](https://graphology.github.io/standard-library/communities-louvain.html) — client-side Louvain benchmarks & directed support (MEDIUM); million-node browser timing extrapolated (LOW)
- [3D Density Histograms for Criteria-driven Edge Bundling](https://arxiv.org/pdf/1504.02687), [Browsing Large Graphs with Tile Pyramids](https://arxiv.org/html/2605.17498v1) — edge bundling / density-map cost (MEDIUM)

---
*Feature research for: GPU graph-terrain explorer (DeepFry Web-of-Trust)*
*Researched: 2026-06-22*

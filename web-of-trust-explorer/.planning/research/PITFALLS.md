# Pitfalls Research

**Domain:** Large-scale browser GPU graph visualization (500k–5M+ nodes, tens of millions of directed edges) pulling directly from Dgraph
**Researched:** 2026-06-22
**Confidence:** MEDIUM-HIGH (Cosmos limits, WebGL 32-bit index, Dgraph @hasInverse storage, and offset-pagination behavior verified against primary sources; scale-threshold numbers are engineering estimates)

> This domain has two load-bearing risks that everything else orbits:
> 1. **The wire** — moving tens of millions of edges from Dgraph into the browser over JSON without timing out, OOMing, or taking minutes.
> 2. **The frame** — keeping pan/zoom/hover at 60fps once that data is in memory, while a GPU force layout runs on the same graph.
> Pitfalls below are ordered so the ones threatening these two come first.

## Critical Pitfalls

### Pitfall 1: Shipping 64-char hex pubkeys over the wire and into GPU buffers

**What goes wrong:**
The Dgraph `pubkey` is a 64-character hex string (`@id`). At 5M nodes that's ~320 MB of node IDs alone before any edges. Edges reference endpoints by pubkey, so a naive edge list ships *two* 64-char strings per edge — at 30M edges that is ~3.8 GB of JSON just for edge endpoints. The browser then keeps these strings as JS string objects (2 bytes/char + object overhead, easily 200+ bytes each after interning fails), and you cannot put strings into a `Float32Array`/`Int32Array` GPU buffer at all.

**Why it happens:**
The schema's natural key is the pubkey, GraphQL responses return it verbatim, and it "just works" at 1k nodes during early dev. The cost is invisible until full-scale data.

**How to avoid:**
Establish an **integer index space at load time**: assign each pubkey a sequential `uint32` id (0..N-1) as it streams in, build a `Map<pubkey, uint32>` once, and immediately discard the hex from the hot path. Store positions/degrees/community in typed arrays indexed by that integer. Edges become `Uint32Array` pairs. Keep a single `Uint8Array`/packed buffer of the 32-byte raw pubkeys (parse hex → bytes, 32 bytes not 64 chars) for search/display, addressed by integer id. This cuts node-id memory ~10x and makes edges directly uploadable to WebGL.

**Warning signs:**
Heap snapshot dominated by `string`; edge array is an array of objects with `source`/`target` string fields; you find yourself doing string comparisons in a render or pick loop.

**Phase to address:** Data-load / ingestion phase (the very first one that touches real data).

---

### Pitfall 2: Pulling the whole graph in one Dgraph query (timeout + response-size wall)

**What goes wrong:**
A single GraphQL/DQL query asking for all profiles and all their `follows` returns a multi-GB JSON blob. Dgraph's query executor and the HTTP layer are not built to stream a 30M-edge result: you hit query deadlines, the `json` budget, server memory pressure, or the browser's `fetch().json()` choking on a multi-GB string. Offset pagination makes it worse — `offset: N` is **O(N)** in Dgraph, so page 5000 is far slower than page 1 and eventually times out.

**Why it happens:**
GraphQL's one-query mental model; it works against the docker-compose dev DB which holds a tiny subset; the O(N) offset cost is non-obvious.

**How to avoid:**
- **Paginate with `after` (cursor), never large `offset`** — `after` is O(1) in Dgraph, `offset` is O(N) and will time out at depth.
- Pull **nodes and edges in separate, flat passes**, not as a nested `follows { follows { ... } }` tree (nesting explodes and re-fetches shared neighbors).
- Page in fixed batches (e.g. 50k–100k entities), assembling typed arrays incrementally so peak memory is one batch, not the whole response.
- Treat the "browser → Dgraph direct, no Go bridge" decision as **on probation**: if cursoring tens of millions of edges over JSON can't load in a few seconds, the documented escape hatch is a thin Go bridge that streams a compact binary edge buffer. Plan the load layer behind an interface so swapping the transport is a one-file change.

**Warning signs:**
First real-data load takes minutes or never returns; Dgraph logs show transaction/deadline aborts; `fetch` resolves but `.json()` hangs; load time grows super-linearly with offset.

**Phase to address:** Data-load phase; the bridge-fallback decision should be an explicit checkpoint at the end of it.

---

### Pitfall 3: `@hasInverse` causes you to transfer (and store) every edge twice

**What goes wrong:**
The schema declares `follows: [Profile] @hasInverse(field: followers)` and `followers: [Profile]`. Under the hood Dgraph materializes these as **two separate predicates** (`Profile.follows` and `Profile.followers`), each fully populated — `followers` is the complete reverse of `follows`. If you query both `follows` and `followers` to "be safe," you pull the entire edge set twice (60M edges instead of 30M), double the JSON, double the parse, double the memory — and then risk drawing each relationship as two overlapping arrows.

**Why it happens:**
Both fields exist in the schema and look like independent data; the reverse-is-derived relationship isn't visible in GraphQL.

**How to avoid:**
Pull **only `follows`** for graph construction (it's the canonical directed edge A→B). Derive in-degree/followers entirely client-side from the integer edge list (a second `Uint32`/counter array), never by querying `followers`. Reserve `followers` queries for a targeted single-node "who follows this pubkey" lookup, if ever.

**Warning signs:**
Edge count is ~2x your expected follow count; every node appears to have symmetric arrows; transfer size is double your estimate.

**Phase to address:** Data-load phase; encode "follows is the source of truth, followers is derived" as a load-layer invariant.

---

### Pitfall 4: Expecting a CPU / SVG / Canvas force layout to scale — it won't

**What goes wrong:**
A d3-force / vis.js / Sigma-CPU prototype looks great at 5k nodes, so it gets promoted. At 500k+ it's a slideshow: CPU O(n²) (or even Barnes-Hut O(n log n)) force ticks take seconds each, the layout never converges in interactive time, and a single global frame can't be drawn. This burns a whole phase before the team accepts GPU is mandatory.

**Why it happens:**
Familiarity with CPU graph libs; small dev data hides the wall; "we'll optimize later."

**How to avoid:**
Commit to a **GPU layout-and-render engine** (cosmos.gl / `@cosmograph/cosmos`, or WebGPU equivalent) from the first rendering phase. Do **not** build a CPU-layout MVP "to be replaced later" — the data structures, the pick path, and the layout loop are all different, so it's throwaway work, not a stepping stone. Spike the GPU engine against full-scale (or synthetic full-scale) data before committing the architecture.

**Warning signs:**
Layout tick measured in seconds; frame rate falls as node count rises; anyone proposes d3-force/vis.js/cytoscape for the global view.

**Phase to address:** Rendering-engine selection phase (and a feasibility spike before it).

---

### Pitfall 5: GPU force-layout simulation-space limit at the high end of scale

**What goes wrong:**
cosmos.gl runs the force simulation in GPU textures with a **bounded simulation-space size**. Per its own maintainers, *"if your graph has several million nodes, they may not fit at all, even if you choose the largest available space size."* The 5M-node target sits exactly in this danger zone: layout may silently fail to spread, collapse into a blob, or refuse to allocate.

**Why it happens:**
"Handles millions of nodes" is true at ~1–2M; the upper end of the stated range (5M+) hits the texture/space ceiling that marketing copy glosses over.

**How to avoid:**
- Validate the chosen engine against your **actual maximum node count** in a spike, not the marketing number.
- Have a **graceful degradation** plan: if 5M won't lay out live, fall back to (a) degree/community-based node sampling for the global view (draw all edges only within a focused region), or (b) a one-time precomputed layout snapshot — explicitly the documented "revisit if live layout can't hit 60fps" escape hatch in PROJECT.md.
- Pin the engine **version**; layout-space limits change between releases.

**Warning signs:**
Layout produces a tight ball regardless of repulsion settings; engine throws on buffer/texture allocation; positions NaN out at high N.

**Phase to address:** Layout phase; tie the sampling/precompute fallback to a measured node-count threshold.

---

### Pitfall 6: Heap blowout from millions of JS objects instead of typed arrays (GC kills 60fps)

**What goes wrong:**
Representing nodes/edges as `{id, x, y, degree, community, ...}` objects creates tens of millions of heap objects. This OOMs the tab (Chrome ~2–4 GB/tab) and, worse, triggers multi-hundred-ms **GC pauses** that drop frames — directly killing the one core-value requirement (60fps).

**Why it happens:**
Objects are the idiomatic JS modeling choice and what most graph libs hand you.

**How to avoid:**
**Structure-of-arrays in typed buffers**: `Float32Array` positions (x,y), `Uint32Array` edges, `Uint8`/`Uint16` for community/degree-bucket. Zero per-frame allocation in the render/layout/pick loops — pre-allocate everything, reuse buffers, no array `.map`/`.filter` per frame. Treat any allocation inside the rAF loop as a bug.

**Warning signs:**
DevTools Performance shows sawtooth heap + "GC" bars correlating with frame drops; tab memory climbs toward the limit during load.

**Phase to address:** Data-model phase (shared with Pitfall 1) and render-loop phase.

---

### Pitfall 7: 32-bit / WebGL buffer ceilings on edge counts

**What goes wrong:**
Edges drawn as indexed lines need 32-bit indices. WebGL1's default index type maxes at 65535 (`UNSIGNED_SHORT`); you must enable `OES_element_index_uint` (WebGL1) or use WebGL2 to get `UNSIGNED_INT` indices (up to ~4.29B). Separately, a single buffer / single `drawElements` call has practical limits, and `MAX_TEXTURE_SIZE` caps how many nodes fit in a layout texture. Hit any of these and you get truncated rendering or `INVALID_OPERATION` with no obvious cause.

**Why it happens:**
The 65535 ceiling is a WebGL1 default that silently caps you; texture-size limits are device-dependent.

**How to avoid:**
Use **WebGL2** (32-bit indices native) or explicitly require/enable `OES_element_index_uint` and fail loudly if absent. Let the chosen engine manage buffer chunking, but **verify it actually renders all your edges** at full scale (count drawn primitives) rather than trusting it. Check `MAX_TEXTURE_SIZE` against required layout-texture dimensions at startup.

**Warning signs:**
Only the first ~65k edges render; `gl.getError()` returns `INVALID_OPERATION`/`INVALID_VALUE`; node count above some power-of-two boundary breaks layout.

**Phase to address:** Rendering-engine phase (capability check at init).

---

### Pitfall 8: Hover / pick at scale done on the CPU per frame

**What goes wrong:**
Naive hover tests every node against the cursor each mousemove — O(n) at 5M nodes = unusable, and it runs on the main thread competing with the render. The hairball makes precise picking worse: tens of millions of edges overdraw into a solid mass where nothing is individually pickable.

**Why it happens:**
Picking is an afterthought; small-data picking is "fast enough" naively.

**How to avoid:**
Use **GPU picking** (render node ids to an offscreen color buffer, read back one pixel under the cursor) — the engine usually provides this; confirm it does at scale. Throttle hover to the frame loop, not every mousemove event. For dense regions, gate edge rendering by zoom (don't draw all 30M edges when zoomed out — draw nodes + sampled/aggregated edges, reveal full edges on zoom-in).

**Warning signs:**
Mousemove stutters; CPU spikes on hover; hovering a dense area is meaningless because everything is under the cursor.

**Phase to address:** Interaction phase.

---

### Pitfall 9: Client-side community detection / centrality OOM or stall

**What goes wrong:**
Running Louvain/Leiden community detection or betweenness/PageRank centrality in JS over a 5M-node / 30M-edge graph blocks the main thread for many seconds-to-minutes or OOMs. Betweenness centrality is roughly O(V·E) — utterly infeasible client-side at this scale.

**Why it happens:**
"Color by community" and "size by centrality" are in the requirements; the obvious move is to compute them in-browser after load.

**How to avoid:**
- **Degree** (size/color by follower/follow count) is O(E) and cheap — do it client-side from the edge list. This satisfies the hub/influencer requirement.
- For **community**: run a GPU-friendly / streaming approximation, or run it in a **Web Worker** off the main thread with progressive results, or use the layout engine's built-in clustering if available. Avoid exact betweenness entirely — approximate or skip.
- Consider computing expensive metrics **once in the Go/Dgraph side** and storing them (out of v1 scope, but the natural escalation if client-side proves too slow).

**Warning signs:**
UI freezes after load while "computing communities"; worker never returns; centrality calc OOMs.

**Phase to address:** Analytics/coloring phase; keep degree separate (cheap, in v1) from community/centrality (gated, possibly approximated).

---

### Pitfall 10: Losing the directed nature (follows vs followers) in the visualization

**What goes wrong:**
The graph is **directed** (A follows B ≠ B follows A), but the layout and rendering flatten it to undirected lines. The result looks plausible but is semantically wrong: you can't tell influencers (high in-degree/followers) from broadcasters (high out-degree/follows), and "hubs" become ambiguous.

**Why it happens:**
Most force layouts and GPU line renderers treat edges as symmetric; directionality requires extra encoding (arrowheads, in/out-degree split, asymmetric color).

**How to avoid:**
Keep edges directed in the model (source→target ordering preserved from `follows`). Compute and store **in-degree and out-degree separately** per node. Encode direction in the view — at minimum color/size nodes by *in-degree* (followers = influence) distinctly from out-degree, and where zoom allows, render direction (arrow/gradient/curve). Make "size by degree" explicitly choose which degree.

**Warning signs:**
Node sizing uses a single undifferentiated "degree"; you can't answer "who is most-followed" vs "who follows the most" from the view.

**Phase to address:** Data-model phase (track both degrees) + analytics/coloring phase (expose the choice).

---

### Pitfall 11: Over-scoping into a polished product instead of a fast terrain tool

**What goes wrong:**
Effort drains into legends, theming, animations, tooltips, settings panels, saved views — none of which serve the one core value (60fps terrain legibility). The performance-critical path (load → typed arrays → GPU layout → smooth pan/zoom) gets under-invested while chrome accumulates.

**Why it happens:**
Graph viz is visually seductive; polish feels like progress; the tool's "local dev tool, not a product" framing erodes over time.

**How to avoid:**
Anchor every phase to PROJECT.md's Core Value: *smooth 60fps on the whole graph*. Out-of-scope list already forbids hosting/auth/multi-user/saved-views — hold that line. Defer all cosmetic work until 60fps-at-full-scale is demonstrated. Use the cheapest possible UI shell.

**Warning signs:**
PRs about styling/UX before a full-scale 60fps demo exists; feature requests creeping toward "make it shareable/pretty."

**Phase to address:** Every phase (scope guard); enforce at roadmap-ordering time.

---

### Pitfall 12: Validating against the docker-compose dev DB, which lacks full-scale data

**What goes wrong:**
The local `docker-compose.dgraph.yml` instance holds a small subset (or freshly-crawled partial) graph. Everything — load time, memory, layout, fps — looks fine at 10k nodes, so feasibility-edge risks (Pitfalls 1–7) stay hidden until real data appears, by which point the architecture is baked.

**Why it happens:**
The dev DB is what's on hand; full-scale data is slow to populate; small data is faster to iterate on.

**How to avoid:**
**Generate synthetic full-scale data early** (5M nodes / 30M edges with realistic power-law degree distribution) and benchmark load + layout + fps against it in the first feasibility spike. Also account for **Dgraph cold-cache first-query latency** — the first query after startup is dramatically slower; warm it before timing, and don't mistake cold-cache slowness for a transport problem (or vice versa).

**Warning signs:**
All benchmarks use the dev DB's tiny graph; no synthetic-scale test exists; "it's fast on my machine" with 20k nodes.

**Phase to address:** Initial feasibility spike (before architecture commit) and the data-load phase.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Keep pubkeys as strings throughout | No id-mapping code | 10x memory, can't GPU-upload, string compares in hot loops | Never past prototype |
| Single un-paginated whole-graph query | One simple fetch | Timeouts, OOM at scale, can't ship | Dev DB only; never for real data |
| Query both `follows` and `followers` | "Complete" data | 2x transfer/memory, duplicate edges | Never; derive followers client-side |
| CPU force-layout MVP "to replace later" | Familiar, fast to build | Throwaway; different data model & pick path | Never as a stepping stone — spike GPU directly |
| Object-per-node/edge model | Idiomatic JS | GC pauses kill 60fps, OOM | Never at target scale |
| Test only on docker-compose dev DB | Fast iteration | Hides every feasibility-edge risk | Early UI work only; gated by a synthetic-scale benchmark |
| Exact (string-equality) pubkey search only | Trivial | No npub support, no prefix search | OK for v1 if npub→hex normalization is added |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| Dgraph GraphQL | One nested query for the whole graph | Flat, cursor-paginated (`after`) passes for nodes then edges |
| Dgraph pagination | `offset: N` for deep pages (O(N), times out) | `after` cursor (O(1)) |
| Dgraph `@hasInverse` | Querying `followers` as if independent data | Query only `follows`; followers is the materialized reverse — derive in-degree client-side |
| Dgraph `pubkey @id` | Assuming substring/prefix search works | `@id` is **exact-match only**; normalize npub→hex and match exact, or add a search index for prefix |
| Dgraph startup | Timing the first query (cold cache) as representative | Warm the DB, then benchmark steady-state |
| Browser→Dgraph direct | Treating it as permanent | Behind a transport interface; Go binary-streaming bridge is the documented escape hatch if JSON is too slow |
| WebGL context | Assuming 32-bit indices available | Require WebGL2 or enable `OES_element_index_uint`; fail loudly otherwise |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Hex pubkeys in buffers/JSON | GBs transferred, can't upload to GPU | Integer index space at load | ~100k+ nodes |
| Un-paginated whole-graph query | Timeout / `.json()` hangs | Cursor pagination, batched typed-array assembly | ~tens of MB of result |
| Object-per-element model | GC sawtooth, dropped frames | Structure-of-arrays typed buffers | ~1M+ elements |
| CPU force layout | Layout ticks in seconds | GPU layout engine | ~50k+ nodes |
| GPU layout space ceiling | Blob/NaN positions | Validate at real N; sampling/precompute fallback | ~several million nodes |
| Drawing all edges when zoomed out | Overdraw, fps drop, hairball | Zoom-gated edge rendering / aggregation | ~millions of edges |
| CPU per-mousemove picking | Hover stutter | GPU picking + frame throttle | ~100k+ nodes |
| Client-side betweenness centrality | Multi-minute freeze / OOM | Degree only in v1; approximate or skip centrality | ~100k+ nodes for exact |

## Security Mistakes

Local read-only dev tool, so the surface is small — but a few domain-specific notes:

| Mistake | Risk | Prevention |
|---------|------|------------|
| Treating the direct browser→Dgraph endpoint as safe to expose | Unauthenticated Dgraph (`:8080`) reachable if the tool is ever served beyond localhost | Keep strictly localhost; never bundle the endpoint into a hosted build (already out of scope) |
| Assuming read-only because intent is read-only | A stray mutation query could write to canonical Dgraph (violates DeepFry data rule) | Use query-only operations; never construct mutations; consider a read-only Dgraph user/ACL if available |
| Rendering pubkeys without sanitization in DOM tooltips | Pubkeys are hex (safe), but npub/label fields could carry injection if added later | Treat any displayed string as untrusted; no `innerHTML` with graph data |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Hairball with no legible structure | Terrain unreadable — defeats the entire purpose | Degree-based sizing + community color + zoom-gated edges so structure emerges |
| No loading feedback during multi-second bulk load | Looks frozen/broken | Progressive load indicator with node/edge counts as batches arrive |
| Direction invisible | Can't distinguish influencers from broadcasters | Encode in/out-degree distinctly; show direction on zoom |
| Search returns nothing for npub input | User pastes npub, gets "not found" | Normalize npub↔hex before exact-match query |
| Fly-to with no neighborhood context | User lands on a dot in the void | Highlight node + its 1-hop neighborhood, dim the rest |

## "Looks Done But Isn't" Checklist

- [ ] **Bulk load:** Works on dev DB — verify against synthetic 5M/30M data without timeout/OOM, and time a *cold-cache* first run.
- [ ] **60fps:** Smooth at 10k — verify pan/zoom/hover stays 60fps at full node count with GPU layout running.
- [ ] **Edge rendering:** Looks complete — verify the drawn primitive count equals the actual edge count (no silent 65535 truncation).
- [ ] **Degree/community:** Colors appear — verify in/out-degree are distinct and community detection actually returned (didn't OOM the worker).
- [ ] **Directed:** Edges drawn — verify direction is recoverable in the view, not flattened to undirected.
- [ ] **Search:** Hex works — verify npub input also resolves and fly-to highlights the neighborhood.
- [ ] **Followers:** In-degree shown — verify it's derived from `follows`, not a second `followers` query (no double edges).
- [ ] **Refresh:** Re-pulls — verify it rebuilds the integer index space cleanly without leaking the old typed arrays.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| JSON transfer too slow / times out | MEDIUM | Activate the Go binary-streaming bridge escape hatch behind the load interface |
| GPU layout won't fit 5M nodes | MEDIUM-HIGH | Switch to node sampling for global view, or one-time precomputed layout snapshot |
| Hex strings already pervasive | HIGH | Retrofit integer index space — touches model, load, render, pick; cheaper to do upfront |
| CPU layout chosen first | HIGH | Re-architect on GPU engine (data model + pick path differ) — effectively a restart of rendering |
| Object model causing GC drops | HIGH | Convert to structure-of-arrays typed buffers throughout the hot path |
| Client-side centrality OOM | LOW-MEDIUM | Drop to degree-only; move community to a worker or approximate |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| 1. Hex pubkeys in buffers | Feasibility spike + Data-load | Heap snapshot: no string-dominated retained set; edges are `Uint32Array` |
| 2. Single un-paginated query | Data-load | Synthetic-scale load completes in seconds via `after` cursors |
| 3. `@hasInverse` double edges | Data-load | Edge count matches follow count (not 2x); followers derived |
| 4. CPU layout doesn't scale | Feasibility spike + Rendering-engine | GPU engine demoed at full scale before architecture commit |
| 5. GPU layout space ceiling | Layout (spike) | Layout validated at actual max N; fallback wired to a threshold |
| 6. Object-model GC | Data-model + Render-loop | Zero per-frame allocation; no GC sawtooth in Performance trace |
| 7. WebGL index/buffer limits | Rendering-engine init | WebGL2/uint-index check at startup; drawn primitives == edge count |
| 8. CPU picking | Interaction | GPU picking; hover stays 60fps at full scale |
| 9. Client-side analytics OOM | Analytics/coloring | Degree client-side; community in worker/approx; no main-thread freeze |
| 10. Directed nature lost | Data-model + Analytics | In/out-degree tracked separately; direction recoverable in view |
| 11. Over-scoping | Roadmap ordering (all phases) | No cosmetic work merged before full-scale 60fps demo |
| 12. Dev-DB-only testing | Feasibility spike | Synthetic 5M/30M benchmark exists and gates architecture |

## Sources

- cosmos.gl / `@cosmograph/cosmos` — "over one million nodes and links," GPU force simulation, and the explicit simulation-space-size limit ("if your graph has several million nodes, they may not fit at all"): https://github.com/cosmosgl/graph , https://openjsf.org/blog/introducing-cosmos-gl , https://nightingaledvs.com/how-to-visualize-a-graph-with-a-million-nodes/ , https://cosmograph.app/docs-general/concept/
- WebGL 32-bit index limit — `OES_element_index_uint` raises indices from 65535 to ~4.29B (WebGL2 native): https://webglfundamentals.org/webgl/lessons/webgl-indexed-vertices.html , https://registry.khronos.org/webgl/specs/latest/2.0/
- Dgraph pagination — `offset` is O(N) (times out at depth), `after` cursor is O(1): https://dgraph.io/docs/query-language/pagination/ , https://github.com/dgraph-io/dgraph/issues/5807
- Dgraph `@hasInverse` materializes two separate predicates (followers = full reverse of follows), distinct from DQL `@reverse`: https://discuss.dgraph.io/t/hasinverse-graphql-to-dql-mapping/10077 , https://discuss.dgraph.io/t/dql-reverse-vs-graphql-hasinverse/15639
- Project context: `.planning/PROJECT.md` (scale target, load model, direct-Dgraph decision and its escape hatch); DeepFry `CLAUDE.md` Dgraph schema (`pubkey @id`, `follows @hasInverse followers`)
- Domain experience: large-scale WebGL graph viz, typed-array/SoA GPU data modeling, Nostr pubkey/npub encoding

---
*Pitfalls research for: large-scale browser GPU graph visualization + Dgraph*
*Researched: 2026-06-22*

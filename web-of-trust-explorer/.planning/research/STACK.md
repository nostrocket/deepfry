# Stack Research

**Domain:** Browser-based GPU graph explorer — single global node-link map of a directed Web-of-Trust follow-graph (500k–5M nodes, tens of millions of directed edges) loaded from Dgraph, live GPU force layout, 60fps pan/zoom/hover.
**Researched:** 2026-06-22
**Confidence:** MEDIUM-HIGH on the render/layout engine choice; MEDIUM on the upper-scale (5M + live layout + browser-direct Dgraph JSON) feasibility, which sits at the edge and has a named fallback.

## Executive Verdict

There is effectively **one** library class in 2025/2026 that does *GPU layout AND GPU render in one engine at million-node scale*: **cosmos.gl** (the OpenJS-Foundation continuation of `@cosmograph/cosmos`). Everything else either renders fast but won't *lay out* a million nodes live (deck.gl, Sigma.js render-only path), or does neither at this scale (Cytoscape, vis-network, G6 default, D3-force). So the engine choice is not really a toss-up — it's cosmos.gl, with a custom WebGPU fallback flagged for the 5M ceiling.

The genuinely risky part is **not** the renderer. It is the **browser-direct Dgraph JSON pull of tens of millions of edges**, which has no streaming on the wire and balloons in JSON. That is the feasibility edge, and the escape hatch (thin Go bridge streaming a binary edge buffer) must be designed-for even if not built in v1.

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| **cosmos.gl** (`@cosmos.gl/graph`) | 3.x (current; OpenJS rename of `@cosmograph/cosmos` ≤2.x) | GPU force-layout **and** rendering engine — the whole map | The only maintained engine that runs the force simulation *and* draws on the GPU (was regl, now luma.gl/WebGL2 as of v3). Claims and demos 1M+ nodes/links interactively. Live layout is its core feature — no precompute needed, which is exactly the project constraint. |
| **TypeScript** | 5.x | Language | cosmos.gl is TS-native; types for the graph/position buffers matter at this scale. |
| **Vite** | 6.x | Dev server + bundler | Lean, instant HMR, zero-config TS, native ESM. Ideal for a single-page local dev tool. Do not reach for Next/Nuxt — there is no SSR/routing need. |
| **Vanilla TS or Svelte 5** | — / 5.x | App shell (controls panel, search box, filters) | cosmos.gl owns a `<canvas>` imperatively; the framework only drives a thin control panel. Keep the framework out of the render loop. Vanilla TS is the leanest; Svelte 5 (runes) if you want reactive controls without React's re-render overhead. **Avoid React** unless the team already lives in it — its render model fights an imperative WebGL canvas and adds weight for no benefit here. |
| **graphology** | 0.25.x | In-memory graph data model + algorithms host | De-facto JS graph data structure; hosts the algorithm ecosystem (degree, Louvain). Holds the canonical in-memory graph; feed cosmos.gl typed position/link arrays from it. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| **graphology-communities-louvain** | 2.x | Client-side community detection → node coloring | Always (a core requirement). ~50k nodes / ~1M edges in ~0.9s; million-node is seconds-scale — **run it in a Web Worker** so the main thread/render loop never stalls. |
| **graphology-metrics** | 2.x | Degree (in/out/total) for node sizing & coloring | Always. Degree is a trivial O(E) pass; can also be computed directly from the edge list without graphology if you skip the full data model. |
| **nostr-tools** | 2.x | npub ↔ hex pubkey encode/decode for the search box | For the "search a pubkey (hex or npub), fly-to + highlight" requirement (bech32 NIP-19). Tiny, no need for the full go-nostr equivalent. |
| **comlink** | 4.x | Ergonomic Web Worker RPC | Wrap the Louvain worker and (optionally) the Dgraph fetch+parse worker so JSON parsing of a huge payload doesn't block the UI thread. |
| **d3-scale / d3-scale-chromatic** | 4.x / 3.x | Map degree→size and community→color | cosmos.gl already bundles minimal d3 color/zoom; pull these only for the controls/legend palettes. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| **Vite** | Build/dev | `npm create vite@latest -- --template svelte-ts` (or `vanilla-ts`). |
| **vitest** | Unit tests | Same config surface as Vite; test the Dgraph→buffer transform and degree/community pipelines (not the GPU). |
| **TypeScript / tsc --noEmit** | Type checking | Buffer/typed-array correctness is the main bug surface. |
| **Chrome DevTools Performance + `chrome://gpu`** | Profiling | Verify 60fps and that layout actually runs on the GPU; watch for fallback to software WebGL. |

## How the Browser Talks to Dgraph (the critical, risky path)

**Use the DQL `/query` endpoint, not GraphQL.** POST to `http://localhost:8080/query` with header `Content-Type: application/dql`. Response is `{ "data": {...}, "extensions": {...} }` JSON.

Why DQL over GraphQL for the bulk pull:
- DQL lets you ask for *only* `pubkey` + the `uid`s of `follows` in one traversal, which is the most compact ID-only shape. GraphQL forces named fields/types and is chattier per edge.
- The GraphQL endpoint (`/graphql`) is for the typed `Profile` schema and is fine for the **search-one-pubkey** query, but not for the whole-graph dump.
- Both speak JSON only. **Neither streams** — the entire result is one JSON body. There is a `?respFormat=rdf` option but RDF triples are *larger* than compact JSON, not smaller.

**Most compact JSON shape:** query an internal integer index (uid) adjacency, not 64-char hex strings, for the edge list. Emit edges as `[srcUid, dstUid]` pairs and keep a single `uid→pubkey` map fetched once. Tens of millions of edges as `{"from":...,"to":...}` objects is the worst case (object key overhead × 10M+); a flat array of integer pairs is dramatically smaller.

**Realistic transfer reality (the feasibility edge):**
- 10M edges as naive JSON objects ≈ **hundreds of MB to >1GB** uncompressed; even as flat integer-pair arrays it is **~100–250 MB** of JSON text. gzip/zstd at the Dgraph/HTTP layer helps the wire, but the browser still must **parse the whole string** (`JSON.parse` is single-shot, blocking, and memory-doubling).
- Dgraph itself can take **multiple seconds** to encode a result this large (the `extensions.server_latency.encoding_ns` will show it).
- **Mitigations that keep v1 browser-direct:** (1) paginate the edge dump with DQL `first:`/`offset:` or `after:` (uid cursor) into chunks of ~1M edges and parse each chunk in a Worker; (2) do the fetch+`JSON.parse`+buffer-fill entirely in a Web Worker so the UI never freezes; (3) request gzip via `Accept-Encoding`.
- **This combination — 5M nodes + tens of millions of edges + browser-direct JSON + live layout — is at the edge of feasibility.** At 500k–1M nodes it is comfortable. At 5M nodes / 30M+ edges, expect 10s+ load times and GBs of transient memory; this is where it can fall over.

**Named fallback (design for it now, build if needed):** a **thin Go bridge** (fits the surrounding deepfry Go stack) that runs the Dgraph DQL/gRPC query server-side and streams a **binary edge buffer** (e.g. length-prefixed `Int32Array` pairs, or Arrow IPC) over a `fetch()` `ReadableStream`. The browser fills GPU buffers incrementally with **zero JSON parsing**. This cuts transfer ~5–10x and removes the parse-blocking cliff. PROJECT.md already names this as the escape hatch — keep the browser's "load source" behind an interface so swapping JSON-direct → binary-stream is a one-file change.

## Community Detection & Degree at Scale

- **Degree (size/color):** trivial. Single O(E) pass over the edge list; do it in the same Worker that parses the data. Both in-degree (followers) and out-degree (follows) are meaningful for a *directed* WoT graph — size by total or by followers (influence).
- **Community detection:** `graphology-communities-louvain`. Directed-modularity supported. ~1s at 50k/1M-edge; **seconds** at million-node — acceptable as a one-time post-load step **in a Worker**. Leiden (higher quality, fewer disconnected communities) has no production-grade JS port at this scale; Louvain is the pragmatic choice. If quality matters more than latency at 5M, run Leiden in the Go bridge and ship community IDs alongside the edge buffer.

## WebGL vs WebGPU at This Scale

- **WebGL2 (via cosmos.gl) is sufficient and recommended for v1**, including up to ~1–2M nodes. It is the proven, maintained path; cosmos.gl v3 runs the force sim in fragment/vertex shaders on WebGL2 today. **There is no mature, off-the-shelf WebGPU graph-layout engine in 2026** — choosing WebGPU means writing your own compute-shader force layout.
- **WebGPU is genuinely better at the 5M ceiling**: compute shaders give ~100–150x throughput on particle/force updates vs WebGL fragment-shader tricks, and that is exactly the layout bottleneck at 5M nodes. Browser support is ~83% (Chrome/Edge/Firefox-on-Windows/Safari 26) — fine for a local dev tool on a known machine.
- **Recommendation:** ship WebGL2/cosmos.gl in v1. Treat a **custom WebGPU compute-shader force layout** as a Phase-N optimization *only if* WebGL2 layout can't sustain 60fps at the actual production node count. Do not start with WebGPU — it is a from-scratch engine, not a library swap.

## Installation

```bash
# Scaffold (pick one shell)
npm create vite@latest wot-explorer -- --template svelte-ts   # or vanilla-ts

# Core
npm install @cosmos.gl/graph graphology

# Supporting
npm install graphology-communities-louvain graphology-metrics nostr-tools comlink d3-scale d3-scale-chromatic

# Dev
npm install -D typescript vitest
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| cosmos.gl (GPU layout+render) | **deck.gl** (`GraphLayer`/`ScatterplotLayer`) | If layout is **precomputed** and you only need to *render* ~1M points at 60fps. deck.gl has no GPU force layout — violates the "live layout" requirement. Good as a custom WebGPU render target later. |
| cosmos.gl | **Sigma.js v3 + graphology** | If the graph is **≤100k nodes** and layout can run CPU-side (forceatlas2 degrades past ~50k edges). Best DX for mid-size; wrong tool at million scale. |
| cosmos.gl | **G6 v5 (AntV)** | Rich interaction/UI out of the box, but its WebGL renderer + layouts target tens of thousands, not millions. Use for feature-rich mid-size graph apps. |
| cosmos.gl | **ngraph (ngraph.graph + ngraph.forcelayout / pixel)** | If you want a lightweight CPU/3D layout toolkit for smaller graphs or offline layout generation. Not a million-node live-layout solution. |
| DQL `/query` direct | **Thin Go bridge streaming binary** | When JSON transfer/parse of tens of millions of edges becomes the load-time bottleneck (the 5M case). This is the planned escape hatch. |
| Louvain (client) | **Leiden in Go bridge** | When community quality at 5M matters more than zero-backend simplicity. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| **D3-force / d3 SVG graph** | CPU layout + DOM/SVG chokes at ~5–10k nodes; orders of magnitude below target. | cosmos.gl |
| **Cytoscape.js / vis-network** | Canvas/CPU; practical ceiling ~5–10k nodes/edges; layouts don't scale. | cosmos.gl |
| **GraphQL `/graphql` for the whole-graph dump** | Chatty typed responses, larger payloads, harder to get a flat ID-only edge list. | DQL `/query` (`application/dql`) for bulk; reserve `/graphql` for single-pubkey search |
| **`?respFormat=rdf`** | RDF triples are larger than compact JSON, not a transfer win. | Compact integer-pair JSON, or binary via the Go bridge |
| **`JSON.parse` of the full payload on the main thread** | Single-shot, blocking, memory-doubling → multi-second UI freeze and OOM risk at 5M. | Parse in a Web Worker, chunked/paginated; or binary stream (no parse) |
| **React for the render shell** | Re-render model fights an imperative WebGL canvas; adds weight for no gain on a single-canvas tool. | Vanilla TS or Svelte 5 for the control panel only |
| **Starting on WebGPU** | No mature WebGPU graph-layout library in 2026; from-scratch compute-shader engine. | WebGL2 via cosmos.gl first; WebGPU only as a measured later optimization |

## Stack Patterns by Variant

**If the production graph is ≤1M nodes / ≤10M edges:**
- cosmos.gl WebGL2 + browser-direct DQL JSON (chunked, Worker-parsed) is sufficient. No Go bridge needed.

**If the production graph approaches 5M nodes / 30M+ edges:**
- Keep cosmos.gl for render+layout, but expect WebGL2 layout to be the 60fps risk — prototype the **WebGPU compute-shader force layout** as the optimization.
- Build the **thin Go bridge** streaming a binary edge buffer; browser-direct JSON parse will be the load-time wall.

**If layout quality (clean communities) becomes a priority:**
- Move Louvain → Leiden server-side in the Go bridge; ship community IDs with the data.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| `@cosmos.gl/graph` 3.x | luma.gl (bundled), WebGL2 | v3 migrated regl→luma.gl; needs WebGL2 (universal in target browsers). Package was `@cosmograph/cosmos` ≤2.x — verify the exact current name/version on npm at install time (rename is recent). |
| graphology 0.25.x | graphology-communities-louvain 2.x, graphology-metrics 2.x | All share the graphology Graph instance; keep versions aligned per graphology's peer ranges. |
| Vite 6.x | TypeScript 5.x, Svelte 5.x, vitest | Standard modern toolchain; no known conflicts. |

## Open Questions / Feasibility Flags

- **Exact current npm name/version of cosmos.gl** — the `@cosmograph/cosmos` → `@cosmos.gl/graph` OpenJS rename is recent; confirm on npm before pinning (MEDIUM confidence on the exact identifier, HIGH on the engine being the right choice).
- **Real 5M-node 60fps on the target GPU** — vendor claims "1M+, not the limit," but independent 5M-node live-layout benchmarks are scarce. Must be validated with a real production-sized export early (spike), not assumed.
- **Browser-direct JSON wall at tens of millions of edges** — the single biggest risk. Prototype the actual Dgraph dump size/time against real data in an early phase; the Go binary-stream bridge is the committed fallback.

## Sources

- OpenJS Foundation — "Introducing cosmos.gl" (engine joins OpenJS, GPU force layout+render, 1M+ nodes) — MEDIUM
- github.com/cosmosgl/graph README (`@cosmos.gl/graph`, WebGL2 via luma.gl in v3, no WebGPU, install/maintenance) — MEDIUM-HIGH
- Nightingale DVS — "How to Visualize a Graph with a Million Nodes" (Cosmograph as the million-node browser tool; CPU chokes ~100k) — MEDIUM
- Sigma.js v3 release notes (ouestware) + GitHub perf issues #239/#967 (WebGL renderer over graphology; layout limits ~50k edges) — MEDIUM
- deck.gl performance docs (ScatterplotLayer ~1M items @60fps; no force layout) — MEDIUM
- Dgraph docs — Raw HTTP / `/query` (`application/dql`, JSON `{data,extensions}`, respFormat=rdf, no streaming) — MEDIUM-HIGH
- Discuss Dgraph — large-DB query latency threads (multi-second on huge result sets) — MEDIUM
- npm graphology-communities-louvain (benchmarks: 50k/994k-edge ≈ 938ms) — MEDIUM-HIGH
- WebGPU browser-support / WebGL-vs-WebGPU 2026 articles (≈83% support, ~150x compute-shader speedup, no iOS Safari) — MEDIUM

---
*Stack research for: browser GPU graph explorer at million-node scale over Dgraph*
*Researched: 2026-06-22*

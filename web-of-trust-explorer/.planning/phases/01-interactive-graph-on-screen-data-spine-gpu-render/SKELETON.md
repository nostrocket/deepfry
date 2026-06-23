# Walking Skeleton — WoT Graph Explorer

**Phase:** 1
**Generated:** 2026-06-23

## Capability Proven End-to-End

A developer runs `npm run dev`, and a graph generated through the swappable `GraphTransport` interface renders as a single GPU node-link map (cosmos.gl / WebGL2) that they can pan and zoom in the browser. (Plan 01 proves this with a small synthetic graph; Plans 02/03 scale the two ends — GPU ceiling and real Dgraph wire — on the same spine.)

## Architectural Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language / build | TypeScript 5.x + Vite 6.x (`vanilla-ts` template) | cosmos.gl is TS-native; typed-array correctness is the main bug surface; Vite gives instant HMR + native ESM Web Worker support, zero-config. No SSR/routing need (single-page local tool). |
| Render + layout engine | `@cosmos.gl/graph@3.0.0` (WebGL2 via luma.gl) — pinned exactly | The only maintained engine that runs the GPU force simulation AND draws on the GPU; `setPointPositions`/`setLinks` ARE the SoA buffers this project mandates; `start/pause/unpause/stop` map 1:1 onto the run/pause/auto-freeze model. Pin 3.0.0 (NOT 3.1.0-beta, NOT legacy `@cosmograph/cosmos`). |
| App shell | Vanilla TS (control panel only) | cosmos.gl owns the `<canvas>` imperatively; the shell only drives Run/Pause + Fit buttons, loader, tooltip, and verdict readout. React is explicitly excluded (re-render model fights an imperative WebGL canvas). |
| Data model | Structure-of-arrays typed buffers (`Float32Array` positions, `Uint32Array`/`Float32Array` links) + `Map<hex,uint32>` dense remap | No per-node heap objects (the 18GB-pool budget at 5M nodes forbids them); cosmos.gl requires SoA buffers anyway. Float32 link buffer is exact only up to 2^24 (16.7M) indices — `MAX_NODE_INDEX` guard. |
| Concurrency | Web Workers for fetch+parse+generate; `Transferable` ArrayBuffers to main | Keeps blocking `JSON.parse` / BA generation off the 60fps render loop (D-08); zero-copy buffer hand-off. `comlink` optional (raw `postMessage`+Transferable is leaner for one-shot bulk). |
| Data access | Browser-direct HTTP to Dgraph `:8080`, `POST /query`, `Content-Type: application/dql`, `after`-cursor paging | DQL gives the most compact ID-only edge dump (vs chatty GraphQL); `after:<uid>` is O(1) paging. READ-ONLY — never mutate Dgraph/StrFry (DeepFry data-separation rule). The Go bridge (PERF-01) is the deferred escape hatch behind the same transport interface. |
| Swappable transport | `GraphTransport` interface; `SyntheticTransport` + `DgraphTransport` implement it | JSON-direct → Go-binary-stream is a one-file swap later (PERF-01, v2), gated on the Phase 1 feasibility verdict. |
| Memory metric | `performance.measureUserAgentSpecificMemory()` (needs COOP `same-origin` + COEP `require-corp` headers in Vite), fallback `performance.memory.usedJSHeapSize` | Peak JS heap is a co-equal verdict metric to framerate (D-08); Chrome is the reference browser (D-07). |
| Test runner | vitest (ships with Vite 6) | CPU pipeline (remap, generator, parser, transport, precision guard) is unit-tested; GPU/60fps + at-scale memory are manual-instrumented spikes. |
| Reference target | MacBook Pro M3 Pro / 18GB unified / Chrome (D-06/D-07) | Single-developer local tool — the box it runs on is the only hardware that matters. |
| Directory layout | `src/{transport,workers,graph,ui}` + `src/types.ts` + `src/main.ts`; `tests/*.test.ts` | Matches RESEARCH recommended structure; keeps the render loop, the workers, and the thin shell cleanly separated. |

## Stack Touched in Phase 1

- [x] Project scaffold (Vite + TS, vitest, COOP/COEP headers, Web Worker support) — Plan 01
- [x] Real UI interaction wired to the data path — pan/zoom on the GPU canvas (Plan 01); Run/Pause + Fit + hover tooltip (Plan 02)
- [x] Real "data read" through the transport — synthetic generator (Plan 01 small / Plan 02 at scale) AND real Dgraph after-cursor bulk-load (Plan 03)
- [x] Local full-stack run command — `npm run dev` exercises generate/fetch → Worker parse → SoA buffers → cosmos.gl GPU render → pan/zoom (no deployment; this is a local dev tool by design)
- [N/A] Deployment — out of scope (local read-only developer tool; hosting contradicts the GPU-in-browser core, per REQUIREMENTS Out of Scope)

## Out of Scope (Deferred to Later Slices)

- Degree/community **overlays** (sizing/coloring by in/out degree, Louvain community color) → Phase 2 (OVER-01/02). Phase 1 only *derives* in-degree (O(E) pass); it does not encode it visually.
- **Search** (hex/npub fly-to), **neighborhood highlight**, full **node-detail panel**, **time-range filter**, **Refresh** → Phase 3 (EXPL/FILT/DATA-02).
- `graphology`, `d3-scale`, `nostr-tools` dependencies → Phase 2/3 (NOT installed in Phase 1 per RESEARCH scope guard).
- **Go binary-streaming bridge** (PERF-01) → v2, gated on the Phase 1 JSON-wire feasibility verdict.
- **Adjustable synthetic scale knob**, Go-seeded Dgraph fixture, three-way Run/Pause/Freeze controls → declined (D-02/D-01/D-12).
- WebGPU compute-shader force layout → Phase-N optimization, only if WebGL2 fails the 60fps verdict.

## Subsequent Slice Plan

Each later phase adds one vertical slice on top of this skeleton without altering its architectural decisions (SoA buffers, swappable transport, cosmos.gl/WebGL2, topology-static/style-dynamic):

- **Phase 2 — Terrain Overlays:** size/color nodes by in/out degree and color by Louvain community, computed one-shot in a Web Worker, mutating only style buffers (never re-layout).
- **Phase 3 — Explore & Slice:** hex/npub search + fly-to + neighborhood highlight, node-detail panel, time-range hide/dim filter, and explicit Refresh — all without breaking the laid-out terrain or 60fps.

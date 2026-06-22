# WoT Graph Explorer

## What This Is

A fast, graphical explorer for the Web-of-Trust follow-graph stored in DeepFry's Dgraph database. It bulk-loads the entire graph into the browser once per session and renders it as a single GPU-accelerated global map, letting a developer pan, zoom, and hover the whole graph at 60fps to understand its shape, structure, and terrain — where the hubs, clusters, bridges, and dense vs sparse regions are. It is a local developer tool, not a public product.

## Core Value

**Smooth 60fps interaction with the whole graph at once.** If everything else fails, panning/zooming/hovering the full follow-graph as a single global map must stay buttery. The entire purpose is to *see the terrain*, and terrain is only legible when you can fluidly move across it.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Bulk-load the entire WoT follow-graph from Dgraph into the browser at session start (a few seconds), then operate on the in-memory copy
- [ ] Render the whole graph as a single global node-link map using GPU (WebGL/WebGPU) — millions of nodes/edges
- [ ] Pan / zoom / hover at 60fps on the full graph
- [ ] Compute graph layout live (GPU-accelerated force layout), no precomputed snapshot required
- [ ] Size and color nodes by degree (follower/follow count) so hubs and influencers stand out
- [ ] Detect communities and color nodes by community to reveal the terrain's regions
- [ ] Search for a pubkey (hex or npub) and fly-to / highlight it and its neighborhood
- [ ] Filter / slice the graph by `kind3CreatedAt` and `last_db_update` (activity / freshness over time)
- [ ] Refresh action that re-pulls the graph from Dgraph for an updated view

### Out of Scope

- Server-side rendering or a hosted/public deployment — local dev tool only, no auth/hosting/rate-limiting [v1]
- A dedicated Go bridge service between browser and Dgraph — browser queries Dgraph directly (HTTP/GraphQL :8080) for simplicity [v1; revisit if JSON transfer becomes the bottleneck]
- Editing or writing graph data — read-only explorer; canonical data lives in StrFry/Dgraph and is never mutated here
- Precomputed/static layout snapshots — layout is computed live each session [revisit only if live layout can't hit 60fps at target scale]
- Multi-user collaboration, saved views, sharing — single-developer tool

## Context

- **Data source:** Dgraph holds an ID-only `Profile` graph. Schema: `pubkey` (`@id`, exact-searchable), `follows: [Profile]` (`@hasInverse` of `followers`), `followers: [Profile]`, `kind3CreatedAt: Int`, `last_db_update: Int`. The follow-graph is directed (A follows B). No event payloads live in Dgraph — only pubkeys and relationships.
- **Endpoints:** Dgraph GraphQL/HTTP at `http://localhost:8080`, gRPC at `localhost:9080`, Ratel UI at `http://localhost:8000`. Brought up via `docker-compose -f docker-compose.dgraph.yml up -d` from the deepfry root.
- **Surrounding stack:** DeepFry is a Go backend stack around an unmodified StrFry Nostr relay; the web-of-trust crawler subscribes to kind-3 events and writes the pubkey graph to Dgraph. This explorer is a new sibling subsystem under `web-of-trust-explorer/`.
- **Scale target:** 500k–5M+ pubkey nodes, tens of millions of follow edges. This is the dominant design constraint — it forces GPU rendering and rules out classic CPU/SVG/Canvas approaches.
- **Repo placement:** Lives in a nested subdir of the `deepfry` git worktree; planning docs and code track to the outer deepfry repo on `main`.

## Constraints

- **Performance**: 60fps pan/zoom/hover on the full graph at 500k–5M+ nodes — This is the core value; the rendering technology must be GPU-based to meet it.
- **Tech stack**: Frontend is necessarily JS/TS for WebGL/WebGPU; the surrounding deepfry stack is Go — Any helper tooling should fit the Go ecosystem, but v1 has no backend service.
- **Data access**: Browser queries Dgraph directly (HTTP/GraphQL :8080) — Chosen for simplicity as a local tool; accepts a slower JSON wire format as a known tradeoff.
- **Load model**: Whole graph bulk-loaded once per session, operated on in memory — Continuous live re-querying at this scale is not viable client-side.
- **Read-only**: Never mutate Dgraph or StrFry data — Canonical data separation rule of the DeepFry project.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| GPU layout-and-render in the browser (e.g. Cosmograph/`@cosmograph/cosmos` or equivalent WebGL/WebGPU graph engine) | Only class of tech that delivers large-scale + global map + 60fps + live layout simultaneously | — Pending |
| Bulk-load whole graph per session, then in-memory | Bulk-pulling tens of millions of edges on every interaction is infeasible; one load + refresh is fresh-enough for a dev tool | — Pending |
| Browser → Dgraph direct (no Go bridge) | Simplicity for a local tool; avoids a new service | ⚠️ Revisit — JSON transfer of millions of edges may become the load-time bottleneck; thin Go bridge streaming binary is the escape hatch |
| Compute layout live, no precompute | User wants always-current terrain without an offline pipeline | — Pending |
| Local dev tool, no auth/hosting | Single-developer/team use against dev Dgraph | — Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-22 after initialization*

# Phase 1: Interactive Graph On Screen (Data Spine + GPU Render) - Context

**Gathered:** 2026-06-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Put the whole follow-graph on screen as one interactive global map and confront the
two dominant risks (JSON wire + GPU layout ceiling).

In scope:
- Bulk-load the entire `follows` graph from Dgraph into memory once per session via
  DQL `after`-cursor paging (followers/in-degree derived client-side), with a visible
  progress state.
- Dense-remap hex pubkeys → uint32 indices in structure-of-arrays typed buffers (no
  per-node heap objects).
- Single global GPU node-link render (cosmos.gl / WebGL2) with a live force layout the
  user can run, pause, and freeze.
- Pan / zoom / hover at 60fps; fit-to-screen / reset to whole map (REND-04).
- Validate the GPU/60fps ceiling against synthetic ~5M-node / ~30M-edge power-law data.
- Data source behind a swappable transport interface.
- A recorded feasibility verdict on browser-direct JSON load time.

Out of scope (later phases): degree/community overlays (Phase 2); search, node-detail
panel, neighborhood highlight, time filter, refresh (Phase 3).

Requirements: DATA-01, DATA-03, REND-01, REND-02, REND-03, REND-04.

</domain>

<decisions>
## Implementation Decisions

### Synthetic Data & Validation Strategy
- **D-01:** The at-scale test graph is generated **in-browser** (in a Worker), not via
  Dgraph or a Go seeder. Zero infra; lets the GPU/layout/60fps risk be hammered instantly.
  This **only** proves the GPU side — it produces no real JSON wire.
- **D-02:** Generator is **fixed at the 5M-node / 30M-edge target** (no adjustable
  scale knob). Simplest; proves the ceiling. (A scale slider to chart the fall-off
  curve was considered and declined — see Deferred.)
- **D-03:** Power-law **shape parameters** (hub skew, degree distribution) are left to
  the researcher/planner to specify a realistic generator — not user-specified.
- **D-04:** The browser-direct **load-time feasibility verdict is measured separately
  against the real dev Dgraph at its current real size**, then extrapolated to target
  scale. The verdict is an honest estimate, not a full-scale measurement. The two risks
  are tested by two different means: synthetic = GPU ceiling, real Dgraph = JSON wire.

### Feasibility Verdict Bar
- **D-05:** **PASS = ≤ ~30s** for a session-start bulk load (Dgraph query → parse →
  buffers ready), extrapolated to target scale. Lenient on purpose — keeps the deferred
  Go binary-streaming bridge (PERF-01, v2) deferred as long as possible. Worse than ~30s
  triggers pulling PERF-01 forward.
- **D-06:** Reference machine for both the 60fps and load-time verdict is **this dev
  machine: MacBook Pro, Apple M3 Pro — 11-core CPU (5P+6E), 14-core GPU, 18 GB unified
  memory, macOS 26.3 / Metal 4.** Single-developer local tool, so the box it runs on is
  the only hardware that matters.
- **D-07:** Reference browser is **Chrome** (matches the research doc's tooling — Chrome
  DevTools Performance + `chrome://gpu`; strongest WebGL2 perf; clearest WebGPU path later).
- **D-08:** ⚠️ **The 18 GB unified-memory pool is a co-equal risk to framerate.** The
  research doc warns 5M/30M can need "GBs of transient memory" during `JSON.parse`
  (single-shot, memory-doubling). On this box the **memory wall may bite before the
  framerate wall.** Researcher must treat peak JS heap as a first-class verdict metric and
  design the parse/buffer-fill to stay within budget (chunked/Worker parse, avoid
  doubling).

### Loading / Progress UX
- **D-09:** Loader shows **staged labels** (`Fetching from Dgraph…` → `Parsing edges…`
  → `Building layout…`) with a **live count ticking up** (e.g. "12.3M / ~30M edges").
  Diagnostic by design — doubles as the feasibility-measurement readout. (% bar declined:
  cursor paging has no honest upfront total.)
- **D-10:** On load completion, surface an **on-screen breakdown + console log** of the
  verdict raw material: fetch ms / parse ms / layout-ready ms / node+edge count / peak JS
  heap. Makes the feasibility verdict a glance and copy-pasteable into CONTEXT/VERIFICATION.

### Layout Controls & Default Behavior
- **D-11:** Force layout **auto-starts settling immediately on load** — the user watches
  the hairball resolve into terrain (the "it's alive" moment; self-demonstrates live layout).
- **D-12:** Controls = a single **Run/Pause toggle + auto-freeze when settled** (motion
  below a threshold → stop iterating, fix positions, free GPU sim cost). "Freeze" = the
  settled resting state you pan/zoom on. The "settled" threshold value is researcher/planner
  discretion.
- **D-13:** Provide **fit-to-screen / reset** as a single action returning the view to the
  whole map (REND-04).

### Hover Behavior (Phase 1 scope)
- **D-14:** Hover = **visual highlight + a minimal tooltip** showing just the raw node
  index or hex pubkey. Proves hit-testing at 60fps on 5M nodes (REND-03) without bleeding
  Phase 3's full node-detail panel into this phase.

### Claude's Discretion
- Exact cosmos.gl npm package name/version (`@cosmos.gl/graph` vs legacy
  `@cosmograph/cosmos`) — researcher confirms on npm at install time (flagged MEDIUM
  confidence in the stack doc).
- Power-law generator parameters (D-03), "settled" motion threshold (D-12), chunk/page
  size for paged load and Worker parse.
- Shape of the swappable transport interface (only that one must exist).
- Edge rendering approach at 30M edges (whether/how edges draw vs nodes-only at extreme
  zoom-out) — planner's call within the 60fps constraint.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Technology Stack (the engine bet — already researched)
- `web-of-trust-explorer/.claude/CLAUDE.md` — full Technology Stack decision: cosmos.gl
  (`@cosmos.gl/graph` 3.x) for GPU layout+render, graphology data model, Vite 6 + TS 5,
  vanilla TS / Svelte 5 control shell (not React). Includes the "How the Browser Talks to
  Dgraph" risk section (JSON wire, no streaming, `JSON.parse` blocking/memory-doubling,
  paginate with DQL `after`/`first`, parse in a Worker, request gzip), WebGL2-vs-WebGPU
  verdict (WebGL2/cosmos.gl for v1), and the "What NOT to Use" list. **This is the primary
  spec for HOW to build Phase 1.**

### Data Source (Dgraph schema + endpoints)
- `web-of-trust-explorer/.planning/PROJECT.md` §Context — Dgraph `Profile` schema:
  `pubkey` (`@id`, exact-searchable), `follows: [Profile]` (`@hasInverse` of `followers`),
  `followers`, `kind3CreatedAt: Int`, `last_db_update: Int`. Directed follow-graph, ID-only
  (no event payloads). Endpoints: GraphQL/HTTP `:8080`, gRPC `:9080`, Ratel `:8000`;
  brought up via `docker-compose -f docker-compose.dgraph.yml up -d` from deepfry root.
- `deepfry/CLAUDE.md` §"Dgraph Schema" + §Infrastructure — same schema confirmed at the
  stack level; data-separation rule (read-only, never mutate Dgraph/StrFry).
- `deepfry/config/dgraph/schema.graphql` — the live `Profile` GraphQL schema definition.

### Requirements & Roadmap
- `web-of-trust-explorer/.planning/REQUIREMENTS.md` — DATA-01/03, REND-01..04 (v1) and the
  v2 deferral of PERF-01 (the Go bridge escape hatch this phase's verdict gates).
- `web-of-trust-explorer/.planning/ROADMAP.md` §"Phase 1" — goal + 5 success criteria.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- None — `web-of-trust-explorer/` is greenfield (no `package.json`, no source yet). This
  phase scaffolds the app (Vite + TS).

### Established Patterns
- Surrounding deepfry stack is Go; any helper tooling (declined here per D-01) would fit
  Go. The web-of-trust **crawler** is the upstream that populates the Dgraph `Profile`
  graph this tool reads — useful as the source-of-truth for what the data actually looks
  like, but not imported.

### Integration Points
- Read-only HTTP to Dgraph at `http://localhost:8080` (DQL `/query`, `application/dql`)
  behind the swappable transport interface. No writes, ever.

</code_context>

<specifics>
## Specific Ideas

- The loader is explicitly a **measurement instrument**, not just decoration — staged
  labels + live counter + post-load timing/memory breakdown exist to produce the
  feasibility verdict (D-09, D-10).
- "It's alive" first impression: auto-settling layout on load is a deliberate UX choice,
  not just a default (D-11).

</specifics>

<deferred>
## Deferred Ideas

- **Adjustable synthetic-data scale knob** (presets/slider to chart where 60fps degrades
  below 5M) — considered, declined for D-02's simplicity. Reconsider if the feasibility
  spike needs a fall-off curve rather than a single ceiling pass/fail.
- **Go generator → seed Dgraph / static fixture** for full end-to-end at-scale load
  testing — declined in favor of in-browser (D-01). This is the natural escalation if the
  extrapolated-from-real-Dgraph verdict (D-04) proves too uncertain.
- **Three explicit Run/Pause/Freeze controls** — declined for the simpler toggle +
  auto-freeze (D-12).
- Per requirements: degree/community overlays → Phase 2; search/fly-to, neighborhood
  highlight, full node-detail panel, time filter, refresh → Phase 3. PERF-01 Go
  binary-streaming bridge → v2 (gated by this phase's verdict).

</deferred>

---

*Phase: 1-Interactive Graph On Screen (Data Spine + GPU Render)*
*Context gathered: 2026-06-22*

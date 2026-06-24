# Phase 1: Foundation + Stats Dashboard - Context

**Gathered:** 2026-06-24
**Status:** Ready for planning

<domain>
## Phase Boundary

The **walking skeleton**: the thinnest end-to-end working slice proving typed GraphQL
transport works browser→lens. Scaffold React 19 + Vite + TypeScript, generate a typed
urql client from the live `/graphql` introspection (GraphQL Codegen client-preset),
connect the client **directly** to the lens at a configurable base URL (env var, default
`http://127.0.0.1:8080/graphql`) over wildcard CORS (no proxy), build the
error-classifying / readiness-gating transport layer, and render one real read — the
corpus `stats` query — polled with a hidden-tab-aware interval and a `maxLevId`-diff
"corpus changed" nudge.

**Out of this phase:** no spam signals, no npub/hex identifier handling, no batch logic,
no drill-down, no charts. Those are Phases 2–4. Keep `transport/` generic so later phases
inherit the classifier, readiness gate, and cursor-accumulator loop without rework.

</domain>

<decisions>
## Implementation Decisions

### Scaffolding & Build
- Scaffold the Vite app **in-place at the `GraphQLExplorer/` repo root** — `package.json`,
  `src/`, `vite.config.ts`, `codegen.ts` live directly in this directory (this dir *is* the
  project per PROJECT.md monorepo placement). No nested `graphql-explorer/` subfolder.
- Pin **Vite 7 + `@vitejs/plugin-react@5`** (conservative daily-driver; Vite 8 not adopted in v1).
- **No router in Phase 1** — single stats view. Routing arrives in Phase 2 (research A4).
- Enforce the `graphql@16` pin with an **automated guard** (postinstall or CI check that
  fails if the resolved `graphql` version is ≥ 17) **in addition to** verifying
  `package-lock.json` resolves `graphql` to 16.x. Pitfall 11 is silent, so guard it.

### Locked from Research (carried into plan — do not relitigate)
- Stack pins: `react`/`react-dom@19`, `urql@5`, `@urql/core@6`, **`graphql@16.14.2` (exact)**,
  `@graphql-codegen/cli@7`, `@graphql-codegen/client-preset@6`, `typescript@5.9`,
  `@types/react@19` / `@types/react-dom@19`.
- **Direct connection, configurable base URL.** urql `url` resolves from
  `import.meta.env.VITE_GRAPHQL_URL ?? 'http://127.0.0.1:8080/graphql'` in exactly one module
  (`transport/config.ts`). Never hardcode the base URL inline; never use a relative `/graphql`
  path; never re-introduce a Vite `server.proxy`. `READY_URL`/`HEALTH_URL` derive from the same base.
- **No credentials** on requests (API unauthenticated; wildcard CORS won't honor them).
- **Error classifier as a single transport boundary** (`transport/errors.ts`): one
  `classify(result)` → discriminated union (`INVALID_CURSOR`, `TOO_MANY_AUTHORS`, `VALIDATION`,
  `INTERNAL`, `NOT_READY`/503, `PAYLOAD_TOO_LARGE`/413, `NETWORK`). Branch on `result.error`
  before reading `result.data` (GraphQL errors arrive on HTTP 200). Views never read `errors[]`.
- **Readiness gate** (`transport/readiness.ts`): poll `GET <base>/ready` until 200 with bounded
  backoff (500ms→5s cap) before the first query; distinct "connecting to relay…" state, never
  a generic error.
- **`useStatsPoll`**: `setTimeout`-reschedule (not `setInterval`), Page Visibility pause on
  hidden tab, `maxLevId`-diff flips a **nudge flag only** (never auto-refetch). Default interval
  **5000ms**, exposed as a tunable constant.
- **Cursor accumulator** (`transport/paginate.ts`): scaffold only this phase; first exercised
  in Phase 2; the Phase-4 `authors` query reuses it.
- "Explicit `limit` on every query" is established now as a transport convention (`stats` takes
  no limit, but the rule applies to all later queries); handle `413` in the HTTP-status branch.

### Project structure (from research)
- `src/transport/` (config, client, errors, readiness, paginate), `src/gql/` (generated — do
  not edit), `src/queries/stats.graphql.ts`, `src/hooks/useStatsPoll.ts`,
  `src/views/StatsDashboard.tsx`, `src/App.tsx` (readiness shell), `src/main.tsx` (urql Provider).
- `codegen.ts` `schema` points at the live introspection URL (Node fetch, CORS-free); checked-in
  SDL fallback is acceptable if the backend is down at codegen time.
- `.env.example` documents `VITE_GRAPHQL_URL`; `.env` is gitignored.

### UI (from approved UI-SPEC — carried verbatim)
- Dark single theme, hand-rolled plain CSS (no shadcn, no component lib). 8-pt spacing scale.
- Mono for data values, sans for chrome; weights 400/600 only. Tokens/colors per UI-SPEC.
- Accent teal `#3DD6C0` reserved to exactly two elements: the "corpus changed" nudge and the
  live-poll active dot. Semantic colors bound to state meaning (connecting blue, recoverable
  amber, hard-failure red) — color never the sole signal (pair with label/shape).
- Four stat cards (`eventCount`, `maxLevId`, `dbVersion`, `pinnedStrfryVersion`), 2×2 grid ≥ md.
- All state treatments + copy strings per UI-SPEC, including empty-corpus (`eventCount=0` is a
  calm fact, NOT an error), poll-paused, and the complete error-state set.
- `pinnedStrfryVersion` rendered as escaped plaintext; large integers use `Intl.NumberFormat`.
- "Refresh stats" is the only CTA (re-pulls `stats` on demand).

### Claude's Discretion
- Exact poll-interval tuning constant location, CSS Modules vs single tokens stylesheet,
  the precise urql `networkError.response.status` access path (verify against `@urql/core@6`
  at implementation — Assumption A2), and component decomposition of `StatsDashboard`.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- None — greenfield. This phase creates the project from scratch inside `spamhunter/GraphQLExplorer/`.

### Established Patterns
- None yet — Phase 1 establishes the foundational `transport/` patterns the whole app inherits.

### Integration Points
- Backend: LMDB2GraphQL lens at `127.0.0.1:8080` — `POST /graphql` (data + introspection),
  `GET /ready`, `GET /health`. Must be reachable to prove the walking skeleton and at codegen time.

</code_context>

<specifics>
## Specific Ideas

- `contract.md` (v1.2, code-verified) is the authoritative interface spec — treat as source of
  truth over the stale `STACK.md`/`PITFALLS.md` proxy guidance.
- The walking-skeleton proof requires a live backend read; success criteria 1–5 are all "a real
  cross-origin browser query succeeds." Codegen has a fallback (checked-in SDL) if backend is down.

</specifics>

<deferred>
## Deferred Ideas

- npub/hex normalization (`nostr-tools`) — Phase 2.
- Cursor pagination exercised, drill-down views, spam signals — Phases 2–3.
- `authors` distinct-pubkey enumeration query — Phase 4 (BATCH-04); transport scaffold here serves it later.
- XSS hardening of event content/raw/tag values, 64-bit bounds math — Phases 2–3.
- Production/public deployment, gateway, TLS — out of v1 entirely.

</deferred>

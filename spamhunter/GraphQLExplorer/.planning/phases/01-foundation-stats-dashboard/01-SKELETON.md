# Walking Skeleton — GraphQL Explorer (Spam Investigation)

**Phase:** 1
**Generated:** 2026-06-24

## Capability Proven End-to-End

An analyst launches the tool via `vite dev` and watches live corpus stats (`eventCount`, `maxLevId`, `dbVersion`, `pinnedStrfryVersion`) update in the browser — proving the typed urql client connects **directly** to the LMDB2GraphQL lens over wildcard CORS (no proxy), through a hardened transport layer (error classifier + readiness gate), with a hidden-tab-aware poll and a "corpus changed" nudge.

## Architectural Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Framework | React 19 + Vite 7 + TypeScript 5.9 (`@vitejs/plugin-react@5`) | Pre-chosen SPA runtime; no SSR/RSC needed. Vite 7 is the conservative daily-driver (Vite 8 was one day old at planning); features used (TS+JSX, `import.meta.env`) are identical across 7/8. |
| Scaffold location | **In-place at the `GraphQLExplorer/` repo root** | This dir *is* the project per PROJECT.md monorepo placement. No nested `graphql-explorer/` subfolder — `package.json`, `src/`, `vite.config.ts`, `codegen.ts` live at root. |
| GraphQL client | `urql@5` + `@urql/core@6`, document cache only | Lightweight, perfect for read-only/polled; graphql-version-agnostic at runtime. No normalized graphcache. |
| Typed operations | `@graphql-codegen/cli@7` + `@graphql-codegen/client-preset@6` → `TypedDocumentNode` | Drift-free types from live introspection (Node, CORS-free). urql consumes `TypedDocumentNode` natively. NOT the legacy `typescript-urql` hooks plugin. |
| `graphql` runtime dep | **exact-pin `16.14.2`** + automated guard | `client-preset@6` peer caps `graphql` at `^16`; `graphql@17` silently breaks codegen. `scripts/check-graphql-pin.cjs` (postinstall) fails if resolved major ≥ 17; lockfile verified to resolve 16.x. |
| Connection model | **Direct, cross-origin, no proxy** | Lens serves wildcard CORS (`Allow-Origin: *`, contract § 3). A browser calls it directly; a Vite `server.proxy` is intentionally absent. |
| Base URL config | One module `src/transport/config.ts`: `import.meta.env.VITE_GRAPHQL_URL ?? 'http://127.0.0.1:8080/graphql'` | FND-02 invariant: never inline-hardcoded, never relative `/graphql`. `READY_URL`/`HEALTH_URL` derive from the same base. |
| Error handling | Single `classify(result)` boundary in `transport/errors.ts` → 7-kind discriminated union | GraphQL errors arrive on HTTP 200 (contract § 7); views never read `errors[]`. INTERNAL never leaks server internals. |
| Readiness | `transport/readiness.ts` `waitForReady()` polling `/ready`, 500ms→5s bounded backoff | `POST /graphql` returns 503 until startup gates pass (contract § 2); distinct "connecting…" state, never a generic error. |
| Pagination | `transport/paginate.ts` opaque-cursor accumulator — **scaffold only** | First exercised Phase 2 (`events`); reused Phase 4 (`authors`). Cursors opaque; `INVALID_CURSOR` restarts page 1. |
| Polling | `hooks/useStatsPoll.ts` `setTimeout`-reschedule + Page Visibility pause + `maxLevId`-diff nudge flag | No `setInterval` (avoids stacked polls); seconds-scale (5000ms tunable); nudge-only, never auto-refetch. |
| Auth / credentials | None — no credentials on any request | API unauthenticated; wildcard CORS won't honor credentials (contract § 3). |
| Styling | Hand-rolled plain CSS (tokens stylesheet + CSS Modules), dark single theme | shadcn explicitly declined; a four-scalar utility doesn't justify Radix+Tailwind. Mono-for-data / sans-for-chrome; teal accent reserved to the nudge + live-poll dot. |
| Routing | **None in Phase 1** (single stats view) | Routing arrives with the second view (Phase 2). No data-router/loader framework. |
| Test runner | `vitest` (introduced 01-02 for the pure classifier) | Pure functions (classifier, later analyzers) unit-tested with zero transport dependency. |
| Deployment | Local-dev only (`vite dev` / `vite preview`), loopback default | v1 is local-dev-only (contract § 10); the unauthenticated wildcard-CORS API must not be exposed beyond loopback without a gateway. |

## Directory Layout

```
GraphQLExplorer/                 # repo root = the project (in-place)
├── package.json, tsconfig*.json, vite.config.ts, codegen.ts, index.html
├── .env.example                 # documents VITE_GRAPHQL_URL
├── scripts/check-graphql-pin.cjs# postinstall guard: fail if graphql major >= 17
└── src/
    ├── main.tsx                 # React root + urql Provider
    ├── App.tsx                  # readiness gate → StatsDashboard
    ├── gql/                     # GENERATED (client-preset) — do not edit
    ├── transport/               # urql/HTTP only, no React UI
    │   ├── config.ts            # GRAPHQL_URL / READY_URL / HEALTH_URL
    │   ├── client.ts            # urql Client
    │   ├── errors.ts            # classify() → ApiError union
    │   ├── readiness.ts         # waitForReady()
    │   └── paginate.ts          # opaque-cursor accumulator (scaffold)
    ├── queries/stats.graphql.ts # StatsDocument
    ├── hooks/useStatsPoll.ts    # poll + visibility pause + nudge
    ├── views/StatsDashboard.tsx # 4 cards + state set + nudge + refresh
    └── styles/tokens.css        # dark-theme tokens + spacing scale
```

## Stack Touched in Phase 1

- [x] Project scaffold (React 19 + Vite 7 + TS, lint via tsc, vitest test runner)
- [x] Routing — single view, no router (deliberate; Phase 2 adds the second view)
- [x] Real backend read — the live `stats` query over direct cross-origin urql
- [x] UI wired to the API — StatsDashboard renders live scalars + Refresh CTA + nudge
- [x] Deployment — documented local full-stack run: `npm run dev` against the lens on `127.0.0.1:8080`

## Out of Scope (Deferred to Later Slices)

- npub/hex normalization (`nostr-tools`) — Phase 2 (ID-01/02)
- Cursor pagination *exercised* against a live query (`events`) — Phase 2; the accumulator is only scaffolded here
- Suspect entry, drill-down, timeline/burst, window-honesty indicator — Phase 2
- Near-dup detection, tag/mention fan-out, kind histogram, raw-JSON inspector — Phase 3
- Batch import, `latestPerAuthor` chunking, `authors` distinct-pubkey enumeration, triage table — Phase 4
- XSS hardening of event `content`/`raw`/tag values, 64-bit bounds math — Phases 2–3
- Router/multi-view navigation — Phase 2
- Production/public deployment, gateway, TLS, rate limiting, tightened CORS — out of v1 entirely (contract § 10)

## Subsequent Slice Plan

Each later phase adds one vertical slice on top of this skeleton without altering its architectural decisions (transport layout, error-classifier boundary, readiness gate, configurable base URL, opaque-cursor accumulator):

- **Phase 2 — Suspect Entry + Drill-Down Core:** paste an npub/hex suspect, normalize to hex (pure `identifier/` module), open an author drill-down with a newest-first timeline, asymmetric burst signal, and a non-removable window-honesty indicator. First *exercises* `transport/paginate.ts` for `events` cursor pagination; adds routing.
- **Phase 3 — Remaining Spam Signals:** duplicate-content (exact-hash → shingle/Jaccard ≈0.8), tag/mention fan-out, kind-distribution histogram, and a lazy raw-JSON inspector (escaped plaintext). Pure analyzers unit-tested with zero transport dependency.
- **Phase 4 — Batch Triage:** import a pubkey list/file (reusing Phase 2 identifier module) or enumerate the corpus via the `authors` query (reusing the scaffolded accumulator), chunk `latestPerAuthor` against the ≤1000-author cap and 256 KiB body limit, and triage many authors in one match-by-author table.

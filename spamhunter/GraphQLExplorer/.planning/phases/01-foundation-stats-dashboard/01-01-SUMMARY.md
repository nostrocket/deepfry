---
phase: 01-foundation-stats-dashboard
plan: 01
subsystem: graphql-explorer-frontend
tags: [scaffold, vite, react19, typescript, urql, graphql-codegen, transport, walking-skeleton]
requires: []
provides:
  - "GRAPHQL_URL / READY_URL / HEALTH_URL (src/transport/config.ts) — single configurable base-URL source"
  - "urql client (src/transport/client.ts) — direct cross-origin, no credentials"
  - "StatsDocument (src/queries/stats.graphql.ts) — codegen-typed"
  - "src/gql/ — generated typed graphql() fn + TypedDocumentNode"
  - "scripts/check-graphql-pin.cjs — graphql major-version tripwire (postinstall)"
  - "src/styles/tokens.css — UI-SPEC design tokens (consumed by 01-03)"
affects:
  - "Phases 2-4 inherit transport/config.ts base-URL pattern + codegen pipeline"
tech-stack:
  added:
    - "react@19, react-dom@19"
    - "urql@5, @urql/core@6"
    - "graphql@16.14.2 (exact pin)"
    - "vite@7, @vitejs/plugin-react@5, typescript@5.9"
    - "@graphql-codegen/cli@7, @graphql-codegen/client-preset@6"
  patterns:
    - "Single-source configurable base URL via VITE_GRAPHQL_URL (FND-02 invariant)"
    - "Direct cross-origin connection over wildcard CORS — no Vite proxy"
    - "Typed GraphQL via client-preset graphql() → TypedDocumentNode"
    - "Postinstall pin guard tripwire on graphql major version"
key-files:
  created:
    - package.json
    - package-lock.json
    - tsconfig.json
    - tsconfig.app.json
    - tsconfig.node.json
    - vite.config.ts
    - codegen.ts
    - schema.graphql
    - index.html
    - .gitignore
    - .env.example
    - scripts/check-graphql-pin.cjs
    - src/main.tsx
    - src/App.tsx
    - src/vite-env.d.ts
    - src/transport/config.ts
    - src/transport/client.ts
    - src/queries/stats.graphql.ts
    - src/gql/gql.ts
    - src/gql/graphql.ts
    - src/gql/index.ts
    - src/gql/fragment-masking.ts
    - src/styles/tokens.css
  modified: []
decisions:
  - "Codegen reads a checked-in SDL (schema.graphql) instead of live introspection: the lens enforces query-depth 12 (contract §12) which rejects codegen's deep introspection query. SDL transcribed verbatim from contract §4 — documented fallback per RESEARCH."
  - "useTypeImports enabled in codegen so generated src/gql/ satisfies the app's verbatimModuleSyntax:true tsconfig."
  - "No dotenv dependency added; codegen reads VITE_ knob from process.env (Vite loads .env for the runtime client)."
metrics:
  duration: ~25m
  completed: 2026-06-24
  tasks: 3
  files: 23
status: complete
---

# Phase 1 Plan 01: Foundation Walking Skeleton Summary

Scaffolded a React 19 + Vite 7 + TypeScript SPA in-place at the GraphQLExplorer repo root with a graphql@16.14.2 exact pin (guarded by a postinstall tripwire), a GraphQL-Codegen client-preset typed urql client whose base URL resolves from a single `VITE_GRAPHQL_URL` source, and proved a real cross-origin `stats` read against the live LMDB2GraphQL lens end-to-end — the thinnest browser→lens path (FND-01, FND-02).

## What Was Built

- **Task 1 — Scaffold + pin + guard** (`3228111`): `package.json` with conservative pins (react/react-dom@19, urql@5, @urql/core@6, graphql exact `16.14.2`, vite@7, plugin-react@5, ts@5.9, codegen cli@7/client-preset@6); npm scripts (dev, build, codegen, codegen:watch, postinstall); `scripts/check-graphql-pin.cjs` (fails when resolved graphql major > 16, graceful when graphql absent); `vite.config.ts` with plugin-react only and **no** server.proxy; tsconfig project refs; `index.html`; `src/main.tsx` (React 19 root); `src/styles/tokens.css` (UI-SPEC tokens); `.gitignore` (excludes `.env`, keeps `.env.example`); `.env.example`.
- **Task 2 — Typed client + codegen** (`e0ca341`): `src/transport/config.ts` (sole base-URL source — `GRAPHQL_URL` from `VITE_GRAPHQL_URL`, default loopback; READY/HEALTH derived); `src/transport/client.ts` (urql Client, no inline literal, no credentials); `codegen.ts` (client-preset, useTypeImports, SDL schema); `schema.graphql` (contract §4 verbatim); `src/queries/stats.graphql.ts` (StatsDocument, 4 StatsResult fields); generated `src/gql/`; `src/vite-env.d.ts`.
- **Task 3 — Live render** (`952df22`): `src/main.tsx` wraps `App` in urql `<Provider value={client}>`; `src/App.tsx` issues `useQuery(StatsDocument)`, inspects `fetching`/`error` before `data` (errors arrive on HTTP 200, contract §7), renders the four scalars as escaped plaintext (no `dangerouslySetInnerHTML`, T-01-01).

## Verification Results

- `node scripts/check-graphql-pin.cjs` → `PIN_OK: graphql 16.14.2 (major 16)`; `package-lock.json` resolves graphql to `16.14.2`.
- `npm run codegen` → produces `src/gql/gql.ts` + `src/gql/graphql.ts`; `StatsQuery` types `eventCount/maxLevId/dbVersion` as `number`, `pinnedStrfryVersion` as `string`.
- `npm run build` (`tsc -b && vite build`) exits 0, emits `dist/`.
- No inline base-URL literal outside `config.ts`; no relative `/graphql` URL; no Vite proxy; no credentials option.
- **Live end-to-end proof:** the exact Stats query returns real data from the live lens (`eventCount: 27114735`, `maxLevId: 47928105`, `dbVersion: 3`, real `pinnedStrfryVersion`); `vite dev` serves the wired app; the production build inlines the live `VITE_GRAPHQL_URL` from `.env`, confirming the env→config→client path resolves end-to-end.

## Live Backend Configuration

The lens in this environment runs at `http://192.168.149.21:8080/graphql` (loopback `127.0.0.1:8080` is occupied by Dgraph). Per the FND-02 invariant:
- The in-code DEFAULT base URL stays `http://127.0.0.1:8080/graphql` in `config.ts` (committed source never hardcodes the 192.168 address).
- A **gitignored** `.env` holds `VITE_GRAPHQL_URL=http://192.168.149.21:8080/graphql` for this environment; `.env.example` (committed) documents the loopback default.
- Vite loads `.env` for `dev`/`build`; the live URL is therefore only in the local `.env`, never in committed code.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Codegen could not introspect the live lens (query-depth limit)**
- **Found during:** Task 2
- **Issue:** `npm run codegen` against the live endpoint failed with `Failed to load schema … Query is nested too deep.` — the lens enforces a query-depth limit of 12 (contract §12), which rejects graphql-codegen's deep introspection query.
- **Fix:** Switched codegen `schema` to a checked-in SDL (`schema.graphql`) transcribed verbatim from contract §4. This is the documented fallback in RESEARCH § "Environment Availability". The runtime client still talks to the live lens directly; only type generation uses the SDL.
- **Files modified:** codegen.ts, schema.graphql (new)
- **Commit:** e0ca341

**2. [Rule 3 - Blocking] Generated src/gql/ violated verbatimModuleSyntax**
- **Found during:** Task 2
- **Issue:** `tsc -b` failed with TS1484 — generated files used value imports for types under the app's `verbatimModuleSyntax: true`.
- **Fix:** Added `config: { useTypeImports: true }` to the codegen client-preset so generated output emits `import type`.
- **Files modified:** codegen.ts
- **Commit:** e0ca341

**3. [Rule 3 - Blocking] Missing Vite client types**
- **Found during:** Task 2
- **Issue:** `import.meta.env` (TS2339) and `.css` module imports (TS2307) were untyped.
- **Fix:** Added `src/vite-env.d.ts` with `/// <reference types="vite/client" />`.
- **Files modified:** src/vite-env.d.ts (new)
- **Commit:** e0ca341

**4. [Rule 2 - Missing critical] Added tsconfig.app.json**
- **Found during:** Task 1
- **Issue:** The plan listed `tsconfig.node.json` but a standard Vite-React project also needs `tsconfig.app.json` for `tsc -b` (project references) to type-check `src/`.
- **Fix:** Authored `tsconfig.app.json` alongside the referenced `tsconfig.json`/`tsconfig.node.json`.
- **Files modified:** tsconfig.app.json (new)
- **Commit:** 3228111

**5. [Rule 3 - Blocking] Avoided unnecessary dotenv dependency**
- **Found during:** Task 2
- **Issue:** An initial codegen draft imported `dotenv` (not installed; package installs are excluded from auto-fix).
- **Fix:** Removed the dotenv path; codegen no longer needs a URL (it reads the SDL). Vite loads `.env` natively for the runtime client.
- **Files modified:** codegen.ts
- **Commit:** e0ca341

## Threat Surface

No new surface beyond the plan's `<threat_model>`. T-01-01 (escaped plaintext render — no `dangerouslySetInnerHTML`) and T-01-02 (loopback default, documented in `.env.example`) are honored. The live `.env` override to a LAN host (`192.168.149.21`) is consistent with T-01-02's documented behavior (operator-chosen base URL) and is gitignored.

## Known Stubs

None that prevent the plan's goal. `src/transport/{errors,readiness,paginate}.ts`, `src/hooks/useStatsPoll.ts`, and `src/views/StatsDashboard.tsx` from the RESEARCH structure are intentionally deferred to plans 01-02 (transport robustness) and 01-03 (full dashboard) — App.tsx renders a raw end-to-end proof per the plan's explicit scope.

## Self-Check: PASSED

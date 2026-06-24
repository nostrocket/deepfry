# Phase 1: Foundation + Stats Dashboard - Research

**Researched:** 2026-06-24
**Domain:** Walking-skeleton scaffold for a read-only GraphQL data-exploration SPA — typed urql client connecting **directly** to the lens over wildcard CORS (configurable base URL, no proxy), proven end-to-end by a polled corpus-stats dashboard (React 19 + Vite + TS + urql + GraphQL Codegen, over the LMDB2GraphQL lens)
**Confidence:** HIGH

## Summary

This phase is the **walking skeleton**: the thinnest end-to-end working slice that proves the typed transport works browser→lens. It scaffolds React 19 + Vite + TypeScript, wires GraphQL Codegen's `client-preset` against the live introspection endpoint, points the urql client **directly at the lens's GraphQL endpoint via a configurable base-URL env var** (default `http://127.0.0.1:8080/graphql`), builds the error-classifying/readiness-gating transport layer, and renders one real read — the corpus `stats` query — polled with a hidden-tab-aware interval and a `maxLevId`-diff "corpus changed" nudge. There are no spam signals, no identifier handling, and no batch logic in this phase; those are Phases 2–4.

**Contract revision (v1.2 reconciliation).** As of contract v1.1 the lens serves **wildcard CORS** (`Access-Control-Allow-Origin: *`; methods `GET/POST/OPTIONS`; header `Content-Type`; no credentials), so a browser calls the API cross-origin directly with **no proxy** (contract §3, Best Practice #1). The earlier "unconfigured CORS → Vite dev proxy" justification is gone; the user's locked decision is **connect directly**. This document therefore removes every proxy-centric element from the previous research (the Vite `server.proxy` hop, the relative-`/graphql` invariant, the "CORS surprises without the proxy" pitfall, the `vite.config.ts` proxy snippet) and replaces them with the direct-connection model: a **configurable base URL via an env var (`VITE_GRAPHQL_URL`, default `http://127.0.0.1:8080/graphql`)**, never hardcoded inline, never a relative proxied path. Codegen is unaffected — it always ran in **Node** against the live introspection URL, which never involved browser CORS. The v1.2 contract also adds a paginated `authors` distinct-pubkey query (contract §6.4); that is a **Phase 4** concern (BATCH-04), not Phase 1 — the generic transport layer built here will serve its cursor pagination later, identically to how it serves `events` pagination.

Everything else from the prior research stays valid and is preserved: the `graphql@16.14.2` exact pin (codegen `client-preset@6` peer cap), the React 19 + Vite + TS + urql + GraphQL Codegen stack and all verified version pins, the `errors[]`-on-200 / `extensions.code` classifier, the `/ready` 503-backoff gating, the `useStatsPoll` hidden-tab-aware polling with `maxLevId`-diff nudge, and the `StatsResult` fields.

**Primary recommendation:** Scaffold with **exact-pinned `graphql@16.14.2`** (codegen `client-preset@6` peers cap at `^16` — `graphql@17` breaks the typed-client toolchain), point the urql client at a **configurable base URL from an env var (`import.meta.env.VITE_GRAPHQL_URL`, default `http://127.0.0.1:8080/graphql`)** — direct connection over wildcard CORS, **no proxy, no relative path** — and add a **single error-classifier boundary** that branches on `result.error`/`extensions.code` before any view reads `data`. Build the transport layer (01-02) generically enough that Phases 2–4 (cursor pagination, batch chunking, the `authors` enumeration) inherit it, but only exercise the `stats` query in this phase.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| FND-01 | App scaffolded (React 19 + Vite + TS) with `graphql` pinned to v16 and a typed client generated from live `/graphql` introspection (GraphQL Codegen + urql) | Standard Stack (verified versions + pin rationale), Code Examples §codegen.ts + §typed urql client, Pitfall 11 (graphql@16 pin) |
| FND-02 | urql client connects **directly** to the lens at a **configurable base URL** (env var, default `http://127.0.0.1:8080/graphql`); the lens serves wildcard CORS (`Allow-Origin: *`, contract v1.1) so a browser calls it cross-origin with **no proxy**; the base URL is **never hardcoded inline** in client code | Code Examples §client.ts (env-var base URL), Architecture Pattern 1 (direct-connection client), Pitfall 4 (hardcoded base URL is the new smell), contract §3 (wildcard CORS) |
| FND-03 | Transport robust — `errors[]` inspected on every 200; `/ready` gating with 503 backoff; explicit `limit` on every query; opaque cursors; `INVALID_CURSOR` restarts pagination | Code Examples §error classifier + §readiness gate, Architecture Patterns 2/3, Pitfalls 1/8/7. *Cursor accumulator is scaffolded this phase; first exercised in Phase 2.* |
| STATS-01 | Dashboard shows `eventCount`, `maxLevId`, `dbVersion`, `pinnedStrfryVersion` | contract.md §5 `StatsResult`, Code Examples §stats query (codegen-typed) |
| STATS-02 | Dashboard polls `maxLevId` on a sensible interval (seconds, pause on hidden tab), signals corpus change, no aggressive auto-refetch | Code Examples §useStatsPoll, Architecture Pattern 4 (poll-and-diff), Pitfall (aggressive polling), Page Visibility API |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Cross-origin connection | Browser/client (urql) | — | Backend serves **wildcard CORS** (contract §3); the browser calls the lens **directly cross-origin** with no proxy. The client reads its target from a configurable env var (`VITE_GRAPHQL_URL`) rather than a hardcoded inline URL or a relative proxied path. |
| GraphQL transport / error classification | Browser/client (urql) | — | urql Client runs in the browser; `errors[]`-on-200 and `extensions.code` mapping happen client-side. There is no BFF/proxy tier in v1 (scoped out — wildcard CORS makes one unnecessary). |
| Type generation | Build tooling (Node, codegen) | — | Codegen runs in **Node** against the live introspection URL — no browser, never any CORS concern (CORS is a browser-only policy; contract §3). Output is a build artifact consumed by the browser tier. Unchanged by the CORS revision. |
| Readiness gating (`/ready`, 503 backoff) | Browser/client (transport layer) | — | The client decides when to issue queries, polling `GET /ready` **directly** at the lens (same base host as `/graphql`). No proxy forwarding involved. |
| Stats polling + change detection | Browser/client (React hook) | — | Page Visibility API + `setTimeout` reschedule live in the browser; the lens has no push/subscription tier (contract §1). |
| Data persistence / corpus | Backend (LMDB2GraphQL over strfry LMDB) | — | Read-only; this phase never writes. The frontend owns no persistence beyond ephemeral React state. |

## Standard Stack

> All versions **re-verified against the live npm registry on 2026-06-24** (prior session, preserved). `graphql@16.14.2` confirmed present; `@graphql-codegen/client-preset@6.0.1` peer range for `graphql` confirmed to cap at `^16.0.0` (no 17). The CORS/proxy revision does **not** touch any package choice or pin.

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `react` + `react-dom` | `19.2.7` | UI runtime | Pre-chosen; current stable SPA runtime, no SSR/RSC needed. `[VERIFIED: npm registry]` |
| `vite` | `8.1.0` (or pin `^7` for conservative) | Dev server + build | Vite is the dev server / bundler. With direct connection there is **no proxy config** to depend on — `server.proxy` is no longer used. The features this phase needs (TS+JSX, env vars via `import.meta.env`) are identical across 7 and 8. `[VERIFIED: npm registry]` |
| `typescript` | `5.9.x` (avoid 6.0) | Types | TS 6.0.3 is live but a brand-new major; greenfield app's value is reliable generated types — don't pioneer the compiler. `[VERIFIED: npm registry — 6.0.3 latest, 5.9 recommended]` |
| `urql` | `5.0.3` | GraphQL client (React bindings) | The `urql` package *is* the React binding; lightweight document cache, perfect for read-only/polled. graphql-version-agnostic (bundles `@0no-co/graphql.web`). `[VERIFIED: npm registry]` |
| `@urql/core` | `6.0.3` | urql client core | Pulled transitively by `urql@5` (`@urql/core@^6`); pin explicitly. No `graphql` peer → why the 16-vs-17 issue is codegen-only. `[VERIFIED: npm registry]` |
| `graphql` | **`16.14.2`** (NOT 17) | GraphQL spec runtime (codegen dependency) | **Exact-pin 16.** `client-preset@6.0.1` peer caps `graphql` at `^16.0.0`; `graphql@17.0.1` (latest) breaks codegen. urql doesn't need it at runtime. `[VERIFIED: npm registry — peer range confirmed prior session]` |

### Supporting (codegen + build)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `@graphql-codegen/cli` | `7.1.3` | Runs codegen | Always (FND-01). Point `schema` at the live introspection URL (Node fetch — CORS-free). `[VERIFIED: npm registry]` |
| `@graphql-codegen/client-preset` | `6.0.1` | Typed `graphql()` document fn + fragment masking → `TypedDocumentNode` | The modern preset — NOT the legacy `typescript-urql` hooks plugin. urql consumes `TypedDocumentNode` natively. `[VERIFIED: npm registry]` |
| `@vitejs/plugin-react` | `6.0.3` (Vite 8) **or** `5.2.0` (Vite 7) | React Fast Refresh + JSX transform | Required. v6↔Vite 8, v5↔Vite 7. React Compiler/babel peers are **optional** — this app does not use them. `[VERIFIED: npm registry]` |
| `@parcel/watcher` | `2.5.6` | Native watcher for `codegen --watch` | **Optional** — only if you run codegen in watch mode. Skip for one-shot `npm run codegen`. `[CITED: codegen CLI peer]` |
| `@types/react` + `@types/react-dom` | `@19` | React 19 type defs | Dev dependency for TS. `[ASSUMED]` (standard pairing; verify exact at install) |

### Not in this phase (deferred to later phases)
- `nostr-tools@2.23.8` — npub/hex normalization is **Phase 2** (ID-01/02). Do NOT install or wire it in Phase 1.
- Hand-rolled CSS/SVG bars, near-dup detection, `leven` — **Phases 2–3**. The stats dashboard needs no charts (it renders four scalar values).
- `react-router` (or hand-rolled switch) — routing arrives with the second view (Phase 2). Phase 1 can be a single stats view; if the planner wants the router skeleton in place now, a tiny path switch suffices — do NOT pull a data-router/loader framework.
- The `authors` distinct-pubkey query (contract §6.4) — **Phase 4** (BATCH-04). Not in Phase 1 scope. The generic cursor-paginating transport built here will serve it later, the same way it serves `events` pagination.

### Alternatives Considered (settled by STACK.md — do not relitigate)
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `urql` | Apollo Client `@apollo/client@4` | ~3–4x bundle; normalized cache unused in a read-only polled app. Pick only if team standardizes on it. |
| `urql` | `graphql-request` + manual state | No React integration / caching / dedup; you'd rebuild urql badly. Keep only for any Node-side CLI. |
| `client-preset` | legacy `typescript-urql` hooks plugin | Superseded; The Guild steers to client-preset. |
| `graphql@16` | `graphql@17` | Codegen + transport peers don't support 17 (2026-06-24). Revisit later. |
| Vite 8 | Vite 7 + plugin-react 5 | Vite 8.1 is one day old (2026-06-23); Vite 7 is the safe daily-driver pin. Either works — and with direct connection there is no proxy feature to differ on. **Lean Vite 7 if in doubt.** |

**Installation:**
```bash
# Scaffold (greenfield)
npm create vite@latest graphql-explorer -- --template react-ts
cd graphql-explorer

# Core runtime — note the EXACT graphql pin
npm install react@19 react-dom@19 urql@5 @urql/core@6 graphql@16.14.2

# Dev dependencies (build + codegen + types)
npm install -D \
  vite@8 @vitejs/plugin-react@6 typescript@5.9 \
  @types/react@19 @types/react-dom@19 \
  @graphql-codegen/cli@7 @graphql-codegen/client-preset@6
```
> Conservative Vite-7 path: `npm install -D vite@7 @vitejs/plugin-react@5`.
> **After install, verify `package-lock.json` resolves `graphql` to 16.x** — this is a phase verification gate (success criterion 2).

## Package Legitimacy Audit

> Run via `gsd-tools query package-legitimacy check --ecosystem npm …` (executed prior session; unchanged by the CORS revision — no packages added or removed). The `--verified` confidence classifier returns MEDIUM for websearch-sourced facts; these packages were sourced from `STACK.md` (npm-registry-verified) AND confirmed against first-party repos, so they are tagged `[VERIFIED: npm registry]`.

| Package | Registry | Age signal | Downloads | Source Repo | Seam Verdict | Disposition |
|---------|----------|-----------|-----------|-------------|--------------|-------------|
| `react` | npm | latest patch 2026-06-01 | 151.9M/wk | github.com/facebook/react | SUS (too-new) | **Approved** — false positive |
| `react-dom` | npm | 2026-06-01 | 142.7M/wk | github.com/facebook/react | SUS (too-new) | **Approved** — false positive |
| `urql` | npm | 2026-06-15 | 999K/wk | github.com/urql-graphql/urql | SUS (too-new) | **Approved** — false positive |
| `@urql/core` | npm | 2026-06-22 | 6.3M/wk | github.com/urql-graphql/urql | SUS (too-new) | **Approved** — false positive |
| `graphql` | npm | 2026-06-16 | 41.3M/wk | github.com/graphql/graphql-js | SUS (too-new) | **Approved** — false positive (pin 16.14.2) |
| `nostr-tools` | npm | 2026-06-23 | 691K/wk | github.com/nbd-wtf/nostr-tools | SUS (too-new) | Deferred to Phase 2 (not installed now) |
| `vite` | npm | 2026-06-23 | 143.0M/wk | github.com/vitejs/vite | SUS (too-new) | **Approved** — false positive |
| `@vitejs/plugin-react` | npm | 2026-06-23 | 65.4M/wk | github.com/vitejs/vite-plugin-react | SUS (too-new) | **Approved** — false positive |
| `typescript` | npm | 2026-04-16 | 219.5M/wk | github.com/microsoft/TypeScript | OK | Approved |
| `@graphql-codegen/cli` | npm | 2026-06-16 | 6.9M/wk | github.com/dotansimha/graphql-code-generator | SUS (too-new) | **Approved** — false positive |
| `@graphql-codegen/client-preset` | npm | 2026-05-27 | 6.1M/wk | github.com/dotansimha/graphql-code-generator | SUS (too-new) | **Approved** — false positive |

**Packages removed due to SLOP verdict:** none.

**On the SUS verdicts — explicit reasoning for the planner:** every SUS flag is driven *solely* by the `too-new` reason, which reflects the **most-recent-patch publish date** (all these packages shipped routine patch releases in mid-June 2026), NOT the package's actual age. These are all multi-year-old, canonical, first-party packages with download counts from 691K/wk to 219M/wk and their official source repositories. There are **no slopsquat signals** (no typo'd names, no missing repos, no zero-download newcomers, no postinstall scripts — confirmed `null` postinstall on all of them). **The planner does NOT need `checkpoint:human-verify` tasks for these installs.** The `too-new` heuristic is a publish-date artifact here, documented and dispositioned.

## Architecture Patterns

### System Architecture Diagram

```
                         BROWSER (any origin — calls the lens directly, cross-origin)
  ┌──────────────────────────────────────────────────────────────────────┐
  │  StatsDashboard view ── renders 4 scalars + "corpus changed" nudge     │
  │        ▲                                                               │
  │        │ { stats, hasNewData, status }                                 │
  │  useStatsPoll ── setTimeout(seconds) + Page Visibility pause           │
  │        │  maxLevId diff → hasNewData (NO auto-refetch of any window)   │
  │        ▼                                                               │
  │  urql Client (url: import.meta.env.VITE_GRAPHQL_URL                    │
  │               ?? 'http://127.0.0.1:8080/graphql')                      │
  │    ├─ readiness gate: poll GET <base>/ready → 200 before first query   │
  │    ├─ error classifier: errors[] on 200 + extensions.code             │
  │    │     → { INVALID_CURSOR | TOO_MANY_AUTHORS | VALIDATION | INTERNAL │
  │    │         | NOT_READY(503) | PAYLOAD_TOO_LARGE(413) | OK }           │
  │    └─ typed StatsDocument (codegen TypedDocumentNode)                  │
  └───────────────┬──────────────────────────────────────────────────────┘
                  │ direct cross-origin POST /graphql, GET /ready, GET /health
                  │ (wildcard CORS: Allow-Origin *, no credentials — contract §3)
                  ▼
          LMDB2GraphQL  127.0.0.1:8080  (read-only; 503 until ready;
                                          OPTIONS preflight answered pre-readiness)
```

Data flow for the one real path this phase proves: app boot → readiness gate polls `<base>/ready` directly → on `200`, urql issues the typed `stats` query directly to the configured `VITE_GRAPHQL_URL` (cross-origin; the browser's preflight is answered by the lens's outermost `CorsLayer`, even while the schema is still warming) → response classified (`errors[]` checked before `data`) → `useStatsPoll` stores `maxLevId`, sets `hasNewData` when it increases on a later tick → dashboard renders four scalars + a non-intrusive nudge.

> **No proxy hop.** There is no Vite dev-server reverse-proxy in this architecture. The browser talks to the lens directly; CORS is handled by the lens's wildcard policy (contract §3). `vite dev` is still how the app is *served*, but it no longer brokers API traffic — which also means `vite preview` / static hosting work fine for the browser (there is no proxy dependency), though v1 remains local-dev-only per contract §10.

### Recommended Project Structure (Phase 1 subset)
```
src/
├── main.tsx                  # mount; urql Provider; readiness shell
├── App.tsx                   # readiness gate ("connecting to relay…") + StatsDashboard
│
├── gql/                      # GENERATED (codegen client-preset) — do not edit
│   ├── graphql.ts
│   └── gql.ts
│
├── transport/                # urql + HTTP concerns ONLY (no React UI)
│   ├── config.ts             # GRAPHQL_URL from import.meta.env.VITE_GRAPHQL_URL (default 127.0.0.1:8080/graphql); derives readyUrl/healthUrl from the same base
│   ├── client.ts             # urql Client, url: GRAPHQL_URL, cacheExchange + fetchExchange
│   ├── errors.ts             # classify(result) → discriminated union (extensions.code + HTTP status)
│   ├── readiness.ts          # poll <base>/ready, gate first query, 503 → retry-with-backoff
│   └── paginate.ts           # opaque-cursor accumulation loop — SCAFFOLD ONLY this phase (Phase 2 uses it; Phase 4 'authors' reuses it)
│
├── queries/
│   └── stats.graphql.ts      # graphql`query Stats { stats { eventCount maxLevId dbVersion pinnedStrfryVersion } }`
│
├── hooks/
│   └── useStatsPoll.ts       # interval poll + Page Visibility pause + maxLevId-diff nudge
│
└── views/
    └── StatsDashboard.tsx    # 4 scalars + connection state + "corpus changed" nudge
```
> `analyzers/`, `identifier/`, `views/signals/`, batch/drill-down hooks appear in later phases. Keep `transport/` generic now so Phases 2–4 inherit the classifier, readiness gate, and cursor loop without rework. Centralizing the base URL in `transport/config.ts` is what keeps it out of inline code (FND-02 invariant).

### Pattern 1: Direct-connection client with a configurable base URL (FND-02)
**What:** The urql `url` comes from a **single configurable env var** — `import.meta.env.VITE_GRAPHQL_URL`, defaulting to `http://127.0.0.1:8080/graphql`. The browser calls the lens **directly, cross-origin**; the lens's wildcard CORS (`Access-Control-Allow-Origin: *`, contract §3) makes this work with **no proxy**. The base URL is resolved in exactly one module (`transport/config.ts`) and never written inline at a call site or hardcoded as a literal in `client.ts`.
**When to use:** Always. This is an invariant — a hardcoded inline base URL (instead of the env var) is the code-review red flag (Pitfall 4). A relative `/graphql` path is **wrong** in this architecture: there is no proxy to resolve it against.
```ts
// transport/config.ts — Source: contract §3 (wildcard CORS) + FND-02
// Single source of truth for where the lens lives. Configurable, never inline-hardcoded.
const DEFAULT_GRAPHQL_URL = 'http://127.0.0.1:8080/graphql'
export const GRAPHQL_URL: string = import.meta.env.VITE_GRAPHQL_URL ?? DEFAULT_GRAPHQL_URL
// /ready and /health live on the same origin/base as /graphql:
const base = new URL(GRAPHQL_URL)
export const READY_URL = new URL('/ready', base).toString()
export const HEALTH_URL = new URL('/health', base).toString()
```
```ts
// transport/client.ts — Source: contract §3; urql core docs
import { Client, cacheExchange, fetchExchange } from '@urql/core'
import { GRAPHQL_URL } from './config'
export const client = new Client({
  url: GRAPHQL_URL,                      // direct, cross-origin → wildcard CORS, no proxy
  exchanges: [cacheExchange, fetchExchange],
  // No credentials: the API is unauthenticated and sets no cookies; wildcard CORS won't honor them anyway (contract §3).
})
```
> **No `vite.config.ts` proxy block.** Earlier research configured `server.proxy` to forward `/graphql`, `/ready`, `/health` to `:8080`. With wildcard CORS that is unnecessary and is intentionally **removed** — `vite.config.ts` only needs the React plugin. Provide a `.env` / `.env.example` documenting `VITE_GRAPHQL_URL` for deployments that point at a different host.

### Pattern 2: Error classifier as a single transport boundary (FND-03)
**What:** One `classify(result)` turns the contract's three failure shapes — transport status (`503`/`413`), GraphQL `errors[]` with `extensions.code` (`INVALID_CURSOR`/`TOO_MANY_AUTHORS`), and code-less validation/`internal error` — into one discriminated union the UI branches on. **Every query passes through it; views never read `errors[]` directly.** In Phase 1 only `stats` flows through, but build it complete so Phases 2–4 inherit it.
**Why:** GraphQL errors arrive on **HTTP 200** (contract §7) — `res.ok`/`status===200` is NOT success. Always branch on `result.error` before reading `result.data` (Pitfall 1). Each `extensions.code` must map to a **distinct, non-blank** UI state (success criterion 5). Because CORS is wildcard, the lens's error responses (200/413/503) **all carry the CORS headers** (contract §3) — so the browser surfaces the real status/body instead of an opaque CORS failure, and the classifier sees true statuses rather than masked network errors.

### Pattern 3: Readiness gate with 503 backoff (FND-03, success criterion 4)
**What:** Before the first query, poll `GET <base>/ready` (derived from `VITE_GRAPHQL_URL`) until `200`; show a distinct **"connecting to relay…"** state (not a generic error) while it returns `503`. Treat any `503` from `/graphql` as "not ready, retry with bounded backoff." Don't poll `/ready` aggressively — a few seconds of backoff is fine.
**Why:** `POST /graphql` returns `503` until startup gates pass (LMDB open, `dbVersion==3`, endianness, comparator self-check — contract §2). Lumping `503` into "API error" makes a healthy-but-warming backend look broken (Pitfall 8). Note the lens answers the CORS **OPTIONS preflight before the readiness gate** (contract §3), so cross-origin preflight succeeds even during warm-up — only the actual `POST` returns `503`.

### Pattern 4: Poll-and-diff for "corpus changed" (STATS-02)
**What:** `useStatsPoll` queries `stats` on a **seconds-scale** interval, keeps the last `maxLevId`, and flips a `hasNewData` flag when it increases. The dashboard shows a **non-intrusive nudge** — it does **NOT** auto-refetch any investigation window (in this phase there is none, but the discipline is structural: poll updates a flag, the analyst decides). **Pause polling when the tab is hidden** via the Page Visibility API (`document.visibilityState` / `visibilitychange`).
**Why:** No subscriptions/push (contract §1); polling is the only change-detection mechanism. Sub-second intervals are abusive; aggressive auto-refetch "moves the ground" under an investigation (anti-pattern). See Code Examples for the hidden-tab implementation.

### Anti-Patterns to Avoid
- **Hardcoding the base URL inline** (`new Client({ url: 'http://127.0.0.1:8080/graphql' })` at a call site) — use the single `VITE_GRAPHQL_URL`-backed constant in `transport/config.ts` so the target is configurable. (Pitfall 4)
- **Using a relative `/graphql` path** — there is no proxy in this architecture; a relative path resolves against the Vite dev origin and won't reach the lens. Use the absolute base URL from the env var. (Pitfall 4)
- **Re-introducing a Vite `server.proxy`** for the API — unnecessary with wildcard CORS; it only adds a hop and a `vite preview`/static-host footgun. (Pitfall 4)
- **Sending credentials** (`fetchOptions: { credentials: 'include' }`) — the API is unauthenticated and wildcard CORS won't honor credentialed requests (contract §3). Don't.
- **Trusting HTTP 200 as success** — read `result.error`/`errors[]` first. (Pitfall 1)
- **`graphql@17` / `^`/`*` ranges on `graphql`** — exact-pin `16.14.2`. (Pitfall 11)
- **Aggressive polling / auto-refetch on `maxLevId` change** — nudge only; pause on hidden tab. (anti-pattern, Pitfall: aggressive polling)
- **A query without an explicit `limit`** — every query passes a `limit` (success criterion 5). `stats` takes no `limit`, but the *convention* is established now for the queries Phases 2–4 add (including the Phase-4 `authors` query); encode "explicit limit" as a transport/lint rule, not an afterthought.
- **Normalizing pages into urql's `graphcache`** — not needed; default document cache only. (anti-pattern; relevant once pagination lands in Phase 2)

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Typed GraphQL operations | Manual TS interfaces mirroring the schema | `@graphql-codegen/client-preset` `graphql()` → `TypedDocumentNode` | Drift-free types straight from live introspection; urql consumes `TypedDocumentNode` natively. Hand-typing re-introduces the bugs codegen exists to kill. |
| GraphQL transport / caching / in-flight dedup | Hand-rolled `fetch` wrapper | `urql` (`cacheExchange` + `fetchExchange`) | A `fetch` helper re-implements dedup, caching, and (critically) the `errors[]`-on-200 branching at every call site → Pitfall 1 everywhere. |
| Cross-origin connectivity | Custom CORS shim, a Vite/BFF proxy, or backend CORS-config request | **Direct connection** — point urql at the configurable base URL | The lens already serves **wildcard CORS** (contract §3); the browser calls it directly with zero extra infrastructure. A proxy is pure overhead here and breaks static hosting / `vite preview`. |
| Base-URL management | Inline string literals scattered across call sites | One `VITE_GRAPHQL_URL`-backed constant in `transport/config.ts` | Centralized + env-configurable; satisfies the FND-02 "never hardcoded inline" invariant and lets the same build target a non-default host. |
| Tab-visibility detection | Custom focus/blur tracking, timers that guess | Page Visibility API (`document.visibilityState`, `visibilitychange`) | Standard, accurate (covers tab switch, minimize, OS sleep), and the documented way to pause polling. |
| GraphQL error taxonomy | Per-call-site string matching on messages | One `classify()` discriminated union keyed on `extensions.code` + HTTP status | The contract's error semantics belong in exactly one place; scattering them guarantees inconsistent UI states. |

**Key insight:** This phase's whole value is *typed transport proven end-to-end*. Hand-rolling any of transport/types/connection reintroduces precisely the silent-failure traps (errors-on-200, type drift) that the chosen stack was selected to eliminate — and with wildcard CORS, the *simplest* connection (direct, no proxy) is also the correct one, so adding a proxy is hand-rolling complexity the backend already removed.

## Common Pitfalls

> Condensed from `PITFALLS.md` and reconciled to contract v1.2; only the pitfalls in Phase 1's scope (the "Phase 0/A/E" set) are reproduced. Phases 2–4 own the rest (window-honesty, timestamps, hex/npub, batch chunking, near-dup, `authors` enumeration).
>
> **Note for the planner:** `PITFALLS.md` (project corpus) still describes the **pre-v1.1 proxy/CORS model** (relative-URL invariant, "CORS surprises without the proxy"). That guidance is **stale** — the v1.2 `contract.md` (wildcard CORS, §3) is the source of truth. Do not propagate the proxy pitfall into plans; it is replaced by Pitfall 4 below.

### Pitfall 1: GraphQL errors on HTTP 200 ("happy 200")
**What goes wrong:** `200 OK` ships with `errors[]` and possibly `data: null`; code that checks `res.ok` renders blank panels or crashes on `data.stats`.
**How to avoid:** Branch on `result.error` before reading `result.data`; centralize in one classifier; map each `extensions.code` to a distinct non-blank state.
**Warning signs:** Blank dashboard with no error shown; `TypeError: cannot read 'stats' of null`; network tab shows 200 but UI is empty.

### Pitfall 4: Hardcoded base URL / re-introducing a proxy (the new connection smell)
**What goes wrong:** With wildcard CORS the connection is trivially direct — but two mistakes recur: (a) **hardcoding the base URL inline** as a string literal instead of reading the configurable `VITE_GRAPHQL_URL` env var, so the app can't be pointed at another host without a code edit; and (b) **re-adding a Vite proxy or a relative `/graphql` path** out of habit from the old CORS workaround, which adds a needless hop and silently breaks the moment the bundle is served outside `vite dev` (`vite preview`, static host). Because CORS is wildcard, none of that plumbing is needed.
**How to avoid:** Resolve the base URL in exactly one place (`transport/config.ts`) from `import.meta.env.VITE_GRAPHQL_URL` with a sane default; pass an **absolute** URL to urql; **do not** configure `server.proxy`; document `VITE_GRAPHQL_URL` in `.env.example`. Code review flags any inline `http://…/graphql` literal or any relative `/graphql` client URL.
**Warning signs:** A literal API URL appears in `client.ts` or a component; a `server.proxy` block reappears in `vite.config.ts`; the app works under `vite dev` but breaks under `vite preview` (the tell-tale sign someone re-added a proxy dependency). Note: since there's no proxy dependency now, `vite preview`/static hosting works fine for the browser — a `preview` break means proxy plumbing crept back in. (v1 remains local-dev-only per contract §10 regardless.)

### Pitfall 8: 503 startup-readiness ignored
**What goes wrong:** App blasts queries on boot, gets `503`, shows "API error" on cold start; analyst thinks the tool is broken.
**How to avoid:** Gate first queries on `GET <base>/ready` → `200`; distinct "connecting to relay…" state; bounded retry-with-backoff; don't poll `/ready` aggressively.
**Warning signs:** "API error" on cold start that resolves on refresh; intermittent failures only when frontend+backend start together.

### Pitfall 11: graphql@17 instead of @16 (codegen breakage)
**What goes wrong:** `npm install graphql` grabs 17 (latest); `client-preset@6` peers cap at `^16` → peer warnings + subtle/empty codegen output. urql still works at runtime, masking it as a "codegen-only mystery."
**How to avoid:** Exact-pin `"graphql": "16.14.2"`. Optionally add a `postinstall`/CI guard that fails if resolved `graphql` ≥ 17. Verify `package-lock.json` resolves 16.x and codegen produces typed output.
**Warning signs:** peer-dependency warnings mentioning `graphql`; empty/incorrect `src/gql/`; lockfile shows `graphql` 17.x.

### Pitfall (polling): aggressive `stats` polling / auto-refetch
**What goes wrong:** Sub-second polling, or auto-refetching on `maxLevId` change, wastes load and (later) moves an investigation's ground.
**How to avoid:** Seconds-scale interval; pause on hidden tab (Page Visibility); `maxLevId` diff updates a *nudge flag only*, never an auto-refetch.
**Warning signs:** needless network chatter; UI that re-pulls data the analyst didn't ask to refresh.

### Pitfall 7 (partial — clamp/`413`/`limit` awareness): scaffold-only this phase
**What goes wrong:** `events.limit`/`perAuthor`/`authors.limit` silently clamp to `[1,500]`; oversized bodies → `413` (a transport status, not an `errors[]` entry). These bite in Phases 2/4, but the **"explicit `limit` on every query"** rule and `413`-as-HTTP-status handling are established in the transport layer now (FND-03, success criterion 5).
**How to avoid:** Encode "explicit limit + treat 500 as a hard ceiling, drive completeness via pagination" as a transport convention; handle `413` in the HTTP-status branch alongside `503`. (Both statuses carry CORS headers under the wildcard policy, so the browser sees them as real statuses — contract §3.)

## Runtime State Inventory

> Greenfield phase — no rename/refactor/migration. **Omitted (not applicable).** There is no existing code, stored data, service config, secret, or build artifact to inventory; this phase creates the project from scratch inside `spamhunter/GraphQLExplorer/`.

## Code Examples

> Verified patterns condensed from `STACK.md`/`ARCHITECTURE.md` (both grounded in the-guild.dev codegen docs and the code-verified `contract.md`) and reconciled to contract v1.2 (direct connection). Treat these as the concrete recipes for the three plans.

### codegen.ts (introspection → typed client) — FND-01
```ts
// Source: STACK.md (the-guild.dev client-preset guide). Codegen runs in Node → no browser, no CORS, ever.
import type { CodegenConfig } from '@graphql-codegen/cli'

const config: CodegenConfig = {
  schema: 'http://127.0.0.1:8080/graphql', // Node fetch → CORS-free; needs API running. (Could read process.env to match VITE_GRAPHQL_URL.)
  documents: ['src/**/*.{ts,tsx}'],
  ignoreNoDocuments: true,
  generates: {
    './src/gql/': { preset: 'client' },    // typed graphql() + TypedDocumentNode
  },
}
export default config
```
```json
// package.json scripts
{ "scripts": {
    "codegen": "graphql-codegen --config codegen.ts",
    "codegen:watch": "graphql-codegen --config codegen.ts --watch"
} }
```
> **Codegen is unchanged by the CORS revision.** It always ran server-side in Node against the live introspection URL — browser CORS never applied to it. (Optionally point `schema` at the same value as `VITE_GRAPHQL_URL` via `process.env` so codegen and the runtime client share one source of truth.)
> **64-bit `Int` note (contract §8):** `kind`/`createdAt` are 64-bit-on-wire typed as `Int` → codegen emits `number`. `stats` fields (`eventCount`, `maxLevId`) are also `Int`→`number`. Fine for v1; the bigint escape hatch (`config.scalars`) is a documented Phase-2/3 concern, not Phase 1. `maxLevId` stays well within safe-integer range for change detection.

### Direct urql client + configurable base URL — FND-02
```ts
// transport/config.ts — Source: contract §3 (wildcard CORS); FND-02
const DEFAULT_GRAPHQL_URL = 'http://127.0.0.1:8080/graphql'
export const GRAPHQL_URL: string = import.meta.env.VITE_GRAPHQL_URL ?? DEFAULT_GRAPHQL_URL
const base = new URL(GRAPHQL_URL)
export const READY_URL = new URL('/ready', base).toString()
export const HEALTH_URL = new URL('/health', base).toString()
```
```ts
// transport/client.ts — Source: contract §3; urql core docs
import { Client, cacheExchange, fetchExchange } from '@urql/core'
import { GRAPHQL_URL } from './config'
export const client = new Client({
  url: GRAPHQL_URL,                  // absolute, configurable, direct cross-origin
  exchanges: [cacheExchange, fetchExchange],
})
```
```bash
# .env.example — document the one knob
VITE_GRAPHQL_URL=http://127.0.0.1:8080/graphql
```
> No `vite.config.ts` proxy. `vite.config.ts` only registers `@vitejs/plugin-react`.

### Typed stats query + usage — STATS-01 / FND-01
```ts
// queries/stats.graphql.ts — Source: contract §6.3 + STACK.md
import { graphql } from '../gql'
export const StatsDocument = graphql(`
  query Stats { stats { eventCount maxLevId dbVersion pinnedStrfryVersion } }
`)
// urql: useQuery({ query: StatsDocument }) → data.stats.maxLevId is typed `number`
```

### Error classifier (discriminated union) — FND-03
```ts
// transport/errors.ts — Source: contract §7 (error table); PITFALLS Pitfall 1
import type { OperationResult } from '@urql/core'

export type ApiError =
  | { kind: 'INVALID_CURSOR' }       // extensions.code — drop cursor, restart page 1 (Phase 2; Phase 4 authors)
  | { kind: 'TOO_MANY_AUTHORS' }     // extensions.code — chunk authors (Phase 4)
  | { kind: 'VALIDATION'; message: string }   // code-less validation message
  | { kind: 'INTERNAL' }             // code-less "internal error" — generic + backoff
  | { kind: 'NOT_READY' }            // HTTP 503
  | { kind: 'PAYLOAD_TOO_LARGE' }    // HTTP 413
  | { kind: 'NETWORK' }              // fetch failed (genuine transport failure; not a CORS artifact — CORS is wildcard)

// Inspect networkError (HTTP status) AND graphQLErrors[].extensions.code BEFORE reading data.
export function classify(result: OperationResult): ApiError | null {
  const ne = result.error?.networkError as (Error & { response?: Response }) | undefined
  const status = ne?.response?.status
  if (status === 503) return { kind: 'NOT_READY' }
  if (status === 413) return { kind: 'PAYLOAD_TOO_LARGE' }
  if (ne && status === undefined) return { kind: 'NETWORK' }
  const gqlErr = result.error?.graphQLErrors?.[0]
  if (gqlErr) {
    const code = gqlErr.extensions?.code as string | undefined
    if (code === 'INVALID_CURSOR') return { kind: 'INVALID_CURSOR' }
    if (code === 'TOO_MANY_AUTHORS') return { kind: 'TOO_MANY_AUTHORS' }
    if (/internal error/i.test(gqlErr.message)) return { kind: 'INTERNAL' }
    return { kind: 'VALIDATION', message: gqlErr.message }
  }
  return null // OK — safe to read result.data
}
```
> The exact shape of urql's `networkError.response` should be confirmed during 01-02 against `@urql/core@6` — urql surfaces HTTP non-2xx via `result.error.networkError`. `[ASSUMED — verify the response-status access path at implementation time]`. The classification *taxonomy* (which codes/statuses exist) is `[CITED: contract.md §7]`. Because CORS is wildcard, error responses carry CORS headers (contract §3), so a `NETWORK` result now genuinely means the transport failed — not a masked CORS rejection (which is what the old no-proxy model risked).

### Readiness gate with backoff — FND-03 / success criterion 4
```ts
// transport/readiness.ts — Source: contract §2; PITFALLS Pitfall 8
import { READY_URL } from './config'
export async function waitForReady(signal?: AbortSignal): Promise<void> {
  let delay = 500
  for (;;) {
    try {
      const res = await fetch(READY_URL, { signal })   // direct, cross-origin (wildcard CORS)
      if (res.status === 200) return
    } catch { /* network not up yet */ }
    await new Promise(r => setTimeout(r, delay))
    delay = Math.min(delay * 2, 5000)                  // bounded backoff, seconds-scale
  }
}
// App shows distinct "connecting to relay…" state until this resolves; then mounts StatsDashboard.
```

### useStatsPoll with hidden-tab pause + maxLevId diff — STATS-02
```ts
// hooks/useStatsPoll.ts — Source: ARCHITECTURE Pattern 3; contract §9; Page Visibility API
import { useEffect, useRef, useState } from 'react'
import { client } from '../transport/client'
import { StatsDocument } from '../queries/stats.graphql'

export function useStatsPoll(intervalMs = 5000) {
  const [stats, setStats] = useState<{ eventCount: number; maxLevId: number; dbVersion: number; pinnedStrfryVersion: string }>()
  const [hasNewData, setHasNewData] = useState(false)
  const lastLevId = useRef<number | null>(null)

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>
    let cancelled = false

    const tick = async () => {
      if (document.visibilityState === 'hidden') { schedule(); return }  // pause when hidden
      const r = await client.query(StatsDocument, {}).toPromise()
      const s = r.data?.stats
      if (s && !cancelled) {
        if (lastLevId.current !== null && s.maxLevId > lastLevId.current) setHasNewData(true)
        lastLevId.current = s.maxLevId
        setStats(s)
      }
      schedule()
    }
    const schedule = () => { if (!cancelled) timer = setTimeout(tick, intervalMs) }

    const onVisible = () => { if (document.visibilityState === 'visible') { clearTimeout(timer); tick() } }
    document.addEventListener('visibilitychange', onVisible)
    tick()
    return () => { cancelled = true; clearTimeout(timer); document.removeEventListener('visibilitychange', onVisible) }
  }, [intervalMs])

  // NOTE: hasNewData is a *nudge flag* — never auto-refetch an investigation window off it.
  return { stats, hasNewData, acknowledge: () => setHasNewData(false) }
}
```
> Uses `setTimeout` rescheduling (not `setInterval`) so a slow request never stacks overlapping polls, and so a hidden tab cleanly skips work. `[CITED: MDN Page Visibility API + contract §9 "poll on a sane interval (seconds)"]`. The interval default of 5s is a sane starting value (success criterion 3 says "seconds-scale") — make it a constant the planner can tune.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Vite dev `server.proxy` to dodge unconfigured CORS (relative `/graphql`) | **Direct connection** to the lens via a configurable base URL — lens now serves **wildcard CORS** | contract v1.1 (CORS configured) | No proxy hop; `vite preview`/static hosting work for the browser; base URL is an env knob, not a build-time relative path. |
| codegen `typescript` + `typescript-operations` + `typescript-urql` hooks trio | `@graphql-codegen/client-preset` (typed `graphql()` + `TypedDocumentNode`) | The Guild's recommended path since 2023 | Smaller output, single typed entry point, client-agnostic. Use client-preset. |
| `graphql@16` ubiquitous | `graphql@17.0.0` shipped 2026-06-15 | 2026-06-15 | Codegen toolchain hasn't caught up (`^16` cap) → **must pin 16 for now.** |
| `setInterval` polling | `setTimeout`-reschedule + Page Visibility pause | Long-standing best practice | Avoids overlapping in-flight polls and wasted work on hidden tabs. |

**Deprecated/outdated:**
- **The Vite dev-proxy CORS workaround** — superseded by the lens's wildcard CORS (contract §3). Do not use; connect directly.
- legacy `typescript-urql` hooks plugin — superseded by `client-preset`.
- `graphql@17` for this toolchain — not yet supported by `client-preset@6`/`graphql-request@7`.
- **`STACK.md` / `PITFALLS.md` proxy + relative-URL guidance** — predates contract v1.1; treat `contract.md` v1.2 as authoritative where they conflict.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `@types/react@19` / `@types/react-dom@19` are the correct type-def pairing | Standard Stack | LOW — install-time resolves the exact patch; trivially corrected. |
| A2 | urql `@urql/core@6` surfaces HTTP status via `result.error.networkError.response.status` | Code Examples §error classifier | MEDIUM — if the access path differs, the 503/413 branch needs adjusting. Verify against `@urql/core@6` docs during 01-02. The *taxonomy* (which statuses/codes exist) is contract-cited and not at risk. |
| A3 | 5000 ms is a sane default poll interval | Code Examples §useStatsPoll | LOW — success criterion only says "seconds-scale"; any 2–10s value satisfies it; make it a tunable constant. |
| A4 | A single stats view (no router) is acceptable for Phase 1 | Standard Stack / Structure | LOW — routing arrives in Phase 2; the planner may add a tiny path switch now or defer. Either satisfies the phase. |
| A5 | `import.meta.env.VITE_GRAPHQL_URL` is the right Vite env mechanism (the `VITE_` prefix exposes vars to client code) | Pattern 1 / Code Examples §config.ts | LOW — standard Vite env convention; the prefix is required for client exposure. Verify the var is set in `.env`/`.env.example` at scaffold time. |

**Note:** The version pins (graphql 16.14.2, urql 5/@urql/core 6, codegen cli 7/client-preset 6, vite 8/7, plugin-react 6/5, react 19.2.7, ts 5.9, parcel watcher 2.5.6) are `[VERIFIED: npm registry]` (prior session) — not assumptions. The wildcard-CORS / direct-connection facts are `[CITED: contract.md §3 v1.2]` — not assumptions.

## Open Questions

1. **Vite 7 vs Vite 8 pin**
   - What we know: Both build a React+TS SPA identically; with direct connection there is no proxy feature to differ on. Vite 8.1 is one day old (2026-06-23), Vite 7 is the conservative daily-driver.
   - What's unclear: team appetite for early-adopter risk on a local-dev tool.
   - Recommendation: **Vite 7 + `@vitejs/plugin-react@5`** for stability; Vite 8 acceptable (low blast radius). A one-line decision for the planner — not a blocker.

2. **urql `networkError` status access path (see Assumption A2)**
   - What we know: urql surfaces transport failures via `result.error.networkError`; the contract defines the 503/413 statuses to branch on, and wildcard CORS guarantees those statuses reach the browser un-masked.
   - What's unclear: exact property path to the HTTP status on `@urql/core@6`.
   - Recommendation: confirm during 01-02 implementation against the installed version; the classifier taxonomy is fixed, only the extraction detail is open.

## Environment Availability

> This phase's only external dependency is the LMDB2GraphQL backend at the configured base URL (default `127.0.0.1:8080`), needed at codegen time (live introspection, Node) and runtime (the `stats` query / `/ready` gate, browser, direct cross-origin).

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Node.js + npm | scaffold, codegen, dev server | (verify at plan time) | — | none — hard requirement |
| LMDB2GraphQL `POST /graphql` introspection | FND-01 codegen `schema` source | (verify backend running) | contract v1.2 | Use a saved introspection JSON / SDL as codegen `schema` if the live endpoint is down |
| LMDB2GraphQL `GET /ready`, `/health` | FND-03 readiness gate, STATS-02 poll | (verify backend running) | contract v1.2 | none for runtime proof — the walking skeleton's whole point is a live read |

**Missing dependencies with no fallback:** the live LMDB2GraphQL backend at the configured base URL must be reachable to *prove* the skeleton (success criteria 1–5 are all "a real cross-origin browser query succeeds"). For **codegen specifically**, a fallback exists: point `codegen.ts` `schema` at a checked-in introspection result/SDL instead of the live URL, so typing work isn't blocked when the backend is down. The schema is fully specified in `contract.md §4` and can be transcribed to a local `.graphql` SDL if needed.

**Missing dependencies with fallback:** codegen schema source (live URL → local SDL/introspection JSON), as above.

## Security Domain

> `security_enforcement: true`, ASVS level 1 (`.planning/config.json`). Scoped to this phase's actual surface: a local-dev SPA over an unauthenticated read-only API (now reached **directly** over wildcard CORS), no user input rendered yet (no event content/identifiers in Phase 1 — those are Phases 2–3).

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | API is unauthenticated by design (loopback-only); v1 is single local analyst (out of scope per PROJECT.md). |
| V3 Session Management | no | No sessions, no auth, no per-user state. The client sends **no credentials** (wildcard CORS won't honor them — contract §3). |
| V4 Access Control | no | No authorization model; network placement (loopback bind) is the only access control. Wildcard CORS does **not** restrict access — it only relaxes the browser same-origin policy (contract §3/§10). |
| V5 Input Validation | partial | Phase 1 renders only backend-controlled scalars (`eventCount`/`maxLevId`/`dbVersion`/`pinnedStrfryVersion`). `pinnedStrfryVersion` is a `String!` — render as **escaped text**, never HTML, even though it's low-risk. User-supplied input (pubkeys, event content) arrives in Phases 2–3. |
| V6 Cryptography | no | No crypto in this phase. Signatures are pre-verified by strfry; the tool must NOT re-verify (contract §5). |
| V7 Error Handling & Logging | yes | The error classifier must surface distinct non-blank states without leaking internals. The backend deliberately hides `"internal error"` details (contract §7) — the UI shows a generic failure + backoff, never fabricated detail. |
| V14 Configuration | yes | The **configurable base URL** (`VITE_GRAPHQL_URL`, default loopback) + loopback-only backend are the security-relevant config. README/`.env.example` must state v1 is local-dev only; any wider exposure of the unauthenticated, full-introspection, GraphiQL-enabled, **wildcard-CORS** API needs a gateway (out of scope — contract §10). |

### Known Threat Patterns for this stack (Phase 1 surface)

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Rendering `pinnedStrfryVersion` (a backend string) as HTML | Tampering / XSS | Render as escaped plaintext; never `dangerouslySetInnerHTML`. React escapes by default — do not opt out. |
| Exposing the unauthenticated, **wildcard-CORS** API beyond loopback | Information Disclosure | Keep the default `127.0.0.1` base; document local-dev-only; wildcard CORS means *any* origin can query if reachable, so network placement is the only control (contract §10). No production path in v1. |
| Leaking backend internals via error UI | Information Disclosure | Classifier maps `"internal error"` to a generic state; don't echo raw server messages that could carry internals. |
| Pointing the client at an untrusted base URL | Tampering (defense-in-depth) | `VITE_GRAPHQL_URL` should default to loopback; document that overriding it to a public/untrusted host sends queries (and trusts responses) from that origin. |

> **Deferred to later phases (not Phase 1):** XSS hardening of event `content`/`raw`/tag values (Phases 2–3 — render as escaped plaintext, never markdown/HTML); npub/hex input validation (Phase 2); 64-bit bounds-checks on `kind`/`createdAt` math (Phases 2–3). Flagged here so the planner knows they are *intentionally* out of this phase's scope, not overlooked.

## Sources

### Primary (HIGH confidence)
- `contract.md` **v1.2** (code-verified 2026-06-24) — endpoints, `StatsResult` schema (§5), **wildcard CORS** (`Allow-Origin: *`, methods `GET/POST/OPTIONS`, header `Content-Type`, no credentials; CORS headers on 200/413/503; OPTIONS preflight answered before readiness gate) (§3); Best Practice #1 "call the API directly — no proxy required" (§9); `errors[]`-on-200 + `extensions.code` (§7); `503`/`/ready`/`/health` (§2); `413`/256 KiB; opaque cursors + `INVALID_CURSOR` (§6.1/§6.4/§7); `TOO_MANY_AUTHORS`; silent `[1,500]` clamp (§6/§12); poll-`maxLevId` best practice (§9); 64-bit `Int` (§8); loopback-only deployment (§10); **`authors` distinct-pubkey query** (§6.4 — Phase 4 / BATCH-04, noted in passing only).
- npm registry (live, 2026-06-24, prior session) — verified: react 19.2.7, react-dom 19.2.7, urql 5.0.3, @urql/core 6.0.3, graphql 16.14.2 (exists) vs 17.0.1 (latest), `@graphql-codegen/client-preset@6.0.1` graphql peer caps at `^16.0.0`, @graphql-codegen/cli 7.1.3, vite 8.1.0, @vitejs/plugin-react 6.0.3, typescript 6.0.3 (recommend 5.9), nostr-tools 2.23.8, @parcel/watcher 2.5.6. Postinstall scripts `null` on codegen/urql packages. (Unchanged by the CORS revision.)

### Secondary (MEDIUM confidence)
- the-guild.dev/graphql/codegen `client-preset` guide — `codegen.ts` shape, `preset: 'client'`, live-URL schema source (via STACK.md).
- MDN Page Visibility API — `document.visibilityState` / `visibilitychange` for hidden-tab poll pause.
- Vite env-vars docs — `import.meta.env` + the `VITE_` prefix for client-exposed config (`VITE_GRAPHQL_URL`).

### Tertiary (LOW confidence) / stale corpus flag
- `.planning/research/STACK.md`, `PITFALLS.md` (2026-06-24) — **still reflect the pre-v1.1 proxy/CORS model** (relative-`/graphql` invariant, Vite `server.proxy`, "CORS surprises without the proxy"). **Superseded by contract v1.2 §3.** Used here only for the parts that did NOT change (version pins, codegen recipe, poll/error-classifier patterns); the proxy/relative-URL guidance is deliberately **not** propagated into this revised research.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — every version verified against the live npm registry (prior session); the graphql@16 peer cap confirmed directly. Unaffected by the CORS revision.
- Architecture / patterns (direct connection): HIGH — derived directly from the code-verified contract v1.2 §3 (wildcard CORS) + §9 (Best Practice #1, no proxy). The direct-client recipe is concrete and copy-pasteable.
- Pitfalls: HIGH — each is a deterministic property of contract.md v1.2.
- One MEDIUM open detail: the exact urql `networkError` status access path (Assumption A2 / Open Question 2) — taxonomy is fixed, extraction detail to confirm at implementation.

**Research date:** 2026-06-24 (revised for contract v1.2: wildcard CORS / direct connection)
**Valid until:** 2026-07-24 (30 days; stable stack — re-check the `graphql@17` peer-support status before extending, as the codegen toolchain may add 17 support).

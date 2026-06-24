# Stack Research

**Domain:** Read-only GraphQL data-exploration SPA (Nostr spam-investigation frontend, local-dev-first)
**Researched:** 2026-06-24
**Confidence:** HIGH (versions verified live against npm registry; compatibility cross-checked against peerDependency declarations and official docs)

## Verdict

The pre-chosen stack — **React 19 + Vite + TypeScript + urql + GraphQL Codegen + nostr-tools** — is **confirmed as the correct 2026 choice** for this project. No reason to relitigate. The refinements below are about *version pinning* and *one real compatibility trap*, not about swapping libraries.

**The one trap that matters:** `graphql@17.0.0` shipped 2026-06-15 (nine days ago). GraphQL Codegen and `graphql-request` both still cap their peer range at `^16`. **Pin `graphql@16.x`, not 17.** urql itself doesn't care (it bundles its own `@0no-co/graphql.web`), but the codegen toolchain does. This is the single highest-value finding in this document.

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `react` + `react-dom` | `19.2.7` | UI runtime | Pre-chosen; current stable. React 19 is mature (shipped late 2024), stable in 2026, first-class TS support. No SSR/RSC needed here — plain SPA. |
| `vite` | `8.1.0` (or pin `^7` if conservative) | Dev server + build | Pre-chosen. Vite owns the dev-proxy story (the documented CORS fix). v8 is current but **new** (8.0 Mar 2026, 8.1 *released 2026-06-23, one day ago*). See "Vite 7 vs 8" decision below. |
| `typescript` | `5.9.x` (avoid `6.0` for now) | Types | TS 6.0.3 is live but brand-new (major bump). For a greenfield app whose value is generated types, prefer the battle-tested `5.9` line. nostr-tools only requires `>=5.0`. Codegen output is plain TS, version-agnostic. |
| `urql` | `5.0.3` | GraphQL client (React bindings) | Pre-chosen. The `urql` package **is** the React binding (depends on `@urql/core@^6`). Lightweight, document-cache by default (perfect for read-only — no normalized cache needed), tiny vs Apollo. graphql-version-agnostic via `@0no-co/graphql.web`. |
| `@urql/core` | `6.0.3` | urql client core | Pulled in transitively by `urql`; pin explicitly to keep the peer range satisfied (`urql@5` needs `@urql/core@^6`). |
| `graphql` | **`16.14.2`** (NOT 17) | GraphQL spec runtime (codegen dep) | **Pin 16.** Codegen `client-preset@6` peer = `^16` max; `graphql-request@7` peer = `14 - 16`. `graphql@17` (2026-06-15) is unsupported by the tooling. urql doesn't need it at runtime but codegen does. |

### GraphQL Codegen (typed client from introspection)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `@graphql-codegen/cli` | `7.1.3` | Runs codegen | Always. Point `schema` at the live introspection endpoint (via the dev proxy). |
| `@graphql-codegen/client-preset` | `6.0.1` | The modern preset: typed `graphql()` document function + fragment masking | Use this preset, not the legacy per-file plugin stack. It generates a `gql/` folder with a typed `graphql()` you wrap your queries in; urql consumes the resulting `TypedDocumentNode` with zero extra config. |
| `@parcel/watcher` | `2.5.6` | Native file watcher for codegen `--watch` | Optional. Only needed if you run `codegen --watch`; it's a peer of the CLI. Skip if you run codegen one-shot in an npm script. |

> **Why `client-preset` over the old `typescript` + `typescript-operations` + `typescript-urql` plugin trio:** the client-preset is The Guild's current recommended path (2023+), produces smaller output, gives you a single typed `graphql()` entry point, and works with any client that accepts `TypedDocumentNode` (urql does, natively, via `@graphql-typed-document-node/core`). The old `typescript-urql` plugin that generated `useXxxQuery` hooks is legacy — do not use it.

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `nostr-tools` | `2.23.8` | npub/note bech32 ↔ hex via `nip19` | Always. Import the subpath: `import { nip19 } from 'nostr-tools'` or `import * as nip19 from 'nostr-tools/nip19'`. Use `nip19.decode(npub)` → `{ type:'npub', data: <hex> }` and `nip19.npubEncode(hex)`. The API speaks hex only; humans paste npub. **Only nip19 is needed** — you do not need signing/relay/event-verification modules (strfry already verified sigs; contract says do not re-verify). |
| `@vitejs/plugin-react` | `6.0.3` (with Vite 8) **or** `5.2.0` (with Vite 7) | React Fast Refresh + JSX transform | Required. v6 requires Vite 8; v5 pairs with Vite 7. The React Compiler / babel peers (`babel-plugin-react-compiler`, `@rolldown/plugin-babel`) are **optional** — only needed if you opt into the React Compiler, which this app does not. |

### Charts — no library (build it)

| Approach | Why |
|----------|-----|
| **Hand-rolled CSS/SVG bars** | The only charts here are: kind-distribution breakdown, posting-rate / burst histogram. These are bar charts of a handful of buckets. A `<div>` with `width: ${pct}%` (CSS bars) or a 30-line inline `<svg>` covers 100% of the need with zero dependency, zero bundle cost, and full styling control. This matches the project constraint ("lightweight CSS/SVG bars, no heavy chart lib"). |

### Near-duplicate detection — no library (build it)

| Approach | Why |
|----------|-----|
| **Client-side normalization + similarity, hand-rolled** | The API exposes no content search/dedup; you compute on the fetched event set (bounded by `limit`/`perAuthor` ≤ 500, so N is small). For spam, the useful signals are: (1) **exact/normalized-text grouping** — lowercase, trim, collapse whitespace, strip URLs, then hash/bucket by normalized string (a `Map<string, Event[]>` — O(n), catches copy-paste spam); (2) **near-duplicate** — character shingling + Jaccard, or normalized Levenshtein for short notes. For N ≤ 500 a naive O(n²) pairwise compare is fine (≤125k comparisons). Write ~40 lines; no dependency. If you ever want a primitive, `leven@4.1.0` (single-function Levenshtein, zero deps) is the only thing worth importing — but it is not needed for v1. |

## Installation

```bash
# Scaffold (greenfield)
npm create vite@latest graphql-explorer -- --template react-ts
cd graphql-explorer

# Core runtime
npm install react@19 react-dom@19 urql@5 @urql/core@6 graphql@16 nostr-tools@2

# Dev dependencies (build + codegen + types)
npm install -D \
  vite@8 @vitejs/plugin-react@6 typescript@5.9 \
  @types/react@19 @types/react-dom@19 \
  @graphql-codegen/cli@7 @graphql-codegen/client-preset@6
```

> If choosing the conservative Vite 7 path instead: `npm install -D vite@7 @vitejs/plugin-react@5`.

## Vite dev-server proxy — the CORS fix (REQUIRED)

The backend sets **no CORS headers** (contract §3). A browser SPA on `:5173` calling the API on `:8080` is cross-origin → blocked. The fix is to make the browser see **one origin**: serve the app from Vite and have Vite reverse-proxy `/graphql` to the API. The browser only ever talks to `:5173`; CORS never enters the picture (proxy hop is server-to-server).

The client must therefore call the **relative path `/graphql`**, never the absolute `http://127.0.0.1:8080/graphql`.

`vite.config.ts`:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/graphql': {
        target: 'http://127.0.0.1:8080', // LMDB2GraphQL default bind (loopback)
        changeOrigin: true,
      },
      // Optional: gate-checking readiness from the SPA without CORS pain.
      '/ready':  { target: 'http://127.0.0.1:8080', changeOrigin: true },
      '/health': { target: 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
})
```

urql client (note the relative URL):

```ts
import { Client, cacheExchange, fetchExchange } from '@urql/core'

export const client = new Client({
  url: '/graphql',                 // same-origin → proxied by Vite → no CORS
  exchanges: [cacheExchange, fetchExchange],
})
```

> `changeOrigin: true` rewrites the `Host` header to the target — harmless here and avoids surprises if the API ever vhosts. The shorthand `proxy: { '/graphql': 'http://127.0.0.1:8080' }` (as the contract shows) also works; the object form is preferred so you can add `/ready` and `/health` and stay explicit.

## codegen.ts — minimal shape (introspection → typed client)

Point `schema` at the same relative `/graphql` path **through the running dev proxy**, OR directly at the API. Two options:

**Option A — hit the API directly (simplest; codegen runs in Node, no CORS):**

```ts
import type { CodegenConfig } from '@graphql-codegen/cli'

const config: CodegenConfig = {
  schema: 'http://127.0.0.1:8080/graphql', // Node fetch → no CORS; needs API running
  documents: ['src/**/*.{ts,tsx}'],
  ignoreNoDocuments: true,                 // don't fail on files with no gql`
  generates: {
    './src/gql/': { preset: 'client' },    // client-preset → typed graphql() fn
  },
}
export default config
```

**Option B — through the Vite proxy (`http://localhost:5173/graphql`):** only if you prefer a single endpoint; requires the dev server up. Option A is simpler because codegen runs server-side in Node where CORS does not apply.

Usage in code (fully typed, no manual interfaces):

```ts
import { graphql } from './gql'

export const StatsQuery = graphql(`
  query Stats { stats { eventCount maxLevId dbVersion pinnedStrfryVersion } }
`)
// urql: useQuery({ query: StatsQuery }) → data is fully typed; result.data.stats.maxLevId is number
```

`package.json` scripts:

```json
{
  "scripts": {
    "codegen": "graphql-codegen --config codegen.ts",
    "codegen:watch": "graphql-codegen --config codegen.ts --watch"
  }
}
```

> **64-bit `Int` caveat (contract §8):** `kind` and `createdAt` are 64-bit on the wire but typed as GraphQL `Int` → codegen emits `number`. Real Nostr values stay within JS safe-integer range, so `number` is fine. No `bigint` scalar config needed for v1. If you ever see a value near `2^53`, revisit with a custom scalar mapping in codegen `config.scalars`.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| **urql** | **Apollo Client** (`@apollo/client@4.2.3`) | Apollo is excellent when you need a normalized cache, optimistic mutations, or heavy cache-graph reads. This app is **read-only, author-keyed, polled** — none of Apollo's strengths apply, and it's ~3-4x the bundle. urql's document cache is exactly right. Pick Apollo only if the team already standardizes on it elsewhere. |
| **urql** | **graphql-request** (`7.4.0`) + manual React state | graphql-request is a thin one-shot fetcher with no React integration, no caching, no in-flight dedup. Fine for a script; for an interactive SPA with polling + pagination you'd rebuild urql badly by hand. Use only for a Node-side CLI utility, not the UI. |
| **urql** | **TanStack Query + graphql-request** | A legitimate modern combo (TanStack handles caching/polling/refetch, graphql-request does transport). Defensible, but adds two libraries vs urql's one, and you lose codegen's first-class urql ergonomics. urql is the lower-friction choice here. |
| **client-preset** | legacy `typescript-urql` hooks plugin | Only if you specifically want generated `useXxxQuery` hooks and dislike the `graphql()` document pattern. Not recommended — it's the older path and The Guild steers everyone to client-preset now. |
| **graphql@16** | **graphql@17** | When codegen + your transport libs all declare `^17` support. As of 2026-06-24 they do not. Revisit in a few months. |
| **hand-rolled bars** | **recharts@3.9.0** / visx / chart.js | Only if the UI grows real charting needs (zoom, tooltips on dense time series, multiple series). For bucket bar charts, a chart lib is 50-150 KB of overhead for something a flexbox does. Defer. |
| **Vite 8** | **Vite 7** | See decision below. |

## Vite 7 vs Vite 8 decision

- **Vite 8.0** = 2026-03-12, **8.1** = 2026-06-23 (yesterday). It's current and the React plugin (`@vitejs/plugin-react@6`) targets it.
- **Risk:** Vite 8 moves toward the Rolldown bundler; ecosystem plugins are still catching up, and 8.1 is one day old.
- **Recommendation:** For maximum stability on a tool you depend on daily, **Vite 7 + plugin-react 5 is the safe pin**. If you want the latest and are comfortable being an early adopter (this is a local-dev internal tool, low blast radius), **Vite 8 + plugin-react 6 is fine** — the dev-proxy feature (the only Vite feature this project leans on) is stable across both. Either is acceptable; lean Vite 7 if in doubt.

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| `graphql@17` | Released 2026-06-15; codegen (`^16`) and graphql-request (`14-16`) don't support it yet → install warnings / subtle breakage | `graphql@16.14.2` |
| `typescript@6.0` | Brand-new major; greenfield app's whole value is reliable generated types — don't pioneer the compiler | `typescript@5.9.x` |
| `@apollo/client` | Heavy normalized-cache client for a read-only polled app; ~3-4x bundle, unused features | `urql@5` |
| `graphql-request` as the UI client | No caching, no React integration, no request dedup | `urql@5` (keep graphql-request only for Node-side scripts, if any) |
| legacy `@graphql-codegen/typescript-urql` hooks plugin | Older codegen path; superseded | `@graphql-codegen/client-preset@6` |
| `recharts` / `chart.js` / `d3` | Bundle bloat for simple bucket bar charts | CSS/SVG bars by hand |
| `string-similarity` / fuzzy-search libs | Unmaintained or oversized for N≤500 client-side dedup | Hand-rolled normalize + bucket + (optional) `leven@4` |
| Configuring CORS / `nostr-tools` event verification | Contract says CORS is unconfigured (use proxy) and sigs are pre-verified by strfry (§5: "do not re-verify on the client") | Vite dev proxy; trust `sig`, use only `nip19` |
| Subscriptions / WebSocket exchange in urql | API has no Subscription type (contract §1) | Poll `stats.maxLevId` on an interval |

## Version Compatibility

| Package | Compatible With | Notes |
|---------|-----------------|-------|
| `urql@5.0.3` | `react >= 16.8`, `@urql/core@^6` | React 19 fine. Pin `@urql/core@6` explicitly. |
| `@urql/core@6.0.3` | (no `graphql` peer) | Uses `@0no-co/graphql.web` internally → independent of your `graphql` version. This is why graphql 16-vs-17 only affects codegen. |
| `@graphql-codegen/client-preset@6.0.1` | `graphql ^16` (max) | **Hard cap at 16.** Do not install graphql 17. |
| `@graphql-codegen/cli@7.1.3` | `graphql ^16` (max), optional `@parcel/watcher@^2` | Same cap. |
| `nostr-tools@2.23.8` | `typescript >=5.0` | Subpath import `nostr-tools/nip19`. Deps are `@noble/*` / `@scure/*` (audited crypto). |
| `@vitejs/plugin-react@6.0.3` | `vite ^8`; React Compiler peers optional | Pairs with Vite 8 only. |
| `@vitejs/plugin-react@5.2.0` | `vite ^7` | Use this if pinning Vite 7. |

## Stack Patterns by Variant

**If prioritizing stability (recommended for a daily-driver tool):**
- Vite 7 + `@vitejs/plugin-react@5` + TypeScript 5.9 + graphql 16
- Because the only bleeding-edge pieces (Vite 8, TS 6, graphql 17) buy nothing this project needs and add early-adopter risk.

**If you want latest-and-greatest (acceptable — low blast radius, local tool):**
- Vite 8 + `@vitejs/plugin-react@6`, but still **graphql 16** (non-negotiable until codegen supports 17) and TypeScript 5.9.

**If a Node-side batch/CLI helper is ever added (e.g. bulk pubkey pre-fetch):**
- That helper can use `graphql-request@7` directly (server-side → no CORS, no React). Keep it out of the browser bundle. Still graphql 16.

## Sources

- npm registry (live, 2026-06-24) — verified current versions and `peerDependencies` for: react 19.2.7, vite 8.1.0 (8.0 2026-03-12 / 8.1 2026-06-23), typescript 6.0.3 (recommend 5.9), urql 5.0.3, @urql/core 6.0.3 (no graphql peer; uses @0no-co/graphql.web), graphql 17.0.1 (released 2026-06-15) vs 16.14.2, @graphql-codegen/cli 7.1.3, @graphql-codegen/client-preset 6.0.1 (graphql ^16 max), graphql-request 7.4.0 (graphql 14-16), @apollo/client 4.2.3, nostr-tools 2.23.8 (exports ./nip19), @vitejs/plugin-react 6.0.3 (vite ^8) / 5.2.0 (vite 7). **HIGH confidence** — direct registry reads + peer-dep cross-check.
- github.com/vitejs/vite-plugin-react README — confirmed React Compiler / babel peers are **optional**, Fast Refresh needs react >= 16.9. **HIGH confidence.**
- the-guild.dev/graphql/codegen (client-preset / react-vue guide) — confirmed `codegen.ts` shape, `preset: 'client'`, live-URL `schema` source supported. **HIGH confidence.**
- `contract.md` v1.0 (code-verified 2026-06-23) — CORS unconfigured (§3, proxy fix), introspection enabled (§3), read-only no subscriptions (§1), hex-only/nip19 conversion (§8), do-not-re-verify sigs (§5), 64-bit Int caveat (§8). **HIGH confidence** (authoritative).

---
*Stack research for: read-only GraphQL data-exploration SPA (Nostr spam investigation)*
*Researched: 2026-06-24*

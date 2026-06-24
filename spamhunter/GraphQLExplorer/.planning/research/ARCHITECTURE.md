# Architecture Research

**Domain:** Read-only, author-centric spam-investigation SPA (React 19 + Vite + TS + urql + GraphQL Codegen + nostr-tools) over the LMDB2GraphQL read-only GraphQL lens
**Researched:** 2026-06-24
**Confidence:** HIGH (component boundaries and data flow derive directly from the code-verified `contract.md` v1.0, confirmed `STACK.md`, and the dependency-mapped `FEATURES.md`)

## The One Load-Bearing Decision

This is a **layered SPA with a pure functional analytics core**. The decisive structural rule:

> **The transport/GraphQL layer fetches a bounded *event window* into memory. Pure analyzer functions consume that window and emit signal results. View components render those results. Analyzers never touch urql, fetch, or React.**

Everything else follows from this. It gives you a testable spam-signal core (unit-test analyzers against fixture event arrays with zero network), a thin replaceable transport, and views that are dumb projections of `{ events, analyzerOutputs, windowMeta }`. The "window-size honesty indicator" is not a feature bolted on — it is the contract between the transport layer (which owns *how much was fetched*) and the analyzer layer (which computes *over exactly that much*).

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                          VIEW LAYER (React 19)                         │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────────┐  │
│  │ Entry/   │ │ Author   │ │ Batch    │ │ Stats    │ │ Raw-JSON   │  │
│  │ Import   │ │ DrillDown│ │ Triage   │ │ Dashboard│ │ Inspector  │  │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └─────┬──────┘  │
│       │            │ (4 signal panels share one window)    │         │
├───────┴────────────┴────────────┴───────────┴─────────────┴─────────┤
│                    HOOK LAYER (React-aware adapters)                   │
│  ┌─────────────────┐ ┌──────────────────┐ ┌───────────────────────┐  │
│  │ useAuthorWindow │ │ useLatestPerAuthor│ │ useStatsPoll           │  │
│  │ (paginate+accum)│ │ (batch, chunked)  │ │ (interval, maxLevId)   │  │
│  └────────┬────────┘ └────────┬─────────┘ └──────────┬────────────┘  │
│           │   ┌───────────────────────────────────┐   │              │
│           │   │ useAnalyzers (memoized derive)     │◄──┘ (window in)  │
│           │   └───────────────┬───────────────────┘                  │
├───────────┴───────────────────┼──────────────────────────────────────┤
│   ANALYZER CORE (pure TS)      │      TRANSPORT LAYER (urql)           │
│  ┌──────────────────────────┐ │ ┌──────────────────────────────────┐ │
│  │ rate/burst   nearDup      │ │ │ urql Client (url:'/graphql')     │ │
│  │ tagAggregate kindHistogram│ │ │  ├ typed queries (codegen gql/)  │ │
│  │ spamScore (rollup)        │ │ │  ├ errors[] classifier           │ │
│  │ — input: Event[]          │ │ │  ├ readiness gate (/ready)       │ │
│  │ — output: SignalResult    │ │ │  └ cursor accumulator helper     │ │
│  └──────────────────────────┘ │ └──────────────┬───────────────────┘ │
│  ┌──────────────────────────┐ │                │                     │
│  │ identifier (nip19↔hex)   │ │   Vite dev proxy /graphql → :8080    │
│  └──────────────────────────┘ │                ▼                     │
└────────────────────────────────────────────────┼─────────────────────┘
                                                   ▼
                              LMDB2GraphQL  (POST /graphql, read-only)
```

### Component Responsibilities

| Component | Responsibility | Typical Implementation |
|-----------|----------------|------------------------|
| **Transport (urql client)** | Single same-origin `/graphql` client; owns the `errors[]` classifier, `/ready` gating, and the opaque-cursor accumulation loop. Nothing above it knows about HTTP. | `@urql/core` `Client` with `cacheExchange`+`fetchExchange`, a custom `mapExchange`/error helper to surface `extensions.code`. |
| **Codegen artifacts (`gql/`)** | The typed GraphQL document layer. Single `graphql()` entry; emits `TypedDocumentNode` urql consumes natively. | `@graphql-codegen/client-preset` output. Generated, never hand-edited. |
| **Identifier module** | npub/note bech32 ↔ 64-char lowercase hex; validation; "display form vs query form". Pure, no React. | `nostr-tools/nip19` wrapped in `toHex(input): Result<hex>` / `toNpub(hex)`. |
| **Query hooks** | React-aware glue: invoke transport, manage pagination accumulation state, expose `{ events, hasMore, windowMeta, loadMore, error }`. The *only* place urql meets React. | Custom hooks wrapping urql `useQuery`/`Client.query`. |
| **Analyzer core** | The four spam signals + spam-score rollup, as **pure functions** `(Event[], thresholds) → SignalResult`. The unit-testable heart. | Plain TS modules in `analyzers/`. No imports from `urql`, `react`, or `fetch`. |
| **Signal views** | Render one `SignalResult` each (timeline, dup clusters, tag fan-out, kind histogram). Dumb projections. | React components taking analyzer output + raw events as props. |
| **Drill-down shell** | Owns one author's fetched window; hosts the four signal panels + window-size indicator; routes raw-JSON inspector. | Container component bound to `useAuthorWindow(hex)`. |
| **Batch triage view** | Owns a ≤1000 author list; chunks for `latestPerAuthor`; runs rollup per author; sortable table; drill-in links. | Container bound to `useLatestPerAuthor`. |
| **Stats dashboard** | Polls `stats`; detects `maxLevId` change; nudges "new data". | Container bound to `useStatsPoll`. |

## Recommended Project Structure

```
src/
├── main.tsx                      # mount; urql Provider; router
├── App.tsx                       # routes + readiness gate shell
│
├── gql/                          # GENERATED (codegen client-preset) — do not edit
│   ├── graphql.ts
│   └── gql.ts
│
├── transport/                    # urql + HTTP concerns ONLY (no React UI)
│   ├── client.ts                 # urql Client, url: '/graphql'
│   ├── errors.ts                 # classify errors[] → {code, message}; INVALID_CURSOR/TOO_MANY_AUTHORS
│   ├── readiness.ts              # poll /ready, gate first query, treat 503 as retry
│   └── paginate.ts               # generic cursor-accumulation loop (after→endCursor until hasMore=false)
│
├── queries/                      # gql`` documents (codegen scans these)
│   ├── events.graphql.ts         # events(filter, after, limit)
│   ├── latestPerAuthor.graphql.ts
│   └── stats.graphql.ts
│
├── identifier/                   # PURE — nip19 ↔ hex, validation
│   ├── identifier.ts             # toHex, toNpub, isValidHex64, parseBatchList
│   └── identifier.test.ts
│
├── analyzers/                    # PURE FUNCTIONS — the testable core, zero deps on transport/react
│   ├── types.ts                  # Event, SignalResult, WindowMeta, Thresholds
│   ├── rate.ts                   # Signal 1: inter-post gaps, posts/min, regularity (CV)
│   ├── nearDup.ts                # Signal 2: normalize, exact-hash bucket, shingle+Jaccard, clustering
│   ├── tags.ts                   # Signal 3: p/e/t per-event + window fan-out
│   ├── kinds.ts                  # Signal 4: kind histogram
│   ├── spamScore.ts              # rollup over the four SignalResults (transparent sub-scores)
│   └── *.test.ts                 # unit tests against fixture Event[] — NO network
│
├── hooks/                        # React-aware adapters (transport ↔ analyzers ↔ views)
│   ├── useAuthorWindow.ts        # paginate single author, accumulate, expose windowMeta+loadMore
│   ├── useLatestPerAuthor.ts     # batch, chunk ≤1000, merge by author (not index)
│   ├── useStatsPoll.ts           # interval poll, maxLevId change detection
│   └── useAnalyzers.ts           # useMemo(analyzers(window, thresholds))
│
├── views/
│   ├── EntryView.tsx             # paste/import → normalize → route
│   ├── BatchImportView.tsx       # list paste/file parse, ≤1000 guard, body-size guard
│   ├── AuthorDrillDown.tsx       # shell: window-size indicator + 4 signal panels + raw inspector
│   ├── BatchTriageView.tsx       # sortable spam-score table
│   ├── StatsDashboard.tsx        # polled corpus stats
│   └── signals/                  # dumb projections of SignalResult
│       ├── TimelinePanel.tsx     #   + BurstChart (CSS/SVG bars)
│       ├── ContentDupPanel.tsx
│       ├── TagFanoutPanel.tsx
│       ├── KindHistogramPanel.tsx
│       └── RawJsonInspector.tsx
│
├── components/                   # shared dumb UI (Bar, EmptyState, ErrorBanner, WindowMeta)
└── routes.tsx                    # /author/:hex, /batch, /stats, / (entry)
```

### Structure Rationale

- **`analyzers/` is physically separated from everything React/network.** This is the enforcement mechanism for the load-bearing decision: an analyzer importing `react` or `urql` is a code-review red flag. It lets the spam logic be tested as `expect(nearDup(fixtures, {jaccard:0.8})).toEqual(...)` with no harness.
- **`transport/` owns all HTTP weirdness** (the contract's silent clamping, opaque cursors, 503/`errors[]`/`extensions.code`). Views and analyzers never see a raw GraphQL response — only typed data or a classified error.
- **`hooks/` is the only seam where the three worlds meet.** Keeping it thin (and isolated) means the analyzer↔view contract stays serializable and the transport stays swappable.
- **`identifier/` is pure and standalone** because it's needed by *two* entry paths (single + batch) before any query runs — it's a dependency, not a view concern.
- **`gql/` is generated** — treated as build output, gitignored or committed but never hand-edited (regenerated from live introspection via the dev proxy).

## Architectural Patterns

### Pattern 1: Pure Analyzer Core (window-in, signal-out)

**What:** Each spam signal is a pure function `(events: Event[], thresholds) → SignalResult`. No side effects, no fetch, no React. The window is the sole input; window size is carried alongside as `WindowMeta`.
**When to use:** All four signals + the rollup. Always.
**Trade-offs:** (+) trivially unit-testable, deterministic, recomputed cheaply via `useMemo`, portable to a future Web Worker if N grows. (−) requires discipline — no sneaking a fetch into an analyzer "just to get more data" (the hook owns fetching).

**Example:**
```typescript
// analyzers/nearDup.ts — pure, testable in isolation
export function nearDup(events: Event[], t: { jaccard: number } = { jaccard: 0.8 }): DupResult {
  const norm = (s: string) => s.toLowerCase().trim().replace(/\s+/g, ' ').replace(/https?:\/\/\S+/g, '');
  const exact = new Map<string, number[]>();           // free win: normalized-hash bucket
  events.forEach((e, i) => push(exact, norm(e.content), i));
  // near-dup: brute-force O(n²) Jaccard over shingles — fine for N≤500 (contract limit)
  // union-find over candidate pairs → clusters
  return { clusters, dupRatio, windowSize: events.length };
}
```

### Pattern 2: Cursor-Accumulation Hook (window grows, analytics re-derive)

**What:** `useAuthorWindow` holds an accumulating `Event[]` plus `endCursor`/`hasMore`. `loadMore()` fetches the next page with the **same filter**, appends, and bumps `windowMeta.fetchedCount`. Analyzers re-run via `useMemo` keyed on the array.
**When to use:** Single-author drill-down and "fetch deeper" expansion.
**Trade-offs:** (+) the window-size indicator is automatic (it's just `windowMeta`); analytics honesty is structural. (−) accumulation lives in hook state, *not* urql's cache — urql's document cache caches each *page* response, but the merged window is your own state. Don't fight urql to normalize across pages; the contract's opaque cursors make offset-style cache merging fragile anyway.

**Example:**
```typescript
function useAuthorWindow(hex: string, pageLimit = 100) {
  const [events, setEvents] = useState<Event[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(true);
  const loadMore = async () => {
    const page = await client.query(EventsDoc,
      { filter: { authors: [hex] }, after: cursor, limit: pageLimit }).toPromise();
    const code = classify(page.error, page.data);     // INVALID_CURSOR → reset cursor=null, restart
    setEvents(prev => [...prev, ...page.data.events.events]);
    setCursor(page.data.events.endCursor);
    setHasMore(page.data.events.hasMore);
  };
  return { events, hasMore, windowMeta: { fetchedCount: events.length, hasMore }, loadMore };
}
```

### Pattern 3: Poll-and-Diff for "new data" (no subscriptions)

**What:** `useStatsPoll` queries `stats` on an interval (seconds, not ms — contract §9), keeps the last `maxLevId`, and flips a `hasNewData` flag when it increases. The UI shows a non-intrusive "new events available — refresh" nudge; it does **not** auto-refetch investigation windows (that would move the analyst's ground under them).
**When to use:** Stats dashboard, and a global "new data" indicator.
**Trade-offs:** (+) cheapest possible change detection; matches the read-only/no-push reality. (−) polling cadence is a guess — make it configurable; pause polling when the tab is hidden (`visibilitychange`) to avoid waste.

**Example:**
```typescript
function useStatsPoll(intervalMs = 5000) {
  const [stats, setStats] = useState<StatsResult>();
  const lastLevId = useRef(0);
  useEffect(() => {
    const tick = async () => {
      const r = await client.query(StatsDoc, {}).toPromise();
      const s = r.data?.stats; if (!s) return;
      setStats(prev => ({ ...s, hasNewData: s.maxLevId > lastLevId.current }));
      lastLevId.current = s.maxLevId;
    };
    const id = setInterval(tick, intervalMs); tick();
    return () => clearInterval(id);
  }, [intervalMs]);
  return stats;
}
```

### Pattern 4: Error Classifier as a Transport Boundary

**What:** A single `classify(response)` turns the contract's three failure shapes — transport status (`503`/`413`), GraphQL `errors[]` with `extensions.code` (`INVALID_CURSOR`/`TOO_MANY_AUTHORS`), and codeless validation/`internal error` — into one discriminated union the UI branches on. **Every** query passes through it; views never inspect `errors[]` directly.
**When to use:** Cross-cutting — wraps all three query hooks.
**Trade-offs:** (+) one place encodes the contract's error semantics; uniform error UI. (−) must be kept in sync if the backend adds codes (low risk; contract is v1.0 stable).

## Data Flow

### Request Flow — single author (the core path)

```
Analyst pastes "npub1..." or hex
    ↓
identifier.toHex()  →  validate 64-char lowercase hex   ──fail──► inline "bad pubkey" (no query)
    ↓ (hex)
route /author/:hex
    ↓
useAuthorWindow(hex)  →  transport.client.query(EventsDoc, {filter:{authors:[hex]}, limit:100})
    ↓
classify(response)  ──503──► readiness retry   ──INVALID_CURSOR──► drop cursor, restart page 1
    ↓ (data)
accumulate events[] + endCursor + hasMore   →  windowMeta { fetchedCount, hasMore }
    ↓
useAnalyzers(events, thresholds)  =  { rate, nearDup, tags, kinds, spamScore }   (pure, memoized)
    ↓
AuthorDrillDown renders: WindowMeta indicator + 4 signal panels (+ RawJsonInspector on demand)
    ↑
"Load more" → loadMore() → next page (same filter) → events[] grows → analyzers re-derive
```

### Request Flow — batch triage

```
Paste/file list → identifier.parseBatchList() → hex[]  (dedupe, validate, count)
    ↓
guard: hex.length ≤ 1000 (else chunk)   +   body-size awareness (256 KiB → split authors[])
    ↓
useLatestPerAuthor(hex[], kind=1, perAuthor=3..10)  →  one query per ≤1000 chunk
    ↓
classify  ──TOO_MANY_AUTHORS──► re-chunk smaller and retry   ──413──► shrink authors[] per request
    ↓ (AuthorGroup[])
merge by `author` key  (NEVER zip by index — empty groups omitted, contract §5)
    ↓
per author: spamScore over that author's small window  (transparent sub-scores)
    ↓
BatchTriageView: sortable table → click → /author/:hex (full drill-down, deeper fetch)
```

### State Management

```
urql document cache  ── caches individual page/query RESPONSES (dedup in-flight, cheap re-render)
        │
        ▼
Hook-local React state  ── owns the ACCUMULATED window (events[], cursor, hasMore, windowMeta)
        │                   owns batch author list + merged AuthorGroup map
        │                   owns poll state (lastLevId, hasNewData)
        ▼
useMemo(analyzers)  ── DERIVED, never stored; recompute on window change
        │
        ▼
View components  ── pure projection; thresholds + active author live in URL/local state
```

**Verdict on the urql-cache-vs-local-state question:** use urql's **default document cache** (no normalized `graphcache`) — the app is read-only and author-keyed, so there are no cache-invalidation needs and nothing to normalize. The *accumulated window* is **deliberately your own React state**, not the cache, because (a) the contract's opaque cursors make cross-page cache merging fragile, and (b) the window is the analyzer's input contract and must be explicit. Thresholds and the active author belong in **URL (deep-linkable) + light local state**, not the cache.

### Key Data Flows

1. **Window → analyzers → views is one-directional and pure.** Views never call analyzers with anything but the current window; analyzers never reach back for more data. "Fetch deeper" is a hook action, not an analyzer action.
2. **Identifier normalization happens once, at the edge.** Everything downstream (queries, routes, dedup keys) uses hex; npub is reconstructed only for display.
3. **Errors converge at one classifier** before reaching any view, so the UI has exactly one error-state vocabulary.

## Routing Model

**Recommendation: client-side routing with hex pubkey in the path.** Routes:

| Route | View | State source |
|-------|------|--------------|
| `/` | EntryView (paste single / go to batch) | — |
| `/author/:hex` | AuthorDrillDown | `:hex` drives `useAuthorWindow`; deep-linkable/shareable |
| `/batch` | BatchImportView → BatchTriageView | author list in local state (too big for URL) |
| `/stats` | StatsDashboard | polled |

A tiny router (`react-router` or even a hand-rolled hash/path switch — the app has ~4 routes) suffices; do not pull in a data-router/loader framework. The `:hex` route is the deep-link differentiator from FEATURES.md. Batch lists stay in memory (a 1000-npub list won't fit a URL).

## Build Order (dependency-ordered)

This ordering falls directly out of the FEATURES.md dependency graph and the layering above. Each step is independently demonstrable.

1. **Scaffold + Vite dev proxy + urql client** (`url:'/graphql'`). Prove a same-origin `stats` query returns data through the proxy. *Unblocks everything; validates the CORS fix is real.*
2. **Codegen wired** (introspection → `gql/`, typed `stats`). *Locks in end-to-end types before more queries exist.*
3. **Transport hardening: error classifier + readiness gate + cursor accumulator** (`transport/`). *Foundational per FEATURES.md — every query depends on it. Build before any signal.*
4. **Identifier module** (pure, tested). *Required by both entry paths; no UI yet.*
5. **Analyzer core, all four signals + types** (pure, tested against fixtures, NO network). *Can be built fully in parallel with steps 1–4 because it has zero transport dependency — this is the parallelization win of the pure core. Tests are the spec.*
6. **`useAuthorWindow` + AuthorDrillDown shell + window-size indicator.** Wire one real author through transport → accumulate → analyzers. *First vertical slice of core value.*
7. **Signal views** (timeline+burst, content+dup, tag fan-out, kind histogram, raw inspector) — each a dumb projection of an already-tested analyzer. *Can fan out in parallel once step 6 + step 5 exist.*
8. **Stats dashboard + poll/diff** (`useStatsPoll`). *Independent; can land anytime after step 2.*
9. **Spam-score rollup** (analyzer) — depends on all four signals (step 5/7). *Sequenced after signals exist.*
10. **Batch import + `useLatestPerAuthor` + triage table.** Depends on identifier (4), rollup (9), transport chunking (3). *The P2 expansion.*
11. **Differentiators** (clustering, burst sparkline, mention fan-out, export, deep-link) — each extends an existing analyzer/view. *Post-validation.*

**Parallelization note:** steps 4 and 5 (identifier, analyzers) are pure and have no transport dependency — they can be built and fully tested before or alongside the transport layer (steps 1–3). This is the practical payoff of isolating the analyzer core.

## Scaling Considerations

This is a single-analyst local-dev tool; "scale" means *event-window size* and *batch breadth*, not concurrent users.

| Scale | Architecture Adjustments |
|-------|--------------------------|
| Window ≤ 500 events (one page) | Naive O(n²) pairwise Jaccard is fine (≤125k comparisons). No optimization. |
| Window growing via "load deeper" (thousands) | Near-dup O(n²) starts to bite. Re-derive incrementally (only diff new page against existing buckets) or move `nearDup` into a Web Worker — trivial because it's already a pure function. |
| Batch 1000 authors × small perAuthor | Watch the 256 KiB body cap (huge `authors[]`) and `TOO_MANY_AUTHORS`; chunk requests. Cross-author dedup over the union is the heaviest compute — defer to v2, Worker it. |

### Scaling Priorities

1. **First bottleneck: near-dup O(n²) on large accumulated windows.** Fix: incremental re-derive against existing exact-hash buckets, then Worker-offload the pure `nearDup`. No architecture change — the pure-function boundary makes Worker migration mechanical.
2. **Second bottleneck: batch request size / cost (`authors × perAuthor`).** Fix: keep `perAuthor` small (3–10) for triage, chunk `authors` ≤1000 *and* under 256 KiB, drill deep only on click.

## Anti-Patterns

### Anti-Pattern 1: Analyzers that fetch

**What people do:** A "near-dup" function that, finding few events, calls the API for more.
**Why it's wrong:** Destroys testability, couples the pure core to transport, hides the window size (breaking the honesty indicator), and makes analytics non-deterministic.
**Do this instead:** Hooks own fetching and the window; analyzers receive `Event[]` and report over exactly that. "Need more data" is a `loadMore()` user action.

### Anti-Pattern 2: Normalizing the window into urql's cache

**What people do:** Reach for `@urql/exchange-graphcache` to merge paginated pages into one normalized list.
**Why it's wrong:** Opaque cursors (contract §6.1) anchor to internal sort keys, not offsets — cache merging is fragile and gains nothing for a read-only app. It adds a heavy dependency to fight a problem you don't have.
**Do this instead:** Default document cache for page responses; accumulate the merged window in hook-local state.

### Anti-Pattern 3: Zipping `latestPerAuthor` results by index

**What people do:** `authors[i]` ↔ `result[i]`.
**Why it's wrong:** Authors with zero matches are **omitted** (contract §5) — indices drift, you mislabel every author after the first gap.
**Do this instead:** Build a `Map<author, AuthorGroup>` and look up by the `author` field; render "no events" for missing keys explicitly.

### Anti-Pattern 4: Rendering `content` as HTML/markdown

**What people do:** Render note `content` with `dangerouslySetInnerHTML` or a markdown engine "to look nice."
**Why it's wrong:** This is *spam* content — XSS payloads and hostile markup are exactly what you're investigating. Executing it is a self-own.
**Do this instead:** Render as plaintext. The raw-JSON inspector shows canonical bytes safely.

### Anti-Pattern 5: Auto-refetching the analyst's window on poll

**What people do:** `maxLevId` changed → silently re-pull the current author's events.
**Why it's wrong:** Moves the ground under an in-progress investigation; analytics shift mid-judgment.
**Do this instead:** Poll only updates a "new data available" nudge; the analyst decides when to refresh.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| LMDB2GraphQL `POST /graphql` | urql client at relative `/graphql`, proxied by Vite to `127.0.0.1:8080` | Same-origin via proxy solves CORS (contract §3); call relative path only, never absolute. |
| LMDB2GraphQL `GET /ready` | Readiness gate before first query; 503-as-retry fallback | Proxy `/ready` too (STACK.md vite.config). |
| nostr-tools `nip19` | Library call in identifier module | npub/note ↔ hex only; no signing/relay/verify modules. |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| transport ↔ hooks | typed data or classified error (discriminated union) | Hooks never see raw `errors[]`. |
| hooks ↔ analyzers | `Event[]` + `WindowMeta` in, `SignalResult` out | One-directional, pure, serializable. |
| analyzers ↔ views | `SignalResult` props | Views are dumb projections; no logic. |
| identifier ↔ (entry, batch, routes) | `Result<hex>` | Normalization at the edge, hex everywhere downstream. |

## Sources

- `contract.md` v1.0 (code-verified 2026-06-23) — endpoints, schema, opaque cursors, silent clamping, `errors[]`/`extensions.code`, 503/`/ready`, 256 KiB cap, `TOO_MANY_AUTHORS`, empty-group omission, fixed ordering, author-claimed `createdAt`. **HIGH (authoritative).**
- `.planning/research/STACK.md` (2026-06-24) — urql document cache (no normalized cache), client-preset `TypedDocumentNode`, Vite dev-proxy CORS fix, relative `/graphql`, poll-not-subscribe. **HIGH.**
- `.planning/research/FEATURES.md` (2026-06-24) — feature dependency graph, "window-size honesty" framing, four signals, build/MVP sequencing, anti-features. **HIGH on mapping, MEDIUM on heuristic thresholds.**
- `.planning/PROJECT.md` — scope, read-only constraint, local-dev-first, key decisions. **HIGH.**

---
*Architecture research for: author-centric Nostr spam-investigation SPA over LMDB2GraphQL*
*Researched: 2026-06-24*

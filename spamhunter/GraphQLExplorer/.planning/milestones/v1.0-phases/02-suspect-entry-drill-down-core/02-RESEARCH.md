# Phase 2: Suspect Entry + Drill-Down Core - Research

**Researched:** 2026-06-24
**Domain:** Nostr identifier normalization (nip19 ↔ hex), cursor-paginated author window over a read-only GraphQL lens, asymmetric burst/rate analysis under forgeable timestamps, honest-denominator forensic UI (React 19 + Vite + urql, hand-rolled CSS).
**Confidence:** HIGH (transport/codebase reuse, contract semantics, nip19 API empirically verified); MEDIUM (burst thresholds — sane defaults, not corpus-validated this phase).

## Summary

Phase 2 is the first multi-view slice. Almost everything it needs already exists in Phase 1's `src/transport/` boundary: the urql `client` (POST-only), the 7-kind `classify()` discriminated union (with `INVALID_CURSOR`, `NOT_READY`/503, `PAYLOAD_TOO_LARGE`/413, `VALIDATION`, `INTERNAL`, `NETWORK` already modeled), the `waitForReady()` gate, and a scaffolded `accumulatePages()` cursor loop that this phase is the **first to exercise**. The work is therefore mostly *composition and three new pure modules*, not new infrastructure: a pure `identifier/` module (nip19 decode + hex validation + parse-vs-zero-match distinction), a `useAuthorWindow` hook (paginate via `events(filter:{authors:[hex]}, after, limit)`, accumulate, derive `windowMeta`, expose `loadMore`), and a pure rate/burst analyzer over author-claimed `createdAt`.

The phase's two load-bearing honesty contracts are not decoration — they are the product. (1) A **non-removable window-size denominator** ("computed over N fetched events · hasMore · time range") so a partial fetch is never read as exoneration; the literature on forensic false-negatives confirms this is the correct posture — "absence of evidence" is routinely mis-read as "evidence of absence," and the denominator is the textual guard against it. (2) **Asymmetric burst interpretation**: a burst (tight interarrival clustering) is a suspicious signal; *quiet* timing proves nothing because `createdAt` is author-claimed and forgeable — so the panel must have **no green/clean state** and a permanent forgeability caveat.

The one genuinely novel decision the plan must lock (not infrastructure): **what `note`/`nprofile` decode to.** `note` is bech32 for an *event id*, `nprofile` wraps a *pubkey*, and `npub` is a *pubkey* — yet all three decode to a 32-byte hex string that is shape-indistinguishable from a pubkey. The success criteria say "paste a single pubkey as npub/note/nprofile," so the module must define a deliberate policy (recommended below) rather than silently treating an event id as an author.

**Primary recommendation:** Build three pure, unit-tested modules (`identifier/`, the rate/burst analyzer, and a `windowMeta` deriver) with zero transport coupling, wire them through one new `events` query document + a `useAuthorWindow` hook that drives Phase 1's `accumulatePages`/`classify`, and render two new surfaces (paste bar in the shell + author drill-down route) using a minimal hash router and the inherited CSS-token system. Install `nostr-tools` pinned to an exact version (use `nip19` only); treat its decode as **throw-on-invalid** (the basis of the parse-failure branch). Reject `nsec` explicitly (secret-key safety).

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| nip19 ↔ hex decode/encode, validation, parse-vs-zero distinction | Client (pure `identifier/` module) | — | Pure string logic; no network; unit-testable in the existing Node vitest env |
| Routing (`#/a/<hex>`) | Client (browser) | — | Bookmarkable/shareable URL state; hash router needs no server |
| Author event fetch + cursor pagination | API / lens (`events` query) | Client transport (`client`, `classify`, `accumulatePages`) | The lens owns ordering, cursors, limits; client only loops + classifies |
| Window accumulation + `windowMeta` derivation | Client (`useAuthorWindow` hook + pure deriver) | — | Accumulates pages the lens returns; pure derivation of N / range / hasMore |
| Rate / burst analysis | Client (pure analyzer) | — | Derived entirely from fetched events' `createdAt`; no network; unit-testable |
| Error → UI state mapping | Client transport (`classify`) | View | Single boundary already built in Phase 1; views branch on the union |
| Escaped-plaintext rendering of content/IDs | Client (React default) | — | XSS guard; never `dangerouslySetInnerHTML` |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `nostr-tools` | `2.23.x` (pin exact — see audit) | nip19 bech32 ↔ hex (`nip19.decode`, `nip19.npubEncode`) | `[VERIFIED: nbd-wtf official repo + npm]` Canonical Nostr tooling library by nbd-wtf (the de-facto Nostr lib author); 660k weekly downloads; created 2021. Implements NIP-19 exactly. |
| React | `^19` (installed) | UI | `[VERIFIED: package.json]` Already in project |
| `@urql/core` / `urql` | `^6` / `^5` (installed) | GraphQL client + React bindings | `[VERIFIED: package.json]` Phase 1 transport boundary |
| `graphql` | `16.14.2` (exact pin, guarded) | GraphQL runtime | `[VERIFIED: package.json + check-graphql-pin.cjs]` Pinned; postinstall tripwire fails on major > 16 |
| `@graphql-codegen/*` | cli `^7`, client-preset `^6` (installed) | Typed `events` document | `[VERIFIED: package.json]` Reuse `graphql()` fn → `TypedDocumentNode`; reads checked-in `schema.graphql` SDL |
| `vitest` | `^3.2.6` (installed) | Unit tests for pure modules | `[VERIFIED: package.json]` Node env (`vitest.config.ts` `environment: 'node'`) |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| (none new beyond `nostr-tools`) | — | — | Routing, charts, and analysis are all hand-rolled per project constraints. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `nostr-tools` (full lib) | `@scure/base` (`bech32`) directly | `@scure/base` is the lighter primitive nostr-tools itself depends on (8.6M wk downloads). But you'd hand-roll the NIP-19 TLV parsing for `nprofile` (it's TLV-encoded, not a bare bech32 payload) — error-prone. `nostr-tools` is tree-shakeable; importing only `nip19` keeps the bundle small. **Recommend `nostr-tools`** (per UI-SPEC + CONTEXT, already locked). `[ASSUMED]` bundle impact |
| Hash router (recommended) | History API (`pushState`) router | History API needs a dev/prod server fallback (rewrite all paths to `index.html`). Hash routing (`#/a/<hex>`) works on a static `vite build` with zero server config. CONTEXT marks the mechanism as Claude's discretion but says "tiny hash/path switch, NOT a data-router." **Recommend hash routing.** `[CITED: CONTEXT.md]` |
| Hand-rolled CSS/SVG rate bars | a charting library (recharts, visx, chart.js) | Project constraint (UI-SPEC + CONTEXT) forbids a heavy chart lib. Rate bars are simple `<div>`/`<svg>` rects scaled to a max. **Hand-roll.** `[CITED: UI-SPEC.md §Design System]` |

**Installation:**
```bash
npm install nostr-tools@2.23.8   # pin exact; verify version per audit before installing
```
(After install, re-run `npm run codegen` only if the `events` document is added in the same step — codegen reads the SDL, not the new dep.)

**Version verification:** `npm view nostr-tools version` → `2.23.8` (published 2026-06-23). `[VERIFIED: npm registry]`. The package root was created 2021-01-04. Pin an **exact** version (not `^`) so a future auto-bump can't pull an unreviewed release into a forensic tool.

## Package Legitimacy Audit

| Package | Registry | Age (pkg) | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----------|-----------|-------------|---------|-------------|
| `nostr-tools` | npm | 5+ yrs (created 2021-01-04) | 660,704 / wk | github.com/nbd-wtf/nostr-tools | OK (see note) | **Approved — pin exact** |

**Note on the `SUS` seam verdict:** `gsd-tools query package-legitimacy check` returned `SUS / "too-new"`. This is a **false positive on the version, not the package**: the seam read `publishedAt` of the latest *patch* (`2.23.8`, published 2026-06-23 — one day before research) and flagged recency, but the **package** is 5+ years old, has 660k weekly downloads, a known authoritative source repo (nbd-wtf, the canonical Nostr tooling maintainer), no `postinstall` script, and is the library the UI-SPEC + CONTEXT already locked. The legitimacy signal that matters (established package, real maintainer, high adoption, no install hooks) is clean.

**Mitigation (still warranted for a forensic tool):** Pin an **exact** version and do not float on `^`. The planner SHOULD add a `checkpoint:human-verify` step before the install task to (a) confirm the chosen exact version (`2.23.8` or a slightly older stable line if preferred to avoid the day-old patch), and (b) eyeball the lockfile diff for the `@noble/*` / `@scure/*` transitive set (`@noble/curves`, `@noble/hashes`, `@scure/base`, `@scure/bip32`, `@scure/bip39`, `nostr-wasm`) — all are nbd-wtf/paulmillr-maintained crypto primitives, expected.

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** `nostr-tools` — flagged by the seam on version-recency only; assessed as a false positive above. Planner adds a `checkpoint:human-verify` before install per protocol.

`npm view nostr-tools scripts.postinstall` → none. No network/filesystem install hook. `[VERIFIED: npm registry]`

## Architecture Patterns

### System Architecture Diagram

```
                          ┌─────────────────────────────────────────────┐
  analyst pastes          │              App shell (header)              │
  npub/note/nprofile/hex  │   ┌───────────────┐                          │
        │                 │   │  Paste bar     │  submit (accent "Inspect │
        ▼                 │   │  (entry)       │          author")        │
  ┌───────────────┐       │   └──────┬────────┘                          │
  │ identifier/    │◀──────────────  │  parse                            │
  │  parse()       │  throws→PARSE_  │                                   │
  │  (pure, nip19) │  FAILURE        ▼                                   │
  └──────┬────────┘            ┌──────────────┐  valid → route          │
         │ {hex, npub, kind}   │  hashRouter  │  #/a/<hex>               │
         │                     └──────┬───────┘                          │
         │  PARSE_FAILURE             │                                  │
         │  (stays on dashboard,      ▼                                  │
         │   inline amber error)  ┌─────────────────────────────────────┴──┐
         └──────────────────────▶ │      AuthorDrillDown view (route)        │
                                  │  ┌────────────────────────────────────┐ │
                                  │  │ identity header (npub + hex, mono)  │ │
                                  │  └────────────────────────────────────┘ │
                                  │            │ hex                         │
                                  │            ▼                             │
                                  │  ┌────────────────────────────────────┐ │
   ┌──────────────┐  events page  │  │ useAuthorWindow(hex)               │ │
   │ LMDB2GraphQL │◀──────────────┼──┤  → client.query(EventsDocument,    │ │
   │ lens (POST   │  filter:{      │  │     {filter:{authors:[hex]},after, │ │
   │ /graphql)    │   authors:[hex]│  │     limit:100})                    │ │
   │ createdAt    │   } constant   │  │  → classify() ─┐                   │ │
   │ DESC,levId   │──────────────▶ │  │  → accumulate  │ INVALID_CURSOR    │ │
   │ DESC; opaque │  EventsPage    │  │    pages       │ → reset after=null│ │
   │ cursor       │  {events,      │  │  → windowMeta  ▼ (restart page 1)  │ │
   └──────────────┘  endCursor,    │  └───┬───────────────┬────────────────┘ │
                     hasMore}      │      │ events[]      │ windowMeta        │
                                  │      ▼               ▼                   │
                                  │  ┌──────────┐  ┌──────────────────────┐ │
                                  │  │ timeline │  │ window-size indicator │ │  ◀ NON-REMOVABLE,
                                  │  │ newest-  │  │ "over N · hasMore ·   │ │    every signal
                                  │  │ first    │  │  range" (DRILL-05)    │ │    surface
                                  │  │ rows     │  └──────────────────────┘ │
                                  │  │ [Load    │  ┌──────────────────────┐ │
                                  │  │  more]   │  │ rate/burst analyzer   │ │
                                  │  └──────────┘  │ (pure, asymmetric) +  │ │
                                  │                │ forgeable caveat ◀────┼─┼─ PERSISTENT
                                  │                └──────────────────────┘ │
                                  └──────────────────────────────────────────┘
```

Data flow for the primary use case: paste → `identifier.parse()` → (throw ⇒ inline parse error, stay home) or (ok ⇒ route to `#/a/<hex>`) → `useAuthorWindow(hex)` issues `events(filter:{authors:[hex]}, limit:100)` through Phase 1's `client` → `classify()` → accumulate into a window → derive `windowMeta` + run the rate/burst analyzer → render timeline + indicator + caveat; "Load more" passes `endCursor` verbatim as `after`.

### Recommended Project Structure
```
src/
├── identifier/                 # NEW — pure, unit-tested, no UI, no network
│   ├── identifier.ts           # parse(input) → ParseResult; encode helpers; isHexPubkey
│   └── identifier.test.ts
├── analysis/                   # NEW — pure analyzers (this phase: rate/burst only)
│   ├── rate.ts                 # analyzeRate(events) → RateResult (asymmetric, bounds-checked)
│   ├── rate.test.ts
│   └── thresholds.ts           # single tunable module for burst constants (CONTEXT discretion)
├── hooks/
│   ├── useAuthorWindow.ts      # NEW — paginate + accumulate + windowMeta + loadMore
│   └── useStatsPoll.ts         # (existing)
├── queries/
│   ├── events.graphql.ts       # NEW — EventsDocument (codegen-typed)
│   └── stats.graphql.ts        # (existing)
├── router/
│   └── hashRouter.ts           # NEW — tiny hash route ('#/' | '#/a/<hex>')
├── transport/                  # (existing — reuse verbatim) client, classify, accumulatePages, waitForReady
├── views/
│   ├── AuthorDrillDown.tsx     # NEW — drill-down shell, timeline, panels
│   ├── AuthorDrillDown.module.css
│   ├── SuspectEntryBar.tsx     # NEW — paste bar (shell header)
│   ├── SuspectEntryBar.module.css
│   ├── WindowIndicator.tsx     # NEW — DRILL-05 non-removable denominator (shared)
│   ├── RatePanel.tsx           # NEW — CSS/SVG bars + persistent caveat
│   ├── StatsDashboard.tsx      # (existing — remains home)
│   └── ConnectingShell.tsx     # (existing — reuse for drill-down first load)
└── App.tsx                     # MODIFIED — mount router + shell + entry bar
```

### Pattern 1: Identifier parse with parse-vs-zero-match distinction (ID-01/02/03)
**What:** A pure function that turns any user input into a discriminated result: a normalized lowercase-hex pubkey + display forms, OR an explicit parse failure. The parse failure is the *only* thing that produces the input-level error; a successful parse always navigates, and "zero events" is decided later by the query, never here.
**When to use:** The single entry point from the paste bar; also reused if Phase 4 batch import normalizes mixed input.
**Example:**
```typescript
// Source: nip19 behavior empirically verified against nostr-tools@2.23.8 this session.
// nip19.decode() THROWS on invalid input; returns {type, data} on success.
import { nip19 } from 'nostr-tools'

const HEX64 = /^[0-9a-f]{64}$/

export type ParseResult =
  | { ok: true; hex: string; npub: string; sourceKind: 'hex' | 'npub' | 'note' | 'nprofile' }
  | { ok: false; reason: 'EMPTY' | 'NOT_RECOGNIZED' | 'REJECTED_NSEC' }

export function parseIdentifier(raw: string): ParseResult {
  const input = raw.trim()
  if (!input) return { ok: false, reason: 'EMPTY' }

  // 1. Bare 64-char hex — normalize case, then validate the lowercase form.
  const lower = input.toLowerCase()
  if (HEX64.test(lower)) {
    return { ok: true, hex: lower, npub: nip19.npubEncode(lower), sourceKind: 'hex' }
  }

  // 2. bech32 forms — decode() THROWS on malformed input (this is the parse-failure branch).
  try {
    const decoded = nip19.decode(input)          // {type, data}
    switch (decoded.type) {
      case 'npub':                                // data = hex pubkey string
        return { ok: true, hex: decoded.data, npub: nip19.npubEncode(decoded.data), sourceKind: 'npub' }
      case 'nprofile': {                          // data = { pubkey, relays[] }
        const hex = decoded.data.pubkey
        return { ok: true, hex, npub: nip19.npubEncode(hex), sourceKind: 'nprofile' }
      }
      case 'note':                                // data = hex EVENT id — see policy decision below
        // POLICY (recommended): a note is an event id, NOT a pubkey. See Open Question 1.
        return { ok: false, reason: 'NOT_RECOGNIZED' } // OR resolve via event lookup (heavier)
      case 'nsec':                                // SECURITY: a secret key — never accept/route/store
        return { ok: false, reason: 'REJECTED_NSEC' }
      default:
        return { ok: false, reason: 'NOT_RECOGNIZED' }
    }
  } catch {
    return { ok: false, reason: 'NOT_RECOGNIZED' }  // decode threw ⇒ genuine parse failure
  }
}
```
**Verified facts (this session, against `nostr-tools@2.23.8`):**
- `nip19.decode('npub1…')` → `{type:'npub', data:'<64hex>'}` `[VERIFIED]`
- `nip19.decode('note1…')` → `{type:'note', data:'<64hex>'}` (event id, not pubkey) `[VERIFIED]`
- `nip19.decode('nprofile1…')` → `{type:'nprofile', data:{pubkey:'<64hex>', relays:[…]}}` `[VERIFIED]`
- `nip19.decode('npub1garbage')` → **throws** `Error: Unknown letter…` `[VERIFIED]`
- `nip19.decode('not bech32')` → **throws** `[VERIFIED]`
- `nip19.npubEncode('<64hex>')` → `'npub1…'` `[VERIFIED]`
- `nip19.decode('nsec1…')` → `{type:'nsec', …}` succeeds — so the module MUST explicitly reject it. `[VERIFIED]`

### Pattern 2: `useAuthorWindow` — paginate + accumulate + windowMeta + loadMore (DRILL-05/06)
**What:** A hook that wraps Phase 1's transport: one constant filter `{authors:[hex]}`, an explicit `limit` (100 per CONTEXT), accumulate pages, derive `windowMeta`, expose `loadMore()` and live-re-derived analytics. It re-implements the cursor discipline that `accumulatePages` documents but for *incremental* (button-driven) loading rather than load-all.
**When to use:** The drill-down's data source. (Note: `accumulatePages` loads *all* pages in a loop — good for "widen to ceiling"; for button-driven "Load more" you call one page per click. The plan should decide: reuse `accumulatePages` for a "load to 500 ceiling" action, OR add a sibling `fetchNextPage` single-step. Both honor the same opaque-cursor + INVALID_CURSOR rules.)
**Example:**
```typescript
// Source: contract §6.1 (events args, opaque cursor, INVALID_CURSOR), §12 (limit ceiling 500),
// Phase 1 src/transport/{client,errors,paginate}.ts. Filter is CONSTANT across pages.
import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../transport/client'
import { classify, type ApiError } from '../transport/errors'
import { EventsDocument } from '../queries/events.graphql'

const PAGE_LIMIT = 100  // CONTEXT: 100/page, append toward the 500 ceiling

export interface WindowEvent { kind: number; createdAt: number; content: string; id: string }
export interface WindowMeta {
  count: number               // N fetched events (the denominator)
  hasMore: boolean            // partial window if true → NEVER read as clean
  oldest: number | null       // min createdAt in window
  newest: number | null       // max createdAt in window
}

export function useAuthorWindow(hex: string) {
  const [events, setEvents] = useState<WindowEvent[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  const [loading, setLoading] = useState(true)
  const [hasMore, setHasMore] = useState(false)
  const after = useRef<string | null>(null)   // opaque cursor — passed back VERBATIM

  const fetchPage = useCallback(async (cursor: string | null) => {
    // CONSTANT filter across every page (contract §6.1 cursor rule).
    const result = await client
      .query(EventsDocument, { filter: { authors: [hex] }, after: cursor, limit: PAGE_LIMIT })
      .toPromise()
      .catch(() => 'THREW' as const)
    if (result === 'THREW') { setError({ kind: 'NETWORK' }); return }
    const apiError = classify(result)
    if (apiError) {
      if (apiError.kind === 'INVALID_CURSOR') {   // drop cursor, RESTART page 1
        after.current = null
        setEvents([])
        return fetchPage(null)
      }
      setError(apiError); return
    }
    const page = result.data?.events
    if (page) {
      setEvents((prev) => (cursor === null ? page.events : [...prev, ...page.events]))
      after.current = page.endCursor ?? null
      setHasMore(page.hasMore)
      setError(null)
    }
  }, [hex])

  useEffect(() => {                              // reset + first page when author changes
    after.current = null; setEvents([]); setLoading(true)
    void fetchPage(null).finally(() => setLoading(false))
  }, [hex, fetchPage])

  const loadMore = useCallback(() => {
    if (!hasMore) return
    setLoading(true)
    void fetchPage(after.current).finally(() => setLoading(false))
  }, [hasMore, fetchPage])

  // windowMeta derived live as the window widens (pure — see deriveWindowMeta).
  const windowMeta: WindowMeta = deriveWindowMeta(events, hasMore)
  return { events, windowMeta, error, loading, hasMore, loadMore }
}
```
> Note the **400 client → react-state mapping** edge: because cursor restart re-fetches inside the same hook, a fast double-click on "Load more" can race; gate `loadMore` on `loading` (or track an in-flight ref) so two page requests don't append twice. (Pitfall 4.)

### Pattern 3: Asymmetric rate/burst analyzer (DRILL-01) — pure, bounds-checked
**What:** A pure function over the fetched window's `createdAt` values that bins posting activity and flags burst windows, with explicit asymmetry: it returns a `burstDetected` signal but **never** a "clean" verdict, and it carries the count of analyzable events so the caller can show the denominator.
**When to use:** Fed by `useAuthorWindow`'s `events`; re-runs whenever the window widens.
**Example:**
```typescript
// Source: contract §8 (createdAt author-claimed, 64-bit, may collide; events newest-first).
// Heuristic grounded in bot-detection literature: tight interarrival clustering is the
// strongest single temporal automation signal (see Sources). thresholds.ts is the single
// tunable home (CONTEXT discretion). Bounds-checked: createdAt is a 64-bit Int (contract §8).
import { BURST } from './thresholds'

// Author-claimed seconds. Reject implausible values rather than mis-compute (CONTEXT security).
const MIN_TS = 0
const MAX_TS = 4_102_444_800           // 2100-01-01 — anything beyond is forged/garbage
const isSaneTs = (t: number) =>
  Number.isSafeInteger(t) && t >= MIN_TS && t <= MAX_TS

export interface RateResult {
  analyzedCount: number                // denominator — events with a SANE createdAt
  rejectedCount: number                // out-of-range timestamps flagged, not silently dropped
  bins: { start: number; count: number }[]   // for the CSS/SVG bars
  burstDetected: boolean               // TRUE = suspicious; FALSE = INCONCLUSIVE, never "clean"
  tightestIntervalSec: number | null   // smallest gap between consecutive sane events
}

export function analyzeRate(createdAts: number[]): RateResult {
  const sane = createdAts.filter(isSaneTs).sort((a, b) => a - b)   // ascending for intervals
  const rejectedCount = createdAts.length - sane.length
  if (sane.length < 2) {
    return { analyzedCount: sane.length, rejectedCount, bins: [], burstDetected: false, tightestIntervalSec: null }
  }
  // Interarrival gaps; a burst = K events within a short window.
  let tightest = Infinity
  let burst = false
  for (let i = 1; i < sane.length; i++) {
    const gap = sane[i] - sane[i - 1]
    if (gap < tightest) tightest = gap
  }
  // Sliding-window burst: >= BURST.minEvents within BURST.windowSec.
  for (let i = 0; i < sane.length; i++) {
    let j = i
    while (j < sane.length && sane[j] - sane[i] <= BURST.windowSec) j++
    if (j - i >= BURST.minEvents) { burst = true; break }
  }
  return {
    analyzedCount: sane.length,
    rejectedCount,
    bins: binByInterval(sane, BURST.binSec),
    burstDetected: burst,
    tightestIntervalSec: tightest === Infinity ? null : tightest,
  }
}
```
```typescript
// analysis/thresholds.ts — the single tunable home (CONTEXT: "where burst constants live").
// Sane DEFAULTS only; corpus-validation of these is explicitly deferred to Phase 3 (STATE).
export const BURST = {
  windowSec: 60,     // bot-detection literature: ~42% of programmatic posts are <60s apart
  minEvents: 5,      // >= 5 posts within the window = a burst worth flagging
  binSec: 3600,      // 1-hour bins for the rate bars (display granularity)
} as const
```

### Pattern 4: Non-removable window-size indicator (DRILL-05)
**What:** A presentational component that takes `WindowMeta` and renders the denominator string. It has **no dismiss prop and no hidden state** — it renders even at `N = 0`. When `hasMore` is true, the "partial window" segment is amber (recoverable) so it cannot be missed.
**Example:**
```tsx
// Source: UI-SPEC §"Window-size honesty indicator"; copy strings are VERBATIM from the UI-SPEC.
const fmt = new Intl.NumberFormat()
const utc = (s: number) => new Date(s * 1000).toISOString().replace('T', ' ').slice(0, 19) + 'Z'

export function WindowIndicator({ meta }: { meta: WindowMeta }) {
  if (meta.count === 0) {
    return <p className={styles.indicator}>Computed over 0 fetched events · no events in window</p>
  }
  const range = `${utc(meta.oldest!)} → ${utc(meta.newest!)}`
  return (
    <p className={styles.indicator}>
      Computed over {fmt.format(meta.count)} fetched events ·{' '}
      {meta.hasMore
        ? <span className={styles.partial}>more available — partial window</span>   // amber
        : <span>full window</span>}{' '}
      · {range}
    </p>
  )
}
```

### Pattern 5: Minimal hash router
**What:** A `useHashRoute()` hook reading `window.location.hash`, subscribing to `hashchange`, returning `{ route: 'home' | 'author', hex }`. No library. Works on static `vite build`.
**Example:**
```typescript
// Source: CONTEXT "tiny hash/path switch, NOT a data-router". MDN hashchange event.
import { useEffect, useState } from 'react'
const HEX64 = /^[0-9a-f]{64}$/
export type Route = { name: 'home' } | { name: 'author'; hex: string } | { name: 'notfound' }
function parseHash(): Route {
  const h = window.location.hash
  if (h === '' || h === '#/' || h === '#') return { name: 'home' }
  const m = /^#\/a\/([0-9a-f]{64})$/.exec(h)            // already-normalized lowercase hex only
  return m && HEX64.test(m[1]) ? { name: 'author', hex: m[1] } : { name: 'notfound' }
}
export function useHashRoute(): Route {
  const [route, setRoute] = useState<Route>(parseHash)
  useEffect(() => {
    const on = () => setRoute(parseHash())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return route
}
// Navigation = `window.location.hash = '#/a/' + hex` (set only AFTER parse succeeds).
```

### Anti-Patterns to Avoid
- **Treating "valid identifier, zero events" as a parse error (or vice-versa).** These are two *distinct* states (ID-03). Parse failure stays on the dashboard with an inline amber input error; zero-match navigates to the drill-down and shows a neutral, calm empty state *with the window indicator still present*. Conflating them lets a typo read as a clean author.
- **Any green / "clean" / success state in the rate panel.** Asymmetry means no-burst ⇒ inconclusive, never exonerating. No success color anywhere in that panel (UI-SPEC).
- **Parsing/constructing/cross-using the opaque cursor.** Pass `endCursor` back verbatim as `after`; on `INVALID_CURSOR`, reset to `after=null` and restart page 1 (never hand-build a cursor).
- **Reading `result.data` before `classify()`.** Errors arrive on HTTP 200; always branch on the union first (Phase 1 boundary).
- **`dangerouslySetInnerHTML` for content/IDs.** Render escaped plaintext (React default). Event `content` is attacker-controlled.
- **Treating `note` as an author.** `note` decodes to an *event id*, not a pubkey; silently querying `authors:[<eventId>]` yields a guaranteed zero-match that looks like "clean author" — exactly the ID-03 failure mode. (See Open Question 1.)
- **Accepting `nsec`.** It decodes successfully but is a *secret key*; routing it puts a private key in the URL/history. Reject explicitly.
- **Floating `nostr-tools` on `^`.** Pin exact in a forensic tool.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| bech32 + NIP-19 TLV decode | A custom bech32 decoder + nprofile TLV parser | `nostr-tools` `nip19` | TLV encoding (nprofile) and bech32 checksum are easy to get subtly wrong; the canonical lib is audited and matches NIP-19 exactly |
| Cursor pagination loop | A new accumulator | `src/transport/paginate.ts` (`accumulatePages`) | Already scaffolded in Phase 1 for exactly this; honors opaque-cursor + hasMore discipline |
| Error taxonomy | New error handling at the call site | `src/transport/errors.ts` (`classify`) | 7-kind union already models INVALID_CURSOR/503/413/validation/internal/network |
| Readiness gate | A new connecting state | `waitForReady` + `ConnectingShell` | Phase 1 owns the bounded-backoff probe and the single connecting shell |
| Typed query | Hand-written types | `graphql()` codegen fn → `EventsDocument` | Generates `data.events.events[].createdAt: number` from the SDL |
| Number formatting | Manual grouping | `Intl.NumberFormat` | Phase 1 convention; locale grouping for `N` |

**Key insight:** Phase 2 is ~80% composition of existing transport primitives plus three small *pure* modules. The only new dependency is `nostr-tools`; everything else (routing, charts, pagination, errors) is either inherited or deliberately hand-rolled per project constraints.

## Common Pitfalls

### Pitfall 1: `note`/`nprofile` decode to the wrong hex meaning
**What goes wrong:** `note` → event id; `nprofile` → `{pubkey, relays}` (pubkey nested, not at `data` directly). Code that does `nip19.decode(x).data` uniformly will pass an event id (from `note`) or an object (from `nprofile`) into `authors:[…]`.
**Why it happens:** All three "look the same" (32-byte hex) and the docs gloss the per-type `data` shape.
**How to avoid:** Branch on `decoded.type`; for `nprofile` read `.data.pubkey`; decide `note` policy explicitly (recommend reject — Open Question 1). Unit-test each type.
**Warning signs:** A pasted `nprofile` produces `[object Object]` in the URL; a `note` produces a zero-match drill-down for a valid-looking id.

### Pitfall 2: Partial window read as exoneration
**What goes wrong:** Analyst fetches 100 of 5,000 events, sees no burst, concludes "clean."
**Why it happens:** No visible denominator; quiet first page looks definitive.
**How to avoid:** The non-removable window indicator with amber "partial window" when `hasMore`; the rate panel has no clean state; the forgeability caveat is permanent.
**Warning signs:** Any UI path where the rate panel renders without the indicator + caveat both present.

### Pitfall 3: Forged `createdAt` mis-computes the rate
**What goes wrong:** A forged far-future or year-0 timestamp blows up the time range / bin math, or a negative gap appears.
**Why it happens:** `createdAt` is author-claimed and forgeable (contract §8); also 64-bit, so values can exceed JS safe-integer.
**How to avoid:** `isSaneTs` bounds-check (`Number.isSafeInteger` + `[0, 2100]` range); count rejects separately (`rejectedCount`) and surface them rather than silently dropping; sort before computing interarrival gaps.
**Warning signs:** `Invalid Date` in the range string; a negative or absurd `tightestIntervalSec`.

### Pitfall 4: "Load more" double-fire / cursor race
**What goes wrong:** Rapid clicks issue two concurrent next-page requests; events appended twice or cursor advanced twice.
**Why it happens:** `after` is a ref advanced inside an async fetch; a second click reads the stale ref.
**How to avoid:** Disable/gate `loadMore` while `loading`; track an in-flight flag; the UI already shows a "Loading…" disabled state (UI-SPEC).
**Warning signs:** Duplicate event rows; skipped pages.

### Pitfall 5: GET vs POST regression
**What goes wrong:** A new query path issues GET and lands on the GraphiQL HTML IDE, surfacing as a spurious NETWORK error.
**Why it happens:** urql defaults to GET for queries; the lens only executes on POST.
**How to avoid:** Route everything through the existing `client` (`preferGetMethod:false`). Never `fetch('/graphql')` ad hoc.
**Warning signs:** HTML in a response; parse error on a query that works in curl.

### Pitfall 6: Hash route accepts non-normalized input
**What goes wrong:** `#/a/<UPPERCASE or npub>` reaches the drill-down un-normalized; query in non-lowercase-hex silently never matches (contract §8).
**How to avoid:** The router regex matches lowercase 64-hex only; navigation sets the hash only *after* `parseIdentifier` normalizes; a non-matching hash → `notfound`.
**Warning signs:** A pasted-into-URL npub yields a zero-match drill-down.

## Code Examples

(See Patterns 1–5 above — each is a verified or contract-grounded example.)

### Events query document (codegen-typed)
```typescript
// Source: contract §4/§6.1 schema. Select ONLY rendered fields (skip raw/sig — contract §9.6).
// createdAt/kind are 64-bit Int but stay number-typed via codegen (contract §8; v1 cheap bounds).
import { graphql } from '../gql'
export const EventsDocument = graphql(`
  query Events($filter: EventFilterInput, $after: String, $limit: Int) {
    events(filter: $filter, after: $after, limit: $limit) {
      events { id pubkey kind createdAt content }
      endCursor
      hasMore
    }
  }
`)
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `note`/`npub` confusion in early Nostr clients | NIP-19 distinct prefixes + TLV (`nprofile`/`nevent`) | NIP-19 standardized | Must branch on `decoded.type`, not treat all bech32 as pubkeys |
| 32-bit GraphQL `Int` for timestamps | 64-bit Int scalar (truncation-safe) | contract v1.x | Treat `kind`/`createdAt` as number/bigint-safe; v1 uses cheap bounds-check (ROB-01 defers true bigint) |

**Deprecated/outdated:**
- Constructing/parsing cursors — never supported; opaque only.
- `orderBy` — does not exist; ordering is fixed `createdAt DESC, levId DESC`; sort client-side if another order is needed.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `note` should be rejected as "not a pubkey" rather than resolved to its author via an event lookup | Pattern 1 / Open Q1 | If users expect pasting a `note` to find its author, reject is wrong UX; but silently treating an event id as a pubkey is worse (false zero-match). Needs user confirmation. |
| A2 | Burst defaults (`windowSec:60`, `minEvents:5`, `binSec:3600`) are sane for this corpus | Pattern 3 / thresholds | Mis-tuned thresholds over/under-flag bursts; explicitly deferred to Phase 3 corpus validation (STATE). Honesty posture (denominator + caveat) holds regardless. |
| A3 | Hash routing (vs History API) is acceptable | Pattern 5 | If shareable non-hash URLs are required later (OUT-02 v2), a migration is needed; but CONTEXT explicitly favors a tiny hash switch. |
| A4 | Selecting `id pubkey kind createdAt content` (omitting `raw`/`sig`/`tags`) is sufficient for the timeline | Events document | Phase 3 adds tag/kind/raw panels; if the timeline needs `tags` for a preview, add later. `raw` deliberately omitted (large payload, contract §9.6). |
| A5 | `nostr-tools` tree-shakes so importing only `nip19` keeps the bundle reasonable | Stack / Alternatives | If bundle bloats, switch to `@scure/base` + hand-rolled TLV; lower priority for a local-dev tool. |
| A6 | Exact version `2.23.8` is safe despite being a 1-day-old patch of an old package | Legitimacy audit | A brand-new patch *could* carry a regression; mitigated by human-verify checkpoint + option to pin a slightly older stable line. |

## Open Questions (RESOLVED)

> All three resolved 2026-06-24 during plan-phase; the plans implement these resolutions.
> - **Q1 → RESOLVED: reject `note`** (option a). Adopted by 02-01. ROADMAP/CONTEXT wording reconciled to match. User-confirmed.
> - **Q2 → RESOLVED: single-step page fetch** per click (matches UI-SPEC). Adopted by 02-02.
> - **Q3 → RESOLVED: house thresholds in `thresholds.ts`**, corpus-validation deferred to Phase 3. Adopted by 02-03.

1. **What does pasting a `note` (event-id bech32) do?**
   - What we know: `note` decodes to a 32-byte event id, not a pubkey. The success criteria list `note` as an accepted entry form, but the tool is author-centric (entry is "a single pubkey").
   - What's unclear: Should a `note` be (a) rejected as "not an author identifier," or (b) resolved — fetch the event by `ids:[<id>]`, read its `pubkey`, then drill into that author? Option (b) is a real feature (paste a spammy note → inspect its author) but adds an event-lookup step and an error path (event not in corpus).
   - Recommendation: **Default to (a) reject with a clear message** for this MVP slice (keeps the pure module pure and the slice small), and capture (b) as a deferred enhancement — UNLESS discuss confirms users will paste notes. The planner should surface this as a `checkpoint` if (b) is desired, since it changes the `identifier/` contract and adds a query.

2. **`accumulatePages` (load-all) vs single `fetchNextPage` for "Load more"?**
   - What we know: `accumulatePages` loops to exhaustion; the UI-SPEC "Load more" is a per-click single page toward a 500 ceiling.
   - Recommendation: Use **single-step page fetch** for the button (one page per click, matching the UI-SPEC), and optionally a "load to ceiling" action that reuses `accumulatePages`. Both share the opaque-cursor/INVALID_CURSOR rules. Low risk; a plan-time decision.

3. **Burst thresholds** — sane defaults now, corpus-validated in Phase 3 (STATE blocker). No action needed this phase beyond housing them in `thresholds.ts`.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Node + npm | install, codegen, test | ✓ | (Phase 1 toolchain) | — |
| `nostr-tools` | identifier module | ✗ (not yet installed) | install `2.23.8` | none — required; `@scure/base` is a heavier-to-use alternative |
| LMDB2GraphQL lens | live `events` query | ✓ (Phase 1 proved) | v1.2 @ `VITE_GRAPHQL_URL` | codegen reads checked-in SDL; live query needs the lens running |
| vitest (Node env) | pure-module tests | ✓ | `^3.2.6` | — |

**Missing dependencies with no fallback:** `nostr-tools` (install step required; gate behind the legitimacy human-verify checkpoint).
**Missing dependencies with fallback:** none material.

## Validation Architecture

> `workflow.nyquist_validation` is `false` in `.planning/config.json`. **Section omitted per the skip condition** — Phase 1's vitest infra (Node env, `src/**/*.test.ts`) is nonetheless the home for the new pure-module tests (`identifier.test.ts`, `rate.test.ts`), and the planner SHOULD include unit tests for the pure modules regardless (they are the highest-value, lowest-cost coverage in this phase).

## Security Domain

`security_enforcement: true`, ASVS L1, block-on `high`.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | API is unauthenticated, read-only (contract §1) |
| V3 Session Management | no | No sessions/cookies; wildcard CORS, no credentials |
| V4 Access Control | no | No authz surface; single local analyst |
| V5 Input Validation | **yes** | `parseIdentifier` validates/normalizes all input; hex regex; `isSaneTs` bounds-check on 64-bit `createdAt`; router accepts lowercase-64hex only |
| V6 Cryptography | no (consume only) | No crypto authored; `nostr-tools` crypto primitives consumed, sigs already verified by strfry (contract §5 — do not re-verify) |
| V7 Error Handling | **yes** | Reuse `classify`: INTERNAL carries no server message; VALIDATION shown verbatim (user-safe per contract §7); never leak internals |
| V5/V14 Output Encoding | **yes** | Render `content`/`createdAt`/ids/`pinnedStrfryVersion` as escaped plaintext (React default); never `dangerouslySetInnerHTML` |

### Known Threat Patterns for {React SPA + read-only GraphQL}

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| XSS via attacker-controlled event `content` | Tampering | Escaped plaintext via JSX; no `dangerouslySetInnerHTML`; single-line truncated preview |
| Secret-key (`nsec`) entered into a URL/history | Information disclosure | `parseIdentifier` explicitly rejects `nsec`; navigation only on a valid pubkey |
| Forged `createdAt` corrupting analysis / DoS via absurd values | Tampering / DoS | `isSaneTs` bounds-check; flag (`rejectedCount`) rather than mis-compute; no unbounded loops over forged ranges |
| Mis-targeted queries to an untrusted lens | Spoofing | `VITE_GRAPHQL_URL` single source; point only at a trusted lens (inherited V14, T-01-02) |
| Self-inflicted DoS (aggressive paging) | DoS | Button-driven single-page loads; explicit `limit` 100; 500 ceiling; no auto-loop on render |
| Leaking server internals in errors | Information disclosure | `classify` INTERNAL branch carries no message (inherited Phase 1) |

## Project Constraints (from CLAUDE.md)

From repo-root `/Users/g/git/deepfry/CLAUDE.md` and project scope:
- **Project boundary:** This is the GraphQLExplorer project under `spamhunter/`. Scope ALL work to it — do not touch sibling deepfry projects.
- **Commit to `main`, no feature branches** (deepfry convention + `config.json` `branching_strategy: none`).
- **Read-only posture:** the tool never writes/publishes events; no Mutation/Subscription exists (contract §1, REQUIREMENTS Out of Scope).
- **Secrets/config:** `VITE_GRAPHQL_URL` via `.env` (gitignored); never hardcode the LAN lens address in committed source (Phase 1 invariant).
- `claude_md_path` is `./.claude/CLAUDE.md` per `config.json` — no project-local `.claude/CLAUDE.md` content beyond the monorepo root was found governing src conventions; the inherited conventions are the Phase 1 transport/UI-SPEC patterns documented above.

## User Constraints (from CONTEXT.md)

### Locked Decisions
- **URL routing added now:** lowercase hex in the URL (`#/a/<hex>`), bookmarkable/shareable, back-button works; **minimal hash/path switch, NOT a data-router/loader framework**.
- **Suspect entry = a paste bar in the app shell/header**; the stats dashboard remains home/landing; entering a suspect routes to the drill-down. No replace-the-dashboard landing page.
- **Timeline page size = 100/page** (`events.limit` default); "Load more" appends toward the 500 ceiling; every query passes an explicit `limit`.
- Accept `npub` / `note` / `nprofile` bech32 AND 64-char hex; normalize to **lowercase hex**; **query in hex**; **display both forms**. Use `nostr-tools` (nip19) — **install in this phase**.
- **ID-03 distinction:** "couldn't parse" (malformed → inline validation error) vs "valid identifier, zero matching events" (neutral distinct empty state). A typo must NEVER read as a clean author.
- **Timeline:** newest-first across ALL kinds (no kind filter); ordering fixed by contract (`createdAt` DESC, `levId` DESC — no `orderBy`).
- **Asymmetric burst:** burst present = suspicious; burst ABSENT ≠ clean; persistent **"createdAt is author-claimed and forgeable"** caveat beside the rate chart.
- **Window-honesty indicator (DRILL-05):** every signal surface shows a **non-removable** "computed over N fetched events · hasMore · time range".
- **Pagination (DRILL-06):** cursor pagination with a **constant filter**; opaque cursor passed **verbatim**; `INVALID_CURSOR` restarts from page 1 (reuse `transport/paginate.ts`); analytics re-derive live.
- **Charts:** lightweight hand-rolled CSS/SVG rate bars — no heavy chart lib.
- **Security:** render content/`createdAt`/identifiers as **escaped plaintext** (React default); never `dangerouslySetInnerHTML`; 64-bit `kind`/`createdAt` bounds-aware in rate math (flag out-of-range rather than mis-compute).

### Claude's Discretion
- Exact routing mechanism (hash vs History API) and drill-down component decomposition. → **Recommend hash routing** (static-build friendly).
- Rate-chart visual form (sparkline vs bars) within the CSS/SVG constraint.
- Where the burst-default constants live (a single tunable module), pending research. → **Recommend `src/analysis/thresholds.ts`**.

### Deferred Ideas (OUT OF SCOPE)
- Duplicate/near-dup content, tag/mention fan-out, kind distribution, raw-JSON inspector — **Phase 3** (DRILL-02/03/04).
- Batch list import + multi-author triage table + `authors` enumeration query — **Phase 4** (BATCH-01..04).
- Exact near-dup / burst threshold tuning beyond Phase 2's rate signal — **Phase 3** validates against corpus.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| ID-01 | Paste npub or 64-char hex, open that author's drill-down | Pattern 1 (`parseIdentifier`) + Pattern 5 (router) — bech32/hex → normalized hex → `#/a/<hex>` |
| ID-02 | Normalize npub/note bech32 ↔ hex (nip19), query in lowercase hex, display both forms | Pattern 1 — `nip19.decode`/`npubEncode` (verified); identity header shows npub + hex (UI-SPEC) |
| ID-03 | Distinguish "couldn't parse" from "valid identifier, zero matching events" | Pattern 1 (`ParseResult` discriminated union; throw ⇒ parse failure) + zero-match neutral empty state (UI-SPEC); Anti-pattern + Pitfall 1/6 |
| DRILL-01 | Timeline newest-first across kinds + asymmetric burst/rate indicator; forgeable caveat | Pattern 3 (analyzer, asymmetric, bounds-checked) + Events document (no kind filter, fixed order) + persistent caveat (UI-SPEC) |
| DRILL-05 | Non-removable window-size indicator on every signal surface | Pattern 2 (`windowMeta`) + Pattern 4 (`WindowIndicator`, no dismiss, renders at N=0) |
| DRILL-06 | Load more (cursor pagination, constant filter) to widen the window; analytics re-derive live | Pattern 2 (`useAuthorWindow`: constant filter, verbatim cursor, INVALID_CURSOR restart, live re-derive) |

## Sources

### Primary (HIGH confidence)
- `contract.md` (LMDB2GraphQL v1.2, code-verified) — schema §4/§5, `events` args/cursors §6.1, errors §7, data semantics §8 (author-claimed/64-bit `createdAt`, fixed ordering), best practices §9, limits §12.
- Phase 1 source — `src/transport/{client,errors,paginate,readiness,config}.ts`, `src/hooks/useStatsPoll.ts`, `src/views/{StatsDashboard,ConnectingShell}.tsx`, `codegen.ts`, `schema.graphql`, `src/styles/tokens.css`.
- `02-CONTEXT.md`, `02-UI-SPEC.md` (approved 6/6), `.planning/REQUIREMENTS.md`, `.planning/STATE.md`.
- **`nostr-tools@2.23.8` nip19 API — empirically verified this session** (decode/encode return shapes, throw-on-invalid, nsec decodes): npub→`{type,data:hex}`, note→`{type,data:hex(eventId)}`, nprofile→`{type,data:{pubkey,relays}}`, garbage→throws.
- `npm view nostr-tools` — version `2.23.8`, created 2021-01-04, 660,704 wk downloads, repo `github.com/nbd-wtf/nostr-tools`, no postinstall.

### Secondary (MEDIUM confidence)
- Bot/spam temporal-detection literature — interarrival-time clustering as the strongest single temporal automation signal; ~42% of programmatic posts <60s apart vs ~17% web; both extreme-uniformity and extreme-burstiness flag automation. ([getstream.io](https://getstream.io/blog/bot-detection-moderation/), [Springer review](https://link.springer.com/article/10.1007/s00521-023-08352-z))
- Forensic false-negative / "absence of evidence" framing — confirms the honest-denominator posture: failure-to-find is routinely mis-read as evidence of absence. ([ScienceDirect](https://www.sciencedirect.com/science/article/abs/pii/S0379073818304870), [arXiv 2412.05398](https://arxiv.org/pdf/2412.05398))

### Tertiary (LOW confidence)
- Burst threshold *defaults* (`60s` / `5 events` / `1h bins`) — sane starting points from the literature; corpus-validation deferred to Phase 3 (STATE).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all but `nostr-tools` already installed/proven; nip19 API empirically verified.
- Architecture / reuse: HIGH — composes verified Phase 1 transport primitives; contract semantics code-verified.
- Identifier semantics (note/nprofile/nsec): HIGH — empirically tested this session.
- Burst thresholds: MEDIUM — sane literature-grounded defaults, not corpus-validated (deferred to Phase 3 by design).
- Honesty-UI posture: HIGH — locked by approved UI-SPEC + supported by forensic literature.

**Research date:** 2026-06-24
**Valid until:** 2026-07-24 (stable; re-verify `nostr-tools` version + the `note` policy decision before planning if delayed)

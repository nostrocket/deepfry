# Phase 4: Batch Triage - Research

**Researched:** 2026-06-25
**Domain:** Client-side batch fan-out querying (chunked `latestPerAuthor`), cursor enumeration (`authors`), left-join merge-by-key, in-browser file parsing, sortable table — all over the existing React 19 + urql + GraphQL-Codegen Phase 1–3 stack.
**Confidence:** HIGH

## Summary

This phase scales the single-suspect workflow to many authors. Almost every decision is already
locked in `04-CONTEXT.md` (16 decisions) and the approved `04-UI-SPEC.md`. The research effort is
therefore HOW to implement it correctly by reusing Phase 1–3 infrastructure, not WHAT to build.
**Zero new runtime dependencies** — `identifier/`, `transport/client` + `classify()`, the four pure
`analysis/*` analyzers, `thresholds.ts`, `WindowIndicator`, and `hashRouter` already exist and cover
the entire surface; file reading uses the platform `FileReader`/`File` API.

The single most important quantitative finding: **the ≤1000-author cap binds long before the 256 KiB
body limit for `latestPerAuthor`.** Measured empirically, a 1000-author request body is only ~66 KiB
(67 bytes marginal per 64-hex author + ~322 bytes fixed query/variables scaffold). The 256 KiB limit
would not bind until ~3900 authors — which `TOO_MANY_AUTHORS` forbids anyway. So the chunk size is
governed by `min(1000, byteBudget)` where byteBudget is effectively never the binding term at
`perAuthor=5`. The 413 path still must be implemented as a **runtime safety net** (halve-and-retry),
because the body limit is on the request and a future fatter selection or proxy framing could push it
up — but the *static* chunk size is the author cap, conservatively backed off (recommend **500**).

The genuinely new logic — and where the plan should concentrate verification — is: (1) the dual-axis
chunk-size constant + 413 halve-and-retry degrade, (2) the `authors` enumeration loop with a Stop
control and `INVALID_CURSOR` restart, (3) the **left-join merge-by-`author`-key** that surfaces
zero-match authors as explicit "0 events" rows (the response omits them entirely — confirmed by
contract §5/§8), and (4) running the four per-author analyzers over the tiny `perAuthor=5` window to
produce the transparent per-signal indicators. Everything else is composition of proven parts.

**Primary recommendation:** Build a `useLatestPerAuthor` imperative hook mirroring `useAuthorWindow`'s
classify-before-data + throw-guard + run-token discipline, a separate `useAuthorEnumeration` hook for
the `authors` loop, a pure `mergeByAuthor(inputHexes, groups)` left-join function (unit-tested to pin
"never index-zipped"), and a pure `triageAuthor(events)` adapter that runs the four analyzers and
returns the four boolean/count indicators. Chunk size constant `TRIAGE.chunkAuthors = 500`,
`TRIAGE.kind = 1`, `TRIAGE.perAuthor = 5`, `TRIAGE.largeSetWarn = 1000`, all in `thresholds.ts`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Token paste/file parse + normalize/dedupe | Browser / Client | — | Pure client-side; reuses `parseIdentifier`. No backend involvement. |
| File reading (.txt/.csv) | Browser / Client | — | Platform `FileReader`/`File` API; never uploaded to a server. |
| Author-set enumeration (`authors`) | API / Backend (read) | Client (loop+Stop) | Backend owns the distinct-pubkey index scan; client drives pagination + honesty snapshot. |
| Chunked event fetch (`latestPerAuthor`) | API / Backend (read) | Client (chunking) | Backend executes ≈authors×perAuthor scans; client owns chunk-sizing to respect caps. |
| Per-author triage indicators | Browser / Client | — | The four pure analyzers run client-side over the fetched window (no server analysis — out of scope). |
| Merge-by-author / triage table render | Browser / Client | — | Pure client join + presentational table. |
| Drill-in navigation | Browser / Client | — | Existing `#/a/<hex>` hash route — no backend, no new route. |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `@urql/core` | ^6 (6.0.3 installed) | POST-only GraphQL client; chunk + enumeration fetches | Already the project's transport (`transport/client.ts`); reuse verbatim. [VERIFIED: package.json + client.ts] |
| `graphql` | 16.14.2 (pinned) | GraphQL runtime; codegen depends on it | Pinned exact per FND-01; do NOT bump. [VERIFIED: package.json] |
| React | ^19 | Views + hooks | Existing app framework. [VERIFIED: package.json] |
| GraphQL Codegen client-preset | cli ^7 / client-preset ^6 | `graphql()` typed documents for the two new queries | Established codegen flow; the BLOCKING step. [VERIFIED: package.json + codegen.ts] |
| `nostr-tools` (`nip19`) | 2.23.8 (exact) | bech32 ↔ hex — only via `parseIdentifier` | Single sanctioned call site; never add a second. [VERIFIED: identifier.ts] |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Platform `FileReader` / `File` | — | Read uploaded .txt/.csv text in-browser | File-import source; not a library. [CITED: 04-UI-SPEC Design System] |
| `Intl.NumberFormat` | — | Locale-grouped counts in mono | Every count surface (reuse `formatInt` pattern). [VERIFIED: WindowIndicator.tsx] |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hand-rolled `<table>` + local sort | TanStack Table / a data-grid lib | UI-SPEC explicitly forbids any table/grid library (zero new deps); a ≤1000-row local sort is trivial. Don't add. |
| FileReader text parse | A CSV parser (papaparse) | Tokens split on whitespace/newline/comma then `parseIdentifier` each — no quoted-field CSV semantics needed. A parser is over-engineering for a flat pubkey list. |
| `urql` document cache | `requestPolicy: 'network-only'` | Honesty contract requires current corpus state; reuse the Phase-2 `network-only` scoping for batch fetches too (a re-triage must hit the network). |

**Installation:**
```bash
# NONE — this phase adds zero runtime and zero dev dependencies.
# After adding the two new graphql() documents, run the BLOCKING codegen step:
npm run codegen
```

**Version verification:** No packages added, so no registry verification needed. All reused packages
are already installed and pinned (`graphql` exact-pinned by a `postinstall` guard
`scripts/check-graphql-pin.cjs`). [VERIFIED: package.json]

## Package Legitimacy Audit

> **Not applicable — this phase installs ZERO external packages.** Confirmed against `04-UI-SPEC.md`
> ("New dependency: **none**") and `package.json` (no additions). All functionality composes existing
> dependencies + the platform `FileReader` API.

**Packages removed due to [SLOP] verdict:** none (no packages introduced)
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram

```
                 ┌──────────────────── #/batch view ───────────────────────┐
                 │                                                          │
  paste textarea ─┐                                                        │
  file (.txt/.csv)─┤── tokenize (whitespace/newline/comma) ──┐             │
  "enumerate" ────┘            │                             │             │
        │                      ▼                             ▼             │
        │            parseIdentifier(token)          parseIdentifier each  │
        │            per token (REUSE)                                     │
        │                      │                                           │
        ▼                      ▼                                           │
  useAuthorEnumeration   normalize→lowercase hex, DEDUPE (Set),           │
  (authors loop):        COUNT valid / dup / unparseable ─────────────────┤
   page authors(after,         │                                          │
   limit) until !hasMore       │  import summary (N valid·M dup·K unparse) │
   · Stop control              │  large-set WARN if > TRIAGE.largeSetWarn  │
   · INVALID_CURSOR restart    │                                          │
   · running count snapshot    ▼                                          │
        │             dedupedInputHexes : string[]  ◄── THE FULL INPUT SET │
        └──────────────────────┤  (left-join base — load-bearing)         │
                               ▼                                           │
                   useLatestPerAuthor (chunk loop):                        │
                     split into chunks of TRIAGE.chunkAuthors (≤1000)      │
                     for each chunk:                                       │
                       client.query(LatestPerAuthorDocument, {kind,        │
                         perAuthor, authors: chunk}) → classify() FIRST    │
                       413 → halve chunk & retry (graceful degrade)        │
                       503/INVALID/THREW → retry control, partial kept     │
                       sequential / small-bounded concurrency (pacing)     │
                     accumulate AuthorGroup[] across chunks                │
                               │                                          │
                               ▼                                          │
                  mergeByAuthor(dedupedInputHexes, allGroups):            │
                    Map<author → events>; LEFT JOIN from input set        │
                    → row per input author; missing ⇒ events:[] "0 events"│
                               │                                          │
                               ▼                                          │
                  triageAuthor(row.events) per row (REUSE analyzers):     │
                    analyzeRate → burstDetected                           │
                    nearDup → duplicateCount>0                            │
                    analyzeTags → massMention||stuffing (fan-out)         │
                    + eventCount (window denominator)                     │
                               │                                          │
                               ▼                                          │
            Sortable triage table (default: event count desc)            │
              · transparent per-signal chips (amber-on-signal)           │
              · "0 events" explicit rows · WindowIndicator "triaged N/M" │
              · row click → window.location.hash = #/a/<hex> (REUSE)     │
                 └──────────────────────────────────────────────────────┘
```

### Recommended Project Structure
```
src/
├── queries/
│   ├── latestPerAuthor.graphql.ts   # NEW graphql() document (kind, perAuthor, authors)
│   └── authors.graphql.ts           # NEW graphql() document (after, limit)
├── hooks/
│   ├── useLatestPerAuthor.ts        # NEW — chunk loop, classify-gated, 413 degrade
│   └── useAuthorEnumeration.ts      # NEW — authors loop, Stop, INVALID_CURSOR restart
├── analysis/
│   ├── triage.ts                    # NEW pure — triageAuthor(events) → indicators
│   ├── mergeByAuthor.ts             # NEW pure — left-join input set ⋈ AuthorGroup[]
│   ├── chunk.ts                     # NEW pure — chunkAuthors(hexes, size) + byte estimate
│   └── thresholds.ts                # EXTEND — add TRIAGE constants
├── router/
│   └── hashRouter.ts                # EXTEND — add { name: 'batch' } route variant
└── views/
    ├── BatchTriage.module.css       # NEW
    ├── BatchImport.tsx              # NEW — paste/file/enumerate sources + import summary
    └── TriageTable.tsx             # NEW — sortable table + per-signal indicators
```

### Pattern 1: Imperative classify-before-data fetch loop (REUSE from `useAuthorWindow`)
**What:** Every batch fetch (each chunk, each enumeration page) follows the exact discipline already
in `useAuthorWindow.ts`: `client.query(...).toPromise().catch(() => 'THREW')`, then `classify(result)`
BEFORE reading `result.data`, with a `runId`/in-flight ref to drop stale results.
**When to use:** The chunk loop and the enumeration loop both.
**Example:**
```typescript
// Source: src/hooks/useAuthorWindow.ts:133-198 (VERIFIED — reuse this exact shape)
const result = await client
  .query(LatestPerAuthorDocument, { kind, perAuthor, authors: chunk }, { requestPolicy: 'network-only' })
  .toPromise()
  .catch(() => 'THREW' as const)
if (result === 'THREW') { /* NETWORK — retain partial, offer retry */ }
const apiError = classify(result)              // BEFORE result.data (errors arrive on HTTP 200)
if (apiError) { /* branch: 413 halve, 503/INVALID retry, INTERNAL hard-fail */ }
const groups = result.data?.latestPerAuthor    // only now safe to read
```

### Pattern 2: Dual-axis chunk sizing (NEW — but the math is decided below)
**What:** Size each chunk by `min(TRIAGE.chunkAuthors, byteBudgetAuthors)`. The byte budget is a pure
function of a conservative per-author byte constant; in practice it never binds at `perAuthor=5`.
**When to use:** Splitting the deduped input/enumerated set before the chunk loop.
**Example:**
```typescript
// Source: NEW src/analysis/chunk.ts — math VERIFIED empirically (see Code Examples)
export const SAFE_BYTES_PER_AUTHOR = 80   // measured 67; round up for UTF-8/whitespace/proxy framing
export const SAFE_FIXED_OVERHEAD = 4096   // generous query-doc + header margin vs measured 322
export const BODY_LIMIT_BYTES = 256 * 1024
export function byteBudgetAuthors(): number {
  return Math.floor((BODY_LIMIT_BYTES - SAFE_FIXED_OVERHEAD) / SAFE_BYTES_PER_AUTHOR) // ≈3225
}
export function chunkSize(): number {
  return Math.min(TRIAGE.chunkAuthors /* 500 */, 1000 /* hard cap */, byteBudgetAuthors())
}
// ⇒ 500 in practice. The author cap (1000) binds far before the byte budget (~3225).
```

### Pattern 3: 413 graceful-degrade (halve-and-retry) — NEW runtime safety net
**What:** If a chunk returns `PAYLOAD_TOO_LARGE` despite static sizing, halve that chunk and re-issue
the two halves; bottom out at chunk size 1 to avoid infinite recursion.
**When to use:** The `classify()` branch for `kind === 'PAYLOAD_TOO_LARGE'` inside the chunk loop.
**Example:**
```typescript
// Source: 04-UI-SPEC "413 → That chunk was too large — shrinking and retrying." + contract §7
async function fetchChunk(chunk: string[]): Promise<AuthorGroup[]> {
  const result = await query(chunk)
  const err = classify(result)
  if (err?.kind === 'PAYLOAD_TOO_LARGE' && chunk.length > 1) {
    const mid = Math.ceil(chunk.length / 2)
    return [...await fetchChunk(chunk.slice(0, mid)), ...await fetchChunk(chunk.slice(mid))]
  }
  // ...other branches; on success return result.data.latestPerAuthor
}
```

### Pattern 4: Left-join merge-by-author (NEW — load-bearing honesty, BATCH-03)
**What:** Build `Map<author, Event[]>` from all returned `AuthorGroup`s, then iterate the FULL deduped
INPUT set to produce one row per input author. A missing author in the map ⇒ `events: []` ⇒ explicit
"0 events" row. NEVER index-zip the response array against the input array.
**When to use:** After all chunks resolve (and incrementally as chunks stream in).
**Example:**
```typescript
// Source: contract §5/§8 ("authors with zero matching events are omitted; match by author, don't
// zip by index") + 04-CONTEXT BATCH-03. Pin "never index-zipped" with a comment + a unit test.
export interface TriageRow { author: string; events: WindowEvent[] }
export function mergeByAuthor(inputHexes: string[], groups: { author: string; events: WindowEvent[] }[]): TriageRow[] {
  const byAuthor = new Map<string, WindowEvent[]>()
  for (const g of groups) byAuthor.set(g.author, g.events)   // key strictly by author
  return inputHexes.map((hex) => ({ author: hex, events: byAuthor.get(hex) ?? [] })) // LEFT join
}
```

### Pattern 5: Per-author triage adapter (REUSE the four analyzers)
**What:** A pure function mapping a row's small event window to the four transparent indicators. Reuse
the existing analyzer signatures verbatim; do NOT re-implement detection.
**When to use:** Per row when rendering the table (or precomputed once per row after merge).
**Example:**
```typescript
// Source: src/analysis/{rate,nearDup,tags}.ts (VERIFIED signatures). isSaneTs already applied
// INSIDE analyzeRate/analyzeKinds — callers pass raw createdAt, the analyzer filters forgeries.
import { analyzeRate } from './rate'
import { nearDup } from './nearDup'
import { analyzeTags } from './tags'
export interface TriageIndicators {
  eventCount: number; burst: boolean; nearDup: boolean; tagFanOut: boolean
}
export function triageAuthor(events: WindowEvent[]): TriageIndicators {
  const rate = analyzeRate(events.map((e) => e.createdAt))
  const dup = nearDup(events.map((e) => ({ id: e.id, content: e.content })))
  const tags = analyzeTags(events.map((e) => ({ id: e.id, tags: e.tags })))
  return {
    eventCount: events.length,            // 0..perAuthor — neutral denominator, never "worse"
    burst: rate.burstDetected,
    nearDup: dup.duplicateCount > 0,
    tagFanOut: tags.massMention || tags.stuffing,
  }
}
```

### Pattern 6: `authors` enumeration loop with Stop + INVALID_CURSOR restart (NEW)
**What:** Page `authors(after, limit)` byte-ascending, accumulating distinct hexes until
`hasMore === false`. A Stop control aborts (keeps the partial snapshot). `INVALID_CURSOR` restarts
from the top (drop cursor, clear accumulated set, re-page) with a bounded retry budget — mirroring the
`useAuthorWindow` cursorRetry guard so a looping lens can't spin forever.
**When to use:** The "enumerate corpus" import source.
**Example:**
```typescript
// Source: contract §6.4 (opaque cursor verbatim, byte-ascending, terminates at keyspace end) +
// useAuthorWindow INVALID_CURSOR recovery (lines 160-181, VERIFIED). limit: 500 (the ceiling).
let after: string | null = null
const seen = new Set<string>()
do {
  if (stopRequested.current) break                          // Stop control
  const result = await client.query(AuthorsDocument, { after, limit: 500 }, { requestPolicy: 'network-only' })
    .toPromise().catch(() => 'THREW')
  const err = classify(result)                              // BEFORE data
  if (err?.kind === 'INVALID_CURSOR') { after = null; seen.clear(); /* bounded restart */ continue }
  if (err) { /* 503 backoff / NETWORK retry / INTERNAL hard-fail — keep partial */ break }
  const page = result.data!.authors
  for (const pk of page.authors) seen.add(pk)              // distinct already, Set is belt-and-suspenders
  setRunningCount(seen.size)                                // live snapshot count
  after = page.endCursor ?? null                            // opaque, verbatim
} while (after)
```

### Anti-Patterns to Avoid
- **Index-zipping the `latestPerAuthor` response against the input array.** The response OMITS
  zero-match authors (contract §5/§8), so `response[i]` does NOT correspond to `input[i]`. Always
  left-join by the `author` key. (This is the single most important correctness pin in the phase.)
- **Sizing chunks by the 256 KiB body limit as the primary axis.** At `perAuthor=5` the body limit
  never binds before the 1000-author cap (measured 66 KiB at 1000 authors). Treat 413 as a runtime
  safety net, not the static sizing driver.
- **Reading `result.data` before `classify()`.** Errors arrive on HTTP 200; reading data first leaks
  partial/garbage and skips the 413/503/INVALID branches. (FND-03, the whole point of `classify()`.)
- **Re-implementing nip19 in the batch tokenizer.** Route every token through `parseIdentifier` — it
  already rejects `note`/`nsec` and normalizes npub/nprofile/hex. A second call site is forbidden.
- **A single opaque "spam score" / a "clean" column.** Each signal is its own transparent column;
  absence of a tripped signal is never a positive verdict (asymmetry inherited from Phases 2–3).
- **Treating a quiet or "0 events" row as exoneration.** `perAuthor=5` is a first-pass screen; the
  persistent framing line + drill-in copy carry that. Never green, never "safe".
- **Aggressive/parallel-unbounded chunk firing.** The goal forbids overloading the backend — sequential
  or small-bounded concurrency with a progress indicator.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| bech32 ↔ hex + reject note/nsec | A second nip19 parser | `parseIdentifier` / `isHexPubkey` (`identifier/`) | Single sanctioned normalizer; already handles all shapes + rejections + ID-03. |
| HTTP-200-errors / 413 / 503 / INVALID_CURSOR / TOO_MANY_AUTHORS taxonomy | A new error mapper | `classify()` (`transport/errors.ts`) | All 7 kinds — including the exact ones batch chunking must respect — already modeled. |
| Burst / near-dup / tag fan-out detection | New per-author heuristics | `analyzeRate` / `nearDup` / `analyzeTags` | Pure, asymmetric, bounds-checked, tested; just adapt outputs to booleans. |
| Forged 64-bit `createdAt` bounds check | A new timestamp guard | `isSaneTs` (used inside `analyzeRate`/`analyzeKinds`) | Single source for timestamp bounds; analyzers already apply it internally. |
| Window-honesty denominator | A new "triaged N of M" widget from scratch | Reuse the `WindowIndicator` pattern / `formatInt` | Locale-grouped mono, amber-on-partial, non-removable — same contract at batch scale. |
| Cursor pagination discipline | A new cursor handler | The `useAuthorWindow` opaque-cursor + INVALID_CURSOR-restart pattern | Proven; opaque cursor passed verbatim, bounded retry guard already worked out. |
| Drill-down view | A new author view / route | Existing `#/a/<hex>` route + `AuthorDrillDown` | Row click sets `window.location.hash`; no new drill-down code. |
| Sortable table | A data-grid / table library | Hand-rolled `<table>` + local `.sort()` | ≤1000 rows; UI-SPEC forbids any table/grid lib (zero new deps). |
| CSV/file parse | papaparse / a CSV lib | `FileReader.readAsText` + split on `/[\s,]+/` then `parseIdentifier` each | Flat pubkey list — no quoted-field semantics; a parser is over-engineering. |

**Key insight:** This phase is ~90% composition of existing, tested parts. The new code is small and
pure (chunk math, left-join, enumeration loop, triage adapter) and should be unit-tested in the Node
vitest environment exactly like the Phase 2–3 analyzers.

## Common Pitfalls

### Pitfall 1: Index-zipping the grouped response
**What goes wrong:** Authors with zero matching events are dropped from `latestPerAuthor`, so
`result.data.latestPerAuthor[i].author !== inputAuthors[i]`. Index-zipping silently misattributes one
author's events to another and hides zero-match authors entirely.
**Why it happens:** The natural `inputs.map((a, i) => ({ a, events: response[i].events }))` looks
correct and passes when every author happens to match.
**How to avoid:** `mergeByAuthor` left-joins from the input set keyed strictly by `author` (Pattern 4).
Pin it with a comment AND a unit test where some input authors are absent from the response.
**Warning signs:** Row author hex doesn't match the events' `pubkey`; "0 events" rows never appear.

### Pitfall 2: Mis-sizing chunks against the wrong axis
**What goes wrong:** Sizing chunks to "fit 256 KiB" yields ~3900 authors/chunk, instantly tripping
`TOO_MANY_AUTHORS` (cap 1000). Conversely, assuming the body limit can never bind ignores that 413 is
on the *request* body and a future fatter selection could change it.
**Why it happens:** The two caps look co-equal in the requirement text; they aren't at `perAuthor=5`.
**How to avoid:** Static size = `min(500, 1000, byteBudget)` = 500. Keep the 413 halve-and-retry as a
runtime net (Pattern 3). Document the measured numbers (66 KiB @ 1000 authors).
**Warning signs:** `TOO_MANY_AUTHORS` errors on the first chunk; or chunks that grow when `perAuthor`
or the field selection changes.

### Pitfall 3: Reading data before classify on a chunk
**What goes wrong:** A 413/503/INVALID chunk has `result.data` null/partial; reading it first crashes
or renders garbage and skips the degrade/retry branches.
**Why it happens:** Forgetting that GraphQL errors come back on HTTP 200.
**How to avoid:** `classify(result)` before `result.data?.latestPerAuthor` on EVERY chunk and EVERY
enumeration page (Pattern 1). The throw-guard `.catch(() => 'THREW')` is mandatory.
**Warning signs:** A failed chunk kills the whole batch instead of retaining partial results.

### Pitfall 4: Unbounded INVALID_CURSOR restart on enumeration
**What goes wrong:** If `authors(after: null)` itself returns `INVALID_CURSOR` (a looping lens), the
naive "drop cursor and restart" recurses forever — a permanent spinner.
**Why it happens:** The recovery for an expired cursor is to restart page 1, but page 1 can also fail.
**How to avoid:** Mirror `useAuthorWindow`'s `cursorRetry` budget (one restart; a second consecutive
INVALID_CURSOR — or INVALID_CURSOR on a null cursor — surfaces the error). (VERIFIED: useAuthorWindow
lines 108-181.)
**Warning signs:** Enumeration running count stuck at 0 with no error shown.

### Pitfall 5: Stale chunk/page results overwriting a newer batch
**What goes wrong:** Starting a new triage (or stopping + restarting enumeration) while old fetches are
in flight; the late resolver writes stale rows.
**Why it happens:** Async fetches don't auto-cancel on re-run.
**How to avoid:** Reuse the `runId` ref + in-flight guard pattern from `useAuthorWindow` (lines 105,
143-146): a superseded run drops its result entirely.
**Warning signs:** Rows from a previous batch bleed into the current table.

### Pitfall 6: `tags` not selected in the new document
**What goes wrong:** The tag fan-out indicator silently reads empty `tags` and never trips.
**Why it happens:** Easy to copy the minimal selection and forget `tags` (and `content` for near-dup).
**How to avoid:** The `latestPerAuthor` document MUST select `id pubkey kind createdAt content tags`
(the same six `useAuthorWindow` selects — mirror `events.graphql.ts`). Exclude `raw` (large, unused).
**Warning signs:** Tag fan-out column never amber even on obviously mass-mentioning authors.

### Pitfall 7: Forgetting the BLOCKING codegen step
**What goes wrong:** The new `graphql()` documents have no generated types; `LatestPerAuthorDocument`
imports fail or are `any`.
**Why it happens:** Types come from `npm run codegen`, not at edit time.
**How to avoid:** Plan a task that runs `npm run codegen` AFTER adding both documents and BEFORE the
hooks that consume them. Codegen reads the checked-in `schema.graphql` (not live introspection — the
depth-12 limit rejects the introspection query). [VERIFIED: codegen.ts header]
**Warning signs:** `tsc -b` fails on missing exports from `src/gql`.

## Common Operations / Code Examples

### Verified chunk-sizing measurement
```text
// Source: empirical Buffer.byteLength over the actual query doc + 64-hex authors (VERIFIED this session)
fixed overhead (query + variables scaffold), 0 authors: 322 bytes
marginal bytes per author (64-hex + JSON array framing): 67.00 bytes
body at 1000 authors (the hard cap):                     65.7 KiB   ← well under 256 KiB
authors that would fit in 256 KiB (raw):                 ~3907
⇒ the ≤1000-author cap binds FIRST. Recommend static chunk = 500 (conservative), with a
   per-author safety constant of 80 bytes ⇒ byteBudget ≈ 3225 (never binds at perAuthor=5).
```

### New `graphql()` document (mirror `events.graphql.ts`)
```typescript
// Source: src/queries/events.graphql.ts pattern (VERIFIED) + contract §6.2
import { graphql } from '../gql'
export const LatestPerAuthorDocument = graphql(`
  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {
    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {
      author
      events { id pubkey kind createdAt content tags }   # tags + content REQUIRED for indicators; raw excluded
    }
  }
`)
```
```typescript
export const AuthorsDocument = graphql(`
  query Authors($after: String, $limit: Int) {
    authors(after: $after, limit: $limit) { authors endCursor hasMore }
  }
`)
```

### hashRouter `batch` route variant (EXTEND)
```typescript
// Source: src/router/hashRouter.ts (VERIFIED) — add a union variant + a matcher, leave author/home intact
export type Route =
  | { name: 'home' }
  | { name: 'batch' }                              // NEW
  | { name: 'author'; hex: string }
  | { name: 'notfound' }
export function parseHash(hash: string): Route {
  if (hash === '' || hash === '#' || hash === '#/') return { name: 'home' }
  if (hash === '#/batch') return { name: 'batch' } // NEW (exact match)
  const m = AUTHOR_HASH.exec(hash)
  if (m) return { name: 'author', hex: m[1] }
  return { name: 'notfound' }
}
// App.tsx: add `{route.name === 'batch' && <BatchImport />}` to the route outlet.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| (n/a — new surface) | Client-side chunked fan-out over a read-only GraphQL lens | this phase | No server-side batch analysis; all triage is client-pure (out of scope per CONTEXT). |

**Deprecated/outdated:** none — this phase only composes the current Phase 1–3 stack. No library
churn. `graphql` stays exact-pinned (do not bump).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Static chunk size **500** (vs 1000 cap) is the right conservative default | Std Stack / Pattern 2 | LOW — Claude's-discretion per CONTEXT; tunable in `thresholds.ts`. 500 ≈ 33 KiB body, generous pacing margin. Either value works. |
| A2 | `SAFE_BYTES_PER_AUTHOR = 80` is conservative enough | Pattern 2 | LOW — measured marginal is 67; 80 has ~19% margin and only affects the (non-binding) byte budget. |
| A3 | Small-bounded concurrency vs strictly sequential is fine for pacing | Pacing | LOW — CONTEXT marks concurrency Claude's discretion; sequential is the safe default. |
| A4 | `largeSetWarn = 1000` is a sensible non-blocking warning threshold | Std Stack | LOW — discretion per CONTEXT; warns, never blocks; tunable. |
| A5 | `enumeration limit = 500` (the ceiling) minimizes round-trips | Pattern 6 | LOW — contract §12 confirms 500 ceiling; fewer pages = fewer round-trips, same total work. |

**All A1–A5 fall under "Claude's Discretion" in CONTEXT** — they are tuning constants, not new
decisions, and every one lives in `thresholds.ts`. No locked decision is assumed.

## Open Questions (RESOLVED)

> Both resolved 2026-06-25 during plan/check; the plans adopt the recommendations.
> - **Q1 → RESOLVED:** incremental re-merge against the full input set after each chunk (04-01 Task 3).
> - **Q2 → RESOLVED:** omit not-yet-fetched rows; the batch `WindowIndicator` "triaged N of M" carries partial-ness with M = full input count.
> Naming note: earlier drafts referred to the batch view as `BatchTriage`; the plan/implementation uses **`BatchImport.tsx`** (with `TriageTable.tsx`).

1. **Incremental vs end-of-batch merge rendering.**
   - What we know: rows should stream in as chunks resolve (UI-SPEC "results stream into the table by
     author-key merge").
   - What's unclear: whether to re-run `mergeByAuthor` incrementally per chunk or accumulate groups and
     merge once at the end. Both are correct; incremental gives better perceived progress.
   - Recommendation: accumulate `AuthorGroup[]` and re-merge against the full input set after each chunk
     (cheap for ≤1000 rows) so the table fills progressively with "0 events" placeholders for not-yet-
     fetched authors replaced as chunks land. Plan can choose; verify "triaged N of M" reflects it.

2. **Should the table render all input authors immediately (as pending) or only fetched ones?**
   - What we know: zero-match authors must show as "0 events"; partial batch must read as partial.
   - What's unclear: distinguishing "not yet triaged" from "triaged, 0 events" mid-run.
   - Recommendation: the batch `WindowIndicator` "triaged N of M" carries the partial-ness; rows for
     not-yet-fetched authors can be omitted until their chunk lands (avoids a false "0 events"), with M
     = full input count. Confirm with the planner against the UI-SPEC state table.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Node + npm (codegen, vitest, vite) | codegen / build / test | ✓ | per project | — |
| Live lens at `VITE_GRAPHQL_URL` | enumeration + chunk fetches (runtime only) | runtime | contract v1.2 | Codegen reads checked-in `schema.graphql`, NOT live introspection — build/test never need the lens. [VERIFIED: codegen.ts] |
| Browser `FileReader` / `File` | file-import source | ✓ (platform) | — | Paste textarea is the always-available alternative source. |

**Missing dependencies with no fallback:** none — codegen and tests run fully offline against the SDL.
**Missing dependencies with fallback:** the live lens is only needed at runtime; absent it, the app
shows the connecting/error states already built (Phase 1 readiness gate).

## Validation Architecture

> `workflow.nyquist_validation` is `false` in `.planning/config.json` — this section is INFORMATIONAL
> (the formal Nyquist map is skipped), but the existing vitest infra should still cover the new pure logic.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest 3.2.6 (Node env) [VERIFIED: package.json] |
| Config file | none separate — `vitest run` via `npm test` (analyzers are pure, Node-runnable) |
| Quick run command | `npm test` |
| Full suite command | `npm test` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BATCH-01 | tokenize/normalize/dedupe + valid/dup/unparseable counts via parseIdentifier | unit | `npm test` | ❌ new (`src/analysis/import.test.ts` or inline) |
| BATCH-02 | chunk size = min(500,1000,byteBudget); 413 halve-and-retry bottoms at 1 | unit | `npm test` | ❌ new (`src/analysis/chunk.test.ts`) |
| BATCH-03 | mergeByAuthor LEFT-joins — zero-match author ⇒ "0 events", NEVER index-zipped | unit | `npm test` | ❌ new (`src/analysis/mergeByAuthor.test.ts`) — load-bearing pin |
| BATCH-03 | triageAuthor maps the 4 analyzers to indicators (incl. tagFanOut from tags) | unit | `npm test` | ❌ new (`src/analysis/triage.test.ts`) |
| BATCH-04 | enumeration loop terminates on !hasMore; INVALID_CURSOR bounded restart; Stop | unit | `npm test` | ❌ new (hook logic — extract pure helpers where possible) |

### Wave 0 Gaps
- [ ] `src/analysis/mergeByAuthor.test.ts` — the "never index-zipped" + "0 events left-join" pin (CONTEXT calls this out explicitly).
- [ ] `src/analysis/chunk.test.ts` — chunk-size math + 413 halve recursion bottom.
- [ ] `src/analysis/triage.test.ts` — indicator mapping (esp. tagFanOut = massMention||stuffing).
- [ ] `hashRouter` test extension — `#/batch` → `{ name: 'batch' }`, unknown still `notfound`.
- [ ] Framework install: none — vitest already present.

## Security Domain

> `security_enforcement: true`, `security_asvs_level: 1` in config — this section is REQUIRED.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | API is unauthenticated, single local analyst (out of scope). |
| V3 Session Management | no | No sessions/cookies; wildcard CORS, no credentials. |
| V4 Access Control | no | Read-only lens; network-placement is the only control. |
| V5 Input Validation | **yes** | All pasted/uploaded/enumerated tokens normalized via `parseIdentifier`; only lowercase-64-hex reaches the API; note/nsec rejected. Counts/limits clamped. |
| V6 Cryptography | no | No client crypto beyond nip19 decode in the single identifier module; sigs pre-verified by strfry. |
| V7 Error Handling | **yes** | `classify()` maps INTERNAL to a generic kind carrying NO server message (T-01-04); never leak internals on a chunk error. |
| V12 Files/Resources | **yes** | Uploaded files read in-browser only (never sent anywhere); enforce a max-file-size constant; treat file contents as untrusted text. |
| V14 Config | partial | Base URL from `VITE_GRAPHQL_URL` only; no inline literal. |

### Known Threat Patterns for this stack (React + urql, read-only)

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| XSS via pasted/uploaded/enumerated tokens, content, tags, author hex | Tampering / Info-disclosure | Render all author-supplied strings as escaped plaintext (React default); never `dangerouslySetInnerHTML` (UI-SPEC Security row). Listed unparseable tokens are escaped mono. |
| Server-internal leak on a chunk/enumeration error | Info-disclosure | `classify()` INTERNAL kind carries no message; show generic copy (T-01-04). |
| Self-inflicted DoS — unbounded enumeration / huge file / chunk storm | DoS (self) | Stop control + large-set non-blocking warning; bounded chunk size + pacing (sequential/small concurrency); max-file-size constant; bounded INVALID_CURSOR retry. |
| Cross-use / hand-built cursors | Tampering | Treat both `events` and `authors` cursors as opaque, passed verbatim; never construct or cross-use (contract §6.4). |
| Misattribution via index-zip | Tampering (data integrity) | Strict merge-by-`author` left-join (BATCH-03) — a correctness AND integrity control. |

## Project Constraints (from CLAUDE.md)

- **Monorepo boundary:** scope ALL work to `spamhunter/GraphQLExplorer`; do not touch sibling projects. [CITED: /Users/g/git/deepfry/CLAUDE.md]
- **Commit to main, no feature branches** (config `branching_strategy: none`; memory note). [CITED: config.json + MEMORY.md]
- **Read-only API:** no mutation/subscription; no persistence — saved lists / export are out of scope (deferred). [CITED: CONTEXT deferred + REQUIREMENTS Out of Scope]
- **GSD subagents anchor to git root:** pass absolute paths; the project `.planning/` is the subdir one, not the git-root one. [CITED: MEMORY.md]
- **`graphql` exact-pinned** (postinstall guard) — never bump. [VERIFIED: package.json]

## Sources

### Primary (HIGH confidence)
- `contract.md` (LMDB2GraphQL v1.2, code-verified) — §5 (zero-match omitted, match by author), §6.2
  (latestPerAuthor caps), §6.4 (authors enumeration, opaque cursor), §7 (error table), §12 (limits cheat sheet).
- `src/hooks/useAuthorWindow.ts` — classify-before-data, throw-guard, runId, INVALID_CURSOR bounded restart (reuse pattern).
- `src/transport/errors.ts` + `classify()` — 7-kind union incl. TOO_MANY_AUTHORS / PAYLOAD_TOO_LARGE / INVALID_CURSOR.
- `src/analysis/{rate,nearDup,tags,kinds}.ts` + `thresholds.ts` — analyzer signatures + isSaneTs + single-tunable-home convention.
- `src/identifier/identifier.ts` — parseIdentifier / isHexPubkey (single nip19 site).
- `src/router/hashRouter.ts`, `src/views/WindowIndicator.tsx`, `src/queries/events.graphql.ts`, `codegen.ts`, `package.json` — reuse + wiring.
- Empirical `Buffer.byteLength` measurement (this session) — chunk-sizing math (66 KiB @ 1000 authors).
- `04-CONTEXT.md` (16 locked decisions) + `04-UI-SPEC.md` (approved 6/6).

### Secondary (MEDIUM confidence)
- none required — every claim grounded in checked-in source or the code-verified contract.

### Tertiary (LOW confidence)
- none.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — zero new deps; all reused packages installed/pinned and read this session.
- Architecture: HIGH — composition of patterns verified in the actual Phase 1–3 source.
- Chunk-sizing math: HIGH — measured empirically against the real query doc; cap-binds-first conclusion is unambiguous.
- Pitfalls: HIGH — derived from the contract's explicit warnings (§5/§8 omit + don't-zip; §7 errors) and existing code.

**Research date:** 2026-06-25
**Valid until:** 2026-07-25 (stable — no fast-moving deps; re-verify only if the lens contract version changes from v1.2).

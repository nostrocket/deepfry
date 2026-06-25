# Phase 3: Remaining Spam Signals - Research

**Researched:** 2026-06-25
**Domain:** Client-side spam-signal analysis (near-duplicate detection, tag aggregation, kind histograms) + lazy GraphQL single-event fetch, in React 19 + TS + urql
**Confidence:** HIGH (decisions are locked in CONTEXT; every reuse target was read in-session; algorithm choices are standard textbook techniques)

## Summary

Phase 3 adds three **pure, zero-dependency TypeScript analyzers** (`nearDup`, `tags`, `kinds`) and three **stacked signal panels** on the existing `AuthorDrillDown` view, plus a **lazy raw-JSON inspector** for a single event. Almost all decisions are already locked in `03-CONTEXT.md` and `03-UI-SPEC.md`; this research is about HOW to implement well by **mirroring Phase 2's exact conventions**, not re-deciding. The Phase 2 codebase gives a complete blueprint: `src/analysis/rate.ts` (pure analyzer shape + `isSaneTs` bounds-check + no-clean-field rule), `src/analysis/thresholds.ts` (single tunables home), `src/views/RatePanel.tsx` (hand-rolled-bars + co-located `WindowIndicator` + persistent caveat panel), and `src/hooks/useAuthorWindow.ts` (the imperative `client.query(...).toPromise().catch()` transport pattern that the lazy raw fetch must copy verbatim).

The two non-trivial technical questions both have clean, dependency-free answers. **Near-duplicate clustering** over a ≤500-event window is best done as a two-stage pipeline: (1) exact bucketing by a normalized-content key (NFC + lowercase + whitespace-collapse + trim) using a `Map`, then (2) word-shingle (k=3) Jaccard with **union-find (disjoint-set)** to form transitive near-duplicate clusters. The O(n²) pair comparison is the only quadratic step; at n≤500 that is ≤125k Jaccard ops, each cheap over `Set`s — acceptable, but bound it explicitly (cap the comparison set / short-circuit on size disparity) so a future larger window can't pathologically stall the UI thread. **The lazy raw fetch** reuses the established imperative `client.query(EventsDocument-analog, { filter: { ids: [id] }, limit: 1 }).toPromise().catch(() => 'THREW')` → `classify()` pattern from `useAuthorWindow` — there is no `event(id)` query; `ids` filter is the only single-event path, and `raw` is selected ONLY in this new document.

**Primary recommendation:** Build slice 03-01 (three pure analyzers + thresholds additions, TDD RED→GREEN against fixtures, zero network) exactly mirroring `rate.ts`/`rate.test.ts`; build slice 03-02 (three panels + lazy inspector) mirroring `RatePanel.tsx` + `useAuthorWindow.ts`. Add `tags` to `EventsDocument`, run `npm run codegen`, keep `raw` out of the window query. Introduce zero new runtime dependencies.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Near-Duplicate Content Detection (DRILL-02)**
- Two-stage algorithm: exact normalized-hash bucketing FIRST, then shingle/Jaccard ≈0.8 for near-duplicates. Pure, unit-tested, zero transport coupling.
- Normalization for the exact-hash stage: Unicode NFC + lowercase + collapse internal whitespace + trim. Do NOT strip URLs / mentions / punctuation (spam frequently varies only in those; stripping would over-merge distinct posts).
- Shingle size k=3 word shingles (default), housed in `analysis/thresholds.ts` alongside the BURST constants; flagged for corpus validation.
- Presentation: group near-duplicates into clusters with a count, ALWAYS framed against the window denominator ("3 of 50 fetched are near-duplicates") — never a bare "0 duplicates". A cluster indicator on relevant timeline rows + a summary line.

**Tag/Mention Aggregation (DRILL-03)**
- Tags covered: `p` (mentions / fan-out), `e` (event references), `t` (hashtags / stuffing).
- Data source: add `tags` to the existing `EventsDocument` window query so aggregation re-derives over the SAME accumulated window. `raw` stays excluded from the list query (lazy only). Accept the small per-page payload increase from `tags`.
- Surface: top-N most-mentioned pubkeys (fan-out) and top-N hashtags (stuffing) with counts over the window, plus a per-event outlier flag (single event carrying an unusually high tag count). All framed against the window denominator.
- "mass-mention" / "stuffing" thresholds: sane defaults in `analysis/thresholds.ts`, corpus-validated — not locked in discuss.

**Kind Distribution + Raw Inspector (DRILL-04)**
- Kind histogram: hand-rolled CSS/SVG bars (no chart library). Label each bar with the NIP/kind name where known plus the raw kind number.
- Out-of-safe-range `kind` / `createdAt`: flag a count ("N events with out-of-range kind/timestamp") reusing the Phase 2 `isSaneTs` / bounds-check discipline — never silently mis-compute or drop.
- Raw-JSON inspector fetch: lazy and on-demand per event via `events(filter: { ids: [selectedId] }, limit: 1) { events { raw } }` — `raw` is NEVER selected in the list/window query. There is no `event(id)` query; the `ids` filter is the single-event path.
- Raw rendering: escaped plaintext in a `<pre>` (React default escaping), pretty-printed if the bytes parse as JSON but shown verbatim otherwise; NEVER executed as HTML/markdown (no `dangerouslySetInnerHTML`). XSS-safe.

**Panel Layout & Window-Honesty Integration**
- Layout: stacked panels on the existing `AuthorDrillDown` view (one scrollable forensic picture), not tabs.
- Per-panel honesty: every new signal panel (dup, tags, kinds) carries the non-removable `WindowIndicator` (DRILL-05 carried forward).
- Live re-derivation: all panels re-derive over the accumulated window on "Load more" (pure analyzers consuming the Phase 2 `useAuthorWindow` window).
- Accent discipline: teal `--accent` stays reserved for the "Inspect author" submit ONLY; signal panels use neutral/amber, never green/teal/"clean"/"safe".

### Claude's Discretion
- Exact component decomposition of the three panels and the raw-inspector trigger UX (row expand vs modal vs detail drawer), within the escaped-plaintext + lazy-fetch constraints.
- Internal shingle/hash implementation details and the precise default threshold numbers (pending corpus-validation research).
- Exact NIP-name lookup table for kind labels (known kinds labeled; unknown shown as the number).

### Deferred Ideas (OUT OF SCOPE)
- Batch / multi-author triage and list import — Phase 4 (BATCH-01..04).
- Server-side or persisted analysis — out of scope (read-only client; pure analyzers only).
- Cross-author duplicate detection (same content across different authors) — NOT in DRILL-02 scope (single-author this phase); note as a possible future capability.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DRILL-02 | Content view highlights near-duplicate / repeated text via client-side detection (exact-hash then shingle/Jaccard ≈0.8) | `## Architecture Patterns` Pattern 1 (two-stage near-dup + union-find clustering); `## Don't Hand-Roll`; `## Common Pitfalls` 1 & 2; thresholds additions |
| DRILL-03 | Tag/mention aggregation (p/e/t) surfaces mass-mention and hashtag-stuffing patterns | Pattern 2 (defensive tag parsing + top-N counting + per-event outlier); `## Code Examples` tag-shape; `EventsDocument` `tags` addition + codegen step |
| DRILL-04 | Kind-distribution breakdown plus a raw-JSON inspector (`raw` fetched lazily) | Pattern 3 (kind histogram + `isSaneTs` reuse + NIP lookup); Pattern 4 (lazy single-event `ids` fetch via imperative `client.query`); escaped `<pre>` rendering |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Near-duplicate detection | Client (pure `src/analysis/nearDup.ts`) | — | Read-only API has no analysis query; CONTEXT mandates client-side, pure, zero-transport (mirrors `rate.ts`) |
| Tag/mention aggregation | Client (pure `src/analysis/tags.ts`) | — | Aggregation re-derives over the in-memory window; pure function of `WindowEvent[]` |
| Kind histogram + bounds flag | Client (pure `src/analysis/kinds.ts`) | — | Histogram + `isSaneTs` reuse; pure function of `WindowEvent[]` |
| Window data supply (`tags` added) | API / Backend (lens `events` query) | Client hook (`useAuthorWindow`) | `tags` is a field on the existing window query; the hook accumulates pages, analyzers consume read-only |
| Lazy raw bytes for one event | API / Backend (lens `events(filter:{ids})`) | Client (new lazy hook/handler) | Canonical bytes live server-side; fetched on demand via the `ids` single-event path |
| Escaped `<pre>` rendering | Browser / Client (React text nodes) | — | XSS-safe rendering is a pure client concern (React default escaping) |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| (none — all pure TS) | n/a | near-dup hashing/shingling, tag aggregation, kind histogram | `[VERIFIED: 03-UI-SPEC "New dependency: none"]` CONTEXT + UI-SPEC both mandate ZERO new runtime dependencies; analyzers are pure `src/analysis/` modules |
| react / react-dom | ^19 (installed) | panels + inspector UI | `[VERIFIED: package.json]` already the app stack |
| @urql/core | ^6 (6.0.3 installed) | lazy raw-by-id `client.query` | `[VERIFIED: src/transport/client.ts + errors.ts]` reuse the existing `client` verbatim |
| graphql | 16.14.2 (pinned exact) | document parsing | `[VERIFIED: package.json]` pinned by FND-01 |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| @graphql-codegen/cli | ^7 (dev) | regen typed `EventsDocument` + new raw-by-id doc after adding `tags`/`raw` | `[VERIFIED: package.json + codegen.ts]` run `npm run codegen` once the document strings change |
| vitest | ^3.2.6 (dev) | TDD RED→GREEN for the three pure analyzers | `[VERIFIED: package.json scripts.test = "vitest run"]` Node env, no DOM/network — matches `rate.test.ts` |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hand-rolled union-find | A library (e.g. a disjoint-set npm pkg) | `[VERIFIED: CONTEXT "No external library"]` FORBIDDEN — union-find is ~20 lines; a dependency is pure liability here |
| Hand-rolled shingle/Jaccard | `string-similarity` / MinHash libs | `[VERIFIED: CONTEXT]` FORBIDDEN — Jaccard over `Set`s is trivial at n≤500; MinHash/LSH is premature at this scale |
| `useQuery` (urql React hook) for lazy raw | imperative `client.query().toPromise()` | `[VERIFIED: src/hooks/useAuthorWindow.ts]` the codebase already uses the IMPERATIVE pattern (one-shot, on-demand, with the mandatory `.catch(() => 'THREW')` guard); a declarative `useQuery` would fetch on mount, defeating "lazy". USE the imperative pattern. |

**Installation:**
```bash
# No installs. Phase 3 adds zero runtime dependencies (CONTEXT + UI-SPEC).
# After editing the GraphQL document strings, regenerate types:
npm run codegen
```

**Version verification:** No new packages to verify — every dependency is already in `package.json` and was read in-session.

## Package Legitimacy Audit

> Not applicable. Phase 3 installs **zero** external packages (CONTEXT + UI-SPEC both mandate no new runtime dependency; analyzers are pure TypeScript). No registry lookups, no slopsquat surface.

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| (none) | — | — | — | — | — | No packages installed this phase |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram

```
                       useAuthorWindow (Phase 2, REUSED unchanged-ish)
                       ┌─────────────────────────────────────────────┐
 lens events(authors)  │ accumulates pages → WindowEvent[] + meta     │
 ───────────────────►  │ (+ NEW: tags[] now on each WindowEvent)      │
                       └───────────────┬─────────────────────────────┘
                                       │ read-only window (re-derives on Load more)
                 ┌─────────────────────┼──────────────────────────┬───────────────┐
                 ▼                     ▼                          ▼               ▼
        nearDup(events)         analyzeTags(events)        analyzeKinds(events)   (timeline rows)
        ┌──────────────┐        ┌────────────────┐         ┌──────────────────┐  each row gets a
        │ stage1: exact│        │ parse p/e/t    │         │ histogram by kind│  "View raw" trigger
        │   norm key   │        │ (defensive)    │         │ + isSaneTs flag  │        │
        │ Map bucket   │        │ top-N counts   │         │ out-of-range cnt │        │
        │ stage2:      │        │ per-event      │         └────────┬─────────┘        │
        │ shingle k=3  │        │ outlier flag   │                  │                  │
        │ Jaccard≥0.8  │        └───────┬────────┘                  │                  │
        │ union-find   │                │                           │                  │
        └──────┬───────┘                │                           │                  │
               ▼                        ▼                           ▼                  ▼
        DuplicatePanel           TagsPanel                   KindsPanel          RawInspector
        (+WindowIndicator)       (+WindowIndicator)          (+WindowIndicator)  (lazy, on click):
               └────────────────────────┴───────────────────────────┘            client.query(
                        stacked on AuthorDrillDown loaded branch                    {filter:{ids:[id]},
                        (neutral default; amber on detected signal)                  limit:1}{events{raw}})
                                                                                     │ .toPromise().catch
                                                                                     ▼ classify() → <pre>
                                                                                       escaped, pretty if JSON
```

Data flow for the primary use case ("analyst inspects an author"): the Phase-2 `useAuthorWindow` window feeds three pure analyzers (re-run every render, so Load more widens the analysis live); each analyzer's result renders into a stacked panel carrying its own `WindowIndicator`; clicking "View raw" on any timeline row fires a SEPARATE one-shot lazy query for that event's `raw` bytes, classified through the existing `classify()` boundary and rendered as escaped `<pre>`.

### Recommended Project Structure
```
src/
├── analysis/
│   ├── thresholds.ts          # EXTEND: add NEAR_DUP {k, jaccard} + TAGS {massMention, stuffing, highTagCount}
│   ├── nearDup.ts             # NEW: pure two-stage detector → NearDupResult
│   ├── nearDup.test.ts        # NEW: TDD fixtures (mirror rate.test.ts)
│   ├── tags.ts                # NEW: pure p/e/t aggregator → TagsResult
│   ├── tags.test.ts           # NEW
│   ├── kinds.ts               # NEW: pure histogram + bounds flag → KindsResult
│   ├── kinds.test.ts          # NEW
│   └── kindNames.ts           # NEW (or const in kinds.ts): NIP kind-number → name lookup
├── hooks/
│   └── useAuthorWindow.ts     # EDIT: add `tags` to WindowEvent + (panels consume the window)
│   └── useRawEvent.ts         # NEW (Claude's discretion): lazy raw-by-id fetch hook OR inline handler
├── queries/
│   ├── events.graphql.ts      # EDIT: add `tags` to selection (NOT raw)
│   └── rawEvent.graphql.ts    # NEW: events(filter:{ids},limit:1){events{raw}} document
├── views/
│   ├── AuthorDrillDown.tsx    # EDIT: mount the 3 panels (stacked) in the loaded branch; row "View raw"
│   ├── DuplicatePanel.tsx + .module.css   # NEW (mirror RatePanel)
│   ├── TagsPanel.tsx + .module.css        # NEW
│   ├── KindsPanel.tsx + .module.css       # NEW
│   └── RawInspector.tsx + .module.css     # NEW (escaped <pre>)
```

### Pattern 1: Two-stage near-duplicate detection (DRILL-02)
**What:** Stage 1 buckets events by an exact normalized-content key (`O(n)` via `Map`). Stage 2 finds near-dupes among remaining content using word-shingle Jaccard and groups them transitively with union-find.
**When to use:** The `nearDup(events)` analyzer over the accumulated window.
**Why union-find (not greedy):** Near-duplicate similarity is NOT transitive in general (A≈B, B≈C, A≉C). Greedy "assign to first matching cluster" produces order-dependent, inconsistent clusters. Union-find gives a deterministic, order-independent transitive-closure clustering (A and C land in one cluster iff a similarity chain connects them) — the honest, reproducible choice. `[ASSUMED]` (standard textbook clustering; corpus-validate the visual grouping)

```typescript
// Source: standard normalize → shingle → Jaccard → union-find pipeline (no library).
// thresholds.ts: NEAR_DUP = { k: 3, jaccard: 0.8 } (defaults; corpus-validatable)

// --- normalization (exact-hash stage key) ---
// CONTEXT: NFC + lowercase + collapse internal whitespace + trim.
// Do NOT strip URLs / mentions / punctuation (spam varies only in those).
export function normalizeContent(s: string): string {
  return s.normalize('NFC').toLowerCase().replace(/\s+/g, ' ').trim()
}

// --- word shingles (k=3) ---
export function shingles(normalized: string, k: number): Set<string> {
  const words = normalized.split(' ').filter(Boolean)
  const out = new Set<string>()
  if (words.length < k) {
    if (words.length > 0) out.add(words.join(' ')) // short posts: whole-text shingle
    return out
  }
  for (let i = 0; i + k <= words.length; i++) out.add(words.slice(i, i + k).join(' '))
  return out
}

// --- Jaccard over two shingle sets ---
export function jaccard(a: Set<string>, b: Set<string>): number {
  if (a.size === 0 && b.size === 0) return 1
  let inter = 0
  for (const x of a) if (b.has(x)) inter++
  const union = a.size + b.size - inter
  return union === 0 ? 0 : inter / union
}

// --- union-find (disjoint set) ---
class DSU {
  private parent: number[]
  constructor(n: number) { this.parent = Array.from({ length: n }, (_, i) => i) }
  find(x: number): number { while (this.parent[x] !== x) { this.parent[x] = this.parent[this.parent[x]]; x = this.parent[x] } return x }
  union(a: number, b: number): void { const ra = this.find(a), rb = this.find(b); if (ra !== rb) this.parent[ra] = rb }
}
// Stage-2 loop: precompute each event's shingle set ONCE, then compare pairs i<j,
// union when jaccard >= NEAR_DUP.jaccard. Bound the O(n²): skip a pair early when
// |a.size - b.size| / max(size) > (1 - jaccard) (size disparity makes ≥0.8 impossible).
```

### Pattern 2: Defensive Nostr tag parsing + top-N + per-event outlier (DRILL-03)
**What:** Iterate every event's `tags` (an array of string arrays), defensively read `tag[0]` (name) and `tag[1]` (value), count by name into `Map`s, sort descending, slice top-N. Track per-event total tag count for the outlier flag.
**When to use:** The `analyzeTags(events)` analyzer.
**Critical robustness:** `tags` is attacker-controlled. A tag row may be empty, have a non-string-ish shape, or omit `tag[1]`. Guard every access (`Array.isArray(tag) && typeof tag[0] === 'string'`); skip malformed rows but COUNT them (parity with `rejectedCount` discipline) rather than throwing.

```typescript
// Source: NIP-01 tag shape [CITED: github.com/nostr-protocol/nips/blob/master/01.md]
// tags: [["p", <pubkey>, <relay?>], ["e", <id>, <relay?>, <author?>], ["t", <hashtag>], ...]
// NB: p and e are NIP-01; the `t` (hashtag) convention is NIP-12/widely-deployed, not in 01.md.
export interface TagsResult {
  analyzedCount: number               // events whose tags were read
  malformedTagRows: number            // skipped, counted — never silently dropped
  topMentions: { value: string; count: number }[]   // p
  topHashtags: { value: string; count: number }[]    // t
  eventRefCount: number               // e references (summarized as a count per UI-SPEC)
  outlierEvents: { id: string; tagCount: number }[]  // events over TAGS.highTagCount
  // NO clean/ok/safe field (asymmetry rule, mirrors RateResult)
}
```

### Pattern 3: Kind histogram with bounds-check reuse + NIP lookup (DRILL-04)
**What:** Count events by `kind` into a `Map<number, count>`, sort, render hand-rolled bars (mirror `RatePanel` bar JSX). Look up a human name per kind from a small static table; unknown kinds show the number + "(unknown kind)". Reuse `isSaneTs` for `createdAt` AND apply an analogous integer bounds-check for `kind` (also a forgeable 64-bit value).
**When to use:** The `analyzeKinds(events)` analyzer.
**Bounds discipline:** `kind` is `Int!` in the schema but the underlying value is author-claimed/64-bit (contract §8 same as `createdAt`). Reuse `isSaneTs` for `createdAt`; for `kind`, accept `Number.isSafeInteger(kind) && kind >= 0` (kinds are non-negative) and flag the rest into an out-of-range count — never bucket a forged kind into the histogram.

```typescript
// Source: kind numbers [CITED: github.com/nostr-protocol/nips README "Event Kinds"]
// Known kinds get a label; unknown render the number alone (UI-SPEC).
export const KIND_NAMES: Record<number, string> = {
  0: 'Metadata', 1: 'Short Text Note', 3: 'Follows', 4: 'Encrypted DM',
  5: 'Deletion Request', 6: 'Repost', 7: 'Reaction',
  1984: 'Reporting', 9735: 'Zap', 10002: 'Relay List', 30023: 'Long-form',
}
// import { isSaneTs } from './rate'  // REUSE — do not re-implement the createdAt bounds
```

### Pattern 4: Lazy single-event raw fetch (DRILL-04) — IMPERATIVE, mirrors useAuthorWindow
**What:** On "View raw" click, fire ONE `client.query` for `events(filter:{ids:[id]}, limit:1){events{raw}}`, awaited via `.toPromise().catch(() => 'THREW')`, classified through the existing `classify()` before reading data. `raw` is selected ONLY in this document.
**When to use:** The raw inspector trigger (per timeline row).
**Why imperative not `useQuery`:** `useQuery` fetches on mount → not lazy. The codebase's `useAuthorWindow` already establishes the imperative one-shot pattern with the mandatory throw-guard and pre-data `classify()`; copy it.

```typescript
// Source: src/hooks/useAuthorWindow.ts (imperative fetch + throw-guard + classify-before-data)
// + src/transport/errors.ts classify(). New document selects `raw` (ids path; no event(id) query).
const result = await client
  .query(RawEventDocument, { filter: { ids: [id] }, limit: 1 }, { requestPolicy: 'network-only' })
  .toPromise()
  .catch(() => 'THREW' as const)
if (result === 'THREW') { /* NETWORK */ return }
const apiError = classify(result)              // BEFORE reading data
if (apiError) { /* map to retryable/hard per UI-SPEC state table */ return }
const raw = result.data?.events?.events?.[0]?.raw   // undefined => zero-match note
// render: pretty = (try JSON.parse(raw) -> JSON.stringify(parsed, null, 2)) else raw verbatim
// into <pre> via JSX text node (escaped). NEVER dangerouslySetInnerHTML.
```

### Anti-Patterns to Avoid
- **A "clean"/"0 duplicates"/"no issues" verdict field or copy.** Forbidden by the asymmetry rule (mirrors `RateResult` having no clean/ok/safe key). Always frame "X of N fetched"; absence is a neutral fact, never green/teal.
- **Greedy first-match clustering for near-dupes.** Order-dependent and inconsistent — use union-find.
- **Selecting `raw` in `EventsDocument`.** Inflates every page; `raw` is lazy-only.
- **`dangerouslySetInnerHTML` for raw bytes / hashtags / content.** XSS sink on attacker-controlled data — render as escaped JSX text nodes only.
- **Re-implementing `isSaneTs`.** Import it from `rate.ts`.
- **Stripping URLs/mentions/punctuation before the exact-hash key.** CONTEXT forbids it — over-merges distinct spam variants.
- **Throwing on a malformed tag row / unparseable raw JSON.** Count/flag and continue (parity with `rejectedCount`); render verbatim on JSON-parse failure.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Error classification on the lazy raw fetch | A new try/catch + status sniffing | `classify()` from `src/transport/errors.ts` | The 7-kind taxonomy + HTTP-status extraction is already solved and tested; re-deriving it splits the failure semantics (Pitfall the Phase-2 errors.ts explicitly warns about) |
| `createdAt` bounds-check | A second sane-timestamp helper | `isSaneTs` exported from `src/analysis/rate.ts` | Single source for the forgeable-64-bit discipline |
| Window denominator UI | A per-panel denominator widget | `WindowIndicator` from `src/views/WindowIndicator.tsx` | Non-removable, verbatim Phase-2 component; co-locate one per panel |
| Locale number formatting | Ad-hoc `toLocaleString` calls | `new Intl.NumberFormat()` (as in `RatePanel`/`WindowIndicator`) | Consistency with Phases 1–2 |
| Hand-rolled bars | Importing a chart library | The `RatePanel` bar JSX pattern (CSS height %, neutral fill) | UI-SPEC: no chart library, inline only |

**Key insight:** Phase 3 is overwhelmingly a *reuse* phase. The pure-analyzer shape, the panel shape, the transport boundary, the denominator, and the bounds-check all already exist and are tested. The ONLY genuinely new logic is the near-dup pipeline (~60 lines) and the tag aggregation (~40 lines) — everything else is composition of Phase-2 parts.

## Common Pitfalls

### Pitfall 1: O(n²) near-dup comparison stalls the UI thread on a wide window
**What goes wrong:** Stage-2 pairwise Jaccard is O(n²) in window size; at large windows (or if Load more is used many times) the synchronous analyzer blocks the main thread and the panel jank-freezes.
**Why it happens:** The analyzer re-runs on EVERY render of `AuthorDrillDown` (it's a plain function call, like `analyzeRate`).
**How to avoid:** (1) Precompute each event's shingle `Set` once per analyzer run; (2) short-circuit pairs whose size disparity makes ≥0.8 impossible; (3) cap the stage-2 candidate set if the window grows beyond a guard (e.g. only compare within the post-stage-1 survivors); (4) consider `useMemo` keyed on `events.length`/identity in the panel so it doesn't recompute on unrelated re-renders. At n≤500 the raw cost is fine, but bound it so a future larger window can't pathologically stall.
**Warning signs:** Input lag after several Load more clicks; long task warnings in devtools.

### Pitfall 2: Unicode/empty-content edge cases in normalization
**What goes wrong:** Posts that are only whitespace/emoji, very short posts (< k words), or different Unicode normal forms of the same glyphs either crash shingle generation or wrongly fail to bucket.
**Why it happens:** k=3 shingling assumes ≥3 words; NFC matters for visually identical strings.
**How to avoid:** Apply `.normalize('NFC')` (CONTEXT-mandated); for `< k` words fall back to a whole-text shingle (see Pattern 1); treat empty-normalized content as its own exact bucket (empty string is a valid key, but don't cluster empties as "near-duplicates" of substantive posts).
**Warning signs:** A cluster that lumps every short/empty post together.

### Pitfall 3: Malformed / hostile tag rows
**What goes wrong:** `tags` is `[[String!]!]!` per schema, but values are author-controlled — a row could be `[]`, or `["p"]` with no value. Naive `tag[1]` reads `undefined` and pollutes counts; `tag[0]` of an empty row throws nothing but mis-buckets.
**Why it happens:** Trusting attacker-supplied structure.
**How to avoid:** Guard `Array.isArray(tag) && typeof tag[0] === 'string'` and require `typeof tag[1] === 'string'` before counting a value; count skipped rows into `malformedTagRows` (mirrors `rejectedCount`).
**Warning signs:** An `undefined` entry in the top-N list; NaN counts.

### Pitfall 4: `raw` JSON.parse on hostile / non-JSON bytes
**What goes wrong:** The inspector pretty-prints when `raw` parses as JSON, but `raw` is canonical bytes that may not be JSON (or may be huge / malformed). `JSON.parse` throws.
**Why it happens:** Assuming `raw` is always valid JSON.
**How to avoid:** `try { pretty = JSON.stringify(JSON.parse(raw), null, 2) } catch { pretty = raw /* verbatim */ }`; render the result as an escaped JSX text node in `<pre>` with the matching UI-SPEC caption ("Canonical bytes — escaped, not executed." vs "Raw bytes shown verbatim (not valid JSON)."). NEVER `dangerouslySetInnerHTML`.
**Warning signs:** A white-screen crash when opening raw on a non-JSON event.

### Pitfall 5: Codegen drift after editing the document strings
**What goes wrong:** Adding `tags` to `EventsDocument` (or creating the raw-by-id document) without running `npm run codegen` leaves `src/gql/graphql.ts` + `gql.ts` stale; `result.data.events.events[].tags` is untyped/`unknown` and the build (`tsc -b`) fails or types are wrong.
**Why it happens:** The `client` preset bakes the exact query string into a `Documents` map keyed by the verbatim string; a changed string must be regenerated.
**How to avoid:** After any document edit, run `npm run codegen`, then `tsc -b` (via `npm run build`) / `npm test`. Confirm `tags` is purely additive — Phase-2 reads of `id/pubkey/kind/createdAt/content` are unaffected. Keep `raw` OUT of `EventsDocument` (lazy-only).
**Warning signs:** TS error that `EventsDocument`'s string isn't in the overload set; `tags` typed `any`.

### Pitfall 6: Adding `tags` to `WindowEvent` type without updating the interface
**What goes wrong:** `useAuthorWindow.ts` defines `WindowEvent` with exactly 5 fields and casts `page.events as WindowEvent[]`. Adding `tags` to the query but not to `WindowEvent` means the panels can't read `e.tags` typed.
**How to avoid:** Add `tags: string[][]` to the `WindowEvent` interface alongside the query edit (and a doc-comment update — the existing comment says "exactly the five fields").
**Warning signs:** `Property 'tags' does not exist on type 'WindowEvent'`.

## Code Examples

### Nostr tag array shape (DRILL-03)
```typescript
// Source: [CITED: github.com/nostr-protocol/nips/blob/master/01.md]
// "Each tag is an array of one or more strings, with some conventions around them."
// p (mention): ["p", "<32-byte hex pubkey>", "<relay url optional>"]
// e (event ref): ["e", "<32-byte hex event id>", "<relay url optional>", "<author pubkey optional>"]
// t (hashtag, NIP-12 convention): ["t", "<hashtag>"]
// Robust read:
for (const ev of events) {
  for (const tag of ev.tags) {
    if (!Array.isArray(tag) || typeof tag[0] !== 'string') { malformed++; continue }
    const name = tag[0]
    const value = tag[1]
    if (name === 'e') { eventRefCount++; continue }
    if (typeof value !== 'string') { malformed++; continue }
    if (name === 'p') bumpCount(mentions, value)
    else if (name === 't') bumpCount(hashtags, value)
  }
}
```

### Lazy raw-by-id GraphQL document (DRILL-04)
```typescript
// Source: schema.graphql (EventFilterInput.ids; Event.raw) + codegen.ts client preset.
// src/queries/rawEvent.graphql.ts
import { graphql } from '../gql'
export const RawEventDocument = graphql(`
  query RawEvent($filter: EventFilterInput, $limit: Int) {
    events(filter: $filter, limit: $limit) {
      events { id raw }
    }
  }
`)
// call: client.query(RawEventDocument, { filter: { ids: [id] }, limit: 1 }, { requestPolicy: 'network-only' })
// (after editing, run `npm run codegen`)
```

### `tags` added to the window query (DRILL-03) — additive
```typescript
// src/queries/events.graphql.ts — add ONE line; raw stays OUT.
//   events { id pubkey kind createdAt content tags }   // <- tags added
// Then: npm run codegen ; update WindowEvent interface to add `tags: string[][]`.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Per-call inline error handling | Single `classify()` boundary | Phase 1 (FND-03) | The lazy raw fetch must route through it, not re-derive |
| Symmetric "pass/fail" signal verdicts | Asymmetric "suspicious-when-present, inconclusive-when-absent" (no clean field) | Phase 2 (DRILL-01) | All three new analyzers must follow it structurally |
| Generic similarity libs (string-similarity/MinHash) | Plain shingle+Jaccard+union-find at small n | this phase | Zero dependency; honest, debuggable |

**Deprecated/outdated:**
- Using `useQuery` for an on-demand action — defeats laziness; use the imperative `client.query` pattern already in `useAuthorWindow`.

## Runtime State Inventory

> Not applicable. Phase 3 is a **greenfield** feature addition (new pure analyzer modules + new panels + one additive query field + one new lazy query). No rename/refactor/migration; no stored data keys, service config, OS-registered state, secrets/env-var renames, or build-artifact renames are touched. Verified by: this phase only ADDS files under `src/analysis` and `src/views`, EDITS two existing files (`events.graphql.ts` selection, `useAuthorWindow.ts` `WindowEvent` type), and regenerates `src/gql/*` via codegen — none of which is runtime-cached state.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Union-find (vs greedy) is the right clustering choice for non-transitive near-dup similarity | Pattern 1 | LOW — both produce clusters; union-find is just more deterministic/honest. Corpus-validate the visual grouping. |
| A2 | k=3 shingle + Jaccard 0.8 are sane starting defaults | thresholds / Pattern 1 | MEDIUM — explicitly flagged corpus-validatable (STATE Phase-3 note); honesty framing holds regardless. Defaults live in `thresholds.ts` for one-place tuning. |
| A3 | "mass-mention"/"stuffing"/"high tag count" thresholds (e.g. ≥N mentions, ≥N hashtags, ≥N total tags on one event) | thresholds / Pattern 2 | MEDIUM — CONTEXT explicitly leaves the numbers to corpus validation; pick conservative defaults, document as tunable. |
| A4 | `kind` should be bounds-checked as `Number.isSafeInteger(kind) && kind >= 0` | Pattern 3 | LOW — mirrors `isSaneTs` discipline; worst case a forged kind is flagged out-of-range rather than charted. |
| A5 | The `t` (hashtag) tag is the widely-deployed convention, not strictly in NIP-01 | Pattern 2 / Code Examples | LOW — DRILL-03 success criterion explicitly names `t`; the corpus uses it. Parsing `tag[0]==='t'` is correct regardless of which NIP formalizes it. |
| A6 | Selecting `raw` requires a NEW document (no `event(id)` query); `ids` filter + `limit:1` is the path | Pattern 4 | NONE — verified against `schema.graphql` (only `events(filter)`, no `event(id)`); EventFilterInput has `ids`. |

## Open Questions

1. **Exact threshold numbers (Jaccard cutoff, mass-mention/stuffing/high-tag-count)**
   - What we know: defaults belong in `thresholds.ts`; honesty posture is threshold-independent (STATE/CONTEXT).
   - What's unclear: the corpus-calibrated values.
   - Recommendation: ship documented conservative defaults (`NEAR_DUP={k:3,jaccard:0.8}`; `TAGS` picks like massMention≈ a high percentile, highTagCount≈ a high per-event count), flag for the corpus-validation step (already a logged Phase-3 blocker in STATE). Do not block planning on them.

2. **Raw-inspector trigger UX (row-expand vs drawer vs modal)**
   - What we know: explicitly Claude's discretion; must be lazy + escaped `<pre>`.
   - Recommendation: row-inline expand (collapsible `<pre>` under the clicked row) is simplest, keeps the analyst in the timeline context, and needs no modal/focus-trap machinery. Decide in planning.

3. **`useMemo` on the analyzers**
   - What we know: analyzers re-run every render (like `analyzeRate`).
   - Recommendation: memoize near-dup (the only O(n²) one) keyed on `events` identity; tags/kinds are O(n·tags) and can stay plain. Decide during plan.

## Environment Availability

> Not applicable. Phase 3 is purely client-side code + config (new TS modules, one GraphQL field, codegen regen). The only external dependency is the lens, already wired in Phase 1 (`VITE_GRAPHQL_URL`, `client`, `classify`, readiness gate). No new tools, services, or runtimes are introduced. Codegen reads the checked-in `schema.graphql` (no live introspection needed — depth-limit fallback already in place).

## Project Constraints (from CLAUDE.md)

- **Monorepo boundary:** scope ALL work to `spamhunter/GraphQLExplorer`; do not touch sibling projects. `[CITED: /Users/g/git/deepfry/CLAUDE.md "Project Boundary Rule"]`
- **Commit to main, no feature branches.** `[CITED: MEMORY "Commit to main, no branches"]` (config `git.branching_strategy: none`)
- **GSD subagents anchor to git root** — pass absolute paths; the project `.planning` is at `spamhunter/GraphQLExplorer/.planning`, NOT the git toplevel. `[CITED: MEMORY "GSD subagents anchor to git root"]`
- **`graphql` pinned to v16 (16.14.2 exact); `nostr-tools` pinned 2.23.8.** `[VERIFIED: package.json]` — a `postinstall` pin-check (`scripts/check-graphql-pin.cjs`) enforces graphql v16; do not bump it.
- **Read-only API** — no mutations/subscriptions; analysis is client-side only. `[CITED: REQUIREMENTS Out of Scope]`
- **Escaped plaintext rendering, no `dangerouslySetInnerHTML`** for any attacker-controlled value. `[CITED: 03-UI-SPEC Security row]`

## Security Domain

> `security_enforcement: true`, ASVS Level 1 (`.planning/config.json`). Block-on: high.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | API is unauthenticated, single local analyst (REQUIREMENTS Out of Scope) |
| V3 Session Management | no | No sessions / no auth |
| V4 Access Control | no | Read-only single-user client; no privileged operations |
| V5 Input Validation / V5.3 Output Encoding | **yes** | React default text-node escaping for ALL attacker-controlled data (content, tags, hashtags, pubkeys, `raw` bytes); defensive tag-row parsing; `isSaneTs`/kind bounds-checks; `JSON.parse` wrapped in try/catch |
| V6 Cryptography | no | No crypto performed (sigs already verified on ingest by strfry — REQUIREMENTS) |
| V7 Error Handling | **yes** | Reuse `classify()` boundary; INTERNAL errors stay generic (no server internals leaked — already enforced in `errors.ts`); lazy-fetch errors map to UI-SPEC retryable/hard copy |
| V12 Files/Resources (DoS) | **yes (self-DoS)** | Bound the O(n²) near-dup comparison (Pitfall 1); `raw` fetched lazily/one-at-a-time to avoid pulling huge payloads (DRILL-04 rationale) |

### Known Threat Patterns for {React client over attacker-controlled Nostr events}

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Stored XSS via event content / hashtag / `raw` bytes rendered as HTML | Tampering / Elevation | Render ONLY as escaped JSX text nodes in `<pre>`/spans; NEVER `dangerouslySetInnerHTML` (UI-SPEC + CLAUDE.md) |
| Malformed/hostile `tags` rows crashing the aggregator | Denial of Service | Defensive `Array.isArray`/`typeof` guards; count malformed rows, never throw (Pitfall 3) |
| Forged 64-bit `kind`/`createdAt` mis-charting the histogram | Tampering | `isSaneTs` + kind bounds-check; flag out-of-range count, never bucket/mis-compute (Pattern 3) |
| Non-JSON / huge `raw` crashing the inspector | Denial of Service | `try/catch` around `JSON.parse`, render verbatim on failure (Pitfall 4); `white-space: pre-wrap; word-break: break-all` so long lines don't break layout |
| Quadratic near-dup blocking the main thread | Denial of Service (self) | Precompute shingle sets, size-disparity short-circuit, bound/`useMemo` (Pitfall 1) |
| Server internals leaked via lazy-fetch error | Information Disclosure | Reuse `classify()` → generic INTERNAL; UI-SPEC hard-error copy is generic |

## Sources

### Primary (HIGH confidence)
- In-session reads of the Phase-2 codebase: `src/analysis/rate.ts`, `rate.test.ts`, `thresholds.ts`, `src/hooks/useAuthorWindow.ts`, `src/views/RatePanel.tsx`, `AuthorDrillDown.tsx`, `WindowIndicator.tsx`, `src/transport/client.ts`, `errors.ts`, `src/queries/events.graphql.ts`, `stats.graphql.ts`, `src/gql/gql.ts`, `codegen.ts`, `package.json`, `schema.graphql` — the reuse blueprint.
- `.planning/phases/03-remaining-spam-signals/03-CONTEXT.md` + `03-UI-SPEC.md` — locked decisions + interaction contract.
- `.planning/config.json` — `nyquist_validation:false` (validation section omitted), `security_enforcement:true` ASVS L1.

### Secondary (MEDIUM confidence)
- [CITED: github.com/nostr-protocol/nips/blob/master/01.md] — tag array shape; `p`/`e` definitions; kind 0; kind categories.
- [CITED: github.com/nostr-protocol/nips README "Event Kinds"] — common kind numbers/names for the lookup table.

### Tertiary (LOW confidence)
- Threshold defaults (k=3, Jaccard 0.8, mass-mention/stuffing numbers) — training-knowledge standards, explicitly corpus-validatable (Assumptions A2/A3).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new deps; every existing dep verified in `package.json` and read in-session.
- Architecture / reuse: HIGH — every reuse target read directly; new logic is standard textbook (shingle/Jaccard/union-find).
- Pitfalls: HIGH — derived from the actual code (codegen string-keying, `WindowEvent` 5-field cast, `classify()` boundary, `isSaneTs`).
- Thresholds: LOW (by design) — defaults only; corpus-validation deferred (STATE Phase-3 blocker).

**Research date:** 2026-06-25
**Valid until:** 2026-07-25 (stable — internal codebase + settled NIP-01 facts; only the threshold numbers are expected to move, against the live corpus)

# Phase 3: Remaining Spam Signals - Pattern Map

**Mapped:** 2026-06-25
**Files analyzed:** 16 (10 new, 6 modified) — see File Classification
**Analogs found:** 16 / 16 (every file has a Phase 1/2 analog; this is overwhelmingly a *reuse + compose* phase)

> Project root: `/Users/g/git/deepfry/spamhunter/GraphQLExplorer` (a monorepo subproject; all paths below are relative to it unless absolute). Phase 3 adds ZERO runtime dependencies. The only genuinely new logic is the near-dup pipeline (~60 lines) and the tag aggregation (~40 lines); everything else copies a Phase-1/2 part directly.

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/analysis/nearDup.ts` (NEW) | analyzer (pure) | transform (window→clusters) | `src/analysis/rate.ts` | exact (pure-analyzer shape) |
| `src/analysis/tags.ts` (NEW) | analyzer (pure) | transform (window→top-N) | `src/analysis/rate.ts` | exact (pure-analyzer shape) |
| `src/analysis/kinds.ts` (NEW) | analyzer (pure) | transform (window→histogram) | `src/analysis/rate.ts` | exact (reuses `isSaneTs`) |
| `src/analysis/kindNames.ts` (NEW) | const/lookup table | transform | `src/analysis/thresholds.ts` (static `const` module) | role-match |
| `src/analysis/nearDup.test.ts` (NEW) | test | transform | `src/analysis/rate.test.ts` | exact |
| `src/analysis/tags.test.ts` (NEW) | test | transform | `src/analysis/rate.test.ts` | exact |
| `src/analysis/kinds.test.ts` (NEW) | test | transform | `src/analysis/rate.test.ts` | exact |
| `src/analysis/thresholds.ts` (EDIT) | config (tunables) | — | itself (additive: `NEAR_DUP`, `TAGS`) | self / exact |
| `src/views/DuplicatePanel.tsx` + `.module.css` (NEW) | component (panel) | request-response (read-only render) | `src/views/RatePanel.tsx` + `.module.css` | exact |
| `src/views/TagsPanel.tsx` + `.module.css` (NEW) | component (panel) | request-response (read-only render) | `src/views/RatePanel.tsx` + `.module.css` | exact |
| `src/views/KindsPanel.tsx` + `.module.css` (NEW) | component (panel) | request-response (read-only render) | `src/views/RatePanel.tsx` + `.module.css` | exact (hand-rolled bars) |
| `src/views/RawInspector.tsx` + `.module.css` (NEW) | component (lazy detail) | request-response (lazy single-event fetch) | `src/hooks/useAuthorWindow.ts` (fetch) + `RatePanel.module.css` (styling) | role-match (fetch is imperative, render is escaped `<pre>`) |
| `src/queries/rawEvent.graphql.ts` (NEW) | query document | request-response (single event by `ids`) | `src/queries/events.graphql.ts` | exact |
| `src/queries/events.graphql.ts` (EDIT) | query document | request-response | itself (additive: add `tags`; NOT `raw`) | self / exact |
| `src/hooks/useAuthorWindow.ts` (EDIT) | hook | request-response (pagination) | itself (add `tags: string[][]` to `WindowEvent`) | self / exact |
| `src/views/AuthorDrillDown.tsx` (EDIT) | view (host) | request-response | itself (mount 3 panels stacked; `RatePanel` mount line is the template) | self / exact |
| `src/gql/*` (REGEN) | generated | — | n/a — run `npm run codegen` after document edits | — |

Optional (Claude's discretion, RESEARCH §Structure): a `src/hooks/useRawEvent.ts` lazy-fetch hook may be extracted from `RawInspector.tsx` — same analog (`useAuthorWindow.ts` imperative fetch). Decide in planning.

---

## Pattern Assignments

### `src/analysis/nearDup.ts` (analyzer, pure transform)

**Analog:** `src/analysis/rate.ts`

**Module-header + import + asymmetry convention** (`rate.ts:1-20`): copy the doc-header style verbatim — state PURE / no React / no transport, state the asymmetry (suspicious-when-present, inconclusive-when-absent → **no `clean`/`ok`/`safe` field**), and pull tunables from `./thresholds`:
```typescript
// rate.ts:20
import { BURST } from './thresholds'
// nearDup.ts: import { NEAR_DUP } from './thresholds'
```

**Result-interface shape** (`rate.ts:37-48`): a named interface with a denominator field (`analyzedCount`), a "flagged but counted, never dropped" field (mirror `rejectedCount`), and the signal payload — but **NO clean/ok/safe key** (asymmetry is structural). For nearDup: `NearDupResult { analyzedCount; clusters: {kind:'exact'|'near'; memberIds:string[]; count:number}[]; duplicateCount; /* members across all clusters */ }`.

**Pure-function signature + degenerate-input guard** (`rate.ts:84-99`): filter/guard first, early-return an inconclusive empty result for `< 2` comparable items (no crash, no negative math):
```typescript
// rate.ts:91 — the < 2 guard pattern to mirror for nearDup's < 2 events
if (sane.length < 2) {
  return { analyzedCount: sane.length, rejectedCount, bins: [], burstDetected: false, tightestIntervalSec: null }
}
```

**New logic (RESEARCH § Pattern 1 — no analog in codebase, copy from RESEARCH):** `normalizeContent` (NFC + lowercase + collapse whitespace + trim; do NOT strip URLs/mentions/punctuation), `shingles(k=3)`, `jaccard`, and the `DSU` union-find class (RESEARCH lines 193-228). Bound the O(n²) (Pitfall 1): precompute each shingle `Set` once, size-disparity short-circuit `|a.size-b.size|/max > (1-jaccard)`.

---

### `src/analysis/tags.ts` (analyzer, pure transform)

**Analog:** `src/analysis/rate.ts`

**Same pure-analyzer scaffold** as nearDup (header, no clean field, `analyzedCount` denominator). The "count-malformed-but-don't-throw" discipline mirrors `rate.ts`'s `rejectedCount` (`rate.ts:40,87`): `malformedTagRows` counts skipped rows, never throws.

**Result interface** (RESEARCH lines 239-247 — already specified):
```typescript
export interface TagsResult {
  analyzedCount: number
  malformedTagRows: number          // mirrors rejectedCount — skipped, counted, never dropped
  topMentions: { value: string; count: number }[]   // p
  topHashtags: { value: string; count: number }[]    // t
  eventRefCount: number             // e references
  outlierEvents: { id: string; tagCount: number }[]  // over TAGS.highTagCount
  // NO clean/ok/safe field
}
```

**Defensive tag parsing** (RESEARCH lines 353-364 — schema `tags: [[String!]!]!` but values are attacker-controlled): guard `Array.isArray(tag) && typeof tag[0] === 'string'`, require `typeof tag[1] === 'string'` before counting a value; count skipped into `malformedTagRows`.

---

### `src/analysis/kinds.ts` (analyzer, pure transform)

**Analog:** `src/analysis/rate.ts` — and it **REUSES `isSaneTs` from rate.ts** (do not re-implement):
```typescript
// rate.ts:33-35 — import this, do not re-derive the createdAt bounds-check
export function isSaneTs(t: number): boolean {
  return Number.isSafeInteger(t) && t >= MIN_TS && t <= MAX_TS
}
// kinds.ts: import { isSaneTs } from './rate'
```

**Kind bounds-check** (RESEARCH Pattern 3, A4): `kind` is also forgeable 64-bit — accept `Number.isSafeInteger(kind) && kind >= 0`, flag the rest into an out-of-range count, **never bucket a forged kind**. Same epistemic discipline as `isSaneTs` (`rate.ts:11-17` header explains it).

**Histogram bins → bars:** produce `{ kind:number; count:number }[]` sorted, mirroring `rate.ts`'s `bins: {start;count}[]` (`rate.ts:43`) so `KindsPanel` can copy `RatePanel`'s bar JSX 1:1. Name lookup via `KIND_NAMES` (RESEARCH lines 258-262 / `src/analysis/kindNames.ts`).

---

### `src/analysis/thresholds.ts` (config — EDIT, additive)

**Analog:** itself. Append `NEAR_DUP` and `TAGS` alongside `BURST`, following the existing `as const` + literature-default + corpus-validation-deferred comment style (`thresholds.ts:1-17`):
```typescript
// thresholds.ts:13-17 — the existing BURST block is the template
export const BURST = { windowSec: 60, minEvents: 5, binSec: 3600 } as const
// add (defaults; corpus-validatable — RESEARCH A2/A3):
export const NEAR_DUP = { k: 3, jaccard: 0.8 } as const
export const TAGS = { highTagCount: /* conservative default */, massMention: /* */, stuffing: /* */ } as const
```

---

### `src/analysis/{nearDup,tags,kinds}.test.ts` (tests, TDD RED→GREEN)

**Analog:** `src/analysis/rate.test.ts`

**Vitest Node env, pure (no DOM/network)** (`rate.test.ts:12-15`):
```typescript
import { describe, it, expect } from 'vitest'
import { analyzeRate, isSaneTs, type RateResult } from './rate'
```

**The MANDATORY asymmetry test** (`rate.test.ts:35-43`) — copy verbatim per analyzer (assert the result has no clean/ok/safe key):
```typescript
it('does not expose any clean/ok/safe field (asymmetry is structural)', () => {
  const keys = Object.keys(result)
  expect(keys).not.toContain('clean'); expect(keys).not.toContain('ok'); expect(keys).not.toContain('safe')
})
```

**Degenerate-input tests** (`rate.test.ts:72-89`): 0 items and 1 item → inconclusive empty result, no crash. **Forged/out-of-range tests** (`rate.test.ts:91-103`): mirror for kinds (forged `kind`) — flagged & counted, not dropped. **Threshold-driven fixtures** read constants from `thresholds.ts` rather than hard-coding numbers (`rate.test.ts:15,49,57`).

---

### `src/views/DuplicatePanel.tsx` + `.module.css` (component, render)

**Analog:** `src/views/RatePanel.tsx` + `src/views/RatePanel.module.css`

**Props + re-derive-every-render + Intl formatter** (`RatePanel.tsx:26-32`):
```typescript
const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)
export function RatePanel({ events, windowMeta }: { events: WindowEvent[]; windowMeta: WindowMeta }) {
  const rate = analyzeRate(events.map((e) => e.createdAt))  // re-derives on Load more
```
DuplicatePanel signature is identical (`{ events, windowMeta }`); call `nearDup(...)` instead.

**Co-located `WindowIndicator` + amber-signal + persistent caveat structure** (`RatePanel.tsx:34-91`): `<section className={styles.panel} aria-label="...">`, head with title + conditional amber badge (`RatePanel.tsx:38-44`), then `<WindowIndicator meta={windowMeta} />` (`RatePanel.tsx:48`), then the body, then a persistent caveat `<p>` (`RatePanel.tsx:87-90`). For DuplicatePanel: cluster badges use `styles.burstBadge` analog (amber + dot + "near-duplicate"/"exact duplicate" label per UI-SPEC); zero-cluster renders a neutral muted fact ("No near-duplicates among the {N} fetched events"), never green.

**CSS — copy `RatePanel.module.css` wholesale** (`RatePanel.module.css:13-113`): `.panel` card (`--surface`, 1px `--border`, 8px radius, `--space-md` pad, `RatePanel.module.css:13-21`), `.burstBadge` amber-marker (`:40-51`), `.stateDot` (`:53-60`), `.caveat` muted body (`:111-113`). Amber = `--recoverable` ONLY; **no teal, no green** (header comment `:5-10`).

---

### `src/views/TagsPanel.tsx` + `.module.css` (component, render)

**Analog:** `src/views/RatePanel.tsx` + `.module.css` (panel shell) — same `{ events, windowMeta }` props, same re-derive (`analyzeTags(events)`), same co-located `WindowIndicator` + persistent asymmetry caveat.

Top-N lists: rows = mono value (truncated pubkey / escaped hashtag) + mono count + neutral `--border` dividers (UI-SPEC Typography "Data" role). Outlier flag reuses the `.burstBadge` amber pattern (`RatePanel.tsx:38-44`, `RatePanel.module.css:40-60`) with labels "high mention fan-out"/"hashtag stuffing"/"high tag count". `e`-references shown as a count.

---

### `src/views/KindsPanel.tsx` + `.module.css` (component, render — hand-rolled bars)

**Analog:** `src/views/RatePanel.tsx` (the bars are the closest match in the codebase)

**Hand-rolled CSS bars — copy `RatePanel.tsx:53-71` directly** (no chart lib; UI-SPEC constraint):
```typescript
const maxCount = rate.bins.reduce((m, b) => (b.count > m ? b.count : m), 0)  // RatePanel.tsx:32
// ...
{rate.bins.map((bin) => {
  const heightPct = maxCount > 0 ? Math.max(4, (bin.count / maxCount) * 100) : 4
  return (
    <span key={bin.start} className={styles.barSlot} title={`${bin.count}`}>
      <span className={styles.bar} style={{ height: `${heightPct}%` }} />
    </span>
  )
})}
```
**Bar CSS** (`RatePanel.module.css:62-92`): `.bars` flex/align-flex-end/`--border` baseline (`:63-71`), `.barSlot` (`:73-79`), `.bar` neutral `--text-muted` fill (`:81-86`). Kinds bars stay **neutral always** (kinds are neutral data — UI-SPEC); do NOT apply the `.barsBurst` amber tint to bars.

**Out-of-range flagged note** reuses the amber note pattern (`RatePanel.tsx:78-84` `rejectedNote` is the template): "{N} events with out-of-range kind/timestamp" — paired with a dot, never silently dropped. Per-bar labels: NIP name (sans 13px/600) + raw kind (mono); unknown → number + muted "(unknown kind)".

---

### `src/views/RawInspector.tsx` + `.module.css` (lazy single-event detail)

**Analog (fetch):** `src/hooks/useAuthorWindow.ts` — the imperative one-shot fetch (NOT `useQuery`, which fetches on mount → not lazy).

**Imperative fetch + MANDATORY throw-guard + classify-before-data** (`useAuthorWindow.ts:128-152`) — copy this skeleton for the on-"View raw"-click handler:
```typescript
const result = await client
  .query(RawEventDocument, { filter: { ids: [id] }, limit: 1 }, { requestPolicy: 'network-only' })
  .toPromise()
  .catch(() => 'THREW' as const)              // useAuthorWindow.ts:135 — mandatory guard
if (result === 'THREW') { /* NETWORK */ return }
const apiError = classify(result)             // useAuthorWindow.ts:151 — BEFORE reading data
if (apiError) { /* map per errorTreatment */ return }
const raw = result.data?.events?.events?.[0]?.raw   // undefined ⇒ zero-match note
```
Imports: `client` from `../transport/client`, `classify` from `../transport/errors` (`useAuthorWindow.ts:21-22`).

**Error → UI mapping:** reuse the `errorTreatment` switch from `AuthorDrillDown.tsx:118-144` to map `ApiError` → `{tone:'recoverable'|'hardFail', message}` (UI-SPEC retryable/hard rows). INTERNAL stays generic (no server internals — `errors.ts:11-14`).

**Escaped `<pre>` render (XSS-safe — V5.3 / UI-SPEC):** render `raw` as a JSX text node inside `<pre>` (React default escaping); **NEVER `dangerouslySetInnerHTML`**. Pretty-print only if it parses (Pitfall 4):
```typescript
let body: string
try { body = JSON.stringify(JSON.parse(raw), null, 2) } catch { body = raw /* verbatim */ }
// <pre> body </pre>  + caption: "Canonical bytes — escaped, not executed." | "Raw bytes shown verbatim (not valid JSON)."
```
**CSS:** `.panel`/`<pre>` from `RatePanel.module.css:13-21`; the `<pre>` needs `font-family: var(--font-mono)`, 13px, `white-space: pre-wrap; word-break: break-all`, `--border` 1px, `--space-md` pad (UI-SPEC Typography). Trigger/close = neutral chrome (`.copyButton` styling in `AuthorDrillDown` — never accent).

---

### `src/queries/rawEvent.graphql.ts` (query document, NEW)

**Analog:** `src/queries/events.graphql.ts`

**`graphql()` document via codegen client preset** (`events.graphql.ts:1,16-30`) — `raw` is selected ONLY here (never in `EventsDocument`). Schema confirmed: `EventFilterInput.ids: [String!]`, `Event.raw: String!`. There is no `event(id)` query — `ids` filter + `limit:1` is the single-event path.
```typescript
import { graphql } from '../gql'
export const RawEventDocument = graphql(`
  query RawEvent($filter: EventFilterInput, $limit: Int) {
    events(filter: $filter, limit: $limit) { events { id raw } }
  }
`)
// after creating: run `npm run codegen`
```

---

### `src/queries/events.graphql.ts` (query document, EDIT — additive)

**Analog:** itself. Add ONE field `tags` to the selection (`events.graphql.ts:19-25`); keep `raw` OUT (lazy-only). Update the doc-comment that currently says "ONLY the five fields" (`events.graphql.ts:6-10`).
```graphql
events { id pubkey kind createdAt content tags }   # tags added; raw stays OUT
```
Then `npm run codegen` (Pitfall 5: the client preset keys on the verbatim query string — a changed string MUST be regenerated or `tags` is untyped and `tsc -b` fails).

---

### `src/hooks/useAuthorWindow.ts` (hook, EDIT — additive)

**Analog:** itself. Add `tags: string[][]` to the `WindowEvent` interface (`useAuthorWindow.ts:30-36`) alongside the query edit (Pitfall 6 — the hook casts `page.events as WindowEvent[]` at `:185`; a missing field leaves panels unable to read `e.tags` typed). Update the "exactly the five fields" doc-comment (`:29`). No fetch-logic change — `requestPolicy: 'network-only'` (`:128-133`) already in place; panels consume the window read-only.

---

### `src/views/AuthorDrillDown.tsx` (host view, EDIT)

**Analog:** itself — the existing `RatePanel` mount is the exact template for the three new panels.

**Stacked-panel mount in the loaded branch** (`AuthorDrillDown.tsx:240-243`) — add the three new panels right beside `RatePanel`, all in the `>= 1 event` branch (the zero-match branch `:197-208` has nothing to analyze):
```tsx
// AuthorDrillDown.tsx:243 — the template line; stack the 3 new panels after it
<RatePanel events={events} windowMeta={windowMeta} />
// + <DuplicatePanel events={events} windowMeta={windowMeta} />
// + <TagsPanel events={events} windowMeta={windowMeta} />
// + <KindsPanel events={events} windowMeta={windowMeta} />
```
Import the new panels alongside `RatePanel` (`:32`). The "View raw" per-row trigger mounts in `TimelineRow` (`:147-157`) — neutral chrome, fires the lazy `RawInspector` fetch. Keep gating order (connecting → error-shell → loaded) unchanged.

---

## Shared Patterns

### Pure-analyzer convention (no clean field, denominator, count-don't-drop)
**Source:** `src/analysis/rate.ts:1-48,84-99`
**Apply to:** `nearDup.ts`, `tags.ts`, `kinds.ts` (and their tests)
- Module header states PURE / no React-transport, states the asymmetry.
- Result interface has a denominator (`analyzedCount`), a flagged-but-counted field (`rejectedCount`/`malformedTagRows`/out-of-range count), the signal payload, and **NO `clean`/`ok`/`safe` key**.
- Degenerate input → inconclusive empty result, no crash, no negative math.

### Forgeable-value bounds-check (reuse, don't re-implement)
**Source:** `src/analysis/rate.ts:33-35` (`isSaneTs`, `MIN_TS`, `MAX_TS`)
**Apply to:** `kinds.ts` — `import { isSaneTs } from './rate'` for `createdAt`; add the analogous `Number.isSafeInteger(kind) && kind >= 0` check for `kind`. Never bucket/mis-compute a forged value — flag into a count.

### Imperative transport boundary (lazy fetch)
**Source:** `src/hooks/useAuthorWindow.ts:128-152` + `src/transport/client.ts` + `src/transport/errors.ts:49-81`
**Apply to:** `RawInspector.tsx` (and optional `useRawEvent.ts`)
- `client.query(doc, vars, { requestPolicy: 'network-only' }).toPromise().catch(() => 'THREW')` — the mandatory throw-guard.
- `classify(result)` BEFORE reading `result.data` (errors arrive on HTTP 200).
- Map the returned `ApiError` via the `errorTreatment` switch (`AuthorDrillDown.tsx:118-144`).

### Non-removable window denominator
**Source:** `src/views/WindowIndicator.tsx:26-61` (consumed as `<WindowIndicator meta={windowMeta} />`)
**Apply to:** `DuplicatePanel`, `TagsPanel`, `KindsPanel` — co-locate one each, even at N=0. (NOT on `RawInspector` — it is a per-event drill, not a window-wide signal.)

### Panel shell + hand-rolled bars + amber-on-signal (never teal/green)
**Source:** `src/views/RatePanel.tsx:29-93` + `src/views/RatePanel.module.css:13-113`
**Apply to:** all three panels. `--surface` card / 1px `--border` / 8px radius / `--space-md` pad; 13px/600 muted title; bars neutral `--text-muted` on a `--border` baseline; a detected signal tints `--recoverable` (amber) ALWAYS paired with a text label + a dot/badge shape; persistent caveat `<p>`. **No `--accent` (teal), no green/success color** anywhere in any panel (UI-SPEC accent reservation #3 is the Inspect-author submit only).

### Escaped-plaintext rendering (XSS-safe)
**Source:** `RatePanel.tsx:18-19` header rule; `AuthorDrillDown.tsx:154` (`{event.content}` text node); `WindowIndicator.tsx:11-13`
**Apply to:** all attacker-controlled values — content previews, hashtags, mentioned pubkeys, `raw` bytes. Render as JSX text nodes only; **NEVER `dangerouslySetInnerHTML`**. Wrap `JSON.parse(raw)` in try/catch (render verbatim on failure).

### Locale number formatting
**Source:** `RatePanel.tsx:26-27` / `WindowIndicator.tsx:18-19` (`new Intl.NumberFormat()`)
**Apply to:** every count / denominator in the three panels.

### Codegen-after-document-edit (build gate)
**Source:** `src/queries/events.graphql.ts:1,16` (the `graphql()` client-preset document)
**Apply to:** after editing `events.graphql.ts` (add `tags`) and creating `rawEvent.graphql.ts`, run `npm run codegen`, then `npm run build` / `npm test`. The preset keys on the verbatim string (Pitfall 5).

---

## No Analog Found

No file is fully analog-less. Two pieces of *internal logic* (not files) have no codebase precedent and must be copied from RESEARCH rather than an existing file:

| Logic | Lives in | Source instead of codebase |
|-------|----------|----------------------------|
| Two-stage near-dup pipeline (`normalizeContent` / `shingles` / `jaccard` / `DSU` union-find) | `src/analysis/nearDup.ts` | RESEARCH § Pattern 1 (lines 186-228) — standard textbook, zero deps |
| Defensive `p`/`e`/`t` tag aggregation + top-N + outlier | `src/analysis/tags.ts` | RESEARCH § Pattern 2 + Code Examples (lines 230-247, 353-364) |
| `KIND_NAMES` NIP lookup table | `src/analysis/kindNames.ts` | RESEARCH § Pattern 3 (lines 258-262) |

The *file shape* around each of these is still the `rate.ts` pure-analyzer convention above.

---

## Metadata

**Analog search scope:** `src/analysis/`, `src/views/`, `src/hooks/`, `src/queries/`, `src/transport/`; `schema.graphql` (root) for `EventFilterInput.ids` / `Event.tags` / `Event.raw`.
**Files scanned (read in full this session):** `rate.ts`, `rate.test.ts`, `thresholds.ts`, `useAuthorWindow.ts`, `RatePanel.tsx`, `RatePanel.module.css`, `WindowIndicator.tsx`, `AuthorDrillDown.tsx`, `client.ts`, `errors.ts`, `events.graphql.ts`, `schema.graphql` (targeted) + the three Phase-3 planning docs.
**Pattern extraction date:** 2026-06-25

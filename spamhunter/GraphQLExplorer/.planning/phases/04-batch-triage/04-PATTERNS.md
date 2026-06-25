# Phase 4: Batch Triage - Pattern Map

**Mapped:** 2026-06-25
**Files analyzed:** 18 (new + modified)
**Analogs found:** 18 / 18 (every file has a Phase 1–3 analog; zero greenfield)

> All paths are absolute under the project root
> `/Users/g/git/deepfry/spamhunter/GraphQLExplorer`. This phase is ~90% composition of
> existing, tested parts — the planner should treat each "Analog" below as the file to
> copy structure/discipline from, and the cited line ranges as the exact excerpts.

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/analysis/chunk.ts` (NEW) | utility (pure) | transform | `src/analysis/rate.ts` (pure module + thresholds import) | role-match |
| `src/analysis/chunk.test.ts` (NEW) | test | transform | `src/analysis/nearDup.test.ts` | exact |
| `src/analysis/mergeByAuthor.ts` (NEW) | utility (pure) | transform (left-join) | `src/hooks/useAuthorWindow.ts` `deriveWindowMeta` (pure derive) + `src/analysis/tags.ts` | role-match |
| `src/analysis/mergeByAuthor.test.ts` (NEW) | test | transform | `src/analysis/nearDup.test.ts` | exact |
| `src/analysis/triage.ts` (NEW) | utility (pure adapter) | transform | `src/analysis/tags.ts` (analyzer fan-in) | role-match |
| `src/analysis/triage.test.ts` (NEW) | test | transform | `src/analysis/nearDup.test.ts` | exact |
| `src/analysis/thresholds.ts` (MODIFY — add `TRIAGE`) | config | — | existing `BURST`/`NEAR_DUP`/`TAGS` blocks in same file | exact |
| `src/queries/latestPerAuthor.graphql.ts` (NEW) | query doc | request-response | `src/queries/events.graphql.ts` | exact |
| `src/queries/authors.graphql.ts` (NEW) | query doc | request-response (cursor) | `src/queries/events.graphql.ts` (+ `rawEvent.graphql.ts`) | exact |
| `src/hooks/useLatestPerAuthor.ts` (NEW) | hook | batch / request-response | `src/hooks/useAuthorWindow.ts` | role+flow-match |
| `src/hooks/useAuthorEnumeration.ts` (NEW) | hook | streaming / cursor pagination | `src/hooks/useAuthorWindow.ts` (cursor loop + INVALID_CURSOR) | role+flow-match |
| `src/views/BatchImport.tsx` (NEW) | component (view) | event-driven (form/file) | `src/views/SuspectEntryBar.tsx` (form + parseIdentifier + accent submit) | role-match |
| `src/views/BatchTriage.module.css` (NEW) | config (CSS) | — | `src/views/RatePanel.module.css` | role-match |
| `src/views/TriageTable.tsx` (NEW) | component (view) | request-response (render) | `src/views/AuthorDrillDown.tsx` (table/rows + WindowIndicator + error treatment) | role-match |
| `src/views/TriageTable.module.css` (NEW, or shared w/ BatchTriage) | config (CSS) | — | `src/views/RatePanel.module.css` | role-match |
| `src/router/hashRouter.ts` (MODIFY — add `batch` variant) | router | — | itself (existing union + matcher) | exact |
| `src/App.tsx` (MODIFY — add `#/batch` outlet + nav) | component (shell) | — | itself (existing route outlet switch) | exact |
| `npm run codegen` (BLOCKING build step) | build | — | `package.json` `codegen` script + `codegen.ts` | exact |

> The UI-SPEC keeps BatchImport + TriageTable as the two surfaces behind one `#/batch`
> route. The planner may collapse both CSS modules into one `BatchTriage.module.css`
> (RESEARCH structure names it once) — both are valid; pick one and reference it from both views.

---

## Pattern Assignments

### `src/analysis/chunk.ts` (utility, transform)

**Analog:** `src/analysis/rate.ts` (a pure module that imports its constants from
`./thresholds` and exports named pure functions + exported test-visible helpers).

**Module-header + thresholds-import pattern** (rate.ts lines 1-20): copy the doc-comment
posture (what / why / asymmetry-or-bounds note) and the single-tunable-home import:
```typescript
// thresholds.ts is the single tunable home for the ... constants (CONTEXT discretion).
import { BURST } from './thresholds'   // → import { TRIAGE } from './thresholds'
```

**Exported-pure-constants + pure-function shape** (RESEARCH § Pattern 2; this is the
target shape — analog is the export style of rate.ts `MIN_TS`/`MAX_TS`/`isSaneTs`):
```typescript
export const SAFE_BYTES_PER_AUTHOR = 80   // measured 67; UTF-8/whitespace/proxy margin
export const SAFE_FIXED_OVERHEAD = 4096
export const BODY_LIMIT_BYTES = 256 * 1024
export function byteBudgetAuthors(): number {
  return Math.floor((BODY_LIMIT_BYTES - SAFE_FIXED_OVERHEAD) / SAFE_BYTES_PER_AUTHOR) // ≈3225
}
export function chunkSize(): number {
  return Math.min(TRIAGE.chunkAuthors /* 500 */, 1000 /* hard cap */, byteBudgetAuthors())
}
export function chunkAuthors(hexes: string[], size: number): string[][] { /* slice loop */ }
```
The 413 halve-and-retry recursion (RESEARCH § Pattern 3) lives in the **hook**, not here;
`chunk.ts` is pure (the recursion's `chunk.slice(0, mid)` is a chunk op, but the fetch is the hook's).

---

### `src/analysis/mergeByAuthor.ts` (utility, transform — load-bearing left-join)

**Analog:** `src/hooks/useAuthorWindow.ts` `deriveWindowMeta` (lines 76-87) — the project's
established "extracted pure derive function for testability" pattern. Output-interface
style follows `src/analysis/tags.ts` `TagsResult` (tags.ts lines 21-38).

**Pure-derive extraction comment** (useAuthorWindow.ts lines 71-75):
```typescript
/**
 * Pure window-denominator derivation (extracted for clarity + testability). ... Pure —
 * no React/network.
 */
export function deriveWindowMeta(events: WindowEvent[], hasMore: boolean): WindowMeta { ... }
```

**Target left-join (the single most important correctness pin — RESEARCH § Pattern 4 /
Pitfall 1 / CONTEXT BATCH-03). Pin it with a comment AND the unit test:**
```typescript
export interface TriageRow { author: string; events: WindowEvent[] }
export function mergeByAuthor(
  inputHexes: string[],
  groups: { author: string; events: WindowEvent[] }[],
): TriageRow[] {
  const byAuthor = new Map<string, WindowEvent[]>()
  for (const g of groups) byAuthor.set(g.author, g.events)   // key STRICTLY by author
  return inputHexes.map((hex) => ({ author: hex, events: byAuthor.get(hex) ?? [] })) // LEFT join
  // NEVER index-zip: latestPerAuthor OMITS zero-match authors (contract §5/§8), so
  // groups[i].author !== inputHexes[i]. A missing author ⇒ events:[] ⇒ explicit "0 events".
}
```
Reuse `WindowEvent` from `src/hooks/useAuthorWindow.ts` (lines 34-41) as the row event type —
do NOT define a second event interface.

---

### `src/analysis/triage.ts` (utility, pure adapter — fan-in over the 4 analyzers)

**Analog:** `src/analysis/tags.ts` — the analyzer that maps a window to a result object with
boolean signal-present flags (`massMention`/`stuffing`, tags.ts lines 34-37, 102-121).

**Reuse the existing analyzer signatures verbatim** (do NOT re-implement detection). The
three analyzers consumed:
- `analyzeRate(createdAts: number[]) → { burstDetected, ... }` — `src/analysis/rate.ts:84`, flag at `:44`. `isSaneTs` is applied INSIDE `analyzeRate` (rate.ts:85), so pass raw `e.createdAt`.
- `nearDup(events: { id; content }[]) → { duplicateCount, ... }` — `src/analysis/nearDup.ts:103`, count at `:95`.
- `analyzeTags(events: { id; tags }[]) → { massMention, stuffing, ... }` — `src/analysis/tags.ts:57`, flags at `:36-37`.

**Target adapter** (RESEARCH § Pattern 5):
```typescript
import { analyzeRate } from './rate'
import { nearDup } from './nearDup'
import { analyzeTags } from './tags'
import type { WindowEvent } from '../hooks/useAuthorWindow'
export interface TriageIndicators { eventCount: number; burst: boolean; nearDup: boolean; tagFanOut: boolean }
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
**Asymmetry to carry** (tags.ts header lines 4-9): NO `clean`/`ok`/`safe` field — the four
indicators are signal-present flags + a neutral count; absence ≠ clean.

---

### `src/analysis/thresholds.ts` (config — ADD a `TRIAGE` block)

**Analog:** the existing `BURST` (lines 13-17), `NEAR_DUP` (lines 25-28), `TAGS` (lines
35-39) blocks IN THIS SAME FILE. Copy the exact `export const X = { ... } as const` shape
+ the "single tunable home / sane defaults / honesty posture is threshold-independent"
doc-comment voice.

**Add** (RESEARCH § Summary + Assumptions A1–A5 — all under CONTEXT "Claude's Discretion"):
```typescript
export const TRIAGE = {
  kind: 1,            // text notes — the spam-bearing kind (BATCH-02; tunable)
  perAuthor: 5,       // deliberately tiny per-author window — a FIRST-PASS SCREEN
  chunkAuthors: 500,  // conservative static chunk; the ≤1000-author cap binds before 256 KiB
  largeSetWarn: 1000, // non-blocking warning threshold above this many authors
  enumLimit: 500,     // authors() page size ceiling (contract §12)
  // file-size / byte-estimate constants may live here too (chunk.ts owns the byte math)
} as const
```

---

### `src/queries/latestPerAuthor.graphql.ts` (query doc, request-response)

**Analog:** `src/queries/events.graphql.ts` (the `graphql()` document + field-selection
rationale comment).

**Imports + document shape** (events.graphql.ts lines 1, 17-32):
```typescript
import { graphql } from '../gql'
export const LatestPerAuthorDocument = graphql(`
  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {
    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {
      author
      events { id pubkey kind createdAt content tags }   # tags + content REQUIRED; raw EXCLUDED
    }
  }
`)
```
**CRITICAL (RESEARCH Pitfall 6):** select the SAME six fields events.graphql.ts selects
(`id pubkey kind createdAt content tags`) — `tags` feeds tagFanOut, `content` feeds nearDup.
Exclude `raw` (large, unused — same rationale as events.graphql.ts lines 8-11). Schema
confirmed: `latestPerAuthor(kind: Int!, perAuthor: Int!, authors: [String!]!): [AuthorGroup!]!`
(schema.graphql:12, `AuthorGroup` at :40).

---

### `src/queries/authors.graphql.ts` (query doc, cursor request-response)

**Analog:** `src/queries/events.graphql.ts` (document shape) + `rawEvent.graphql.ts` (a
second small document in the same dir).

**Target** (RESEARCH § Code Examples; schema `authors(after: String, limit: Int): AuthorsPage!`
at schema.graphql:13, `AuthorsPage { authors endCursor hasMore }` at :34):
```typescript
import { graphql } from '../gql'
export const AuthorsDocument = graphql(`
  query Authors($after: String, $limit: Int) {
    authors(after: $after, limit: $limit) { authors endCursor hasMore }
  }
`)
```

> **BLOCKING after both documents land:** run `npm run codegen` BEFORE the hooks consume
> `LatestPerAuthorDocument`/`AuthorsDocument` (RESEARCH Pitfall 7). Codegen reads the
> checked-in `./schema.graphql` (codegen.ts:13), never live introspection.

---

### `src/hooks/useLatestPerAuthor.ts` (hook, batch / request-response)

**Analog:** `src/hooks/useAuthorWindow.ts` — copy the classify-before-data discipline,
the throw-guard, the `runId` stale-drop, and the in-flight ref VERBATIM.

**Imports** (useAuthorWindow.ts lines 20-23):
```typescript
import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../transport/client'
import { classify, type ApiError } from '../transport/errors'
import { LatestPerAuthorDocument } from '../queries/latestPerAuthor.graphql'
```

**Throw-guard + network-only + classify-before-data** (useAuthorWindow.ts lines 133-198 —
reuse this EXACT shape per chunk; RESEARCH § Pattern 1):
```typescript
const result = await client
  .query(LatestPerAuthorDocument,
    { kind: TRIAGE.kind, perAuthor: TRIAGE.perAuthor, authors: chunk },
    { requestPolicy: 'network-only' })          // honesty: re-triage must hit the network
  .toPromise()
  .catch(() => 'THREW' as const)                // MANDATORY throw-guard (WR-04)
if (myRun !== runId.current) { /* superseded — drop */ return }
if (result === 'THREW') { /* NETWORK — retain partial, offer retry */ }
const apiError = classify(result)               // BEFORE result.data (errors on HTTP 200)
if (apiError) { /* 413 halve, 503/INVALID retry, INTERNAL hard-fail — NEVER kill batch */ }
const groups = result.data?.latestPerAuthor     // only now safe to read
```

**Stale-drop run token** (useAuthorWindow.ts lines 105, 143-146, 207-223) — reuse the
`runId` ref pattern so a restarted batch drops late chunk resolvers (RESEARCH Pitfall 5).

**413 halve-and-retry** (RESEARCH § Pattern 3 — NEW logic, bottoms at chunk length 1):
```typescript
if (apiError?.kind === 'PAYLOAD_TOO_LARGE' && chunk.length > 1) {
  const mid = Math.ceil(chunk.length / 2)
  return [...await fetchChunk(chunk.slice(0, mid)), ...await fetchChunk(chunk.slice(mid))]
}
```
Chunk the deduped input via `chunkAuthors(hexes, chunkSize())` from `chunk.ts`; merge with
`mergeByAuthor(dedupedInputHexes, accumulatedGroups)` after each chunk (incremental re-merge,
RESEARCH Open Q1). Pacing: sequential (or small bounded concurrency — A3 discretion).

---

### `src/hooks/useAuthorEnumeration.ts` (hook, streaming / cursor pagination)

**Analog:** `src/hooks/useAuthorWindow.ts` cursor loop — specifically the opaque-cursor
discipline and the bounded INVALID_CURSOR restart.

**INVALID_CURSOR bounded-restart guard** (useAuthorWindow.ts lines 106-113 doc + 160-181
logic; RESEARCH Pitfall 4) — copy the `cursorRetry` ref budget so a looping lens can't spin
forever:
```typescript
// allow exactly one cursor-drop retry; a second consecutive INVALID_CURSOR — or
// INVALID_CURSOR on a null cursor — surfaces the error instead of recursing.
if (apiError.kind === 'INVALID_CURSOR') {
  if (cursor === null || cursorRetry.current >= 1) { setError(apiError); return }
  cursorRetry.current += 1; after.current = null; seen.clear(); /* restart page 1 */
}
```

**Opaque-cursor verbatim loop** (useAuthorWindow.ts lines 193 `after.current = page.endCursor
?? null`; RESEARCH § Pattern 6 — NEW: a Stop ref + a live running-count + accumulate-to-Set):
```typescript
let after: string | null = null
const seen = new Set<string>()
do {
  if (stopRequested.current) break                                    // Stop control
  const result = await client.query(AuthorsDocument,
    { after, limit: TRIAGE.enumLimit }, { requestPolicy: 'network-only' })
    .toPromise().catch(() => 'THREW' as const)
  const err = classify(result)                                        // BEFORE data
  if (err?.kind === 'INVALID_CURSOR') { /* bounded restart, see above */ continue }
  if (err) break                                                      // 503/NETWORK/INTERNAL — keep partial
  const page = result.data!.authors
  for (const pk of page.authors) seen.add(pk)
  setRunningCount(seen.size)                                          // live snapshot count
  after = page.endCursor ?? null                                     // opaque, verbatim
} while (after && !stopRequested.current)
```
The enumerated set feeds the SAME `useLatestPerAuthor` pipeline (one code path — CONTEXT BATCH-04).

---

### `src/views/BatchImport.tsx` (component, event-driven form/file)

**Analog:** `src/views/SuspectEntryBar.tsx` — the form + `parseIdentifier` + accent-submit
+ inline amber note pattern.

**parseIdentifier reuse + accent submit** (SuspectEntryBar.tsx lines 22-24, 33-45, 63-65):
```typescript
import { parseIdentifier } from '../identifier/identifier'   // single sanctioned nip19 site
// ... run EACH token through parseIdentifier; accept hex/npub/nprofile, reject note/nsec.
<button type="submit" className={styles.submit} disabled={submitDisabled}>Triage</button>
```
**Tokenize + normalize + dedupe + COUNT** (CONTEXT BATCH-01; RESEARCH § Don't Hand-Roll):
split on `/[\s,]+/`, route each through `parseIdentifier`, lowercase-hex into a `Set`, count
`valid / duplicates / unparseable` and LIST the unparseable tokens (escaped mono — never
silently drop). The unparseable list + import summary is the BatchImport honesty surface.

**File source:** browser `FileReader.readAsText` (platform, not a library — RESEARCH § Don't
Hand-Roll); enforce a max-file-size constant (V12 — file read in-browser, never uploaded).

**Accent reservation (UI-SPEC):** the **"Triage"** submit is the ONE accent action this
phase; "enumerate corpus", Stop, file picker, paste textarea all stay neutral chrome —
mirror SuspectEntryBar's neutral input + accent submit split (SuspectEntryBar.tsx lines 19-21).

**Escaped plaintext** (SuspectEntryBar.tsx lines 16-18): all pasted/uploaded/enumerated
tokens render via JSX interpolation only — never `dangerouslySetInnerHTML` (V5/XSS).

---

### `src/views/TriageTable.tsx` (component, render — sortable table + indicators + drill-in)

**Analog:** `src/views/AuthorDrillDown.tsx` — the table/rows render, the `WindowIndicator`
co-location, the `errorTreatment` switch, and the row→hash navigation.

**WindowIndicator reuse for batch denominators** (AuthorDrillDown.tsx lines 218-219, 230-231;
WindowIndicator.tsx whole file) — the batch needs "triaged N of M authors" and "N authors
snapshot". WindowIndicator currently takes `{ meta: WindowMeta }` (WindowIndicator.tsx:26);
the planner either (a) extends it with a batch variant or (b) builds a sibling using the
same `Intl.NumberFormat` `formatInt` + amber-on-partial treatment (WindowIndicator.tsx
lines 18-19, 51-60). Prefer reusing `formatInt` + the partial-amber `<span>` pattern.

**Header-row + data-row + cell render** (AuthorDrillDown.tsx lines 163-176, 233-243): copy
the hand-rolled header-row + `events.map(...)` row render shape (NO table/grid library —
UI-SPEC). Sorting is a pure local `.sort()` on the merged `TriageRow[]` (default: event
count desc), no refetch.

**Row → existing `#/a/<hex>` drill-in** (SuspectEntryBar.tsx:39 / AuthorDrillDown.tsx:44-46
`window.location.hash`):
```typescript
onClick={() => { window.location.hash = '#/a/' + row.author }}   // row.author is normalized lowercase hex
```
Reuse the EXISTING `#/a/<hex>` route — no new drill-down code (CONTEXT IN-02 / RESEARCH).

**errorTreatment per-chunk note** (AuthorDrillDown.tsx lines 91-156): reuse the
`errorTreatment(error)` switch + `InlineNote`/`ErrorShell` tone mapping for per-chunk
recoverable (413/503/INVALID, retain partial + Retry) vs hard-fail (INTERNAL, generic).

**Per-signal indicator color rules (load-bearing — RatePanel.tsx is the analog):**
amber-on-signal chip + text label + dot, neutral dash when absent, NO green/teal, NO
"clean" column (RatePanel.tsx lines 38-44 burstBadge; RatePanel.module.css lines 39-50).
"0 events" is explicit muted data, not omission (UI-SPEC State Treatments).

---

### `src/views/BatchTriage.module.css` / `TriageTable.module.css` (config, CSS)

**Analog:** `src/views/RatePanel.module.css` — the panel/card shape + token-only discipline +
the amber-on-signal color rule.

**Card + token use** (RatePanel.module.css lines 13-37): `--surface` fill, `--border` 1px,
8px radius, `--space-md` padding, 13px/600 muted title. **Amber-on-signal chip**
(RatePanel.module.css lines 39-50, `.burstBadge` — `color: var(--recoverable)` + dot +
label). NO new tokens; consume `src/styles/tokens.css` only (UI-SPEC Design System). Teal
(`--accent`) appears ONLY on the Triage submit, NOWHERE in the table/indicators.

---

### `src/router/hashRouter.ts` (router — ADD `batch` variant)

**Analog:** itself — the existing `Route` union (lines 15-18) + `parseHash` matcher (lines
24-29). Extend, leave `home`/`author`/`notfound` intact (RESEARCH § hashRouter snippet):
```typescript
export type Route =
  | { name: 'home' }
  | { name: 'batch' }                              // NEW
  | { name: 'author'; hex: string }
  | { name: 'notfound' }
// in parseHash, BEFORE the AUTHOR_HASH match:
if (hash === '#/batch') return { name: 'batch' }   // NEW (exact match)
```
**Test extension** (Wave 0 gap): `#/batch` → `{ name: 'batch' }`; unknown still `notfound`.
Test analog: `src/analysis/nearDup.test.ts` describe/it/expect structure.

---

### `src/App.tsx` (component, shell — ADD `#/batch` outlet + nav)

**Analog:** itself — the existing route-outlet switch (App.tsx lines 54-58) + the shell
header (lines 49-53). Add:
```typescript
{route.name === 'batch' && <BatchTriage />}        // new outlet line
```
Add a NEUTRAL nav affordance to `#/batch` in the shell header (UI-SPEC App-shell nav —
NOT accent; accent stays on the two "go" submits). The readiness gate (App.tsx lines 32-45)
and `ConnectingShell` are preserved verbatim.

---

## Shared Patterns

### Classify-before-data fetch boundary
**Source:** `src/transport/errors.ts` `classify()` (lines 49-81) + `src/transport/client.ts`
(the POST-only client, lines 13-17).
**Apply to:** `useLatestPerAuthor`, `useAuthorEnumeration` — call `classify(result)` BEFORE
reading `result.data` on EVERY chunk and EVERY enumeration page. The 7-kind union already
models the exact errors batch chunking must respect: `TOO_MANY_AUTHORS` (errors.ts:21,
avoided by chunking), `PAYLOAD_TOO_LARGE`/413 (errors.ts:25/57, halve-and-retry),
`INVALID_CURSOR` (errors.ts:20/63, bounded restart), `NOT_READY`/503 (errors.ts:24/56,
backoff), `INTERNAL` (errors.ts:23, generic no-leak), `NETWORK` (errors.ts:26).
```typescript
const apiError = classify(result)   // returns null only when safe to read result.data
if (apiError) { /* branch by apiError.kind */ }
```

### Throw-guard + network-only + run-token stale-drop
**Source:** `src/hooks/useAuthorWindow.ts` (throw-guard lines 139-140; network-only lines
133-138; runId lines 105/143-146/207-223; in-flight ref lines 97-98).
**Apply to:** both new hooks — the throw-guard maps a rejected exchange to NETWORK, the
run-token drops superseded results, network-only keeps the batch honest (a re-triage hits
the network, never a cached page).

### Single sanctioned identifier normalizer
**Source:** `src/identifier/identifier.ts` `parseIdentifier` (lines 43-79) + `isHexPubkey`
(lines 39-41).
**Apply to:** `BatchImport` tokenizer — route EVERY token through `parseIdentifier`; it
already accepts hex/npub/nprofile and rejects `note`/`nsec` (identifier.ts lines 69-74). A
second nip19 call site is forbidden (anti-pattern). Use `isHexPubkey` if a hex-shape gate is
needed before navigation.

### Window-honesty denominator
**Source:** `src/views/WindowIndicator.tsx` (whole file; `formatInt` lines 18-19; amber-
partial `<span>` lines 51-60).
**Apply to:** the two batch denominators ("triaged N of M authors", "N authors snapshot") —
reuse `Intl.NumberFormat` `formatInt` + the amber-on-partial emphasis; non-removable while a
table is shown (no dismiss). The per-author `perAuthor=5` window is carried by the persistent
first-pass-screen framing line (UI-SPEC).

### Pure analyzer + thresholds single-home + extracted-pure-derive
**Source:** `src/analysis/{rate,nearDup,tags}.ts` (asymmetric posture, `as const` outputs,
NO clean field) + `src/analysis/thresholds.ts` (single tunable home) + `deriveWindowMeta`
(useAuthorWindow.ts lines 76-87, the "extract pure for testability" convention).
**Apply to:** `chunk.ts`, `mergeByAuthor.ts`, `triage.ts` — all pure, Node-vitest-testable,
import constants from `thresholds.ts`, carry NO clean/ok/safe field.

### Accent reservation + escaped-plaintext + amber-on-signal CSS
**Source:** `src/views/SuspectEntryBar.tsx` (accent submit + escaped JSX, lines 16-21,
63-65) + `src/views/RatePanel.module.css` (amber-on-signal chip, lines 39-50).
**Apply to:** `BatchImport` ("Triage" = the one new accent action), `TriageTable`
(amber-on-signal chips, neutral dash on absence, no green/teal/clean), all CSS modules
(token-only, no new tokens).

### graphql() document + BLOCKING codegen
**Source:** `src/queries/events.graphql.ts` (lines 1, 17-32) + `codegen.ts` (schema =
checked-in `./schema.graphql`, lines 13/17-26) + `package.json` `codegen` script.
**Apply to:** both new query docs — add them, then run `npm run codegen` BEFORE the hooks
import the generated `*Document` symbols (else `tsc -b` fails on missing `src/gql` exports).

---

## No Analog Found

None. Every Phase 4 file maps to a Phase 1–3 analog (the phase is composition of proven
parts — RESEARCH § Don't Hand-Roll "~90% composition"). The genuinely NEW *logic* (it
still reuses analog *shapes*) is concentrated in four spots the planner should verify hardest:

| New logic | Lives in | Why no perfect analog |
|-----------|----------|-----------------------|
| Dual-axis chunk sizing + 413 halve-and-retry | `chunk.ts` (math) + `useLatestPerAuthor` (recursion) | First chunked fan-out; reuses rate.ts pure-module shape + useAuthorWindow fetch boundary, but the halve recursion is new. |
| Left-join merge-by-author (zero-match ⇒ "0 events") | `mergeByAuthor.ts` | No prior left-join; the `deriveWindowMeta` extract-pure pattern is the closest shape. Pin with comment + test (BATCH-03). |
| `authors` enumeration loop (Stop + INVALID_CURSOR restart + live count) | `useAuthorEnumeration.ts` | First multi-page accumulation against a real query; reuses useAuthorWindow cursor discipline + `accumulatePages` intent (paginate.ts), but the Stop/running-count is new. |
| Triage adapter (4 analyzers → 4 indicators) | `triage.ts` | New fan-in; reuses the three analyzer signatures verbatim (tags.ts is the closest single-analyzer shape). |

---

## Metadata

**Analog search scope:** `src/analysis/`, `src/hooks/`, `src/queries/`, `src/router/`,
`src/transport/`, `src/views/`, plus `codegen.ts`, `package.json`, `schema.graphql`.
**Files scanned:** ~20 source files read (full or targeted); schema queries confirmed
(`latestPerAuthor` schema.graphql:12, `authors` :13, `AuthorsPage` :34, `AuthorGroup` :40).
**Pattern extraction date:** 2026-06-25

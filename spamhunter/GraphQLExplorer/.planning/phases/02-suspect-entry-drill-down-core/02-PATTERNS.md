# Phase 2: Suspect Entry + Drill-Down Core - Pattern Map

**Mapped:** 2026-06-24
**Files analyzed:** 14 (11 new, 3 modified)
**Analogs found:** 13 / 14 (1 has no direct analog — pure analyzer)

> Phase 2 is ~80% composition of Phase 1's `src/transport/` boundary plus three small *pure* modules. Almost every new file has a strong Phase-1 analog. The only genuinely new shape is the rate/burst analyzer (pure math, no analog). Treat the transport boundary (`client`, `classify`, `paginate`) as **reuse verbatim** — do not re-derive it.

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/identifier/identifier.ts` | utility (pure) | transform | `src/transport/errors.ts` (discriminated-union return + defensive parse) | role-match |
| `src/identifier/identifier.test.ts` | test | — | `src/hooks/useStatsPoll.test.ts` | exact |
| `src/analysis/rate.ts` | utility (pure) | transform / batch | *(none — pure interarrival math)* | no-analog |
| `src/analysis/rate.test.ts` | test | — | `src/hooks/useStatsPoll.test.ts` | exact |
| `src/analysis/thresholds.ts` | config (constants) | — | `useStatsPoll.ts` `POLL_INTERVAL_MS` export | partial |
| `src/queries/events.graphql.ts` | query | request-response | `src/queries/stats.graphql.ts` | exact |
| `src/hooks/useAuthorWindow.ts` | hook | CRUD / pagination | `src/hooks/useStatsPoll.ts` | role-match |
| `src/router/hashRouter.ts` | hook / router | event-driven | `useStatsPoll.ts` (effect + listener + cleanup) | partial |
| `src/views/AuthorDrillDown.tsx` | view / component | request-response | `src/views/StatsDashboard.tsx` | exact |
| `src/views/AuthorDrillDown.module.css` | config (CSS) | — | `src/views/StatsDashboard.module.css` | exact |
| `src/views/SuspectEntryBar.tsx` | component | event-driven | `StatsDashboard.tsx` `Header` + inline-note sub-components | role-match |
| `src/views/WindowIndicator.tsx` | component (presentational) | — | `StatsDashboard.tsx` `InlineNote` / `CorpusChangedNudge` | role-match |
| `src/views/RatePanel.tsx` | component | — | `StatsDashboard.tsx` `StatCards` + CSS bars (new SVG) | partial |
| `src/App.tsx` | view (entry) | — | `src/App.tsx` (existing — extend readiness gate to mount router) | self (modify) |

*Note: CSS module siblings for `SuspectEntryBar`, `WindowIndicator`, `RatePanel` follow `StatsDashboard.module.css` verbatim — see Shared Pattern "CSS Module + tokens".*

---

## Pattern Assignments

### `src/identifier/identifier.ts` (utility, transform)

**Analog:** `src/transport/errors.ts` (the discriminated-union + defensive-read pattern). The RESEARCH § Pattern 1 code is the literal target body; the *project shape* to match is the `ApiError`-style union and the file-top doc-comment convention.

**Union-return pattern** — mirror `errors.ts:19-26` (`ApiError` union) with a `ParseResult` union. The success arm carries normalized data; the failure arm carries a `reason` discriminant. Views/the entry bar branch on `ok`, never inspect raw input.

```typescript
// errors.ts:19-26 — the shape to copy (named, discriminated, one arm per outcome)
export type ApiError =
  | { kind: 'INVALID_CURSOR' }
  | { kind: 'VALIDATION'; message: string }
  | { kind: 'INTERNAL' }
  ...
```
→ becomes `ParseResult = { ok: true; hex; npub; sourceKind } | { ok: false; reason }` (RESEARCH Pattern 1, lines 171-173).

**Defensive try/catch around the library call** — mirror the defensive `as`-cast + try posture in `errors.ts:36-41` (`httpStatus`). `nip19.decode()` THROWS on malformed input; the `catch` arm IS the parse-failure branch (RESEARCH lines 203-205, verified against `nostr-tools@2.23.8`).

**Doc-comment-first convention** — every Phase-1 module opens with a `//` block citing the contract section + RESEARCH pattern + the security note. Match `errors.ts:1-14` / `config.ts:1-19`. For `identifier.ts` the security note is V5 input-validation + the explicit `nsec` rejection (RESEARCH § Security Domain).

**Imports pattern** (new dep — narrow import):
```typescript
import { nip19 } from 'nostr-tools'   // import ONLY nip19 (tree-shake — RESEARCH A5)
```

**Critical per-type branching** (RESEARCH Pitfall 1, lines 428-432): branch on `decoded.type`; `nprofile` reads `.data.pubkey` (nested), `note` → reject (`NOT_RECOGNIZED`, A1 policy), `nsec` → reject (`REJECTED_NSEC`, security). Do NOT read `decoded.data` uniformly.

---

### `src/analysis/rate.ts` (utility, transform — NO ANALOG)

**Analog:** none. This is pure interarrival math with no Phase-1 precedent. Use RESEARCH § Pattern 3 (lines 289-340) as the literal target. Borrow only the *conventions* below.

**Conventions to inherit from Phase 1:**
- **Result is a named interface** (like `errors.ts` `ApiError`, `useStatsPoll.ts` `Stats`): `RateResult { analyzedCount, rejectedCount, bins[], burstDetected, tightestIntervalSec }`.
- **Bounds-check, never mis-compute** (CONTEXT security; RESEARCH Pitfall 3): `isSaneTs` = `Number.isSafeInteger(t) && 0 <= t <= 4_102_444_800`; count rejects in `rejectedCount`, never silently drop.
- **Asymmetry is structural**: `burstDetected: false` means INCONCLUSIVE, never "clean". No boolean named `clean`/`ok`. (Anti-pattern, RESEARCH line 405.)
- **Pure + unit-tested** like `shouldNudge` — zero React/network imports so it runs in the Node vitest env.

**Constants import:**
```typescript
import { BURST } from './thresholds'
```

---

### `src/analysis/thresholds.ts` (config constants)

**Analog:** `useStatsPoll.ts:28` `export const POLL_INTERVAL_MS = 5000` — the "single named tunable" convention (CONTEXT discretion: "where burst constants live").

```typescript
// useStatsPoll.ts:24-28 — the tunable-constant convention to copy
export const POLL_INTERVAL_MS = 5000
```
→ becomes `export const BURST = { windowSec: 60, minEvents: 5, binSec: 3600 } as const` (RESEARCH lines 345-349). One file, doc-comment noting corpus-validation is deferred to Phase 3 (STATE).

---

### `src/queries/events.graphql.ts` (query, request-response)

**Analog:** `src/queries/stats.graphql.ts` (EXACT — same file shape).

**Full pattern** (`stats.graphql.ts:1-14`):
```typescript
import { graphql } from '../gql'
// doc-comment citing the exact contract section the fields come from
export const StatsDocument = graphql(`
  query Stats {
    stats { eventCount maxLevId dbVersion pinnedStrfryVersion }
  }
`)
```
→ becomes `EventsDocument` (RESEARCH lines 471-481). Select ONLY rendered fields: `events { id pubkey kind createdAt content } endCursor hasMore`. **Omit `raw`/`sig`/`tags`** (contract §9.6, large payload, A4). Variables: `($filter: EventFilterInput, $after: String, $limit: Int)` — names match `schema.graphql:11`.

After adding this document, run `npm run codegen` so `data.events.events[].createdAt` is typed `number`.

---

### `src/hooks/useAuthorWindow.ts` (hook, CRUD / pagination)

**Analog:** `src/hooks/useStatsPoll.ts` (role-match — same hook anatomy: typed state, `useRef` for out-of-band values, `useEffect` lifecycle with `cancelled` guard, the `.catch(() => 'THREW')` discipline, classify-before-data).

**State + return-interface pattern** — mirror `useStatsPoll.ts:30-52` (named `Stats` + `UseStatsPoll` interfaces). Define `WindowEvent`, `WindowMeta`, and the hook's return (`events, windowMeta, error, loading, hasMore, loadMore`). RESEARCH lines 231-237.

**The MANDATORY throw-guard** (`useStatsPoll.ts:103-114`) — copy verbatim; this is a hard Phase-1 invariant (WR-04):
```typescript
const result = await client
  .query(EventsDocument, { filter: { authors: [hex] }, after: cursor, limit: PAGE_LIMIT })
  .toPromise()
  .catch(() => 'THREW' as const)
if (result === 'THREW') { setError({ kind: 'NETWORK' }); return }
```

**classify-before-data** (`useStatsPoll.ts:116-131`) — copy the order exactly: classify first, branch on the union, only then read `result.data`. NEVER read `result.data` before `classify()` (RESEARCH line 407 / errors.ts:5-8).
```typescript
const apiError = classify(result)
if (apiError) { setError(apiError); return }  // ← but add INVALID_CURSOR special-case (below)
const page = result.data?.events
```

**INVALID_CURSOR restart** (Phase-2-specific, documented in `paginate.ts:16-19`): on `apiError.kind === 'INVALID_CURSOR'`, set `after.current = null`, clear events, re-fetch page 1 — never hand-build a cursor. RESEARCH lines 255-260.

**Opaque cursor as a `useRef`** — mirror `useStatsPoll.ts:73` (`lastLevId = useRef`). `after = useRef<string|null>(null)`; pass `page.endCursor` back VERBATIM (`paginate.ts:12-19`).

**Imports pattern** (`useStatsPoll.ts:19-22`):
```typescript
import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../transport/client'
import { classify, type ApiError } from '../transport/errors'
import { EventsDocument } from '../queries/events.graphql'
```

**Effect cleanup + reset on key change** — mirror `useStatsPoll.ts:78-162` (`cancelled` flag, reset on dep change). Here the dep is `hex`: reset cursor + events + loading when the author changes (RESEARCH lines 271-274).

**Load-more race guard** (RESEARCH Pitfall 4): gate `loadMore` on `loading` so a double-click can't append twice — mirrors `useStatsPoll.ts:82-84` `schedule()` non-overlap discipline.

> **Plan decision (RESEARCH Open Q2):** button-driven `loadMore` = single page per click (matches UI-SPEC "Load more"). Optionally a "load to 500 ceiling" action that reuses `accumulatePages` (`paginate.ts:59-72`). Both honor the same opaque-cursor + INVALID_CURSOR rules. Single-step is the recommended primary.

---

### `src/router/hashRouter.ts` (hook / router, event-driven)

**Analog:** `useStatsPoll.ts` (partial — the `useEffect` + `addEventListener` + cleanup-`removeEventListener` shape, e.g. the `visibilitychange` wiring at `useStatsPoll.ts:144-162`).

**Listener-effect pattern to copy** (`useStatsPoll.ts:144-162`):
```typescript
const onVisibility = () => { ... }
document.addEventListener('visibilitychange', onVisibility)
return () => { ...; document.removeEventListener('visibilitychange', onVisibility) }
```
→ becomes a `hashchange` listener (RESEARCH Pattern 5, lines 380-401). Return a discriminated `Route` union (`{name:'home'} | {name:'author';hex} | {name:'notfound'}`) — same union discipline as `ApiError`.

**Security regex** (RESEARCH Pitfall 6): match lowercase 64-hex ONLY (`/^#\/a\/([0-9a-f]{64})$/`); navigation sets the hash only AFTER `parseIdentifier` normalizes. A non-matching hash → `notfound`, never a silent zero-match.

---

### `src/views/AuthorDrillDown.tsx` (view / component, request-response)

**Analog:** `src/views/StatsDashboard.tsx` (EXACT — this is the second view and follows the dashboard's anatomy precisely).

**View anatomy to copy** (`StatsDashboard.tsx`):
- File-top doc-comment citing UI-SPEC + security (escaped plaintext, T-01-07). `StatsDashboard.tsx:1-18`.
- Small named sub-components per region (`Header`, `StatCards`, `InlineNote`, `ErrorShell`). For the drill-down: identity header, timeline list, `WindowIndicator`, `RatePanel`, empty-state.
- **Connecting / error / loaded gating** — copy `StatsDashboard.tsx:178-235` exactly:
```typescript
if (loading && !data && !error) return <ConnectingShell />   // reuse Phase-1 shared shell
if (error && !data) { /* full ErrorShell, tone from errorTreatment(error) */ }
// else render loaded view + inline note for transient errors
```
- **`errorTreatment(error)` switch** — copy `StatsDashboard.tsx:143-176` VERBATIM (every `ApiError` kind → tone + UI-SPEC copy string). The drill-down's strings differ ("reading this author's events" vs "the corpus") but the structure and the NETWORK-shows-`GRAPHQL_URL` rule (`StatsDashboard.tsx:168-174`) are identical.

**Number formatting** (`StatsDashboard.tsx:26-27`):
```typescript
const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)
```

**ID-03 two-state distinction** (CONTEXT / RESEARCH Anti-pattern line 404): parse failure never reaches this view (it stays on the dashboard via the entry bar); a *valid-but-zero-match* renders the calm neutral empty state HERE — modeled on `StatsDashboard.tsx:224-232` (`emptyCaption`, "calm fact, not an error"), WITH the `WindowIndicator` still present.

**Security** — render `content` / `createdAt` / hex / npub as escaped plaintext via JSX interpolation (`StatsDashboard.tsx:86` `pinnedStrfryVersion` is the precedent). Never `dangerouslySetInnerHTML`.

---

### `src/views/SuspectEntryBar.tsx` (component, event-driven)

**Analog:** `StatsDashboard.tsx` `Header` (`StatsDashboard.tsx:30-64`) for the control-row layout, and `InlineNote` (`StatsDashboard.tsx:102-116`) for the parse-error treatment.

**Inline-error pattern to copy** (`InlineNote`, `StatsDashboard.tsx:102-116`) — amber `recoverable` tone, dot + label (color never sole signal):
```typescript
function InlineNote({ tone, children }: { tone: 'recoverable' | 'hardFail'; children: string }) {
  const toneClass = tone === 'recoverable' ? styles.recoverable : styles.hardFail
  return <div className={`${styles.note} ${toneClass}`} role="status" aria-live="polite">
    <span aria-hidden="true" className={styles.stateDot} /><span>{children}</span>
  </div>
}
```
→ the parse-failure state uses this exact shape with the verbatim UI-SPEC string "Not a valid npub / note / nprofile or 64-char hex." On submit: `parseIdentifier(input)` → `ok:false` shows this inline note + STAYS on the dashboard; `ok:true` sets `window.location.hash = '#/a/' + hex`.

**Accent-button discipline** — the "Inspect author" submit is the app's ONE accent action (UI-SPEC Accent reservation #3). Model the button on `StatsDashboard.tsx:58-60` `.refreshButton` markup but apply accent styling (the `.nudgeDismiss` accent treatment in CSS, `StatsDashboard.module.css:146-161`). The input itself stays neutral chrome.

---

### `src/views/WindowIndicator.tsx` (component, presentational — NON-REMOVABLE)

**Analog:** `StatsDashboard.tsx` `InlineNote` / `CorpusChangedNudge` (presentational, takes props, renders a labeled status line).

**Pattern** (RESEARCH Pattern 4, lines 352-375): takes `{ meta: WindowMeta }`, renders the denominator string. **No dismiss prop, no hidden branch** — renders even at `N === 0` (DRILL-05). When `hasMore`, the "partial window" segment is `styles.recoverable` (amber) — reuse the amber semantic class from `StatsDashboard.module.css:286-288`.

**Copy strings VERBATIM from UI-SPEC** (lines 222-224): full / partial / N=0 variants. Use `Intl.NumberFormat` for `N` (matches `StatsDashboard.tsx:26`).

---

### `src/views/RatePanel.tsx` (component — hand-rolled CSS/SVG bars)

**Analog:** `StatsDashboard.tsx` `StatCards` (`StatsDashboard.tsx:81-99`) for the map-over-data render shape; the SVG bars themselves are NEW (no chart lib — UI-SPEC, CONTEXT).

**Map-render pattern** (`StatCards`, `StatsDashboard.tsx:89-98`): map `rateResult.bins` to `<div>`/`<svg>` rects scaled to the max bin. Neutral `--text-muted` fill on a `--border` baseline.

**Asymmetric color rule (load-bearing)** — UI-SPEC §Rate-panel color rule: a detected burst MAY tint amber (`--recoverable`) + the text label "burst"; **NO green, NO success color, NO teal accent** anywhere in this panel. Quiet bars = neutral, never "clean". Pair color with the bar-spike shape + label (color-never-sole-signal, `StatsDashboard.module.css:6-9` discipline).

**Persistent forgeable caveat** (DRILL-01) — always rendered beside the chart, muted body text, non-dismissible. Verbatim UI-SPEC string (line 225). Plus the `WindowIndicator` co-located (DRILL-05).

---

### `src/views/*.module.css` (config — for SuspectEntryBar, WindowIndicator, RatePanel, AuthorDrillDown)

**Analog:** `src/views/StatsDashboard.module.css` (EXACT — copy structure + token usage). See Shared Pattern "CSS Module + tokens".

---

### `src/App.tsx` (MODIFIED — mount router + shell + entry bar)

**Analog:** itself (`src/App.tsx:16-31`) — extend, don't replace. Keep the `waitForReady` gate + `<ConnectingShell/>` (`App.tsx:16-29`). After `ready`, instead of returning `<StatsDashboard/>` directly, render the app shell (header hosting `<SuspectEntryBar/>`) + switch on `useHashRoute()`: `home → StatsDashboard`, `author → AuthorDrillDown(hex)`, `notfound → a neutral not-found within the shell`.

**Readiness-gate pattern to preserve** (`App.tsx:19-29`):
```typescript
useEffect(() => {
  const controller = new AbortController()
  waitForReady(controller.signal).then(() => setReady(true)).catch(() => {})
  return () => controller.abort()
}, [])
if (!ready) return <ConnectingShell />
```

---

## Shared Patterns

### Transport boundary (reuse VERBATIM — do not re-derive)
**Source:** `src/transport/{client,errors,paginate,readiness,config}.ts`
**Apply to:** `useAuthorWindow.ts`, `App.tsx`, every data-fetching path.
- `client` (`client.ts:13-17`) — `preferGetMethod:false` (POST only; GET hits GraphiQL IDE). Route ALL queries through it; never `fetch('/graphql')` ad hoc (RESEARCH Pitfall 5).
- `classify()` (`errors.ts:49-81`) — the 7-kind union; branch on it before `result.data`. `INVALID_CURSOR` already modeled (`errors.ts:20, 63`).
- `accumulatePages()` (`paginate.ts:59-72`) — optional "load to ceiling" reuse; opaque cursor passed verbatim.
- `GRAPHQL_URL` (`config.ts:27`) — single base-URL source; the NETWORK error message echoes it.

### Discriminated-union returns
**Source:** `src/transport/errors.ts:19-26` (`ApiError`)
**Apply to:** `ParseResult` (identifier), `Route` (router), `RateResult`/`WindowMeta` (named result interfaces). One arm per outcome; callers branch on the discriminant, never inspect raw inputs.

### Hook anatomy
**Source:** `src/hooks/useStatsPoll.ts`
**Apply to:** `useAuthorWindow.ts`, `hashRouter.ts`.
- Named state/return interfaces (`:30-52`).
- `useRef` for out-of-band values (cursor, last-seen) (`:73-76`).
- `useEffect` with a `cancelled` guard + cleanup that removes listeners (`:78-162`).
- The `.catch(() => 'THREW' as const)` rejection guard on `.toPromise()` (`:103-114`) — MANDATORY (WR-04).
- Pure decision logic extracted for testing (like `shouldNudge`, `:60-62`).

### View state-gating + errorTreatment
**Source:** `src/views/StatsDashboard.tsx:143-235`
**Apply to:** `AuthorDrillDown.tsx`.
- `errorTreatment(error)` switch over every `ApiError` kind → tone + verbatim UI-SPEC copy (`:143-176`).
- connecting → error-shell → loaded gating order (`:178-205`).
- INTERNAL is generic (no server internals); VALIDATION verbatim; NETWORK echoes `GRAPHQL_URL` (`:162-174`).

### CSS Module + tokens
**Source:** `src/views/StatsDashboard.module.css` + `src/styles/tokens.css`
**Apply to:** every new `*.module.css`.
- Consume `tokens.css` vars only (`--bg/--surface/--accent/--text/--text-muted/--border`, `--connecting/--recoverable/--hard-fail`, 8-pt `--space-*`, `--font-sans/--font-mono`). No new tokens.
- Semantic color classes `.connecting/.recoverable/.hardFail` (`StatsDashboard.module.css:282-292`) reused for state tones.
- `.stateDot` = `currentColor` dot paired with every colored state (`:294-302`) — color never sole signal.
- Accent (`--accent`) ONLY on the suspect-entry submit (Phase-2 reservation #3); never on Load-more, rows, or the rate panel.
- mono (`--font-mono`) for data/IDs (hex, npub, kind, createdAt, N); sans for chrome/prose.

### Escaped-plaintext output encoding (security V5/V14)
**Source:** `StatsDashboard.tsx:86` (`pinnedStrfryVersion` via JSX interpolation)
**Apply to:** all rendered `content` / `createdAt` / id / npub / hex.
- React default escaping only; NEVER `dangerouslySetInnerHTML` (event `content` is attacker-controlled). Content preview = single-line truncated.

### Test convention
**Source:** `src/hooks/useStatsPoll.test.ts`
**Apply to:** `identifier.test.ts`, `rate.test.ts`.
- `import { describe, it, expect } from 'vitest'`; Node env (no DOM/network); test the pure function's behavior. Cover: each nip19 type (npub/note/nprofile/nsec/garbage/hex) per RESEARCH lines 209-215; `isSaneTs` bounds + asymmetric burst per RESEARCH Pattern 3.

---

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `src/analysis/rate.ts` | utility (pure) | transform | No interarrival/time-series analysis exists in Phase 1. Use RESEARCH § Pattern 3 (lines 289-340) as the literal target; inherit only the project *conventions* (named result interface, bounds-check-don't-mis-compute, asymmetric — no "clean" flag, pure + unit-tested). Constants live in the sibling `thresholds.ts`. |

The SVG rate-bar *rendering* inside `RatePanel.tsx` also has no Phase-1 precedent (Phase 1 used no charts) — hand-roll `<div>`/`<svg>` rects per UI-SPEC; reuse the `StatCards` map-render shape for structure.

---

## Metadata

**Analog search scope:** `src/transport/`, `src/hooks/`, `src/queries/`, `src/views/`, `src/router/` (n/a — new), `src/styles/`, `src/App.tsx`, `schema.graphql`, `package.json`.
**Files scanned:** 14 (entire `src/` Phase-1 surface).
**Pattern extraction date:** 2026-06-24

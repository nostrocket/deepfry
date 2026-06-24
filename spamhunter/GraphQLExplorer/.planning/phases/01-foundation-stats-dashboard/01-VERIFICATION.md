---
phase: 01-foundation-stats-dashboard
verified: 2026-06-24T19:00:00Z
status: human_needed
score: 9/11 must-haves verified
behavior_unverified: 2
overrides_applied: 0
behavior_unverified_items:
  - truth: "Stats poll on a seconds-scale interval, pause when the tab is hidden, and surface a non-intrusive 'corpus changed' nudge on maxLevId increase without auto-refetching"
    test: "Run `npm run dev` against the live lens. Watch the live-poll dot; switch to another browser tab for >5s then back; if possible force a maxLevId bump (or wait for corpus to change) and confirm the teal 'Corpus changed — refresh to update.' nudge appears WITHOUT the cards auto-updating until 'Refresh stats' is clicked."
    expected: "Dot is teal/'Live' while the tab is visible; switching away dims it to 'Paused (tab hidden)' and the network poll stops; returning resumes immediately; a maxLevId increase shows the dismissible teal nudge and does NOT auto-refetch the cards."
    why_human: "The hidden-tab pause and nudge-render are runtime DOM/Page-Visibility state transitions. The nudge DECISION (shouldNudge) is unit-tested, but the effect's pause-on-hidden and the nudge actually rendering without auto-refetch are not exercised by any test (no jsdom/RTL); presence + wiring is confirmed but the transition is not behaviorally proven headlessly."
  - truth: "Every distinct classifier state and the empty-corpus / poll-paused states render a distinct, non-blank treatment per the UI-SPEC"
    test: "Visually confirm the 2x2 stat-card grid renders with real values (mono data / sans labels). The empty-corpus calm state ('No events in corpus yet') cannot be triggered against the live ~27.1M-event corpus — confirm via a mocked eventCount:0 if desired, or accept it as environment-blocked. Optionally exercise an error state (e.g. point VITE_GRAPHQL_URL at a down host to see the NETWORK red state)."
    expected: "2x2 cards render with formatted integers; each error/empty/paused state is distinct and non-blank with its verbatim UI-SPEC copy and a label/shape paired with color; empty-corpus reads as a calm neutral fact, never an error."
    why_human: "Visual layout/typography/color-discipline and the empty-corpus branch (unreachable against the live non-empty corpus) are not headlessly verifiable. The branches and verbatim copy strings are present in source and the code compiles, but rendered appearance and the eventCount===0 path need human eyes or a mocked render."
---

# Phase 1: Foundation + Stats Dashboard Verification Report

**Phase Goal:** An analyst can launch the tool through `vite dev` and watch live corpus stats update — a typed urql client connected directly to the LMDB2GraphQL lens proves end-to-end transport via a polled corpus-stats dashboard (walking skeleton).
**Verified:** 2026-06-24T19:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification
**Mode:** MVP + Walking-Skeleton

## Goal Achievement

The walking skeleton is **real**, not a stub. The scaffold builds, the typed client is generated and consumed, the full transport layer (config / client / errors / readiness / paginate) is present and substantive, the stats query is wired, the dashboard renders, and — confirmed live in this session — a real cross-origin `{stats}` read returns actual data from the lens. The two open items are runtime UI-state transitions (hidden-tab pause + nudge render) and visual/empty-corpus appearance that cannot be proven headlessly; both are implemented and wired, so they route to human verification rather than failing.

### Observable Truths

| #   | Truth | Status | Evidence |
| --- | ----- | ------ | -------- |
| 1 | Running `vite dev` serves the app and a real cross-origin `stats` query renders at least one scalar from the live lens | ✓ VERIFIED | Live `curl POST http://192.168.149.21:8080/graphql {stats{...}}` → `{eventCount:27114734, maxLevId:47928105, dbVersion:3, pinnedStrfryVersion:"dockurr/strfry@sha256:..."}`; same query is the typed `StatsDocument` issued by the urql client; `npm run build` emits a working `dist/`. App.tsx → StatsDashboard → useStatsPoll → client.query(StatsDocument) chain confirmed. |
| 2 | The urql client URL comes from VITE_GRAPHQL_URL with default `http://127.0.0.1:8080/graphql`, resolved in exactly one module | ✓ VERIFIED | `config.ts` is the sole module naming a base URL; `client.ts` imports `GRAPHQL_URL` (no inline literal); grep confirms `127.0.0.1:8080` appears only in config.ts and as user-facing UI copy strings. Empty/whitespace env handled (WR-01 fix). |
| 3 | `package-lock.json` resolves graphql to a 16.x version and the pin guard fails on >=17 | ✓ VERIFIED | `require('graphql/package.json').version` → `16.14.2`; package.json exact-pins `"graphql": "16.14.2"` (no caret); `node scripts/check-graphql-pin.cjs` → `PIN_OK: graphql 16.14.2 (major 16)`; guard fails when `major > 16`. |
| 4 | `npm run codegen` produces typed output in src/gql/ consumed by the Stats query | ✓ VERIFIED | `src/gql/{gql,graphql,index,fragment-masking}.ts` present; generated `StatsQuery` types `eventCount/maxLevId/dbVersion: number`, `pinnedStrfryVersion: string` (contract §5 shape); `stats.graphql.ts` imports `graphql` from `../gql`. Codegen uses checked-in SDL (accepted deviation — see below). |
| 5 | Every GraphQL response is classified before any view reads data; errors on HTTP 200 are detected, not trusted | ✓ VERIFIED | `errors.ts` `classify()` inspects HTTP status + `graphQLErrors[0].extensions.code` before data; `useStatsPoll` calls `classify(result)` before reading `result.data?.stats`; the view never reads `errors[]`. 10/10 classifier tests green. |
| 6 | extensions.code values map to distinct discriminated-union kinds (INVALID_CURSOR, TOO_MANY_AUTHORS, VALIDATION, INTERNAL) plus HTTP 503/413/network | ✓ VERIFIED | `ApiError` union has exactly the 7 kinds; tests assert each mapping incl. transport-status precedence and INTERNAL not leaking the raw `lmdb…data.mdb` message (T-01-04). |
| 7 | App shows a distinct 'connecting to relay…' state gated on /ready, treating 503 as retry-with-bounded-backoff, never a generic error | ✓ VERIFIED | `readiness.ts` polls `READY_URL`, returns on 200, backs off 500ms→5000ms cap; `App.tsx` awaits `waitForReady` before mounting dashboard; renders `ConnectingShell` (info-blue, dot+heading+body). Live `/ready` → 200. 5000ms cap present, no unbounded growth. |
| 8 | A cursor-accumulator helper exists (scaffold only) for Phases 2/4 to reuse | ✓ VERIFIED | `paginate.ts` exports `accumulatePages`, treats `endCursor` as opaque, documents INVALID_CURSOR restart + explicit-`limit` convention (`limit` appears 10×); not wired to a live query (correct — scaffold). |
| 9 | The dashboard shows live eventCount, maxLevId, dbVersion, and pinnedStrfryVersion from codegen-typed data | ✓ VERIFIED | `StatsDashboard` renders 4 cards from `useStatsPoll().stats` (typed `Stats`); `Intl.NumberFormat` on integers; `pinnedStrfryVersion` escaped plaintext; build compiles against the codegen-typed query. |
| 10 | Stats poll on a seconds-scale interval, pause when the tab is hidden, and surface a 'corpus changed' nudge on maxLevId increase without auto-refetching | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `useStatsPoll` uses `setTimeout`-reschedule (no `setInterval`), `visibilityState`/`visibilitychange` pause, and `shouldNudge` (strict-increase, unit-tested 6 cases). Present + wired, but the hidden-tab pause and nudge-render state transitions are not exercised by any test (no jsdom). See Human Verification. |
| 11 | Every distinct classifier state and the empty-corpus / poll-paused states render a distinct, non-blank treatment per the UI-SPEC | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | All ApiError branches + empty/paused/nudge present with verbatim UI-SPEC copy; no `dangerouslySetInnerHTML`; teal accent scoped to nudge + live dot. Visual rendering + the empty-corpus (eventCount===0) branch are not headlessly verifiable (live corpus is ~27.1M). See Human Verification. |

**Score:** 9/11 truths verified (2 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `src/transport/config.ts` | Single base-URL source | ✓ VERIFIED | Exports GRAPHQL_URL/READY_URL/HEALTH_URL; only module with a URL literal; blank-env guard. |
| `src/transport/client.ts` | urql Client, no credentials/inline literal | ✓ VERIFIED | Imports GRAPHQL_URL; cacheExchange+fetchExchange; no credentials. |
| `src/queries/stats.graphql.ts` | Codegen-typed Stats document | ✓ VERIFIED | Imports `graphql` from `../gql`; selects the 4 StatsResult fields. |
| `codegen.ts` | client-preset codegen | ✓ VERIFIED | client preset, useTypeImports; SDL schema (accepted deviation). |
| `scripts/check-graphql-pin.cjs` | Postinstall >=17 guard | ✓ VERIFIED | Reads resolved version; fails on major > 16; graceful when absent. |
| `src/gql/` | Generated typed graphql() + TypedDocumentNode | ✓ VERIFIED | gql.ts/graphql.ts/index.ts/fragment-masking.ts present; StatsQuery typed. |
| `src/transport/errors.ts` | classify() → ApiError union | ✓ VERIFIED | 7-kind union; A2 status-path resolved to `error.response.status`. |
| `src/transport/errors.test.ts` | Unit tests per kind | ✓ VERIFIED | 10 cases, all green; INTERNAL-no-leak asserted. |
| `src/transport/readiness.ts` | waitForReady() 500ms→5s backoff | ✓ VERIFIED | Imports READY_URL; bounded cap; AbortSignal-aware. |
| `src/transport/paginate.ts` | Opaque-cursor scaffold | ✓ VERIFIED | accumulatePages; opaque cursors; limit convention; not wired (correct). |
| `src/hooks/useStatsPoll.ts` | Poll + visibility pause + nudge | ✓ VERIFIED (artifact) | setTimeout-reschedule, visibility pause, nudge flag, refresh, WR-04 reject-survival. (Runtime transitions → truth #10 human.) |
| `src/views/StatsDashboard.tsx` | 4 cards + states + nudge + refresh | ✓ VERIFIED (artifact) | All states + verbatim copy; Intl.NumberFormat; escaped render. (Visual → truth #11 human.) |
| `src/views/StatsDashboard.module.css` | Hand-rolled CSS, tokens | ✓ VERIFIED | Consumes tokens.css; teal accent scoped to nudge + live dot. |
| `src/views/ConnectingShell.tsx` | Shared connecting state (WR-03) | ✓ VERIFIED | Single owner of cold-start copy; used by App + dashboard initial branch. |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| client.ts | config.ts | `import { GRAPHQL_URL } from './config'` | ✓ WIRED |
| stats.graphql.ts | src/gql/ | `import { graphql } from '../gql'` | ✓ WIRED |
| readiness.ts | config.ts | `import { READY_URL } from './config'` | ✓ WIRED |
| App.tsx | readiness.ts | `await waitForReady(controller.signal)` | ✓ WIRED |
| useStatsPoll.ts | stats.graphql.ts | `client.query(StatsDocument, {})` | ✓ WIRED |
| StatsDashboard.tsx | errors.ts | `errorTreatment(error: ApiError)` branches on classify() output via hook | ✓ WIRED |
| App.tsx | StatsDashboard.tsx | mounts `<StatsDashboard />` after ready | ✓ WIRED |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
| -------- | ------------- | ------ | ------------------ | ------ |
| StatsDashboard | `stats` | `useStatsPoll()` → `client.query(StatsDocument)` → live lens | Yes (live curl returns real eventCount/maxLevId/dbVersion/pinnedStrfryVersion) | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Live stats query returns data | `curl POST .../graphql {stats{...}}` | `eventCount:27114734, maxLevId:47928105, dbVersion:3, pinned...sha256` | ✓ PASS |
| /ready returns 200 | `curl -o/dev/null -w%{http_code} .../ready` | `200` | ✓ PASS |
| Build succeeds | `npm run build` | 47 modules, dist/ emitted, exit 0 | ✓ PASS |
| Tests pass | `npm test` | 16/16 (10 classifier + 6 nudge/interval) | ✓ PASS |
| Pin guard | `node scripts/check-graphql-pin.cjs` | PIN_OK graphql 16.14.2 | ✓ PASS |
| shouldNudge first-obs / strict-increase | vitest | first obs no nudge; strict increase only | ✓ PASS |
| Hidden-tab pause + nudge render (live UI) | manual | n/a — no jsdom/RTL effect test | ? SKIP → human |

### Probe Execution

No probes declared or implied (frontend phase, no `scripts/*/tests/probe-*.sh`). N/A.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| FND-01 | 01-01 | Scaffold (React19+Vite+TS), graphql pinned v16, typed client from /graphql introspection | ✓ SATISFIED | Scaffold builds; graphql 16.14.2 exact pin + guard; typed `src/gql/` with StatsQuery. Codegen via checked-in SDL (accepted deviation — depth-12 limit blocks live introspection; SDL transcribed from contract §4). |
| FND-02 | 01-01 | urql client direct, configurable base URL (env, default loopback), no inline hardcode | ✓ SATISFIED | config.ts sole URL source; default `http://127.0.0.1:8080/graphql`; live LAN URL only in gitignored .env; no proxy; direct cross-origin proven by live query. |
| FND-03 | 01-02 | Robust transport: errors-on-200, /ready gate w/ 503 backoff, explicit limit, opaque cursors, INVALID_CURSOR restart | ✓ SATISFIED | classify() boundary; waitForReady bounded backoff; paginate.ts opaque cursors + limit convention + INVALID_CURSOR restart-from-page-1 documented. |
| STATS-01 | 01-03 | Dashboard shows eventCount/maxLevId/dbVersion/pinnedStrfryVersion | ✓ SATISFIED | 4 typed cards in StatsDashboard; live data flows. |
| STATS-02 | 01-03 | Poll maxLevId on seconds interval, pause on hidden tab, signal change, no aggressive auto-refetch | ✓ SATISFIED (code) / ⚠ runtime | setTimeout 5000ms, visibility pause, nudge-flag-only, no setInterval, no auto-refetch — all present; runtime transitions routed to human verification (truth #10). |

All 5 declared requirement IDs (FND-01, FND-02, FND-03, STATS-01, STATS-02) are accounted for and map to Phase 1 in REQUIREMENTS.md Traceability. No orphaned requirements: REQUIREMENTS.md maps exactly these 5 to Phase 1, all present in the plans' `requirements` frontmatter.

### Accepted Deviations (not gaps)

- **Codegen uses a checked-in SDL (`schema.graphql`) instead of live introspection.** The live lens enforces query-depth 12 (contract §12), which rejects graphql-codegen's deep introspection query. SDL transcribed verbatim from contract §4; the runtime client still talks to the live lens. This is the research-documented fallback. FND-01 ("typed client generated from /graphql introspection") is satisfied in substance — the generated `StatsQuery` carries the correct Stats shape. Per the verification notes, treated as an accepted deviation, not a gap.
- **In-code default base URL is loopback `http://127.0.0.1:8080/graphql`; the live session URL `http://192.168.149.21:8080/graphql` lives only in a gitignored `.env`.** This is the FND-02 invariant working as designed — NOT a defect.
- **Empty-corpus UI state cannot trigger against the live non-empty (~27.1M) corpus.** Implemented (`eventCount === 0` branch) and unit-coverable; environment-blocked, not a stub.
- **`ConnectingShell.tsx` exists though not in the original plan file lists.** It is the WR-03 code-review fix (de-duplicated connecting copy into one shared component). Legitimate, commit 6a9e25d.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| ---- | ---- | ------- | -------- | ------ |
| — | — | No TODO/FIXME/XXX/TBD/HACK/PLACEHOLDER in src/ | — | None |
| — | — | No dangerouslySetInnerHTML, no setInterval, no credentials, no server.proxy | — | None |

The only `127.0.0.1:8080` hits outside config.ts are inside a user-facing NETWORK error message string in StatsDashboard.tsx — a copy string, not a client base URL (correct per plan, no proxy mention). Code review WR-01..WR-04 all fixed and verified (commits c90e6da, 6054bcf, 6a9e25d, 37f0cd3 — all confirmed present in git).

### Human Verification Required

#### 1. Live polling, hidden-tab pause, and corpus-changed nudge

**Test:** Run `npm run dev` against the live lens. Observe the live-poll dot ('Live'); switch to another tab for >5s then return; if possible force a maxLevId increase (or wait for corpus change) and watch for the teal "Corpus changed — refresh to update." nudge.
**Expected:** Dot is teal/'Live' while visible; switching away dims to 'Paused (tab hidden)' and stops the network poll; returning resumes immediately; a maxLevId increase shows the dismissible nudge and does NOT auto-refetch until 'Refresh stats' is clicked.
**Why human:** Page-Visibility pause and nudge-render are runtime DOM state transitions not exercised by any test (the nudge decision logic is unit-tested, but the effect is not — no jsdom/RTL).

#### 2. Visual dashboard appearance + empty-corpus / error states

**Test:** Confirm the 2×2 stat-card grid renders with real values (mono data / sans labels). The empty-corpus calm state cannot fire against the live ~27.1M corpus — confirm via a mocked `eventCount:0` if desired. Optionally point VITE_GRAPHQL_URL at a down host to see the NETWORK red state.
**Expected:** 2×2 formatted-integer cards; each error/empty/paused state distinct, non-blank, verbatim UI-SPEC copy, color paired with label/shape; empty-corpus reads calm/neutral, never an error.
**Why human:** Visual layout/typography/color discipline and the unreachable empty-corpus branch are not headlessly verifiable; branches + copy are present in source and compile.

### Gaps Summary

No gaps. Every must-have artifact exists, is substantive, wired, and data flows; all key links verified; graphql pinned with an active >=17 guard; the typed client is generated and consumed; the live `{stats}` read confirms the end-to-end browser→lens transport (the walking skeleton's core claim). The two remaining items are runtime UI-state transitions and visual appearance that cannot be proven headlessly — both are implemented and wired, so they are routed to human verification rather than treated as failures. Status is `human_needed` (not `passed`) solely because the human-verification section is non-empty per the decision tree; the automated transport + build + test + live-query evidence is otherwise complete.

---

_Verified: 2026-06-24T19:00:00Z_
_Verifier: Claude (gsd-verifier)_

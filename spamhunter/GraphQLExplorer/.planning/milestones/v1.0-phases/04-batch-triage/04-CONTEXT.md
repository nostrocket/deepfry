# Phase 4: Batch Triage - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — 16 decisions across 4 areas, all recommended answers accepted

<domain>
## Phase Boundary

Scale the single-suspect workflow to many: import a list of pubkeys (paste, file, or by
enumerating the corpus), normalize/dedupe them, fetch a small per-author event window via
chunked `latestPerAuthor` queries that respect the backend's caps, and present a sortable triage
table of at-a-glance per-signal indicators — every row matched strictly by `author` key, every
indicator framed as a first-pass screen over a deliberately tiny window (never exoneration).
Clicking a row drops into the existing Phase 2/3 drill-down.

In scope: BATCH-01, BATCH-02, BATCH-03, BATCH-04. Out of scope: server-side batch analysis,
persistence/saved lists, cross-author duplicate detection, new single-author signals (Phase 3).
</domain>

<decisions>
## Implementation Decisions

### Batch Import & Normalization (BATCH-01)
- **Three import sources:** a paste textarea, a file upload (.txt/.csv), and the "enumerate corpus"
  source (BATCH-04) — all feeding one pipeline.
- **Parsing:** split tokens on whitespace / newlines / commas; run each through the Phase 2
  `parseIdentifier` (reuse — do NOT re-implement nip19). Accept `npub` / `nprofile` / 64-char hex;
  reject `note` and `nsec` (consistent with Phase 2 — note is an event id, nsec is a secret).
- **Dedupe + invalid transparency:** normalize to lowercase hex, dedupe, and COUNT — show
  "N valid · M duplicates removed · K unparseable", with the unparseable entries listed (never
  silently dropped — honesty posture).
- **Size handling:** accept large lists and chunk them; WARN (do not hard-block) when the set is
  very large, so the analyst stays in control.

### Chunked Querying (BATCH-02)
- **Dual-axis chunking:** size each `latestPerAuthor` chunk by `min(≤1000-author cap, 256 KiB body
  budget)` — whichever binds first — computed from a per-author byte estimate (so a fat request
  trips neither `TOO_MANY_AUTHORS` nor `413`).
- **Triage axis:** `kind=1` (text notes — the spam-bearing kind) with `perAuthor=5`, BOTH tunable
  in `analysis/thresholds.ts` (consistent single-home convention from Phases 2–3).
- **Pacing:** issue chunks sequentially (or with small bounded concurrency) with a progress
  indicator — the goal explicitly forbids overloading the backend.
- **Per-chunk error handling:** each chunk goes through the Phase 1 `classify()` boundary; a failed
  chunk keeps partial results + offers retry and NEVER kills the whole batch; the table shows
  "triaged N of M authors" (window-honesty for the batch itself).

### Corpus Enumeration Source (BATCH-04)
- **Enumeration loop:** paginate the `authors` query (opaque cursor passed verbatim, byte-ascending)
  looping until `hasMore` is false, with a visible running count and a **Stop** control.
- **Snapshot honesty:** present the enumerated set as a LIVE SNAPSHOT with its count
  ("N distinct authors as of this fetch") — the window-honesty posture applied to the author set,
  not just to per-author events.
- **Large-set guard:** the running count + Stop control let the analyst bail; warn before triaging
  a very large discovered set (it feeds the chunked pipeline, which is bounded, but the user should
  consent to the scale).
- **Unified pipeline:** discovered pubkeys feed the SAME chunked triage pipeline as paste/upload —
  one code path, not a parallel implementation.

### Triage Table, Honesty & Drill-In (BATCH-03)
- **Row matching:** match strictly by the `author` key from `AuthorGroup` — NEVER zip results by
  index. Authors with zero matching events are shown EXPLICITLY as "0 events" (a zero-match author
  is data, not an omission).
- **Indicators:** transparent per-signal columns reusing the Phase 2/3 pure analyzers per author
  (event count in window, burst flag, near-dup flag, tag fan-out flag) — NOT a single opaque "spam
  score". No `clean`/`ok`/`safe` verdict column (asymmetry carried forward).
- **Honesty framing:** every indicator is computed over a deliberately tiny per-author window
  (`perAuthor=5`) — frame the whole table as a FIRST-PASS SCREEN, never exoneration; copy directs
  the analyst to "drill in for the full picture". This is the central honesty contract of the phase.
- **Sorting + drill-in:** sortable columns (default: event count descending); clicking a row opens
  that author's existing Phase 2/3 drill-down via the `#/a/<hex>` hash route (reuse — no new route).

### Claude's Discretion
- Exact triage-table column set/order and the per-author "indicator" visual treatment (badge vs
  dot vs mini-bar), within the no-"clean", amber-on-signal, accent-reserved rules.
- Precise per-author byte-estimate constant for the 256 KiB chunk math, and the default chunk
  concurrency (1 vs a small bound).
- File-parse details (delimiter sniffing for .csv, max file size constant) and the large-set warning
  threshold number — sane defaults, tunable.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `src/identifier/identifier.ts` — `parseIdentifier` + `isHexPubkey` for batch token normalization
  (reuse verbatim; rejects note/nsec already).
- `src/transport/client.ts` + `classify()` — the POST-only urql client + 7-kind error union
  (`TOO_MANY_AUTHORS`, `PAYLOAD_TOO_LARGE`/413, `INVALID_CURSOR`) already modeled — exactly the
  errors batch chunking must respect. Reuse the imperative `client.query().toPromise().catch(()=>'THREW')`
  + classify-before-data pattern (as in `useAuthorWindow.ts`) for the chunk loop and the `authors`
  enumeration loop.
- `src/analysis/` — `rate.ts` (burst), `nearDup.ts`, `tags.ts`, `kinds.ts`, `isSaneTs` — run per
  author over the small triage window to produce the per-signal indicators (pure, reuse).
- `src/analysis/thresholds.ts` — single tunables home; add `TRIAGE` constants (kind=1, perAuthor=5,
  chunk byte budget, large-set warn threshold).
- `src/views/WindowIndicator.tsx` — the honesty-denominator component; reuse for the
  "triaged N of M" and "N authors snapshot" framing.
- `src/router/hashRouter.ts` + `#/a/<hex>` — drill-in target (reuse; row click navigates).
- The schema: `latestPerAuthor(kind: Int!, perAuthor: Int!, authors: [String!]!): [AuthorGroup!]!`
  and `authors(after, limit): AuthorsPage!` (`{ authors: [String!]!, endCursor, hasMore }`).

### Established Patterns
- Pure analyzers + single thresholds module; explicit `limit`; opaque cursor verbatim; classify()
  before reading data; escaped plaintext; bounds-checked forgeable timestamps.
- Window-honesty denominator on every signal surface; asymmetric "absence ≠ clean".
- Codegen `graphql()` document per query; add `latestPerAuthor` + `authors` documents + run
  `npm run codegen` (a BLOCKING step — types come from codegen).

### Integration Points
- New batch-import view + triage-table view, reachable from the app shell (a new hash route, e.g.
  `#/batch`, alongside the existing dashboard + `#/a/<hex>`).
- Row drill-in reuses the existing `#/a/<hex>` route (no new drill-down code).
- New `latestPerAuthor` + `authors` query documents + codegen.

</code_context>

<specifics>
## Specific Ideas

- The triage table's honesty contract is the headline: indicators over `perAuthor=5` are a SCREEN,
  not a verdict — copy must say so, and there is no "clean" column.
- Match by `author` key is load-bearing (BATCH-03) — a comment + a test should pin that results are
  never index-zipped.
- One chunked pipeline serves paste, file, and corpus-enumeration sources alike.

</specifics>

<deferred>
## Deferred Ideas

- Saved/persisted suspect lists and re-run history — out of scope (read-only client, no persistence).
- Server-side or cross-author batch analysis — out of scope.
- Triaging on multiple kinds simultaneously — v1 triages a single configurable kind (default 1);
  multi-kind triage is a possible future refinement.
- Exporting triage results (CSV download) — not in the BATCH-01..04 scope; note as possible future.

</deferred>

# Roadmap: GraphQL Explorer (Spam Investigation)

## Overview

A read-only, author-centric spam-investigation SPA over the LMDB2GraphQL lens. The journey
starts by standing up a robust typed transport — a direct urql client against the lens's
wildcard CORS — and proving it end-to-end with the simplest real query (the corpus stats
dashboard). It then delivers the
core value as a vertical slice — paste a suspect pubkey, drill into their event timeline with
the window-honesty indicator baked in from the first signal — and broadens that drill-down with
the remaining spam signals (duplicate content, tag/mention fan-out, kind distribution + raw
inspector). Finally it scales the workflow from one suspect to a batch list with chunked,
match-by-author triage. Every phase is an end-to-end, user-visible capability; the pure
analyzer core is built and unit-tested alongside its transport, never depending on it.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Foundation + Stats Dashboard** - Typed urql client connecting directly to the lens (wildcard CORS) with robust transport, proven by a polled corpus-stats dashboard
- [ ] **Phase 2: Suspect Entry + Drill-Down Core** - Paste an npub/hex suspect and judge them from a timeline with burst signal and a non-removable window-honesty indicator
- [ ] **Phase 3: Remaining Spam Signals** - Duplicate-content, tag/mention fan-out, and kind-distribution panels plus a lazy raw-JSON inspector
- [ ] **Phase 4: Batch Triage** - Import a pubkey list/file and triage many authors in one chunked, match-by-author table

## Phase Details

### Phase 1: Foundation + Stats Dashboard
**Goal**: An analyst can launch the tool through `vite dev` and watch live corpus stats update,
proving the typed client and robust transport work end-to-end against the lens's wildcard CORS —
connecting directly, no proxy.
**Mode:** mvp
**Depends on**: Nothing (first phase)
**Requirements**: FND-01, FND-02, FND-03, STATS-01, STATS-02
**Success Criteria** (what must be TRUE):
  1. App runs via `vite dev`; the urql client connects directly to a configurable base URL (env var, default `http://127.0.0.1:8080/graphql`, never hardcoded inline) — the lens's wildcard CORS (`Access-Control-Allow-Origin: *`) means a cross-origin browser query succeeds with no proxy and no CORS error
  2. The stats dashboard shows live `eventCount`, `maxLevId`, `dbVersion`, and `pinnedStrfryVersion`, rendered from codegen-typed data (`package-lock.json` resolves `graphql` to 16.x and codegen produces typed output — never 17)
  3. Stats poll `maxLevId` on a seconds-scale interval, pause when the tab is hidden, and surface a non-intrusive "corpus changed" nudge without aggressively auto-refetching
  4. On cold start the UI shows a distinct "connecting to relay…" state (gated on `/ready`, treating `503` as retry-with-backoff), not a generic error
  5. Every response is checked for `errors[]` on HTTP 200 before reading `data`; `extensions.code` (`INVALID_CURSOR` / `TOO_MANY_AUTHORS` / internal / validation) maps to distinct, non-blank states, and every query passes an explicit `limit`
**Plans**: 3 plans

Plans:
- [ ] 01-01-PLAN.md — Scaffold in-place (React 19 + Vite 7 + TS) with exact-pinned `graphql@16.14.2` + guard, codegen `client-preset`, configurable direct-connection urql client, and a live `stats` read rendered end-to-end (FND-01, FND-02)
- [ ] 01-02-PLAN.md — Transport hardening — single `errors[]`-on-200 `classify()` boundary, `/ready` gating with 503 bounded backoff + connecting state, opaque-cursor accumulator scaffold (FND-03)
- [ ] 01-03-PLAN.md — Stats dashboard view + `useStatsPoll` (seconds-scale, hidden-tab pause, `maxLevId`-diff nudge, no auto-refetch) with the complete distinct-state UI (STATS-01, STATS-02)

### Phase 2: Suspect Entry + Drill-Down Core
**Goal**: An analyst can paste a single suspect (npub or hex), land on that author's drill-down,
and form a defensible first judgment from a newest-first timeline with an asymmetric burst
signal — always reading their conclusion against an honest, non-removable window-size denominator.
**Mode:** mvp
**Depends on**: Phase 1
**Requirements**: ID-01, ID-02, ID-03, DRILL-01, DRILL-05, DRILL-06
**Success Criteria** (what must be TRUE):
  1. User can paste a single pubkey as `npub`/`note`/`nprofile` bech32 or 64-char hex; the app normalizes to lowercase hex (queries in hex), displays both forms, and opens that author's drill-down
  2. The UI visibly distinguishes "couldn't parse the identifier" from "valid identifier, zero matching events" — a typo never reads as a clean author
  3. Drill-down shows the author's event timeline newest-first across kinds with posting-rate / burst indicators interpreted asymmetrically: burst present = suspicious, burst absent ≠ clean, with a persistent "createdAt is author-claimed and forgeable" caveat beside the rate chart
  4. Every signal surface shows a non-removable window-size indicator — "computed over N fetched events · hasMore · time range" — so a partial window is never read as exoneration
  5. User can load more events (cursor pagination with a constant filter, opaque cursor passed verbatim, `INVALID_CURSOR` restarts from page 1) to widen the analysis window, and analytics re-derive live
**Plans**: TBD
**UI hint**: yes

Plans:
- [ ] 02-01: Pure `identifier/` module (nip19 ↔ hex, validation, parse-vs-zero-match distinction) — unit-tested, no UI
- [ ] 02-02: `useAuthorWindow` (paginate, accumulate, windowMeta, loadMore) + drill-down shell + window-size indicator
- [ ] 02-03: Rate/burst analyzer (pure, asymmetric, bounds-checked timestamps) + timeline/burst panel with provenance caveat

### Phase 3: Remaining Spam Signals
**Goal**: An analyst sees the full forensic picture for an author — repeated/near-duplicate
content, mass-mention and hashtag-stuffing patterns, and kind distribution — and can drop into
the canonical bytes of any single event without bloating the list query.
**Mode:** mvp
**Depends on**: Phase 2
**Requirements**: DRILL-02, DRILL-03, DRILL-04
**Success Criteria** (what must be TRUE):
  1. The content view highlights near-duplicate / repeated text via client-side detection (exact normalized-hash bucketing then shingle/Jaccard ≈0.8), with results always framed against the window denominator (e.g. "3 of 50 fetched", never bare "0 duplicates")
  2. Tag/mention aggregation over `p`/`e`/`t` tags surfaces mass-mention fan-out and hashtag stuffing for the fetched window
  3. A kind-distribution breakdown shows the author's event-kind histogram, with out-of-safe-range `kind`/`createdAt` values flagged rather than silently mis-computed
  4. A raw-JSON inspector shows the canonical `raw` bytes for any selected event, fetched lazily on demand (never selected in list queries) and rendered as escaped plaintext — never executed as HTML/markdown
**Plans**: TBD
**UI hint**: yes

Plans:
- [ ] 03-01: Pure `nearDup` + `tags` + `kinds` analyzers (unit-tested against fixtures, zero network)
- [ ] 03-02: Content-dup, tag-fanout, and kind-histogram signal panels + lazy raw-JSON inspector (escaped plaintext)

### Phase 4: Batch Triage
**Goal**: An analyst can paste or upload a list of suspects and triage them together in one
sortable table, then drill into any author — without silently truncating, misattributing, or
overloading the backend.
**Mode:** mvp
**Depends on**: Phase 3
**Requirements**: BATCH-01, BATCH-02, BATCH-03, BATCH-04
**Success Criteria** (what must be TRUE):
  1. User can import a batch of pubkeys by pasting a list or uploading a file; mixed `npub`/hex entries are normalized (reusing the Phase 2 identifier module), deduped, and counted
  2. Batch `latestPerAuthor` queries are chunked to respect both the ≤1000-author cap (`TOO_MANY_AUTHORS`) and the 256 KiB body limit (`413`) — chunking on whichever binds first — using a deliberately small `perAuthor` (3–10) for triage
  3. User sees a triage table of authors with at-a-glance spam indicators; rows are matched by the `author` key (never zipped by index), and authors with zero matching events are shown explicitly as "0 events"
  4. Clicking a triage row opens that author's full Phase 2/3 drill-down for deeper investigation
  5. As an alternative to pasting/uploading, the user can enumerate the corpus's distinct authors via the paginated `authors` query (opaque cursor, byte-ascending, loop until `hasMore` is false) and feed the discovered pubkeys into the same chunked triage pipeline — with the window-honesty posture applied (the enumerated set is a live snapshot, shown with its count)
**Plans**: TBD
**UI hint**: yes

Plans:
- [ ] 04-01: Batch import (paste/file parse + `authors`-query enumeration source, dedupe, ≤1000 + body-size guards) + `useLatestPerAuthor` dual-axis chunking, merge-by-author
- [ ] 04-02: Sortable triage table (transparent per-signal indicators, explicit "0 events" rows) with drill-in links

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation + Stats Dashboard | 0/3 | Not started | - |
| 2. Suspect Entry + Drill-Down Core | 0/3 | Not started | - |
| 3. Remaining Spam Signals | 0/2 | Not started | - |
| 4. Batch Triage | 0/2 | Not started | - |

# Phase 2: Suspect Entry + Drill-Down Core - Context

**Gathered:** 2026-06-24
**Status:** Ready for planning

<domain>
## Phase Boundary

Deliver the core value as a vertical slice: an analyst pastes a single suspect (npub/note/
nprofile bech32 or 64-char hex), lands on that author's drill-down, and forms a defensible
first judgment from a newest-first event timeline with an **asymmetric** burst signal — always
read against an **honest, non-removable window-size denominator**. Introduces the second view
(and the app's first routing), the pure `identifier/` module, the paginating author-window hook,
and the rate/burst analyzer.

**In scope:** identifier parse/normalize/validate (ID-01/02/03), drill-down shell + timeline
(DRILL-01), window-honesty indicator (DRILL-05), cursor pagination / load-more (DRILL-06),
asymmetric burst/rate analyzer.

**Out of scope (later phases):** duplicate-content / tag-fanout / kind-distribution panels +
raw-JSON inspector (Phase 3, DRILL-02/03/04); batch list triage (Phase 4). Builds on Phase 1's
`transport/` (client with `preferGetMethod:false`, `classify()`, readiness, the opaque-cursor
`paginate.ts` accumulator — first exercised here).

</domain>

<decisions>
## Implementation Decisions

### Entry, Routing & Timeline (this phase's grey areas)
- **URL routing (added now).** Introduce lightweight routing with the author's lowercase hex in
  the URL (e.g. `#/a/<hex>`), so drill-downs are bookmarkable/shareable and the browser back
  button works. Phase 1 deferred routing; the second view arrives here. Keep it minimal — a tiny
  hash/path switch, NOT a data-router/loader framework (consistent with the research's "no heavy
  router" guidance).
- **Suspect entry UI = a paste bar in the app shell/header.** The stats dashboard remains the
  home/landing surface; entering a suspect routes to their drill-down. No dedicated
  replace-the-dashboard landing page.
- **Timeline page size = 100 per page** (the `events.limit` contract default); "Load more"
  appends toward the 500 ceiling. Every query passes an explicit `limit` (Phase 1 convention).
- **Burst-detection thresholds are deferred to the research step** — sane defaults validated
  against the live corpus, not locked in discuss (STATE flagged this phase for exactly this
  heuristic/UX research). The window-honesty denominator is shown regardless of threshold choice.

### Locked by success criteria (carried into plan — not relitigated)
- Accept `npub` / `note` / `nprofile` bech32 AND 64-char hex; normalize to **lowercase hex**;
  **query the API in hex**; **display both forms** (human-friendly + hex). Use `nostr-tools`
  (nip19) — install in this phase (was deferred from Phase 1).
- **ID-03 distinction:** the UI visibly separates "couldn't parse the identifier" (malformed →
  inline validation error, e.g. red "not a valid npub/hex") from "valid identifier, zero matching
  events" (neutral, distinct empty state). A typo must NEVER read as a clean author.
- **Timeline:** newest-first across ALL kinds (no kind filter — kind distribution is Phase 3).
  Ordering is fixed by the contract (`createdAt` DESC, `levId` DESC — no `orderBy`).
- **Asymmetric burst interpretation:** burst present = suspicious; burst ABSENT ≠ clean. A
  persistent **"createdAt is author-claimed and forgeable"** caveat sits beside the rate chart.
- **Window-honesty indicator (DRILL-05):** every signal surface shows a **non-removable**
  indicator — "computed over N fetched events · hasMore · time range" — so a partial window is
  never read as exoneration.
- **Pagination (DRILL-06):** cursor pagination with a **constant filter**; opaque cursor passed
  **verbatim**; `INVALID_CURSOR` restarts from page 1 (reuses Phase 1 `transport/paginate.ts`).
  Analytics re-derive live as the window widens.
- **Charts:** lightweight hand-rolled CSS/SVG rate bars (project constraint — no heavy chart lib).
- **Security:** render event content / `createdAt` / identifiers as **escaped plaintext**
  (React default); never `dangerouslySetInnerHTML`. 64-bit `kind`/`createdAt` bounds-aware in
  rate math (flag out-of-safe-range rather than mis-compute).

### Claude's Discretion
- Exact routing mechanism (hash vs History API) and the drill-down component decomposition.
- Rate-chart visual form (sparkline vs bars) within the CSS/SVG constraint.
- Where the burst-default constants live (a single tunable module), pending research.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets (from Phase 1)
- `src/transport/client.ts` — urql Client, **`preferGetMethod:false`** (POST required; GET hits
  the GraphiQL IDE). All Phase 2 queries go through this client.
- `src/transport/classify`/`errors.ts` — the 7-kind `classify()` boundary; branch on it, never
  read `errors[]` in views. `INVALID_CURSOR` is already a discriminated kind.
- `src/transport/paginate.ts` — opaque-cursor accumulator **scaffolded in Phase 1, first
  exercised here** for the events window.
- `src/transport/config.ts` — `GRAPHQL_URL` (required env var). `src/transport/readiness.ts`.
- `src/queries/` — add an `events` query document (codegen-typed) alongside `stats`.
- `src/views/ConnectingShell.tsx`, `useStatsPoll.ts` (pattern reference for hooks).
- Codegen uses the checked-in `schema.graphql` SDL (live introspection is depth-limited).

### Established Patterns
- Single transport boundary; pure logic separated from UI (analyzers unit-tested, no network).
- Hand-rolled CSS with `tokens.css`; dark single theme; mono-for-data / sans-for-chrome.
- `setTimeout`-reschedule + Page Visibility for any polling; explicit `limit` on every query.

### Integration Points
- New `events` query (filter by author, constant filter, opaque cursor) through the existing client.
- New route/view mounted from the app shell; entry bar added to the shell.
- `nostr-tools` (nip19) newly installed for npub/note/nprofile ↔ hex.

</code_context>

<specifics>
## Specific Ideas

- `contract.md` is authoritative: `events(filter, after, limit)` ordering fixed, cursors opaque,
  `INVALID_CURSOR` semantics, `createdAt` author-claimed, 64-bit `Int` for kind/createdAt.
- STATE blocker note: Phase 2 flagged for UX/heuristic research on window-honesty framing +
  asymmetric burst under forgeable `createdAt` (MEDIUM confidence) — resolve in the research step.

</specifics>

<deferred>
## Deferred Ideas

- Duplicate/near-dup content, tag/mention fan-out, kind distribution, raw-JSON inspector — Phase 3.
- Batch list import + multi-author triage table + `authors` enumeration query — Phase 4.
- Exact near-dup / burst threshold tuning beyond Phase 2's rate signal — Phase 3 validates against corpus.

</deferred>

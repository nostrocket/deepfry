# GraphQL Explorer (Spam Investigation)

## What This Is

An author-centric spam-investigation frontend for human analysts, built on top of the
**LMDB2GraphQL** read-only GraphQL lens over a strfry Nostr relay's live LMDB corpus.
An investigator pastes a suspect pubkey or imports a batch list (npub or hex), then drills
into each author across four spam signals — event timeline & posting rate, duplicate
content, tag/mention patterns, and kind distribution — backed by a polled corpus stats
dashboard. It is read-only: it reads the relay through one GraphQL endpoint and renders
investigation views; it never writes events.

## Core Value

**An analyst can take a suspect pubkey and quickly judge whether the author is a spammer**
by seeing their events and spam signals in one place. If everything else fails, this must work.

## Requirements

### Validated

- ✓ Accept a suspect pubkey by paste (npub/nprofile/64-hex) and open its drill-down — v1.0
- ✓ Import a batch list of pubkeys (paste/file/corpus-enumeration; npub or hex) and triage authors together — v1.0
- ✓ Normalize npub/bech32 ↔ hex; query the API in hex, display both forms (single `parseIdentifier` site, rejecting note/nsec) — v1.0
- ✓ Author drill-down: newest-first event timeline with asymmetric posting-rate / burst indicators — v1.0
- ✓ Author drill-down: near-duplicate / repeated-text highlighting (two-stage hash→Jaccard) — v1.0
- ✓ Author drill-down: tag/mention aggregation (p/e/t) surfacing mass-mention & hashtag stuffing — v1.0
- ✓ Author drill-down: kind-distribution breakdown + lazy raw-JSON inspector — v1.0
- ✓ Corpus stats dashboard (eventCount, maxLevId, dbVersion, pinnedStrfryVersion), polled for change — v1.0
- ✓ Robust API access: cursor pagination, limit clamping, `errors[]`-on-200 handling, `/ready` gating — v1.0
- ✓ Non-removable window-honesty denominator on every signal surface (asymmetric: absence ≠ clean) — v1.0 (emergent core principle)
- ✓ Dual-axis batch chunking (≤1000-author cap + 256 KiB body, 413 halve-retry), match-by-author — v1.0
- ✓ Direct connection to the lens via wildcard CORS (no proxy) — v1.0 (superseded the planned dev-proxy; see note)

### Active

(None yet — define with `/gsd-new-milestone` for the next milestone)

### Out of Scope

- Writing/publishing/editing/deleting events — the API is read-only (no Mutation/Subscription)
- Global firehose / "browse all events" feed — analyst explicitly chose author-centric entry only
- Full thread/reply navigation as a primary view — tag analysis covers the spam-relevant subset
- Content search / "find authors by text" — the API exposes no such query; entry is by known pubkey
- Realtime push / live updates — no subscriptions; change detection is via polling `maxLevId`
- Auth / multi-user / per-user state — API is unauthenticated; tool is single local analyst
- Production/public deployment, gateway, TLS, rate limiting — local-dev-first; out of v1
- Re-verifying signatures or aggregating multiple relays — single-relay corpus, strfry already verified

## Context

- **Backend contract:** `contract.md` (v1.0, code-verified 2026-06-23) is the authoritative interface
  spec — endpoints, schema, limits, error codes, data semantics. Treat it as the source of truth.
- **Backend shape:** `POST /graphql` (only data endpoint), `GET /graphql` (GraphiQL), `GET /health`,
  `GET /ready`. Default bind `127.0.0.1:8080`. Returns `503` until startup gates pass.
- **Schema:** `Query { events(filter, after, limit): EventsPage!, latestPerAuthor(kind, perAuthor, authors): [AuthorGroup!]!, stats: StatsResult! }`. Read-only. Introspection enabled (→ typed codegen).
- **Hard limits:** `events.limit` default 100 / ceiling 500; `latestPerAuthor.perAuthor` ceiling 500;
  `authors` max 1000 (else `TOO_MANY_AUTHORS`); request body 256 KiB (else `413`); query depth 12,
  complexity 2000. Cost of `latestPerAuthor` ≈ authors × perAuthor — keep both lean.
- **Data semantics:** ids/pubkeys/sig and e/p tag values are lowercase hex (64/64/128/64).
  `kind`/`createdAt` are 64-bit. Ordering fixed: `createdAt` DESC, `levId` DESC (no `orderBy`).
  `createdAt` is author-claimed (matters for rate analysis). `raw` is canonical; typed fields derived.
  Expired (NIP-40) events filtered server-side.
- **CORS is wildcard-open** (`Access-Control-Allow-Origin: *`, contract v1.2) — a cross-origin browser
  app connects DIRECTLY to the lens with no proxy. (Superseded the original v1 plan of a same-origin Vite
  dev proxy; FND-02 was reworked accordingly.) Base URL via the required `VITE_GRAPHQL_URL` env var.
- **Monorepo placement:** lives at `spamhunter/GraphQLExplorer/` inside the DeepFry monorepo. Scoped
  to this directory only (monorepo project-boundary rule). `.planning/` tracks to the outer deepfry repo.

## Constraints

- **Tech stack**: React 19 + Vite + TypeScript — best-supported for a read-only data-exploration UI; contract provides the exact Vite dev-proxy recipe.
- **GraphQL client**: urql + GraphQL Codegen — introspection is on, so generate a fully typed client from the live schema.
- **Nostr identifiers**: nostr-tools (nip19) for npub/note ↔ hex conversion.
- **Charts/dup-detection**: lightweight CSS/SVG bars (no heavy chart lib); duplicate detection is client-side (no API support).
- **Transport**: must handle `503`/`/ready` gating, opaque cursor pagination, silent limit clamping, and always inspect `errors[]` on HTTP 200 (branch on `INVALID_CURSOR` / `TOO_MANY_AUTHORS`).
- **Deployment**: local-dev-first against `127.0.0.1:8080`; no auth/CORS/gateway infra in v1.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Author-centric, no firehose | Analyst's chosen workflow; entry is always a known pubkey | ✓ Good — shipped, drill-down is the spine of all 4 phases |
| React + Vite + TS + urql + Codegen | Typed client from live introspection | ✓ Good — codegen-typed reads across all phases, build clean |
| Accept npub + hex, query in hex | Humans paste npub from clients; API speaks hex only | ✓ Good — single `parseIdentifier` site; note/nsec rejected |
| Client-side duplicate detection | API has no content-search/dedup; must compute on fetched events | ✓ Good — pure two-stage near-dup analyzer, unit-tested |
| Local-dev-first deployment | Unauthenticated API + analyst-only tool; production hardening deferred | ✓ Good — v1 runs via `vite dev` against the lens |
| Direct connection (wildcard CORS), no proxy | Contract v1.2 confirmed `ACAO: *`; a proxy was unnecessary | ✓ Good — superseded the planned dev proxy (FND-02 reworked) |
| Window-honesty denominator + asymmetric framing | A partial fetch window must never read as exoneration | ✓ Good — became the project's defining principle, on every signal |

## Current State

**Shipped: v1.0 MVP — Spam Investigation Explorer (2026-06-25).** All 4 phases complete and
human-validated; 18/18 requirements satisfied; ~5,800 LOC TypeScript; 128 tests; cross-phase
integration verified WIRED end-to-end. Runs via `vite dev` against the lens (`VITE_GRAPHQL_URL`,
wildcard CORS, no proxy). The four MVP slices: (1) typed transport + stats dashboard, (2) suspect
drill-down with the window-honesty denominator + asymmetric burst, (3) near-dup / tag-fan-out /
kind-distribution panels + lazy raw inspector, (4) chunked batch triage matched by author.

**Known tech debt (non-blocking, see v1.0-MILESTONE-AUDIT.md):** orphaned `accumulatePages`
scaffold export; `BatchIndicator` not literally sharing the `WindowIndicator` component (semantics
correct); near-dup/tag thresholds are documented sane defaults pending live-corpus tuning.

**Deferred to a future milestone:** saved/persisted suspect lists, multi-kind batch triage, CSV
export of triage results, cross-author duplicate detection, server-side analysis.

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-25 after v1.0 milestone*

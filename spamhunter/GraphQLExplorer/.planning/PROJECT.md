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

(None yet — ship to validate)

### Active

- [ ] Accept a suspect pubkey by paste/lookup (npub or 64-char hex) and open its drill-down
- [ ] Import a batch list of pubkeys (paste or file; npub or hex) and review authors together
- [ ] Normalize npub/bech32 ↔ hex; query the API in hex, display human-friendly forms
- [ ] Author drill-down: event timeline (newest-first) with posting-rate / burst indicators
- [ ] Author drill-down: content view with near-duplicate / repeated-text highlighting
- [ ] Author drill-down: tag/mention aggregation (p/e/t) to surface mass-mention & hashtag stuffing
- [ ] Author drill-down: kind distribution breakdown + raw-JSON inspector for any event
- [ ] Corpus stats dashboard (eventCount, maxLevId, dbVersion, pinnedStrfryVersion), polled for change
- [ ] Robust API access: cursor pagination, limit clamping awareness, GraphQL `errors[]` handling, `/ready` gating
- [ ] Local-dev run with a Vite dev proxy to `127.0.0.1:8080` (solves the unconfigured CORS)

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
- **CORS is not configured** — a cross-origin browser app is blocked. v1 uses a same-origin dev proxy.
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
| Author-centric, no firehose | Analyst's chosen workflow; entry is always a known pubkey | — Pending |
| React + Vite + TS + urql + Codegen | Typed client from live introspection; contract's documented CORS fix is a Vite proxy | — Pending |
| Accept npub + hex, query in hex | Humans paste npub from clients; API speaks hex only | — Pending |
| Client-side duplicate detection | API has no content-search/dedup; must compute on fetched events | — Pending |
| Local-dev-first deployment | Unauthenticated API + analyst-only tool; production hardening deferred | — Pending |

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
*Last updated: 2026-06-24 after initialization*

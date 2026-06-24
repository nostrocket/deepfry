# Requirements: GraphQL Explorer (Spam Investigation)

**Defined:** 2026-06-24
**Core Value:** An analyst can take a suspect pubkey and quickly judge whether the author is a spammer.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases (see Traceability).

### Foundation (transport & scaffold)

- [ ] **FND-01**: App is scaffolded (React 19 + Vite + TypeScript) with `graphql` pinned to v16 and a typed client generated from the live `/graphql` introspection (GraphQL Codegen + urql)
- [ ] **FND-02**: A Vite dev proxy serves the UI and `/graphql` (plus `/ready`, `/health`) from the same origin, solving the unconfigured CORS; the client uses a relative `/graphql` URL (never a hardcoded absolute API host)
- [ ] **FND-03**: Transport is robust — `errors[]` is inspected on every HTTP 200, queries are gated on `/ready` with retry/backoff on `503`, every query passes an explicit `limit`, cursors are treated as opaque, and an `INVALID_CURSOR` restarts pagination from page 1

### Identifiers (suspect entry)

- [ ] **ID-01**: User can paste a single pubkey as npub or 64-char hex and open that author's drill-down
- [ ] **ID-02**: UI normalizes npub/note bech32 ↔ hex (nip19), queries the API in lowercase hex, and displays both forms
- [ ] **ID-03**: UI visibly distinguishes "couldn't parse the identifier" from "valid identifier, zero matching events" (malformed hex silently never matches)

### Author drill-down (spam signals)

- [ ] **DRILL-01**: Drill-down shows the author's event timeline newest-first across kinds, with posting-rate / burst indicators interpreted asymmetrically (burst = suspicious; absence of burst ≠ clean, because `createdAt` is author-claimed)
- [ ] **DRILL-02**: Content view highlights near-duplicate / repeated text via client-side detection (exact-hash then shingle/Jaccard ≈0.8)
- [ ] **DRILL-03**: Tag/mention aggregation (p/e/t) surfaces mass-mention and hashtag-stuffing patterns
- [ ] **DRILL-04**: Kind-distribution breakdown plus a raw-JSON inspector for any event (`raw` fetched lazily to avoid large payloads)
- [ ] **DRILL-05**: Every signal shows a window-size honesty indicator — "computed over N fetched events" with `hasMore` awareness — so partial windows are never read as exoneration
- [ ] **DRILL-06**: User can load more events (cursor pagination, constant filter) to widen the analysis window

### Batch triage

- [ ] **BATCH-01**: User can import a batch of pubkeys (paste a list or upload a file; mixed npub/hex accepted and normalized)
- [ ] **BATCH-02**: Batch queries are chunked to respect both the ≤1000-authors cap and the 256 KiB body limit (avoiding `TOO_MANY_AUTHORS` / `413`), using a small `perAuthor` for triage
- [ ] **BATCH-03**: User sees a triage table of authors with at-a-glance spam indicators; results are matched by `author` (not zipped by index), and authors with zero matching events are shown as such

### Stats dashboard

- [ ] **STATS-01**: Dashboard shows corpus stats — `eventCount`, `maxLevId`, `dbVersion`, `pinnedStrfryVersion` (the last for relay-version drift)
- [ ] **STATS-02**: Dashboard polls `maxLevId` on a sensible interval (seconds, pause on hidden tab) and signals when the corpus changed — no aggressive auto-refetch

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Analysis

- **ANL-01**: Cross-author clustering / spam-ring detection (shared content, tag, or mention patterns across many authors)
- **ANL-02**: Configurable heuristic thresholds (Jaccard cutoff, burst posts/min, mass-mention count) via a settings panel, tuned against the real corpus

### Output

- **OUT-01**: Export investigation findings (CSV/JSON) for a single author or a batch
- **OUT-02**: Shareable deep links to an author drill-down view

### Robustness

- **ROB-01**: Full 64-bit precision handling for `kind`/`createdAt` (bigint-safe codegen scalar) instead of the v1 cheap bounds-check

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Writing/publishing/editing/deleting events | API is read-only — no Mutation/Subscription |
| Global firehose / browse-all-events feed | Analyst chose author-centric entry only |
| Full thread/reply navigation as a primary view | Tag analysis covers the spam-relevant subset |
| Content search / find-authors-by-text | API exposes no such query; entry is by known pubkey |
| Realtime push / live updates | No subscriptions; change detection is polling `maxLevId` |
| Auth / multi-user / per-user state | API is unauthenticated; single local analyst |
| Production/public deployment, gateway, TLS, rate limiting | Local-dev-first; deferred |
| Re-verifying signatures / aggregating multiple relays | Single-relay corpus; strfry already verified on ingest |
| ML / automatic spam classification (verdicts) | Transparent human-in-the-loop posture; read-only API can't act on a verdict |

## Traceability

Which phases cover which requirements. Populated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| FND-01 | TBD | Pending |
| FND-02 | TBD | Pending |
| FND-03 | TBD | Pending |
| ID-01 | TBD | Pending |
| ID-02 | TBD | Pending |
| ID-03 | TBD | Pending |
| DRILL-01 | TBD | Pending |
| DRILL-02 | TBD | Pending |
| DRILL-03 | TBD | Pending |
| DRILL-04 | TBD | Pending |
| DRILL-05 | TBD | Pending |
| DRILL-06 | TBD | Pending |
| BATCH-01 | TBD | Pending |
| BATCH-02 | TBD | Pending |
| BATCH-03 | TBD | Pending |
| STATS-01 | TBD | Pending |
| STATS-02 | TBD | Pending |

**Coverage:**
- v1 requirements: 17 total
- Mapped to phases: 0 (filled by roadmap)
- Unmapped: 17 ⚠️ (resolved at roadmap creation)

---
*Requirements defined: 2026-06-24*
*Last updated: 2026-06-24 after initial definition*

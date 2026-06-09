# Roadmap: Web-of-Trust Crawler — v1.1 Write Integrity & Hardening

**Milestone:** v1.1 — Eliminate silent data loss in the Dgraph write path and harden it (chunked writes, resource leak, injection-safe remove path, regression coverage)
**Created:** 2026-06-09
**Granularity:** Coarse
**Coverage:** 7/7 v1.1 requirements mapped
**Numbering:** Continues from v1.0 (last phase was Phase 2) — this milestone starts at Phase 3

## Phases

- [ ] **Phase 3: Write-Path Correctness + Regression Coverage** - Fix the chunked-write data drop so >chunk-size follow-lists persist completely, preserve the version guard's genuine-duplicate dedup, eliminate the per-iteration `defer cancel()` leak, and add the unit + integration tests that prove it
- [ ] **Phase 4: Remove-Path Injection Hardening** - Rewrite `RemoveFollower` to use parameterised `$`-Vars / `%q`-quoted nquads and reject malformed pubkeys before mutating, consistent with the rest of `pkg/dgraph`

## Phase Details

### Phase 3: Write-Path Correctness + Regression Coverage
**Goal**: A pubkey with a follow-list larger than the chunk threshold has its complete follow set persisted to Dgraph in a single crawl, the version guard still short-circuits true duplicates, the chunk loop holds at most one live context at a time, and tests would catch a regression of the data-drop bug.
**Depends on**: Phase 2 (v1.0 — shipped)
**Requirements**: CHUNK-01, CHUNK-02, LEAK-01, TEST-03, TEST-04
**Success Criteria** (what must be TRUE):
  1. After one crawl of a pubkey with more than the chunking threshold (>500) of follows, every follow across all chunks is present in Dgraph — not just the first 200 — so chunks 2…N are no longer discarded by the `kind3createdAt <= existingKind3CreatedAt` guard at `pkg/dgraph/dgraph.go:165`.
  2. Re-crawling an already-fully-ingested pubkey at the same or older `kind3CreatedAt` still short-circuits without redundant follow rewrites — the genuine-duplicate dedup behaviour is preserved (the fix distinguishes "same event, subsequent chunk" from "same event, already complete").
  3. Processing an N-chunk follow-list holds at most one live chunk context/`cancel` at a time — the `defer cancel()` accumulation at `pkg/crawler/chunks.go:39-40` is gone and each chunk's context is released before the next iteration begins.
  4. An integration test (`//go:build integration`, runs under `make test-integration`) asserts that a follow-list larger than the chunk size results in the full follow set persisted; it fails against pre-fix code and passes post-fix.
  5. Unit tests (no live Dgraph, run under `make test` / `-short`) cover the chunk-splitting boundary logic in `processFollowsInChunks` and would catch a regression in chunk count / chunk membership.
**Plans**: TBD

### Phase 4: Remove-Path Injection Hardening
**Goal**: `RemoveFollower` can no longer be injected through `signerPubkey`/`followee` and refuses malformed input, bringing the dead-but-latent remove path in line with the safe query patterns used elsewhere in `pkg/dgraph`.
**Depends on**: Phase 3
**Requirements**: SEC-01, SEC-02
**Success Criteria** (what must be TRUE):
  1. `RemoveFollower` builds its DQL query and mutations using parameterised `$`-Vars and/or `%q`-quoted nquads instead of raw string concatenation of `signerPubkey`/`followee` (`pkg/dgraph/dgraph.go:344-355`), matching the `RemovePubKeyIfNoFollowers` pattern in the same file.
  2. `RemoveFollower` validates that both pubkey arguments are 64-char hex and returns an error for malformed input before issuing any mutation.
  3. A unit test (no live Dgraph) proves a malformed pubkey is rejected with an error and that special characters in input cannot alter the query/mutation structure.
**Plans**: TBD

## Progress Table

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 3. Write-Path Correctness + Regression Coverage | 0/0 | Not started | - |
| 4. Remove-Path Injection Hardening | 0/0 | Not started | - |

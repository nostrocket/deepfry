# Requirements: Web-of-Trust Crawler — v1.1 Write Integrity & Hardening

**Defined:** 2026-06-09
**Core Value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

## v1.1 Requirements

Correctness + hardening pass on the crawler's Dgraph write path. Each maps to roadmap phases.

### Write Integrity

- [x] **CHUNK-01**: A pubkey whose follow-list exceeds the chunk size (chunking triggers above 500 follows; chunks of 200) has its **complete** follow-list persisted to Dgraph — every chunk's follows are written, not just the first. The per-event `kind3CreatedAt` version guard in `AddFollowers` (`pkg/dgraph/dgraph.go:165`) no longer discards chunks 2…N, which all carry the same `createdAt`.
- [x] **CHUNK-02**: The version guard's intended behaviour is preserved for genuine duplicates — a re-crawl of an already-fully-ingested pubkey at the same or older `kind3CreatedAt` still short-circuits without redundant rewrites. The fix distinguishes "same event, subsequent chunk" from "same event, already complete."

### Resource Hygiene

- [x] **LEAK-01**: `processFollowsInChunks` releases each chunk's context/`cancel` before the next iteration begins — no `defer cancel()` accumulation across the loop. Processing an N-chunk follow-list holds at most one live chunk context at a time.

### Injection Hardening

- [ ] **SEC-01**: `RemoveFollower` builds its DQL query and mutations using parameterised variables (`$`-Vars) and/or `%q`-quoted nquads instead of raw string concatenation of `signerPubkey`/`followee` — consistent with the `RemovePubKeyIfNoFollowers` pattern in the same file.
- [ ] **SEC-02**: `RemoveFollower` rejects malformed pubkey inputs (validates 64-char hex) before issuing any mutation, returning an error. Defense-in-depth — the function currently has no callers, so this also guards future use.

### Verification

- [ ] **TEST-03**: An automated regression test reproduces the chunk data-drop on pre-fix code and passes post-fix — asserting that a follow-list larger than the chunk size results in the full follow set persisted. Integration test acceptable (`//go:build integration`, `make test-integration`).
- [ ] **TEST-04**: Unit-testable coverage (no live Dgraph, runs under `make test` / `-short`) exists for the parts that don't require Dgraph: the chunk-splitting boundary logic in `processFollowsInChunks`, and `RemoveFollower`'s input validation/escaping (malformed pubkeys rejected; special characters cannot alter query structure).

## Future Requirements

Deferred to future milestones (tracked in `.planning/codebase/CONCERNS.md`).

### Reliability

- **REL-01**: Address "too many open sockets" / relay-connection scaling.
- **REL-02**: Fix whitelist-plugin issues.
- **REL-03**: Reduce quarantine false-positives (legit events sent to quarantine).
- **REL-04**: Add caching layer.

### Tuning

- **TUNE-01**: Raise `stale_pubkey_threshold` toward the `86400` code default to spend more budget expanding the graph vs re-refreshing known accounts.

### Coverage

- **TEST-05**: Broaden the unit/integration suite beyond the write path (relay state machine, config load, clusterscan) — out of this milestone's hardening scope.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Modifying StrFry | Protocol rule: StrFry stays unmodified — extend only via plugins/external services |
| Editing `~/deepfry/web-of-trust.yaml` for testing | Live config must not change; use a temp `HOME` per spec §6 |
| Storing event payloads in Dgraph | Data-separation rule: Dgraph holds the ID-only graph; payloads live in StrFry LMDB |
| Changing the chunk size / chunking threshold | Tuning, not a correctness fix; current 200/500 values stay unless a fix requires otherwise |
| Open-socket / whitelist / quarantine / cache fixes | Separate concerns; their own future milestones (see Future) |
| `GSD-BUG-plan-phase-false-negative-agent-check.md` | GSD tooling note in repo root, not a crawler defect |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| CHUNK-01 | Phase 3 | Complete |
| CHUNK-02 | Phase 3 | Complete |
| LEAK-01 | Phase 3 | Complete |
| TEST-03 | Phase 3 | Pending |
| TEST-04 | Phase 3 | Pending |
| SEC-01 | Phase 4 | Pending |
| SEC-02 | Phase 4 | Pending |

**Coverage:**

- v1.1 requirements: 7 total
- Mapped to phases: 7 (Phase 3: CHUNK-01, CHUNK-02, LEAK-01, TEST-03, TEST-04 — Phase 4: SEC-01, SEC-02)
- Unmapped: 0

---
*Requirements defined: 2026-06-09*
*Last updated: 2026-06-09 — roadmap traceability populated (Phases 3–4)*

# Phase 3: Write-Path Correctness + Regression Coverage - Context

**Gathered:** 2026-06-09
**Status:** Ready for planning

<domain>
## Phase Boundary

Fix the chunked-write data drop so a follow-list larger than the chunk threshold persists **completely** to Dgraph; preserve genuine-duplicate dedup (CHUNK-02); eliminate the per-iteration `defer cancel()` leak (LEAK-01); and add the unit (TEST-04) + integration (TEST-03) tests that would catch a regression.

Requirements: CHUNK-01, CHUNK-02, LEAK-01, TEST-03, TEST-04.

**In scope additions surfaced during discussion (accepted by user):**
- A shared hex-pubkey validation gate applied at **every** Dgraph pubkey-add site (defense-in-depth, broader than Phase 4's SEC-02 which it will share).

**Out of scope (unchanged):** changing the chunk threshold (500) or chunk size (200); `RemoveFollower`'s parameterised-DQL rewrite (Phase 4, SEC-01); open-socket/whitelist/quarantine/cache concerns; any StrFry change; storing event payloads in Dgraph.
</domain>

<decisions>
## Implementation Decisions

### Fix Architecture (CHUNK-01)
- **D-01:** Move chunking **inside `pkg/dgraph`**. `AddFollowers` receives the **full** follow set in one call; the version guard and the remove-all-existing-follows step run **once** per event. Only the internal followee-resolution query and edge-creation mutations are batched to stay under the ~4MB gRPC message limit. The bug class (chunks 2…N skipped/clobbered) disappears because there is exactly one logical write per event.
- **D-02:** Delete `pkg/crawler/chunks.go` entirely and remove the `>500` branch at `pkg/crawler/crawler.go:535`. The crawler always calls `AddFollowers(ctx, pubkey, createdAt, fullFollows)` regardless of follow-list size — a single write path.
- **D-03 (LEAK-01):** Satisfied by deletion — the leaking `defer cancel()` loop in `chunks.go:39-40` no longer exists. The unified `AddFollowers` uses one transaction + one query context for the whole operation, so no per-batch contexts are created and there is nothing to leak.

### Dedup Preservation (CHUNK-02)
- **D-04:** Satisfied **by design**, no new logic. With one `AddFollowers` call per event, the existing guard (`kind3createdAt <= existingKind3CreatedAt -> return nil` at `dgraph.go:165`) runs once. A re-crawl of an already-fully-ingested pubkey at the same or older `createdAt` short-circuits exactly as today; a strictly-newer event replaces the full set. There is no longer a "subsequent chunk of the same event" case to special-case.

### Write Atomicity
- **D-05:** Single transaction, **all-or-nothing**. The guard check, the remove-all-existing-follows, and all batched followee/edge mutations are staged on one `txn` and committed once. This prevents a mid-write failure from leaving a half-emptied follow-list (a new flavour of the same data-loss bug). Each individual `Mutate`/`Query` message stays small via batching; the transaction spans the whole list.

### Internal Batch Size
- **D-06:** Reuse **200** as the internal batch size for both the followee-resolution query parts **and** the edge mutations. Note for implementer: the current code builds a single `followee_0…followee_N` bulk query (`dgraph.go:227-233`) — that **query string** must also be batched (not just the edge nquads), since at ~10k followees it alone exceeds 4MB.

### Timeout
- **D-07:** **Size-scaled timeout** for the unified write — derive the deadline from follow-count (base budget + per-batch budget) rather than a single fixed `c.timeout`, so large pubkeys get proportionally longer to complete one transaction. Document the chosen formula and where it would be tuned.

### Pubkey Validation Gate (new, accepted)
- **D-08:** Add a **shared exported validator** in `pkg/dgraph` (e.g. `ValidatePubkey` / `isValidHexPubkey`, pattern `^[0-9a-f]{64}$`) and call it at **every** Dgraph pubkey-add site: signer node creation (`dgraph.go:146`), followee stub creation (`dgraph.go:254`), and `MarkAttempted` (`dgraph.go:512`). Dedupe `cmd/healthcheck/main.go:17`'s copy to point at this single source of truth. Phase 4's `RemoveFollower` (SEC-02) MUST reuse this same helper rather than rolling its own.
- **D-09:** **Skip-and-log behaviour** on invalid input, matching the crawler's existing convention: an invalid **signer** → `AddFollowers` returns an error and nothing is written (a follow-list can't be attributed to a bad pubkey); an invalid **followee** → skip that one entry, persist the valid remainder, log the skip. A single bad followee must not abort a large write.

### Regression Test Strategy (TEST-03 / TEST-04)
- **D-10 (TEST-04, unit, no Dgraph):** Extract a **pure helper** `chunkSlice(items []string, size int) [][]string` (or equivalent) with no Dgraph dependency. Unit test asserts chunk **count + membership** at boundaries: 0 → 0 chunks; 200 → 1 chunk; 201 → 2 chunks (200,1); 500; 501; 10000 → 50 chunks with union == input. Runs under `make test` / `-short`. White-box `package dgraph`, plain `if`/`t.Fatalf`, no testify.
- **D-11 (TEST-03, integration):** Use a **real largest kind-3 event harvested from the wild** as the fixture rather than synthetic data.
  - **Capture mechanism:** opt-in only — gated behind an explicit flag/env (off by default) so production crawls stay payload-free and unchanged (respects the no-payloads-outside-StrFry rule). When enabled, the crawler tracks the largest kind-3 event seen and writes it to disk, replacing the stored fixture only when a strictly larger one is encountered.
  - **Fixture location / pointer:** committed under `pkg/dgraph/testdata/largest-kind3-<count>.json`. The integration test globs that pattern and selects the highest `<count>` ("filename as a pointer"). If no fixture exists, `t.Skip` with a message explaining how to harvest one.
  - **Red/green proof:** demonstrate the test fails against pre-fix code by temporarily reverting the fix (e.g. `git stash`), noted in the test comment. Because harvesting needs a live crawl against real relays (manual verification per spec §6), the committed fixture will likely be **absent during this phase's execution**, so TEST-03 will `t.Skip` until harvested — acceptable and consistent with integration gating (`//go:build integration`, `make test-integration`).
  - Test isolation: per-test cleanup/teardown against the local Dgraph; assert full count + membership of the persisted follow set.

### Claude's Discretion
- Exact name/signature of the validation helper and the `chunkSlice` helper.
- Precise size-scaled timeout formula (D-07) and the harvest flag/env name (D-11).
- Internal structure of the batched query/mutation loop within `AddFollowers`.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope
- `.planning/ROADMAP.md` § "Phase 3" — goal + 5 success criteria.
- `.planning/REQUIREMENTS.md` § v1.1 — CHUNK-01, CHUNK-02, LEAK-01, TEST-03, TEST-04 (and SEC-02 context for the shared validator).

### Code under change
- `pkg/dgraph/dgraph.go` — `AddFollowers` (guard at `:165`, signer write `:146`, followee stub write `:254`, bulk followee query `:227`); `MarkAttempted` (`:512`).
- `pkg/crawler/chunks.go` — `processFollowsInChunks` (to be deleted; leak at `:39-40`).
- `pkg/crawler/crawler.go:535,538` — the `>500` chunk branch (to be removed) and the direct `AddFollowers` call.
- `cmd/healthcheck/main.go:17` — existing `validPubkey` regex `^[0-9a-f]{64}$` to be deduped against the new shared helper.
- `pkg/dgraph/dgraph_stale_test.go` — only existing test; the integration-test + white-box conventions to mirror.

### Conventions / constraints
- `.planning/codebase/TESTING.md` — test framework, build-tag gating, white-box placement, `make test` vs `make test-integration`.
- `web-of-trust/CLAUDE.md` and root `CLAUDE.md` — data-separation rule (no payloads outside StrFry), temp-`HOME` for config tests, `Profile` schema compatibility.
- `8pc_crawled.md` §6 — verification plan (manual live run; temp `HOME`). Note: this doc's *fix* sections (A–E) are the shipped v1.0 work, not this phase.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `cmd/healthcheck/main.go:17` `validPubkey` regex (`^[0-9a-f]{64}$`) — promote to the shared `pkg/dgraph` validator (D-08) and point healthcheck at it.
- `AddFollowers`'s existing single-txn structure (`NewTxn` + `CommitNow:false` + final `Commit`) already matches the desired all-or-nothing model (D-05) — the change is to batch the internal query/mutations and run guard+remove-all once.
- `dgraph_stale_test.go` — template for white-box `package dgraph` integration tests (`mustMutate`, `NewClient`, `EnsureSchema`, build tag).

### Established Patterns
- Crawler convention: "invalid pubkeys → log and skip; continue" (`crawler.go:266,507`) — D-09 extends this to the Dgraph layer.
- Upsert/replace semantics: kind-3 is replaceable; `AddFollowers` removes all existing follows then re-adds — must stay atomic (D-05).
- `%q`-quoted nquads already used for pubkey writes (`dgraph.go:146,254`).

### Integration Points
- `cmd/crawler/main.go` loop → `crawler.FetchAndUpdateFollows` → `AddFollowers` (now always full-set).
- `make test-integration` target now exists in the Makefile (`go test -tags=integration ./...`).
- Opt-in fixture-harvest flag wires into the crawler event loop where kind-3 events are received (`crawler.go` ~`:535`).

</code_context>

<specifics>
## Specific Ideas

- "Use a real kind-3 event from the wild" for TEST-03 — the crawler harvests the largest event it sees to disk (opt-in), and the test uses the filename (`largest-kind3-<count>.json`) as a pointer to auto-select the biggest harvested fixture.
- "Every instance of adding any pubkey to Dgraph is gated by validation regardless of where the pubkey is being added. All pubkeys must be valid Nostr pubkeys in hex." → D-08/D-09.

</specifics>

<deferred>
## Deferred Ideas

- `RemoveFollower` parameterised-DQL rewrite (SEC-01) stays in **Phase 4**; only the **shared validator** (D-08) lands now, which Phase 4 will reuse for SEC-02.
- Broader test coverage beyond the write path (relay state machine, config, clusterscan) — TEST-05, future milestone.
- `stale_pubkey_threshold` tuning (TUNE-01) — future.

None of the above expand Phase 3's write-path scope.

</deferred>

---

*Phase: 3-Write-Path Correctness + Regression Coverage*
*Context gathered: 2026-06-09*

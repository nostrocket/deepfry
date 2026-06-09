# Phase 3: Write-Path Correctness + Regression Coverage - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-09
**Phase:** 3-Write-Path Correctness + Regression Coverage
**Areas discussed:** Fix architecture, Crawler-side cleanup, Dedup preservation, Write atomicity, Timeout, Integration-test fixture, Internal batch size, Testability refactor, Pubkey validation

---

## Fix Architecture

| Option | Description | Selected |
|--------|-------------|----------|
| Chunk inside pkg/dgraph | AddFollowers takes full set; guard + remove-all once; batch internal query/edges | ✓ |
| Append-mode flag | Per-chunk calls; chunk 1 replaces, chunks 2…N skip guard + remove-all (append) | |
| You decide | — | |

**User's choice:** Chunk inside pkg/dgraph
**Notes:** Scout surfaced that AddFollowers' "remove all existing follows" means per-chunk calls clobber prior chunks even if the guard were bypassed — so collapsing to one logical write is the clean fix and dissolves the bug class.

---

## Crawler-side cleanup (LEAK-01)

| Option | Description | Selected |
|--------|-------------|----------|
| Delete chunks.go entirely | Crawler always calls AddFollowers(full set); >500 branch removed; leak gone by deletion | ✓ |
| Keep chunks.go, fix leak in place | Preserve two-path structure, delegate to full-set AddFollowers, fix defer cancel() | |
| You decide | — | |

**User's choice:** Delete chunks.go entirely
**Notes:** Single write path; LEAK-01 satisfied because the leaking loop no longer exists.

---

## Dedup preservation (CHUNK-02)

**User's choice:** Satisfied by design (no question asked) — one AddFollowers call per event means the guard runs once; genuine re-crawls at same/older createdAt short-circuit unchanged. Confirmed and recorded as D-04.

---

## Write atomicity

| Option | Description | Selected |
|--------|-------------|----------|
| Single transaction | remove-all + batched mutations on one txn, committed once — all-or-nothing | ✓ |
| Per-batch commits | Each batch commits independently; tolerates partial progress, risks half-emptied list | |
| You decide | — | |

**User's choice:** Single transaction
**Notes:** Prevents a mid-write failure from leaving a follow-list with fewer follows than before.

---

## Timeout

| Option | Description | Selected |
|--------|-------------|----------|
| Single c.timeout for whole op | One context.WithTimeout for the entire call | |
| Size-scaled timeout | Deadline derived from follow-count (base + per-batch budget) | ✓ |
| You decide | — | |

**User's choice:** Size-scaled timeout
**Notes:** Large pubkeys (10k+ follows) need proportionally longer for one transaction.

---

## Integration-test fixture (TEST-03)

| Option | Description | Selected |
|--------|-------------|----------|
| Seed >threshold, assert exact count | Generated ~501–600 follows, assert all present, per-test cleanup | |
| Minimal >batch fixture | Just over one batch (201), fast, full-count assert | |
| You decide | — | |

**User's choice:** Other (free text) — "Use a real kind-3 event from the wild. When running the crawler, persist the largest kind-3 event we encounter and replace it only when we see a bigger one. Persist to disk. The test should use the filename as a pointer so that it automatically uses the biggest event we've found."
**Notes:** Led to two follow-up decisions — capture is opt-in (off by default, respects no-payloads-outside-StrFry); fixture committed under pkg/dgraph/testdata/largest-kind3-<count>.json with t.Skip fallback when absent.

### Capture gate (follow-up)

| Option | Description | Selected |
|--------|-------------|----------|
| Opt-in flag/env, off by default | Capture only when explicitly enabled; production stays payload-free | ✓ |
| Always-on in crawl loop | Always writes largest event to disk; persistent payload side-effect | |
| You decide | — | |

**User's choice:** Opt-in flag/env, off by default

### Fixture location (follow-up)

| Option | Description | Selected |
|--------|-------------|----------|
| Committed testdata + skip fallback | testdata/largest-kind3-<count>.json; glob picks max; t.Skip if absent | ✓ |
| Runtime path, no commit | Lives outside repo; skip if absent | |
| You decide | — | |

**User's choice:** Committed testdata + skip fallback

---

## Internal batch size

| Option | Description | Selected |
|--------|-------------|----------|
| Reuse 200 | Keep the existing 200 constant for internal query parts + edge mutations | ✓ |
| Separate internal constant | Distinct tuned internal batch size | |
| You decide | — | |

**User's choice:** Reuse 200
**Notes:** Threshold (500)/size (200) stay out-of-scope-frozen; both the bulk followee query string and edge nquads must be batched.

---

## Testability refactor (TEST-04)

| Option | Description | Selected |
|--------|-------------|----------|
| Extract pure helper | chunkSlice(items, size); unit-test boundaries 0/1/200/201/500/501/10000 | ✓ |
| Test behaviourally only | Rely on integration test; no fast unit guard | |
| You decide | — | |

**User's choice:** Extract pure helper

---

## Pubkey validation (added by user)

**User's directive (free text):** "Ensure that every instance of adding any pubkey to dgraph is gated by validation regardless of where the pubkey is being added. All pubkeys must be valid nostr pubkeys in hex."

### Invalid-pubkey behaviour (follow-up)

| Option | Description | Selected |
|--------|-------------|----------|
| Skip-and-log per pubkey | Invalid signer → error, no write; invalid followee → skip one, keep rest, log | ✓ |
| Hard error on any invalid | Any invalid pubkey aborts the whole call | |
| You decide | — | |

**User's choice:** Skip-and-log per pubkey

### Helper location (follow-up)

| Option | Description | Selected |
|--------|-------------|----------|
| Shared helper in pkg/dgraph | One validator at every write site; dedupe healthcheck; reused by Phase 4 RemoveFollower | ✓ |
| Validate only at AddFollowers entry | Narrower; doesn't fully satisfy "every instance" | |
| You decide | — | |

**User's choice:** Shared helper in pkg/dgraph
**Notes:** Expands Phase 3 beyond its listed requirements; Phase 4 (SEC-02) will reuse this helper rather than rolling its own.

---

## Claude's Discretion

- Exact names/signatures of the validation helper and `chunkSlice`.
- Size-scaled timeout formula and the harvest flag/env name.
- Internal structure of the batched query/mutation loop in `AddFollowers`.

## Deferred Ideas

- SEC-01 `RemoveFollower` parameterised-DQL rewrite → Phase 4 (reuses the new shared validator).
- TEST-05 broader coverage (relay state machine, config, clusterscan) → future milestone.
- TUNE-01 `stale_pubkey_threshold` tuning → future.

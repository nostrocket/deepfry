# Phase 5: Pubkey Validation Hardening - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-10
**Phase:** 5-Pubkey-Validation-Hardening
**Areas discussed:** Invalid stamp (VALID-03), Purge trigger (VALID-02 — dropped), Test scope

---

## Invalid stamp (VALID-03)

### Q1: How should MarkAttempted handle invalid pubkeys?

| Option | Description | Selected |
|--------|-------------|----------|
| Remove the skip | Drop isValidHexPubkey check; all pubkeys (valid and garbage) flow through ResolvePubkeysToUIDs | |
| Return UIDs from GetStalePubkeys | Change GetStalePubkeys to return PubkeyNode with UIDs; MarkAttempted stamps by UID directly | |
| Keep skip, add separate garbage-stamp path | Split path for valid vs invalid | |
| Recover-or-purge (user-specified) | Try to recover the correct pubkey; if successful write back; if not purge | ✓ |

**User's choice:** Free-text: "Attempt to recover the correct pubkey (e.g. when all uppercase transform to lowercase) and if successful then write this back to Dgraph. If unsuccessful purge this invalid pubkey from Dgraph entirely."

**Notes:** User requested a live query of the DB before deciding. Found 21 garbage nodes across three types: 4 short truncated hex (`f1`, `2a`, `cbdc`, `de`), 9 uppercase 64-char hex (real pubkeys stored wrong-case), 8 binary relay URL blobs (`\x01\x15wss://relay.mostr.pub...` TLV-encoded). All have 0 follows, 0 followers.

---

### Q2: For recoverable uppercase pubkeys — if the lowercase version already exists in Dgraph, what should happen?

| Option | Description | Selected |
|--------|-------------|----------|
| Purge the uppercase node | It's a duplicate stub with 0 edges; delete it | ✓ |
| Merge edges then purge | Move follows/followers edges, then delete | |
| You decide | Leave to implementer | |

**User's choice:** Purge the uppercase node.
**Notes:** All 9 uppercase nodes have 0 follows and 0 followers — no edge-merging needed.

---

### Q3: Where should the recover-or-purge logic live?

| Option | Description | Selected |
|--------|-------------|----------|
| MarkAttempted (inline) | Cleanup happens in the hot path when crawler first encounters a garbage pubkey | ✓ |
| Standalone startup migration | One-time EnsureSchema step at deploy | |
| healthcheck -recover flag | Operator-triggered one-shot cleanup | |

**User's choice:** MarkAttempted (inline).

---

### Q4: After recovery — should the recovered node be re-crawled immediately or held back?

| Option | Description | Selected |
|--------|-------------|----------|
| Re-crawl it (clear last_attempt) | Recovered node re-enters fresh frontier; may have real kind-3 events | ✓ |
| Stamp and hold | Stamp last_attempt=now; node follows normal aging schedule | |
| You decide | Leave to implementer | |

**User's choice:** Re-crawl it — clear/leave last_attempt so the recovered node gets crawled fresh with correct lowercase pubkey.
**Notes:** All 9 uppercase nodes already have last_attempt=NONE; no explicit delete needed. Just update the pubkey field via UID mutation.

---

## Purge trigger (VALID-02)

**User asked:** "Do we still need this given the updated VALID-03?"

**Discussion:** VALID-03's inline recover/purge in MarkAttempted fully covers VALID-02's cleanup goal. All 19 unstamped garbage nodes have last_attempt=NONE and will surface from the stale frontier naturally. They are isolated stubs (0 edges) — no trust-scoring impact while they persist. `healthcheck -purge` remains available manually but is not a Phase 5 deliverable.

**Decision:** VALID-02 dropped entirely.

---

## Test scope

### Q1: What test coverage should Phase 5 deliver?

| Option | Description | Selected |
|--------|-------------|----------|
| Unit test: validator swap (VALID-01) | Test ValidatePubkey / isValidHexPubkey in pkg/dgraph; no Dgraph required | ✓ |
| Integration test: MarkAttempted recover/purge (VALID-03) | Insert garbage nodes, call MarkAttempted, assert recovery or deletion | ✓ |
| Integration test: end-to-end no-garbage write | Synthetic kind-3 event with garbage p-tags; assert nothing invalid reaches Dgraph | ✓ |

**User's choice:** All three.

---

### Q2: For the VALID-01 unit test — what should it actually assert?

| Option | Description | Selected |
|--------|-------------|----------|
| Test ValidatePubkey directly in pkg/dgraph | Unit-test the validator function with all known garbage types; no crawler wiring | ✓ |
| Test updateFollowsFromEvent via a thin helper | Extract p-tag filtering loop, unit-test that helper | |

**User's choice:** Test ValidatePubkey directly in pkg/dgraph.
**Notes:** The crawler fix is covered by the integration tests. ValidatePubkey test covers uppercase, short hex, relay blob, and valid lowercase.

---

## Claude's Discretion

- Exact name/signature of the recover-or-purge helper within MarkAttempted (inline vs. extracted function).
- Whether VALID-03 and VALID-01 integration tests are separate files or combined into `dgraph_validation_test.go`.
- Handling of garbage nodes that ResolvePubkeysToUIDs cannot find (garbage string in batch but not in Dgraph — silently skip).

## Deferred Ideas

- VALID-02 (explicit purge step) — dropped as redundant given VALID-03.
- Broader pkg/crawler unit tests (TEST-05) — future milestone.

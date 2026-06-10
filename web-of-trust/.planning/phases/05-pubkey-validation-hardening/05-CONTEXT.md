# Phase 5: Pubkey Validation Hardening - Context

**Gathered:** 2026-06-10
**Status:** Ready for planning

<domain>
## Phase Boundary

Fix the validator bug so garbage pubkeys never enter Dgraph (VALID-01), and ensure that existing garbage nodes already in the DB are recovered or purged inline the first time the updated crawler encounters them in the stale batch (VALID-03).

Requirements: VALID-01, VALID-03.

**VALID-02 dropped:** Explicit startup migration / healthcheck -purge step is redundant — VALID-03's inline recover/purge handles all 19 existing unstamped garbage nodes the first time they surface from the stale frontier. All 21 garbage nodes are isolated stubs (0 follows, 0 followers); they do not affect trust scoring. `healthcheck -purge` remains available as a manual tool but is not a Phase 5 deliverable.

**Out of scope (unchanged):** FILTER, RELAY, PERF, TIMEOUT, METRIC requirements; RemoveFollower injection hardening (SEC-01/02); any StrFry change; event payload storage.

</domain>

<decisions>
## Implementation Decisions

### VALID-01: Validator fix in crawler
- **D-01:** Replace `nostr.GetPublicKey` with `dgraph.ValidatePubkey` at both call sites in `pkg/crawler/crawler.go`: line 266 (`FetchAndUpdateFollows` — validates pubkeys from the stale batch) and line 507 (`updateFollowsFromEvent` — validates p-tag followees). The shared `dgraph.ValidatePubkey` function in `pkg/dgraph/validate.go` is already the canonical validator (shipped in Phase 3); this is a drop-in swap.
- **D-02:** Skip-and-log behavior is preserved at both call sites — invalid pubkeys are logged and skipped; a single bad followee must not abort the whole write (per Phase 3 D-09).

### VALID-03: MarkAttempted recover-or-purge
- **D-03:** When `MarkAttempted` encounters a pubkey that fails `isValidHexPubkey`, it does NOT simply skip it. Instead it attempts inline recovery:
  1. **Recoverable (64-char uppercase hex):** `strings.ToLower(pubkey)` yields a valid hex pubkey. Resolve the garbage node's UID via `ResolvePubkeysToUIDs` (using the exact garbage string — Dgraph stores it as-is and `eq(pubkey, value)` finds it). Then:
     - If the lowercase version already exists as a separate node: purge the uppercase node (`DeleteNodes([uid])`). The lowercase node handles itself.
     - If not: update the `pubkey` field in-place via UID mutation (`<uid> <pubkey> "lowercase" .`). Leave `last_attempt` unset — all 9 uppercase garbage nodes already have `last_attempt=NONE`, so no explicit delete needed; the recovered node re-enters the fresh frontier and gets crawled with the correct lowercase pubkey.
  2. **Unrecoverable (short hex like `f1`/`2a`, binary relay URL blobs):** Delete the node from Dgraph entirely via `DeleteNodes([uid])`. No `last_attempt` stamp — the node is gone.
- **D-04:** The recover-or-purge logic lives inline in `MarkAttempted` — when the crawler encounters a garbage pubkey in the batch, cleanup happens immediately without a separate migration step.
- **D-05:** `ResolvePubkeysToUIDs` is used to find UIDs for garbage nodes without any changes to that function — it queries by exact value regardless of format, which already handles garbage strings correctly.

### Real garbage nodes in the live DB (context for implementer)
- **10 unrecoverable nodes to purge:** `f1`, `2a` (short hex, length 2); `cbdc`, `de` (short hex, length 4 and 2 — already have `last_attempt` stamps from before Phase 3, will age out naturally but purge catches them too if encountered); 8 × `0115wss://relay.mostr.pub...` binary blobs (114-char hex-encoded TLV, from Mostr ActivityPub bridge relay hints written into p-tags).
- **9 recoverable nodes:** uppercase-hex 64-char pubkeys (`83E818DF...`, `85080D3B...`, `3F770D65...`, `472F440F...`, `A341F45F...`, `C4EABAE1...`, `E88A691E...`, `C49D52A5...`, `16E36665...`). All have `last_attempt=NONE` and 0 edges.

### Test coverage
- **D-06 (unit, no Dgraph):** Add tests to `pkg/dgraph` for `ValidatePubkey` and `isValidHexPubkey` covering all known garbage types from the live DB: uppercase hex (64-char), short hex (`f1`, `cbdc`), relay URL blob (`0115wss://...`), and valid lowercase hex. Runs under `make test` (no build tag).
- **D-07 (integration, `//go:build integration`):** Add integration test in `pkg/dgraph` for `MarkAttempted` recover/purge behavior: insert an uppercase-hex node, a short-hex node, and a relay-blob node; call `MarkAttempted`; assert uppercase node's pubkey was updated to lowercase (and `last_attempt=NONE`), short-hex and relay-blob nodes were deleted. Requires live Dgraph.
- **D-08 (integration, `//go:build integration`):** Add integration test for end-to-end no-garbage write: synthesize a kind-3 event with garbage p-tags (uppercase hex, short hex, relay blob); process via `updateFollowsFromEvent`; assert zero invalid-pubkey nodes exist in Dgraph after the write. Covers VALID-01 at the crawler layer.

### Claude's Discretion
- Exact name/signature of the recover-or-purge helper within `MarkAttempted` (inline vs. extracted function).
- Whether D-07 and D-08 are separate test files or combined into one `dgraph_validation_test.go`.
- Handling of garbage nodes that `ResolvePubkeysToUIDs` cannot find (i.e. the garbage string exists in the batch but not in Dgraph — silently skip, nothing to recover or purge).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope
- `.planning/ROADMAP.md` § "Phase 5" — goal + 3 success criteria (note: success criterion 2 maps to VALID-02 which is dropped; treat criterion 1 and 3 as the active gates).
- `.planning/REQUIREMENTS.md` § VALID-01, VALID-03 — formal requirement text. VALID-02 is dropped; note this in the plan.

### Code under change
- `pkg/crawler/crawler.go:266` — `FetchAndUpdateFollows`: `nostr.GetPublicKey` call to replace (VALID-01).
- `pkg/crawler/crawler.go:507` — `updateFollowsFromEvent`: `nostr.GetPublicKey` call to replace (VALID-01).
- `pkg/dgraph/dgraph.go:569` — `MarkAttempted`: add recover-or-purge logic before the existing UID-stamp path (VALID-03).
- `pkg/dgraph/clusterscan.go:43` — `ResolvePubkeysToUIDs`: used as-is to find UIDs for garbage nodes; no changes needed.
- `pkg/dgraph/validate.go` — `ValidatePubkey` (exported) and `isValidHexPubkey` (unexported): the canonical validators; used by D-01 and D-03.

### Test references
- `pkg/dgraph/dgraph_stale_test.go` — template for white-box `package dgraph` integration tests (`mustMutate`, `NewClient`, `EnsureSchema`, build tag, timestamp-based unique fixture pubkeys).
- `.planning/codebase/TESTING.md` — test framework, build-tag gating, `make test` vs `go test -tags=integration`.

### Conventions / constraints
- `web-of-trust/CLAUDE.md` and root `CLAUDE.md` — data-separation rule, temp-`HOME` for config tests, `Profile` schema compatibility.
- Phase 3 CONTEXT.md (`.planning/phases/03-write-path-correctness-regression-coverage/03-CONTEXT.md`) — D-08/D-09: shared validator design, skip-and-log convention; D-10/D-11: test placement and integration-gating precedents.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/dgraph/validate.go` — `ValidatePubkey(pubkey string) error` (exported) and `isValidHexPubkey(pubkey string) bool` (unexported). Both enforce `^[0-9a-f]{64}$`. Already used at 3 call sites in `dgraph.go` (`:128`, `:265`, `:579`). `cmd/healthcheck/main.go` already uses `dgraph.ValidatePubkey` — confirming the exported form is the right entry point for cross-package use.
- `pkg/dgraph/clusterscan.go:43` — `ResolvePubkeysToUIDs`: queries by `eq(pubkey, [val1, val2, ...])` using `strconv.Quote` for safety. Works for any string value, including garbage pubkeys stored as-is in Dgraph.
- `pkg/dgraph/dgraph.go` — `DeleteNodes(ctx, []string)`: accepts UIDs; used by `healthcheck` for purge. Reuse for unrecoverable nodes in VALID-03.
- `pkg/dgraph/dgraph_stale_test.go` — `mustMutate` and `countFrontier` test helpers; `fmt.Sprintf("%064x", time.Now().UnixNano())` fixture pattern.

### Established Patterns
- Crawler convention: invalid pubkeys → log and skip; continue (`crawler.go:266`, `:507`) — D-02 preserves this at the crawler layer.
- Skip-and-log at Dgraph layer for followee stubs (`dgraph.go:265`) — VALID-03 changes the skip to recover/purge for `MarkAttempted`.
- UID-based nquad mutation pattern already in `MarkAttempted` (`dgraph.go:594-596`): `<uid> <last_attempt> "ts" .` — same pattern for updating `<pubkey>` field.

### Integration Points
- `cmd/crawler/main.go` loop → `FetchAndUpdateFollows` → `MarkAttempted` (called with the same pubkey set after relay queries).
- The 21 garbage nodes confirmed in live DB at offsets 150k–250k; all have 0 follows, 0 followers; 19 have `last_attempt=NONE` (unstamped); 2 (`cbdc`, `de`) already stamped from pre-Phase 3.

</code_context>

<specifics>
## Specific Ideas

- The 9 uppercase-hex nodes are likely real Nostr pubkeys with valid keys — just stored with the wrong case. Recovering them (lowercase update) rather than purging means those accounts' kind-3 events may actually be fetchable once corrected.
- The relay-blob nodes (`0115wss://relay.mostr.pub...`) come from the Mostr ActivityPub bridge — NIP-19 TLV-encoded relay hints that ended up in p-tag position 1 (where the pubkey should be). These are structurally invalid and cannot be recovered.
- `cbdc` and `de` nodes: already stamped (`last_attempt` set). They will age out naturally via the existing aged-frontier logic. If VALID-03's `MarkAttempted` encounters them (short hex, unrecoverable), they will be purged. Either outcome is correct.

</specifics>

<deferred>
## Deferred Ideas

- **VALID-02 (explicit purge step):** Dropped — redundant given VALID-03 inline recover/purge. `healthcheck -purge` remains available as a manual tool if needed.
- **Broader `pkg/crawler` unit tests:** TEST-05 (relay state machine, config, clusterscan coverage) — future milestone.

</deferred>

---

*Phase: 5-Pubkey-Validation-Hardening*
*Context gathered: 2026-06-10*

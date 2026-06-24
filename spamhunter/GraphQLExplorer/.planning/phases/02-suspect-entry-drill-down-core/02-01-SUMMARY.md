---
phase: 02-suspect-entry-drill-down-core
plan: 01
subsystem: identifier
tags: [nostr-tools, nip19, bech32, npub, hex, discriminated-union, vitest, tdd]

# Dependency graph
requires:
  - phase: 01-foundation-stats
    provides: discriminated-union + defensive-parse convention (src/transport/errors.ts ApiError), vitest Node-env test convention (useStatsPoll.test.ts), exact-pin discipline (graphql@16.14.2 + check-graphql-pin.cjs guard)
provides:
  - "parseIdentifier(raw) → ParseResult discriminated union (ok arm: lowercase hex + canonical npub + sourceKind; fail arm: EMPTY/NOT_RECOGNIZED/REJECTED_NSEC)"
  - "isHexPubkey(s) helper + exported HEX64 contract (canonical lowercase 64-hex)"
  - "nostr-tools@2.23.8 exact-pinned dependency (nip19 decode/encode)"
  - "ID-03 honesty contract: parse failure is the ONLY error this module emits; zero-match is decided downstream"
affects: [suspect-entry-bar, hash-router, useAuthorWindow, batch-import-phase-4]

# Tech tracking
tech-stack:
  added: [nostr-tools@2.23.8 (nip19)]
  patterns:
    - "Pure normalizer module: imports only nip19, zero React/urql/transport coupling (Node-vitest runnable)"
    - "Discriminated ParseResult union mirroring ApiError; callers branch on ok, never inspect raw input"
    - "Defensive try/catch around nip19.decode — the throw IS the genuine parse-failure branch"
    - "Per-type nip19 branching: nprofile reads nested .data.pubkey, never decoded.data uniformly"

key-files:
  created:
    - src/identifier/identifier.ts
    - src/identifier/identifier.test.ts
  modified:
    - package.json
    - package-lock.json

key-decisions:
  - "nostr-tools pinned exactly at 2.23.8 (no caret) per repo pin discipline; legitimacy human-verify gate approved (maintainer fiatjaf/nbd-wtf, 5+ yr package, no postinstall hook)"
  - "note (event id) → NOT_RECOGNIZED: an event id is not an author identifier; routing as authors:[id] would read as a clean-author false zero-match"
  - "nsec (secret key) → REJECTED_NSEC distinct arm: secret never normalized to hex/npub, never echoed; fail arm carries no decoded material"
  - "Case-normalize (toLowerCase) before validating so mixed-case pasted hex is accepted"

patterns-established:
  - "Pure analyzer module convention: nip19-only import, discriminated union return, doc-comment-first citing contract + security note"
  - "TDD RED (test + stub) → GREEN (implementation) commit cadence for pure modules"

requirements-completed: [ID-01, ID-02, ID-03]

# Metrics
duration: 12min
completed: 2026-06-24
status: complete
---

# Phase 2 Plan 01: Identifier Normalizer Summary

**Pure `parseIdentifier` module turning pasted npub/nprofile/64-hex into a normalized lowercase-hex + canonical npub via nostr-tools nip19, with explicit nsec/note/garbage rejection — the ID-03 parse-vs-zero-match honesty foundation.**

## Performance

- **Duration:** 12 min
- **Started:** 2026-06-24T14:03:31Z
- **Completed:** 2026-06-24T14:15:55Z
- **Tasks:** 3 (1 human-verify gate approved, 2 implementation)
- **Files modified:** 4

## Accomplishments
- Installed `nostr-tools@2.23.8` exact-pinned (no caret) behind an approved package-legitimacy gate; the existing `check-graphql-pin.cjs` postinstall guard still passes.
- Built `parseIdentifier` as a pure discriminated-union normalizer: hex/npub/nprofile accept arms (normalized lowercase hex + canonical npub + sourceKind); EMPTY/NOT_RECOGNIZED/REJECTED_NSEC fail arms.
- Locked the ID-03 security distinction in unit tests: a typo/garbage yields an explicit `NOT_RECOGNIZED` parse failure (never a silent ok), `nsec` is rejected without ever exposing the secret, and `note` is rejected as a non-author.
- 9 identifier tests pass; full suite 25/25 green; `tsc -b` clean.

## Task Commits

Each task was committed atomically (TDD cadence):

1. **Task 1: Legitimacy gate** — no commit (blocking-human gate; approved `nostr-tools@2.23.8`)
2. **Task 2: install + failing tests (RED)** — `6098a09` (test) — pin nostr-tools@2.23.8, add identifier.test.ts + stub, suite runs RED
3. **Task 3: implement parseIdentifier (GREEN)** — `20d6822` (feat) — per-type branching, nsec/note reject, suite GREEN

_REFACTOR phase: none needed — GREEN implementation was already clean._

## Files Created/Modified
- `src/identifier/identifier.ts` — `parseIdentifier(raw) → ParseResult`, `isHexPubkey` helper, `HEX64` matcher, `ParseResult` type; pure nip19-only module
- `src/identifier/identifier.test.ts` — vitest coverage of every nip19 branch + EMPTY/REJECTED_NSEC/NOT_RECOGNIZED + case-normalization + round-trip
- `package.json` — added `nostr-tools: 2.23.8` (exact, no caret)
- `package-lock.json` — locked nostr-tools + transitive deps

## Decisions Made
- **Exact pin 2.23.8** — followed the repo's graphql-pin discipline; legitimacy verified independently against the live registry (maintainer fiatjaf/nbd-wtf, package created 2021-01-04, no postinstall hook; only a publish-time `prepublish` script).
- **note → NOT_RECOGNIZED** — an event id is not an author; accepting it would produce a false clean-author zero-match (the ID-03 failure mode).
- **nsec → REJECTED_NSEC (distinct arm)** — a secret key must never be normalized, routed into URL/history, or stored; the fail arm carries no decoded material (asserted in tests).
- **Case-normalize before validate** — mixed-case pasted hex is accepted and normalized to lowercase.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None. nip19 return shapes were probed against the installed library before writing the test fixtures, confirming npub `.data` (string), nprofile `.data.pubkey` (nested), and throw-on-garbage behavior matched the plan/RESEARCH.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The identifier contract is locked and unit-proven; plan 02-02 (suspect entry bar) and the hash router can reuse `parseIdentifier`/`isHexPubkey` as the single normalizer without re-deriving bech32 handling.
- `EventsDocument` / `useAuthorWindow` (plans 02-02/02-03) will consume the normalized `hex` from the ok-arm for `authors:[hex]` queries; the parse-vs-zero-match boundary is established (parse failure stays on the dashboard; zero-match renders in the drill-down).

## Self-Check: PASSED

- FOUND: src/identifier/identifier.ts
- FOUND: src/identifier/identifier.test.ts
- FOUND commit: 6098a09 (test RED)
- FOUND commit: 20d6822 (feat GREEN)

---
*Phase: 02-suspect-entry-drill-down-core*
*Completed: 2026-06-24*

---
phase: 04-batch-triage
reviewed: 2026-06-25T00:00:00Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - src/analysis/chunk.ts
  - src/analysis/mergeByAuthor.ts
  - src/analysis/triage.ts
  - src/analysis/batchImport.ts
  - src/hooks/useLatestPerAuthor.ts
  - src/hooks/useAuthorEnumeration.ts
  - src/queries/latestPerAuthor.graphql.ts
  - src/queries/authors.graphql.ts
  - src/router/hashRouter.ts
  - src/views/BatchImport.tsx
  - src/views/TriageTable.tsx
  - src/App.tsx
findings:
  critical: 1
  warning: 6
  info: 4
  total: 11
status: issues_found
---

# Phase 4: Code Review Report

**Reviewed:** 2026-06-25T00:00:00Z
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

Phase 4 (Batch Triage) was reviewed at standard depth with heavy adversarial weight on the
five load-bearing invariants named in the prompt (left-join by author, dual-axis chunking +
413 degrade, paginated enumeration with bounded restart, defensive token parsing, escaped /
amber-only render).

The pure analysis modules (`mergeByAuthor`, `triage`, `chunk`, `batchImport`) are correct in
isolation and well-tested. The BATCH-03 left-join invariant is genuinely sound and cannot
misattribute. The transport-discipline reuse in both hooks (throw-guard, classify-before-data,
runId stale-drop) is faithful to the Phase-2 pattern.

The most serious problem is structural and security-relevant: **the tested BATCH-01 tokenizer
(`src/analysis/batchImport.ts`) is never imported anywhere — the production view ships its own
re-implemented copy.** The module that all the BATCH-01 honesty/validation tests exercise is
dead code; the code that actually runs is untested. This directly violates the module's own
documented "a second decode site is forbidden" invariant.

Secondary concerns cluster around the 413 halve-and-retry path (retry index bookkeeping is not
updated after a split, and a successful split that is later half-failed loses partial work),
an unbounded enumeration loop with no page ceiling, and a `retryChunk` race that resets global
`loading` regardless of run identity.

## Critical Issues

### CR-01: BATCH-01 tokenizer is duplicated — the tested module is dead code; the shipped one is untested

**File:** `src/views/BatchImport.tsx:44-66` and `src/analysis/batchImport.ts:28-47`

**Issue:** There are two `parseBatchInput` implementations. `src/analysis/batchImport.ts`
is the BATCH-01 module that every test in `batchImport.test.ts` imports and asserts against —
but a repo-wide grep shows it is imported by **nothing** in production (`grep -rn
"analysis/batchImport" src` returns zero hits). The view at `src/views/BatchImport.tsx`
defines its own private `parseBatchInput` (lines 44-66) and that is the only one that runs.

Consequences:
1. The shipped tokenizer has **zero test coverage**. The nsec-rejection test, the
   note-rejection test, the dedupe-count test, and the "never silently dropped" honesty
   tests all exercise a function that users never reach.
2. The module header of `batchImport.ts` states "the single sanctioned bech32-decode
   site; a second decode site is forbidden." The view's copy is exactly that forbidden
   second decode/tokenize site. The two can silently diverge — a future fix to one (e.g.
   tightening the split regex, or handling a new `parseIdentifier` reason) will not
   propagate to the other, and the tests will keep passing while production drifts.
3. The two return different shapes (`BatchImportResult` with `validHexes` vs `ParsedTokens`
   with `valid`), confirming they were written independently rather than refactored.

This is classified Critical because it defeats the input-validation test suite that is the
phase's stated security control (defensive token parsing, reject note/nsec). A security
control whose tests do not cover the deployed code is effectively unverified.

**Fix:** Delete the private copy in the view and consume the tested module. Adapt the field
names (or have the module return the shape the view needs):

```tsx
// src/views/BatchImport.tsx
import { parseBatchInput } from '../analysis/batchImport'
// remove the local function parseBatchInput (lines 44-66) and the ParsedTokens interface;
// map BatchImportResult.validHexes / duplicateCount / unparseable at the call sites.
const pasteParsed = useMemo(() => parseBatchInput(pasteText), [pasteText])
// pasteParsed.validHexes, pasteParsed.duplicateCount, pasteParsed.unparseable
```

If the view genuinely needs a different shape, change `batchImport.ts` to return it and keep
the single source of truth — do not maintain two tokenizers.

## Warnings

### WR-01: 413 halve-and-retry leaves `chunkAuthorsByIndex` holding the oversized chunk — Retry re-issues the body that already failed

**File:** `src/hooks/useLatestPerAuthor.ts:101-108, 160-182`

**Issue:** When a chunk 413s, `fetchChunk` splits it in half recursively and returns the
merged groups, but `chunkAuthorsByIndex.current[index]` is never updated to reflect the
split — it still stores the full oversized chunk. This is only self-correcting because the
413 recursion re-runs on retry. But if the *first* sub-half succeeds and the *second* half
fails for an unrelated reason (NETWORK), the whole chunk is reported as errored (line 104/106
short-circuit returns the error), the already-fetched left-half groups are discarded
(never pushed to `accumulated`), and Retry re-issues the entire oversized chunk from
scratch — re-413ing, re-splitting, and re-fetching the half that already succeeded. Partial
work inside a chunk is lost on any mid-split failure, contradicting the "a failed chunk keeps
partial results, never silently truncates" invariant at sub-chunk granularity.

**Fix:** Accumulate successful sub-chunk groups before propagating a sibling failure, or
flatten the 413 degrade into the top-level sequential loop so each post-split sub-chunk is an
independent retryable unit with its own index entry:

```ts
// On a sub-half failure, still surface the left half's groups instead of discarding:
const left = await fetchChunk(chunk.slice(0, mid), myRun)
if (!Array.isArray(left)) return left
const right = await fetchChunk(chunk.slice(mid), myRun)
if (!Array.isArray(right)) {
  // push left's authors as covered before returning the error, OR
  // record sub-chunk errors separately so left is not lost.
}
```

### WR-02: `retryChunk` unconditionally resets global `loading`, racing the main run loop

**File:** `src/hooks/useLatestPerAuthor.ts:166, 178`

**Issue:** `retryChunk` calls `setLoading(true)` then `setLoading(false)` (line 178)
unconditionally, without checking `myRun === runId.current`. If the user starts a new batch
(`run` bumps `runId` and sets `loading` based on the new input) while an in-flight retry from
the previous run resolves, the stale retry's `setLoading(false)` will clear the new run's
loading state, causing the spinner to disappear while the new batch is still fetching its
first chunk. Also, `retryChunk` sets `loading` true even though the main loop may still be
running other chunks, and on completion sets it false even if the main loop is still in
flight — the two share one boolean with no coordination.

**Fix:** Gate the final state writes on run identity, consistent with the rest of the hook:

```ts
const outcome = await fetchChunk(chunk, myRun)
if (myRun !== runId.current) return
// ... accumulate ...
if (myRun === runId.current) setLoading(false)
```

Better: derive `loading` from outstanding work (pending chunk count) rather than imperatively
toggling it from two independent code paths.

### WR-03: Enumeration loop has no page ceiling — an empty page with `hasMore: true` spins until Stop

**File:** `src/hooks/useAuthorEnumeration.ts:96-137`

**Issue:** The loop exits only on `!after || !outcome.hasMore`, an error, or a Stop. If the
backend returns `hasMore: true` with a non-null `endCursor` but an empty/repeating `authors`
page (a backend bug, a stuck cursor, or a pathological keyspace), `seen` never grows, the
loop never terminates, and the only escape is the user clicking Stop. There is no maximum
page count or no-progress detector. The prompt explicitly calls for "no infinite loop." Stop
mitigates the UI freeze risk but does not bound the loop on its own.

**Fix:** Add a no-progress / max-pages guard:

```ts
let pages = 0
const sizeBefore = seen.size
// after a page:
pages += 1
if (seen.size === sizeBefore && pages > 1) { /* no new authors twice running → stop or error */ }
if (pages > MAX_ENUM_PAGES) { setError({ kind: 'NETWORK' }); setEnumerating(false); return }
```

### WR-04: `TOO_MANY_AUTHORS` chunk error is treated as "Retry" — retrying re-sends the same oversized author list

**File:** `src/views/TriageTable.tsx:132-133`, `src/hooks/useLatestPerAuthor.ts:148-152`

**Issue:** `chunkErrorTreatment` maps `TOO_MANY_AUTHORS` to the recoverable "Couldn't load
this chunk — Retry." copy, and `retryChunk` re-issues the *identical* chunk authors. Unlike
413 (which halve-retries inside `fetchChunk`), `TOO_MANY_AUTHORS` has no
shrink-on-retry path — the static `chunkSize()` is 500 and the ≤1000 cap should prevent it,
but if the backend's cap is lower than assumed, every Retry deterministically re-fails with
the same error. The prompt names TOO_MANY_AUTHORS as a chunking trigger; the chunk loop
never actually responds to it by shrinking. Retry here is a no-op loop for the user.

**Fix:** Treat `TOO_MANY_AUTHORS` like 413 — halve the chunk and re-issue inside `fetchChunk`
rather than surfacing a Retry that cannot succeed:

```ts
if ((apiError.kind === 'PAYLOAD_TOO_LARGE' || apiError.kind === 'TOO_MANY_AUTHORS')
    && chunk.length > 1) {
  // halve-and-retry
}
```

### WR-05: 413 recursion at `chunk.length === 1` returns the raw `PAYLOAD_TOO_LARGE` — UI shows a recoverable Retry that can never shrink further

**File:** `src/hooks/useLatestPerAuthor.ts:101-109`, `src/views/TriageTable.tsx:125-126`

**Issue:** When a single author's request still 413s, the recursion bottoms out
(`chunk.length > 1` is false) and returns `{ kind: 'PAYLOAD_TOO_LARGE' }`. The UI maps that
to the recoverable "That chunk was too large — shrinking and retrying." message with a Retry
button. But there is nothing left to shrink — a one-author chunk cannot be halved. Retry will
deterministically 413 again forever. The copy ("shrinking and retrying") is also misleading:
no automatic shrink or retry happens at this point; it is now a hard failure presented as
recoverable.

**Fix:** Distinguish the unsplittable-413 terminal case from the recoverable mid-size 413,
and present it as a hard per-chunk failure (no Retry, or a distinct message), e.g. return a
sentinel the UI maps to `hardFail`.

### WR-06: `BODY_LIMIT_BYTES` constant is exported and documented as the 413 trigger but never enforced at runtime

**File:** `src/analysis/chunk.ts:25, 32-34`, `src/hooks/useLatestPerAuthor.ts`

**Issue:** `byteBudgetAuthors()` derives a per-chunk author cap from `BODY_LIMIT_BYTES`, and
`chunkSize()` takes its `Math.min`. But because the author cap (500) always binds first
(per the module's own analysis), the byte axis is effectively inert — and crucially, nothing
measures the *actual* serialized body size before sending. If `perAuthor`, the selected
fields, or hex length ever change such that 500 authors exceed 256 KiB, the static math will
not catch it; only the runtime 413 will (and per WR-04/WR-05 the runtime degrade has gaps).
The "defensive lower bound" is documented as load-bearing but does not actually bound the
runtime payload. This is a latent correctness gap rather than a present bug.

**Fix:** Either measure the JSON body length before issuing (and pre-split when over budget),
or add a test that pins the real serialized size of a `chunkSize()`-author request below
`BODY_LIMIT_BYTES` so a future field/perAuthor change fails the test rather than production.

## Info

### IN-01: `ConnectingShell` import in TriageTable couples a pure-ish results view to a transport-state component

**File:** `src/views/TriageTable.tsx:29, 202-204`

**Issue:** TriageTable returns `<ConnectingShell />` when `loading && rows.length === 0`. This
is reasonable, but note that `loading` can be stale-cleared by WR-02; once that is fixed this
branch is fine. Minor: consider an empty-but-loading state that still shows the persistent
framing line so the "first-pass screen" caveat is visible even during the first fetch.

**Fix:** Optional — render the framing line above the ConnectingShell.

### IN-02: `error` state in `useLatestPerAuthor` is declared but never used (write-only `[error]` tuple)

**File:** `src/hooks/useLatestPerAuthor.ts:65, 50-51`

**Issue:** `const [error] = useState<ApiError | null>(null)` is never set and is documented as
"currently unused." It is exported in the return object. Dead state surface — either wire it
to a genuine fatal-run condition or drop it to avoid implying a behavior that does not exist.

**Fix:** Remove `error` from the hook's state and public interface until a fatal-run path
exists, or document the consumer that depends on it.

### IN-03: Enumeration error copy claims "restarting from the top" but the loop has already given up

**File:** `src/views/BatchImport.tsx:253-255`

**Issue:** When `enumeration.error.kind === 'INVALID_CURSOR'` the view shows "Enumeration
cursor expired — restarting from the top." But by the time `error` is set in the hook, the
bounded restart budget is exhausted (a second consecutive INVALID_CURSOR), so nothing is
actually restarting — the loop has terminated. The copy misleads the analyst into thinking
recovery is in progress.

**Fix:** Use copy that matches the terminal state, e.g. "Enumeration cursor expired — press
Enumerate to restart," and surface an explicit restart affordance.

### IN-04: `mergeHexSets` and the dedupe logic in the view duplicate the Set-based dedupe already in `batchImport.ts`

**File:** `src/views/BatchImport.tsx:69-79`

**Issue:** Once CR-01 is resolved by consuming the tested module, `mergeHexSets` is the only
remaining bespoke dedupe; it is fine but overlaps the module's responsibility. Minor
consolidation opportunity to keep all dedupe in one tested place.

**Fix:** Optional — move set-union dedupe into the analysis layer alongside `parseBatchInput`.

---

_Reviewed: 2026-06-25T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

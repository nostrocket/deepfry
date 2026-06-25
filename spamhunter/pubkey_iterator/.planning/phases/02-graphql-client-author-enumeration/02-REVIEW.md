---
phase: 02-graphql-client-author-enumeration
reviewed: 2026-06-25T00:00:00Z
depth: deep
files_reviewed: 9
files_reviewed_list:
  - src/enumerate.rs
  - src/graphql/client.rs
  - src/graphql/envelope.rs
  - src/graphql/queries.rs
  - src/graphql/mod.rs
  - src/main.rs
  - src/model.rs
  - src/store/mod.rs
  - src/store/writer.rs
findings:
  blocker: 1
  high: 0
  medium: 2
  low: 3
  total: 6
status: resolved
resolved: 2026-06-25T00:00:00Z
resolution: All 6 findings fixed (BL-01, MD-01, MD-02, LW-01, LW-02, LW-03). cargo build/test/clippy green; tests 30→32 (+terminal_page_flushed_before_done, +invalid_cursor_on_page1_aborts). One commit per finding (fix(02): ...).
---

# Phase 2: Code Review Report

**Reviewed:** 2026-06-25
**Depth:** deep (cross-file: enumerate ↔ store ↔ writer actor ↔ graphql client)
**Files Reviewed:** 9
**Status:** issues_found

## Summary

The two-layer error dispatch in `client.rs` is correct and well-ordered (status → in-body `errors[]` → `data`), SQL is uniformly parameterized (`params![]`/`?N`, no `format!` into SQL anywhere in `writer.rs`/`mod.rs`), pubkeys are hex-validated before the DB write, and the single-writer invariant is preserved (the short-lived `run_write_conn` touches only the `run` row). The retry/abort/INVALID_CURSOR branches are individually sound and well-tested.

The one serious defect is a **flush-before-cursor violation on the terminal page**: the invariant the module docstring claims to uphold ("the cursor never advances past unwritten pubkeys") is broken precisely at clean termination, where the run is marked `done` via a separate committed connection *before* the terminal page's pubkeys are guaranteed durable. Because a `done` run is never re-enumerated by `--resume`, this is a silent data-loss window. Everything else is medium/low.

## Blockers

### BL-01: Terminal page can be lost — `mark_run_done` commits before the last page's pubkeys are flushed

**File:** `src/enumerate.rs:180-194` (and the invariant claimed at `src/enumerate.rs:104-113`, `src/enumerate.rs:175-179`)

**Issue:** In the pagination loop, every non-terminal page flushes before advancing the cursor:

```rust
match page.end_cursor {
    Some(c) => {
        store.flush()?;            // barrier: terminal batch durable
        store.set_run_cursor(run_id, &c)?;
        after = Some(c);
    }
    None => break,                 // <-- NO flush() here
}
```

On the terminal page (`endCursor == null`), `insert_pubkeys(valid)` was enqueued at line 171, but the `None` arm `break`s with **no `store.flush()`**. Control then reaches:

```rust
let max_end = client.stats().await?.max_lev_id;
store.mark_run_done(run_id, max_end)?;   // short-lived connection, commits immediately
Ok(())
```

`mark_run_done` opens its own connection and commits `status='done'` synchronously. The terminal page's pubkeys, however, are still only sitting in the flume channel — the writer actor is guaranteed to have committed them only after `store.close()` drops the sender and **joins** the writer thread (in `main.rs:39`).

This reverses the load-bearing ordering for the last page: the `done` mark becomes durable on disk *before* the terminal page's pubkeys are guaranteed durable. If the process is killed (OOM, SIGKILL, power loss) in the window between `mark_run_done` committing and the writer actor committing its final batch, the run is recorded `done` while the terminal page's pubkeys were never written. `latest_unfinished_run` filters `WHERE status != 'done'` (`src/store/mod.rs:118`), so `--resume` will never re-fetch that run — the loss is permanent and silent. This is the exact flush-before-cursor failure mode (Pitfall 2 / D-07) the code documents as closed for every *other* page.

Note the existing tests do not catch this because `store.close()` is always called immediately after `run()` returns and joins the writer, masking the crash window.

**Fix:** Flush before marking the run done, so the terminal batch is durable before the terminal-state row is committed by the separate connection:

```rust
None => break,
// ...
}
}

// Terminal page's pubkeys must be durable before the run is marked done.
store.flush()?;
let max_end = client.stats().await?.max_lev_id;
store.mark_run_done(run_id, max_end)?;
Ok(())
```

(Placing the `flush()` after the loop covers the terminal page regardless of how the loop exits with a pending `insert_pubkeys`.)

## Medium

### MD-01: `INVALID_CURSOR` on a fresh (`after == None`) request spins forever

**File:** `src/enumerate.rs:145-148`

**Issue:** The restart branch unconditionally resets the cursor and re-loops:

```rust
Err(ClientError::Graphql { code: Some(ref c), .. }) if c == "INVALID_CURSOR" => {
    after = None;
    continue;
}
```

If the adapter returns `INVALID_CURSOR` while `after` is already `None` (page 1), the branch sets `after = None` (no change) and `continue`s, re-issuing the identical `authors(None, 500)` request. There is no retry ceiling, no backoff, and no abort on this path — the loop becomes a tight, un-sleeping hot loop hammering the adapter indefinitely. The contract may guarantee `after=None` never yields `INVALID_CURSOR`, but this is the trust boundary and the rest of the file validates defensively (see the hex check at `:95`). A buggy or version-skewed adapter turns a transient error into an unbounded busy-loop that also never returns control to the operator.

**Fix:** Guard the restart so it only fires when a non-null cursor was actually in play, and abort (cursor preserved) otherwise:

```rust
Err(ClientError::Graphql { code: Some(ref c), .. })
    if c == "INVALID_CURSOR" && after.is_some() =>
{
    after = None;
    continue;
}
```

The remaining (`after == None`) `INVALID_CURSOR` then falls through to the generic `Err(e)` arm, which marks the run aborted and returns — a loud, bounded failure instead of a silent spin.

### MD-02: Drift-probe `stats()` and `begin_run` are not covered by the retry helper

**File:** `src/enumerate.rs:134`, `src/enumerate.rs:192`

**Issue:** Page fetches go through `fetch_page_with_retry`, which retries `503`/transport/codeless errors with bounded backoff (the documented D-06/D-08 policy). But the two drift-probe calls — `client.stats().await?` at the start (`:134`) and at clean termination (`:192`) — call the client directly with `?`. A `503` from the adapter (the very "adapter still booting" condition the retry policy exists for) on the *start* probe aborts the whole run before page 1 is ever attempted, and a transient `503`/transport blip on the *end* probe aborts a run whose entire keyspace was already enumerated and cursor-advanced — discarding the `done` mark for a recoverable hiccup. The asymmetry (pages tolerate transients, probes don't) is surprising given the start probe races adapter startup most directly.

**Fix:** Route the probes through a small retry wrapper too (or generalize `fetch_page_with_retry` into a `retry(|| client.stats())`-style helper), so `Unavailable`/`Transport` on a probe gets the same bounded backoff rather than aborting the run.

## Low

### LW-01: `set_run_max_lev_end` is dead code

**File:** `src/store/mod.rs:163-170`

**Issue:** `set_run_max_lev_end` is never called by production code — `enumerate::run` records the end high-water mark exclusively through `mark_run_done(run_id, max_end)` (`src/enumerate.rs:193`), which sets `max_lev_id_end` itself (`src/store/mod.rs:189`). The only callers of `set_run_max_lev_end` are in the store's own unit tests (`:695`). It is a public method with no production caller and no behavioral coverage of a real path.

**Fix:** Remove `set_run_max_lev_end` (and its `set_run_max_lev_roundtrip` assertion of it), or document it as a Phase-3 seam if a future code path is expected to need a standalone end-mark distinct from `mark_run_done`.

### LW-02: Progress log over-counts on resume / overlap

**File:** `src/enumerate.rs:172-173`

**Issue:** `count += valid.len() as u64` sums the *validated* pubkeys of every fetched page and the log prints it as "enumerated {count} distinct pubkeys". On a resume that re-fetches an overlapping page (the exact scenario `pubkeys_idempotent_on_resume` exercises — PK2 served twice), the same pubkey is counted twice even though `INSERT OR IGNORE` collapses it to one row. The number is neither distinct nor the row count; it is "rows seen across pages", which contradicts the log's own wording and will mislead an operator estimating progress.

**Fix:** Either reword the log to "fetched {count} pubkeys (pre-dedup)" or drop the running total and log per-page counts. If a true distinct count is wanted, query `SELECT count(*) FROM pubkey` at termination.

### LW-03: `block_on` / `current_thread` runtime claim is fine, but `Resp::ok`/`status` 503 bodies are `{}` while the dispatch never parses them

**File:** `src/enumerate.rs:473`, `src/graphql/client.rs:99-100` (informational)

**Issue:** Not a defect in shipping code — noting for test-fidelity. The 503 test fixtures return body `{}` (e.g. `src/enumerate.rs:473`), and the client short-circuits 503 at `client.rs:99` before any body parse, so the body is irrelevant. This is correct, but a future reader may assume the `{}` body is meaningful. A one-line comment in the stub ("body ignored — 503 short-circuits before parse") would prevent a misread. No code change required.

---

_Reviewed: 2026-06-25_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: deep_

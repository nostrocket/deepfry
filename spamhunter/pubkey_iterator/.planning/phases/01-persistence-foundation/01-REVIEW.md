---
phase: 01-persistence-foundation
reviewed: 2026-06-25T00:00:00Z
depth: standard
files_reviewed: 7
files_reviewed_list:
  - Cargo.toml
  - src/lib.rs
  - src/model.rs
  - src/store/mod.rs
  - src/store/schema.rs
  - src/store/writer.rs
  - src/store/queries.rs
findings:
  critical: 0
  warning: 4
  info: 3
  total: 7
status: issues_found
---

# Phase 1: Code Review Report

**Reviewed:** 2026-06-25
**Depth:** standard
**Files Reviewed:** 7
**Status:** issues_found

## Summary

Reviewed the `pubkey_iterator` persistence crate: a single-writer SQLite actor with
batched transactions, idempotent UPSERTs, and WAL mode. The core security posture is
sound — **every SQL value is bound via `params![]` / `?N`; there is no `format!`
interpolation of pubkeys, layer names, or values anywhere (T-01-01 satisfied).** The
schema is parameter-free DDL. No injection, no hardcoded secrets, no unsafe code, no
`eval`-equivalents.

The defects found are all in the **error-handling and concurrency-contract** surface,
not the SQL/data path. The dominant theme: the writer thread can die on a DB error and
the API does not surface that cleanly — it converts a recoverable error into either a
**panic in the producer** or a **silently swallowed result**. None of the existing 5
tests exercise a writer-side failure, so this path is untested. There is also one
**documented-invariant violation**: `begin_run` opens a second write connection while
the module docstring twice asserts that no second write connection exists.

No BLOCKER/critical findings — the happy path is correct, idempotency holds across both
in-batch and cross-batch boundaries (tests prove it), and FK ordering inside the writer
transaction is correct (pubkey upserted before score/signal).

## Narrative Findings (AI reviewer)

## Warnings

### WR-01: Writer-thread death converts a recoverable DB error into a producer-side panic

**File:** `src/store/mod.rs:107-111` (with cause at `src/store/writer.rs:58-78`)
**Issue:**
If any statement in `writer_loop` returns `Err` (`tx.transaction()?`, any
`execute(...)?`, or `tx.commit()?`), the loop returns `Err`, the thread exits, and the
`flume::Receiver` is dropped — the channel is now closed on the receive side. The
**next** call to:

```rust
pub fn persist(&self, p: Persist) {
    if let Some(tx) = &self.tx {
        tx.send(p).expect("writer thread is alive while Store is open");
    }
}
```

will have `tx.send()` return `Err(SendError)` because the receiver is gone, and
`.expect(...)` **panics the producer thread**. The doc comment claims this "Panics only
if the writer thread has already gone away (a programming error — `persist` after
`close`)", but that is not the only cause: a transient SQL error (disk full, busy
timeout exceeded, constraint violation from a future caller) silently kills the writer,
and the real error is buried in the joined `JoinHandle` while the producer dies with a
misleading panic message. The actual `rusqlite::Error` is only observable later, at
`close()`, and only if `close()` is reached before the panic.

**Fix:**
Make `persist` fallible (or store the last writer error) so the DB error is surfaced at
the producer instead of panicking. Minimal version:

```rust
pub fn persist(&self, p: Persist) -> rusqlite::Result<()> {
    match &self.tx {
        Some(tx) => tx.send(p).map_err(|_| {
            // Writer is gone; the real cause is the rusqlite::Error captured by
            // close()/join. Surface a clear signal rather than panicking.
            rusqlite::Error::InvalidQuery // or a custom error: "writer thread terminated"
        }),
        None => Ok(()), // see WR-03
    }
}
```

If keeping the infallible signature is required for the call-site ergonomics, at minimum
distinguish "writer died on error" from "persist after close" rather than asserting the
former cannot happen.

### WR-02: `begin_run` opens a second write connection, violating the documented single-writer invariant

**File:** `src/store/mod.rs:87-100`
**Issue:**
The module docstring states twice that the design "deliberately exposes NO second write
connection" (mod.rs:10-13, 66-69) and that "Every write funnels through the single writer
actor." `begin_run` breaks this:

```rust
pub fn begin_run(&self, config_json: &str) -> rusqlite::Result<i64> {
    let conn = Connection::open(&self.path)?;   // <-- a SECOND write connection
    ...
    conn.execute("INSERT INTO run ...", params![now, config_json])?;
    Ok(conn.last_insert_rowid())
}
```

This is a genuine second writer on the same DB file, concurrent with the actor's writer
connection. WAL tolerates this (one writer at a time, `busy_timeout(5s)` absorbs
contention), so it is unlikely to corrupt or deadlock in practice — which is why this is
a WARNING, not a BLOCKER. But it:
1. Contradicts the stated invariant, eroding the guarantee future phases will rely on.
2. Can hit `SQLITE_BUSY` and fail with a 5s stall if the actor is mid-commit on a large
   batch when `begin_run` fires.
3. Bypasses the actor's ordering, so the "all writes funnel through one place" mental
   model no longer holds for reviewers/maintainers.

**Fix:**
Either (a) route the run-row INSERT through the writer actor (add a `Persist`-style
variant or a dedicated control message that returns the `run_id` over a oneshot/
`flume::bounded(0)` reply channel), or (b) keep `begin_run` as-is but **correct the
docstrings** to say "one write connection at a time; `begin_run` is the only out-of-band
writer and is safe under WAL+busy_timeout." Do not let code and the invariant doc
disagree.

### WR-03: `persist()` silently drops the payload after `close()` instead of signalling

**File:** `src/store/mod.rs:107-111`
**Issue:**
When `tx` is `None` (i.e. after `close()` set it to `None`, or in the `Drop` path),
`persist` takes the `if let Some` false branch and **returns silently, discarding `p`**.
Combined with WR-01, the contract is now inconsistent: persisting after the writer
*died* panics, but persisting after a clean *close* is a silent no-op that loses data
with no indication. A caller that accidentally persists post-close gets neither an error
nor the row.

**Fix:**
Make the post-close case explicit. If `persist` becomes fallible (WR-01), return an
`Err` here too. If it must stay infallible, at least `debug_assert!(self.tx.is_some(),
"persist after close drops the payload")` so the misuse is caught in tests/debug builds
rather than silently losing data.

### WR-04: `Drop` swallows the writer's flush result — implicit-drop path can lose data with no signal

**File:** `src/store/mod.rs:138-143`
**Issue:**
```rust
fn drop(&mut self) {
    self.tx = None;
    if let Some(handle) = self.writer.take() {
        let _ = handle.join();   // <-- rusqlite::Result discarded
    }
}
```
The `Drop` path discards both the `JoinError` (panic in writer) and the
`rusqlite::Result<()>` returned by `writer_loop`. The docstring sells `Drop` as a
"best-effort flush." But if the final batch's `tx.commit()` failed, the caller who relied
on Drop (never called `close()`) gets **no signal at all** that committed rows were lost.
This is the silent-data-loss footgun the WAL+NORMAL durability note (mod.rs:1-8) is
careful about everywhere else. The explicit `close()` path surfaces the error correctly
(mod.rs:122-124); only the implicit Drop path is blind.

**Fix:**
At minimum, log/eprintln the dropped error so a failed final flush is observable:

```rust
fn drop(&mut self) {
    self.tx = None;
    if let Some(handle) = self.writer.take() {
        match handle.join() {
            Ok(Err(e)) => eprintln!("pubkey_iterator: writer flush failed on drop: {e}"),
            Err(_)     => eprintln!("pubkey_iterator: writer thread panicked on drop"),
            Ok(Ok(())) => {}
        }
    }
}
```
Better: document that `close()` is the supported shutdown and Drop is fallback-only, and
keep the eprintln so the fallback failure is never fully silent.

## Info

### IN-01: `Run` / `Fingerprint` / `Label` / `Weight` models and their tables have no read or write path in this phase

**File:** `src/model.rs:18-89`, `src/store/schema.rs:48-72`, `src/store/queries.rs` (whole file)
**Issue:**
`Run`, `Fingerprint`, `Label`, and `Weight` are declared and the `fingerprint`,
`label`, and `weight` tables are created, but nothing in this crate inserts into or reads
from them (verified: no `Fingerprint`/`Label`/`Weight` references outside `model.rs`; the
writer only touches `pubkey`/`score`/`signal`). `begin_run` inserts a `run` row but the
`Run` struct itself is never constructed or read. This is expected scaffolding for a
foundation phase, but it is currently dead code/dead schema and will read as unused until
a later phase wires it. Flag so it is tracked, not forgotten.

**Fix:** No action this phase. Confirm later phases consume these, or add `#[allow(dead_code)]`
with a `// Phase N` note so the intent is explicit and the compiler `dead_code` warning
(on the model fields/structs) is acknowledged rather than ambient.

### IN-02: `begin_run` connection does not set `synchronous=NORMAL`, unlike the writer

**File:** `src/store/mod.rs:88-90`
**Issue:**
`begin_run`'s short-lived connection sets `foreign_keys=ON` and `busy_timeout` but not
`synchronous=NORMAL`, so the run-row INSERT commits at the connection default
(`synchronous=FULL` once the DB is WAL). Correctness is fine (FULL is stricter, not
looser); it is an inconsistency with the documented durability profile and a redundant
extra fsync. Minor.

**Fix:** Add `conn.pragma_update(None, "synchronous", "NORMAL")?;` for consistency, or
fold the run-row write into the actor (see WR-02) where the pragma is already set.

### IN-03: Documented "64-char lowercase hex" pubkey invariant is not enforced anywhere

**File:** `src/store/schema.rs:26` (comment), enforced nowhere; write path `src/store/writer.rs:64`
**Issue:**
The `pubkey` column comment declares "64-char lowercase hex", and `model.rs:1-14`
documents bool/u64 storage conventions, but no code validates pubkey length/charset or
the documented hex format before insert. Not a security issue (fully parameterized, so no
injection), and not a correctness bug for SQLite (TEXT accepts anything). It is a
data-quality gap: a malformed or uppercase pubkey would persist silently and could break
later `(run_id, pubkey)` joins or dedup that assume the canonical form.

**Fix:** No action required for Phase 1 if upstream guarantees canonical pubkeys. If not,
add a cheap validation (`pk.len() == 64 && pk.bytes().all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())`)
at the API boundary or document explicitly that the caller owns this guarantee.

---

_Reviewed: 2026-06-25_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

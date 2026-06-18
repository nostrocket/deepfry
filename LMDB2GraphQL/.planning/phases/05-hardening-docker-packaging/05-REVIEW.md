---
phase: 05-hardening-docker-packaging
reviewed: 2026-06-15T00:00:00Z
depth: standard
files_reviewed: 4
files_reviewed_list:
  - spam/src/main.rs
  - spam/src/server.rs
  - spam/tests/ready_window_test.rs
  - spam/build.rs
findings:
  critical: 2
  warning: 4
  info: 2
  total: 8
status: resolved
resolution: "All 2 BLOCKERs (CR-01, CR-02) and 4 WARNINGs (WR-01..04) fixed in commits c02240a/c9ae7cf via the bind-once readiness-gated-router redesign; confirmed RESOLVED by independent re-review (no new blockers)."
---

# Phase 05: Code Review Report

**Reviewed:** 2026-06-15T00:00:00Z
**Depth:** standard
**Files Reviewed:** 4
**Status:** issues_found

## Summary

This review covers the phase 05-03 OPS-01 gap-closure change: `main.rs` now binds
a `/health` + `/ready` probe router *before* the LMDB-open + comparator self-check
gate chain, flips the readiness `AtomicBool` to `true` only after the gate chain
returns `Ok`, then gracefully shuts down the probe server and re-binds the same
address to serve the full GraphQL router. `server.rs` adds `build_probe_router`;
`ready_window_test.rs` is the new regression test; `build.rs` has only a
doc-comment edit.

The intent (OPS-01) is sound and the regression test correctly drives the
`false→true` transition through a router-served surface on a *shared* `Arc`.
However, the implementation strategy — shut down the probe server, release the
address, then re-bind a fresh listener — reintroduces a visible availability gap
at the exact moment OPS-01 is trying to eliminate, and mishandles the
ephemeral-port (`:0`) and address-logging cases. Those are the two BLOCKERs below.

No write LMDB transaction was introduced (verified: only `open_read_only_env`,
`read_meta`, and `run_comparator_self_check` against `&env`). The `build.rs`
change is benign.

## Critical Issues

### CR-01: Probe-shutdown → re-bind sequence creates a connection-refused gap precisely when readiness flips to 200

**File:** `spam/src/main.rs:147-165`
**Issue:**
The post-gate sequence is:

1. `ready.store(true, ...)` (line 147) — probe `/ready` now answers 200
2. `shutdown.notify_one(); let _ = probe_handle.await;` (lines 154-155) — probe server stops, the **TCP address is released**
3. `NetListener::bind(&cfg.bind_address).await?` (line 163) — re-bind a fresh listener
4. `axum::serve(listener, router).await` (line 182) — full router starts accepting

Between step 2 and step 4 there is a window in which **no socket is listening on
the bind address at all**. An orchestrator polling `/ready` during this window
gets `ECONNREFUSED`, not `200` and not `503`. This defeats the OPS-01 goal: the
reason for binding the probe before the gate chain was to give the orchestrator a
continuously-observable `503→200` transition. The actual observable sequence is
`503 … 200(briefly) … connection-refused … 200`, and a probe landing in the gap
can be read by k8s/Nomad as a crash (restart) or as "not yet up," reintroducing
exactly the flapping OPS-01 was meant to remove. The comment on lines 158-159
asserts re-bind "is reliable… immediately after graceful shutdown (SO_REUSEADDR)";
SO_REUSEADDR concerns `TIME_WAIT` reuse — it does not make the address
continuously *served*. The gap is structural, not a `TIME_WAIT` artifact.

**Fix:** Do not tear down and re-bind. Bind once and serve a single router for the
process lifetime, gating the data surface on readiness rather than swapping
listeners. The cleanest fix: serve `build_router` (whose `/ready` already returns
503 until the flag flips) on the originally-bound listener, never releasing it:

```rust
// Bind ONCE before the gate chain.
let listener = tokio::net::TcpListener::bind(&cfg.bind_address).await?;
// ... run gate chain (env open -> Meta gates -> self-check) ...
ready.store(true, Ordering::Release);
// Serve the full router on the SAME listener — no probe task, no re-bind.
axum::serve(listener, build_router(schema, Arc::clone(&ready))).await?;
```

If the data surface must not be *mounted* until ready, hand the same bound
`listener` value into the full server after the gate chain without an intervening
close/re-open. The current code is forced into the re-bind only because the probe
task consumes `listener`; restructure so the listener is not moved into the probe
task.

### CR-02: Re-bind with an ephemeral port (`:0`) binds a different port than the gate-window socket, and the logged address is wrong

**File:** `spam/src/main.rs:64-68, 163-165, 180`
**Issue:**
`cfg.bind_address` is a free-form string (`config.rs:41`) and may legitimately be
`127.0.0.1:0` (ephemeral port). The first bind (line 64) picks concrete port P1
and `local_addr` (line 68) captures P1. After the probe shuts down, the re-bind
(line 163) passes `&cfg.bind_address` again — still `:0` — so the OS assigns a
**different** port P2. The full router then serves on P2, but:

- line 180 logs `addr = %local_addr` (P1) — operators/orchestrators are told the
  wrong port;
- any client that discovered P1 during the gate window can no longer reach the
  service.

Even with a fixed port, line 180 logs the *probe* listener's `local_addr`, not the
re-bound listener's — it is only coincidentally correct. A latent correctness bug
masked by the common fixed-port deployment.

**Fix:** Eliminating the re-bind (CR-01) removes this bug — one listener, one
`local_addr`. If a re-bind is retained, recompute and log from the re-bound
listener, and reject `:0` if a stable address is required:

```rust
let listener = NetListener::bind(&cfg.bind_address).await?;
let local_addr = listener.local_addr()?;          // may differ from the probe's
tracing::info!(addr = %local_addr, "GraphQL server listening");
```

## Warnings

### WR-01: Probe `axum::serve` result is discarded — a failed probe serve silently blinds the gate window

**File:** `spam/src/main.rs:91-97`
**Issue:**
`let _ = axum::serve(listener, probe_router).with_graceful_shutdown(...).await;`
discards the serve `Result`. If `axum::serve` returns `Err` early (e.g. an
accept-loop error), the probe task ends silently while `main` is still inside the
gate chain. For the rest of the gate window the orchestrator gets
connection-refused on `/ready` instead of the intended `503` — with no log and no
signal. Same window-blinding outcome as CR-01, triggered by an error path.

**Fix:**
```rust
if let Err(e) = axum::serve(listener, probe_router)
    .with_graceful_shutdown(async move { probe_shutdown.notified().await; })
    .await
{
    tracing::error!(error = %e, "probe server exited with error");
}
```

### WR-02: `probe_handle.await` JoinError discarded — a panicked probe task is swallowed

**File:** `spam/src/main.rs:155`
**Issue:**
`let _ = probe_handle.await;` discards the `JoinError`. If the probe task panicked
rather than exiting cleanly, `main` re-binds anyway and the panic is never
surfaced in logs, so the operator has no signal that the readiness window
misbehaved.

**Fix:**
```rust
if let Err(e) = probe_handle.await {
    tracing::error!(error = %e, "probe server task panicked");
}
```

### WR-03: `shutdown.notify_one()` correctness depends on Notify's stored-permit semantics; a missed notify deadlocks `probe_handle.await`

**File:** `spam/src/main.rs:88-97, 154`
**Issue:**
`tokio::sync::Notify::notify_one()` is relied on to wake the probe task's
graceful-shutdown future. The probe future must reach `probe_shutdown.notified()`
inside `with_graceful_shutdown` before the wake matters. `Notify` does store a
single permit for a *future* `notified()` call, so this is usually safe — but the
safety hinges on that subtle stored-permit guarantee, which is undocumented at this
call site. If that guarantee were ever not satisfied, `probe_handle.await`
(line 155) would block forever, since nothing else notifies — a hang on the happy
path.

**Fix:** Document the reliance on `Notify`'s stored-permit behaviour, or use a
mechanism without the subtlety (a `oneshot` channel, or `tokio_util`'s
`CancellationToken`). Removing the re-bind (CR-01) eliminates the shutdown
handshake entirely.

### WR-04: Comment claims the non-loopback warning fires "regardless of gate outcome" — it only fires after a successful bind

**File:** `spam/src/main.rs:74-82`
**Issue:**
The comment (line 74) says the warning "fires right after bind, regardless of gate
outcome." It does fire before the gate chain, but it does not fire if `bind`
(line 64) or `local_addr()` (line 68) fail — the `?` exits first. The wording
overstates the guarantee and should not be mistaken for a security invariant.

**Fix:** Reword to "fires right after a successful bind, before the gate chain
runs." No code change needed.

## Info

### IN-01: `build_probe_router` is preceded by a doc block describing `build_router`'s four routes and body-limit layer — wrong for the item it documents

**File:** `spam/src/server.rs:61-113`
**Issue:**
Lines 61-84 describe four routes (`GET`/`POST /graphql`, `/health`, `/ready`) and
the `RequestBodyLimitLayer`, then run straight into `build_probe_router` (line
108), which has *none* of those — no `/graphql`, no body-limit layer. The accurate
probe-router doc already exists at lines 85-107; the earlier block is a stale
copy/paste of `build_router`'s doc attached to the wrong item. The same block is
then repeated verbatim at lines 115-138 above the real `build_router`.

**Fix:** Delete the misplaced lines 61-84 (four-route / body-limit description that
bleeds into `build_probe_router`); keep only the accurate `/health` + `/ready`
doc at lines 85-107.

### IN-02: `env.clone()` / `meta.clone()` are correct (not LMDB re-opens) — noted to preempt false flags

**File:** `spam/src/main.rs:172, 174`
**Issue:**
`env.clone()` is a cheap refcount clone (heed `Env`, per `schema.rs` ownership
notes) and `meta.clone()` is a plain struct clone. Neither re-opens the LMDB env.
No defect — flagged only because a reviewer scanning for accidental env re-opens
might otherwise stop here.

**Fix:** None required. Optionally add `// cheap refcount clone` beside
`env.clone()`.

---

_Reviewed: 2026-06-15T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

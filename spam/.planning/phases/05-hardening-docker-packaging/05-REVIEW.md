---
phase: 05-hardening-docker-packaging
reviewed: 2026-06-15T00:00:00Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - .github/workflows/lmdb2graphql.yml
  - docker-compose.lmdb2graphql.yml
  - spam/Dockerfile
  - spam/config/lmdb2graphql.yaml.example
  - spam/src/graphql/resolvers.rs
  - spam/src/graphql/schema.rs
  - spam/src/graphql/types.rs
  - spam/src/main.rs
  - spam/src/server.rs
  - spam/tests/body_limit_test.rs
  - spam/tests/generate_fixture.sh
  - spam/tests/health_ready_test.rs
findings:
  critical: 1
  warning: 7
  info: 5
  total: 13
status: issues_found
---

# Phase 5: Code Review Report

**Reviewed:** 2026-06-15
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

Phase 5 adds /health + /ready probes, surfaces `pinnedStrfryVersion` through stats, and ships
Docker packaging plus a CI correctness gate. The code is well-commented and the invariants the
phase cares about (read-only LMDB, `:ro` bind mount, loopback publish, pinned digest lockstep)
are mostly honored. The pinned strfry digest `545555da…d63d2c5` is consistent across all five
required locations (Dockerfile.strfry, CI workflow, generate_fixture.sh, config.rs, and the
example config) — that lockstep invariant holds.

However, the headline OPS-01 feature — the readiness probe — has a concrete defect: the readiness
flag is set `true` immediately on the line after construction, before the gate it is supposed to
guard semantically takes effect, and the HTTP server only begins serving *after* that store. The
practical result is that `/ready` can never return 503 in production, defeating the probe's
purpose during the startup window. There are also several robustness gaps (no `bind_address`
validation, container exposure semantics that contradict the loopback intent, restart-loop /
config-mount fragility) worth fixing before shipping.

## Critical Issues

### CR-01: `/ready` probe can never report "not ready" in production — readiness window collapses to zero

**File:** `spam/src/main.rs:84-86`, `spam/src/main.rs:97-122`
**Issue:** The OPS-01 readiness contract is "return 503 while startup gates are incomplete, 200
once ready." But the flag is created and flipped on adjacent lines:

```rust
let ready = Arc::new(AtomicBool::new(false));
ready.store(true, Ordering::Release);   // flipped immediately
```

and the router/listener that exposes `/ready` is only built and served *afterward*
(`build_router(... ready)` at line 98, `axum::serve` at line 122). The probe therefore observes
`true` from the very first request it can ever receive. The 503 branch in `ready_handler`
(`server.rs:133`) is dead in production — it is only exercised by unit tests that construct the
flag directly. An orchestrator polling `/ready` gains nothing over `/health`: any window where
the service is "up but not ready" does not exist because the socket isn't bound until after the
flag is already true.

This is a correctness defect in the feature the phase was built to deliver. The comment at
`main.rs:81-83` asserts "set true only after all startup gates pass … the flag is never set true
before run_comparator_self_check returns" — which is true but irrelevant, because the listener
binds after the store, so external observers never see the false state the probe is designed to
report.

**Fix:** Bind the listener and spawn the server *before* the gate chain completes, sharing the
`ready` flag, so the probe is live during startup; flip the flag only after the self-check
returns. Concretely, restructure so the HTTP server starts early:

```rust
let ready = Arc::new(AtomicBool::new(false));
// build a minimal router that can answer /health and /ready immediately
// (AppState/schema can be injected once gates pass, or use a holder)
let listener = tokio::net::TcpListener::bind(&cfg.bind_address).await?;
let server = tokio::spawn(axum::serve(listener, build_router(schema, Arc::clone(&ready))).into_future());

// ... run version/endianness gates + comparator self-check ...
run_comparator_self_check(&env, &golden)?;
ready.store(true, Ordering::Release);   // NOW observable as a real false→true transition
server.await??;
```

If serving the GraphQL surface before gates pass is undesirable, at minimum bind the listener and
serve `/health` + `/ready` before the comparator self-check (the slow gate), so `/ready` reports
503 during that window. As written, the probe provides no signal it claims to provide.

## Warnings

### WR-01: `bind_address` is never validated — a malformed value is a late, opaque startup failure

**File:** `spam/src/main.rs:101-103`, `spam/src/config.rs` (no validation)
**Issue:** `bind_address` is a free-form `String` parsed straight into
`TcpListener::bind(&cfg.bind_address)`. A typo (`"0.0.0.0;8080"`, `"localhost:8080"` with no DNS,
missing port) is not caught at config-load time; it surfaces only at the bind call, after all the
expensive LMDB gates and the comparator self-check have run. In Docker with `restart: unless-stopped`
this produces a slow crash-restart loop (open env → run self-check → fail bind → exit → repeat),
each cycle re-opening the 10 TiB-mapped env.
**Fix:** Validate at config load by parsing to `SocketAddr` (or `ToSocketAddrs`) and returning a
clear error before any LMDB work:

```rust
cfg.bind_address.parse::<std::net::SocketAddr>()
    .with_context(|| format!("invalid bind_address: {:?}", cfg.bind_address))?;
```

### WR-02: Example config and compose force `0.0.0.0` bind, undercutting the loopback default and CR-01 intent

**File:** `spam/config/lmdb2graphql.yaml.example:27`, `docker-compose.lmdb2graphql.yml:33-39`
**Issue:** `config.rs` deliberately defaults `bind_address` to `127.0.0.1:8080` to prevent silent
public exposure (the in-code CR-01 rationale). But the *shipped example config* hardcodes
`bind_address: "0.0.0.0:8080"` as an uncommented active line, and the compose file requires it.
The publish rule `127.0.0.1:8080:8080` does scope host exposure to loopback — *but only on the
default bridge*. This service also joins `deepfry-net` (an external user-defined network). On a
user-defined network every other container can reach `0.0.0.0:8080` directly, bypassing the host
publish rule entirely. So the unauthenticated, full-introspection GraphQL endpoint is reachable by
any container on `deepfry-net`, which the comments do not acknowledge (they only reason about host
exposure). The defense-in-depth story ("`:ro` even if code bugs", loopback default) is weakened by
shipping a wide bind as the copy-paste default.
**Fix:** Keep the in-container bind as narrow as the deployment allows. If only the host needs
access, do not attach to `deepfry-net` and bind `127.0.0.1` in-container too. If other DeepFry
containers must query it, document that the endpoint is reachable network-wide and gate it
(network ACL / the consumer set), and change the example to present `0.0.0.0` as a commented opt-in
rather than the active default.

### WR-03: Healthcheck depends on `wget`, which is not guaranteed in the runtime image

**File:** `docker-compose.lmdb2graphql.yml:44`, `spam/Dockerfile:47-52`
**Issue:** The healthcheck is `["CMD", "wget", "-qO-", "http://localhost:8080/health"]`. The
runtime stage is `alpine:3.21`, whose BusyBox does ship `wget`, so this works *today*. But the
binary is statically linked specifically so the runtime needs nothing but the binary and
`ca-certificates`; the healthcheck silently reintroduces a dependency on BusyBox applets that the
image build does not assert. If the base is ever slimmed (distroless, `scratch`, BusyBox built
without `wget`) the healthcheck fails open/closed unexpectedly with no compile-time signal.
**Fix:** Either make the dependency explicit/owned, or remove it. The cleanest option is a
built-in health subcommand on the binary itself (`/app/lmdb2graphql --healthcheck` doing a local
HTTP GET), giving `["CMD", "/app/lmdb2graphql", "--healthcheck"]` with zero external-tool
dependency. At minimum, add a comment in the Dockerfile that the healthcheck relies on BusyBox
`wget`.

### WR-04: `restart: unless-stopped` + fail-closed startup gates = crash-loop on any drift

**File:** `docker-compose.lmdb2graphql.yml:26`, `spam/src/main.rs:52-74`
**Issue:** Startup intentionally fails closed on `dbVersion != 3`, endianness mismatch, or
comparator self-check failure (correct). Combined with `restart: unless-stopped`, a genuine
incompatibility (strfry upgraded, digest drift, corrupt fixture) becomes an infinite restart loop:
each restart re-opens the env, re-reads Meta, re-runs the self-check, exits non-zero, and restarts.
There is no backoff ceiling and the `start_period: 10s` only suppresses healthcheck failures, not
the restart cycle. Operators see a tight loop with the real error scrolling past, masked by repeated
startup noise.
**Fix:** Use `restart: on-failure:N` (bounded retries) for a service whose failures are
configuration/compatibility errors that won't self-heal, or document that an operator must
`docker compose stop` and investigate on repeated exits. Consider a distinct exit code for
"compatibility gate failed" vs transient errors so an orchestrator can distinguish "do not restart
me" from "retry."

### WR-05: Config mount path is brittly coupled to the container's root home directory

**File:** `docker-compose.lmdb2graphql.yml:34`, `spam/src/config.rs:72-73`
**Issue:** `config::load()` resolves the config as `dirs::home_dir().join("deepfry/lmdb2graphql.yaml")`.
The Dockerfile sets `WORKDIR /app` but no `USER`, so the process runs as root with home `/root`,
and the compose mounts config to `/root/deepfry/lmdb2graphql.yaml` — these line up *only because*
the image runs as root. Running the container as a non-root user (a common hardening step, and
sensible here since the only mount is `:ro`) would change `dirs::home_dir()` to the new user's home
and the config mount would silently miss, causing a startup failure that looks like "config not
found" with no hint that the cause is the home-dir assumption.
**Fix:** Make the config path explicit and independent of `$HOME` in container deployments — e.g.
honor an env var override (`LMDB2GRAPHQL_CONFIG_PATH`) in `config::load()`, or pass the path as a
CLI argument, and have the compose mount/point at that fixed path. This also removes the implicit
"must run as root" coupling.

### WR-06: `latestPerAuthor` returns a `HashMap`-derived list with nondeterministic ordering

**File:** `spam/src/graphql/resolvers.rs:158-175`
**Issue:** `latest_per_author` collects into `HashMap<String, Vec<_>>` and then iterates
`groups.into_iter()` to build `Vec<AuthorGroup>`. `HashMap` iteration order is unspecified and
randomized per-process (Rust's default `RandomState`), so the order of `AuthorGroup`s in the
response varies between identical requests. Clients that assume request-order or any stable order
of authors will see flapping results; it also makes responses harder to test/diff and breaks any
naive cursor/caching downstream. The per-author event ordering is preserved, but the group ordering
is not.
**Fix:** Emit groups in the caller-supplied `authors` order (deterministic and intuitive):

```rust
let result: Vec<AuthorGroup> = authors_in_request_order.iter()
    .filter_map(|a| groups.remove(a).map(|events| AuthorGroup {
        author: a.clone(),
        events: events.into_iter().map(decoded_event_to_gql).collect(),
    }))
    .collect();
```

(or sort by `author` before mapping). Requires keeping the original `authors` vec available after
the `spawn_blocking` move.

### WR-07: CI uses unpinned `rust:alpine` / rustup-stable — non-reproducible toolchain undermines the correctness gate

**File:** `.github/workflows/lmdb2graphql.yml:34-37`, `spam/Dockerfile:16`
**Issue:** The phase's stated value is a *correctness gate* — comparator parity proven against a
pinned strfry digest. But the toolchain on both sides is unpinned: CI installs `--default-toolchain
stable` (whatever stable is the day CI runs) and the Dockerfile builds `FROM rust:alpine` (a moving
tag). The strfry digest is pinned immutably; the Rust toolchain that compiles the byte-identical
comparator reimplementation is not. A future stable that changes codegen, or an `rust:alpine` bump,
can shift behavior of the very comparator/zstd code the gate is supposed to certify, with no
lockfile-level record of what compiled the "passing" artifact. CLAUDE.md also specifies pinning via
`rust-toolchain.toml` and `channel = "stable" + minimum version` — but that file is *deleted* in
both CI and Dockerfile (because it pins a macOS host triple), so the pin is lost on the platforms
that actually ship the binary.
**Fix:** Replace the Apple-pinned `rust-toolchain.toml` with a host-agnostic pin
(`channel = "1.NN.0"` with no triple) so it is valid on Linux/musl and macOS alike, and stop
deleting it in CI/Docker. Pin `rust:alpine` to a digest or explicit version tag
(`rust:1.NN-alpine3.21`). This makes the correctness gate reproducible against a known compiler,
matching the rigor already applied to the strfry digest.

## Info

### IN-01: `body_limit_test.rs` doc comments describe the wrong mechanism

**File:** `spam/tests/body_limit_test.rs:3-7`
**Issue:** The test's module doc says the limit is applied "via `DefaultBodyLimit::max(...)`", but
the implementation (and the whole point of WR-02-LAYER in `server.rs`) is that `DefaultBodyLimit`
does *not* bite on the `post_service` path and was replaced by `RequestBodyLimitLayer`. The test
comment contradicts the fix it validates and will mislead the next reader.
**Fix:** Update the doc comment to reference `RequestBodyLimitLayer`.

### IN-02: `test_small_body_accepted` assertion is tautological

**File:** `spam/tests/body_limit_test.rs:57-61`
**Issue:** `assert!(resp.status().is_success() || resp.status().as_u16() == 200, ...)` — `200` is a
success status, so the second clause is fully subsumed by `is_success()`. The `|| == 200` adds
nothing.
**Fix:** Drop the redundant clause: `assert!(resp.status().is_success(), ...)`. Consider asserting
the body actually contains GraphQL data to make the "accepted" claim meaningful (a 200 with a
GraphQL error would still pass today).

### IN-03: `state_from` helper is used by only one resolver while two others inline `ctx.data_unchecked`

**File:** `spam/src/graphql/resolvers.rs:207-209` vs `:69`, `:189`
**Issue:** `state_from(ctx)` was introduced "to reduce repetition" but only `latest_per_author`
uses it; `events` and `stats` call `ctx.data_unchecked::<AppState>()` directly. The abstraction is
inconsistent — it neither removes the repetition it claims to nor is applied uniformly.
**Fix:** Either use `state_from` in all three resolvers or inline it everywhere and delete the
helper.

### IN-04: Docker `EXPOSE 8080` is hardwired but the bind port is config-driven

**File:** `spam/Dockerfile:57`, `spam/config/lmdb2graphql.yaml.example:27`
**Issue:** `EXPOSE 8080` and the healthcheck/publish all assume port 8080, but `bind_address` is
operator-configurable. An operator who changes the in-container port in YAML gets a silently wrong
`EXPOSE`/healthcheck. `EXPOSE` is documentation-only so this is low-impact, but it is a latent
inconsistency.
**Fix:** Document that changing the in-container port requires updating `EXPOSE`, the healthcheck
URL, and the publish mapping together; or standardize on a fixed in-container port and only vary
the host-side publish.

### IN-05: `ca-certificates` installed in runtime image with no current consumer

**File:** `spam/Dockerfile:49-52`
**Issue:** The comment admits `ca-certificates` is for "any future TLS calls … (no-op overhead now)."
The service makes no outbound TLS calls; this is speculative dependency surface in a hardening phase
whose goal is a minimal runtime image.
**Fix:** Remove until an actual TLS consumer exists (YAGNI); re-add in the phase that introduces the
outbound call. Minor — keep if a near-term feature is planned.

---

_Reviewed: 2026-06-15_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

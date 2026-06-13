---
phase: 04-graphql-api
reviewed: 2026-06-13T00:00:00Z
depth: standard
files_reviewed: 9
files_reviewed_list:
  - src/config.rs
  - src/graphql/mod.rs
  - src/graphql/resolvers.rs
  - src/graphql/schema.rs
  - src/graphql/types.rs
  - src/lib.rs
  - src/main.rs
  - src/server.rs
  - Cargo.toml
findings:
  critical: 1
  warning: 5
  info: 4
  total: 10
status: issues_found
---

# Phase 4: Code Review Report

**Reviewed:** 2026-06-13
**Depth:** standard
**Files Reviewed:** 9
**Status:** issues_found

## Summary

Phase 4 wires the GraphQL API layer (types, schema, resolvers, axum server) over the
Phase-3 query engine. The read-only invariants are well honored: every resolver uses
`read_txn`/`.open()`, no `write_txn` or `.create()` appears, the schema uses
`EmptyMutation`/`EmptySubscription`, and `map_query_error` correctly maps internal LMDB/payload
errors to opaque `"internal error"` while exposing only cursor-decode reasons. Error mapping,
cursor fail-closed handling, and the limit clamps are sound.

However, the DoS posture has real gaps that the code's own comments wave away. The schema
deliberately omits depth/complexity limits, axum has no request-body cap, and `latestPerAuthor`
fan-out is uncapped — all reachable on a server that, by default config, **binds 0.0.0.0**. That
default-public bind combined with full introspection + GraphiQL and no rate/size limiting is the
headline concern. Several silent integer-cast semantic bugs in filter construction round out the
warnings.

## Critical Issues

### CR-01: Default config binds GraphQL server to all interfaces (0.0.0.0) with unauthenticated full-DB access

**File:** `src/config.rs:46-48`, `src/server.rs:42-55`, `src/main.rs:90-92`
**Issue:** `default_bind_address()` returns `"0.0.0.0:8080"`. When `bind_address` is omitted from
`lmdb2graphql.yaml` (the common case), the GraphQL endpoint listens on every network interface.
That endpoint serves the entire strfry event corpus with **no authentication**, **introspection
enabled**, and a **GraphiQL playground** (`server.rs:53-55`). The server.rs comment justifies this
with "no credentials are at risk" — but the risk is not credential leakage, it is unauthenticated
bulk read access to the whole relay's event data from any host that can route to the box. The
CLAUDE.md deployment model is "co-located with strfry, mounting strfry-db read-only," which implies
the service is an internal sidecar that should bind loopback / the compose network, not the world.

Combined with the absent DoS controls (see WR-01/WR-02), a default-public bind turns every other
"accepted v1 risk" into an internet-facing one.

**Fix:** Default to loopback, forcing operators to opt into wider exposure:
```rust
fn default_bind_address() -> String {
    // Bind loopback by default; operators must explicitly widen exposure in YAML.
    // The Docker compose network maps the published port explicitly.
    "127.0.0.1:8080".to_string()
}
```
If 0.0.0.0 must remain (container networking), gate it behind an explicit, documented config flag
and emit a `tracing::warn!` at startup when binding a non-loopback address so the exposure is never
silent. Also consider disabling introspection/GraphiQL in production builds.

## Warnings

### WR-01: GraphQL schema has no depth or complexity limit despite being the documented v1 DoS guard

**File:** `src/graphql/schema.rs:58-66`
**Issue:** `build_schema` explicitly does NOT call `.limit_depth()` or `.limit_complexity()`,
claiming "the limit ceiling (500) plus the engine's MAX_ROUNDS bound is the v1 DoS guard." That
reasoning is incomplete: the 500/MAX_ROUNDS bounds only constrain a *single* `events` resolver's
LMDB scan. They do nothing about (a) deeply nested / recursive selection sets, (b) introspection
query amplification, or (c) a single request containing many top-level aliased resolver calls
(`a: events{...} b: events{...} ...`), each of which spawns its own blocking LMDB scan. async-graphql
ships these limiters precisely for this; CLAUDE.md repeatedly calls out "built-in depth/complexity
limits (essential for protecting unbounded SQLite scans)" as a reason the crate was chosen — yet
they are unused.

**Fix:**
```rust
Schema::build(Query, EmptyMutation, EmptySubscription)
    .data(app_state)
    .extension(Tracing)
    .limit_depth(12)        // tune to the legitimate query shape
    .limit_complexity(2000) // bound aliased-resolver fan-out
    .finish()
```

### WR-02: No HTTP request body size limit on the GraphQL route

**File:** `src/server.rs:42-47`
**Issue:** `build_router` mounts `GraphQL::new(schema)` with no body-size cap. A client can POST an
arbitrarily large query/variables document; axum's default body limit applies to extractors but the
async-graphql Tower service path does not get a meaningful application-level cap here. A multi-MB
`authors` array (see WR-03) or a giant query string is accepted and buffered. On a default-public
bind (CR-01) this is a trivial memory/CPU amplification vector.

**Fix:** Wrap the route with a body limit layer:
```rust
use axum::extract::DefaultBodyLimit;
Router::new()
    .route("/graphql", get(graphiql).post_service(GraphQL::new(schema)))
    .layer(DefaultBodyLimit::max(256 * 1024)) // 256 KiB request cap
```

### WR-03: `latestPerAuthor` author count is uncapped — multiplicative LMDB fan-out

**File:** `src/graphql/resolvers.rs:116-151`
**Issue:** `per_author` is clamped to ≤500 but `authors.len()` is "deliberately uncapped (D-08)."
Total work is `authors × per_author`, each author triggering an independent bounded LMDB scan +
hydrate inside `latest_per_author` (`engine.rs:472-562`). A request with 1,000,000 authors and
`per_author=500` schedules up to 500M scan/hydrate operations on a single `spawn_blocking` task.
The doc comment flags this as "accepted v1 risk … for Phase-5 observability," but observability does
not bound the work — and with no body limit (WR-02) and a public bind (CR-01) this is exploitable,
not theoretical. At minimum it should be bounded together with the body limit.

**Fix:** Cap author count and return a client error past the ceiling (e.g. 1000), e.g.:
```rust
const MAX_AUTHORS: usize = 1000;
if authors.len() > MAX_AUTHORS {
    return Err(async_graphql::Error::new(format!(
        "too many authors: max {MAX_AUTHORS}"
    )).extend_with(|_, e| e.set("code", "TOO_MANY_AUTHORS")));
}
```

### WR-04: Negative `since`/`until` silently coerced to 0, changing query semantics

**File:** `src/graphql/resolvers.rs:208-209`
**Issue:** `f.since.map(|s| s.max(0) as u64)` and the matching `until` line clamp negatives to 0.
For `since` that is harmless (timestamps are ≥0). For `until`, a negative value means "before the
epoch," but `.max(0)` turns it into `until = 0`, which the engine interprets as "events at or before
timestamp 0" — i.e. effectively an empty result, not an error. The client gets a silently-wrong
empty page instead of feedback that the input was invalid. The cast also masks genuinely malformed
client input.

**Fix:** Reject negative timestamps explicitly rather than coercing:
```rust
fn nonneg_ts(v: Option<i64>, field: &str) -> GqlResult<Option<u64>> {
    match v {
        Some(x) if x < 0 => Err(async_graphql::Error::new(
            format!("{field} must be a non-negative Unix timestamp"))),
        Some(x) => Ok(Some(x as u64)),
        None => Ok(None),
    }
}
```

### WR-05: `latest_per_author` resolver retrieves `AppState` twice via `state_from(ctx)`

**File:** `src/graphql/resolvers.rs:130-131`
**Issue:** The resolver calls `state_from(ctx)` twice — once for `.env.clone()` and once for
`Arc::clone(&...dict_cache)`. Each call does a `ctx.data_unchecked::<AppState>()` lookup. This is
not a correctness bug, but it diverges from the `events`/`stats` resolvers which bind
`let state = ...` once, and `data_unchecked` panics if the type is absent — calling it twice doubles
the panic surface and obscures the single-source-of-state pattern used elsewhere. Inconsistent
pattern that invites future drift.

**Fix:**
```rust
let state = state_from(ctx);
let env = state.env.clone();
let dict_cache = Arc::clone(&state.dict_cache);
```

## Info

### IN-01: `kind` accepts negative values that wrap to huge u64 with no validation

**File:** `src/graphql/resolvers.rs:119, 135`
**Issue:** `kind: i64` is passed to the engine as `kind as u64`. A negative `kind` (e.g. `-1`) wraps
to `u64::MAX`, producing an empty result rather than a clear validation error. Not a crash and not
exploitable, but a confusing silent no-op for a malformed argument.
**Fix:** Validate `kind >= 0` and return a client error, or document that kinds are u64 and reject
negatives at the resolver boundary.

### IN-02: `max_lev_id` cast `u64 as i64` can produce a negative value at extreme counts

**File:** `src/graphql/resolvers.rs:282`, `src/graphql/types.rs:125`
**Issue:** `u64::from_ne_bytes(arr) as i64` will wrap to a negative number if a levId ever exceeds
`i64::MAX` (~9.2e18). Practically unreachable for strfry insertion counters, so informational only,
but worth a comment acknowledging the truncation point (the existing `// k is ... 8 bytes` comment
does not mention sign).
**Fix:** Add a comment noting the i64 ceiling, or saturate: `.try_into().unwrap_or(i64::MAX)`.

### IN-03: `try_into().unwrap_or([0u8; 8])` masks an impossible-but-silent corruption case

**File:** `src/graphql/resolvers.rs:281`
**Issue:** When reading the last EventPayload key, `k.try_into().unwrap_or([0u8; 8])` silently yields
`max_lev_id = 0` if the key is ever not exactly 8 bytes. For an IntegerComparator DB this cannot
happen, but the silent fallback would mask a genuine schema/format mismatch (exactly the class of
"silently-wrong" failure CLAUDE.md warns about). Prefer surfacing the anomaly.
**Fix:** Map a wrong-length key to a `QueryError`/log rather than silently returning 0.

### IN-04: GraphiQL playground and introspection enabled unconditionally

**File:** `src/server.rs:49-55`
**Issue:** `GET /graphql` always serves GraphiQL and introspection is on in all builds. For a
read-only adapter this is defensible, but it broadens the public attack/recon surface when paired
with CR-01. Consider gating GraphiQL behind a dev/debug config flag so production deployments serve
only `POST /graphql`.
**Fix:** Feature-flag or config-gate the GraphiQL `get(graphiql)` handler; mount only the POST
service when disabled.

---

_Reviewed: 2026-06-13_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

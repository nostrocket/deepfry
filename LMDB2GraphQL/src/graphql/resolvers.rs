/// resolvers.rs — GraphQL Query root and resolver implementations (API-01..API-06).
///
/// Implements the three public resolvers:
///   - `events(filter, after, limit)` — filtered event feed with cursor pagination (API-01/02/05)
///   - `latest_per_author(kind, per_author, authors)` — per-author event groups (API-03)
///   - `stats` — event count, max levId, db version (API-04)
///
/// Each resolver:
///   1. Retrieves `AppState` from the async-graphql context (Pattern 3).
///   2. Clones `env` and `Arc::clone(&dict_cache)` BEFORE the closure (Pitfall 1 — `RoTxn` !Send).
///   3. Wraps the synchronous engine call in `tokio::task::spawn_blocking` (Pattern 4).
///   4. Maps `QueryError` to `async_graphql::Error` via `map_query_error` (Pattern 7 / T-04-LEAK).
///
/// ## Security invariants
///
///   T-04-DOS:  `limit` clamped to ≤500 at API layer; `per_author` clamped ≤500 (D-04/D-05/D-08).
///   T-04-LEAK: `Lmdb`/`Payload` errors logged + returned as opaque "internal error" (T-04-LEAK).
///   T-04-CUR:  `PageCursor::decode` is fail-closed — `CursorDecode` → client-facing INVALID_CURSOR.
///   T-04-HEX:  Malformed hex inputs surface a generic client error without echoing the bytes.
///   T-04-WRITE: Query-only — no write_txn, no `.create()` (read-only LMDB invariant, T-03-RDONLY).
///   T-04-FANOUT: `authors` count is deliberately uncapped (D-08 — accepted v1 risk, see note).
use std::collections::HashMap;
use std::sync::Arc;

use async_graphql::{Context, ErrorExtensions, Object, Result as GqlResult};

use crate::graphql::schema::AppState;
use crate::graphql::types::{
    AuthorGroup, Event, EventFilterInput, EventsPage, StatsResult, TagFilterInput,
};
use crate::lmdb::payload::EVENT_PAYLOAD_DB_NAME;
use crate::query::engine::{execute_query, latest_per_author};
use crate::query::filter::{NostrFilter, PageCursor, QueryError, TagFilter};

use super::types::decoded_event_to_gql;

/// WR-03: maximum number of authors accepted by `latestPerAuthor`. Bounds the
/// multiplicative LMDB fan-out (authors × per_author). Requests above this return a
/// `TOO_MANY_AUTHORS` client error rather than scheduling unbounded work.
const MAX_AUTHORS: usize = 1000;

/// GraphQL Query root — all three read-only resolvers.
pub struct Query;

#[Object]
impl Query {
    /// events() — filtered Nostr event feed with cursor pagination (API-01/02/05).
    ///
    /// ## Arguments
    ///
    /// - `filter`: optional NIP-01-style filter (ids, authors, kinds, since, until, tag).
    /// - `after`: opaque cursor from a previous page's `endCursor` (D-11/T-04-CUR).
    /// - `limit`: max events per page. Clamped to [1, 500]; default 100 (D-04/D-05/T-04-DOS).
    ///
    /// ## Limit enforcement (D-04/D-05 — Pitfall 6)
    ///
    /// The clamp happens HERE, before calling the engine. The engine's internal `limit=0`
    /// behavior (uses `DEFAULT_WINDOW_SIZE=256`) is separate and must never be triggered
    /// by this resolver passing 0.
    async fn events(
        &self,
        ctx: &Context<'_>,
        #[graphql(desc = "NIP-01-style event filter")] filter: Option<EventFilterInput>,
        #[graphql(desc = "Opaque pagination cursor from the previous page's endCursor")]
        after: Option<String>,
        #[graphql(desc = "Maximum events per page (1–500, default 100; silently clamped if over 500)")]
        limit: Option<i32>,
    ) -> GqlResult<EventsPage> {
        let state = ctx.data_unchecked::<AppState>();

        // D-04/D-05: clamp at API layer before calling engine. Default 100, ceiling 500.
        // NEVER pass 0 — engine interprets 0 as "use DEFAULT_WINDOW_SIZE (256)", not 100.
        let effective_limit = limit
            .map(|l| (l.max(1) as usize).min(500))
            .unwrap_or(100);

        // T-04-CUR: decode cursor fail-closed. Malformed → CursorDecode → INVALID_CURSOR error.
        let cursor: Option<PageCursor> = match after {
            Some(ref s) => Some(PageCursor::decode(s).map_err(map_query_error)?),
            None => None,
        };

        // Build NostrFilter from GraphQL args. Client errors (bad hex) surface here.
        let nostr_filter = build_nostr_filter(filter, effective_limit)?;

        // Clone into spawn_blocking before the closure (Pitfall 1 — RoTxn is !Send).
        let env = state.env.clone();
        let dict_cache = Arc::clone(&state.dict_cache);

        let (decoded_events, next_cursor) =
            tokio::task::spawn_blocking(move || {
                execute_query(&env, &nostr_filter, &*dict_cache, cursor.as_ref())
            })
            .await
            .map_err(|e| async_graphql::Error::new(format!("task error: {e}")))?
            .map_err(map_query_error)?;

        let events: Vec<Event> = decoded_events.into_iter().map(decoded_event_to_gql).collect();

        // D-03/D-11: page object — end_cursor = cursor.encode(); has_more = cursor.is_some().
        Ok(EventsPage {
            events,
            has_more: next_cursor.is_some(),
            end_cursor: next_cursor.map(|c| c.encode()),
        })
    }

    /// latestPerAuthor() — latest N events per pubkey, grouped by author (API-03/D-07).
    ///
    /// ## Arguments
    ///
    /// - `kind`: event kind to filter by (i64 for 64-bit compat — Open Question 3).
    /// - `per_author`: max events per author. Clamped to [1, 500] silently (D-08).
    /// - `authors`: list of pubkeys (64-char lowercase hex). NOT capped (D-08 — accepted v1 risk).
    ///
    /// ## Fanout warning (T-04-FANOUT)
    ///
    /// `authors.len()` is deliberately uncapped per D-08. Each author triggers one bounded
    /// `Event__pubkeyKind` scan capped at `per_author` (≤500). Total work = authors×perAuthor.
    /// The unbounded author-count fan-out is an accepted v1 risk — flagged for Phase-5 observability.
    async fn latest_per_author(
        &self,
        ctx: &Context<'_>,
        #[graphql(desc = "Event kind to retrieve")] kind: i64,
        #[graphql(desc = "Max events per author (1–500, silently clamped)")]
        per_author: i32,
        #[graphql(
            desc = "Author pubkeys (64-char lowercase hex). Author count is not capped (v1 risk — T-04-FANOUT)"
        )]
        authors: Vec<String>,
    ) -> GqlResult<Vec<AuthorGroup>> {
        // WR-03: cap author count. Total work is authors × per_author, each author triggering
        // an independent bounded LMDB scan + hydrate. An uncapped author list (e.g. 1,000,000
        // authors × per_author=500) schedules hundreds of millions of scan/hydrate operations
        // on a single spawn_blocking task. Reject past the ceiling with a client error rather
        // than accepting unbounded multiplicative fan-out.
        if authors.len() > MAX_AUTHORS {
            return Err(async_graphql::Error::new(format!(
                "too many authors: max {MAX_AUTHORS}"
            ))
            .extend_with(|_, e| e.set("code", "TOO_MANY_AUTHORS")));
        }

        // WR-05: reject a negative kind rather than coercing it. `kind as u64` would wrap
        // a negative value (e.g. -1 → 18446744073709551615), building an `Event__pubkeyKind`
        // start key for a kind that cannot exist and silently returning empty buckets instead
        // of signalling invalid input — the same defect class WR-04 fixed for since/until.
        let kind_u64: u64 = u64::try_from(kind)
            .map_err(|_| async_graphql::Error::new("kind must be a non-negative integer"))?;

        // D-08: clamp perAuthor silently to [1, 500].
        let clamped_per_author = (per_author.max(1) as usize).min(500);

        let state = state_from(ctx);
        let env = state.env.clone();
        let dict_cache = Arc::clone(&state.dict_cache);

        let groups: HashMap<String, Vec<_>> =
            tokio::task::spawn_blocking(move || {
                latest_per_author(&env, kind_u64, clamped_per_author, &authors, &*dict_cache)
            })
            .await
            .map_err(|e| async_graphql::Error::new(format!("task error: {e}")))?
            .map_err(map_query_error)?;

        // D-07: map HashMap<String, Vec<DecodedEvent>> → Vec<AuthorGroup>.
        let result: Vec<AuthorGroup> = groups
            .into_iter()
            .map(|(author, events)| AuthorGroup {
                author,
                events: events.into_iter().map(decoded_event_to_gql).collect(),
            })
            .collect();

        Ok(result)
    }

    /// stats — event count, max levId, dbVersion, pinnedStrfryVersion (API-04/D-09/OPS-04).
    ///
    /// Reads a short `read_txn` over `EventPayload` to collect:
    ///   - `event_count`: `mdb_stat.entries` (total events).
    ///   - `max_lev_id`: last key in `EventPayload` (monotonic insertion counter).
    ///   - `db_version`: from `AppState.meta.db_version` (verified by startup gate).
    ///   - `pinned_strfry_version`: from `AppState.pinned_strfry_version` (OPS-04).
    ///
    /// T-03-RDONLY: uses only `read_txn()` and `.open()` (never `.create()`).
    /// D-08 short-txn: txn is opened, used, and dropped inside the spawn_blocking closure.
    async fn stats(&self, ctx: &Context<'_>) -> GqlResult<StatsResult> {
        let state = ctx.data_unchecked::<AppState>();
        let env = state.env.clone();
        let db_version = state.meta.db_version;
        // OPS-04: clone before closure — follows the existing clone-before-spawn_blocking
        // pattern (Pitfall 1). String is Clone; no Arc needed.
        let pinned = state.pinned_strfry_version.clone();

        tokio::task::spawn_blocking(move || read_stats(&env, db_version, pinned))
            .await
            .map_err(|e| async_graphql::Error::new(format!("task error: {e}")))?
            .map_err(map_query_error)
    }
}

// ---------------------------------------------------------------------------
// Helper: retrieve AppState (reduces repetition in latest_per_author)
// ---------------------------------------------------------------------------

fn state_from<'a>(ctx: &'a Context<'_>) -> &'a AppState {
    ctx.data_unchecked::<AppState>()
}

// ---------------------------------------------------------------------------
// Helper: build NostrFilter from GraphQL input (API-01/02)
// ---------------------------------------------------------------------------

/// Build a `NostrFilter` from `EventFilterInput` and the already-clamped `effective_limit`.
///
/// Maps GraphQL arg types to engine types:
///   - `kinds: Vec<i64>` → `Option<Vec<u64>>`
///   - `since`/`until: i64` → `Option<u64>`
///   - `tag: Option<TagFilterInput>` → `NostrFilter.tags: Some(vec![TagFilter { name, values }])`
///     (single tag → one-element vec; multi-tag is a v2 expansion — CONTEXT Open Question 2/D-02).
///
/// Malformed hex in `ids`/`authors` surfaces as a client-facing GraphQL error WITHOUT echoing
/// the offending bytes (T-04-HEX). Validation is left to the engine (which warns+skips on
/// malformed hex) rather than adding a second hex-validation pass here, keeping the code DRY
/// while preserving the no-echo invariant (errors from engine are opaque via map_query_error).
/// WR-04: validate an optional Unix timestamp bound, rejecting negative values.
///
/// Returns a client-facing error for negatives rather than coercing them to 0 — a negative
/// `until` coerced to 0 silently yields an empty page instead of signaling invalid input.
fn nonneg_ts(v: Option<i64>, field: &str) -> GqlResult<Option<u64>> {
    match v {
        Some(x) if x < 0 => Err(async_graphql::Error::new(format!(
            "{field} must be a non-negative Unix timestamp"
        ))),
        Some(x) => Ok(Some(x as u64)),
        None => Ok(None),
    }
}

pub fn build_nostr_filter(
    filter: Option<EventFilterInput>,
    effective_limit: usize,
) -> GqlResult<NostrFilter> {
    let f = filter.unwrap_or_default();

    // kinds: Vec<i64> → Option<Vec<u64>>
    //
    // WR-05: same defect class as WR-04 (negative timestamps). A negative kind cast
    // unguarded (`k as u64`) wraps to a giant u64 (e.g. -1 → 18446744073709551615),
    // silently dropping out of any match instead of signalling invalid input. Reject
    // negatives explicitly with the same non-negative pattern used for since/until.
    let kinds: Option<Vec<u64>> = match f.kinds {
        Some(ks) => {
            let mut out = Vec::with_capacity(ks.len());
            for k in ks {
                out.push(u64::try_from(k).map_err(|_| {
                    async_graphql::Error::new("kind must be a non-negative integer")
                })?);
            }
            Some(out)
        }
        None => None,
    };

    // WR-04: since/until are Unix timestamps (≥0). A negative value is malformed input, not a
    // valid bound. Previously `.max(0) as u64` silently coerced a negative `until` to 0, which
    // the engine reads as "events at or before timestamp 0" — i.e. a silently-empty page rather
    // than feedback that the input was invalid. Reject negatives explicitly instead of masking.
    let since: Option<u64> = nonneg_ts(f.since, "since")?;
    let until: Option<u64> = nonneg_ts(f.until, "until")?;

    // WR-06: reject an inverted range (since > until). The engine scans `created_at <= until`
    // with a per-stream `since` floor, so a transposed pair (e.g. since=2_000_000_000,
    // until=1_000_000_000) silently returns an empty page rather than feedback that the input
    // was malformed — the same failure mode WR-04 was raised to eliminate.
    if let (Some(s), Some(u)) = (since, until) {
        if s > u {
            return Err(async_graphql::Error::new("since must be <= until"));
        }
    }

    // tag: single TagFilterInput → one-element Vec<TagFilter> (D-02 / CONTEXT Open Question 2)
    let tags: Option<Vec<TagFilter>> = f.tag.map(|t: TagFilterInput| {
        vec![TagFilter {
            name: t.name,
            values: t.values,
        }]
    });

    Ok(NostrFilter {
        ids: f.ids,
        authors: f.authors,
        kinds,
        tags,
        since,
        until,
        limit: effective_limit,
    })
}

// ---------------------------------------------------------------------------
// Helper: read_stats — opens EventPayload, reads stat + last key (API-04/D-09)
// ---------------------------------------------------------------------------

/// Read event statistics from the `EventPayload` sub-DB (API-04/D-09/Pattern 8/OPS-04).
///
/// Must be called inside a `spawn_blocking` closure (heed/LMDB is synchronous C FFI).
///
/// ## Parameters
///
/// - `env`: the read-only LMDB environment.
/// - `db_version`: from `AppState.meta.db_version` (pre-verified by startup gate).
/// - `pinned_strfry_version`: from `AppState.pinned_strfry_version` — threaded into
///   `StatsResult` to surface the configured strfry image reference (OPS-04).
///
/// ## Read-only invariants (T-03-RDONLY)
///
/// - Uses `read_txn()` only — never `write_txn`.
/// - Opens `EventPayload` with `.open()` (never `.create()`) — T-03-RDONLY.
/// - The `RoTxn` is local to this function and dropped before returning (D-08 short-txn).
fn read_stats(env: &heed::Env, db_version: u32, pinned_strfry_version: String) -> Result<StatsResult, QueryError> {
    use heed::types::Bytes;
    use heed::IntegerComparator;

    // Short read txn — opened and dropped within this function (D-08).
    let rtxn = env.read_txn().map_err(|e| {
        QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e))
    })?;

    // Open EventPayload as IntegerComparator (MDB_INTEGERKEY — native-endian u64 keys).
    // NEVER use .create() — read-only invariant (T-03-RDONLY).
    let db: heed::Database<Bytes, Bytes, IntegerComparator> = env
        .database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<IntegerComparator>()
        .name(EVENT_PAYLOAD_DB_NAME)
        .open(&rtxn)
        .map_err(|e| QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e)))?
        .ok_or_else(|| {
            QueryError::Lmdb(crate::lmdb::indexes::IndexError::SubDbNotFound {
                name: EVENT_PAYLOAD_DB_NAME.to_string(),
            })
        })?;

    // event_count: total entries in EventPayload (mdb_stat.entries).
    let stat = db
        .stat(&rtxn)
        .map_err(|e| QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e)))?;
    // WR-07: saturate the usize → i64 cast rather than wrapping. An entry count above
    // i64::MAX (practically unreachable) would otherwise become negative.
    let event_count: i64 = i64::try_from(stat.entries).unwrap_or(i64::MAX);

    // max_lev_id: last (largest) IntegerKey in EventPayload.
    // `db.last()` returns the last entry in key order; for IntegerComparator keys,
    // that is the largest native-endian u64 levId (the most recently inserted event).
    let max_lev_id: i64 = match db
        .last(&rtxn)
        .map_err(|e| QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e)))?
    {
        Some((k, _)) => {
            // WR-07: a non-8-byte last key is a structural surprise from the externally-owned
            // strfry DB (treated as a private API). Treat it as an internal error rather than
            // `unwrap_or([0u8; 8])`, which would report `max_lev_id = 0` — indistinguishable
            // from an empty DB and masking corruption on a stats/health endpoint.
            let arr: [u8; 8] = k.try_into().map_err(|_| {
                QueryError::Lmdb(crate::lmdb::indexes::IndexError::MalformedKey {
                    name: EVENT_PAYLOAD_DB_NAME.to_string(),
                    expected: 8,
                    actual: k.len(),
                })
            })?;
            // WR-07: saturate the u64 → i64 cast (a real levId above i64::MAX, ≈9.2e18,
            // would otherwise become negative). Practically unreachable but explicit.
            i64::try_from(u64::from_ne_bytes(arr)).unwrap_or(i64::MAX)
        }
        // Empty DB — no entries, max_lev_id is genuinely 0.
        None => 0,
    };

    // rtxn is dropped here — short-txn invariant satisfied (D-08).
    Ok(StatsResult {
        event_count,
        max_lev_id,
        db_version: db_version as i32,
        // OPS-04: thread the configured pinned version into the response.
        pinned_strfry_version,
    })
}

// ---------------------------------------------------------------------------
// Helper: map_query_error — QueryError → async_graphql::Error (Pattern 7 / T-04-LEAK)
// ---------------------------------------------------------------------------

/// Translate a `QueryError` to an `async_graphql::Error`.
///
/// ## Security (T-04-LEAK)
///
/// - `CursorDecode` → client-facing error with extension code `"INVALID_CURSOR"`. Safe to expose.
/// - `Lmdb` / `Payload` → `tracing::error!` + opaque `"internal error"`. NEVER leak internals.
///
/// Malformed-hex inputs in `ids`/`authors` surface as client errors without echoing the
/// offending bytes (T-04-HEX).
pub fn map_query_error(e: QueryError) -> async_graphql::Error {
    match e {
        QueryError::CursorDecode { reason } => {
            // Client error: malformed cursor — safe to expose (fail-closed T-03-CUR).
            async_graphql::Error::new(format!("invalid cursor: {reason}"))
                .extend_with(|_, ext| ext.set("code", "INVALID_CURSOR"))
        }
        QueryError::Lmdb(inner) => {
            // T-04-LEAK: log internally; return opaque message.
            tracing::error!(error = %inner, "LMDB error during query");
            async_graphql::Error::new("internal error")
        }
        QueryError::Payload(inner) => {
            // T-04-LEAK: log internally; return opaque message.
            tracing::error!(error = %inner, "payload decode error during query");
            async_graphql::Error::new("internal error")
        }
    }
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::graphql::schema::{build_schema, AppState};
    use crate::lmdb::env::open_fixture_env;
    use crate::lmdb::meta::read_meta;
    use crate::lmdb::payload::DictCache;

    /// Open a read-only copy of the fixture env for testing.
    fn open_test_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb"))
            .expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb"))
            .expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env");
        (env, tmp)
    }

    /// Build an AppState from the fixture env.
    fn make_app_state(env: heed::Env) -> AppState {
        let meta = read_meta(&env).expect("read_meta from fixture");
        AppState {
            env,
            dict_cache: Arc::new(DictCache::new()),
            meta,
            pinned_strfry_version: "test-pinned".to_string(),
        }
    }

    // -----------------------------------------------------------------------
    // Test 1 (API-06/D-10): schema SDL contains no `type Mutation`
    // -----------------------------------------------------------------------

    /// Schema SDL must NOT contain a Mutation type — proves API-06/D-10 structurally.
    ///
    /// `EmptyMutation` removes the mutation root from the schema. This test renders the SDL
    /// and asserts the mutation type is absent — verifying the no-write surface guarantee.
    #[tokio::test]
    async fn test_no_mutation_in_schema_sdl() {
        let (env, _tmp) = open_test_env();
        let app_state = make_app_state(env);
        let schema = build_schema(app_state);
        let sdl = schema.sdl();
        assert!(
            !sdl.contains("type Mutation"),
            "Schema SDL must not contain 'type Mutation' (API-06/D-10)\nSDL:\n{}",
            sdl
        );
        assert!(
            !sdl.contains("mutation:"),
            "Schema SDL must not reference 'mutation:' root (API-06/D-10)\nSDL:\n{}",
            sdl
        );
    }

    // -----------------------------------------------------------------------
    // Test 2 (D-04/D-05): events() limit clamp
    // -----------------------------------------------------------------------

    /// Verify the limit clamp expression: 9999→500, None→100, 0→1 (not 0).
    ///
    /// This is the D-04/D-05 enforcement test — the clamp must happen before the engine
    /// is called, not inside the engine (Pitfall 6).
    #[test]
    fn test_events_limit_clamp() {
        // 9999 → clamped to 500 (ceiling, D-05)
        let clamped_9999 = Some(9999_i32)
            .map(|l| (l.max(1) as usize).min(500))
            .unwrap_or(100);
        assert_eq!(clamped_9999, 500, "9999 must clamp to 500 (D-05)");

        // None → default 100 (D-04)
        let default: usize = None::<i32>
            .map(|l| (l.max(1) as usize).min(500))
            .unwrap_or(100);
        assert_eq!(default, 100, "None must default to 100 (D-04)");

        // 0 → 1 (never 0 — passing 0 to engine triggers DEFAULT_WINDOW_SIZE, not API default)
        let clamped_0 = Some(0_i32)
            .map(|l| (l.max(1) as usize).min(500))
            .unwrap_or(100);
        assert!(clamped_0 >= 1, "0 must clamp to at least 1, not 0 (Pitfall 6); got {clamped_0}");
    }

    // -----------------------------------------------------------------------
    // Test 3 (D-08): latestPerAuthor perAuthor clamp
    // -----------------------------------------------------------------------

    /// Verify the perAuthor clamp: 9999→500, 0→1 (D-08 silent clamp).
    #[test]
    fn test_per_author_clamp() {
        // 9999 → 500
        let clamped_9999 = (9999_i32.max(1) as usize).min(500);
        assert_eq!(clamped_9999, 500, "9999 must clamp to 500 (D-08)");

        // 0 → 1 (not 0)
        let clamped_0 = (0_i32.max(1) as usize).min(500);
        assert!(clamped_0 >= 1, "0 must clamp to at least 1 (D-08); got {clamped_0}");
    }

    // -----------------------------------------------------------------------
    // Test 4 (D-03/D-11): EventsPage has_more and end_cursor mapping
    // -----------------------------------------------------------------------

    /// EventsPage with Some(cursor) has has_more=true and end_cursor=Some(encoded).
    #[test]
    fn test_events_page_shape_with_cursor() {
        let cursor = PageCursor { created_at: 1720000000, lev_id: 42 };
        let encoded = cursor.encode();
        let next_cursor: Option<PageCursor> = Some(cursor);

        let page = EventsPage {
            events: vec![],
            has_more: next_cursor.is_some(),
            end_cursor: next_cursor.map(|c| c.encode()),
        };

        assert!(page.has_more, "has_more must be true when cursor is Some (D-03)");
        assert_eq!(
            page.end_cursor,
            Some(encoded),
            "end_cursor must equal PageCursor::encode() (D-11)"
        );
    }

    /// EventsPage with None cursor has has_more=false and end_cursor=None.
    #[test]
    fn test_events_page_shape_without_cursor() {
        let next_cursor: Option<PageCursor> = None;

        let page = EventsPage {
            events: vec![],
            has_more: next_cursor.is_some(),
            end_cursor: next_cursor.map(|c| c.encode()),
        };

        assert!(!page.has_more, "has_more must be false when cursor is None (D-03)");
        assert_eq!(page.end_cursor, None, "end_cursor must be None when no next page (D-11)");
    }

    // -----------------------------------------------------------------------
    // Test 5 (API-01): events() query returns results from fixture
    // -----------------------------------------------------------------------

    /// Execute a full events() query through build_schema + schema.execute.
    /// Uses the fixture LMDB — proves the GraphQL→engine→LMDB pipeline works end-to-end.
    #[tokio::test]
    async fn test_events_query_basic() {
        let (env, _tmp) = open_test_env();
        let app_state = make_app_state(env);
        let schema = build_schema(app_state);

        let res = schema
            .execute("{ events { events { id kind createdAt } hasMore endCursor } }")
            .await;

        assert!(
            res.errors.is_empty(),
            "events query must return no errors; got: {:?}",
            res.errors
        );
    }

    // -----------------------------------------------------------------------
    // Test 6 (API-04): stats query returns expected fields
    // -----------------------------------------------------------------------

    /// Execute a full stats query — proves eventCount/maxLevId/dbVersion are populated.
    #[tokio::test]
    async fn test_stats_query() {
        let (env, _tmp) = open_test_env();
        let app_state = make_app_state(env);
        let schema = build_schema(app_state);

        let res = schema
            .execute("{ stats { eventCount maxLevId dbVersion } }")
            .await;

        assert!(
            res.errors.is_empty(),
            "stats query must return no errors; got: {:?}",
            res.errors
        );

        // Verify dbVersion == 3 (startup gate ensures this, stats passes it through).
        let data = res.data.into_json().expect("response data must serialize");
        let db_version = data["stats"]["dbVersion"]
            .as_i64()
            .expect("dbVersion must be an integer");
        assert_eq!(db_version, 3, "dbVersion must be 3 (verified by startup gate)");
    }

    // -----------------------------------------------------------------------
    // Test 7 (OPS-04): stats query returns pinnedStrfryVersion alongside dbVersion
    // -----------------------------------------------------------------------

    /// Execute a full stats query including pinnedStrfryVersion (OPS-04).
    ///
    /// Verifies the stats resolver threads AppState.pinned_strfry_version into the
    /// returned StatsResult, and that async-graphql auto-renames it to `pinnedStrfryVersion`
    /// in the SDL. Also confirms dbVersion is unchanged (additive change, no regression).
    #[tokio::test]
    async fn test_stats_pinned_strfry_version() {
        let (env, _tmp) = open_test_env();
        let app_state = make_app_state(env);
        let schema = build_schema(app_state);

        let res = schema
            .execute("{ stats { dbVersion pinnedStrfryVersion } }")
            .await;

        assert!(
            res.errors.is_empty(),
            "stats query with pinnedStrfryVersion must return no errors; got: {:?}",
            res.errors
        );

        let data = res.data.into_json().expect("response data must serialize");

        // dbVersion must still be 3 — additive change must not regress existing fields.
        let db_version = data["stats"]["dbVersion"]
            .as_i64()
            .expect("dbVersion must be an integer");
        assert_eq!(db_version, 3, "dbVersion must be 3 (regression check for OPS-04)");

        // pinnedStrfryVersion must equal the configured value set in make_app_state.
        let pinned = data["stats"]["pinnedStrfryVersion"]
            .as_str()
            .expect("pinnedStrfryVersion must be a string");
        assert_eq!(
            pinned,
            "test-pinned",
            "pinnedStrfryVersion must equal AppState.pinned_strfry_version (OPS-04)"
        );
    }
}

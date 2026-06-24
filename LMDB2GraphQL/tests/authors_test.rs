//! Integration tests for the `authors(after, limit)` GraphQL query (API-07).
//!
//! Tests the full GraphQL→resolver→engine→LMDB pipeline against the committed
//! fixture database. The fixture has exactly two distinct pubkeys:
//!
//!   PK1 = 79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798
//!   PK2 = c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5
//!
//! in that byte-ascending order.
//!
//! ## Coverage
//!
//! 1. Full page: `{ authors { authors hasMore endCursor } }` returns [PK1, PK2], hasMore=false,
//!    endCursor=null.
//! 2. Single page (limit=1): returns [PK1], hasMore=true, endCursor=hex(PK1).
//! 3. Multi-page walk: feeding endCursor back as `after` yields [PK1, PK2] exactly once,
//!    terminates with hasMore=false, endCursor=null.
//! 4. Fail-closed bad cursor: three distinct malformed `after` inputs each return a non-empty
//!    errors array with NO echo of the offending bytes (T-07-CUR).
//! 5. No-mutation regression: schema SDL has no `type Mutation` (API-06 preserved).

use lmdb2graphql::graphql::schema::{build_schema, AppState};
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::meta::read_meta;
use lmdb2graphql::lmdb::payload::DictCache;
use std::sync::Arc;

// -----------------------------------------------------------------------
// Fixture helpers (mirrors resolvers.rs and cors_test.rs patterns)
// -----------------------------------------------------------------------

/// Copy the committed fixture to a temp dir and open a read-only env there.
///
/// We copy rather than open in-place so tests run in parallel without
/// interfering with each other or with the committed fixture bytes.
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb"))
        .expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb"))
        .expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}

/// Build an AppState from the fixture env (matches resolvers.rs make_app_state).
fn make_app_state(env: heed::Env) -> AppState {
    let meta = read_meta(&env).expect("read_meta from fixture");
    AppState {
        env,
        dict_cache: Arc::new(DictCache::new()),
        meta,
        pinned_strfry_version: "test-pinned".to_string(),
    }
}

// Fixture pubkey constants (byte-ascending order).
const PK1: &str = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
const PK2: &str = "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5";

// -----------------------------------------------------------------------
// Test 1: Full page — { authors { authors hasMore endCursor } }
// -----------------------------------------------------------------------

/// A full `authors` query with no args returns [PK1, PK2] in byte-ascending order,
/// hasMore=false, and endCursor=null.
///
/// Verifies: correct distinct-pubkey set, correct order, clean termination.
#[tokio::test]
async fn test_authors_full_page() {
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));

    let res = schema
        .execute("{ authors { authors hasMore endCursor } }")
        .await;

    assert!(
        res.errors.is_empty(),
        "authors query must return no errors; got: {:?}",
        res.errors
    );

    let data = res.data.into_json().expect("response data must serialize");

    let authors = data["authors"]["authors"]
        .as_array()
        .expect("authors must be an array");
    assert_eq!(
        authors.len(),
        2,
        "fixture has exactly 2 distinct pubkeys; got {}",
        authors.len()
    );
    assert_eq!(
        authors[0].as_str().unwrap(),
        PK1,
        "first pubkey must be PK1 (byte-ascending)"
    );
    assert_eq!(
        authors[1].as_str().unwrap(),
        PK2,
        "second pubkey must be PK2"
    );

    let has_more = data["authors"]["hasMore"].as_bool().unwrap();
    assert!(!has_more, "hasMore must be false for a complete single-page result");

    let end_cursor = &data["authors"]["endCursor"];
    assert!(
        end_cursor.is_null(),
        "endCursor must be null at end of stream; got: {end_cursor}"
    );
}

// -----------------------------------------------------------------------
// Test 2: Single page with limit=1 — returns PK1, hasMore=true, endCursor=PK1
// -----------------------------------------------------------------------

/// `authors(limit: 1)` returns exactly PK1, hasMore=true, and endCursor=hex(PK1).
///
/// Verifies: limit enforcement, correct cursor value for next-page navigation.
#[tokio::test]
async fn test_authors_limit_1_returns_pk1_with_cursor() {
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));

    let res = schema
        .execute("{ authors(limit: 1) { authors hasMore endCursor } }")
        .await;

    assert!(
        res.errors.is_empty(),
        "authors(limit: 1) must return no errors; got: {:?}",
        res.errors
    );

    let data = res.data.into_json().expect("response data must serialize");

    let authors = data["authors"]["authors"]
        .as_array()
        .expect("authors must be an array");
    assert_eq!(authors.len(), 1, "limit=1 must return exactly 1 pubkey");
    assert_eq!(
        authors[0].as_str().unwrap(),
        PK1,
        "limit=1 must return PK1 (the first byte-ascending pubkey)"
    );

    let has_more = data["authors"]["hasMore"].as_bool().unwrap();
    assert!(has_more, "hasMore must be true when there are more authors to paginate");

    let end_cursor = data["authors"]["endCursor"].as_str().expect("endCursor must be a string");
    assert_eq!(
        end_cursor,
        PK1,
        "endCursor must equal PK1 (the last pubkey returned)"
    );
}

// -----------------------------------------------------------------------
// Test 3: Multi-page walk — feeding endCursor back as `after`
// -----------------------------------------------------------------------

/// Iterating with limit=1 and feeding `endCursor` back as `after` collects [PK1, PK2]
/// exactly once, then terminates with hasMore=false and endCursor=null.
///
/// Verifies: pagination correctness, no repetitions, clean end-of-stream.
#[tokio::test]
async fn test_authors_multi_page_walk() {
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));

    let mut collected: Vec<String> = Vec::new();
    let mut cursor: Option<String> = None;
    let mut pages = 0usize;

    loop {
        // Build query: inject `after` argument when cursor is set.
        let query = match &cursor {
            None => "{ authors(limit: 1) { authors hasMore endCursor } }".to_string(),
            Some(c) => format!(
                r#"{{ authors(limit: 1, after: "{c}") {{ authors hasMore endCursor }} }}"#
            ),
        };

        let res = schema.execute(query.as_str()).await;
        assert!(
            res.errors.is_empty(),
            "multi-page walk page {pages} must return no errors; got: {:?}",
            res.errors
        );

        let data = res.data.into_json().expect("response data must serialize");

        let page_authors = data["authors"]["authors"]
            .as_array()
            .expect("authors must be an array")
            .iter()
            .map(|v| v.as_str().unwrap().to_string())
            .collect::<Vec<_>>();
        collected.extend_from_slice(&page_authors);

        let has_more = data["authors"]["hasMore"].as_bool().unwrap();
        let next_cursor = data["authors"]["endCursor"].as_str().map(|s| s.to_string());

        pages += 1;
        assert!(
            pages <= 10,
            "multi-page walk must terminate within 10 pages (infinite loop guard)"
        );

        if has_more {
            cursor = next_cursor;
        } else {
            // End of stream: hasMore=false, endCursor=null.
            assert!(
                next_cursor.is_none(),
                "endCursor must be null when hasMore=false; got: {:?}",
                next_cursor
            );
            break;
        }
    }

    assert_eq!(
        collected.len(),
        2,
        "multi-page walk must collect exactly 2 distinct pubkeys; got {}",
        collected.len()
    );
    assert_eq!(
        collected[0], PK1,
        "first collected pubkey must be PK1"
    );
    assert_eq!(
        collected[1], PK2,
        "second collected pubkey must be PK2"
    );
}

// -----------------------------------------------------------------------
// Test 4: Fail-closed bad cursor — three distinct malformed `after` inputs
// -----------------------------------------------------------------------

/// A non-hex `after` cursor returns a non-empty errors array with NO echo of the
/// offending bytes.
///
/// Tests input (a): `"zzzz"` — non-hex characters.
/// This exercises the hex-decode rejection gate (step 1 of the two-step validation).
#[tokio::test]
async fn test_authors_bad_cursor_nonhex() {
    let bad_input = "zzzz";
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));

    let query = format!(r#"{{ authors(after: "{bad_input}") {{ authors hasMore endCursor }} }}"#);
    let res = schema.execute(query.as_str()).await;

    assert!(
        !res.errors.is_empty(),
        "non-hex after cursor must produce a non-empty errors array"
    );
    for err in &res.errors {
        let msg = err.message.as_str();
        assert!(
            !msg.contains(bad_input),
            "error message must NOT echo the offending input '{bad_input}'; got: {msg}"
        );
    }
}

/// An even-length but too-short `after` cursor returns a non-empty errors array with
/// NO echo of the offending bytes.
///
/// Tests input (b): `"79be"` — 4 chars → 2 bytes (too short for a 32-byte pubkey).
/// This exercises the hex-decode rejection gate (odd-length check passes, but length
/// is clearly wrong — here tested as a sub-32-byte case).
#[tokio::test]
async fn test_authors_bad_cursor_short() {
    let bad_input = "79be";
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));

    let query = format!(r#"{{ authors(after: "{bad_input}") {{ authors hasMore endCursor }} }}"#);
    let res = schema.execute(query.as_str()).await;

    assert!(
        !res.errors.is_empty(),
        "too-short after cursor must produce a non-empty errors array"
    );
    for err in &res.errors {
        let msg = err.message.as_str();
        assert!(
            !msg.contains(bad_input),
            "error message must NOT echo the offending input '{bad_input}'; got: {msg}"
        );
    }
}

/// An EVEN-length but NOT-64-char `after` cursor returns a non-empty errors array with
/// NO echo of the offending bytes.
///
/// Tests input (c): `"79be66"` — 6 chars → 3 bytes — even length, valid hex chars,
/// but NOT 32 bytes. This is the critical test: `decode_hex` alone would ACCEPT this
/// input (even length, valid hex). Only the `try_into::<[u8; 32]>()` 32-byte gate
/// rejects it. Verifies that the resolver enforces the full two-step validation
/// (hex-decode AND byte-length).
#[tokio::test]
async fn test_authors_bad_cursor_even_but_not_32_bytes() {
    let bad_input = "79be66"; // 6 chars → 3 bytes — even length valid hex, but NOT 32 bytes
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));

    let query = format!(r#"{{ authors(after: "{bad_input}") {{ authors hasMore endCursor }} }}"#);
    let res = schema.execute(query.as_str()).await;

    assert!(
        !res.errors.is_empty(),
        "even-but-not-64-char after cursor must produce a non-empty errors array \
         (proves the try_into::<[u8;32]> 32-byte gate, not just the hex-decode gate)"
    );
    for err in &res.errors {
        let msg = err.message.as_str();
        assert!(
            !msg.contains(bad_input),
            "error message must NOT echo the offending input '{bad_input}'; got: {msg}"
        );
    }
}

// -----------------------------------------------------------------------
// Test 5: No-mutation regression — schema SDL has no `type Mutation`
// -----------------------------------------------------------------------

/// The schema SDL must NOT contain a `type Mutation` — authors() is read-only and
/// adding it must not accidentally enable a mutation surface (API-06 preserved).
///
/// Mirrors `test_no_mutation_in_schema_sdl` in resolvers.rs unit tests.
#[tokio::test]
async fn test_authors_no_mutation_in_schema_sdl() {
    let (env, _tmp) = open_temp_fixture_env();
    let schema = build_schema(make_app_state(env));
    let sdl = schema.sdl();

    assert!(
        !sdl.contains("type Mutation"),
        "Schema SDL must not contain 'type Mutation' after adding authors() (API-06)\nSDL:\n{}",
        sdl
    );
    assert!(
        !sdl.contains("mutation:"),
        "Schema SDL must not reference 'mutation:' root after adding authors() (API-06)\nSDL:\n{}",
        sdl
    );

    // Additionally verify the authors field IS present in the SDL.
    assert!(
        sdl.contains("authors"),
        "Schema SDL must expose the 'authors' query field; got:\n{}",
        sdl
    );
}

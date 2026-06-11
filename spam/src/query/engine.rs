/// engine.rs — Public query engine API (QRY-01/02/03/05, D-07/D-08/D-10/D-11/D-12).
///
/// Composes router + merge + hydrate into two public entry points:
///
/// - [`execute_query`]: routes a `NostrFilter` to the selected index, merges per-prefix reverse
///   scans, over-fetches + hydrates + drops NIP-40-expired events until `limit` valid events are
///   collected (D-07), returns them in `(created_at DESC, lev_id DESC)` order (D-10) plus an
///   opaque resume cursor (D-11).
///
/// - [`latest_per_author`]: returns ≤ `per_author` newest events per requested pubkey, grouped
///   by pubkey, each bucket `created_at DESC` (QRY-03 / D-12).
///
/// ## Invariants
///
/// - Read-only: no `.create()`, no `write_txn` (T-03-RDONLY).
/// - Bounded: the over-fetch loop stops at `valid.len() >= limit` OR empty merge batch; each
///   batch is `DEFAULT_WINDOW_SIZE`-bounded; per-author scans are `per_author`-bounded (T-03-DOS).
/// - NIP-40: `is_expired` uses direct `SystemTime::now()` — NOT an injected clock (D-09).
/// - Cursor: `PageCursor` is length/format-validated at decode (T-03-CUR in 03-01); here it only
///   sets an upper bound + exclusion comparison — out-of-range values yield empty/older pages.

use crate::lmdb::payload::DictCache;
use crate::lmdb::scan::{scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE};
use crate::lmdb::types::{DecodedEvent, LevId, NostrEvent};
use crate::query::filter::{NostrFilter, PageCursor, QueryError};
use crate::query::hydrate::hydrate_lev_ids;
use crate::query::merge::merge_prefixes;
use crate::query::router::{build_start_keys, select_index, SelectedIndex};
use std::collections::HashMap;
use std::time::{SystemTime, UNIX_EPOCH};

// ---------------------------------------------------------------------------
// NIP-40 expiration predicate (D-08/D-09)
// ---------------------------------------------------------------------------

/// True iff the event has expired per NIP-40.
///
/// Scans `event.tags` for a tag where `tag[0] == "expiration"`, parses `tag[1]` as a `u64`
/// Unix timestamp, and returns true iff `exp != 0 && exp <= now`.
///
/// `now` comes from direct `SystemTime::now()` (D-09 — NOT injectable). Tests must use
/// future-dated (year 2100) or `0`/absent expiration values for determinism.
///
/// ## Security (T-03-NIP40)
///
/// - `tag.len() >= 2` guards indexing — never panics on short tags.
/// - `tag[1].parse::<u64>()` failure is silently ignored (event treated as non-expiring on
///   unparseable value) — never `unwrap`-panics on malformed expiration.
fn is_expired(event: &NostrEvent) -> bool {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    for tag in &event.tags {
        if tag.len() >= 2 && tag[0] == "expiration" {
            if let Ok(exp) = tag[1].parse::<u64>() {
                if exp != 0 && exp <= now {
                    return true;
                }
            }
        }
    }
    false
}

// ---------------------------------------------------------------------------
// execute_query — route, merge, over-fetch + NIP-40, cursor (QRY-01/02/05)
// ---------------------------------------------------------------------------

/// Extract the short index name from a `SelectedIndex`.
fn index_short_name(selected: &SelectedIndex) -> &'static str {
    match selected {
        SelectedIndex::Single(n) => n,
        SelectedIndex::Multi(n) => n,
    }
}

/// Execute a `NostrFilter` query returning up to `filter.limit` valid `DecodedEvent`s.
///
/// ## Algorithm (D-07 over-fetch + backfill)
///
/// 1. `select_index(filter)` → `SelectedIndex`; `build_start_keys(filter, &selected, Reverse)`.
/// 2. If `cursor` is `Some`, replace the trailing `created_at(8 LE)` in each start_key with
///    `cursor.created_at`, and drop any merge candidate whose `(created_at, lev_id)` ≥
///    `(cursor.created_at, cursor.lev_id)` before hydration (D-11 `Bound::Excluded` semantics).
/// 3. Pull a batch from `merge_prefixes`. Advance the scan window to just below the oldest
///    `created_at` in the batch. Hydrate via `hydrate_lev_ids`. Drop `is_expired` events
///    (QRY-05) and tag-value residual predicates. Push survivors into `valid`.
/// 4. Stop when `valid.len() >= filter.limit` OR merge returns empty. Truncate to `limit`.
/// 5. Cursor: if `valid.len() == limit` (more may exist), build `Some(PageCursor)` from the
///    LAST emitted event's `(created_at, lev_id)`.
///
/// ## Ordering
///
/// Guaranteed by `merge_prefixes` (D-10 `(created_at, lev_id)` DESC). Not re-sorted here.
///
/// ## Security
///
/// - T-03-DOS: bounded by `limit` (or `DEFAULT_WINDOW_SIZE` when `limit==0`).
/// - T-03-NIP40: `is_expired` tag parse never panics.
/// - T-03-CUR: cursor sets only an upper bound; out-of-range → empty/older pages.
/// - T-03-RDONLY: no `.create()`, no `write_txn`.
pub fn execute_query(
    env: &heed::Env,
    filter: &NostrFilter,
    dict_cache: &DictCache,
    cursor: Option<&PageCursor>,
) -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError> {
    execute_query_internal(env, filter, dict_cache, cursor, DEFAULT_WINDOW_SIZE)
}

/// Internal implementation with configurable batch size (for tests).
///
/// `batch_size` overrides the per-call merge window size. Production callers always use
/// `DEFAULT_WINDOW_SIZE` via `execute_query`.
#[cfg(test)]
pub(crate) fn execute_query_with_batch(
    env: &heed::Env,
    filter: &NostrFilter,
    dict_cache: &DictCache,
    cursor: Option<&PageCursor>,
    batch_size: usize,
) -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError> {
    execute_query_internal(env, filter, dict_cache, cursor, batch_size)
}

fn execute_query_internal(
    env: &heed::Env,
    filter: &NostrFilter,
    dict_cache: &DictCache,
    cursor: Option<&PageCursor>,
    batch_size: usize,
) -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError> {
    let limit = if filter.limit == 0 {
        DEFAULT_WINDOW_SIZE
    } else {
        filter.limit
    };

    let selected = select_index(filter);
    let short_name = index_short_name(&selected);

    // Cursor exclusion boundary: events at or above (cursor.created_at, cursor.lev_id) are
    // excluded from the result (they were already emitted on the previous page).
    let cursor_boundary: Option<(u64, LevId)> = cursor.map(|c| (c.created_at, c.lev_id));

    // Build start keys. When a cursor is present, use cursor.created_at as the upper bound
    // (replaces the filter.until-derived trailing bytes) so we start scanning from where we
    // left off rather than from the absolute `until` bound.
    let initial_filter: NostrFilter;
    let start_filter = if cursor.is_some() {
        initial_filter = NostrFilter {
            until: Some(cursor.unwrap().created_at),
            ..filter.clone()
        };
        &initial_filter
    } else {
        filter
    };

    let base_start_keys = build_start_keys(start_filter, &selected, ScanDirection::Reverse);
    if base_start_keys.is_empty() {
        return Ok((vec![], None));
    }

    // valid: (created_at, lev_id, DecodedEvent) — tracks sort key alongside event.
    let mut valid: Vec<(u64, LevId, DecodedEvent)> = Vec::with_capacity(limit);
    let mut skip_count: usize = 0;

    // window_boundary: tracks the (ts, lev_id) of the LAST item in each scan batch.
    // On each iteration:
    //   - prev_window_boundary is used to (1) update the start key ts and (2) filter out
    //     already-seen events at the same ts with lev_id >= prev_window_boundary.lev_id.
    //   - window_boundary is updated from the current batch's last item for the next iteration.
    //
    // ## DUPSORT-correct windowing
    //
    // `scan_index_bounded` for Reverse with `Included(key)` bound positions the cursor at the
    // SMALLEST dup of a DUPSORT key (CR-01). A batch ending at ts=T with lev_id=L may miss
    // dups at ts=T with lev_id < L (already emitted), OR may re-emit the smallest dup.
    // Solution:
    // - Set the next start key's ts to `prev_window_boundary.ts` (NOT `ts-1`) so the scan
    //   restarts at ts=T and sees any remaining dups.
    // - Filter out items with (ts, lev_id) >= prev_window_boundary to drop re-emitted items.
    // - Items at ts=T with lev_id < prev_window_boundary.lev_id are new and included.
    let mut window_boundary: Option<(u64, LevId)> = None;
    let mut current_start_keys = base_start_keys;

    loop {
        // Capture the previous boundary BEFORE advancing the scan for this iteration.
        // This is used to filter out already-seen events from the current batch.
        let prev_window_boundary = window_boundary;

        // Advance scan window: update start_key's trailing 8 bytes to prev_window_boundary.ts
        // so the next scan starts at the same ts and can find remaining dups.
        if let Some((wb_ts, _)) = prev_window_boundary {
            if wb_ts == 0 {
                break; // Already at the oldest possible bound.
            }
            current_start_keys = update_start_keys_ts(&current_start_keys, wb_ts);
        }

        let batch = merge_prefixes(env, short_name, &current_start_keys, batch_size)
            .map_err(QueryError::Lmdb)?;

        if batch.is_empty() {
            break;
        }

        // Update window_boundary from this batch's last item — used in the NEXT iteration.
        if let Some((last_ts, last_lev, _)) = batch.last() {
            window_boundary = Some((*last_ts, *last_lev));
        }

        // Apply exclusion filters to the current batch:
        // 1. Cursor exclusion: drop events at or above (cursor.created_at, cursor.lev_id).
        // 2. Window exclusion: drop events at or above prev_window_boundary (already emitted
        //    in the previous batch — handles DUPSORT boundary re-emission correctly).
        let filtered_batch: Vec<(u64, LevId)> = batch
            .into_iter()
            .filter_map(|(ts, lev_id, _key)| {
                // Cursor exclusion: events at or above the page cursor are already-emitted.
                if let Some((cur_ts, cur_lev)) = cursor_boundary {
                    if ts > cur_ts || (ts == cur_ts && lev_id >= cur_lev) {
                        return None;
                    }
                }
                // Window exclusion: events at or above the previous batch's last item were
                // already emitted (or are re-emitted DUPSORT duplicates from Included bound).
                if let Some((wb_ts, wb_lev)) = prev_window_boundary {
                    if ts > wb_ts || (ts == wb_ts && lev_id >= wb_lev) {
                        return None;
                    }
                }
                Some((ts, lev_id))
            })
            .collect();

        if filtered_batch.is_empty() {
            // All items were excluded (above cursor boundary or above prev_window_boundary).
            // If window_boundary didn't advance (same as prev), we're stuck at a ts where
            // all events have been emitted. Advance the window to ts-1 to move past it.
            if window_boundary == prev_window_boundary {
                // No progress: all items at this ts have been seen. Advance past this ts.
                if let Some((stuck_ts, _)) = window_boundary {
                    if stuck_ts == 0 {
                        break;
                    }
                    current_start_keys = update_start_keys_ts(&current_start_keys, stuck_ts.saturating_sub(1));
                    window_boundary = window_boundary.map(|(ts, lev)| (ts.saturating_sub(1), lev));
                }
            }
            continue;
        }

        let lev_ids: Vec<LevId> = filtered_batch.iter().map(|(_, l)| *l).collect();
        let hydrated = hydrate_lev_ids(env, &lev_ids, dict_cache, &mut skip_count)?;

        // Zip hydrated events with their (created_at, lev_id) tuples.
        // hydrate_lev_ids preserves input order (D-10), so zip is correct.
        for ((ts, lev_id), decoded) in filtered_batch.iter().zip(hydrated.into_iter()) {
            // Drop NIP-40 expired events (QRY-05 / D-08).
            if is_expired(&decoded.event) {
                continue;
            }

            // Tag residual predicate: if filter has tags, verify the decoded event carries a
            // matching tag+value. The Event__tag key prefix guarantees tag_name byte match but
            // we confirm tag_value on the decoded event for full correctness (QRY-02).
            if let Some(tags_filter) = &filter.tags {
                let mut passes = false;
                'outer: for tf in tags_filter {
                    for ev_tag in &decoded.event.tags {
                        if ev_tag.len() >= 2
                            && ev_tag[0] == tf.name
                            && tf.values.iter().any(|v| v == &ev_tag[1])
                        {
                            passes = true;
                            break 'outer;
                        }
                    }
                }
                if !passes {
                    continue;
                }
            }

            valid.push((*ts, *lev_id, decoded));
            if valid.len() >= limit {
                break;
            }
        }

        if valid.len() >= limit {
            break;
        }
    }

    valid.truncate(limit);

    // Build next-page cursor iff we filled the limit (indicating more events may exist).
    let next_cursor = if valid.len() == limit {
        valid.last().map(|(ts, lev_id, _)| PageCursor {
            created_at: *ts,
            lev_id: *lev_id,
        })
    } else {
        None
    };

    let events: Vec<DecodedEvent> = valid.into_iter().map(|(_, _, ev)| ev).collect();
    Ok((events, next_cursor))
}

/// Update start_keys' trailing 8 bytes (created_at LE) to `ts`.
///
/// Used for DUPSORT-correct windowing: the next batch restarts at the same ts as the
/// previous batch's oldest entry, relying on lev_id exclusion filtering to drop
/// already-seen dups at that ts (rather than using ts-1 which would skip remaining dups).
fn update_start_keys_ts(keys: &[Vec<u8>], ts: u64) -> Vec<Vec<u8>> {
    keys.iter()
        .map(|k| {
            if k.len() < 8 {
                return k.clone();
            }
            let mut new_key = k.clone();
            let offset = new_key.len() - 8;
            new_key[offset..].copy_from_slice(&ts.to_le_bytes());
            new_key
        })
        .collect()
}

// ---------------------------------------------------------------------------
// latest_per_author — grouped per-pubkey buckets (QRY-03 / D-12)
// ---------------------------------------------------------------------------

/// Return ≤ `per_author` newest events per pubkey for the given `kind` (QRY-03 / D-12).
///
/// For each author in `authors`:
/// 1. Hex-decode the pubkey (32 bytes). Skip with `tracing::warn!` on malformed hex.
/// 2. Build a single `Event__pubkeyKind` reverse start_key:
///    `pubkey(32) ‖ kind(8 LE) ‖ u64::MAX(8 LE created_at upper bound)`.
/// 3. `scan_index_bounded(env, "Event__pubkeyKind", Reverse, &start_key, per_author)` — capped
///    at `per_author` (D-12 bucket bound).
/// 4. `hydrate_lev_ids` the resulting levIds.
/// 5. Drop `is_expired` events (consistent NIP-40 filtering, QRY-05).
/// 6. Insert surviving newest-first events into the result map keyed by author hex.
///
/// Buckets are independent — one short txn per scan, one per hydrate lookup (D-08).
/// NOT a flat merged stream — output is grouped by pubkey (D-12).
///
/// ## Security
///
/// - T-03-DOS: per-author scan capped at `per_author`; no unbounded walks.
/// - T-03-RDONLY: uses `scan_index_bounded` + `hydrate_lev_ids` — no write txn.
pub fn latest_per_author(
    env: &heed::Env,
    kind: u64,
    per_author: usize,
    authors: &[String],
    dict_cache: &DictCache,
) -> Result<HashMap<String, Vec<DecodedEvent>>, QueryError> {
    if per_author == 0 {
        return Ok(HashMap::new());
    }

    let mut result: HashMap<String, Vec<DecodedEvent>> = HashMap::new();

    for author_hex in authors {
        // Hex-decode the 32-byte pubkey.
        let pubkey_bytes = match decode_hex_32(author_hex) {
            Some(b) => b,
            None => {
                tracing::warn!(
                    pubkey = author_hex.as_str(),
                    "latest_per_author: skipping author with malformed hex pubkey"
                );
                continue;
            }
        };

        // Build Event__pubkeyKind start key: pubkey(32) ‖ kind(8 LE) ‖ u64::MAX(8 LE)
        // The u64::MAX trailing created_at is the reverse scan upper bound (newest first).
        let mut start_key = Vec::with_capacity(48);
        start_key.extend_from_slice(&pubkey_bytes);
        start_key.extend_from_slice(&kind.to_le_bytes());
        start_key.extend_from_slice(&u64::MAX.to_le_bytes());

        // Bounded reverse scan — one short RoTxn per call (D-08).
        // Scan starts at `pk2||kind||u64::MAX` and walks backwards. In a reverse scan over
        // Event__pubkeyKind (StringUint64Uint64 comparator), events with the same pubkey
        // but a SMALLER kind value sort BEFORE the target kind and may be returned. Filter
        // strictly to the expected (pubkey, kind) pair by checking key bytes[32..40].
        let scan_results = scan_index_bounded(
            env,
            "Event__pubkeyKind",
            ScanDirection::Reverse,
            &start_key,
            per_author,
        )
        .map_err(QueryError::Lmdb)?;

        // Filter to only entries that match the exact (pubkey_bytes, kind) combination.
        // Event__pubkeyKind key layout: pubkey(32) ‖ kind(8 LE) ‖ created_at(8 LE)
        let kind_bytes = kind.to_le_bytes();
        let matching: Vec<LevId> = scan_results
            .into_iter()
            .filter_map(|(key_bytes, lev_id)| {
                // Key must be exactly 48 bytes and kind bytes [32..40] must match.
                if key_bytes.len() == 48
                    && key_bytes[0..32] == pubkey_bytes[..]
                    && key_bytes[32..40] == kind_bytes
                {
                    Some(lev_id)
                } else {
                    None
                }
            })
            .collect();

        if matching.is_empty() {
            // Author has no events of this kind — omit from result (no empty bucket).
            continue;
        }

        let lev_ids: Vec<LevId> = matching;
        let mut skip_count = 0usize;
        let hydrated = hydrate_lev_ids(env, &lev_ids, dict_cache, &mut skip_count)?;

        // Drop NIP-40 expired events and collect into the bucket.
        let events: Vec<DecodedEvent> = hydrated
            .into_iter()
            .filter(|ev| !is_expired(&ev.event))
            .collect();

        if !events.is_empty() {
            result.insert(author_hex.clone(), events);
        }
    }

    Ok(result)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

/// Hex-decode a string into exactly 32 bytes, or return None.
fn decode_hex_32(s: &str) -> Option<[u8; 32]> {
    if s.len() != 64 {
        return None;
    }
    let bytes = s.as_bytes();
    let mut out = [0u8; 32];
    for i in 0..32 {
        let hi = nibble(bytes[i * 2])?;
        let lo = nibble(bytes[i * 2 + 1])?;
        out[i] = (hi << 4) | lo;
    }
    Some(out)
}

fn nibble(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;
    use crate::query::filter::{NostrFilter, TagFilter};

    const PK1: &str = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    const PK2: &str = "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5";
    // Tag value from seed: 64 'a' characters.
    const TAG_VALUE_64A: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

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

    // -----------------------------------------------------------------------
    // Task 1: execute_query tests
    // -----------------------------------------------------------------------

    /// Test 1 (routing+order, QRY-01): execute_query for kinds=[1], limit=3 returns ≤3
    /// DecodedEvents with created_at non-increasing (D-10). The newest kind=1 event
    /// (levId 11, ts=1720000000) must be first.
    #[test]
    fn test_execute_query_kinds_routing_and_order() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let filter = NostrFilter {
            kinds: Some(vec![1]),
            limit: 3,
            ..Default::default()
        };

        let (events, _cursor) = execute_query(&env, &filter, &cache, None)
            .expect("execute_query must succeed");

        assert_eq!(events.len(), 3, "limit=3 must return exactly 3 events");

        // First event must be ts=1720000000 (levId=11, newest kind=1).
        assert_eq!(
            events[0].event.created_at, 1720000000,
            "first event must have ts=1720000000 (newest kind=1, levId=11)"
        );

        // All returned events must be kind=1.
        for ev in &events {
            assert_eq!(ev.event.kind, 1, "all returned events must be kind=1");
        }

        // Verify created_at non-increasing (D-10).
        let mut prev_ts = u64::MAX;
        for ev in &events {
            assert!(
                ev.event.created_at <= prev_ts,
                "created_at must be non-increasing: got {} after {}",
                ev.event.created_at, prev_ts
            );
            prev_ts = ev.event.created_at;
        }
    }

    /// Test 2 (tag, QRY-02): execute_query for a TagFilter name="e" value=64xa
    /// returns the 3 tagged fixture events (levIds 6, 8, 11) newest-first, hydrated.
    #[test]
    fn test_execute_query_tag_filter() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let filter = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "e".to_string(),
                values: vec![TAG_VALUE_64A.to_string()],
            }]),
            limit: 10,
            ..Default::default()
        };

        let (events, _cursor) = execute_query(&env, &filter, &cache, None)
            .expect("execute_query with tag filter must succeed");

        // Exactly 3 tagged events in the fixture.
        assert_eq!(
            events.len(),
            3,
            "tag filter must return exactly 3 events (levIds 6, 8, 11)"
        );

        // Newest first: levId=11 (ts=1720000000), levId=8 (ts=1700000256), levId=6 (ts=1700000255).
        assert_eq!(events[0].event.created_at, 1720000000, "first: ts=1720000000");
        assert_eq!(events[1].event.created_at, 1700000256, "second: ts=1700000256");
        assert_eq!(events[2].event.created_at, 1700000255, "third: ts=1700000255");

        // All must carry the tag e=64xa.
        for ev in &events {
            let has_tag = ev.event.tags.iter().any(|t| {
                t.len() >= 2 && t[0] == "e" && t[1] == TAG_VALUE_64A
            });
            assert!(has_tag, "every returned event must carry the e tag with the expected value");
        }
    }

    /// Test 3 (NIP-40 exclusion, QRY-05/D-08): is_expired predicate unit test.
    ///
    /// Tests the predicate directly with synthetic NostrEvent values. NIP-40 tests must use
    /// future-dated (year 2100) or 0/absent expiration values (D-09 — `now` is live system time).
    #[test]
    fn test_is_expired_predicate() {
        use crate::lmdb::types::NostrEvent;

        fn make_event(expiration: Option<&str>) -> NostrEvent {
            NostrEvent {
                id: "0".repeat(64),
                pubkey: "0".repeat(64),
                created_at: 1700000000,
                kind: 1,
                tags: expiration
                    .map(|v| vec![vec!["expiration".to_string(), v.to_string()]])
                    .unwrap_or_default(),
                content: String::new(),
                sig: "0".repeat(128),
            }
        }

        // Past expiration (100 seconds since epoch) → expired.
        let past = make_event(Some("100"));
        assert!(is_expired(&past), "expiration=100 (past) must be considered expired");

        // Future expiration (year 2100 = ts 4102444800) → NOT expired (D-09: deterministic future).
        let future = make_event(Some("4102444800"));
        assert!(!is_expired(&future), "expiration=4102444800 (year 2100) must NOT be expired");

        // No expiration tag → not expired.
        let no_exp = make_event(None);
        assert!(!is_expired(&no_exp), "no expiration tag → not expired");

        // expiration=0 → not expired (zero means no expiry per NIP-40 predicate).
        let zero_exp = make_event(Some("0"));
        assert!(!is_expired(&zero_exp), "expiration=0 → not expired");

        // Malformed expiration → not expired (T-03-NIP40: parse failure is non-fatal, no panic).
        let bad_exp = make_event(Some("not_a_number"));
        assert!(!is_expired(&bad_exp), "malformed expiration → not expired (no panic)");

        // Short tag (only tag name, no value) → not expired, no panic.
        let short_tag = NostrEvent {
            tags: vec![vec!["expiration".to_string()]],
            ..make_event(None)
        };
        assert!(!is_expired(&short_tag), "short tag (no value) → not expired, no panic");
    }

    /// Test 4 (over-fetch backfill, D-07): with a small batch override (batch_size=2),
    /// a query whose result set has no expired events still returns the full expected count.
    /// Proves the backfill loop terminates correctly when nothing is dropped.
    ///
    /// The 11 fixture events carry NO expiration tag, so none are dropped.
    #[test]
    fn test_execute_query_overfetch_backfill() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();

        // 7 kind=1 events exist in the fixture.
        let filter = NostrFilter {
            kinds: Some(vec![1]),
            limit: 7,
            ..Default::default()
        };

        // Use batch_size=2 so the backfill loop must execute ≥4 iterations to collect 7 events.
        let (events, _cursor) = execute_query_with_batch(&env, &filter, &cache, None, 2)
            .expect("over-fetch loop must succeed");

        assert_eq!(
            events.len(),
            7,
            "backfill must return all 7 kind=1 events even with batch_size=2"
        );

        // Verify non-increasing order (D-10).
        let mut prev_ts = u64::MAX;
        for ev in &events {
            assert!(
                ev.event.created_at <= prev_ts,
                "created_at must be non-increasing: {} after {}",
                ev.event.created_at, prev_ts
            );
            prev_ts = ev.event.created_at;
        }
    }

    /// Test 5 (cursor resume, D-11): page1 limit=2 + cursor → page2, no overlap;
    /// page1 + page2 == limit=4 single-query first four results.
    #[test]
    fn test_execute_query_cursor_resume() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();

        let filter = NostrFilter {
            kinds: Some(vec![1]),
            limit: 2,
            ..Default::default()
        };

        // Page 1.
        let (page1, cursor1) = execute_query(&env, &filter, &cache, None)
            .expect("page 1 must succeed");
        assert_eq!(page1.len(), 2, "page 1 must return 2 events");
        assert!(cursor1.is_some(), "page 1 must return a cursor (7 kind=1 events exist)");

        let cursor = cursor1.unwrap();

        // Page 2 using the cursor.
        let (page2, _cursor2) = execute_query(&env, &filter, &cache, Some(&cursor))
            .expect("page 2 must succeed");
        assert_eq!(page2.len(), 2, "page 2 must return 2 events");

        // No overlap: page2 events must be strictly older than the last page1 event.
        // (They can have the same ts if lev_id is excluded, but event ids must differ.)
        let page1_last_ts = page1.last().unwrap().event.created_at;
        let page1_last_id = page1.last().unwrap().event.id.clone();
        for ev in &page2 {
            assert_ne!(
                ev.event.id, page1_last_id,
                "page2 must not repeat the cursor event"
            );
            // created_at can be ≤ (ties with lower lev_id are possible), not >.
            assert!(
                ev.event.created_at <= page1_last_ts,
                "page2 events must be ≤ page1's last ts ({page1_last_ts}), got {}",
                ev.event.created_at
            );
        }

        // page1 + page2 must equal the first 4 results of a single limit=4 query.
        let filter4 = NostrFilter {
            kinds: Some(vec![1]),
            limit: 4,
            ..Default::default()
        };
        let (all4, _) = execute_query(&env, &filter4, &cache, None)
            .expect("limit=4 query must succeed");
        assert_eq!(all4.len(), 4, "limit=4 must return 4 events");

        let combined_ids: Vec<&str> = page1.iter().chain(page2.iter())
            .map(|ev| ev.event.id.as_str())
            .collect();
        let all4_ids: Vec<&str> = all4.iter().map(|ev| ev.event.id.as_str()).collect();

        assert_eq!(
            combined_ids, all4_ids,
            "page1 + page2 must equal the first 4 events from a single limit=4 query"
        );
    }

    // -----------------------------------------------------------------------
    // Task 2: latest_per_author tests (QRY-03 / D-12)
    // -----------------------------------------------------------------------

    /// Test 1: latestPerAuthor(kind=1, per_author=2, [pk1, pk2]) returns 2 buckets.
    ///
    /// Fixture data (from Event__pubkeyKind.json):
    ///   pk1 kind=1: ts=1720000000 (levId=11), ts=1700000256 (levIds=7,8), ts=1700000255 (levIds=5,6), ...
    ///   pk2 kind=1: ts=1710000000 (levId=10), ts=1700000000 (levId=4)
    ///
    /// per_author=2 → pk1: [newest ts=1720000000, next ts=1700000256]; pk2: [both events]
    #[test]
    fn test_latest_per_author_two_buckets() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();

        let result = latest_per_author(&env, 1, 2, &[PK1.to_string(), PK2.to_string()], &cache)
            .expect("latest_per_author must succeed");

        assert_eq!(result.len(), 2, "must return 2 buckets");

        // pk1 bucket: 2 newest kind=1 events.
        let pk1 = result.get(PK1).expect("pk1 must have a bucket");
        assert_eq!(pk1.len(), 2, "pk1 bucket must have 2 events");
        assert_eq!(pk1[0].event.created_at, 1720000000, "pk1[0] must be ts=1720000000");
        assert_eq!(pk1[1].event.created_at, 1700000256, "pk1[1] must be ts=1700000256");

        // pk2 bucket: both kind=1 events (only 2 exist).
        let pk2 = result.get(PK2).expect("pk2 must have a bucket");
        assert_eq!(pk2.len(), 2, "pk2 bucket must have 2 events");
        assert_eq!(pk2[0].event.created_at, 1710000000, "pk2[0] must be ts=1710000000");
        assert_eq!(pk2[1].event.created_at, 1700000000, "pk2[1] must be ts=1700000000");

        // Both buckets must be created_at DESC.
        for (pubkey, bucket) in &result {
            let mut prev_ts = u64::MAX;
            for ev in bucket {
                assert!(
                    ev.event.created_at <= prev_ts,
                    "bucket for {pubkey} must be created_at DESC: {} after {}",
                    ev.event.created_at, prev_ts
                );
                prev_ts = ev.event.created_at;
            }
        }
    }

    /// Test 2: per_author=1 yields exactly the newest event per pubkey.
    #[test]
    fn test_latest_per_author_per_author_one() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();

        let result = latest_per_author(&env, 1, 1, &[PK1.to_string(), PK2.to_string()], &cache)
            .expect("latest_per_author per_author=1 must succeed");

        // pk1: only the newest kind=1 event (ts=1720000000).
        let pk1 = result.get(PK1).expect("pk1 must have a bucket");
        assert_eq!(pk1.len(), 1, "pk1 per_author=1 → 1 event");
        assert_eq!(pk1[0].event.created_at, 1720000000, "pk1 newest kind=1 is ts=1720000000");

        // pk2: only the newest kind=1 event (ts=1710000000).
        let pk2 = result.get(PK2).expect("pk2 must have a bucket");
        assert_eq!(pk2.len(), 1, "pk2 per_author=1 → 1 event");
        assert_eq!(pk2[0].event.created_at, 1710000000, "pk2 newest kind=1 is ts=1710000000");
    }

    /// Test 3: author with no kind=N events yields an absent key (no error, no bogus entry).
    #[test]
    fn test_latest_per_author_no_matching_events() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();

        // pk2 has NO kind=2 events.
        let result = latest_per_author(&env, 2, 5, &[PK2.to_string()], &cache)
            .expect("latest_per_author with no-match author must not error");

        assert!(
            result.get(PK2).is_none(),
            "pk2 has no kind=2 events → must not appear in result (no bogus empty bucket)"
        );
        assert!(result.is_empty(), "result map must be empty");
    }
}

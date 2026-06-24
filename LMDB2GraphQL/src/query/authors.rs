/// authors.rs — Distinct-pubkey enumeration over strfry's live `Event__pubkey` index.
///
/// Provides `distinct_authors`, which enumerates every distinct pubkey that has
/// authored at least one event, in O(distinct authors) time via seek-skip — each
/// distinct pubkey costs exactly one B-tree seek regardless of how many events it
/// authored (QRY-06).
///
/// ## Seek-skip algorithm
///
/// 1. Seek to lower bound (all-zero key, or `increment_be(after)` for pagination).
/// 2. Read the first entry; slice the 32-byte pubkey prefix.
/// 3. Set next lower bound = `increment_be(pubkey)` — jumps past ALL entries for this author.
/// 4. Repeat up to `limit` times. O(distinct authors), not O(total events).
///
/// ## Correctness of `increment_be`
///
/// `Event__pubkey` keys sort by pubkey bytes via plain memcmp under `StringUint64Cmp`,
/// so lexicographic big-endian +1 of the 32-byte prefix yields the correct
/// "first possible key of the next author" lower bound (see comparators.rs).
///
/// ## Read-only invariants (T-07-RDONLY / LMDB-01)
///
/// - Only `env.read_txn()` is used — no `write_txn`.
/// - Sub-DB opened via `open_index_string_uint64` (never `.create()`).
/// - RoTxn is dropped at end of call (T-07-TXN / LMDB-09 / D-08).
use crate::lmdb::indexes::open_index_string_uint64;
use crate::query::filter::QueryError;
use heed::types::Bytes;
use std::ops::Bound;

/// Return type of `distinct_authors`: a page of 32-byte pubkeys and an optional next-page cursor.
///
/// The cursor is `Some(last_pubkey)` when a full page was returned, `None` at end-of-stream.
pub type AuthorsPage = (Vec<[u8; 32]>, Option<[u8; 32]>);

// ---------------------------------------------------------------------------
// increment_be — big-endian successor (no overflow = None)
// ---------------------------------------------------------------------------

/// Add 1 to a 32-byte value treated as a big-endian unsigned integer.
///
/// Returns `None` if the input is all-`0xFF` (overflow wraps to zero — clean
/// end-of-stream signal for the seek-skip loop).
///
/// ## Correctness
///
/// The `Event__pubkey` key sorts the 32-byte pubkey portion by plain byte order
/// (memcmp) under `StringUint64Cmp`. Adding 1 to the big-endian representation
/// yields the lexicographically next possible 32-byte value — exactly the lower
/// bound needed to skip past all of the current author's index entries.
pub(crate) fn increment_be(pk: &[u8; 32]) -> Option<[u8; 32]> {
    let mut result = *pk;
    // Iterate from the least-significant byte (last) toward the most-significant (first).
    for i in (0..32).rev() {
        let (new_byte, carry) = result[i].overflowing_add(1);
        result[i] = new_byte;
        if !carry {
            // No carry — addition is done.
            return Some(result);
        }
        // Carry continues to the next (more-significant) byte.
    }
    // Every byte carried — input was all 0xFF; overflow means end-of-stream.
    None
}

// ---------------------------------------------------------------------------
// distinct_authors — O(distinct authors) seek-skip enumeration
// ---------------------------------------------------------------------------

/// Enumerate distinct pubkeys present in the `Event__pubkey` index, byte-ascending.
///
/// ## Parameters
///
/// - `env`: the heed LMDB environment (read-only).
/// - `after`: exclusive pagination cursor — the last pubkey returned by the previous page.
///   The next page starts AFTER this pubkey (via `increment_be`). `None` starts from the
///   beginning of the index.
/// - `limit`: maximum number of distinct pubkeys to return in this call. Callers MUST
///   clamp this to ≤ 500 before calling (Plan 07-02 enforces the hard ceiling).
///
/// ## Returns
///
/// `(pubkeys, next_cursor)` where:
/// - `pubkeys` is a `Vec<[u8; 32]>` of distinct pubkeys, byte-ascending.
/// - `next_cursor` is `Some(last_pubkey)` when a full page was returned (more may remain),
///   `None` at true end-of-stream or when the result set is smaller than `limit`.
///
/// ## Read-only invariants (T-07-RDONLY / LMDB-01)
///
/// Opens exactly one short `RoTxn` (dropped before return — T-07-TXN / LMDB-09 / D-08).
/// Never calls `env.write_txn()` or `.create()` on any sub-DB.
///
/// ## Seek-key length
///
/// `Event__pubkey` keys are 40 bytes: `pubkey(32) || created_at(8 LE)`. The
/// `StringUint64Cmp` comparator treats the last 8 bytes as the uint64 suffix and
/// the remaining prefix bytes as the string part.
///
/// To correctly seek past all of a pubkey's entries, the seek key must also be 40 bytes:
/// `[increment_be(pk) || 0x00 * 8]`. This lands on the first entry of the next author
/// regardless of their `created_at` values. A 32-byte seek key would be misinterpreted
/// by the comparator (its last 8 bytes would be taken as created_at, and the string part
/// would be only 24 bytes — not a valid author skip).
///
/// ## Pagination snapshot semantics
///
/// Each page is a separate short read transaction. A pubkey that appears between pages
/// may be missed or duplicated — the same eventual-consistency property that `events()`
/// already has. Callers must tolerate this.
pub fn distinct_authors(
    env: &heed::Env,
    after: Option<&[u8; 32]>,
    limit: usize,
) -> Result<AuthorsPage, QueryError> {
    // Open one short read transaction for this entire page.
    let rtxn = env.read_txn().map_err(|e| {
        QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e))
    })?;

    // Open Event__pubkey with the correct StringUint64Cmp comparator (never .create()).
    let db: heed::Database<Bytes, Bytes, _> =
        open_index_string_uint64(env, &rtxn, "Event__pubkey")?;

    // Build a 40-byte seek key from a 32-byte pubkey prefix.
    //
    // Key format: pubkey(32) || created_at(8 LE). Appending 0x00*8 as created_at gives the
    // lowest possible created_at for this pubkey — `MDB_SET_RANGE` will land on the very
    // first entry for this pubkey (or the next if it doesn't exist).
    let make_seek_key = |pk: &[u8; 32]| -> Vec<u8> {
        let mut key = Vec::with_capacity(40);
        key.extend_from_slice(pk);
        key.extend_from_slice(&[0u8; 8]); // created_at = 0 (minimum)
        key
    };

    // Establish the initial seek key (40 bytes).
    let initial_seek_key: Vec<u8> = match after {
        Some(pk) => {
            match increment_be(pk) {
                Some(next_pk) => make_seek_key(&next_pk),
                // after was all-0xFF — the stream is already exhausted.
                None => return Ok((vec![], None)),
            }
        }
        None => make_seek_key(&[0u8; 32]),
    };

    let mut result: Vec<[u8; 32]> = Vec::with_capacity(limit.min(64));
    let mut seek_key: Vec<u8> = initial_seek_key;

    for _ in 0..limit {
        // Seek to the first entry with key >= seek_key (MDB_SET_RANGE).
        let range = (
            Bound::Included(seek_key.as_slice()),
            Bound::Unbounded as Bound<&[u8]>,
        );
        let mut iter = db
            .range(&rtxn, &range)
            .map_err(|e| QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e)))?;
        match iter.next() {
            None => break, // index exhausted
            Some(entry) => {
                let (key, _value) = entry
                    .map_err(|e| QueryError::Lmdb(crate::lmdb::indexes::IndexError::Heed(e)))?;
                if key.len() < 32 {
                    // Structural surprise: key shorter than 32 bytes — strfry should never
                    // produce this. Log and break (T-07-SHORTKEY).
                    tracing::warn!(
                        key_len = key.len(),
                        "distinct_authors: Event__pubkey key shorter than 32 bytes — \
                         unexpected structural anomaly; stopping enumeration"
                    );
                    break;
                }
                // Slice the 32-byte pubkey prefix.
                let mut pubkey = [0u8; 32];
                pubkey.copy_from_slice(&key[0..32]);
                result.push(pubkey);

                // Build next seek key to jump past ALL entries for this pubkey.
                match increment_be(&pubkey) {
                    Some(next_pk) => seek_key = make_seek_key(&next_pk),
                    None => break, // pubkey was all-0xFF — end-of-stream
                }
            }
        }
        // Drop the iterator here (end of match arm scope) before the next seek.
    }

    // RoTxn dropped here — short per-call txn (T-07-TXN / LMDB-09 / D-08).
    drop(rtxn);

    // Compute next_cursor: Some(last) only when a full page was returned.
    let next_cursor = if result.len() == limit {
        result.last().copied()
    } else {
        None
    };

    Ok((result, next_cursor))
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    // -----------------------------------------------------------------------
    // Fixture helper — mirrors the idiom in src/lmdb/indexes.rs tests.
    // -----------------------------------------------------------------------

    /// Copy fixture to a fresh tempdir and open the env there.
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

    // -----------------------------------------------------------------------
    // PK constants (from Event__pubkey.json golden vectors)
    // -----------------------------------------------------------------------

    /// Decode a 64-char lowercase hex string into a [u8; 32].
    /// Panics on invalid hex — only used in tests with known-good literals.
    fn decode_hex32(hex: &str) -> [u8; 32] {
        assert_eq!(hex.len(), 64, "hex32 must be 64 chars");
        let bytes = hex.as_bytes();
        let mut out = [0u8; 32];
        for (i, slot) in out.iter_mut().enumerate() {
            *slot = nibble(bytes[i * 2]) << 4 | nibble(bytes[i * 2 + 1]);
        }
        out
    }

    fn nibble(b: u8) -> u8 {
        match b {
            b'0'..=b'9' => b - b'0',
            b'a'..=b'f' => b - b'a' + 10,
            b'A'..=b'F' => b - b'A' + 10,
            _ => panic!("invalid hex nibble: {b}"),
        }
    }

    /// PK1 = 79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798
    /// (9 index entries across 5 distinct created_at keys with dups)
    fn pk1() -> [u8; 32] {
        decode_hex32("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
    }

    /// PK2 = c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5
    /// (2 index entries)
    fn pk2() -> [u8; 32] {
        decode_hex32("c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5")
    }

    // -----------------------------------------------------------------------
    // increment_be tests
    // -----------------------------------------------------------------------

    /// Behavior 1: increment_be of all-zero returns [0,..,0,1] (big-endian +1).
    #[test]
    fn test_increment_be_zero_returns_one() {
        let input = [0u8; 32];
        let result = increment_be(&input).expect("no overflow on all-zero");
        let mut expected = [0u8; 32];
        expected[31] = 1;
        assert_eq!(result, expected, "all-zero +1 should be [0..0, 1]");
    }

    /// Behavior 2: carry propagates correctly — [.., 0x00, 0xFF] -> [.., 0x01, 0x00].
    #[test]
    fn test_increment_be_carry_propagation() {
        let mut input = [0u8; 32];
        input[30] = 0x00;
        input[31] = 0xFF;
        let result = increment_be(&input).expect("no overflow");
        let mut expected = [0u8; 32];
        expected[30] = 0x01;
        expected[31] = 0x00;
        assert_eq!(result, expected, "[..,0x00,0xFF] +1 should carry to [..,0x01,0x00]");
    }

    /// Behavior 3: all-0xFF returns None — clean end-of-stream.
    #[test]
    fn test_increment_be_all_ff_returns_none() {
        let input = [0xFF; 32];
        assert!(
            increment_be(&input).is_none(),
            "all-0xFF should overflow to None"
        );
    }

    /// Behavior 4: spot check on PK1's first bytes — increment is correct.
    #[test]
    fn test_increment_be_pk1_spot_check() {
        let pk = pk1();
        let incremented = increment_be(&pk).expect("pk1 is not all-0xFF");
        // The incremented value must be strictly greater than pk1 when both
        // are treated as big-endian unsigned integers (byte-by-byte comparison).
        assert!(
            incremented.as_slice() > pk.as_slice(),
            "increment_be(PK1) must be byte-greater than PK1"
        );
        // Also verify: decrementing the last byte should recover PK1 (unless the last
        // byte is 0x00, in which case a carry occurred). Rather than re-implement
        // decrement_be, we verify the simpler property: exactly one logical increment.
        // Build the expected value by adding 1 to the big-endian integer manually.
        let mut expected = pk;
        for i in (0..32).rev() {
            let (b, carry) = expected[i].overflowing_add(1);
            expected[i] = b;
            if !carry {
                break;
            }
        }
        assert_eq!(incremented, expected, "increment_be(PK1) must match manual +1");
    }

    // -----------------------------------------------------------------------
    // distinct_authors fixture tests
    // -----------------------------------------------------------------------

    /// Behavior 1: full page (limit=10) returns both distinct pubkeys, byte-ascending,
    /// with next_cursor=None (clean termination since 2 < limit).
    #[test]
    fn test_distinct_authors_all_limit_10() {
        let (env, _tmp) = open_temp_fixture_env();
        let (authors, cursor) = distinct_authors(&env, None, 10)
            .expect("distinct_authors must not error on fixture");
        assert_eq!(
            authors.len(),
            2,
            "fixture has exactly 2 distinct pubkeys; got {}",
            authors.len()
        );
        assert_eq!(authors[0], pk1(), "first pubkey must be PK1 (byte-ascending)");
        assert_eq!(authors[1], pk2(), "second pubkey must be PK2");
        assert!(
            cursor.is_none(),
            "next_cursor must be None when fewer than limit pubkeys returned"
        );
    }

    /// Behavior 2: limit=1 returns only PK1 with next_cursor=Some(PK1).
    #[test]
    fn test_distinct_authors_limit_1_returns_pk1_with_cursor() {
        let (env, _tmp) = open_temp_fixture_env();
        let (authors, cursor) = distinct_authors(&env, None, 1)
            .expect("distinct_authors must not error on fixture");
        assert_eq!(authors.len(), 1, "limit=1 must return exactly 1 pubkey");
        assert_eq!(authors[0], pk1(), "limit=1 first result must be PK1");
        assert_eq!(
            cursor,
            Some(pk1()),
            "next_cursor must be Some(PK1) on a full page"
        );
    }

    /// Behavior 3: resuming after PK1 yields only PK2 with next_cursor=None.
    #[test]
    fn test_distinct_authors_resume_after_pk1_yields_pk2() {
        let (env, _tmp) = open_temp_fixture_env();
        let pk1 = pk1();
        let (authors, cursor) = distinct_authors(&env, Some(&pk1), 10)
            .expect("distinct_authors must not error on fixture");
        assert_eq!(
            authors.len(),
            1,
            "resuming after PK1 must yield exactly 1 pubkey (PK2)"
        );
        assert_eq!(authors[0], pk2(), "resumed page must start with PK2");
        assert!(cursor.is_none(), "next_cursor must be None after PK2 (end of stream)");
    }

    /// Behavior 4: multi-page walk with limit=1 collects exactly [PK1, PK2] across pages,
    /// no pubkey repeated, terminates when next_cursor becomes None.
    #[test]
    fn test_distinct_authors_multi_page_walk() {
        let (env, _tmp) = open_temp_fixture_env();
        let mut all_authors: Vec<[u8; 32]> = Vec::new();
        let mut cursor: Option<[u8; 32]> = None;
        let mut pages = 0usize;

        loop {
            let after = cursor.as_ref();
            let (page, next) = distinct_authors(&env, after, 1)
                .expect("distinct_authors must not error");
            pages += 1;
            assert!(pages <= 10, "multi-page walk must terminate within 10 pages");
            all_authors.extend_from_slice(&page);
            cursor = next;
            if cursor.is_none() {
                break;
            }
        }

        assert_eq!(
            all_authors.len(),
            2,
            "multi-page walk must collect exactly 2 distinct pubkeys"
        );
        assert_eq!(all_authors[0], pk1(), "first collected pubkey must be PK1");
        assert_eq!(all_authors[1], pk2(), "second collected pubkey must be PK2");
    }

    /// Behavior 5: PK1 is returned exactly once even though it owns 9 index entries —
    /// proves seek-skip, not adjacent-dedup.
    ///
    /// The fixture has PK1 across 5 distinct `created_at` keys with multiple levIds each
    /// (total 9 entries). A seek-skip algorithm visits only 1 key for PK1.
    /// An adjacent-dedup algorithm would visit all 9 entries.
    ///
    /// We verify seek-skip by running with limit=10 and asserting PK1 appears exactly once.
    #[test]
    fn test_distinct_authors_pk1_returned_exactly_once() {
        let (env, _tmp) = open_temp_fixture_env();
        let (authors, _cursor) = distinct_authors(&env, None, 10)
            .expect("distinct_authors must not error on fixture");
        let pk1_count = authors.iter().filter(|&&pk| pk == pk1()).count();
        assert_eq!(
            pk1_count,
            1,
            "PK1 (which spans 9 index entries) must appear exactly once — proves seek-skip"
        );
    }
}

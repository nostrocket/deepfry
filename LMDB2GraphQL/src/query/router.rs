/// router.rs — Index selection (D-02) and per-prefix start_key construction (D-03) for the Phase-3 query engine.
///
/// ## Purpose
///
/// Given a `NostrFilter`, `select_index` picks the most selective applicable `Event__*` index
/// following the D-02 fixed priority order (no cardinality estimation — hardcoded heuristic).
/// `build_start_keys` then constructs one composite `start_key` per value-prefix, pushing
/// `since`/`until` into the trailing `created_at(8 LE)` bound (D-03).
///
/// ## Key format reference (from indexes.rs table + spec §3.1)
///
/// | Index               | Key layout                                           |
/// |---------------------|------------------------------------------------------|
/// | `Event__id`         | id(32 raw bytes) ‖ created_at(8 LE)                 |
/// | `Event__pubkey`     | pubkey(32 raw bytes) ‖ created_at(8 LE)             |
/// | `Event__kind`       | kind(8 LE) ‖ created_at(8 LE)                       |
/// | `Event__pubkeyKind` | pubkey(32 raw bytes) ‖ kind(8 LE) ‖ created_at(8 LE)|
/// | `Event__tag`        | tagName(1 byte) ‖ tagValue(var) ‖ created_at(8 LE)  |
/// | `Event__created_at` | created_at (MDB_INTEGERKEY — plain 8 LE bytes)       |
///
/// ## Thread safety
///
/// All functions are pure / stateless — they may be called from any thread.

use crate::lmdb::scan::ScanDirection;
use crate::query::filter::NostrFilter;

// ---------------------------------------------------------------------------
// Internal: hex decoding (no external `hex` crate needed)
// ---------------------------------------------------------------------------

/// Decode a lowercase or uppercase hex string into bytes.
/// Returns `Err(String)` describing the failure for warn logging.
///
/// Made `pub(crate)` so engine.rs can reuse instead of maintaining a duplicate (IN-02).
pub(crate) fn decode_hex(s: &str) -> Result<Vec<u8>, String> {
    if s.len() % 2 != 0 {
        return Err(format!("odd hex length: {}", s.len()));
    }
    let mut out = Vec::with_capacity(s.len() / 2);
    let bytes = s.as_bytes();
    for i in (0..bytes.len()).step_by(2) {
        let hi = nibble(bytes[i]).map_err(|e| format!("byte {}: {}", i, e))?;
        let lo = nibble(bytes[i + 1]).map_err(|e| format!("byte {}: {}", i + 1, e))?;
        out.push((hi << 4) | lo);
    }
    Ok(out)
}

/// Made `pub(crate)` so engine.rs can reuse via the shared `decode_hex` path (IN-02).
pub(crate) fn nibble(b: u8) -> Result<u8, &'static str> {
    match b {
        b'0'..=b'9' => Ok(b - b'0'),
        b'a'..=b'f' => Ok(b - b'a' + 10),
        b'A'..=b'F' => Ok(b - b'A' + 10),
        _ => Err("invalid hex character"),
    }
}

// ---------------------------------------------------------------------------
// Index selection result
// ---------------------------------------------------------------------------

/// Result of `select_index`: identifies which `Event__*` index to scan and whether
/// a single start_key suffices or one start_key per value-prefix is required (D-05).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SelectedIndex {
    /// A single start_key spans the whole index (e.g. `Event__id` point scan, or
    /// `Event__created_at` default feed scan — D-04).
    Single(&'static str),

    /// One start_key per value-prefix; merge required (D-05).
    /// Used when the filter has multiple authors, kinds, (author,kind) pairs, or tag values.
    Multi(&'static str),
}

// ---------------------------------------------------------------------------
// D-02: Fixed priority index selection
// ---------------------------------------------------------------------------

/// Select the most selective applicable `Event__*` index for `filter` (D-02).
///
/// Priority order (highest selectivity first — no cardinality estimation):
/// 1. `ids.is_some()`                             → `Single("Event__id")`
/// 2. `authors.is_some() && kinds.is_some()`      → `Multi("Event__pubkeyKind")`
/// 3. `authors.is_some()`                         → `Multi("Event__pubkey")`
/// 4. `kinds.is_some()`                           → `Multi("Event__kind")`
/// 5. `tags.is_some()`                            → `Multi("Event__tag")` (QRY-02)
/// 6. all None                                    → `Single("Event__created_at")` (D-04 default feed)
///
/// This is a pure function — no I/O, no LMDB access.
pub fn select_index(filter: &NostrFilter) -> SelectedIndex {
    if filter.ids.is_some() {
        SelectedIndex::Single("Event__id")
    } else if filter.authors.is_some() && filter.kinds.is_some() {
        SelectedIndex::Multi("Event__pubkeyKind")
    } else if filter.authors.is_some() {
        SelectedIndex::Multi("Event__pubkey")
    } else if filter.kinds.is_some() {
        SelectedIndex::Multi("Event__kind")
    } else if filter.tags.is_some() {
        SelectedIndex::Multi("Event__tag")
    } else {
        SelectedIndex::Single("Event__created_at")
    }
}

// ---------------------------------------------------------------------------
// D-03: Per-prefix start_key construction with time bound pushdown
// ---------------------------------------------------------------------------

/// Build one composite `start_key` per value-prefix for the selected index.
///
/// Each key ends with the trailing `created_at(8 LE)` bound from D-03:
/// - `Reverse` scan: use `filter.until.unwrap_or(u64::MAX)` — seeks to the newest end
/// - `Forward` scan: use `filter.since.unwrap_or(0)` — seeks to the oldest end
///
/// ## Per-index layouts (key-format table, indexes.rs + spec §3.1)
///
/// - `Event__id`         : each id-hex(32) ‖ ts(8 LE)          — one key per id
/// - `Event__pubkey`     : each author-hex(32) ‖ ts(8 LE)        — one key per author
/// - `Event__kind`       : each kind(8 LE) ‖ ts(8 LE)            — one key per kind
/// - `Event__pubkeyKind` : each (author × kind): pubkey(32) ‖ kind(8 LE) ‖ ts(8 LE)
/// - `Event__tag`        : each (tag.name first-byte ‖ tag-value-bytes) ‖ ts(8 LE)
/// - `Event__created_at` : single plain ts(8 LE) key (MDB_INTEGERKEY)
///
/// ## Security (T-03-HEX)
///
/// Unparseable hex id/pubkey values are skipped with `tracing::warn!` rather than panicking.
/// Callers should expect fewer start_keys than input values if any hex is malformed.
pub fn build_start_keys(
    filter: &NostrFilter,
    selected: &SelectedIndex,
    direction: ScanDirection,
) -> Vec<Vec<u8>> {
    let ts = match direction {
        ScanDirection::Reverse => filter.until.unwrap_or(u64::MAX),
        ScanDirection::Forward => filter.since.unwrap_or(0),
    };
    let ts_bytes = ts.to_le_bytes();

    let mut keys: Vec<Vec<u8>> = match selected {
        SelectedIndex::Single("Event__id") => {
            // One start_key per id — id(32 raw) ‖ created_at(8 LE)
            let ids = filter.ids.as_deref().unwrap_or(&[]);
            ids.iter()
                .filter_map(|hex_id| {
                    match decode_hex(hex_id) {
                        Ok(bytes) if bytes.len() == 32 => {
                            let mut k = Vec::with_capacity(40);
                            k.extend_from_slice(&bytes);
                            k.extend_from_slice(&ts_bytes);
                            Some(k)
                        }
                        Ok(bytes) => {
                            tracing::warn!(
                                hex = hex_id.as_str(),
                                decoded_len = bytes.len(),
                                "skipping filter value with invalid hex: id must decode to 32 bytes"
                            );
                            None
                        }
                        Err(e) => {
                            tracing::warn!(
                                hex = hex_id.as_str(),
                                reason = e,
                                "skipping filter value with invalid hex"
                            );
                            None
                        }
                    }
                })
                .collect()
        }

        SelectedIndex::Multi("Event__pubkey") => {
            // One start_key per author — pubkey(32 raw) ‖ created_at(8 LE)
            let authors = filter.authors.as_deref().unwrap_or(&[]);
            authors.iter()
                .filter_map(|hex_pk| decode_pubkey_warn(hex_pk).map(|pk| {
                    let mut k = Vec::with_capacity(40);
                    k.extend_from_slice(&pk);
                    k.extend_from_slice(&ts_bytes);
                    k
                }))
                .collect()
        }

        SelectedIndex::Multi("Event__kind") => {
            // One start_key per kind — kind(8 LE) ‖ created_at(8 LE)
            let kinds = filter.kinds.as_deref().unwrap_or(&[]);
            kinds.iter()
                .map(|&kind| {
                    let mut k = Vec::with_capacity(16);
                    k.extend_from_slice(&kind.to_le_bytes());
                    k.extend_from_slice(&ts_bytes);
                    k
                })
                .collect()
        }

        SelectedIndex::Multi("Event__pubkeyKind") => {
            // One start_key per (author × kind) pair — pubkey(32) ‖ kind(8 LE) ‖ created_at(8 LE)
            let authors = filter.authors.as_deref().unwrap_or(&[]);
            let kinds = filter.kinds.as_deref().unwrap_or(&[]);
            let mut keys = Vec::new();
            for hex_pk in authors {
                match decode_pubkey_warn(hex_pk) {
                    Some(pk) => {
                        for &kind in kinds {
                            let mut k = Vec::with_capacity(48);
                            k.extend_from_slice(&pk);
                            k.extend_from_slice(&kind.to_le_bytes());
                            k.extend_from_slice(&ts_bytes);
                            keys.push(k);
                        }
                    }
                    None => {} // warn already emitted by decode_pubkey_warn
                }
            }
            keys
        }

        SelectedIndex::Multi("Event__tag") => {
            // One start_key per (tag_name_byte ‖ tag_value_raw_bytes) prefix ‖ created_at(8 LE)
            //
            // strfry's exact rule for Event__tag value encoding (T-03-CR07-I):
            //   - If value is EXACTLY 64 lowercase hex chars (0-9, a-f), decode to 32 raw bytes.
            //   - All other values (including uppercase hex, short even-length hex like "beef",
            //     and non-hex) use the raw UTF-8 bytes unchanged.
            //
            // "beef" (4-char even-length hex) must NOT be hex-decoded: strfry stores topic
            // tags as raw UTF-8. Only 32-byte event/pubkey ids (64 lowercase hex chars) are
            // decoded to binary in strfry's index. Getting this wrong silently misses matches.
            //
            // Tag name validation (T-03-WR04-I):
            //   - Tag names must be exactly 1 character (single byte). Nostr protocol tag names
            //     are single-letter (e.g. "e", "p", "t"). A multi-char or empty name would
            //     either silently scan the wrong prefix (old first-byte truncation bug) or panic.
            //     Instead: skip the tag with a warn and produce zero start keys for it.
            let tags = filter.tags.as_deref().unwrap_or(&[]);
            let mut keys = Vec::new();
            for tag in tags {
                // WR-04 / T-03-WR04-I: require exactly 1 character; warn and skip otherwise.
                if tag.name.len() != 1 {
                    tracing::warn!(
                        name = tag.name.as_str(),
                        "tag name must be exactly one character — skipping filter"
                    );
                    continue;
                }
                let name_byte = tag.name.as_bytes()[0];

                for value in &tag.values {
                    // CR-07 / T-03-CR07-I: strfry's 32-byte-id decode rule.
                    // Decode ONLY when value is exactly 64 lowercase-hex chars.
                    let value_bytes: Vec<u8> = if value.len() == 64
                        && value.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f'))
                    {
                        // Safe to unwrap: we've verified all bytes are valid lowercase hex.
                        decode_hex(value).unwrap_or_else(|_| value.as_bytes().to_vec())
                    } else {
                        // Raw UTF-8 — this is what strfry stores for non-id tag values.
                        value.as_bytes().to_vec()
                    };
                    let mut k = Vec::with_capacity(1 + value_bytes.len() + 8);
                    k.push(name_byte);
                    k.extend_from_slice(&value_bytes);
                    k.extend_from_slice(&ts_bytes);
                    keys.push(k);
                }
            }
            keys
        }

        SelectedIndex::Single("Event__created_at") => {
            // Single plain ts(8 LE) key (MDB_INTEGERKEY — D-04 default global feed)
            vec![ts_bytes.to_vec()]
        }

        // Unreachable in well-formed code — select_index only returns the six arms above.
        _ => {
            tracing::warn!("build_start_keys called with unknown SelectedIndex variant — returning empty");
            vec![]
        }
    };

    // WR-01 dedup: sort + dedup the produced start-key vector so that duplicate filter
    // values (e.g. authors=[pk1, pk1]) do not produce duplicate start keys. merge_prefixes
    // re-merges by (created_at, lev_id) so start-key order does not affect output order.
    keys.sort();
    keys.dedup();
    keys
}

// ---------------------------------------------------------------------------
// D-06: Extract created_at from key bytes without hydration
// ---------------------------------------------------------------------------

/// Extract `created_at` from the trailing 8 bytes of an `Event__*` key — no hydration.
///
/// All `Event__*` keys end with `created_at(8 LE)` (spec §3.1). This extracts it without
/// opening an LMDB transaction or decoding any payload (D-06 / D-10).
///
/// ## Safety (T-03-HEX)
///
/// Uses `saturating_sub` to avoid underflow on short keys, and `try_into().unwrap_or([0u8;8])`
/// so a key shorter than 8 bytes returns `0` rather than panicking.
pub fn created_at_from_key(key: &[u8]) -> u64 {
    let offset = key.len().saturating_sub(8);
    u64::from_le_bytes(key[offset..].try_into().unwrap_or([0u8; 8]))
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

/// Hex-decode a 32-byte pubkey, emitting `tracing::warn!` and returning `None` on failure.
/// Used by build_start_keys for all pubkey-bearing index paths (T-03-HEX).
fn decode_pubkey_warn(hex_pk: &str) -> Option<[u8; 32]> {
    match decode_hex(hex_pk) {
        Ok(bytes) => match bytes.try_into() {
            Ok(arr) => Some(arr),
            Err(_) => {
                tracing::warn!(
                    hex = hex_pk,
                    "skipping filter value with invalid hex: pubkey must decode to 32 bytes"
                );
                None
            }
        },
        Err(e) => {
            tracing::warn!(
                hex = hex_pk,
                reason = e,
                "skipping filter value with invalid hex"
            );
            None
        }
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

    /// Copy fixture to tempdir and open an env there.
    /// Identical helper to the one in indexes.rs, payload.rs, scan.rs — same established idiom.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    const PK1: &str = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    const PK2: &str = "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5";

    /// Test 1: select_index on a filter with `ids=Some(..)` returns Event__id (highest priority).
    #[test]
    fn test_select_index_ids_highest_priority() {
        // ids alone
        let f = NostrFilter {
            ids: Some(vec!["deadbeef".to_string()]),
            ..Default::default()
        };
        assert_eq!(select_index(&f), SelectedIndex::Single("Event__id"));

        // ids + authors + kinds — ids still wins
        let f2 = NostrFilter {
            ids: Some(vec!["deadbeef".to_string()]),
            authors: Some(vec![PK1.to_string()]),
            kinds: Some(vec![1]),
            ..Default::default()
        };
        assert_eq!(select_index(&f2), SelectedIndex::Single("Event__id"));
    }

    /// Test 2: all six D-02 arms.
    #[test]
    fn test_select_index_all_six_arms() {
        // 1. ids (already in test_select_index_ids_highest_priority)
        // 2. authors + kinds → Event__pubkeyKind
        let f = NostrFilter {
            authors: Some(vec![PK1.to_string()]),
            kinds: Some(vec![1]),
            ..Default::default()
        };
        assert_eq!(select_index(&f), SelectedIndex::Multi("Event__pubkeyKind"));

        // 3. authors only → Event__pubkey
        let f = NostrFilter {
            authors: Some(vec![PK1.to_string()]),
            ..Default::default()
        };
        assert_eq!(select_index(&f), SelectedIndex::Multi("Event__pubkey"));

        // 4. kinds only → Event__kind
        let f = NostrFilter {
            kinds: Some(vec![1]),
            ..Default::default()
        };
        assert_eq!(select_index(&f), SelectedIndex::Multi("Event__kind"));

        // 5. tags only → Event__tag
        let f = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "e".to_string(),
                values: vec!["deadbeef".to_string()],
            }]),
            ..Default::default()
        };
        assert_eq!(select_index(&f), SelectedIndex::Multi("Event__tag"));

        // 6. all None → Event__created_at (D-04 default feed)
        let f = NostrFilter::default();
        assert_eq!(select_index(&f), SelectedIndex::Single("Event__created_at"));
    }

    /// Test 3: kinds=[1] filter with until=1720000000 → reverse start_key trailing 8 bytes = 1720000000 LE,
    /// and leading 8 bytes = 1_u64 (kind=1) LE. (D-03 pushes until into the created_at bound.)
    #[test]
    fn test_build_start_keys_kind_reverse_until_bound() {
        let f = NostrFilter {
            kinds: Some(vec![1]),
            until: Some(1720000000),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__kind");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(keys.len(), 1, "one kind → one start_key");
        let key = &keys[0];
        assert_eq!(key.len(), 16, "Event__kind key must be 16 bytes: kind(8) ‖ ts(8)");

        // Leading 8 bytes = kind=1 as LE u64
        let kind_bytes: [u8; 8] = key[0..8].try_into().unwrap();
        assert_eq!(
            u64::from_le_bytes(kind_bytes),
            1u64,
            "leading 8 bytes must encode kind=1"
        );

        // Trailing 8 bytes = until=1720000000 as LE u64
        let ts_bytes: [u8; 8] = key[8..16].try_into().unwrap();
        assert_eq!(
            u64::from_le_bytes(ts_bytes),
            1720000000u64,
            "trailing 8 bytes must encode until=1720000000 (D-03)"
        );
    }

    /// Test 4: authors=[pk1] kinds=[1] → Event__pubkeyKind start_key is 48 bytes:
    /// pubkey(32 raw) ‖ kind(8 LE) ‖ created_at(8 LE).
    #[test]
    fn test_build_start_keys_pubkey_kind_48_bytes() {
        let f = NostrFilter {
            authors: Some(vec![PK1.to_string()]),
            kinds: Some(vec![1]),
            until: Some(1720000000),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__pubkeyKind");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(keys.len(), 1, "one (author,kind) pair → one start_key");
        let key = &keys[0];
        assert_eq!(key.len(), 48, "Event__pubkeyKind key must be 48 bytes: pubkey(32) ‖ kind(8) ‖ ts(8)");

        // Verify pubkey bytes
        let expected_pk = decode_hex(PK1).unwrap();
        assert_eq!(&key[0..32], expected_pk.as_slice(), "leading 32 bytes must be pk1 raw bytes");

        // Verify kind bytes
        let kind_bytes: [u8; 8] = key[32..40].try_into().unwrap();
        assert_eq!(u64::from_le_bytes(kind_bytes), 1u64, "bytes 32..40 must encode kind=1");

        // Verify ts bytes
        let ts_bytes: [u8; 8] = key[40..48].try_into().unwrap();
        assert_eq!(
            u64::from_le_bytes(ts_bytes),
            1720000000u64,
            "trailing bytes 40..48 must encode until=1720000000"
        );
    }

    /// Additional: created_at_from_key never panics on short keys.
    #[test]
    fn test_created_at_from_key_short_key_no_panic() {
        // empty key → 0
        assert_eq!(created_at_from_key(&[]), 0u64);
        // 4-byte key → 0 (< 8 bytes)
        assert_eq!(created_at_from_key(&[1, 2, 3, 4]), 0u64);
        // exactly 8 bytes → decoded value
        let ts = 1720000000u64;
        let key = ts.to_le_bytes().to_vec();
        assert_eq!(created_at_from_key(&key), ts);
        // 16-byte key → trailing 8 bytes
        let mut k16 = vec![0u8; 8];
        k16.extend_from_slice(&ts.to_le_bytes());
        assert_eq!(created_at_from_key(&k16), ts);
    }

    /// Test 5 (WR-01): duplicate authors produce exactly one start key after dedup.
    ///
    /// `build_start_keys` with authors=[PK1, PK1] must return exactly 1 key — not 2.
    /// Without dedup, merge_prefixes would scan the same prefix twice, doubling every
    /// result. This is the WR-01 regression guard.
    #[test]
    fn test_build_start_keys_dedup_duplicate_authors() {
        // Duplicate PK1 in authors → should produce exactly 1 start key.
        let f = NostrFilter {
            authors: Some(vec![PK1.to_string(), PK1.to_string()]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__pubkey");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(
            keys.len(),
            1,
            "duplicate authors must produce exactly 1 start key after dedup (WR-01), got {}",
            keys.len()
        );

        // Same test for duplicate kinds.
        let f2 = NostrFilter {
            kinds: Some(vec![1, 1, 1]),
            ..Default::default()
        };
        let selected2 = SelectedIndex::Multi("Event__kind");
        let keys2 = build_start_keys(&f2, &selected2, ScanDirection::Reverse);
        assert_eq!(
            keys2.len(),
            1,
            "duplicate kinds must produce exactly 1 start key after dedup, got {}",
            keys2.len()
        );

        // Distinct values must still produce distinct keys.
        let f3 = NostrFilter {
            authors: Some(vec![PK1.to_string(), PK2.to_string()]),
            ..Default::default()
        };
        let selected3 = SelectedIndex::Multi("Event__pubkey");
        let keys3 = build_start_keys(&f3, &selected3, ScanDirection::Reverse);
        assert_eq!(
            keys3.len(),
            2,
            "distinct authors must produce 2 start keys, got {}",
            keys3.len()
        );
    }

    // -----------------------------------------------------------------------
    // CR-07 / WR-04 regression tests (plan 03-07 gap closure)
    // -----------------------------------------------------------------------

    /// CR-07: a literal short hex-looking tag value ("beef") must use raw UTF-8 bytes,
    /// NOT hex-decoded bytes (0xBE 0xEF). Only 64-char lowercase hex values are decoded.
    ///
    /// This test verifies that `build_start_keys` for a tag filter with value="beef"
    /// produces a key whose value segment (bytes 1..key.len()-8) equals b"beef" (4 UTF-8 bytes),
    /// not [0xBE, 0xEF] (2 hex-decoded bytes). Getting this wrong silently misses real matches
    /// because strfry stores the tag value as raw UTF-8 in its index.
    #[test]
    fn test_build_start_keys_tag_literal_hex_not_decoded() {
        let f = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "t".to_string(),
                values: vec!["beef".to_string()],
            }]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__tag");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(keys.len(), 1, "one tag value → one start key");
        let key = &keys[0];
        // key layout: name_byte(1) ‖ value_bytes(var) ‖ ts(8)
        // For "beef" (raw UTF-8): value_bytes = [0x62, 0x65, 0x65, 0x66] (4 bytes)
        // Total key length = 1 + 4 + 8 = 13
        assert_eq!(
            key.len(),
            13,
            "key must be 13 bytes (1 name + 4 UTF-8 'beef' bytes + 8 ts): got {} bytes",
            key.len()
        );
        // name byte = 't' = 0x74
        assert_eq!(key[0], b't', "name byte must be 't'");
        // value segment = raw UTF-8 of "beef" = [0x62, 0x65, 0x65, 0x66]
        assert_eq!(
            &key[1..5],
            b"beef",
            "value segment must be raw UTF-8 bytes of 'beef', not hex-decoded [0xBE, 0xEF]"
        );
        // Confirm NOT hex-decoded: if it were, the value would be 2 bytes [0xBE, 0xEF]
        // and total key length would be 11 (1 + 2 + 8), which we already reject above.
    }

    /// CR-07: an uppercase 64-char hex value must NOT be decoded as a 32-byte id.
    ///
    /// strfry's rule is lowercase-only for 32-byte id values. An uppercase hex string
    /// that looks like a 64-char event id must be stored as raw UTF-8 in the tag filter
    /// start key (because strfry would NOT decode it to binary either).
    #[test]
    fn test_build_start_keys_tag_uppercase_hex_not_decoded() {
        // 64-char string with uppercase hex characters — must NOT be decoded.
        let uppercase_hex = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
        assert_eq!(uppercase_hex.len(), 64, "test value must be exactly 64 chars");
        assert!(
            uppercase_hex.bytes().all(|b| b == b'A'),
            "test value must be all uppercase A"
        );

        let f = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "e".to_string(),
                values: vec![uppercase_hex.to_string()],
            }]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__tag");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(keys.len(), 1, "one tag value → one start key");
        let key = &keys[0];
        // If NOT decoded: value_bytes = 64 UTF-8 bytes, total = 1 + 64 + 8 = 73
        // If incorrectly decoded: value_bytes = 32 binary bytes, total = 1 + 32 + 8 = 41
        assert_eq!(
            key.len(),
            73,
            "uppercase 64-char hex must be raw UTF-8 (73 bytes), not decoded binary (41 bytes): got {}",
            key.len()
        );
        // Value segment must be the raw uppercase ASCII bytes.
        assert_eq!(
            &key[1..65],
            uppercase_hex.as_bytes(),
            "value segment must be raw UTF-8 of the uppercase string"
        );
    }

    /// WR-04: a multi-character tag name must produce ZERO start keys (skipped with warn).
    ///
    /// Nostr protocol tag names are single-letter. A multi-char name would either silently
    /// scan the wrong prefix (old first-byte truncation) or produce meaningless results.
    /// The correct behavior is to skip the tag entirely and emit zero start keys.
    #[test]
    fn test_build_start_keys_tag_multichar_name_skipped() {
        let f = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "emoji".to_string(),
                values: vec!["somevalue".to_string()],
            }]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__tag");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(
            keys.len(),
            0,
            "multi-char tag name 'emoji' must produce zero start keys (WR-04), got {}",
            keys.len()
        );
    }

    /// WR-04: an empty tag name must produce ZERO start keys (skipped with warn).
    ///
    /// An empty name would also be invalid for a scan (no byte to use as the prefix).
    #[test]
    fn test_build_start_keys_tag_empty_name_skipped() {
        let f = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "".to_string(),
                values: vec!["somevalue".to_string()],
            }]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__tag");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(
            keys.len(),
            0,
            "empty tag name must produce zero start keys (WR-04), got {}",
            keys.len()
        );
    }

    /// CR-07 positive case: the existing 64-char lowercase hex tag value (64 'a' chars)
    /// must STILL be hex-decoded to 32 raw bytes for correct index key construction.
    ///
    /// This is the existing test scenario from the fixture — levIds 6, 8, 11 carry
    /// tag e=aaaa...aa (64 lowercase hex 'a' chars), which strfry stores as 32 bytes
    /// of 0xAA. The start key must hex-decode the value to match strfry's key format.
    #[test]
    fn test_build_start_keys_tag_64char_lowercase_hex_decoded() {
        let tag_value_64a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
        assert_eq!(tag_value_64a.len(), 64, "test value must be exactly 64 chars");

        let f = NostrFilter {
            tags: Some(vec![TagFilter {
                name: "e".to_string(),
                values: vec![tag_value_64a.to_string()],
            }]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__tag");
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(keys.len(), 1, "one tag value → one start key");
        let key = &keys[0];
        // If CORRECTLY decoded: value_bytes = 32 raw bytes, total = 1 + 32 + 8 = 41
        assert_eq!(
            key.len(),
            41,
            "64-char lowercase hex must be hex-decoded to 32 bytes (41 bytes total), got {}",
            key.len()
        );
        // All 32 value bytes must be 0xAA (hex "aa" repeated)
        assert!(
            key[1..33].iter().all(|&b| b == 0xAA),
            "value segment must be 32 bytes of 0xAA (decoded from 'aaa...aaa')"
        );
    }

    /// Fixture smoke test: build_start_keys for pk1 reverse produces a key that finds events in LMDB.
    #[test]
    fn test_build_start_keys_pubkey_finds_events_in_fixture() {
        let (env, _tmp) = open_temp_fixture_env();
        let f = NostrFilter {
            authors: Some(vec![PK1.to_string()]),
            ..Default::default()
        };
        let selected = select_index(&f);
        assert_eq!(selected, SelectedIndex::Multi("Event__pubkey"));
        let keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(keys.len(), 1);
        // Verify the key actually finds events when passed to scan_index_bounded
        let results = crate::lmdb::scan::scan_index_bounded(
            &env,
            "Event__pubkey",
            ScanDirection::Reverse,
            &keys[0],
            5,
        ).expect("scan must not error");
        // pk1 has 9 events total; limit=5 should return 5
        assert_eq!(results.len(), 5, "pk1 scan with limit=5 must return 5 events");
        // Verify created_at values are non-increasing (newest-first)
        let mut prev = u64::MAX;
        for (key_bytes, _lev_id) in &results {
            let ts = created_at_from_key(key_bytes);
            assert!(ts <= prev, "created_at must be non-increasing (newest-first), got ts={} after prev={}", ts, prev);
            prev = ts;
        }
    }
}

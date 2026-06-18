/// filter.rs — Engine-facing input/contract types for the Phase-3 query engine.
///
/// This module defines the public type contracts that every other Phase-3 plan builds against:
/// - `NostrFilter` / `TagFilter`: structured input from the Phase-4 GraphQL resolvers (D-01..D-04).
/// - `PageCursor`: opaque pagination cursor encoding `(created_at, lev_id)` (D-10, D-11).
/// - `QueryError`: unified error boundary for the engine (T-03-CUR, thiserror house style).
///
/// None of these types are deserialized from LMDB — they are constructed by Phase-4 resolvers.
/// `PageCursor` is the sole exception: it is decoded from an untrusted caller-supplied string
/// and must fail-closed on malformed input (T-03-CUR, requirement QRY-01).

use base64::{engine::general_purpose::STANDARD, Engine as _};

// ---------------------------------------------------------------------------
// Input filter types (D-01..D-04)
// ---------------------------------------------------------------------------

/// NIP-01 REQ filter — the query engine's input contract (D-01..D-04).
///
/// All fields are `Option` because every filter predicate is optional; an all-`None`
/// filter is valid (triggers the default reverse `Event__created_at` walk, D-04).
///
/// Field routing per D-02 (most selective first):
///   - `ids`                     → `Event__id` index scan
///   - `authors` AND `kinds`     → `Event__pubkeyKind` index scan (most selective combined)
///   - `authors` only            → `Event__pubkey` index scan
///   - `kinds` only              → `Event__kind` index scan
///   - `tags`                    → `Event__tag` index scan (QRY-02)
///   - all None / non-indexable  → `Event__created_at` reverse walk (D-04 default feed)
///
/// `since`/`until` are pushed into scan bounds (D-03) rather than applied as residual
/// predicates — all `Event__*` keys carry trailing `created_at(8 LE)`, enabling bound pushdown.
///
/// Constructed by Phase-4 GraphQL resolvers. NOT deserialized from LMDB bytes.
#[derive(Debug, Clone, Default)]
pub struct NostrFilter {
    /// Event ids to match (hex strings). Routes to `Event__id` (D-02).
    pub ids: Option<Vec<String>>,

    /// Author pubkeys to match (hex strings). Routes to `Event__pubkey` or
    /// `Event__pubkeyKind` depending on whether `kinds` is also set (D-02).
    pub authors: Option<Vec<String>>,

    /// Event kinds to match. Routes to `Event__kind` or `Event__pubkeyKind` (D-02).
    pub kinds: Option<Vec<u64>>,

    /// Tag predicates — e.g. `#e` / `#p`. Routes to `Event__tag` (D-02, QRY-02).
    /// Each `TagFilter` matches one `#<name>` predicate with one or more values.
    pub tags: Option<Vec<TagFilter>>,

    /// Include events at or after this Unix timestamp (seconds).
    /// Pushed into scan bounds (D-03) — not a residual predicate.
    pub since: Option<u64>,

    /// Include events at or before this Unix timestamp (seconds).
    /// Pushed into scan bounds (D-03) — not a residual predicate.
    pub until: Option<u64>,

    /// Maximum number of events to return. `0` → uses `DEFAULT_WINDOW_SIZE` (256) as the
    /// per-prefix scan limit (D-04 engine side). Phase 4 enforces the hard ceiling before
    /// calling the engine.
    pub limit: usize,
}

/// A single tag predicate: `#<tag_name>` with one or more values (QRY-02).
///
/// Example: `#e` filter with one value → `TagFilter { name: "e", values: ["deadbeef..."] }`.
/// Matched against `tags[i][0]` (tag name) and `tags[i][1]` (tag value) in the decoded event.
#[derive(Debug, Clone)]
pub struct TagFilter {
    /// Single-character tag name (e.g. `"e"`, `"p"`).
    pub name: String,

    /// One or more values to match against `tags[i][1]`.
    pub values: Vec<String>,
}

// ---------------------------------------------------------------------------
// Pagination cursor (D-10, D-11)
// ---------------------------------------------------------------------------

/// Opaque pagination cursor — encodes the last emitted `(created_at, lev_id)` pair (D-11).
///
/// **Encoding:** `base64(created_at(8 LE) ‖ lev_id(8 LE))` — 16 raw bytes, 24 base64 chars.
/// Consumers treat this as an opaque blob and never inspect the internals.
///
/// The engine decodes it to construct the `start_key` bytes for the next page's scan
/// using Phase-2's `Bound::Excluded` resume mechanism (plan 02-03 D-06).
///
/// NOT a GraphQL type — Phase 4 wraps this in a Connection cursor type.
///
/// **Security (T-03-CUR):** cursor strings originate from Phase-4 callers and are treated
/// as untrusted input. `decode` fails closed: base64 decode failure OR wrong length returns
/// `QueryError::CursorDecode`, never panics or produces out-of-bounds access.
#[derive(Debug, Clone)]
pub struct PageCursor {
    /// `created_at` timestamp (Unix seconds) of the last emitted event (D-10).
    pub created_at: u64,

    /// `levId` of the last emitted event — tie-breaker in the `(created_at DESC, levId DESC)`
    /// total order (D-10). NOT chronological; monotonic insertion counter (spec §3.4).
    pub lev_id: u64,
}

impl PageCursor {
    /// Encode as an opaque base64 blob — consumers never inspect internals (D-11).
    ///
    /// Layout: `created_at(8 LE) ‖ lev_id(8 LE)` → 16 raw bytes → 24-char base64 string.
    pub fn encode(&self) -> String {
        let mut buf = [0u8; 16];
        buf[0..8].copy_from_slice(&self.created_at.to_le_bytes());
        buf[8..16].copy_from_slice(&self.lev_id.to_le_bytes());
        STANDARD.encode(buf)
    }

    /// Decode from an opaque blob. Returns `QueryError::CursorDecode` on any malformed input.
    ///
    /// Fail-closed contract (T-03-CUR):
    ///   - malformed base64 → `Err(QueryError::CursorDecode { .. })`
    ///   - wrong byte length (≠ 16) → `Err(QueryError::CursorDecode { .. })`
    ///   - valid 16 bytes → `Ok(PageCursor { created_at, lev_id })`
    ///
    /// Never panics on untrusted input (no `unwrap` on the decode path except on
    /// `try_into` after a length-exact slice, which cannot fail).
    pub fn decode(s: &str) -> Result<Self, QueryError> {
        let bytes = STANDARD.decode(s).map_err(|e| QueryError::CursorDecode {
            reason: e.to_string(),
        })?;
        if bytes.len() != 16 {
            return Err(QueryError::CursorDecode {
                reason: format!("expected 16 bytes, got {}", bytes.len()),
            });
        }
        // SAFETY: length is exactly 16; both try_into calls are on 8-byte slices — cannot fail.
        Ok(PageCursor {
            created_at: u64::from_le_bytes(bytes[0..8].try_into().unwrap()),
            lev_id: u64::from_le_bytes(bytes[8..16].try_into().unwrap()),
        })
    }
}

// ---------------------------------------------------------------------------
// Error type (thiserror house style, mirrors IndexError from indexes.rs)
// ---------------------------------------------------------------------------

/// Unified error boundary for the Phase-3 query engine.
///
/// Follows the thiserror house style established in `src/lmdb/indexes.rs`:
/// `#[from]` for upstream error kinds, named-field struct variants for context.
///
/// Variants:
/// - `Lmdb` — propagated from LMDB / heed scan primitives via `IndexError`.
/// - `Payload` — propagated from payload decode / hydration via `PayloadError`.
/// - `CursorDecode` — fail-closed boundary for untrusted cursor bytes (T-03-CUR).
#[derive(Debug, thiserror::Error)]
pub enum QueryError {
    /// Underlying LMDB / heed error propagated from scan or hydrate.
    #[error("LMDB error: {0}")]
    Lmdb(#[from] crate::lmdb::indexes::IndexError),

    /// Payload decode / hydration error propagated from payload.rs.
    #[error("Payload error: {0}")]
    Payload(#[from] crate::lmdb::payload::PayloadError),

    /// Cursor bytes could not be decoded — fail-closed boundary (T-03-CUR).
    ///
    /// Produced by `PageCursor::decode` on malformed base64 or wrong byte length.
    /// Phase-4 resolvers must handle this and return a GraphQL error, never panic.
    #[error("Cursor decode error: {reason}")]
    CursorDecode { reason: String },
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    /// Test 1 (D-04): `NostrFilter::default()` constructs an all-None/empty filter.
    ///
    /// Proves the default empty-filter path (D-04: default global feed) is representable.
    #[test]
    fn test_nostr_filter_default_all_none() {
        let f = NostrFilter::default();
        assert!(f.ids.is_none());
        assert!(f.authors.is_none());
        assert!(f.kinds.is_none());
        assert!(f.tags.is_none());
        assert!(f.since.is_none());
        assert!(f.until.is_none());
        assert_eq!(f.limit, 0);
    }

    /// Test 2 (D-11): `PageCursor::encode()` then `PageCursor::decode()` round-trips.
    ///
    /// Ensures `(created_at, lev_id)` is exactly preserved through base64 encode/decode.
    #[test]
    fn test_page_cursor_round_trip() {
        let original = PageCursor {
            created_at: 1720000000,
            lev_id: 11,
        };
        let encoded = original.encode();
        let decoded = PageCursor::decode(&encoded).expect("round-trip should succeed");
        assert_eq!(decoded.created_at, original.created_at);
        assert_eq!(decoded.lev_id, original.lev_id);
    }

    /// Test 3 (T-03-CUR): malformed base64 input returns `QueryError::CursorDecode`, no panic.
    #[test]
    fn test_page_cursor_decode_malformed_base64() {
        let result = PageCursor::decode("not!base64!!");
        assert!(
            matches!(result, Err(QueryError::CursorDecode { .. })),
            "Expected CursorDecode error, got: {:?}",
            result
        );
    }

    /// Test 4 (T-03-CUR): base64 that decodes to wrong byte length returns `QueryError::CursorDecode`, no panic.
    ///
    /// base64 of 3 bytes → 4 base64 chars; decoded length = 3, not 16.
    #[test]
    fn test_page_cursor_decode_wrong_length() {
        // base64-encode exactly 3 bytes — decodes to 3 bytes, not 16.
        let short = base64::engine::general_purpose::STANDARD.encode([0xAB, 0xCD, 0xEF]);
        let result = PageCursor::decode(&short);
        assert!(
            matches!(result, Err(QueryError::CursorDecode { .. })),
            "Expected CursorDecode error for wrong-length input, got: {:?}",
            result
        );
    }
}

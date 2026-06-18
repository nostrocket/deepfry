/// types.rs — shared LMDB domain types for lmdb2graphql.
///
/// These types are used across the lmdb module and exported for use by the
/// GraphQL layer and self-check gate in later phases.

/// Internal event identifier in strfry.
///
/// `levId` ("Local EVent ID") is an auto-incrementing uint64 primary key.
/// It is monotonic (newer events get larger levIds) but NOT chronological
/// (events arrive out-of-order relative to created_at). It is the key of
/// both the `Event` and `EventPayload` sub-DBs (spec §3.4).
///
/// In every `Event__*` index sub-DB:
///   - The KEY is the composite field (e.g. `pubkey(32) ‖ created_at(8 LE)`)
///   - The VALUE is the 8-byte little-endian levId
///
/// This is the levId extraction contract used by the self-check oracle (plan 01-02
/// Task 5, plan 01-03 Task 3).
pub type LevId = u64;

/// Parsed record from the strfry `Meta` sub-DB.
///
/// strfry stores a single Meta record (id=1) as a FlatBuffers-encoded table.
/// The fields are parsed from the FlatBuffer at verified byte offsets.
///
/// ## SPIKE A3 — Verified field layout
///
/// The actual Meta LMDB value is a FlatBuffers table (48 bytes for the fixture).
/// Sub-DB name: `rasgueadb_defaultDb__Meta`
///
/// Raw value hex (from fixture probe, 2026-06-10):
/// `14 00 00 00 00 00 00 00 00 00 0a 00 1c 00 14 00 0c 00 04 00 0a 00 00 00 01 00
///  00 00 00 00 00 00 01 00 00 00 00 00 00 00 03 00 00 00 00 00 00 00`
///
/// Layout:
///   - bytes[0..4]:   root_offset = 20 (FlatBuffers root offset)
///   - bytes[4..10]:  padding/alignment (6 bytes of zeros)
///   - bytes[10..20]: FlatBuffers vtable (vtsize=10, objsize=28, 3 field slots: [20, 12, 4])
///   - bytes[20..48]: FlatBuffers table data (28 bytes)
///     - table_abs + 4  = byte 24: negentropyModificationCounter (u64 LE)
///     - table_abs + 12 = byte 32: endianness (u32 LE)
///     - table_abs + 20 = byte 40: dbVersion (u32 LE)
///
/// strfry source (onAppStartup.cpp line 63):
///   `env.insert_Meta(txn, CURR_DB_VERSION, endianness=1, negentropyModCounter=1);`
///   endianness=1 means little-endian (strfry checks `s->endianness() != 1` for mismatch).
///
/// Source: SPIKE A3 probe against fixture sha256:8b871be8...
///   confirmed by /Users/gareth/git/nostrocket/strfry/strfry/src/onAppStartup.cpp:
///   `if (s->endianness() != 1) throw herr("DB was created on a machine with different endianness");`
#[derive(Debug, Clone)]
pub struct MetaRecord {
    /// DB version written by strfry. Must equal `CURR_DB_VERSION = 3`.
    /// From FlatBuffer at absolute byte offset 40 in the raw LMDB value.
    pub db_version: u32,

    /// DB endianness marker: 1 = little-endian, anything else = mismatch.
    /// strfry sets this to 1 unconditionally (strfry targets x86-64/arm64 = LE only).
    /// From FlatBuffer at absolute byte offset 32 in the raw LMDB value.
    pub endianness: u32,

    /// Monotonic counter bumped on any event add/delete.
    /// Useful as a cheap "did anything change?" probe (spec §4.2 O7).
    /// From FlatBuffer at absolute byte offset 24 in the raw LMDB value (u64 LE).
    pub negentropy_mod_counter: u64,
}

/// Decoded Nostr event struct — deserialized from an `EventPayload` value (spec §3.2, D-01..D-04).
///
/// Deserialization is LENIENT: the 7 known fields are required and typed; unknown top-level
/// fields are silently ignored (the serde default — the strict unknown-field guard is
/// deliberately absent here). This is
/// intentional for forward-compatibility with events strfry accepted that carry extra
/// top-level fields (D-02). A payload missing any of the 7 required fields fails to
/// deserialize (returns a serde error rather than a defaulted value).
///
/// Field typing follows spec §3.2 / NIP-01:
///   - `id`, `pubkey`, `sig`: lowercase hex strings (validated upstream by strfry, not here)
///   - `created_at`, `kind`: unsigned integers
///   - `tags`: a list of string lists — `Vec<Vec<String>>` (D-03), so Phase 3 tag scans and
///     NIP-40 expiration parsing can index `tags[i][0]` (tag name) / `tags[i][1]` (value)
///   - `content`: arbitrary UTF-8 string
///
/// Do NOT use the `nostr` crate — use `serde_json` + this local struct (D-04, CLAUDE.md).
/// Signatures are NOT re-verified on the decode path: strfry already validated the event on
/// ingest (D-04), and decoded field values remain UNTRUSTED data for downstream phases (D-12).
#[derive(Debug, Clone, serde::Deserialize)]
pub struct NostrEvent {
    /// 32-byte event id as a 64-char lowercase hex string (NIP-01).
    pub id: String,

    /// 32-byte author public key as a 64-char lowercase hex string (NIP-01).
    pub pubkey: String,

    /// Unix timestamp (seconds) the author claims the event was created at.
    pub created_at: u64,

    /// Nostr event kind (NIP-01); e.g. 0 = metadata, 1 = text note, 3 = contacts.
    pub kind: u64,

    /// Event tags as a list of string lists (D-03). `tags[i][0]` is the tag name,
    /// `tags[i][1..]` the tag values. Typed for Phase 3 tag scans / NIP-40 expiration.
    pub tags: Vec<Vec<String>>,

    /// Arbitrary event content (interpretation depends on `kind`).
    pub content: String,

    /// 64-byte Schnorr signature as a 128-char lowercase hex string (NIP-01).
    pub sig: String,
    // D-02: lenient, forward-compatible — the strict unknown-field guard is intentionally
    // omitted so events carrying extra top-level fields still deserialize.
}

/// Output of a single `EventPayload` decode — both the typed struct AND the retained raw
/// JSON bytes (D-01). One decode produces both, so no consumer ever double-parses the payload.
///
/// - `event` gives Phase 3 typed field access (filter routing, latestPerAuthor, NIP-40).
/// - `raw_json` gives Phase 4 an exact passthrough field WITHOUT re-serializing (re-serializing
///   would not be byte-identical to what strfry stored — key order / whitespace differ).
///
/// `raw_json` is an owned `Vec<u8>` because heed LMDB byte slices are only valid for the read
/// txn lifetime; the payload decoder copies the bytes out before dropping the read txn (D-08).
/// This makes a `DecodedEvent` fully txn-independent and safe to return from a resolver.
#[derive(Debug, Clone)]
pub struct DecodedEvent {
    /// The typed, deserialized event (lenient — see `NostrEvent`).
    pub event: NostrEvent,

    /// The exact JSON bytes of the event as stored by strfry, with the leading type-tag byte
    /// stripped (i.e. `payload[1..]` for a 0x00 payload, or the decompressed frame for 0x01).
    /// Owned + txn-independent — for Phase 4 exact passthrough (no re-serialize).
    pub raw_json: Vec<u8>,
}

#[cfg(test)]
mod tests {
    use super::*;

    /// D-02 lenient: a JSON object carrying all 7 known fields PLUS an extra unknown
    /// top-level field deserializes successfully; the unknown field is ignored.
    #[test]
    fn test_nostr_event_ignores_unknown_fields() {
        let json = r#"{
            "id": "1bdede2c",
            "pubkey": "abc",
            "created_at": 1700000000,
            "kind": 1,
            "tags": [["e", "deadbeef"]],
            "content": "hello",
            "sig": "ff",
            "some_future_field": {"nested": true},
            "another_unknown": 42
        }"#;
        let ev: NostrEvent =
            serde_json::from_str(json).expect("lenient deserialize must ignore unknown fields");
        assert_eq!(ev.id, "1bdede2c");
        assert_eq!(ev.kind, 1);
        assert_eq!(ev.content, "hello");
    }

    /// A JSON object missing a required field (here `sig`) must FAIL to deserialize
    /// (serde error), NOT silently default. Proves the 7 fields are required.
    #[test]
    fn test_nostr_event_missing_required_field_errors() {
        let json = r#"{
            "id": "1bdede2c",
            "pubkey": "abc",
            "created_at": 1700000000,
            "kind": 1,
            "tags": [],
            "content": "hello"
        }"#;
        let result: Result<NostrEvent, _> = serde_json::from_str(json);
        assert!(
            result.is_err(),
            "missing required field `sig` must produce a serde error, not a default"
        );
    }

    /// D-03: `tags` deserializes as `Vec<Vec<String>>` from a nested-array JSON value.
    #[test]
    fn test_nostr_event_tags_are_vec_vec_string() {
        let json = r#"{
            "id": "x",
            "pubkey": "y",
            "created_at": 1,
            "kind": 1,
            "tags": [["e", "deadbeef", "wss://relay"], ["p", "cafef00d"]],
            "content": "",
            "sig": "z"
        }"#;
        let ev: NostrEvent = serde_json::from_str(json).expect("typed-tags deserialize");
        assert_eq!(ev.tags.len(), 2);
        assert_eq!(ev.tags[0], vec!["e", "deadbeef", "wss://relay"]);
        assert_eq!(ev.tags[1], vec!["p", "cafef00d"]);
        // Compile-time proof of the element type: each tag is a Vec<String>.
        let _name: &String = &ev.tags[0][0];
    }
}

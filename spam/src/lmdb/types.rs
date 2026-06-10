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

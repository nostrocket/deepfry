/// meta.rs — Read and validate the strfry Meta record.
///
/// strfry stores a single Meta record (id=1) in the `rasgueadb_defaultDb__Meta` sub-DB.
/// The value is a FlatBuffers-encoded table with three fields:
///   - dbVersion (u32): must equal CURR_DB_VERSION = 3
///   - endianness (u32): 1 = little-endian (strfry checks != 1 for mismatch)
///   - negentropyModificationCounter (u64): change-detection probe
///
/// ## SPIKE A3 RESOLVED — Verified byte offsets in the LMDB value
///
/// The raw LMDB value for the Meta record is FlatBuffers-encoded (48 bytes for the fixture).
/// Absolute byte offsets into the raw value:
///   - byte 24: negentropyModificationCounter (u64 LE)  — FlatBuffer field at vtable offset 4
///   - byte 32: endianness (u32 LE)                     — FlatBuffer field at vtable offset 12
///   - byte 40: dbVersion (u32 LE)                      — FlatBuffer field at vtable offset 20
///
/// The parser reads these via a FlatBuffers vtable walk (not hardcoded absolute offsets),
/// verified at runtime against the fixture. See MetaRecord (types.rs) for full layout diagram.
///
/// ## Sub-DB naming
///
/// strfry (golpe/rasgueadb) prefixes all sub-DB names with `rasgueadb_defaultDb__`.
/// The actual name is: `rasgueadb_defaultDb__Meta`.
/// Discovered via probe of the fixture's unnamed root DB (plan 01-03 Task 1).
use crate::lmdb::types::MetaRecord;
use heed::types::Bytes;

/// Expected `dbVersion` in strfry's LMDB.
/// Source: hoytech/strfry src/constants.h CURR_DB_VERSION = 3
pub const EXPECTED_DB_VERSION: u32 = 3;

/// Endianness marker for little-endian DB.
/// strfry sets endianness=1 for all supported platforms (x86-64, arm64 — all little-endian).
/// Source: strfry onAppStartup.cpp:63 `insert_Meta(txn, CURR_DB_VERSION, 1, 1)`
///         onAppStartup.cpp:68 `if (s->endianness() != 1) throw herr("different endianness")`
pub const STRFRY_LITTLE_ENDIAN_MARKER: u32 = 1;

/// Full sub-DB name for the Meta record in a strfry LMDB env.
/// golpe/rasgueadb adds the `rasgueadb_defaultDb__` prefix to all named sub-DBs.
/// Discovered by iterating the unnamed root DB of the fixture (SPIKE A3 probe, 2026-06-10).
pub const META_DB_NAME: &str = "rasgueadb_defaultDb__Meta";

/// Error type for Meta parsing and gate functions.
#[derive(Debug, thiserror::Error)]
pub enum MetaError {
    #[error("LMDB error opening Meta sub-DB: {0}")]
    Heed(#[from] heed::Error),

    #[error("Meta sub-DB not found — is this a strfry LMDB env? (looked for '{name}')")]
    SubDbNotFound { name: &'static str },

    #[error("Meta record id=1 not found in the Meta sub-DB")]
    RecordNotFound,

    #[error("Meta value too short to parse: got {len} bytes, need at least {need}")]
    ValueTooShort { len: usize, need: usize },

    #[error("Meta FlatBuffer vtable parse error: {0}")]
    VtableError(String),

    /// LMDB-02: dbVersion gate failure
    #[error("strfry dbVersion mismatch: expected {expected}, got {actual} — cannot open this DB")]
    DbVersionMismatch { expected: u32, actual: u32 },

    /// LMDB-03: endianness gate failure
    #[error(
        "DB endianness mismatch: DB endianness marker = {db_endian}, expected {expected_marker} \
         (little-endian). DB may have been written by a big-endian machine."
    )]
    EndiannessMismatch {
        db_endian: u32,
        expected_marker: u32,
    },
}

/// Read the Meta record from the strfry LMDB env.
///
/// Opens `rasgueadb_defaultDb__Meta` with `IntegerComparator` (MDB_INTEGERKEY),
/// fetches record id=1, and parses the FlatBuffer-encoded value.
///
/// # Errors
/// - `MetaError::SubDbNotFound` if the Meta sub-DB does not exist (wrong env)
/// - `MetaError::RecordNotFound` if record id=1 is absent
/// - `MetaError::VtableError` if the FlatBuffer cannot be parsed
pub fn read_meta(env: &heed::Env) -> Result<MetaRecord, MetaError> {
    let rtxn = env.read_txn()?;

    // Open the Meta sub-DB with IntegerComparator (MDB_INTEGERKEY).
    // Note: this is NOT a golpe custom comparator — the Meta key is a plain integer record id.
    let meta_db: heed::Database<Bytes, Bytes, heed::IntegerComparator> = env
        .database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<heed::IntegerComparator>()
        .name(META_DB_NAME)
        .open(&rtxn)?
        .ok_or(MetaError::SubDbNotFound { name: META_DB_NAME })?;

    // Meta key = record id 1, stored as native-endian uint64 (MDB_INTEGERKEY).
    // On a little-endian host: 1u64 = bytes [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
    let key_bytes = 1u64.to_ne_bytes();
    let meta_bytes = meta_db
        .get(&rtxn, key_bytes.as_ref())?
        .ok_or(MetaError::RecordNotFound)?;

    parse_meta_flatbuffer(meta_bytes)
}

/// Parse the FlatBuffer-encoded Meta value.
///
/// ## FlatBuffer layout (SPIKE A3 verified, 2026-06-10)
///
/// The raw LMDB value is a standard FlatBuffers buffer:
///   - bytes[0..4]:   root_offset (u32 LE) — offset from buffer start to table object
///   - bytes[4..root_offset]: alignment padding + vtable
///   - bytes[root_offset..]: table object (soffset_t + field data)
///
/// The vtable has 3 field slots for: negentropyModificationCounter, endianness, dbVersion.
/// Field data is accessed at: table_start_abs + field_offset_from_vtable.
///
/// We parse using the FlatBuffer vtable to be robust to golpe layout changes.
/// Absolute offsets for the fixture are documented in MetaRecord (types.rs).
fn parse_meta_flatbuffer(raw: &[u8]) -> Result<MetaRecord, MetaError> {
    // Minimum viable FlatBuffer: root_offset(4) + soffset_t(4) + vtable_size(2) + vtable_obj(2)
    // + at least one field_offset(2) = 14 bytes. In practice Meta is 48 bytes.
    if raw.len() < 14 {
        return Err(MetaError::ValueTooShort {
            len: raw.len(),
            need: 14,
        });
    }

    // Step 1: Read root_offset — points to the table object within the buffer.
    let root_offset = u32::from_le_bytes(
        raw[0..4].try_into().map_err(|_| MetaError::VtableError("root_offset read".into()))?,
    ) as usize;
    if root_offset + 4 > raw.len() {
        return Err(MetaError::VtableError(format!(
            "root_offset {root_offset} exceeds buffer length {}", raw.len()
        )));
    }

    // Step 2: Read soffset_t at the table object start.
    // The soffset_t gives the position of the vtable relative to the soffset_t's own address.
    // Golpe uses soffset = vtable_abs_position (positive, non-canonical FlatBuffers).
    // Verified: soffset at raw[root_offset] = 10, vtable at raw[10] (not at root_offset + 10).
    let soffset = i32::from_le_bytes(
        raw[root_offset..root_offset + 4]
            .try_into()
            .map_err(|_| MetaError::VtableError("soffset_t read".into()))?,
    );

    // Golpe/rasgueadb stores soffset as the ABSOLUTE position of the vtable in the buffer
    // (not as the signed offset from the table as in canonical FlatBuffers).
    // Verified: root_offset=20, soffset=10, vtable is at raw[10] — NOT at raw[20+10]=raw[30].
    // This is consistent with golpe's internal FlatBuffers implementation.
    let vtable_abs = soffset as usize;
    if vtable_abs + 4 > raw.len() {
        return Err(MetaError::VtableError(format!(
            "vtable at {vtable_abs} exceeds buffer length {}", raw.len()
        )));
    }

    // Step 3: Parse vtable header.
    let vtable_size = u16::from_le_bytes(
        raw[vtable_abs..vtable_abs + 2]
            .try_into()
            .map_err(|_| MetaError::VtableError("vtable_size read".into()))?,
    ) as usize;
    if vtable_size < 4 || vtable_abs + vtable_size > raw.len() {
        return Err(MetaError::VtableError(format!(
            "vtable_size {vtable_size} invalid at position {vtable_abs}"
        )));
    }

    let num_fields = (vtable_size - 4) / 2;
    if num_fields < 3 {
        return Err(MetaError::VtableError(format!(
            "Meta vtable has {num_fields} fields, expected at least 3 (dbVersion, endianness, negentropyModCounter)"
        )));
    }

    // Step 4: Read field offsets from vtable.
    // FlatBuffers field order in vtable matches definition order in golpe.yaml:
    //   field 0 (vtable slot): negentropyModificationCounter (u64)
    //   field 1 (vtable slot): endianness (u32)
    //   field 2 (vtable slot): dbVersion (u32)
    //
    // But SPIKE A3 verified the vtable slot order (field_0 = vtable slot 0, abs=40, value=3=dbVersion).
    // Re-examining: the vtable field order appears to be REVERSE of golpe.yaml definition order
    // (FlatBuffers stores field slots in reverse declaration order):
    //   vtable slot 0 (offset=20 from table): dbVersion = 3
    //   vtable slot 1 (offset=12 from table): endianness = 1
    //   vtable slot 2 (offset=4 from table):  negentropyModCounter = 1 (u64)
    let field_offset_dbversion = u16::from_le_bytes(
        raw[vtable_abs + 4..vtable_abs + 6]
            .try_into()
            .map_err(|_| MetaError::VtableError("field_offset[0] read".into()))?,
    ) as usize;

    let field_offset_endianness = u16::from_le_bytes(
        raw[vtable_abs + 6..vtable_abs + 8]
            .try_into()
            .map_err(|_| MetaError::VtableError("field_offset[1] read".into()))?,
    ) as usize;

    let field_offset_negentropy = u16::from_le_bytes(
        raw[vtable_abs + 8..vtable_abs + 10]
            .try_into()
            .map_err(|_| MetaError::VtableError("field_offset[2] read".into()))?,
    ) as usize;

    // Step 5: Extract field values (fields are at root_offset + field_offset_from_table).
    let db_version = read_u32_at(raw, root_offset, field_offset_dbversion)?;
    let endianness = read_u32_at(raw, root_offset, field_offset_endianness)?;
    let negentropy_mod_counter = read_u64_at(raw, root_offset, field_offset_negentropy)?;

    Ok(MetaRecord {
        db_version,
        endianness,
        negentropy_mod_counter,
    })
}

/// Read a u32 LE from raw at `table_start + field_offset`.
fn read_u32_at(raw: &[u8], table_start: usize, field_offset: usize) -> Result<u32, MetaError> {
    if field_offset == 0 {
        return Ok(0); // FlatBuffers default: absent field = 0
    }
    let abs = table_start + field_offset;
    if abs + 4 > raw.len() {
        return Err(MetaError::VtableError(format!(
            "u32 field at abs {abs} exceeds buffer length {}",
            raw.len()
        )));
    }
    Ok(u32::from_le_bytes(raw[abs..abs + 4].try_into().unwrap()))
}

/// Read a u64 LE from raw at `table_start + field_offset`.
fn read_u64_at(raw: &[u8], table_start: usize, field_offset: usize) -> Result<u64, MetaError> {
    if field_offset == 0 {
        return Ok(0); // FlatBuffers default: absent field = 0
    }
    let abs = table_start + field_offset;
    if abs + 8 > raw.len() {
        return Err(MetaError::VtableError(format!(
            "u64 field at abs {abs} exceeds buffer length {}",
            raw.len()
        )));
    }
    Ok(u64::from_le_bytes(raw[abs..abs + 8].try_into().unwrap()))
}

/// Assert that `meta.db_version == 3`.
///
/// Implements LMDB-02: refuse to start if dbVersion != 3.
/// Returns `Err(MetaError::DbVersionMismatch)` on mismatch.
pub fn assert_db_version(meta: &MetaRecord) -> Result<(), MetaError> {
    if meta.db_version != EXPECTED_DB_VERSION {
        return Err(MetaError::DbVersionMismatch {
            expected: EXPECTED_DB_VERSION,
            actual: meta.db_version,
        });
    }
    Ok(())
}

/// Assert that the DB endianness matches the host endianness.
///
/// Implements LMDB-03: refuse to start on host/DB endianness mismatch.
/// strfry stores endianness=1 for little-endian (all supported platforms).
/// The host is assumed little-endian (x86-64, arm64); this is asserted at compile time via
/// `cfg!(target_endian = "little")`.
///
/// Returns `Err(MetaError::EndiannessMismatch)` if `meta.endianness != STRFRY_LITTLE_ENDIAN_MARKER`.
pub fn assert_endianness(meta: &MetaRecord) -> Result<(), MetaError> {
    // Compile-time: require host is little-endian (x86-64, arm64).
    // This assertion documents the co-location assumption — strfry targets LE only.
    #[cfg(not(target_endian = "little"))]
    compile_error!("lmdb2graphql requires a little-endian host (x86-64 or arm64)");

    if meta.endianness != STRFRY_LITTLE_ENDIAN_MARKER {
        return Err(MetaError::EndiannessMismatch {
            db_endian: meta.endianness,
            expected_marker: STRFRY_LITTLE_ENDIAN_MARKER,
        });
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    /// Copy the committed fixture to a temporary directory and open an env there.
    /// Required because LMDB cannot open the same path twice in the same process
    /// (even read-only). Each test gets its own copy to allow parallel execution.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    /// Read Meta from the committed fixture and assert dbVersion == 3.
    /// This is the positive-control test for the version gate (LMDB-02).
    #[test]
    fn test_read_meta_fixture_dbversion_equals_3() {
        let (env, _tmp) = open_temp_fixture_env();
        let meta = read_meta(&env).expect("read_meta on fixture");
        assert_eq!(
            meta.db_version, 3,
            "fixture Meta.dbVersion must be 3 (LMDB-02 positive control)"
        );
        println!(
            "Meta: db_version={}, endianness={}, negentropy_mod_counter={}",
            meta.db_version, meta.endianness, meta.negentropy_mod_counter
        );
    }

    /// assert_db_version passes for dbVersion == 3.
    #[test]
    fn test_assert_db_version_ok() {
        let meta = MetaRecord {
            db_version: 3,
            endianness: 1,
            negentropy_mod_counter: 0,
        };
        assert!(assert_db_version(&meta).is_ok());
    }

    /// assert_db_version returns Err for dbVersion != 3 (LMDB-02 gate).
    #[test]
    fn test_assert_db_version_err_for_wrong_version() {
        for bad_version in [0u32, 1, 2, 4, 99, u32::MAX] {
            let meta = MetaRecord {
                db_version: bad_version,
                endianness: 1,
                negentropy_mod_counter: 0,
            };
            let result = assert_db_version(&meta);
            assert!(
                result.is_err(),
                "assert_db_version must fail for dbVersion={bad_version}"
            );
            match result.unwrap_err() {
                MetaError::DbVersionMismatch { expected, actual } => {
                    assert_eq!(expected, 3);
                    assert_eq!(actual, bad_version);
                }
                e => panic!("unexpected error variant: {e}"),
            }
        }
    }

    /// assert_endianness passes for endianness == 1 (little-endian DB on little-endian host).
    #[test]
    fn test_assert_endianness_ok() {
        let meta = MetaRecord {
            db_version: 3,
            endianness: 1, // STRFRY_LITTLE_ENDIAN_MARKER
            negentropy_mod_counter: 0,
        };
        assert!(assert_endianness(&meta).is_ok());
    }

    /// assert_endianness returns Err for endianness != 1.
    /// This simulates a DB written by a different endianness machine.
    #[test]
    fn test_assert_endianness_err_for_mismatch() {
        for wrong_endian in [0u32, 2, 255, u32::MAX] {
            let meta = MetaRecord {
                db_version: 3,
                endianness: wrong_endian,
                negentropy_mod_counter: 0,
            };
            let result = assert_endianness(&meta);
            assert!(
                result.is_err(),
                "assert_endianness must fail for endianness={wrong_endian}"
            );
        }
    }

    /// Both gates pass on the committed fixture (integration: live LMDB read).
    #[test]
    fn test_meta_gates_pass_on_fixture() {
        let (env, _tmp) = open_temp_fixture_env();
        let meta = read_meta(&env).expect("read_meta");
        assert_db_version(&meta).expect("assert_db_version on fixture");
        assert_endianness(&meta).expect("assert_endianness on fixture");
    }
}

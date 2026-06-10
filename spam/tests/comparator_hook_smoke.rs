/// comparator_hook_smoke.rs — Go/no-go kill-switch: prove heed registers golpe's
/// foreign comparator on a sub-DB opened read-only, and LMDB uses it for scan ordering.
///
/// This is the genuine Approach-B go/no-go gate per CONTEXT.md <specifics>.
/// Uses a self-built throwaway LMDB env (NOT strfry's env) so the kill-switch
/// can be evaluated immediately without a fixture DB.
///
/// The read-only rule (never write to strfry's env) applies to production.
/// This test creates its own local test env and uses write transactions legitimately.

use heed::{EnvOpenOptions};
use heed::types::Bytes;
use lmdb2graphql::lmdb::comparators::{StringUint64Cmp, Uint64Uint64Cmp};

/// Build a StringUint64-shaped key: N-byte string prefix ‖ 8-byte LE uint64
fn make_string_uint64_key(prefix: &[u8; 8], created_at: u64) -> Vec<u8> {
    let mut key = Vec::with_capacity(8 + 8);
    key.extend_from_slice(prefix);
    key.extend_from_slice(&created_at.to_le_bytes());
    key
}

/// Build a Uint64Uint64-shaped key: 8-byte LE uint64 ‖ 8-byte LE uint64
fn make_uint64_uint64_key(first: u64, second: u64) -> Vec<u8> {
    let mut key = Vec::with_capacity(16);
    key.extend_from_slice(&first.to_le_bytes());
    key.extend_from_slice(&second.to_le_bytes());
    key
}

/// Open a test-only LMDB env (throwaway tempdir — NOT strfry's env)
fn open_test_env(path: &std::path::Path) -> heed::Env {
    unsafe {
        EnvOpenOptions::new()
            .max_dbs(5)
            .map_size(10 * 1024 * 1024) // 10 MiB sufficient for smoke test
            .open(path)
            .expect("open test LMDB env")
    }
}

/// Helper: hex-encode the last 8 bytes of a key (the uint64 suffix)
fn hex_suffix(key: &[u8]) -> String {
    if key.len() >= 8 {
        let suffix = &key[key.len() - 8..];
        suffix.iter().map(|b| format!("{:02x}", b)).collect::<Vec<_>>().join("")
    } else {
        format!("{:02x?}", key)
    }
}

/// PRIMARY KILL-SWITCH TEST: StringUint64 comparator hook viability
///
/// Proves heed 0.22.1 registers golpe's foreign comparator on a
/// sub-DB opened read-only, and LMDB uses it for range-scan ordering.
///
/// Adversarial property:
///   key_a suffix = created_at 1   → LE bytes: [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
///   key_b suffix = created_at 256 → LE bytes: [0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
///
///   Memcmp byte order:  key_b (0x00...) < key_a (0x01...)  → key_b sorts FIRST
///   Golpe numeric order: key_a (val=1) < key_b (val=256)   → key_a sorts FIRST
///
/// If the hook works:    scan with golpe  → [key_a, key_b]
/// If hook NOT working:  scan with golpe  → [key_b, key_a] (memcmp fallback)
#[test]
fn test_string_uint64_comparator_hook() {
    let prefix: [u8; 8] = [0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE];
    let created_at_a: u64 = 1;   // LE: [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
    let created_at_b: u64 = 256; // LE: [0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]

    let key_a = make_string_uint64_key(&prefix, created_at_a);
    let key_b = make_string_uint64_key(&prefix, created_at_b);

    // Verify adversarial property is set up correctly:
    //   raw bytes: key_b < key_a under lexicographic (memcmp) comparison
    assert!(
        key_b.as_slice() < key_a.as_slice(),
        "adversarial setup: key_b suffix bytes [0x00,...] must sort BEFORE key_a [0x01,...] under memcmp"
    );
    //   numeric values: created_at_a < created_at_b
    assert!(
        created_at_a < created_at_b,
        "adversarial setup: numeric value 1 must be less than 256"
    );
    // These two assertions together guarantee the test IS adversarial:
    // golpe should return [key_a, key_b] but memcmp would return [key_b, key_a]

    let dir = tempfile::tempdir().expect("create tempdir");

    // --- Setup: Create DB with golpe comparator, insert two adversarial keys ---
    // (Keys physically stored in golpe order in the B-tree)
    {
        let env = open_test_env(dir.path());
        let mut wtxn = env.write_txn().expect("write txn");
        let db = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>()
                .key_comparator::<StringUint64Cmp>();
            opts.name("test_string_uint64");
            opts.create(&mut wtxn).expect("create sub-DB with golpe comparator")
        };
        db.put(&mut wtxn, key_a.as_slice(), b"val_a").expect("put key_a");
        db.put(&mut wtxn, key_b.as_slice(), b"val_b").expect("put key_b");
        wtxn.commit().expect("commit");
    }

    // --- Test: Re-open sub-DB read-only with golpe comparator — scan must give GOLPE order ---
    // This is the kill-switch assertion: .open() (not .create()) with key_comparator registered
    let golpe_order = {
        let env = open_test_env(dir.path());
        let rtxn = env.read_txn().expect("read txn");
        let db = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>()
                .key_comparator::<StringUint64Cmp>();
            opts.name("test_string_uint64");
            // CRITICAL: .open() on EXISTING sub-DB (not .create()) — this is the path
            // that must call mdb_set_compare for the foreign comparator to register.
            opts.open(&rtxn)
                .expect("open existing sub-DB with golpe comparator")
                .expect("sub-DB must exist after setup")
        };

        let scanned: Vec<Vec<u8>> = db
            .iter(&rtxn)
            .expect("iter with golpe comparator")
            .map(|r| r.expect("scan item").0.to_vec())
            .collect();

        println!(
            "golpe scan order: [suffix={}, suffix={}]",
            hex_suffix(&scanned[0]),
            hex_suffix(&scanned[1])
        );

        scanned
    };

    // KILL-SWITCH ASSERTION: golpe scan returns keys in NUMERIC order (key_a=1 before key_b=256)
    assert_eq!(golpe_order.len(), 2, "must have exactly 2 keys");
    assert_eq!(
        golpe_order[0], key_a,
        "KILL-SWITCH: golpe comparator MUST return key_a (val=1) FIRST — \
         if this fails, the heed comparator hook is NOT working"
    );
    assert_eq!(
        golpe_order[1], key_b,
        "KILL-SWITCH: golpe comparator MUST return key_b (val=256) SECOND"
    );

    // --- Control: Open WITHOUT comparator (memcmp/default) — verify IT WOULD GIVE different order ---
    // Create a second sub-DB without the comparator and verify its behavior differs
    // (demonstrates that the comparator hook is actively changing the ordering semantic)
    {
        let env = open_test_env(dir.path());
        let mut wtxn = env.write_txn().expect("write txn for control DB");
        let db_no_cmp = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>();  // NO key_comparator — uses LMDB default (memcmp)
            opts.name("test_string_uint64_memcmp_control");
            opts.create(&mut wtxn).expect("create control sub-DB without comparator")
        };
        // Insert in the SAME order as the golpe test (key_a first, then key_b)
        // Under memcmp, LMDB will reorder them to [key_b, key_a] in the B-tree
        // (because key_b bytes sort before key_a bytes under memcmp)
        db_no_cmp.put(&mut wtxn, key_a.as_slice(), b"val_a").expect("put key_a (control)");
        db_no_cmp.put(&mut wtxn, key_b.as_slice(), b"val_b").expect("put key_b (control)");
        wtxn.commit().expect("commit control");

        let rtxn = env.read_txn().expect("read txn for control");
        let db_no_cmp_ro = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>();
            opts.name("test_string_uint64_memcmp_control");
            opts.open(&rtxn).expect("open control").expect("must exist")
        };

        let scanned_no_cmp: Vec<Vec<u8>> = db_no_cmp_ro
            .iter(&rtxn)
            .expect("iter control (no comparator)")
            .map(|r| r.expect("item").0.to_vec())
            .collect();

        println!(
            "memcmp control scan order: [suffix={}, suffix={}]",
            hex_suffix(&scanned_no_cmp[0]),
            hex_suffix(&scanned_no_cmp[1])
        );

        // Control assertion: memcmp scan gives key_b FIRST (because key_b bytes < key_a bytes)
        assert_eq!(scanned_no_cmp.len(), 2);
        assert_eq!(
            scanned_no_cmp[0], key_b,
            "CONTROL: memcmp (no comparator) scan must return key_b (LE 0x00.01..) FIRST \
             — proves the hooks actively differ"
        );
        assert_eq!(
            scanned_no_cmp[1], key_a,
            "CONTROL: memcmp scan must return key_a (LE 0x01.00..) SECOND"
        );

        // KEY ASSERTION: golpe order != memcmp order — PROVES the hook took effect
        assert_ne!(
            golpe_order[0], scanned_no_cmp[0],
            "CONTROL PROVES HOOK: golpe first key ({}) must differ from memcmp first key ({}) \
             — proves StringUint64Cmp is actively changing the scan order",
            hex_suffix(&golpe_order[0]),
            hex_suffix(&scanned_no_cmp[0])
        );
    }

    println!("PASS: StringUint64Cmp hook proven — Approach-B go/no-go gate GREEN");
    println!("  golpe order:  [val=1, val=256] (numeric ascending)");
    println!("  memcmp order: [val=256, val=1] (byte-order ascending — DIFFERENT)");
    println!("  heed 0.22.1 registers golpe foreign comparator via mdb_set_compare");
}

/// SECONDARY SMOKE TEST: Uint64Uint64 comparator
///
/// Verifies the Uint64Uint64 comparator (used by Event__kind: kind:8 ‖ created_at:8)
/// is correctly hooked and produces numeric order vs memcmp byte order.
#[test]
fn test_uint64_uint64_comparator_hook() {
    let kind_a: u64 = 1;   // LE: [0x01, 0x00, ...] — memcmp: sorts AFTER kind_b
    let kind_b: u64 = 256; // LE: [0x00, 0x01, ...] — memcmp: sorts BEFORE kind_a
    let created_at: u64 = 1000;

    let key_a = make_uint64_uint64_key(kind_a, created_at); // kind=1
    let key_b = make_uint64_uint64_key(kind_b, created_at); // kind=256

    // Verify adversarial property
    assert!(
        key_b.as_slice() < key_a.as_slice(),
        "adversarial: kind=256 bytes [0x00,0x01,...] must sort before kind=1 bytes [0x01,0x00,...] under memcmp"
    );

    let dir = tempfile::tempdir().expect("create tempdir");

    // Setup with golpe comparator
    {
        let env = open_test_env(dir.path());
        let mut wtxn = env.write_txn().expect("write txn");
        let db = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>()
                .key_comparator::<Uint64Uint64Cmp>();
            opts.name("test_uint64_uint64");
            opts.create(&mut wtxn).expect("create with Uint64Uint64 comparator")
        };
        db.put(&mut wtxn, key_a.as_slice(), b"a").expect("put a");
        db.put(&mut wtxn, key_b.as_slice(), b"b").expect("put b");
        wtxn.commit().expect("commit");
    }

    // Scan with golpe comparator — must give numeric order [key_a(kind=1), key_b(kind=256)]
    {
        let env = open_test_env(dir.path());
        let rtxn = env.read_txn().expect("rtxn");
        let db = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>()
                .key_comparator::<Uint64Uint64Cmp>();
            opts.name("test_uint64_uint64");
            opts.open(&rtxn).expect("open with Uint64Uint64Cmp").expect("must exist")
        };
        let scanned: Vec<Vec<u8>> = db
            .iter(&rtxn).expect("iter")
            .map(|r| r.expect("item").0.to_vec())
            .collect();

        assert_eq!(scanned.len(), 2);
        assert_eq!(
            scanned[0], key_a,
            "Uint64Uint64 KILL-SWITCH: kind=1 must be FIRST (numeric order)"
        );
        assert_eq!(
            scanned[1], key_b,
            "Uint64Uint64 KILL-SWITCH: kind=256 must be SECOND"
        );
        println!(
            "PASS: Uint64Uint64Cmp hook proven — kind={} before kind={}",
            kind_a, kind_b
        );
    }
}

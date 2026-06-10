/// comparators.rs — heed::Comparator impls bridging to golpe C++ FFI
///
/// These types implement heed's Comparator trait, which heed uses to call mdb_set_compare
/// when opening an Event__* sub-DB. LMDB then uses the registered comparator for all
/// range scans and MDB_SET_RANGE positioning within that dbi.
///
/// CRITICAL: Comparator::compare MUST NOT PANIC.
/// A panic across extern "C" is undefined behavior (same as C++ exception — RFC 2945).
/// The FFI call site cannot panic: the C++ is compiled with -fno-exceptions, and the
/// safe wrappers call std::abort() on malformed keys (not recoverable errors).
/// The only other operation is `result.cmp(&0)` which cannot panic.
///
/// Index → Comparator mapping (from golpe.yaml, RESEARCH.md Pattern 4):
///   Event__id          → StringUint64Cmp
///   Event__pubkey      → StringUint64Cmp
///   Event__tag         → StringUint64Cmp
///   Event__kind        → Uint64Uint64Cmp
///   Event__pubkeyKind  → StringUint64Uint64Cmp
///   Event__created_at  → heed::IntegerComparator (MDB_INTEGERKEY — NOT a golpe comparator)

use std::cmp::Ordering;

// FFI declarations for the extern "C" safe wrapper functions
// compiled from reference/golpe_comparators.cpp by build.rs
unsafe extern "C" {
    /// StringUint64 comparator: string-prefix ‖ uint64(8 bytes LE)
    /// Used by: Event__id (id:32 ‖ created_at:8), Event__pubkey, Event__tag
    fn lmdb_comparator__StringUint64_safe(
        a_ptr: *const u8,
        a_len: usize,
        b_ptr: *const u8,
        b_len: usize,
    ) -> i32;

    /// Uint64Uint64 comparator: uint64(8 bytes LE) ‖ uint64(8 bytes LE) = 16 bytes total
    /// Used by: Event__kind (kind:8 ‖ created_at:8)
    fn lmdb_comparator__Uint64Uint64_safe(
        a_ptr: *const u8,
        a_len: usize,
        b_ptr: *const u8,
        b_len: usize,
    ) -> i32;

    /// StringUint64Uint64 comparator: string-prefix ‖ uint64(8 LE) ‖ uint64(8 LE)
    /// Used by: Event__pubkeyKind (pubkey:32 ‖ kind:8 ‖ created_at:8)
    fn lmdb_comparator__StringUint64Uint64_safe(
        a_ptr: *const u8,
        a_len: usize,
        b_ptr: *const u8,
        b_len: usize,
    ) -> i32;
}

/// Golpe StringUint64 comparator for heed.
///
/// Zero-sized enum is the heed convention for Comparator implementors.
/// Used for: Event__id, Event__pubkey, Event__tag
pub enum StringUint64Cmp {}

impl heed::Comparator for StringUint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> Ordering {
        // Safety: golpe_comparators.cpp compiled with -fno-exceptions;
        //         std::abort() on too-short keys (malformed = programming error, not runtime)
        let r = unsafe {
            lmdb_comparator__StringUint64_safe(a.as_ptr(), a.len(), b.as_ptr(), b.len())
        };
        r.cmp(&0)
    }
}

/// Golpe Uint64Uint64 comparator for heed.
///
/// Used for: Event__kind (kind:8 ‖ created_at:8)
pub enum Uint64Uint64Cmp {}

impl heed::Comparator for Uint64Uint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> Ordering {
        let r = unsafe {
            lmdb_comparator__Uint64Uint64_safe(a.as_ptr(), a.len(), b.as_ptr(), b.len())
        };
        r.cmp(&0)
    }
}

/// Golpe StringUint64Uint64 comparator for heed.
///
/// Used for: Event__pubkeyKind (pubkey:32 ‖ kind:8 ‖ created_at:8)
pub enum StringUint64Uint64Cmp {}

impl heed::Comparator for StringUint64Uint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> Ordering {
        let r = unsafe {
            lmdb_comparator__StringUint64Uint64_safe(a.as_ptr(), a.len(), b.as_ptr(), b.len())
        };
        r.cmp(&0)
    }
}

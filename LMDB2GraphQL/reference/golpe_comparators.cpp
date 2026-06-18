/**
 * golpe_comparators.cpp
 *
 * Vendored from hoytech/rasgueadb utils.h.tt (the golpe-generated comparator source).
 * Upstream: https://github.com/hoytech/rasgueadb/blob/master/utils.h.tt
 * Upstream commit: PENDING plan 02 pin (will be filled in with pinned strfry/golpe commit SHA)
 *
 * MODIFICATIONS FROM UPSTREAM:
 *   1. Removed `#include "lmdbxx/lmdb++.h"` — lmdb.h included directly.
 *   2. Replaced all `throw hoytech::error(...)` with `std::abort()` — throwing across
 *      the Rust extern "C" FFI boundary is undefined behavior (RFC 2945). Too-short keys
 *      are a programming error, not a recoverable runtime condition.
 *   3. Replaced `mdb_cmp_memn` (LMDB internal, not in public lmdb.h — SPIKE A7 confirmed)
 *      with an inline equivalent: shorter key sorts first, then memcmp on common length.
 *   4. Added extern "C" safe wrapper functions that take (ptr, len) pairs instead of
 *      MDB_val* — avoids exposing lmdb-sys types to the Rust linker directly.
 *
 * COMPILED WITH:
 *   -std=c++17 -fno-exceptions -fno-rtti (see build.rs)
 *   -fno-exceptions: defense-in-depth; makes any remaining throw calls into std::terminate()
 */

#include <lmdb.h>
#include <cstdlib>
#include <cstring>
#include <cstdint>

// Inline replacement for LMDB's internal mdb_cmp_memn (not in public lmdb.h — SPIKE A7).
// Semantics: shorter key sorts before longer key; on equal length, lexicographic memcmp.
// Source: inferred from LMDB internals and confirmed by rasgueadb usage pattern.
static inline int mdb_cmp_memn_inline(const MDB_val *a, const MDB_val *b) {
    int diff;
    size_t len_a = a->mv_size;
    size_t len_b = b->mv_size;
    size_t len = len_a < len_b ? len_a : len_b;
    diff = memcmp(a->mv_data, b->mv_data, len);
    if (diff) return diff;
    if (len_a < len_b) return -1;
    if (len_a > len_b) return 1;
    return 0;
}

// --- golpe StringUint64 comparator ---
// Key format: string-prefix (variable length) ‖ uint64 (8 bytes, native-endian / little-endian)
// Used by: Event__id, Event__pubkey, Event__tag
// Sort order: lexicographic on string prefix, then numeric ascending on uint64 suffix
static inline int lmdb_comparator__StringUint64(const MDB_val *a, const MDB_val *b) {
    // too-short keys: programming error → abort (not recoverable, eliminates throw UB)
    if (a->mv_size < 8 || b->mv_size < 8)
        std::abort();

    MDB_val a2 = *a, b2 = *b;
    a2.mv_size -= 8;
    b2.mv_size -= 8;

    int stringCompare = mdb_cmp_memn_inline(&a2, &b2);
    if (stringCompare) return stringCompare;

    uint64_t ai, bi;
    memcpy(&ai, (char*)a->mv_data + a->mv_size - 8, 8);
    memcpy(&bi, (char*)b->mv_data + b->mv_size - 8, 8);

    if (ai < bi) return -1;
    else if (ai > bi) return 1;
    return 0;
}

// --- golpe Uint64Uint64 comparator ---
// Key format: uint64 (8 bytes LE) ‖ uint64 (8 bytes LE) — total 16 bytes
// Used by: Event__kind  (kind:8 ‖ created_at:8)
// Sort order: numeric ascending on first uint64, then numeric ascending on second uint64
static inline int lmdb_comparator__Uint64Uint64(const MDB_val *a, const MDB_val *b) {
    if (a->mv_size != 16 || b->mv_size != 16)
        std::abort();

    uint64_t ai, bi;
    memcpy(&ai, (char*)a->mv_data, 8);
    memcpy(&bi, (char*)b->mv_data, 8);

    if (ai < bi) return -1;
    else if (ai > bi) return 1;

    memcpy(&ai, (char*)a->mv_data + 8, 8);
    memcpy(&bi, (char*)b->mv_data + 8, 8);

    if (ai < bi) return -1;
    else if (ai > bi) return 1;

    return 0;
}

// --- golpe StringUint64Uint64 comparator ---
// Key format: string-prefix (variable) ‖ uint64 (8 bytes LE) ‖ uint64 (8 bytes LE)
// Used by: Event__pubkeyKind  (pubkey:32 ‖ kind:8 ‖ created_at:8)
// Sort order: lexicographic on string prefix, then numeric on first uint64, then second
static inline int lmdb_comparator__StringUint64Uint64(const MDB_val *a, const MDB_val *b) {
    if (a->mv_size < 16 || b->mv_size < 16)
        std::abort();

    MDB_val a2 = *a, b2 = *b;
    a2.mv_size -= 16;
    b2.mv_size -= 16;

    int stringCompare = mdb_cmp_memn_inline(&a2, &b2);
    if (stringCompare) return stringCompare;

    uint64_t ai, bi;
    memcpy(&ai, (char*)a->mv_data + a->mv_size - 16, 8);
    memcpy(&bi, (char*)b->mv_data + b->mv_size - 16, 8);

    if (ai < bi) return -1;
    else if (ai > bi) return 1;

    memcpy(&ai, (char*)a->mv_data + a->mv_size - 8, 8);
    memcpy(&bi, (char*)b->mv_data + b->mv_size - 8, 8);

    if (ai < bi) return -1;
    else if (ai > bi) return 1;

    return 0;
}

// =============================================================================
// extern "C" safe wrappers: (ptr, len) pairs instead of MDB_val*
// These are the symbols Rust declares in extern "C" blocks.
// Avoids exposing lmdb-sys types to the Rust linker directly.
// =============================================================================

extern "C" int lmdb_comparator__StringUint64_safe(
    const uint8_t* a_ptr, size_t a_len,
    const uint8_t* b_ptr, size_t b_len
) {
    MDB_val a = {.mv_size = a_len, .mv_data = (void*)a_ptr};
    MDB_val b = {.mv_size = b_len, .mv_data = (void*)b_ptr};
    return lmdb_comparator__StringUint64(&a, &b);
}

extern "C" int lmdb_comparator__Uint64Uint64_safe(
    const uint8_t* a_ptr, size_t a_len,
    const uint8_t* b_ptr, size_t b_len
) {
    MDB_val a = {.mv_size = a_len, .mv_data = (void*)a_ptr};
    MDB_val b = {.mv_size = b_len, .mv_data = (void*)b_ptr};
    return lmdb_comparator__Uint64Uint64(&a, &b);
}

extern "C" int lmdb_comparator__StringUint64Uint64_safe(
    const uint8_t* a_ptr, size_t a_len,
    const uint8_t* b_ptr, size_t b_len
) {
    MDB_val a = {.mv_size = a_len, .mv_data = (void*)a_ptr};
    MDB_val b = {.mv_size = b_len, .mv_data = (void*)b_ptr};
    return lmdb_comparator__StringUint64Uint64(&a, &b);
}

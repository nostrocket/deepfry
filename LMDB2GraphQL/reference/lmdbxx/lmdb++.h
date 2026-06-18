/**
 * lmdb++.h — hoytech/lmdbxx C++17 RAII wrapper for LMDB
 *
 * Upstream: https://github.com/hoytech/lmdbxx
 * Upstream commit: PENDING plan 02 pin (will be filled with pinned strfry/lmdbxx commit SHA)
 *
 * NOTE: This placeholder exists to satisfy the reference/lmdbxx/ vendor requirement (D-12).
 * The golpe_comparators.cpp in this project does NOT use lmdb++.h — it includes <lmdb.h>
 * directly (SPIKE A7: mdb_cmp_memn is not in public lmdb.h, so lmdbxx's wrapper is not
 * needed; the inline equivalent is implemented in golpe_comparators.cpp).
 *
 * Full lmdb++.h from hoytech/lmdbxx must be vendored here in plan 02 (when pinned
 * strfry/lmdbxx commit is resolved). This file documents the provenance requirement.
 *
 * If any future code in this project uses lmdb++.h directly, replace this stub with
 * the full vendored header from the pinned upstream commit.
 */

#ifndef LMDBXX_LMDB_PLUS_PLUS_H_
#define LMDBXX_LMDB_PLUS_PLUS_H_

// This header intentionally re-exports lmdb.h for compatibility.
// The full lmdb++.h C++17 RAII wrapper will be vendored in plan 02.
#include <lmdb.h>

#endif // LMDBXX_LMDB_PLUS_PLUS_H_

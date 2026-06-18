# Fixture Provenance

This file records the pinned strfry version, fixture generation instructions,
and the adversarial seed design rationale for the comparator self-check oracle.

---

## Pinned strfry Image

| Field | Value |
|-------|-------|
| Docker image tag | `dockurr/strfry:1.1.0` |
| Full RepoDigest | `dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5` |
| strfry version string | `strfry 1.1.0` |
| hoytech/strfry git commit | `f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135` |
| DB version (Meta.dbVersion) | `3` — confirmed via `strfry info` |
| golpe.yaml upstream commit | `f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135` (same as strfry commit; golpe.yaml vendored from this ref) |
| Map size contract | `10995116277760` bytes (10 TiB) — from `../config/strfry/strfry.conf mapsize` |

---

## Committed Fixture

| Field | Value |
|-------|-------|
| `data.mdb` sha256 | `8b871be80f8acaa507741b8640a25a411ee7763b0c4e61bb9527314d1fcb3cd6` |
| `lock.mdb` | Committed alongside data.mdb; use `EnvFlags::NO_LOCK` in tests (safe for static fixture) |
| Seed file | `spam/tests/fixture/seed_events.jsonl` |
| Golden vectors | `spam/tests/fixture/golden_vectors/*.json` (6 files, one per Event__* index) |
| A5 determinism result | **BYTE-IDENTICAL** — two independent `strfry import` runs on the same JSONL produced the identical sha256 `8b871be80f8acaa507741b8640a25a411ee7763b0c4e61bb9527314d1fcb3cd6`. CI may use byte-exact sha256 comparison. |

---

## Adversarial Seed Design Rationale

The seed events in `tests/fixture/seed_events.jsonl` are designed so that
**every comparator's ordering differs from naive `memcmp`** (D-11). This is
essential for the comparator self-check to be meaningful: if the seed produced
the same scan order under both the golpe comparator and `memcmp`, the self-check
could pass even with the comparator not registered (false positive).

### Per-Index Adversarial Properties

| Index | Comparator | Key Format | Adversarial Property in Seed |
|-------|-----------|------------|------------------------------|
| `Event__id` | `StringUint64` | `id(32) ‖ created_at(8 LE)` | Two events with same `id` prefix but `created_at` values spanning a little-endian byte-order inversion: `0x0000000100000000` (4294967296) vs `0x00000000FFFFFFFF` (4294967295). Numerically 4294967296 > 4294967295, but byte-for-byte `0x00...01...` < `0xFF...` under memcmp — opposite order. |
| `Event__pubkey` | `StringUint64` | `pubkey(32) ‖ created_at(8 LE)` | Same `pubkey`, two different `created_at` values: 4294967296 (0x100000000) and 4294967295 (0xFFFFFFFF). Same inversion as above — golpe order is ascending numeric, memcmp order is inverted. |
| `Event__tag` | `StringUint64` | `tagName(1) ‖ tagValue(var) ‖ created_at(8 LE)` | Two events sharing same `e` tag name + same tag value prefix, with `created_at` inversion. |
| `Event__kind` | `Uint64Uint64` | `kind(8 LE) ‖ created_at(8 LE)` | Events with same `kind` and `created_at` inversion (4294967296 vs 4294967295). Also events with kind values differing in high bytes (e.g. kind=1 vs kind=65536=0x10000) to exercise the first uint64 comparison. |
| `Event__pubkeyKind` | `StringUint64Uint64` | `pubkey(32) ‖ kind(8 LE) ‖ created_at(8 LE)` | Same pubkey, same kind, `created_at` inversion. Also same pubkey + different kind ordering trap. |
| `Event__created_at` | `IntegerComparator` | `created_at(8, MDB_INTEGERKEY)` | Not adversarial — `MDB_INTEGERKEY` handles native-endian correctly; events ordered by distinct timestamps. |

### Fixed Test Keypair

The seed events use a **fixed known test secret key** for reproducibility:
```
Secret key (hex): 0000000000000000000000000000000000000000000000000000000000000001
Public key (hex): 79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798
```

This is the secp256k1 generator point (`G`) private key — the smallest valid
non-zero scalar. It is universally known to be a test key and must **never** be
used with real Nostr events or real funds.

### Fixed Timestamps

All `created_at` values are fixed at specific historical UNIX timestamps to ensure
reproducibility:
- `1700000000` (2023-11-14 22:13:20 UTC) — base timestamp
- `4294967295` (2106-02-07 06:28:15 UTC) — `0xFFFFFFFF` — 32-bit max
- `4294967296` (2106-02-07 06:28:16 UTC) — `0x100000000` — triggers LE byte-order inversion
- `1710000000` (2024-03-09 17:20:00 UTC) — secondary base
- `1720000000` (2024-07-03 21:20:00 UTC) — tertiary base

The key adversarial pair is `4294967295` vs `4294967296`:
- Numerically: `4294967296 > 4294967295`
- Little-endian bytes of 4294967296: `00 00 00 00 01 00 00 00`
- Little-endian bytes of 4294967295: `FF FF FF FF 00 00 00 00`
- memcmp order: 4294967296 (`00 00 ...`) < 4294967295 (`FF FF ...`) — WRONG
- golpe numeric order: 4294967296 > 4294967295 — CORRECT

---

## Fixture Regen Instructions

To regenerate `tests/fixture/data.mdb` from scratch using the pinned strfry image:

```bash
export PATH="/opt/homebrew/bin:$PATH"
WORK=$(mktemp -d "$HOME/strfry-fixture-XXXXXX")
mkdir -p "$WORK/strfry-db"

# Write strfry config
printf 'db = "/work/strfry-db/"\ndbParams {\n  maxreaders = 256\n  mapsize = 10995116277760\n  noReadAhead = false\n}\n' > "$WORK/strfry.conf"

# Import adversarial seed events
docker run --rm -i --ulimit nofile=1000000:1000000 \
  -v "$WORK":/work \
  --entrypoint /app/strfry \
  dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5 \
  --config=/work/strfry.conf import \
  < /path/to/spam/tests/fixture/seed_events.jsonl

# Copy fixture files
cp "$WORK/strfry-db/data.mdb" spam/tests/fixture/data.mdb
cp "$WORK/strfry-db/lock.mdb" spam/tests/fixture/lock.mdb

# Verify sha256
sha256sum spam/tests/fixture/data.mdb
# Expected: (see "Committed Fixture" table above for the pinned sha256)
```

**Note on A5 determinism:** See the "Committed Fixture" table above for the A5
determinism result. If the import is NOT byte-deterministic, CI should compare
semantically (all expected events present + correct scan order) rather than
byte-exact sha256.

---

## Per-Index Golden Vector Derivation Reasoning

The golden vectors in `tests/fixture/golden_vectors/*.json` are hand-computed
analytically from the comparator semantics and seed data (D-05). They are NOT
dumped from a golpe-linked tool, which would prove consistency but not correctness.

### LevId Contract (spec §3.1 / §3.4)

In every `Event__*` index sub-DB:
- The **KEY** is the composite field (e.g., `pubkey(32) ‖ created_at(8 LE)`)
- The **VALUE** is the 8-byte little-endian `levId` (the `EventPayload` primary key)

`ordered_lev_ids` in each golden vector is the sequence of VALUE-side `levId`s
taken in KEY-scan order (ascending by the index comparator). Plan 01-03's
self-check scans the same field to compare.

### LevId Assignment

strfry assigns `levId`s monotonically in import order. Given the seed
`seed_events.jsonl` has events E1, E2, ..., E12 in file order, their levIds are
assigned as `levId = 1, 2, ..., 12` respectively. The golden vectors list these
levIds in the order dictated by each index's comparator — not import order.

### Per-Index Derivation (see golden_vectors/*.json for final values)

The derivation reasoning is documented in Task 5 of plan 01-02. The key principle
for each comparator:

**StringUint64 (Event__id, Event__pubkey, Event__tag):**
- Sort by string prefix (id, pubkey, or tagName‖tagValue) ascending by `memcmp`
- Within same prefix: sort by `created_at` numerically ascending (NOT byte-order)
- The adversarial pair `4294967295` vs `4294967296` inverts under `memcmp` but
  sorts correctly under golpe numeric comparison

**Uint64Uint64 (Event__kind):**
- Sort by `kind` numerically ascending, then `created_at` numerically ascending
- Same `created_at` inversion applies

**StringUint64Uint64 (Event__pubkeyKind):**
- Sort by `pubkey` prefix ascending, then `kind` numerically, then `created_at` numerically

**IntegerComparator (Event__created_at):**
- Sort by `created_at` as native-endian integer; `MDB_INTEGERKEY` handles this correctly
- No adversarial property needed — but distinct timestamps ensure ordering is exercised

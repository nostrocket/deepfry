# Phase 1: LMDB Foundation & Comparator Proof - Research

**Researched:** 2026-06-10
**Domain:** Rust LMDB custom comparator FFI, golpe/rasgueadb C++ comparator compilation, strfry fixture pinning
**Confidence:** HIGH (core technical claims verified via primary sources; two spike risks flagged LOW)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01:** golpe's C++ comparator ships in production via FFI inside heed's `Comparator::compare`. No Rust reimplementation of comparator logic.
- **D-02:** `build.rs` compiles golpe's vendored comparator source against vendored `lmdb++.h` (lmdbxx, header-only) via the `cc` crate. Alpine release binary carries a C++/golpe build — musl/static-libstdc++ link story is a Phase-1 task.
- **D-03 (LMDB-05 amendment):** "links golpe's custom comparators via FFI and registers them through heed's `Comparator` trait (`mdb_set_compare`)." Stale "reimplement in Rust" wording in REQUIREMENTS.md, PROJECT.md, and CLAUDE.md to be left alone (not Phase 1's job).
- **D-04:** Keep fail-closed startup self-check, reframed as a wiring/registration/drift gate.
- **D-05:** Oracle = committed golden vectors (hand-computed, NOT from a golpe-linked tool).
- **D-06:** Self-check asserts full ordered-sequence equality per index (not just spot checks).
- **D-07:** Prove all six `Event__*` indexes.
- **D-08:** Pin strfry by resolving `dockurr/strfry:latest` → sha256 digest + strfry git commit.
- **D-09:** Also pin parent `Dockerfile.strfry` to the same digest (cross-repo change).
- **D-10:** Fixture strategy = committed binary (`data.mdb` + `lock.mdb`) AND CI regeneration from pinned image.
- **D-11:** Seed is adversarial — forces every comparator's non-`memcmp` ordering.
- **D-12:** Vendor `golpe.yaml` + golpe comparator source + `lmdb++.h` into `reference/`.
- **D-13:** Phase 1 builds the real production foundational crate module, not a throwaway harness.
- **D-14:** Introduce minimal `~/deepfry/lmdb2graphql.yaml` with `strfry-db` path + pinned strfry version/digest.

### Claude's Discretion

- Exact crate/module layout, error type design (`thiserror`), and logging/diagnostics format for gate failures.
- How the pinned-vs-detected strfry version drift is surfaced at startup (basic startup log line is sufficient for Phase 1).

### Deferred Ideas (OUT OF SCOPE)

- Doc-sync follow-up: updating LMDB-05, PROJECT.md, project CLAUDE.md wording to reflect FFI instead of "reimplement in Rust".
- CLAUDE.md cleanup: removing stale rusqlite/SQLite entries.
- OPS-04 (Phase 5): richer version drift surfacing.
- Cross-arch / big-endian (v2, PORT-01).
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| LMDB-01 | Open strfry LMDB env read-only (`MDB_RDONLY`), never open write txn | heed `EnvFlags::READ_ONLY`; verified `raw_init_database` uses read txn for `open()` |
| LMDB-02 | Read `Meta`, refuse if `dbVersion != 3`, exit loudly | `CURR_DB_VERSION = 3` confirmed in strfry `src/constants.h`; startup gate pattern documented |
| LMDB-03 | Read `Meta.endianness`, compare to host, refuse on mismatch | `Meta` struct layout documented in spec §6.3; runtime endian check pattern documented |
| LMDB-04 | Set `map_size ≥ strfry's configured size` | `strfry.conf` confirms `mapsize = 10995116277760` (10 TiB); heed `EnvOpenOptions::map_size()` documented |
| LMDB-05 (amended by D-03) | Link golpe's C++ comparators via FFI, register via `mdb_set_compare` through heed's `Comparator` trait | R1 VERIFIED: heed `raw_init_database` calls `mdb_set_compare` via `custom_key_cmp_wrapper::<C>` for both `open()` and `create()` |
| LMDB-06 | Run comparator self-check against pinned fixture at startup, fail closed on mismatch | Pattern: golden vector oracle (D-05), full ordered-sequence equality (D-06), all six indexes (D-07) |
| LMDB-10 | Target pinned strfry version/digest; log it at startup; fail closed if `dbVersion` mismatches | dockurr/strfry:1.1.0 is current latest (~1 month old); digest pinning via `image@sha256:...` |
</phase_requirements>

---

## Summary

Phase 1 de-risks the entire Approach-B bet. The foundational question — can heed 0.22.1 register a custom comparator on an existing `Event__*` sub-DB opened read-only, and will LMDB use it for range scans? — is **answered affirmatively** (R1 VERIFIED). The mechanism: `DatabaseOpenOptions::open()` (not just `create()`) calls `raw_init_database`, which calls `mdb_set_compare` via a monomorphized `custom_key_cmp_wrapper::<C>` extern C trampoline, regardless of whether `MDB_CREATE` is set. The comparator registration fires on every `mdb_dbi_open` call — both first-open and re-open within a process.

The comparator implementations are found in rasgueadb's `utils.h.tt` template (generated into strfry's `build/golpe.h`). The three functions — `lmdb_comparator__StringUint64`, `lmdb_comparator__Uint64Uint64`, `lmdb_comparator__StringUint64Uint64` — are straightforward C++ that use `memcpy` for uint64 native-endian reads and `mdb_cmp_memn` for the string prefix. However, they contain `throw hoytech::error(...)` on malformed keys — **C++ exceptions thrown inside a function called via `extern "C"` from LMDB and then up through Rust is undefined behavior**. This is the dominant spike risk for R2.

The two remaining unknowns are: (R2) whether the golpe comparator C++ can be compiled with C++ exceptions disabled (`-fno-exceptions`) or otherwise made safe across the Rust FFI boundary, and whether musl/Alpine static C++ linking works with the cc crate; and (R3/R4) the exact dockurr/strfry digest + strfry git commit for the pinned version, and the deterministic fixture generation mechanism.

**Primary recommendation:** Build the `lmdb` module in the sequence dictated by D-13. Prove heed comparator registration first (smallest smoke test — one index, one comparator, scan, assert order). Simultaneously tackle the C++ exception-in-FFI problem (use `-fno-exceptions` in the cc build, replace `throw` with `abort()` or return `-1`/`1` for malformed keys). Then build the full self-check, Meta gates, config, and fixture machinery.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| LMDB env open (MDB_RDONLY, map_size gate) | Rust binary / startup | — | Co-located reader; no network tier involved |
| Meta version gate (dbVersion == 3) | Rust binary / startup | — | Must execute before any other LMDB access |
| Endianness gate | Rust binary / startup | — | Host assertion; no external service |
| Comparator registration (mdb_set_compare via heed) | Rust `lmdb` module | C++ FFI (golpe comparator) | heed owns the registration; golpe C++ provides the function body |
| Self-check: golden vector oracle | Rust test / startup gate | Committed fixture DB | Scan live fixture, compare against committed vectors |
| Fixture generation | Docker (pinned strfry image) | CI pipeline | strfry process writes the LMDB; LMDB2GraphQL reads it |
| Config (lmdb2graphql.yaml) | Rust binary startup | ~/deepfry/ convention | Per DeepFry convention; file must not be deleted in tests |
| Strfry version/digest pin | Dockerfile.strfry (parent repo) + lmdb2graphql.yaml | CI verification | D-08/D-09; cross-repo change |

---

## Standard Stack

### Core (Phase 1 scope only — no SQLite, no GraphQL yet)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `heed` | 0.22.1 | LMDB typed wrapper — env open, read txn, named-DB open, custom comparator | Actively maintained (Meilisearch); verified `mdb_set_compare` registration in both `open()` and `create()` paths; `EnvFlags::READ_ONLY` + `NO_LOCK` support |
| `cc` | 1.2.x (latest) | Compile golpe's C++ comparator source in `build.rs` | Official Rust build-script C/C++ compiler crate; used by the ecosystem for FFI C/C++ compilation |
| `tracing` | 0.1.44 | Structured logging for gate failures and startup events | Standard in the tokio ecosystem |
| `tracing-subscriber` | 0.3.23 | Log output (JSON for Docker, pretty for dev) | `EnvFilter` for runtime log-level control |
| `thiserror` | 2.x (latest) | Error type derivation for the `lmdb` module | Typed error boundary between LMDB, FFI, and future decoder/query layers |
| `anyhow` | 1.x (latest) | Error propagation in `main.rs` and tests | Binary crate error handling |
| `serde` | 1.0.228 | Config struct deserialization | Required for `lmdb2graphql.yaml` config |
| `serde_yaml` or `config` crate | current | YAML config parsing | DeepFry convention is YAML under `~/deepfry/` |

### Supporting (Phase 1 testing)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `tempfile` | 3.x | Temp directory for config loading tests | Avoids touching `~/deepfry/` in tests (per CLAUDE.md) |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `heed` 0.22.1 | `lmdb-rkv` 0.14 | Dormant; unnamed-root iteration not documented; no evidence of `mdb_set_compare` exposure |
| `heed` 0.22.1 | raw `lmdb` 0.8 sys | Abandoned 2018; would require writing the `mdb_set_compare` call manually via `lmdb-sys` |
| `cc` crate | `cxx` crate | `cxx` adds schema overhead inappropriate for a simple C function compile; `cc` is sufficient for compiling a single C++ translation unit |

**Installation (Phase 1 Cargo.toml):**
```toml
[dependencies]
heed = "0.22.1"
tracing = "0.1.44"
tracing-subscriber = { version = "0.3.23", features = ["env-filter", "json"] }
thiserror = "2"
anyhow = "1"
serde = { version = "1", features = ["derive"] }
# Add serde_yaml or config crate for YAML parsing

[build-dependencies]
cc = "1"
```

---

## Package Legitimacy Audit

> slopcheck was not available at research time. All packages below are tagged `[ASSUMED]` from crates.io registry existence + training knowledge. The planner must gate each install behind a `checkpoint:human-verify` task before use if the team has not used these crates previously. However, all crates listed below are canonical Rust ecosystem crates with years of provenance.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| `heed` | crates.io | ~6 yrs | High (Meilisearch production use) | github.com/meilisearch/heed | [ASSUMED] | Approved — Meilisearch production |
| `cc` | crates.io | ~9 yrs | Extreme (rust-lang/cc-rs; ~200M/wk) | github.com/rust-lang/cc-rs | [ASSUMED] | Approved — official Rust project |
| `tracing` | crates.io | ~5 yrs | Extreme (tokio-rs ecosystem) | github.com/tokio-rs/tracing | [ASSUMED] | Approved |
| `tracing-subscriber` | crates.io | ~5 yrs | High | github.com/tokio-rs/tracing | [ASSUMED] | Approved |
| `thiserror` | crates.io | ~5 yrs | Extreme | github.com/dtolnay/thiserror | [ASSUMED] | Approved |
| `anyhow` | crates.io | ~5 yrs | Extreme | github.com/dtolnay/anyhow | [ASSUMED] | Approved |
| `serde` | crates.io | ~9 yrs | Extreme | github.com/serde-rs/serde | [ASSUMED] | Approved |
| `tempfile` | crates.io | ~7 yrs | High | github.com/Stebalien/tempfile | [ASSUMED] | Approved |
| `dirs` | crates.io | ~7 yrs | High | github.com/dirs-dev/directories-rs | [ASSUMED] | Approved — resolves `~/deepfry/` home dir; gate via plan 02 Task 1 checkpoint |
| `serde_yaml_ng` | crates.io | ~1 yr | Moderate (maintained fork of dtolnay's deprecated `serde_yaml`) | github.com/acatton/serde-yaml-ng | [ASSUMED] | Approved — maintained replacement for deprecated `serde_yaml`; gate via plan 02 Task 1 checkpoint |

**Packages removed due to slopcheck [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

*slopcheck was unavailable at research time — all packages tagged `[ASSUMED]`. Planner should add a `checkpoint:human-verify` before the first cargo add if the team has not previously used these crates.*

---

## Architecture Patterns

### System Architecture Diagram

```
[Pinned dockurr/strfry image]
        │  docker run → ingest adversarial seed events via NIP-01 WS
        │
        ▼
[Fixture data.mdb + lock.mdb] ─── committed to repo ──────────────────────┐
        │                                                                  │
        │  read-only volume mount                                          │
        ▼                                                                  │
[LMDB2GraphQL startup]                                          [CI fixture verify]
        │                                                                  │
        ├─ open env (EnvFlags::READ_ONLY, map_size≥10TiB)                 │
        │                                                                  │
        ├─ open Meta dbi → read dbVersion, endianness                      │
        │   ├─ dbVersion != 3 → exit(1) loud error                        │
        │   └─ endianness mismatch → exit(1) loud error                   │
        │                                                                  │
        ├─ for each Event__* index:                                        │
        │   └─ env.database_options()                                      │
        │       .key_comparator::<GolpeComparatorN>()                      │
        │       .open(&rtxn)                                               │
        │       [heed calls mdb_set_compare → custom_key_cmp_wrapper::<C>]│
        │       [C++ FFI: lmdb_comparator__StringUint64 etc.]              │
        │                                                                  │
        ├─ COMPARATOR SELF-CHECK (startup gate, reusable fn):             │
        │   ├─ scan all 6 Event__* indexes on fixture DB                  │
        │   ├─ collect full ordered levId sequence per index               │
        │   ├─ compare against committed golden vectors                    │
        │   └─ any mismatch → exit(1) fail closed                         │
        │                                                                  │
        └─ log: pinned strfry version/digest + detected dbVersion         │
                                                                          │
[Golden vectors] ─── committed alongside fixture ──────────────────────────┘
```

### Recommended Project Structure (Phase 1 deliverables only)

```
spam/
├── build.rs                     # cc crate: compile golpe_comparators.cpp
├── Cargo.toml
├── rust-toolchain.toml          # pin stable channel + min version
├── src/
│   ├── main.rs                  # startup gate, config load, self-check call
│   ├── config.rs                # ~/deepfry/lmdb2graphql.yaml parsing
│   └── lmdb/
│       ├── mod.rs               # pub use; EnvHandle
│       ├── env.rs               # open_read_only_env() → heed::Env
│       ├── meta.rs              # open Meta dbi, read MetaRecord
│       ├── comparators.rs       # GolpeComparator types implementing heed::Comparator
│       ├── indexes.rs           # open_event_indexes() → [Database; 6]
│       ├── self_check.rs        # run_comparator_self_check(env, fixture_path) → Result<()>
│       └── types.rs             # MetaRecord { db_version, endianness, ... }
├── reference/
│   ├── golpe.yaml               # vendored from hoytech/strfry @ pinned commit
│   ├── golpe_comparators.cpp    # vendored: lmdb_comparator__{StringUint64, ...}
│   └── lmdbxx/
│       └── lmdb++.h             # vendored header-only lmdbxx (hoytech/lmdbxx)
├── tests/
│   ├── fixture/
│   │   ├── data.mdb             # committed binary fixture (adversarial seed)
│   │   ├── lock.mdb             # committed binary
│   │   ├── PROVENANCE.md        # documents pinned strfry image + commit + seed
│   │   └── golden_vectors/
│   │       ├── Event__id.json
│   │       ├── Event__pubkey.json
│   │       ├── Event__created_at.json
│   │       ├── Event__kind.json
│   │       ├── Event__pubkeyKind.json
│   │       └── Event__tag.json
│   └── self_check_test.rs       # integration test: run self-check against fixture
└── ~/deepfry/lmdb2graphql.yaml  # (runtime config, not in repo — documented in PROVENANCE.md)
```

### Pattern 1: heed Comparator Registration on Existing DB (R1 VERIFIED)

**What:** heed 0.22.1's `DatabaseOpenOptions::open()` — used for existing databases — calls `raw_init_database`, which calls `mdb_set_compare` whenever the comparator type is not `DefaultComparator` or `IntegerComparator`. This registration fires on the `mdb_dbi_open` call inside every process that needs to scan the dbi.

**When to use:** Every `Event__*` index sub-DB opened by LMDB2GraphQL must be opened with the correct `key_comparator` type. LMDB will then use the registered comparator for all range scans and `MDB_SET_RANGE` positioning within that dbi.

**Verified mechanism (from heed v0.22.1 source):**
```rust
// In heed's raw_init_database (env.rs):
// After mdb_dbi_open succeeds:
if cmp_type_id != TypeId::of::<DefaultComparator>()
    && cmp_type_id != TypeId::of::<IntegerComparator>()
{
    unsafe {
        mdb_result(ffi::mdb_set_compare(
            raw_txn.as_mut(),
            dbi,
            Some(custom_key_cmp_wrapper::<C>),
        ))?
    };
}
```

The `custom_key_cmp_wrapper::<C>` is a monomorphized `extern "C" fn` that calls `C::compare(a_bytes, b_bytes)` and maps `Ordering` to `i32`. This is the safe bridge from Rust's `Comparator` trait to LMDB's C function pointer.

**Important:** `open()` takes a `RoTxn` (read-only transaction), but `mdb_set_compare` is called with the transaction pointer. LMDB's documentation states `mdb_set_compare` must be called before any data access on the dbi within that process — it's process-memory state, not persistent. This is safe in a read-only env because `mdb_set_compare` does not modify the database file.

**Source:** `[VERIFIED: github.com/meilisearch/heed/blob/v0.22.1/heed/src/envs/env.rs]`

```rust
// How to open Event__pubkey with golpe's StringUint64 comparator:
use heed::types::{Bytes, ByteSlice};

pub struct StringUint64Cmp;

impl heed::Comparator for StringUint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> std::cmp::Ordering {
        // Delegate to C++ FFI (see comparators.rs)
        // Must NOT propagate C++ exceptions — use -fno-exceptions in build.rs
        let result = unsafe { ffi::lmdb_comparator__StringUint64_safe(
            a.as_ptr() as _, a.len(),
            b.as_ptr() as _, b.len(),
        )};
        result.cmp(&0)
    }
}

// Opening the existing strfry sub-DB:
let pubkey_db: Database<Bytes, Bytes> = env
    .database_options()
    .types::<Bytes, Bytes>()
    .key_comparator::<StringUint64Cmp>()
    .name("Event__pubkey")
    .open(&rtxn)?
    .expect("Event__pubkey must exist in strfry DB");
```

**Source:** `[ASSUMED]` — pattern derived from heed docs + verified source mechanism

### Pattern 2: golpe Comparator C++ FFI via cc crate

**What:** `build.rs` uses the `cc` crate to compile a C++ translation unit that exposes the three golpe comparators as `extern "C"` functions — with **C++ exceptions disabled** (`-fno-exceptions`) to eliminate the Rust FFI UB risk.

**The C++ exception problem (CRITICAL):** The rasgueadb `utils.h.tt` comparators contain `throw hoytech::error(...)` on keys shorter than expected. C++ exceptions thrown inside an `extern "C"` function called by LMDB (which then enters Rust via the heed `custom_key_cmp_wrapper`) is undefined behavior in Rust (the stack unwind crosses an ABI boundary declared as `extern "C"`). The fix is to compile with `-fno-exceptions` so `throw` becomes `std::terminate()`, or to replace the `throw` calls with non-throwing alternatives (return -1 or abort) in the vendored copy.

**Preferred approach (vendored + sanitized):**
1. Vendor `utils.h.tt` → `reference/golpe_comparators.cpp` with `throw` replaced by `abort()` or a fallback return value (malformed keys are a bug, not a recoverable error).
2. Compile with `-fno-exceptions` as defense-in-depth.

```rust
// build.rs
fn main() {
    cc::Build::new()
        .cpp(true)
        .file("reference/golpe_comparators.cpp")
        .include("reference/lmdbxx")    // for lmdb++.h
        .include("path/to/lmdb/include") // for lmdb.h
        .flag("-fno-exceptions")         // eliminate UB from throw
        .flag("-std=c++17")              // lmdbxx requires C++17
        .compile("golpe_comparators");

    // For Alpine/musl static builds, emit:
    // println!("cargo:rustc-link-lib=static=stdc++");  // if static libstdc++ available
    // Or use libc++:
    // println!("cargo:rustc-link-lib=static=c++");
}
```

**Source:** `[CITED: docs.rs/cc/latest/cc/]`, `[VERIFIED: raw.githubusercontent.com/hoytech/rasgueadb/master/utils.h.tt]`

### Pattern 3: Meta dbi — Endianness and dbVersion

**What:** The `Meta` dbi uses golpe's standard integer key (record id = 1 for the single row). The value is a golpe struct with `dbVersion` (uint32), `endianness` (uint32, 0=little, 1=big), and `negentropyModificationCounter` (uint64) — all stored in host byte order.

**Reading Meta:**
```rust
// Meta is keyed by record id (uint64 MDB_INTEGERKEY)
let meta_db: Database<U64<NativeEndian>, Bytes> = env
    .database_options()
    .types::<U64<NativeEndian>, Bytes>()
    .key_comparator::<IntegerComparator>()
    .name("Meta")
    .open(&rtxn)?
    .expect("Meta must exist");

let meta_bytes = meta_db.get(&rtxn, &1u64)?.expect("Meta record id=1 must exist");
// Parse bytes as golpe struct (little-endian host byte order)
// dbVersion: bytes[0..4] as u32 LE
// endianness: bytes[4..8] as u32 LE (0 = little)
let db_version = u32::from_ne_bytes(meta_bytes[0..4].try_into()?);
let endianness = u32::from_ne_bytes(meta_bytes[4..8].try_into()?);
```

**Note:** The exact byte offsets within the Meta value struct depend on the golpe-generated struct layout, which must be verified against the pinned strfry build's `golpe.yaml` and generated `build/golpe.h`. The spec confirms dbVersion and endianness exist; exact offsets require reading the generated struct. **This is a spike item** — the planner must include a task to read the actual struct layout from the pinned strfry source.

**Source:** `[CITED: spec.md §6.1, §6.3]`, `[ASSUMED]` for exact byte offsets

### Pattern 4: Adversarial Seed Design

**What:** The seed events must force non-`memcmp` ordering for every comparator. The adversarial properties for each comparator:

| Comparator | Index | Key Format | Adversarial Property |
|------------|-------|------------|---------------------|
| `StringUint64` | `Event__id` | `id(32 bytes) ‖ created_at(8 bytes LE)` | Two events with same `id` prefix but different `created_at`; `created_at` values whose little-endian byte order would invert numeric order under `memcmp` |
| `StringUint64` | `Event__pubkey` | `pubkey(32 bytes) ‖ created_at(8 bytes LE)` | Same `pubkey`, two different `created_at` spanning a byte-order inversion point (e.g., `0x01_0000_0000` vs `0xFF_FF_FF_FF`) |
| `StringUint64` | `Event__tag` | `tagName(1 byte) ‖ tagValue(variable) ‖ created_at(8 bytes LE)` | Two events with same tagName+tagValue prefix, different `created_at` with byte-order inversion |
| `Uint64Uint64` | `Event__kind` | `kind(8 bytes LE) ‖ created_at(8 bytes LE)` | Two kinds differing in high bytes but appearing numerically similar; same kind with `created_at` inversion |
| `StringUint64Uint64` | `Event__pubkeyKind` | `pubkey(32 bytes) ‖ kind(8 bytes LE) ‖ created_at(8 bytes LE)` | Same `pubkey`, same `kind`, different `created_at` with inversion; same `pubkey`, different `kind` byte-ordering trap |
| `IntegerComparator` | `Event__created_at` | `created_at(8 bytes, MDB_INTEGERKEY)` | Not adversarial — MDB_INTEGERKEY handles native-endian correctly |

**Minimum adversarial seed:** ~8-12 events are sufficient. Nostr events require valid signatures for strfry to accept them. The seed events must be pre-signed with known keys or generated using a Nostr event construction tool. **This is a spike item for R4** — the fixture generation mechanism must handle signed events.

**Source:** `[CITED: spec.md §6.2, §3.1]`, `[CITED: CONTEXT.md D-11]`

### Anti-Patterns to Avoid

- **Do NOT set `EnvFlags::MDB_CREATE` when opening `Event__*` sub-DBs** — this creates new sub-DBs in strfry's env. Use `open()`, not `create()`. `[CITED: CLAUDE.md heed decision matrix]`
- **Do NOT open a write transaction** — `EnvFlags::READ_ONLY` prevents this at the LMDB level; also mount the Docker volume `:ro`. `[CITED: spec.md §6.4]`
- **Do NOT call `open_database` on `Event__*` sub-DBs WITHOUT registering the comparator** — silently returns wrong range-scan results. The comparator must be registered on every process restart. `[CITED: spec.md §6.2, LMDB internals: comparators are process-memory state only]`
- **Do NOT let the C++ comparators throw exceptions across the FFI boundary** — undefined behavior. Use `-fno-exceptions` and sanitize `throw` in the vendored source. `[CITED: rust-lang.github.io/rfcs/2945-c-unwind-abi.md]`
- **Do NOT use `EnvFlags::NO_LOCK` in tests if strfry is running** — safe only for CI with static fixture (no live strfry). `[CITED: CLAUDE.md Stack Patterns]`
- **Do NOT hand-compute golden vectors from a golpe-linked tool** — this would prove consistency, not correctness (D-05). Compute analytically from the comparator semantics and seed data.
- **Do NOT call `EnvFlags::NO_LOCK` in production** — use only `EnvFlags::READ_ONLY`; `NO_LOCK` bypasses the shared reader table and is only safe for static fixture tests.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| LMDB typed wrapper + `mdb_set_compare` registration | Custom `mdb_dbi_open` + `mdb_set_compare` call chain | `heed` 0.22.1 `key_comparator` + `open()` | heed wraps the unsafe FFI, handles error mapping, and monomorphizes the extern C trampoline correctly |
| C++ exception-safe bridge | Custom Rust `panic`→`catch_unwind`→error wrapper | `-fno-exceptions` in build.rs cc invocation | Simpler; eliminates the problem at source; lmdbxx C++17 compiles cleanly with no-exceptions |
| YAML config parsing | `std::fs::read_to_string` + manual parse | `serde_yaml` or `config` crate | Config crate handles env override, defaults, and nested struct deserialization |
| Endianness detection | Bit-twiddling | `cfg!(target_endian = "little")` + runtime check via `u16::from_ne_bytes([1,0]) == 1` | Idiomatic; compile-time available |
| Integer key comparison | Custom byte-swap | `heed::IntegerComparator` / `MDB_INTEGERKEY` | Built into LMDB; heed maps it to `DatabaseOpenOptions::key_comparator::<IntegerComparator>()` |

**Key insight:** The entire comparator registration complexity reduces to correctly passing the Comparator type parameter to `database_options().key_comparator::<T>().open()`. The heed machinery handles everything below that.

---

## R1: heed Comparator Hook Viability — VERIFIED

> **Go/no-go:** APPROVED. heed 0.22.1 does register custom comparators on existing sub-DBs opened read-only.

### Verification Evidence

1. **`open()` calls `raw_init_database`:** Both `DatabaseOpenOptions::open()` (read txn) and `create()` (write txn) call `raw_init_database::<C, CDUP>`. The difference is only `MDB_CREATE` in the flags. `[VERIFIED: github.com/meilisearch/heed/blob/v0.22.1/heed/src/databases/database.rs]`

2. **`raw_init_database` calls `mdb_set_compare`:** When `TypeId::of::<C>()` is neither `DefaultComparator` nor `IntegerComparator`, the function calls `mdb_set_compare(txn, dbi, Some(custom_key_cmp_wrapper::<C>))`. `[VERIFIED: github.com/meilisearch/heed/blob/v0.22.1/heed/src/envs/env.rs]`

3. **`custom_key_cmp_wrapper::<C>` is a monomorphized `extern "C" fn`:** It converts `MDB_val` pointers to `&[u8]` slices, calls `C::compare(a, b)`, and maps `Ordering` to `i32`. `[VERIFIED: same source]`

4. **LMDB semantics:** `mdb_set_compare` must be called after `mdb_dbi_open` and before any cursor operations. It is valid in read-only mode because it modifies only process-memory metadata (the dbi's comparator function pointer), not the database file. `[CITED: lmdb.tech/doc — mdb_set_compare docs, NCBI mirror]`

5. **Distinct comparator per dbi:** Each named sub-DB has its own `dbi` (LMDB DBI handle). `mdb_set_compare` operates per-dbi. LMDB2GraphQL can register `StringUint64Cmp` on `Event__id`, `Uint64Uint64Cmp` on `Event__kind`, etc. independently. `[VERIFIED: LMDB internals — MDB_dbi is per-named-DB]`

6. **`NO_LOCK` for CI fixtures:** `EnvFlags::NO_LOCK` is an unsafe flag that bypasses the shared reader table. It is safe when strfry is NOT running against the same env (CI-only fixture). For production, use `EnvFlags::READ_ONLY` only. `[CITED: CLAUDE.md Stack Patterns]`

### Remaining Caveat

- **`IntegerComparator` for `Event__created_at`:** The `created_at` index uses `MDB_INTEGERKEY` (set via `DatabaseFlags::INTEGER_KEY` or `IntegerComparator`). This is **not** a custom comparator — it's a built-in LMDB flag. heed maps it correctly. No FFI comparator needed for this index.

---

## R2: golpe C++ Compile + musl Static-Link Story

> **Status: SPIKE RISK — LOW confidence on musl static libstdc++ detail. High confidence on the C++ exception problem and its fix.**

### Comparator Source (VERIFIED)

The three comparators are in rasgueadb's `utils.h.tt`, generated into `build/golpe.h` during strfry's build. The exact C++ (from `raw.githubusercontent.com/hoytech/rasgueadb/master/utils.h.tt`):

```cpp
// Source: [VERIFIED: raw.githubusercontent.com/hoytech/rasgueadb/master/utils.h.tt]

inline int lmdb_comparator__StringUint64(const MDB_val *a, const MDB_val *b) {
    if (a->mv_size < 8 || b->mv_size < 8)
        throw hoytech::error("StringUint64 key too short to compare");

    MDB_val a2 = *a, b2 = *b;
    a2.mv_size -= 8;
    b2.mv_size -= 8;

    auto stringCompare = mdb_cmp_memn(&a2, &b2);
    if (stringCompare) return stringCompare;

    uint64_t ai, bi;
    memcpy(&ai, (char*)a->mv_data + a->mv_size - 8, 8);
    memcpy(&bi, (char*)b->mv_data + b->mv_size - 8, 8);

    if (ai < bi) return -1;
    else if (ai > bi) return 1;
    return 0;
}

inline int lmdb_comparator__Uint64Uint64(const MDB_val *a, const MDB_val *b) {
    if (a->mv_size != 16 || b->mv_size != 16)
        throw hoytech::error("Uint64Uint64 key too short/long to compare");

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

inline int lmdb_comparator__StringUint64Uint64(const MDB_val *a, const MDB_val *b) {
    if (a->mv_size < 16 || b->mv_size < 16)
        throw hoytech::error("StringUint64Uint64 key too short to compare");

    MDB_val a2 = *a, b2 = *b;
    a2.mv_size -= 16;
    b2.mv_size -= 16;

    auto stringCompare = mdb_cmp_memn(&a2, &b2);
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
```

**Semantic observation:** The uint64 values are read with `memcpy` into native `uint64_t` and compared as integers. On a little-endian host (x86-64, arm64), this correctly implements numeric order on values that are stored little-endian in the key. This is the exact ordering strfry depends on, and it differs from `memcmp` for multi-byte values where the byte that varies is not the most-significant.

### The C++ Exception Problem (CRITICAL SPIKE)

The `throw hoytech::error(...)` lines in the comparators are called via:
```
LMDB (C) → mdb_cmp_func (extern "C") → custom_key_cmp_wrapper::<C> (Rust) → Comparator::compare → FFI call to C++ lmdb_comparator__*
```

If the C++ comparator throws, the exception unwinds through `custom_key_cmp_wrapper` (which is `extern "C"`) and into LMDB. This is undefined behavior per the Rust reference ("Unwinding through an `extern "C"` ABI boundary is undefined behavior"). The practical outcome on Linux/x86-64 is typically process abort, but it is UB and cannot be relied upon.

**Fix options (in order of preference):**

1. **`-fno-exceptions` in build.rs:** Makes `throw` into an immediate `std::terminate()`. Comparator exceptions only fire on malformed keys (too-short), which is a programming error, not a runtime condition — `abort()` is the correct response. Add `.flag("-fno-exceptions")` to `cc::Build`. **Also** modify the vendored source to replace `throw` with `std::abort()` or a conditional return for defense-in-depth.

2. **Replace `throw` in vendored copy:** Vendor the comparators into `reference/golpe_comparators.cpp` with `throw hoytech::error(...)` replaced by `std::abort()`. No C++ exception support needed.

3. **`extern "C-unwind"` in Rust (NOT recommended for this case):** The new Rust ABI for exception-safe FFI. Doesn't help here because C++ exceptions crossing `extern "C"` into Rust still require the C++ side to compile with exceptions enabled, and the exception type must be handled.

**Recommended:** Option 1 + 2 combined (compile with `-fno-exceptions`, AND replace `throw` in vendored source with `abort()`). Belt-and-suspenders.

### cc Crate Build Pattern (VERIFIED for Linux; ASSUMED for musl Alpine)

```rust
// build.rs
fn main() {
    println!("cargo:rerun-if-changed=reference/golpe_comparators.cpp");
    println!("cargo:rerun-if-changed=reference/lmdbxx/lmdb++.h");

    cc::Build::new()
        .cpp(true)
        .file("reference/golpe_comparators.cpp")
        .include("reference/lmdbxx")          // hoytech/lmdbxx header-only C++17 wrapper
        .include("path/to/lmdb/sys/include")  // lmdb.h (from lmdb-sys or system)
        .flag("-std=c++17")                   // hoytech/lmdbxx requires C++17
        .flag("-fno-exceptions")              // prevent UB from throw across FFI
        .flag("-fno-rtti")                    // optional: reduce code size
        .compile("golpe_comparators");        // produces libgolpe_comparators.a
}
```

**Musl/Alpine static libstdc++ (LOW confidence — spike required):**
- On Alpine, `apk add musl-dev g++` provides `libstdc++` as static `.a`. Alpine packages static versions under e.g. `libstdc++-dev`.
- The cc crate on musl automatically emits `cargo:rustc-link-lib=stdc++` for the dynamic version. For static, either:
  - `cc::Build::new().cpp_link_stdlib("stdc++")` + `CXXSTDLIB=stdc++` env var, and pass `-static-libstdc++` CXXFLAG, OR
  - Emit `println!("cargo:rustc-link-lib=static=stdc++");` manually in build.rs.
- `g++` on musl (Alpine) produces object files compatible with the musl libc — the resulting static archive links into the musl-based Rust binary.
- **Spike risk:** The exact combination of cc crate flags + musl toolchain for static libstdc++ is not verified against an actual Alpine build. The planner must include a smoke-test task: build the comparator archive on the target Alpine image and verify `ldd` shows no dynamic libstdc++ dependency.

**lmdbxx dependency:** hoytech/lmdbxx is header-only (one file: `lmdb++.h`). It only needs `lmdb.h` as an include dependency. The `heed` crate already depends on `lmdb-sys` which vendors `lmdb.h` — check if `lmdb-sys`'s include path can be found via `OUT_DIR` or if a system `lmdb-devel` package is required.

**Sources:** `[CITED: docs.rs/cc/latest/cc/]`, `[CITED: raw.githubusercontent.com/hoytech/rasgueadb/master/utils.h.tt]`, `[CITED: rust-lang.github.io/rfcs/2945-c-unwind-abi.md]`

---

## R3: dockurr/strfry Version Resolution

> **Status: PARTIALLY RESOLVED — needs runtime docker inspect for exact digest.**

### Current dockurr/strfry state (as of research date 2026-06-10)

- **Latest tag:** `1.1.0` published approximately 2026-04-26 (`~1 month ago` at research time). `[CITED: hub.docker.com/r/dockurr/strfry/tags]`
- **AMD64 partial digest:** `sha256:083d8941253a...` (truncated by Docker Hub UI — full 64-char digest requires `docker manifest inspect` or `docker pull` + `docker inspect`). `[CITED: hub.docker.com/r/dockurr/strfry/tags]`
- **Build process:** dockurr/strfry builds from hoytech/strfry master at release time (release notes show "Pull from upstream hoytech:master"). Version 1.1.0 release notes mention only dependency/action updates, not a new upstream strfry feature. `[CITED: github.com/dockur/strfry/releases]`
- **strfry version in 1.1.0:** Not definitively resolved from available sources. The dockurr/strfry Dockerfile builds from a git submodule checkout of hoytech/strfry, without an explicit version pin ARG. The actual strfry commit in `1.1.0` must be determined by running the image and checking `strfry --version` or inspecting the image layers.
- **dbVersion:** `CURR_DB_VERSION = 3` confirmed in strfry `src/constants.h` as of the current master. `[CITED: github.com/hoytech/strfry/blob/master/src/constants.h]`
- **Parent Dockerfile.strfry:** Currently `FROM dockurr/strfry:latest` (confirmed by reading the file). This is unpinned and must be changed to `FROM dockurr/strfry@sha256:<full-digest>` per D-09.

### How to resolve the full digest (plan-time action)

The planner must include a task to:
1. `docker pull dockurr/strfry:1.1.0`
2. `docker inspect dockurr/strfry:1.1.0 --format '{{index .RepoDigests 0}}'` → full `sha256:...` digest
3. `docker run --rm dockurr/strfry:1.1.0 /app/strfry --version` → exact strfry version string
4. Cross-reference the version string against `hoytech/strfry` git tags or commits
5. Record: image tag + full digest + strfry version string + strfry git commit SHA in `reference/PROVENANCE.md` and `~/deepfry/lmdb2graphql.yaml`
6. Update `../Dockerfile.strfry` `FROM` line to use digest (D-09)

**Note:** Docker is not available in the current environment (macOS dev machine without Docker Desktop). This task must be executed in an environment where Docker is available.

---

## R4: Deterministic Fixture Generation

> **Status: APPROACH CLEAR, SIGNED EVENTS REQUIRE TOOLING.**

### Fixture generation mechanism

1. **Run pinned strfry image** (`dockurr/strfry@sha256:<digest>`) in Docker.
2. **Ingest adversarial seed events** via strfry's WebSocket NIP-01 interface on port 7777.
3. Events must be **valid signed Nostr events** — strfry validates signatures on ingest. The seed events must be pre-signed.
4. **Copy the resulting `data.mdb` + `lock.mdb`** out of the Docker volume and commit to the repo as `tests/fixture/`.

### Signed event generation

Strfry requires NIP-01 compliant signed events. Options:
- **`strfry import` from JSONL** — strfry can import from a newline-delimited JSON file: `strfry import < seed_events.jsonl`. This bypasses the WebSocket and directly imports pre-signed events. `[CITED: github.com/hoytech/strfry README — import command]`
- **`nak` CLI tool** — a Nostr event construction CLI (`github.com/fiatjaf/nak`) can sign events from the command line with a specified private key.
- **Pre-computed JSON** — The adversarial seed events can be authored as a JSONL file with a known test keypair, signed offline, and committed alongside the fixture. The fixture generation script then runs `strfry import < tests/fixture/seed_events.jsonl`.

### Determinism guarantee

The `strfry import` path is deterministic given the same JSONL input and strfry version: the same events produce the same `levId` assignment (monotonic) and the same index key values. The `data.mdb` content will be byte-identical across runs on the same machine architecture with the same strfry version.

**However:** `lock.mdb` contains the reader table and is not meaningful to commit — the CI fixture open must use `EnvFlags::NO_LOCK` (safe for static fixture with no concurrent strfry writer). Commit only `data.mdb`; generate `lock.mdb` at test time with `lmdb_env_create_nolock` or equivalent.

**Actually for LMDB:** The lock file must exist for `mdb_env_open` to succeed unless `MDB_NOLOCK` is used. For committed fixture tests, either (a) commit both `data.mdb` and an empty `lock.mdb`, or (b) use `EnvFlags::NO_LOCK` in test environments. Option (b) is cleaner.

### CI regeneration assertion (D-10)

CI pipeline:
1. Pull pinned image by digest.
2. Run `docker run --rm -v ./ci-fixture:/app/strfry-db <image> strfry import < tests/fixture/seed_events.jsonl`.
3. Compare `sha256sum tests/fixture/data.mdb ci-fixture/data.mdb` — must match.
4. On mismatch: fail CI and alert that the strfry version has diverged or the import is non-deterministic.

---

## Common Pitfalls

### Pitfall 1: Comparator Not Registered — Silent Wrong Results

**What goes wrong:** `Event__*` sub-DB is opened without calling `.key_comparator::<GolpeComparator>()`. LMDB uses default `memcmp`. Range scans on `Event__pubkey` return events in wrong order; `MDB_SET_RANGE` lands at the wrong B-tree position. No error is returned — just wrong data.

**Why it happens:** The `open()` call succeeds regardless. `heed` returns `Ok(Some(db))` whether or not the comparator is set. Forgetting `.key_comparator()` in the builder chain is a silent bug.

**How to avoid:** Make the comparator type a required type parameter by wrapping the open call in a function that forces the generic: `open_event_index::<C: Comparator>(name) -> Database<Bytes, Bytes>`. The self-check will catch it, but the self-check itself must be tested against a fixture where the misregisted comparator produces a different scan order (i.e., the adversarial seed must be adversarial enough to produce a wrong order under `memcmp`).

**Warning signs:** Self-check assertion failure in CI. In production without self-check: events for a pubkey query are missing or belong to wrong pubkeys.

### Pitfall 2: C++ Exception Crosses Rust FFI — Undefined Behavior

**What goes wrong:** A malformed key (shorter than 8 or 16 bytes) triggers `throw hoytech::error(...)` in the C++ comparator. The exception unwinds through the `extern "C"` heed trampoline into LMDB. On most Linux builds this causes a process abort (std::terminate), but it is formally undefined behavior — the compiler can miscompile code around it.

**Why it happens:** The golpe comparators were written for C++ callers that handle `hoytech::error` exceptions. The Rust FFI boundary is `extern "C"`, which does not declare exception behavior. With default compiler flags (exceptions enabled), this is a latent hazard.

**How to avoid:** Compile with `-fno-exceptions`. Replace `throw` in the vendored copy with `std::abort()`. Test with keys shorter than minimum expected lengths.

**Warning signs:** Process crashes during comparator self-check with no Rust panic. Unexplained aborts in LMDB range scans.

### Pitfall 3: Meta Struct Byte Offset Mismatch

**What goes wrong:** The Meta golpe struct layout is assumed incorrectly. If `dbVersion` is not at bytes `[0..4]` in the Meta value, the version gate either always passes (if the parsed value happens to be 3) or always fails.

**Why it happens:** golpe struct layout is generated from `golpe.yaml` and depends on field order + alignment. The exact layout is not in spec.md and must be read from the strfry source.

**How to avoid:** Read strfry's `src/` generated struct for `Meta` from the pinned commit. The planner must include a task: open strfry's `build/golpe.h` (or equivalent generated file) and extract the exact field offsets.

**Warning signs:** Version gate passes even with a wrong-version DB, or fails even with a correct DB.

### Pitfall 4: `map_size` Too Small — Env Open Fails

**What goes wrong:** Opening the LMDB env with `map_size` smaller than strfry's `mapsize` (`10995116277760` = 10 TiB) returns a mapping error. The env cannot be opened.

**Why it happens:** Default or example map sizes in tutorials are 1 GiB or smaller. 10 TiB is unusual.

**How to avoid:** Set `.map_size(10_995_116_277_760)` from config. Expose it as a config parameter. Read from `~/deepfry/lmdb2graphql.yaml` → falls back to `10_995_116_277_760` if not set.

**Warning signs:** `heed::Error::Mdb(MDB_INVALID)` or similar on `env.open()`.

### Pitfall 5: IntegerComparator Misapplied to `Event__created_at`

**What goes wrong:** `Event__created_at` uses `MDB_INTEGERKEY` (heed `IntegerComparator`), not a custom golpe comparator. Applying a `Uint64Uint64Cmp` to it would be incorrect; applying `DefaultComparator` is wrong because `MDB_INTEGERKEY` must be declared.

**Why it happens:** The index-to-comparator mapping (spec §3.1 table) is easy to get wrong for the one integer-key-only index.

**How to avoid:** Map explicitly in code: `Event__created_at` → `IntegerComparator`; all others → golpe custom comparators. Document the mapping in a comment table in `indexes.rs`.

### Pitfall 6: Fixture `lock.mdb` Permissions in CI

**What goes wrong:** Tests fail because `mdb_env_open` cannot write to `lock.mdb` in the committed fixture path.

**Why it happens:** The fixture's `lock.mdb` may not be writable (CI checkout permissions), and without `NO_LOCK`, LMDB requires write access to `lock.mdb`.

**How to avoid:** Use `EnvFlags::NO_LOCK` in tests (CI only, static fixture, no concurrent strfry). Copy fixture to a temp directory before opening in tests if `NO_LOCK` is not an option.

---

## Code Examples

### Opening the strfry env read-only with correct map_size

```rust
// Source: [VERIFIED: docs.rs/heed/0.22.1/heed/struct.EnvOpenOptions.html]
// Source: [CITED: spec.md §4.1, strfry.conf]
use heed::{EnvOpenOptions, EnvFlags};

pub fn open_read_only_env(db_path: &Path, map_size: u64) -> heed::Result<heed::Env> {
    unsafe {
        EnvOpenOptions::new()
            .max_dbs(20)           // ≥ number of named sub-DBs in strfry
            .map_size(map_size)    // must be ≥ strfry's configured mapsize (10 TiB)
            .flags(EnvFlags::READ_ONLY)
            .open(db_path)
    }
}

// For CI fixture tests only (no live strfry):
pub fn open_fixture_env(fixture_path: &Path) -> heed::Result<heed::Env> {
    unsafe {
        EnvOpenOptions::new()
            .max_dbs(20)
            .map_size(10 * 1024 * 1024 * 1024 * 1024)
            .flags(EnvFlags::READ_ONLY | EnvFlags::NO_LOCK)
            .open(fixture_path)
    }
}
```

### Implementing the heed Comparator trait for golpe FFI

```rust
// Source: [ASSUMED — pattern derived from verified heed Comparator trait + FFI mechanism]
// In src/lmdb/comparators.rs

extern "C" {
    // Declared in golpe_comparators.cpp (compiled by build.rs)
    fn lmdb_comparator__StringUint64_safe(
        a_ptr: *const u8, a_len: usize,
        b_ptr: *const u8, b_len: usize,
    ) -> i32;
    // (similarly for Uint64Uint64 and StringUint64Uint64)
}

pub enum StringUint64Cmp {}
impl heed::Comparator for StringUint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> std::cmp::Ordering {
        let result = unsafe {
            lmdb_comparator__StringUint64_safe(
                a.as_ptr(), a.len(),
                b.as_ptr(), b.len(),
            )
        };
        result.cmp(&0)
    }
}

// The C++ wrapper exposed via extern "C" (in golpe_comparators.cpp):
// extern "C" int lmdb_comparator__StringUint64_safe(
//     const uint8_t* a_ptr, size_t a_len,
//     const uint8_t* b_ptr, size_t b_len
// ) {
//     MDB_val a = {a_len, (void*)a_ptr};
//     MDB_val b = {b_len, (void*)b_ptr};
//     if (a_len < 8 || b_len < 8) std::abort();  // throw replaced
//     return lmdb_comparator__StringUint64(&a, &b);
// }
```

### Golden vector oracle format

```json
// tests/fixture/golden_vectors/Event__pubkey.json
// Source: [ASSUMED] — format choice; content must be hand-computed from seed
{
  "comparator": "StringUint64",
  "index": "Event__pubkey",
  "seed_commit": "sha256:<fixture-data.mdb-hash>",
  "ordered_lev_ids": [3, 1, 5, 2, 4, 6]
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hand-roll `mdb_set_compare` via raw lmdb-sys | heed `Comparator` trait + `key_comparator()` | heed 0.20.0 (2023) | Eliminates manual `mdb_dbi_open` + `mdb_set_compare` FFI management |
| Approach A (derived SQLite store, no comparators needed) | Approach B (read strfry indexes live with comparators) | Project decision 2026-06-10 | Eliminates sync engine complexity; adds comparator FFI responsibility |
| Rust reimplementation of golpe comparators | C++ FFI to golpe's actual comparators | D-01 (2026-06-10) | Eliminates reimplementation risk; shifts risk to FFI build/safety story |
| `lmdb-rkv` (Mozilla, dormant) | `heed` (Meilisearch, active) | ~2023-2024 | Named-root iteration, typed API, `IntegerComparator`, active maintenance |

**Deprecated/outdated:**
- `DatabaseFlags::INTEGER_KEY`: Deprecated since heed 0.21 in favor of `DatabaseOpenOptions::key_comparator::<IntegerComparator>()`. Still works but prefer the new API. `[CITED: CLAUDE.md heed decision matrix]`
- `EnvOpenOptions::no_sub_dir()`: Not relevant here (strfry uses the standard subdirectory layout).
- The "Rust reimplementation" plan for golpe comparators: superseded by D-01.

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | dockurr/strfry:1.1.0 builds strfry with `CURR_DB_VERSION = 3` | R3 | Must re-verify fixture against actual image; if dbVersion changed, entire Phase 1 gate logic needs adjustment |
| A2 | The musl/Alpine static libstdc++ link for C++ compiled by the cc crate works without additional RUSTFLAGS | R2 musl story | Build fails on Alpine; requires investigation of `-static-libstdc++` flag or different stdlib selection |
| A3 | Meta struct field offsets: `dbVersion` at bytes 0..4, `endianness` at bytes 4..8 | Pattern 3 | Version gate silently passes or always fails; must be verified from strfry's generated golpe.h |
| A4 | `lmdb-sys` (heed's LMDB C library dependency) exposes an includeable `lmdb.h` path accessible in build.rs for compiling golpe_comparators.cpp | R2 build story | Need to find lmdb.h via environment or add a separate system lmdb-dev package |
| A5 | `strfry import < jsonl` produces a deterministic byte-identical `data.mdb` across runs | R4 | CI fixture regeneration assertion fails; need to investigate non-determinism sources |
| A6 | The heed `Comparator` trait's `compare` function is called synchronously and the Rust code does not use `panic!` in the comparator | R2 safety | Panic across extern C is also UB; ensure comparator `compare` impl never panics |
| A7 | `mdb_cmp_memn` referenced in rasgueadb utils.h.tt is a standard LMDB internal function available via lmdb.h | R2 compile | Build fails if `mdb_cmp_memn` is internal-only (not in public lmdb.h); may need to copy the memn implementation |

**If this table is empty:** N/A — assumptions are present and listed.

---

## Open Questions (RESOLVED)

> Each question below is **resolved-by-delegation**: it is not left open, but explicitly tracked and assigned to a specific plan-time spike task (with a documented fallback). The planner verified there is a concrete owner and escape hatch for each before finalizing the Phase 1 plans. None of these are forgotten or unscoped.

1. **Meta struct exact field offsets** — RESOLVED (delegated to SPIKE A3, plan 01-03 Task 1)
   - What we know: `dbVersion` and `endianness` exist in `Meta` record id=1; both are uint32/uint64 in native byte order.
   - What's unclear: Exact byte offsets in the golpe-generated struct. The struct likely has fields in YAML order with natural alignment, but alignment padding could shift offsets.
   - **Resolution:** Plan 01-03 Task 1 runs SPIKE A3 *first*: confirm the offsets from the pinned strfry build's generated `build/golpe.h` (reachable via the commit pinned in PROVENANCE.md) and record the confirmed offsets in a code comment citing the source. **Fallback if the assumed `[0..4]`/`[4..8]` layout is wrong:** use the offsets actually observed in `golpe.h` (the spike adjusts the parser before the gate is trusted); the fixture's known `dbVersion==3` provides a positive control that catches a wrong offset immediately.

2. **`mdb_cmp_memn` availability** — RESOLVED (delegated to SPIKE A7, plan 01-01 Task 2)
   - What we know: Used in `lmdb_comparator__StringUint64` to compare the string prefix. Declared in LMDB's internal headers.
   - What's unclear: Whether `mdb_cmp_memn` is in the public `lmdb.h` (exposed by `lmdb-sys`) or only in LMDB's private `midl.h`.
   - **Resolution:** Plan 01-01 Task 2 runs SPIKE A7 during the comparator vendoring: check the `lmdb-sys` vendored `lmdb.h` for `mdb_cmp_memn`. **Fallback if absent:** inline the equivalent implementation (`if (a->mv_size != b->mv_size) return a->mv_size < b->mv_size ? -1 : 1; return memcmp(a->mv_data, b->mv_data, a->mv_size);`) directly in the vendored C++ so the build does not depend on a private symbol.

3. **Exact dockurr/strfry:1.1.0 digest and strfry git commit** — RESOLVED (delegated to plan 01-02 `checkpoint:human-action`)
   - What we know: Tag 1.1.0 exists, AMD64 partial digest `083d8941253a`, published ~2026-04-26.
   - What's unclear: Full 64-character sha256 digest; exact hoytech/strfry commit.
   - **Resolution:** Plan 01-02 Task 2 is a `checkpoint:human-action` that resolves the full digest + version string + git commit + `dbVersion==3` confirmation on a Docker-capable host (Docker is unavailable on the dev machine). The resolved values are then recorded across PROVENANCE.md, the parent Dockerfile, and config (plan 01-02 Task 3). **Fallback:** if 1.1.0 reports a `dbVersion != 3`, A1 escalates — the Phase 1 gate logic assumptions are revisited before the fixture is committed.

4. **`strfry import` determinism** — RESOLVED (delegated to SPIKE A5, plan 01-02 fixture task 01-02 Task 5)
   - What we know: strfry assigns `levId` monotonically and writes events sequentially.
   - What's unclear: Whether page layout, B-tree node split decisions, or checkpoint writes produce byte-identical `data.mdb` across independent `import` runs.
   - **Resolution:** Plan 01-02 Task 5 (fixture generation, on the Docker host) runs SPIKE A5: import twice and compare `sha256sum data.mdb`. **Fallback if NOT byte-identical:** record in PROVENANCE.md that CI must compare *semantically* (all expected events present + correct scan order) rather than byte-exact, and flag this for plan 01-03's self-check. Either way the committed fixture is the single pinned artifact the golden vectors and self-check bind to (via `seed_commit` sha256).

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Rust toolchain (rustc, cargo) | All compilation | ✓ | rustc 1.89.0 (2025-08-04) | — |
| Docker | Fixture generation (R3/R4), CI | ✗ (dev machine) | — | Must use CI environment or a machine with Docker |
| C++ compiler (g++ or clang++) | build.rs for golpe comparators | [ASSUMED ✓ on Linux CI] | — | Must be present in Docker build image |
| lmdb-dev / liblmdb (system header) | golpe_comparators.cpp include | [ASSUMED via lmdb-sys] | — | If lmdb-sys doesn't expose lmdb.h, add system package |
| Alpine musl toolchain | Static release binary | [ASSUMED available in rust:alpine image] | — | Use Debian image with musl-tools |
| strfry relay binary | Fixture generation | ✗ (dev machine) | — | Docker-only; not needed for pure Rust compilation |
| strfry DB (live) | Production access | ✗ (dev machine) | — | Use committed fixture for tests |

**Missing dependencies with no fallback (blocking):**
- Docker is required for fixture generation (R3/R4) and CI verification (D-10). Must be available in CI environment.

**Missing dependencies with fallback:**
- System lmdb.h: If not in lmdb-sys, copy from LMDB source or use pkg-config.
- Live strfry: All Phase 1 tests use the committed fixture; live strfry not required until Phase 2+.

---

## Validation Architecture

> `workflow.nyquist_validation` is `false` in config.json — this section is skipped per configuration.

---

## Security Domain

> `security_enforcement: true` in config.json; `security_asvs_level: 1`.

### Applicable ASVS Categories (Phase 1 only — no network surface yet)

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | No auth surface in Phase 1 |
| V3 Session Management | No | No sessions |
| V4 Access Control | Partial | Read-only LMDB access enforced at OS level (`:ro` Docker mount) and by never calling `env.write_txn()` |
| V5 Input Validation | Yes | Validate Meta field values before using (dbVersion, endianness) — never trust raw bytes as safe values |
| V6 Cryptography | No | No crypto in Phase 1 |
| V7 Error Handling | Yes | Startup gate must exit loudly (not silently continue) on version/endianness mismatch |
| V14 Configuration | Yes | Config file at `~/deepfry/lmdb2graphql.yaml` must not be overwritten in tests; use tempdir |

### Known Threat Patterns (Phase 1)

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Malformed LMDB key from corrupted strfry DB | Tampering | Comparator `-fno-exceptions`; abort on too-short keys; read-only env prevents write corruption |
| Strfry upgrade silently changes dbVersion | Tampering/Elevation | Hard gate: refuse to start if `dbVersion != 3`; CI pins exact digest |
| Wrong comparator registered (memcmp instead of golpe) | Tampering | Self-check startup gate; adversarial fixture ensures memcmp produces wrong order |
| Config file overwrite in tests | Tampering | Use tempdir for test config loading (per CLAUDE.md directive) |
| Accidental write txn opened | Tampering (data corruption) | `EnvFlags::READ_ONLY`; Docker `:ro` mount; static analysis: no `env.write_txn()` calls |

---

## Sources

### Primary (HIGH confidence)
- heed 0.22.1 `env.rs` source — `raw_init_database` calls `mdb_set_compare` via `custom_key_cmp_wrapper::<C>` `[VERIFIED: github.com/meilisearch/heed/blob/v0.22.1/heed/src/envs/env.rs]`
- heed 0.22.1 `database.rs` source — `open()` and `create()` both call `raw_init_database` `[VERIFIED: github.com/meilisearch/heed/blob/v0.22.1/heed/src/databases/database.rs]`
- rasgueadb `utils.h.tt` — exact C++ source of all three golpe comparators `[VERIFIED: raw.githubusercontent.com/hoytech/rasgueadb/master/utils.h.tt]`
- strfry `src/constants.h` — `CURR_DB_VERSION = 3` `[CITED: github.com/hoytech/strfry/blob/master/src/constants.h]`
- strfry `golpe.yaml` — index declarations and comparator assignments `[CITED: github.com/hoytech/strfry/blob/master/golpe.yaml]`
- `strfry.conf` — `mapsize = 10995116277760`, `maxreaders = 256` `[VERIFIED: local file at ../config/strfry/strfry.conf]`
- spec.md §3, §6.1, §6.2, §6.3 — on-disk encoding; custom comparator warning; native-endian integers `[CITED: local spec.md]`
- cc crate 1.2.x docs — `cpp(true)`, `flag("-fno-exceptions")`, `cpp_link_stdlib` `[CITED: docs.rs/cc/latest/cc/]`

### Secondary (MEDIUM confidence)
- dockurr/strfry:1.1.0 published ~2026-04-26, AMD64 partial digest `083d8941253a` `[CITED: hub.docker.com/r/dockurr/strfry/tags]`
- heed 0.20.0 release notes — custom comparator added, uses `create()` in examples `[CITED: github.com/meilisearch/heed/releases/tag/v0.20.0]`
- LMDB `mdb_set_compare` constraints — must be called before data access; safe in read-only mode `[CITED: ncbi.nlm.nih.gov lmdb.h documentation]`
- Rust RFC 2945 c-unwind-abi — exceptions crossing `extern "C"` boundary is UB `[CITED: rust-lang.github.io/rfcs/2945-c-unwind-abi.md]`
- golpe rasgueadb README — composite key format and comparator declaration syntax `[CITED: github.com/hoytech/rasgueadb]`
- dockurr/strfry releases — v1.1.0 is current latest, pulls from hoytech:master `[CITED: github.com/dockur/strfry/releases]`

### Tertiary (LOW confidence — flagged for validation)
- musl/Alpine static libstdc++ linking with cc crate — from community discussions, not officially documented `[LOW]`
- Meta struct field offsets (dbVersion at 0..4, endianness at 4..8) — inferred from golpe conventions, not verified from generated source `[LOW — see A3]`
- `mdb_cmp_memn` availability in public lmdb.h — assumed from LMDB internals; must be verified `[LOW — see A7]`
- `strfry import` byte-determinism — assumed from monotonic levId assignment; untested empirically `[LOW — see A5]`

---

## Metadata

**Confidence breakdown:**
- R1 (heed comparator hook): HIGH — verified from heed 0.22.1 source code
- Comparator implementations: HIGH — extracted from rasgueadb primary source
- C++ exception UB problem: HIGH — confirmed by Rust RFC 2945
- R2 (musl static C++ link): LOW — community knowledge, untested on Alpine
- R3 (strfry digest): MEDIUM — partial digest found; full digest needs docker inspect
- R4 (fixture generation): MEDIUM — approach clear; determinism untested
- Meta struct offsets: LOW — inferred, not verified from generated source

**Research date:** 2026-06-10
**Valid until:** 2026-07-10 (30 days — heed and cc are stable; strfry format can change on upstream update)

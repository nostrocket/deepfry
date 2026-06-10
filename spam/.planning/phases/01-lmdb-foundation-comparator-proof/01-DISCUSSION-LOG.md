# Phase 1: LMDB Foundation & Comparator Proof - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-10
**Phase:** 1-LMDB Foundation & Comparator Proof
**Areas discussed:** Fixture + version pin, Self-check oracle, Index coverage, Deliverable shape (+ C++-comparator fork)

---

## Fixture + version pin

### Version pin
| Option | Description | Selected |
|--------|-------------|----------|
| Resolve latest now, pin digest | Pull dockurr/strfry:latest today, record sha256 digest + strfry git commit | ✓ |
| Pin a specific strfry release tag | Choose a known upstream release independent of the dockurr image | |
| I'll specify the version | User-supplied version/commit/digest | |

### Fixture source
| Option | Description | Selected |
|--------|-------------|----------|
| Generate in CI, commit a small seed | Seed script + known events; CI builds fixture fresh | |
| Commit a binary fixture data.mdb | Commit data.mdb (+ lock.mdb) directly as a test asset | |
| Both: committed fixture + CI regen | Committed fixture for fast local tests + CI regen asserts match | ✓ |

### Comparator spec source-of-truth
| Option | Description | Selected |
|--------|-------------|----------|
| Vendor golpe.yaml + cite source lines | Copy golpe.yaml + comparator source into repo with commit SHA | ✓ |
| Reference upstream by pinned commit | Record commit SHA; fetch comparator defs from upstream at pin | |

### Seed design
| Option | Description | Selected |
|--------|-------------|----------|
| Adversarial, targets each comparator | Force the exact cases naive memcmp gets wrong, per comparator | ✓ |
| Realistic sample dump | Representative real-ish events | |
| Both layered | Adversarial core + realistic breadth | |

### Parent Dockerfile pin
| Option | Description | Selected |
|--------|-------------|----------|
| Record + flag, don't edit parent | Record digest as contract; recommend parent pin, don't modify | |
| Also pin the parent Dockerfile | Edit Dockerfile.strfry to pin the same digest | ✓ |

**Notes:** Pinning the parent Dockerfile is a deliberate cross-repo change into `/Users/gareth/git/nostr/deepfry/`, accepted in this phase.

---

## Self-check oracle

### Oracle source (first pass)
| Option | Description | Selected |
|--------|-------------|----------|
| Committed golden vectors | Analytic expected order from seed + comparator rules, hand-audited | (resolved below) |
| Capture from a reference strfry | Extract strfry's actual physical index order | |
| Differential vs reference comparator | Build golpe's comparator as reference, assert pairwise agreement | |
| **Other (user):** "Can we use C++/golpe comparator directly instead of rebuilding it in rust?" | Triggered the C++-comparator fork below | ✓ (re-scoped) |

### Check rigor
| Option | Description | Selected |
|--------|-------------|----------|
| Full ordered-sequence equality per index | Assert full levId sequence == golden, fail closed | ✓ |
| Adjacent-pair monotonicity | Assert each adjacent pair correctly ordered | |
| Both | Full equality gate + per-pair test diagnostics | |

### Keep the self-check at all? (after C++-in-production chosen)
| Option | Description | Selected |
|--------|-------------|----------|
| Keep it — reframed as wiring/drift gate | Guards silent registration failure, mis-wiring, build/endianness drift | ✓ |
| Drop it — trust the linked comparator | Rely only on dbVersion gate + endianness assert | |
| Keep, but lighter (smoke check) | Minimal startup check, full equality in CI only | |

### Oracle source (re-resolved with C++ in production)
| Option | Description | Selected |
|--------|-------------|----------|
| Committed golden vectors (hand-audited) | Independent human-verified ground truth, non-circular | ✓ |
| Dump from a golpe-linked tool | Uses same comparator we ship — circular, weaker proof | |

**User's choice:** Golden vectors, full-sequence equality, self-check kept and reframed.
**Notes:** User challenged whether the self-check is still needed once golpe's real comparator ships. Conclusion: yes — its role shifts from "reimplementation-correctness proof" to a wiring/registration/drift fail-closed gate (catches heed silently falling back to memcmp, wrong comparator on wrong index, build/host drift). Still required by LMDB-06 + criteria #3/#4.

---

## C++-comparator fork (emerged from the oracle discussion)

The user asked whether golpe's C++ comparator could be used directly instead of reimplementing in Rust, then asked about simply including lmdbxx/golpe headers, then asked to confirm the deeper risk: "can heed register a foreign comparator at all?"

Key clarification surfaced: both the Rust and C++ paths register through heed's `Comparator` trait — Rust puts byte logic in `compare()`, C++ makes `compare()` a thin FFI call. So "can heed register a foreign comparator?" has one answer for both paths and is the real go/no-go gate (Research item #1).

### Where does golpe's C++ comparator live?
| Option | Description | Selected |
|--------|-------------|----------|
| Test/oracle only — Rust ships | C++ compiled only for tests/self-check; production stays pure-Rust | |
| Ships in production — drop Rust reimpl | compare() = thin FFI into golpe's compiled comparator; no Rust reimpl | ✓ |
| Build both, compare, decide at spike end | Implement both, compare scan order + build cost | |

**User's choice:** Ship golpe's C++ comparator in production; drop the Rust reimplementation.
**Notes:** Amends LMDB-05 (and PROJECT.md / CLAUDE.md "reimplement in Rust" wording). Static Alpine binary now carries a C++/golpe build. C++ artifact retains option-value as a fallback escape hatch.

---

## Index coverage

| Option | Description | Selected |
|--------|-------------|----------|
| All six indexes | Prove Event__id, __pubkey, __created_at, __kind, __pubkeyKind, __tag | ✓ |
| One representative per comparator | One index per distinct comparator + created_at | |

---

## Deliverable shape

### Deliverable
| Option | Description | Selected |
|--------|-------------|----------|
| Foundational crate module | Real lmdb-access module + reusable startup self-check gate | ✓ |
| Throwaway harness first | Standalone proof binary, rebuild production module in Phase 2 | |

### Config
| Option | Description | Selected |
|--------|-------------|----------|
| Minimal config file now | ~/deepfry/lmdb2graphql.yaml with db path + pinned version | ✓ |
| Hardcode for the spike | Constants now; config file in Phase 5 | |

---

## Claude's Discretion

- Crate/module layout, error type design (`thiserror`), logging/diagnostics format for gate failures.
- How pinned-vs-detected version drift is surfaced at startup (basic log line in Phase 1; full OPS-04 treatment in Phase 5).

## Deferred Ideas

- Doc-sync: update LMDB-05 / PROJECT.md Key Decisions / CLAUDE.md to reflect FFI-to-golpe (not Rust reimpl).
- CLAUDE.md cleanup: remove stale rusqlite/SQLite "derived index" entries (contradict Approach B).
- OPS-04 (Phase 5): richer version-drift surfacing in stats/startup.
- Cross-arch / big-endian support (v2, PORT-01) — out of scope.
- Research items R1–R4 captured in CONTEXT.md `<deferred>`.

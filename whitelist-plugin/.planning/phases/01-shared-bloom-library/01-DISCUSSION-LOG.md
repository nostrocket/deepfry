# Phase 1: Shared Bloom Library - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-29
**Phase:** 1-Shared Bloom Library
**Areas discussed:** Library vs hand-rolled, Generation/version marker, Serialized wire format, Query API input type

---

## Library vs hand-rolled

| Option | Description | Selected |
|--------|-------------|----------|
| Hand-roll minimal filter | ~100 lines, zero new deps, full control of wire format | |
| bits-and-blooms/bloom/v3 | Battle-tested, BinaryMarshaler built in; adds a dependency | ✓ |
| You decide | Defer to researcher/planner | |

**User's choice:** bits-and-blooms/bloom/v3
**Notes:** Confirmed v3.7.1. De-risks the FP-rate math. Hard invariant added during research: never call `bitset.LittleEndian()` (global switch that breaks the portable big-endian format).

---

## Generation/version marker

| Option | Description | Selected |
|--------|-------------|----------|
| Content hash of filter bits | sha256 over the bit array; deterministic; clean 304s; survives restarts | ✓ |
| Server monotonic counter | Simple, but ETag changes even when unchanged; resets on restart | |
| Build timestamp (unix) | Human-readable but changes every refresh regardless of content | |

**User's choice:** Content hash of filter bits
**Notes:** Implemented as `sha256.Sum256(bf.MarshalBinary())`. Becomes the Phase 2 ETag and the Phase 3 disk-staleness signal.

---

## Serialized wire format

| Option | Description | Selected |
|--------|-------------|----------|
| Custom binary header + raw library payload | magic + version + fp-rate + gen marker + payloadLen, then bbloom.WriteTo bytes | ✓ |
| encoding/gob of a struct | Trivial but Go-specific and opaque | |
| JSON metadata + base64 bits | Inspectable/portable but ~33% larger and slower | |

**User's choice:** Custom binary header + library payload (locked after research)
**Notes:** User asked for research first. Research confirmed bits-and-blooms `WriteTo`/`MarshalBinary` is portable big-endian and self-describing for m/k, but does NOT serialize fp-rate. Decision: header carries only fp-rate + format version + 32-byte generation marker + payloadLen; do not re-store m/k (library payload owns them). payloadLen prefix added for truncation-safe framing.

---

## Query API input type

| Option | Description | Selected |
|--------|-------------|----------|
| [32]byte core + hex helper | Matches whitelist map; alloc-free hot path; hex isolated to boundary | ✓ |
| [32]byte only | Smallest surface; callers decode hex | |
| hex string only | Simplest call sites; per-event decode+alloc in hot path | |
| []byte | Matches library directly; loses 32-byte invariant | |

**User's choice:** [32]byte core + hex helper (locked after research)
**Notes:** Research confirmed `bbloom.Add`/`Test` take `[]byte` and `k[:]` slicing is alloc-free. Refined into a role split: `Builder` (server, mutable, build) and immutable `Filter` (plugin, query-only) with `Generation()`/`ETag()`/`WriteTo` + package `ReadFilter`.

---

## Claude's Discretion

- Internal hashing path (owned by bits-and-blooms; no custom hashing of the high-entropy 32-byte pubkey).
- Internal struct field layout, error sentinel naming, `MarshalBinary`-over-`WriteTo` implementation detail.
- Build-time sizing knobs beyond `(n, fp)` — standard `NewWithEstimates` assumed.

## Deferred Ideas

None — discussion stayed within phase scope. Faster refresh (GATE-F1) and bloom metrics (GATE-F2) already live in REQUIREMENTS.md "v2 / Future".

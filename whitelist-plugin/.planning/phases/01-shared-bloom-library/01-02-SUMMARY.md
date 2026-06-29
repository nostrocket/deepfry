---
phase: 01-shared-bloom-library
plan: "02"
subsystem: pkg/bloom
tags: [bloom, benchmark, performance, alloc-free, go]
status: complete

dependency_graph:
  requires:
    - pkg/bloom (Builder, Filter, Contains — Plan 01-01)
  provides:
    - pkg/bloom/bloom_bench_test.go (BenchmarkContains, BenchmarkBuild, TestContainsZeroAllocs)
  affects:
    - Phase 3 (GATE-01..07): proves Contains is alloc-free at the hot path before plugin is built

tech_stack:
  added: []
  patterns:
    - testing.AllocsPerRun as a hard CI gate for alloc-free invariants
    - size-swept b.Run benchmarks with ReportAllocs (mirrors whitelist_bench_test.go shape)
    - genKeys reused from bloom_test.go (same package, no redeclaration)

key_files:
  created:
    - whitelist-plugin/pkg/bloom/bloom_bench_test.go (105 lines)
  modified: []

decisions:
  - genKeys/makeKey already declared in bloom_test.go; bloom_bench_test.go reuses them without redeclaration (same package bloom)
  - miss key generated with offset 1_000_000 beyond member count to guarantee non-membership
  - BenchmarkBuild loops Add+Build inside b.N to characterize full construction cost (34ms, ~7MB at 500k)

metrics:
  duration_seconds: 61
  completed_date: "2026-06-30"
  tasks_completed: 1
  tasks_total: 1
  files_created: 1
  files_modified: 0
---

# Phase 01 Plan 02: Shared Bloom Library — Benchmark Gate Summary

**One-liner:** Size-swept Filter.Contains benchmarks (0 allocs/op all sizes) and a TestContainsZeroAllocs hard CI gate confirming the D-08 alloc-free k[:] hot-path claim.

## What Was Built

Created `whitelist-plugin/pkg/bloom/bloom_bench_test.go` (105 lines, `package bloom`):

**`BenchmarkContains`** — size-swept over {1k, 10k, 100k, 500k} members with two sub-benchmarks per size:
- `hit/n=N`: queries a key known to be a filter member; `b.ReportAllocs()`
- `miss/n=N`: queries a key offset 1M beyond member range (provably absent); `b.ReportAllocs()`
- Measured result: **0 B/op, 0 allocs/op** across all sizes and both paths on Apple M3 Pro

**`BenchmarkBuild`** — full `NewBuilder + Add×500k + Build()` cycle at the production-sized 500k set:
- Measured: ~34ms/op, ~7MB/op, 22 allocs/op (construction cost characterized for Phase 2 capacity planning)

**`TestContainsZeroAllocs`** — a standard Go test (not a benchmark) that uses `testing.AllocsPerRun(100, ...)` over `Filter.Contains(k)` and calls `t.Fatalf` if the average is non-zero. This converts the D-08 alloc-free claim into a hard `go test` gate that will catch any future heap-escape regression without requiring a human to read `-benchmem` output.

## Verification Results

| Check | Command | Result |
|-------|---------|--------|
| Zero-allocs CI gate | `go test ./pkg/bloom/ -run TestContainsZeroAllocs -count=1` | PASS (0.00s) |
| Hit benchmarks 0 allocs/op | `go test -bench=BenchmarkContains -benchmem -run '^$' -benchtime=10000x ./pkg/bloom/` | 0 B/op, 0 allocs/op all sizes |
| Miss benchmarks 0 allocs/op | same run above | 0 B/op, 0 allocs/op all sizes |
| Build benchmark | `go test -bench=BenchmarkBuild -benchmem -run '^$' -benchtime=10x ./pkg/bloom/` | PASS (~34ms, 22 allocs) |
| Full test suite (no regressions) | `go test ./pkg/bloom/ -count=1` | PASS (0.687s) |
| ReportAllocs present | `grep -c 'ReportAllocs' bloom_bench_test.go` | 4 |
| AllocsPerRun present | `grep -c 'AllocsPerRun' bloom_bench_test.go` | 2 |
| LittleEndian absent (D-02) | `grep -v '^//' bloom_bench_test.go \| grep -c 'LittleEndian'` | 0 |

## Deviations from Plan

None — plan executed exactly as written. `genKeys` was already declared in `bloom_test.go` (same package); the bench file reuses it without redeclaration, as the plan permitted ("reuse it").

## Requirement Coverage

- **BLOOM-03 hot-path query (T-01-05 mitigation):** `TestContainsZeroAllocs` + `BenchmarkContains` with `ReportAllocs` enforce 0 allocs/op, preventing a heap-escape regression that would degrade the ~10k events/sec plugin path (D-08). SATISFIED.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | 887d733 | test(01-02): add bloom benchmark gate validating D-08 alloc-free Contains hot path |

## Self-Check: PASSED

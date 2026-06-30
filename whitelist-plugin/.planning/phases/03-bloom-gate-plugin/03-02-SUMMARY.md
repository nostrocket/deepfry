---
phase: 03-bloom-gate-plugin
plan: "02"
subsystem: bloomgate
tags: [bloom, fetcher, conditional-get, atomic-swap, disk-persistence, cold-start, strfry-plugin]
status: complete

dependency_graph:
  requires:
    - "03-01: BloomConfig + BloomChecker (checker.Store, checker.filter atomic.Pointer)"
    - "02-02: Server GET /bloom endpoint (200/304/503 + ETag/If-None-Match wire contract)"
    - "01-01: pkg/bloom ReadFilter / Filter.WriteTo / Filter.ETag / Filter.MarshalBinary"
  provides:
    - "pkg/bloomgate/fetcher.go: BloomFetcher periodic conditional-GET, parse-before-persist, temp+rename, disk-first cold start"
    - "cmd/bloom/main.go: fourth standalone StrFry writePolicy plugin entrypoint"
  affects:
    - "Phase 4 Ops/Integration: cmd/bloom must be added to Makefile and Docker targets"

tech_stack:
  added:
    - "net/http/httptest (test only)"
    - "bytes.NewReader (for parse-before-persist pattern)"
    - "io.ReadAll (body buffering before ReadFilter)"
    - "os.Rename (atomic temp+rename idiom)"
  patterns:
    - "Single-writer/many-reader atomic.Pointer[bloom.Filter] swap (mirrors pkg/whitelist/whitelist.go)"
    - "Retry loop with linear backoff + ctx-cancellation (mirrors pkg/whitelist/whitelist_refresher.go)"
    - "Conditional GET with If-None-Match: filter.ETag()"
    - "Parse-before-persist: bloom.ReadFilter validates before any swap or disk write"
    - "Atomic temp+rename: write bloomPath.tmp then os.Rename (D-08)"
    - "Disk-first cold start: load from bloomPath before any network fetch (D-04)"
    - "Keep-last-good on 304/503/transport/parse-error (D-09/D-10)"
    - "Cold-start blocking delegated entirely to BloomChecker.ready gate (D-06)"

key_files:
  created:
    - "pkg/bloomgate/fetcher.go"
    - "pkg/bloomgate/fetcher_test.go"
    - "cmd/bloom/main.go"
  modified: []

decisions:
  - "FetchOnce() and LoadDisk() exported for testability — allows httptest servers to drive individual fetch cycles without the ticker goroutine"
  - "doFetch() internal helper separates done/retry signals from the retry loop, keeping each 304/503/200/error branch explicit"
  - "Corrupt 200 returns done=true (no retry) — retrying the same corrupt body is unhelpful; next tick will retry"
  - "503 returns done=false (retry) — server may be loading; a few quick retries before the interval is correct"
  - "Persist failure (os.Rename error) logs but does NOT undo the in-memory swap — memory state is consistent; disk is best-effort"
  - "cmd/bloom copies runEventLoop/processLine/safeOutput verbatim from cmd/whitelist — deviation from 'import a shared pkg' was not taken; verbatim copy per plan spec maintains byte-identical scanner buffer sizing"

metrics:
  duration: "~3m30s"
  completed: "2026-06-30"
  task_count: 2
  file_count: 3
---

# Phase 03 Plan 02: BloomFetcher + cmd/bloom Entrypoint Summary

**One-liner:** Periodic conditional-GET BloomFetcher with parse-before-persist and atomic temp+rename disk write, wired into a new cmd/bloom StrFry plugin via the reused Handler/IOAdapter event loop.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| RED  | Failing tests for BloomFetcher | a78e085 | pkg/bloomgate/fetcher_test.go |
| 1    | BloomFetcher (GATE-03/04/05/06) | 8d4a951 | pkg/bloomgate/fetcher.go |
| 2    | cmd/bloom entrypoint (GATE-01/GATE-06) | 4fe8dca | cmd/bloom/main.go |

## Verification Results

```
go test ./pkg/bloomgate/ -run TestBloomFetcher -count=1  → PASS (8/8 tests)
go build ./cmd/bloom/                                    → OK
go vet ./pkg/bloomgate/ ./cmd/bloom/                     → clean
go build ./...                                           → OK (whole module compiles)
git status --porcelain cmd/whitelist cmd/router pkg/client pkg/whitelist pkg/handler pkg/bloom → empty
grep -rc 'LittleEndian' pkg/bloomgate/ cmd/bloom/        → 0 (D-02 honoured)
```

## Acceptance Criteria Verification

- `grep -c 'ReadFilter' pkg/bloomgate/fetcher.go` = 3 (parse-before-persist present)
- `grep -c 'os.Rename' pkg/bloomgate/fetcher.go` = 1 (atomic write present)
- `grep -c 'If-None-Match' pkg/bloomgate/fetcher.go` = 2 (conditional GET present)
- `grep -cE 'StatusNotModified|304' pkg/bloomgate/fetcher.go` = 5 (304 handling present)
- `grep -cE 'StatusServiceUnavailable|503' pkg/bloomgate/fetcher.go` = 5 (503 handling present)
- `grep -c 'LittleEndian' pkg/bloomgate/fetcher.go` = 0 (D-02 invariant respected)
- 304 test asserts bloomPath mtime/size unchanged (no disk write on 304)
- `grep -c 'NewWhitelistHandler' cmd/bloom/main.go` = 1
- `grep -c 'NewJSONLIOAdapter' cmd/bloom/main.go` = 1
- `grep -c 'LoadBloomConfig' cmd/bloom/main.go` = 1
- `grep -c 'bloomgate' cmd/bloom/main.go` = 3
- `grep -c 'pkg/client' cmd/bloom/main.go` = 0 (no per-event HTTP)
- `grep -c 'bloom-plugin' cmd/bloom/main.go` = 1 (correct logger prefix)

## Deviations from Plan

None — plan executed exactly as written.

- BloomFetcher struct fields mirror WhitelistRefresher exactly (checker, serverURL, bloomPath, interval, retryCount, httpClient, ctx, cancel, wg, logger).
- FetchOnce() and LoadDisk() are exported (for testability) as directed by the test requirements.
- doFetch() returns (done bool, retry bool) signals consumed by the retry loop in FetchOnce().
- persist() uses os.Create + Write + Close + os.Rename; removes tmpPath on any write/close error.
- cmd/bloom/main.go copies runEventLoop/processLine/safeOutput verbatim from cmd/whitelist/main.go per spec.

## Known Stubs

None — all plan behaviors are fully wired:
- BloomFetcher.Start() calls LoadDisk() then FetchOnce() then launches ticker goroutine.
- BloomChecker.ready gate integration: Store() is called on 200 parse success and on disk load success.
- Cold-start blocking fully delegated to the checker (D-06); cmd/bloom does not add any startup health check.

## Threat Surface Scan

No new trust boundaries introduced beyond those enumerated in the plan's threat model:
- T-03-04 mitigated: bloom.ReadFilter validates before any swap or disk write.
- T-03-05 mitigated: temp+rename (atomicWrite = persist()) leaves bloomPath never torn.
- T-03-06 mitigated: bloom.ReadFilter caps payloadLen at 1GiB before allocation.
- T-03-07 mitigated: bounded retries + keep-last-good; 503 is not fatal.
- T-03-08 mitigated: checker.ready gate withholds decisions until a filter exists.

## Self-Check: PASSED

Files created:
- FOUND: pkg/bloomgate/fetcher.go
- FOUND: pkg/bloomgate/fetcher_test.go
- FOUND: cmd/bloom/main.go

Commits exist:
- a78e085: test(03-02): add failing tests for BloomFetcher (GATE-03/04/05/06)
- 8d4a951: feat(03-02): create pkg/bloomgate BloomFetcher...
- 4fe8dca: feat(03-02): create cmd/bloom entrypoint wiring...

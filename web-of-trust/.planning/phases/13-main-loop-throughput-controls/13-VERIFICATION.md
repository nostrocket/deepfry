---
phase: 13-main-loop-throughput-controls
verified: 2026-06-18T14:10:29Z
status: passed
score: 12/12 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Phase 13: Main-Loop Throughput Controls Verification Report

**Phase Goal:** Main-loop throughput controls for crawler throughput optimization: independent Dgraph frontier sizing, count-query sampling, updated metrics, and relay filter safety preservation.
**Verified:** 2026-06-18T14:10:29Z
**Status:** passed
**Re-verification:** No - initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|---|---|---|
| 1 | LOOP-01: Operator can configure Dgraph frontier size independently from relay filter batch size. | VERIFIED | `Config.FrontierBatchSize` has `mapstructure:"frontier_batch_size"` and default/guard logic in `pkg/config/config.go:49`, `pkg/config/config.go:104`, `pkg/config/config.go:170`; tests cover default, explicit `500`, and guard fallback in `pkg/config/config_test.go:90`, `pkg/config/config_test.go:110`, `pkg/config/config_test.go:145`. |
| 2 | LOOP-02: Crawler can query a larger frontier while relay requests remain chunked by relay cap. | VERIFIED | Main loop selects with `cfg.FrontierBatchSize` in `cmd/crawler/main.go:299`; crawler relay config remains `FilterBatchSize: cfg.RelayFilterBatchSize` in `cmd/crawler/main.go:243`; relay chunks use `nextAuthorChunk(authors, batchCap)` in `pkg/crawler/crawler.go:1005`. |
| 3 | LOOP-03: Batch metrics report selected, queried, hits, skipped, and marked attempted using exact post-decoupling accounting. | VERIFIED | Main loop derives separate `selectedCount`, `queriedCount := result.Queried`, `hitCount`, `skippedAttempts`, and `markedAttempted` in `cmd/crawler/main.go:406`; these flow to `recordBatch` and `logBatchMetrics` in `cmd/crawler/main.go:441`. |
| 4 | LOOP-04: Larger frontier does not reintroduce oversized relay filter failure mode. | VERIFIED | `TestFrontierBatchLargerThanRelayCapSplitsRelayChunks` constructs 250 authors with cap 100 and verifies `100/100/50` chunks via production helper `nextAuthorChunk` in `pkg/crawler/crawler_filter_test.go:72`; `queryRelay` uses the same helper in `pkg/crawler/crawler.go:1005`. |
| 5 | COUNT-01: Operator can throttle count-query frequency without disabling crawl progress. | VERIFIED | `CountSampleInterval` config exists and is guarded in `pkg/config/config.go:50`, `pkg/config/config.go:105`, `pkg/config/config.go:177`; main loop only runs batch count calls inside `countSamples.due(nextBatchNum)` at `cmd/crawler/main.go:313`; cached path continues at `cmd/crawler/main.go:346`. Startup/end run-record counts are outside the per-batch loop. |
| 6 | COUNT-02: Logs and final run records remain accurate when count queries are skipped. | VERIFIED | Cached snapshots carry `countsCached`, age, cached totals in `cmd/crawler/main.go:164`; `applyMarked` adjusts cached stale estimates in `cmd/crawler/main.go:178`; BATCH_METRICS and run records expose sampled/cached fields in `cmd/crawler/metrics.go:101` and `cmd/crawler/metrics.go:144`. |
| 7 | COUNT-03: Sampled count failures remain recoverable through retryDgraph. | VERIFIED | Sampled `CountPubkeys` and `CountStalePubkeys` calls are wrapped by `retryDgraph` in `cmd/crawler/main.go:315` and `cmd/crawler/main.go:331`; retry behavior has existing focused coverage in `cmd/crawler/main_test.go:126`. |
| 8 | MEASURE-01: Rounds can be compared using BATCH_METRICS and `~/deepfry/crawler-metrics.jsonl`. | VERIFIED | `logBatchMetrics` emits always-on `BATCH_METRICS` JSON in `cmd/crawler/metrics.go:89`; `writeRunRecord` appends JSONL under `~/deepfry` in `cmd/crawler/metrics.go:220`; operator procedure compares both in `13-01-PLAN.md:242`. |
| 9 | MEASURE-02: Run records include new frontier and count-sampling settings. | VERIFIED | `runRecord` has `frontier_batch_size`, `relay_filter_batch_size`, and `count_sample_interval` JSON fields in `cmd/crawler/metrics.go:160`; `buildRunRecord` populates them in `cmd/crawler/metrics.go:211`. |
| 10 | MEASURE-03: Milestone defines operator baseline/optimized verification procedure. | VERIFIED | PLAN verification section instructs baseline and optimized `WOT_ROUND` runs, `frontier_batch_size: 500`, `count_sample_interval: 5`, JSONL comparison, and StrFry/Dgraph safety reminders in `13-01-PLAN.md:242`. |
| 11 | TEST-01: Unit tests cover config defaults/guards without real `~/deepfry`. | VERIFIED | Config tests use `t.Setenv("HOME", t.TempDir())` before config writes/loads in `pkg/config/config_test.go:90`, `pkg/config/config_test.go:110`, and `pkg/config/config_test.go:145`; `go test ./pkg/config -count=1` passed. |
| 12 | TEST-02: Unit tests cover loop accounting for larger selected batches, skipped attempts, and throttled count queries. | VERIFIED | Count schedule and cached stale adjustment tests are in `cmd/crawler/main_test.go:63` and `cmd/crawler/main_test.go:92`; exact queried invalid-pubkey behavior is tested in `pkg/crawler/crawler_filter_test.go:101`; sampled/cached metrics JSON is tested in `cmd/crawler/metrics_test.go:121`. |

**Score:** 12/12 truths verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|---|---|---|---|
| `pkg/config/config.go` | Throughput config fields, defaults, guards | VERIFIED | Exists and substantive; `FrontierBatchSize`/`CountSampleInterval` are mapped and guarded. |
| `cmd/crawler/main.go` | Frontier selection, count sampling, exact accounting | VERIFIED | Exists, substantive, wired through the crawler main loop. |
| `pkg/crawler/crawler.go` | `FetchResult.Queried` exact validated author count | VERIFIED | Exists, substantive; `Queried` is set from `len(authors)` after validation and before relay filter construction. |
| `cmd/crawler/metrics.go` | BATCH_METRICS and run-record sampled/cached context | VERIFIED | Exists, substantive; JSON fields and cumulative counters are wired. |
| `pkg/crawler/crawler_filter_test.go` | Relay chunk safety and queried-count regression tests | VERIFIED | Exists, substantive; tests exercise production helper and `FetchAndUpdateFollows`. |

### Key Link Verification

| From | To | Via | Status | Details |
|---|---|---|---|---|
| `cmd/crawler/main.go` | `pkg/dgraph/dgraph.go:GetStalePubkeys` | Frontier limit | WIRED | Manual check: `GetStalePubkeys(..., cfg.FrontierBatchSize)` at `cmd/crawler/main.go:299`. |
| `cmd/crawler/main.go` | `pkg/crawler/crawler.go:queryRelay` | Relay filter size | WIRED | Manual check: `FilterBatchSize: cfg.RelayFilterBatchSize` at `cmd/crawler/main.go:243`; production relay chunks use `nextAuthorChunk` at `pkg/crawler/crawler.go:1005`. |
| `cmd/crawler/main.go` | `cmd/crawler/metrics.go` | Metrics/run records | WIRED | Manual check: exact counters flow to `recordBatch` and `logBatchMetrics` at `cmd/crawler/main.go:441`; final record built at `cmd/crawler/main.go:475`. |

Note: `gsd-tools query verify.key-links` was attempted, but the PLAN regex patterns were over-escaped for that tool and returned invalid-pattern results. Manual line-level checks above are the verification evidence.

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|---|---|---|---|---|
| `cmd/crawler/main.go` | `pubkeys` | `dgraphClient.GetStalePubkeys(..., cfg.FrontierBatchSize)` | Yes | FLOWING |
| `cmd/crawler/main.go` | `countSnapshot` | Sampled `CountPubkeys`/`CountStalePubkeys` via `retryDgraph`, or cached `countSampleState` | Yes | FLOWING |
| `pkg/crawler/crawler.go` | `FetchResult.Queried` | Validated `authors` slice after `dgraph.ValidatePubkey` | Yes | FLOWING |
| `cmd/crawler/metrics.go` | `batchMetrics`/`runRecord` | Main-loop exact counters and config snapshot | Yes | FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|---|---|---|---|
| Config defaults/guards and temp-HOME isolation | `go test ./pkg/config -count=1` | `ok web-of-trust/pkg/config 0.360s` | PASS |
| Main-loop helper, retry, metrics/run-record tests | `go test ./cmd/crawler -count=1` | `ok web-of-trust/cmd/crawler 0.436s` | PASS |
| Crawler queried count and relay chunk safety tests | `go test ./pkg/crawler -count=1` | `ok web-of-trust/pkg/crawler 0.972s` | PASS |
| Full short suite with coverage | `go test ./... -short -cover` | Passed; command reported coverage for all packages, with already-run packages using Go test cache in this verification run. | PASS |

### Probe Execution

| Probe | Command | Result | Status |
|---|---|---|---|
| Conventional probes | `find scripts -path '*/tests/probe-*.sh' -type f` | No probes found; PLAN/SUMMARY do not declare probes. | SKIP |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|---|---|---|---|---|
| LOOP-01 | 13-01 | Independent Dgraph frontier batch size | SATISFIED | Config fields/tests plus main-loop use of `cfg.FrontierBatchSize`. |
| LOOP-02 | 13-01 | Larger frontier with relay chunking | SATISFIED | Frontier selection decoupled from `FilterBatchSize`; chunk helper test verifies safe split. |
| LOOP-03 | 13-01 | Exact selected/queried/hit/skipped/marked metrics | SATISFIED | Separate counters and metrics/run-record fields wired. |
| LOOP-04 | 13-01 | No oversized-filter regression | SATISFIED | Production chunk helper and regression test. |
| COUNT-01 | 13-01 | Throttle count query frequency | SATISFIED | `countSampleState` and `cfg.CountSampleInterval` control per-batch count calls. |
| COUNT-02 | 13-01 | Accurate skipped-count logs/run records | SATISFIED | Cached flags, age, totals, `new_pubkeys:null`, and run totals tested. |
| COUNT-03 | 13-01 | Sampled count failures use retry path | SATISFIED | Sampled counts call through `retryDgraph`; retry tests pass. |
| MEASURE-01 | 13-01 | Compare rounds with BATCH_METRICS and JSONL | SATISFIED | Always-on batch JSON and appended run JSONL. |
| MEASURE-02 | 13-01 | Run records include new settings | SATISFIED | Run record JSON fields populated from config. |
| MEASURE-03 | 13-01 | Operator verification procedure | SATISFIED | Procedure exists in PLAN verification section with baseline/optimized labels. |
| TEST-01 | 13-01 | Config tests avoid real home | SATISFIED | Tests use temp HOME. |
| TEST-02 | 13-01 | Loop accounting tests | SATISFIED | Count sampling, cached stale, exact queried, and metrics JSON tests pass. |

No Phase 13 orphaned requirements found. `DWRITE-01/02/03` and `TEST-03` are explicitly mapped to Phase 14 in `.planning/REQUIREMENTS.md`.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|---|---:|---|---|---|
| Modified files | - | Debt/stub markers | INFO | `rg` found no `TBD`, `FIXME`, `XXX`, `TODO`, `HACK`, `PLACEHOLDER`, placeholder text, empty implementations, or console-log-only implementations in the Phase 13 modified files. |

### Review Finding Resolution

| Finding | Status | Evidence |
|---|---|---|
| CR-01 normal completion can hang waiting for signal goroutine | RESOLVED | Signal goroutine exits on `ctx.Done()` in `cmd/crawler/main.go:198`; shutdown calls `cancel()`, `signal.Stop`, then `wg.Wait()` in `cmd/crawler/main.go:479`. |
| WR-01 cached `stale_remaining` ignores prior cached batches | RESOLVED | `countSampleState.applyMarked` decrements cached stale state in `cmd/crawler/main.go:178`; called after metrics in `cmd/crawler/main.go:463`; test at `cmd/crawler/main_test.go:92`. |
| WR-02 relay chunk test reimplemented production logic | RESOLVED | Test uses production `nextAuthorChunk` at `pkg/crawler/crawler_filter_test.go:85`; `queryRelay` uses the same helper at `pkg/crawler/crawler.go:1005`. |

### Human Verification Required

None.

### Gaps Summary

No blocking gaps found. Phase 13 goal is achieved in code: frontier sizing is independent from relay chunk sizing, count-query sampling is implemented and observable, metrics/run records are self-describing, `FetchResult.Queried` is exact validated-author count, relay chunk safety is preserved, and review findings are resolved.

---

_Verified: 2026-06-18T14:10:29Z_
_Verifier: the agent (gsd-verifier)_

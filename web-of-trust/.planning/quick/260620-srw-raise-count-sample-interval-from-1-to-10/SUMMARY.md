---
quick_id: 260620-srw
slug: raise-count-sample-interval-from-1-to-10
date: 2026-06-20
status: complete
commit: 52a3fe3
---

# Summary: Raise count_sample_interval 1 → 100

## Outcome

Fixed the crawler throughput bottleneck. The two count queries
(`CountStalePubkeys` ~44s, `CountPubkeys` ~14s) were running on every batch to
populate informational log fields, making `overhead_ms` (~58s) dominate
`batch_ms` (~70s) while actual fetch work was only ~5s — capping throughput at
~1.3 pubkeys/sec.

Root cause: the amortization machinery already existed
(`countSampleState.due/cached/recordSample/applyMarked` in `cmd/crawler/main.go`)
but was disabled by `count_sample_interval` defaulting to `1`.

## Changes

- `pkg/config/config.go` — viper default `count_sample_interval` 1 → 100.
  `<=0` invalid-input guard fallback unchanged (stays 1).
- `pkg/config/config_test.go` — default-value assertion 1 → 100.
- `~/deepfry/web-of-trust.yaml` (live, out of tree) — `count_sample_interval: 100`.

## Verification

- `go build ./...` — clean.
- `go test ./pkg/config/ ./cmd/crawler/ -short` — pass.
- Pending live verification: restart crawler on the strfry host; confirm
  `batch_ms` drops to ~6–10s and `counts_cached:true` appears on non-sampling
  batches. Crawler reads the live yaml on next restart.

## Expected impact

Count cost amortizes to ~0.6s/batch; batch ~70s → ~6–10s, ≈10x throughput.
Cached stale count stays accurate between samples via `applyMarked()`.
No change to crawl correctness.

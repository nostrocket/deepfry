---
quick_id: 260620-srw
slug: raise-count-sample-interval-from-1-to-10
date: 2026-06-20
---

# Quick Task: Raise count_sample_interval 1 → 100

## Problem

Production crawler throughput capped at ~1.3 pubkeys/sec. Batch metrics show
`batch_ms` ~57–85s while `fetch_ms` (the real work) is only ~3–8s. The
`overhead_ms` (~54–79s) is dominated by two count queries that run on **every**
batch purely to populate informational log fields (`stale_remaining`,
`total_pubkeys`):

- `CountStalePubkeys` ~44s avg
- `CountPubkeys` ~14s avg

The sampling machinery to amortize these already exists in
`cmd/crawler/main.go` (`countSampleState.due()/cached()/recordSample()/applyMarked()`),
gated by `count_sample_interval`. It was effectively disabled because the value
defaulted to `1` (sample every batch). `applyMarked()` keeps the cached stale
count accurate between samples by decrementing per marked batch.

## Change

1. `pkg/config/config.go` — bump viper default `count_sample_interval` 1 → 100.
   The `<=0` invalid-input guard fallback stays at `1` (safe slow behavior).
2. `pkg/config/config_test.go` — update default-value assertion 1 → 100.
3. `~/deepfry/web-of-trust.yaml` (live config) — `count_sample_interval: 1 → 100`
   so the running crawler picks it up on next restart.

## Expected impact

Count cost amortizes to ~0.6s/batch (58s / 100). Batch ~70s → ~6–10s,
≈10x throughput. Exact counts still logged every ~100 batches; cached counts
stay accurate via `applyMarked()` in between. No change to crawl correctness.

## Verification

- `go build ./...` clean
- `go test ./pkg/config/ ./cmd/crawler/ -short` pass
- Live verification: restart crawler, confirm `batch_ms` drops and
  `counts_cached:true` appears on non-sampling batches.

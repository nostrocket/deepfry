package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/version"
)

// metricsFileName is the append-only JSONL sink for per-run crawler speed
// records, written under ~/deepfry/ alongside the other config files. Append
// mode only — existing records are never overwritten (CLAUDE.md: never delete
// or overwrite files in ~/deepfry/).
const metricsFileName = "crawler-metrics.jsonl"

// resolveRoundID returns the identifier used to compare optimization rounds.
// Precedence: the WOT_ROUND env var (explicit named experiment) wins; otherwise
// the git commit injected at build time via -ldflags. Falls back to "unknown"
// for a `go run` build with neither set.
func resolveRoundID() string {
	if label := os.Getenv("WOT_ROUND"); label != "" {
		return label
	}
	return version.Commit
}

// runStats accumulates cumulative counters across a single crawler run. The
// main loop is single-threaded so no synchronization is needed.
type runStats struct {
	batches              int
	totalSelected        int
	totalQueried         int
	totalHits            int
	totalSkippedAttempts int
	totalMarkedAttempted int
	countSamples         int
	countCachedBatches   int
}

// recordBatch folds one completed batch into the cumulative run totals.
func (s *runStats) recordBatch(selected, queried, hits, skippedAttempts, markedAttempted int, countsSampled, countsCached bool) {
	s.batches++
	s.totalSelected += selected
	s.totalQueried += queried
	s.totalHits += hits
	s.totalSkippedAttempts += skippedAttempts
	s.totalMarkedAttempted += markedAttempted
	if countsSampled {
		s.countSamples++
	}
	if countsCached {
		s.countCachedBatches++
	}
}

type batchMetrics struct {
	roundID               string
	batchNum              int
	frontierBatchSize     int
	relayFilterBatchSize  int
	countSampleInterval   int
	countsSampled         bool
	countsCached          bool
	countSampleAgeBatches int
	selected              int
	queried               int
	hits                  int
	skippedAttempts       int
	markedAttempted       int
	staleRemaining        int
	totalPubkeys          int
	newPubkeys            *int
	batchDur              time.Duration
	fetchDur              time.Duration
	overheadDur           time.Duration
}

// logBatchMetrics emits one structured BATCH_METRICS JSON line per batch. This
// is always-on (not debug-gated): measuring real production speed is the whole
// point. fetchDur is the relay-fetch (FetchAndUpdateFollows) wall time;
// overheadDur is everything else in the iteration (Dgraph reads/writes +
// bookkeeping) — together they give the relay-vs-overhead split that drives
// optimization decisions.
func logBatchMetrics(m batchMetrics) {
	hitRate := 0.0
	if m.queried > 0 {
		hitRate = float64(m.hits) / float64(m.queried)
	}
	pps := 0.0
	if m.batchDur > 0 {
		pps = float64(m.queried) / m.batchDur.Seconds()
	}
	rec := map[string]interface{}{
		"round_id":                 m.roundID,
		"batch":                    m.batchNum,
		"frontier_batch_size":      m.frontierBatchSize,
		"relay_filter_batch_size":  m.relayFilterBatchSize,
		"count_sample_interval":    m.countSampleInterval,
		"counts_sampled":           m.countsSampled,
		"counts_cached":            m.countsCached,
		"count_sample_age_batches": m.countSampleAgeBatches,
		"selected":                 m.selected,
		"queried":                  m.queried,
		"hits":                     m.hits,
		"skipped_attempts":         m.skippedAttempts,
		"marked_attempted":         m.markedAttempted,
		"stale_remaining":          m.staleRemaining,
		"total_pubkeys":            m.totalPubkeys,
		"hit_rate":                 round3(hitRate),
		"new_pubkeys":              m.newPubkeys,
		"batch_ms":                 m.batchDur.Milliseconds(),
		"fetch_ms":                 m.fetchDur.Milliseconds(),
		"overhead_ms":              m.overheadDur.Milliseconds(),
		"pubkeys_per_sec":          round3(pps),
		"component":                "web-of-trust-crawler",
		"metric_type":              "batch_speed",
	}
	b, _ := json.Marshal(rec)
	log.Printf("BATCH_METRICS: %s", b)
}

// runRecord is the single comparable record appended to the JSONL sink at the
// end of a run. It carries enough config context (timeout, batch size, quorum,
// relay count) that each round's numbers are self-describing when diffed.
type runRecord struct {
	RoundID              string  `json:"round_id"`
	Commit               string  `json:"commit"`
	Version              string  `json:"version"`
	Label                string  `json:"label,omitempty"`
	StartedAt            string  `json:"started_at"`
	EndedAt              string  `json:"ended_at"`
	RuntimeSec           float64 `json:"runtime_sec"`
	Batches              int     `json:"batches"`
	TotalSelected        int     `json:"total_selected"`
	TotalQueried         int     `json:"total_queried"`
	TotalHits            int     `json:"total_hits"`
	TotalSkippedAttempts int     `json:"total_skipped_attempts"`
	TotalMarkedAttempted int     `json:"total_marked_attempted"`
	CountSamples         int     `json:"count_samples"`
	CountCachedBatches   int     `json:"count_cached_batches"`
	HitRate              float64 `json:"hit_rate"`
	PubkeysStart         int     `json:"pubkeys_start"`
	PubkeysEnd           int     `json:"pubkeys_end"`
	NetNewPubkeys        int     `json:"net_new_pubkeys"`
	PubkeysPerSec        float64 `json:"pubkeys_per_sec"`
	NewPubkeysPerSec     float64 `json:"new_pubkeys_per_sec"`
	AvgBatchMs           int64   `json:"avg_batch_ms"`
	AvgFetchMs           int64   `json:"avg_fetch_ms"`
	AvgGetStaleMs        int64   `json:"avg_getstale_ms"`
	AvgCountPubMs        int64   `json:"avg_countpubkeys_ms"`
	AvgCountStaleMs      int64   `json:"avg_countstale_ms"`
	AvgMarkAttMs         int64   `json:"avg_markattempted_ms"`
	TimeoutSec           float64 `json:"timeout_sec"`
	BatchSize            int     `json:"batch_size"`
	FrontierBatchSize    int     `json:"frontier_batch_size"`
	RelayFilterBatchSize int     `json:"relay_filter_batch_size"`
	CountSampleInterval  int     `json:"count_sample_interval"`
	Quorum               float64 `json:"quorum"`
	Relays               int     `json:"relays"`
}

// buildRunRecord assembles the comparable per-run record from the run's
// cumulative state, the per-call timing accumulator, and the active config.
// Rates are normalized per-second so runs of different lengths compare directly.
func buildRunRecord(roundID string, start, end time.Time, pubkeysStart, pubkeysEnd int, stats *runStats, metrics *callMetrics, cfg *config.Config) runRecord {
	runtimeSec := end.Sub(start).Seconds()
	netNew := pubkeysEnd - pubkeysStart
	hitRate := 0.0
	if stats.totalQueried > 0 {
		hitRate = float64(stats.totalHits) / float64(stats.totalQueried)
	}
	pps, npps := 0.0, 0.0
	if runtimeSec > 0 {
		pps = float64(stats.totalQueried) / runtimeSec
		npps = float64(netNew) / runtimeSec
	}
	return runRecord{
		RoundID:              roundID,
		Commit:               version.Commit,
		Version:              version.Version,
		Label:                os.Getenv("WOT_ROUND"),
		StartedAt:            start.UTC().Format(time.RFC3339),
		EndedAt:              end.UTC().Format(time.RFC3339),
		RuntimeSec:           round3(runtimeSec),
		Batches:              stats.batches,
		TotalSelected:        stats.totalSelected,
		TotalQueried:         stats.totalQueried,
		TotalHits:            stats.totalHits,
		TotalSkippedAttempts: stats.totalSkippedAttempts,
		TotalMarkedAttempted: stats.totalMarkedAttempted,
		CountSamples:         stats.countSamples,
		CountCachedBatches:   stats.countCachedBatches,
		HitRate:              round3(hitRate),
		PubkeysStart:         pubkeysStart,
		PubkeysEnd:           pubkeysEnd,
		NetNewPubkeys:        netNew,
		PubkeysPerSec:        round3(pps),
		NewPubkeysPerSec:     round3(npps),
		AvgBatchMs:           metrics.avg("Batch").Milliseconds(),
		AvgFetchMs:           metrics.avg("FetchAndUpdateFollows").Milliseconds(),
		AvgGetStaleMs:        metrics.avg("GetStalePubkeys").Milliseconds(),
		AvgCountPubMs:        metrics.avg("CountPubkeys").Milliseconds(),
		AvgCountStaleMs:      metrics.avg("CountStalePubkeys").Milliseconds(),
		AvgMarkAttMs:         metrics.avg("MarkAttempted").Milliseconds(),
		TimeoutSec:           round3(cfg.Timeout.Seconds()),
		BatchSize:            cfg.RelayFilterBatchSize,
		FrontierBatchSize:    cfg.FrontierBatchSize,
		RelayFilterBatchSize: cfg.RelayFilterBatchSize,
		CountSampleInterval:  cfg.CountSampleInterval,
		Quorum:               cfg.RelayEOSEQuorum,
		Relays:               len(cfg.RelayURLs),
	}
}

// writeRunRecord appends one runRecord to ~/deepfry/crawler-metrics.jsonl.
// Best-effort: a failure is logged as a warning and never aborts shutdown.
func writeRunRecord(rec runRecord) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("WARN: could not resolve home dir for metrics file: %v", err)
		return
	}
	path := filepath.Join(home, "deepfry", metricsFileName)
	line, err := json.Marshal(rec)
	if err != nil {
		log.Printf("WARN: could not marshal run metrics: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("WARN: could not open metrics file %s: %v", path, err)
		return
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		log.Printf("WARN: could not append run metrics to %s: %v", path, err)
		return
	}
	log.Printf("Run metrics appended to %s (round_id=%s)", path, rec.RoundID)
}

// round3 rounds a float to 3 decimal places for compact, comparable JSON.
func round3(f float64) float64 {
	return float64(int64(f*1000+0.5)) / 1000
}

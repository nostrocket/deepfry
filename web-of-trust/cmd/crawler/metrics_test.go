package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"web-of-trust/pkg/config"
)

func TestResolveRoundID_LabelOverridesCommit(t *testing.T) {
	t.Setenv("WOT_ROUND", "baseline-256")
	if got := resolveRoundID(); got != "baseline-256" {
		t.Fatalf("WOT_ROUND should win: got %q, want %q", got, "baseline-256")
	}
}

func TestResolveRoundID_FallsBackToCommit(t *testing.T) {
	t.Setenv("WOT_ROUND", "")
	// version.Commit defaults to "unknown" under `go test` (no ldflags).
	if got := resolveRoundID(); got == "" {
		t.Fatal("round id must never be empty")
	}
}

func TestBuildRunRecord_RatesAndDeltas(t *testing.T) {
	t.Setenv("WOT_ROUND", "")
	start := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	end := start.Add(100 * time.Second)

	stats := &runStats{}
	stats.recordBatch(120, 100, 40, 5, 115, true, false)
	stats.recordBatch(130, 100, 60, 0, 130, false, true)

	cfg := &config.Config{
		Timeout:              15 * time.Second,
		RelayFilterBatchSize: 100,
		FrontierBatchSize:    500,
		CountSampleInterval:  5,
		RelayEOSEQuorum:      0.70,
		RelayURLs:            []string{"wss://a", "wss://b", "wss://c"},
	}

	rec := buildRunRecord("r1", start, end, 1000, 1500, stats, newCallMetrics(), cfg)

	if rec.Batches != 2 || rec.TotalSelected != 250 || rec.TotalQueried != 200 || rec.TotalHits != 100 {
		t.Fatalf("counters wrong: %+v", rec)
	}
	if rec.TotalSkippedAttempts != 5 || rec.TotalMarkedAttempted != 245 {
		t.Fatalf("attempt counters wrong: %+v", rec)
	}
	if rec.CountSamples != 1 || rec.CountCachedBatches != 1 {
		t.Fatalf("count sample counters wrong: %+v", rec)
	}
	if rec.HitRate != 0.5 {
		t.Errorf("hit_rate: got %v want 0.5", rec.HitRate)
	}
	if rec.NetNewPubkeys != 500 {
		t.Errorf("net_new: got %v want 500", rec.NetNewPubkeys)
	}
	// 200 queried / 100s = 2.0/s ; 500 new / 100s = 5.0/s
	if rec.PubkeysPerSec != 2.0 {
		t.Errorf("pubkeys_per_sec: got %v want 2.0", rec.PubkeysPerSec)
	}
	if rec.NewPubkeysPerSec != 5.0 {
		t.Errorf("new_pubkeys_per_sec: got %v want 5.0", rec.NewPubkeysPerSec)
	}
	if rec.BatchSize != 100 || rec.FrontierBatchSize != 500 || rec.RelayFilterBatchSize != 100 ||
		rec.CountSampleInterval != 5 || rec.Quorum != 0.70 || rec.Relays != 3 || rec.TimeoutSec != 15 {
		t.Errorf("config snapshot wrong: %+v", rec)
	}
}

func TestBuildRunRecord_ZeroRuntimeNoPanic(t *testing.T) {
	t.Setenv("WOT_ROUND", "")
	now := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	rec := buildRunRecord("r", now, now, 0, 0, &runStats{}, newCallMetrics(), &config.Config{})
	if rec.PubkeysPerSec != 0 || rec.NewPubkeysPerSec != 0 || rec.HitRate != 0 {
		t.Fatalf("zero-runtime/zero-work rates must be 0: %+v", rec)
	}
}

func TestWriteRunRecord_AppendsJSONL(t *testing.T) {
	// Redirect HOME to a temp dir so we never touch the real ~/deepfry.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "deepfry"), 0755); err != nil {
		t.Fatal(err)
	}

	writeRunRecord(runRecord{RoundID: "r1", TotalQueried: 10})
	writeRunRecord(runRecord{RoundID: "r2", TotalQueried: 20})

	path := filepath.Join(tmp, "deepfry", metricsFileName)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("metrics file not created: %v", err)
	}
	defer f.Close()

	var ids []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec runRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("each line must be valid JSON: %v", err)
		}
		ids = append(ids, rec.RoundID)
	}
	if len(ids) != 2 || ids[0] != "r1" || ids[1] != "r2" {
		t.Fatalf("expected appended [r1 r2], got %v", ids)
	}
}

func TestLogBatchMetrics_SampledAndCachedJSON(t *testing.T) {
	var buf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	newPubkeys := 7
	logBatchMetrics(batchMetrics{
		roundID:               "r1",
		batchNum:              1,
		frontierBatchSize:     500,
		relayFilterBatchSize:  100,
		countSampleInterval:   5,
		countsSampled:         true,
		countsCached:          false,
		countSampleAgeBatches: 0,
		selected:              120,
		queried:               100,
		hits:                  40,
		skippedAttempts:       5,
		markedAttempted:       115,
		staleRemaining:        885,
		totalPubkeys:          1007,
		newPubkeys:            &newPubkeys,
		batchDur:              10 * time.Second,
		fetchDur:              4 * time.Second,
		overheadDur:           6 * time.Second,
	})

	line := strings.TrimPrefix(strings.TrimSpace(buf.String()), "BATCH_METRICS: ")
	var sampled map[string]interface{}
	if err := json.Unmarshal([]byte(line), &sampled); err != nil {
		t.Fatalf("sampled metrics must be JSON: %v\n%s", err, line)
	}
	if sampled["frontier_batch_size"].(float64) != 500 || sampled["relay_filter_batch_size"].(float64) != 100 {
		t.Fatalf("batch size fields missing/wrong: %v", sampled)
	}
	if sampled["counts_sampled"] != true || sampled["counts_cached"] != false || sampled["new_pubkeys"].(float64) != 7 {
		t.Fatalf("sampled count fields wrong: %v", sampled)
	}
	if sampled["selected"].(float64) != 120 || sampled["queried"].(float64) != 100 ||
		sampled["skipped_attempts"].(float64) != 5 || sampled["marked_attempted"].(float64) != 115 {
		t.Fatalf("exact accounting fields wrong: %v", sampled)
	}

	buf.Reset()
	logBatchMetrics(batchMetrics{
		roundID:               "r1",
		batchNum:              2,
		frontierBatchSize:     500,
		relayFilterBatchSize:  100,
		countSampleInterval:   5,
		countsSampled:         false,
		countsCached:          true,
		countSampleAgeBatches: 1,
		selected:              100,
		queried:               100,
		hits:                  30,
		markedAttempted:       100,
		staleRemaining:        785,
		totalPubkeys:          1007,
		newPubkeys:            nil,
		batchDur:              10 * time.Second,
		fetchDur:              4 * time.Second,
		overheadDur:           6 * time.Second,
	})

	line = strings.TrimPrefix(strings.TrimSpace(buf.String()), "BATCH_METRICS: ")
	var cached map[string]interface{}
	if err := json.Unmarshal([]byte(line), &cached); err != nil {
		t.Fatalf("cached metrics must be JSON: %v\n%s", err, line)
	}
	if cached["counts_sampled"] != false || cached["counts_cached"] != true ||
		cached["count_sample_age_batches"].(float64) != 1 {
		t.Fatalf("cached count fields wrong: %v", cached)
	}
	if cached["new_pubkeys"] != nil {
		t.Fatalf("cached metrics must encode new_pubkeys as null, got %v", cached["new_pubkeys"])
	}
}

func TestRound3(t *testing.T) {
	cases := map[float64]float64{
		2.0:      2.0,
		0.428571: 0.429,
		3.14159:  3.142,
	}
	for in, want := range cases {
		if got := round3(in); got != want {
			t.Errorf("round3(%v): got %v want %v", in, got, want)
		}
	}
}

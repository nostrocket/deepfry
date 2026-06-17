package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
	stats.recordBatch(100, 40) // queried 100, 40 hits
	stats.recordBatch(100, 60) // queried 100, 60 hits

	cfg := &config.Config{
		Timeout:              15 * time.Second,
		RelayFilterBatchSize: 100,
		RelayEOSEQuorum:      0.70,
		RelayURLs:            []string{"wss://a", "wss://b", "wss://c"},
	}

	rec := buildRunRecord("r1", start, end, 1000, 1500, stats, newCallMetrics(), cfg)

	if rec.Batches != 2 || rec.TotalQueried != 200 || rec.TotalHits != 100 {
		t.Fatalf("counters wrong: %+v", rec)
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
	if rec.BatchSize != 100 || rec.Quorum != 0.70 || rec.Relays != 3 || rec.TimeoutSec != 15 {
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

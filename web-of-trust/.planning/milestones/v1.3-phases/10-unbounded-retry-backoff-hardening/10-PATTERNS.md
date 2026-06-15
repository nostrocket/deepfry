# Phase 10: Unbounded Retry & Backoff Hardening - Pattern Map

**Mapped:** 2026-06-15
**Files analyzed:** 2 (1 modified, 1 new)
**Analogs found:** 2 / 2 (test analog is partial — see notes)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `cmd/crawler/main.go` (modify) | entry-point / main loop | request-response (gRPC calls w/ retry) | self (lines 147-314 collapse to one helper) + `pkg/crawler/crawler.go` backoff consts | exact (in-file refactor) |
| `cmd/crawler/main_test.go` (new) | test | request-response (deterministic via injected clock) | `pkg/dgraph/backoff_test.go` (table-driven backoff sequence) | role-match / partial |

## Pattern Assignments

### `cmd/crawler/main.go` (entry-point, request-response) — MODIFY

This is an in-file refactor: the four near-identical retry blocks become one generic
`retryDgraph[T any]` helper. The "analog" the new helper generalizes is the existing
block itself. All concrete excerpts below are the current code under change.

**Imports pattern** (`cmd/crawler/main.go:3-21`) — unchanged; `time`, `log`, `context`,
`google.golang.org/grpc/codes`, `google.golang.org/grpc/status` already present, no new imports
needed for the helper (generics need no import):
```go
import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/crawler"
	"web-of-trust/pkg/dgraph"
)
```

**Transient classifier — reuse verbatim** (`cmd/crawler/main.go:37-51`). The helper
calls this; do NOT reimplement. Keep as-is:
```go
func isDgraphTransient(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}
```

**Backoff constants to change** (`cmd/crawler/main.go:25-29`). `dgraphRetryAttempts` is
removed (D-04); initial 5s→1m, max 2m→5m:
```go
// CURRENT (to replace):
const (
	dgraphRetryInitial  = 5 * time.Second
	dgraphRetryMax      = 2 * time.Minute
	dgraphRetryAttempts = 5            // <- REMOVE (indefinite retry, D-04)
)
// TARGET:
const (
	dgraphRetryInitial = 1 * time.Minute  // D-04
	dgraphRetryMax     = 5 * time.Minute  // D-04, aligns with relay maxBackoff
)
```

**Core pattern to generalize — the shutdown-safe retry block** (`cmd/crawler/main.go:147-176`,
representative of all four). Note the `select { case <-time.After: case <-ctx.Done() }`
shutdown-safe wait at lines 162-166, the transient/fatal branch, and the doubling+cap at
167-170. The helper (D-01..D-04) collapses this:
```go
// Get stale pubkeys to process (RESIL-01: retry on transient gRPC errors).
var pubkeys map[string]int64
{
	var retryDelay = dgraphRetryInitial
	for attempt := 0; attempt < dgraphRetryAttempts; attempt++ {
		pubkeys, err = dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, cfg.RelayFilterBatchSize)
		if err == nil {
			break
		}
		if !isDgraphTransient(err) {
			log.Printf("Fatal Dgraph error getting stale pubkeys: %v", err)
			break mainLoop
		}
		log.Printf("Transient Dgraph error getting stale pubkeys (attempt %d/%d): %v; retrying in %v",
			attempt+1, dgraphRetryAttempts, err, retryDelay)
		select {
		case <-time.After(retryDelay):      // <- production sleep; make injectable (D-03)
		case <-ctx.Done():                  // <- SHUTDOWN-01: interrupt mid-backoff
			break mainLoop
		}
		retryDelay *= 2                      // <- BACKOFF doubling (D-04)
		if retryDelay > dgraphRetryMax {     // <- cap at 5m
			retryDelay = dgraphRetryMax
		}
	}
	if err != nil {
		log.Printf("Dgraph unavailable after %d attempts getting stale pubkeys, exiting: %v", dgraphRetryAttempts, err)
		break mainLoop
	}
}
```

**Target helper shape** (D-01/D-02/D-03/D-08). Generic over return type `T`, injectable
`sleepFn func(time.Duration) <-chan time.Time` (prod = `time.After`), threads a metrics
accumulator keyed by `callName`. Inner loop has no attempt cap — transient retries forever;
fatal/ctx-cancel returns the error for the caller to handle:
```go
func retryDgraph[T any](
	ctx context.Context,
	callName string,
	fn func() (T, error),
	metrics *callMetrics,                      // D-08: timing/avg, keyed by callName
	sleepFn func(time.Duration) <-chan time.Time, // D-03: time.After in prod, fake in tests
) (T, error) {
	var zero T
	delay := dgraphRetryInitial
	for {
		start := time.Now()
		v, err := fn()
		if err == nil {
			metrics.record(callName, time.Since(start)) // D-07: success-only duration
			return v, nil
		}
		if !isDgraphTransient(err) {
			return zero, err                  // D-02: caller decides (break vs warn+continue)
		}
		log.Printf("Transient Dgraph error %s: %v; retrying in %v", callName, err, delay) // keep "retrying in %v" — SC#2
		select {
		case <-sleepFn(delay):                // D-03: injectable, deterministic in tests
		case <-ctx.Done():
			return zero, ctx.Err()            // D-02: ctx-cancel returns; SHUTDOWN-01
		}
		delay *= 2                            // 1m→2m→4m→...
		if delay > dgraphRetryMax {
			delay = dgraphRetryMax            // cap 5m
		}
	}
}
```

**Call-site collapse** — the three read calls keep `break mainLoop` on error
(`main.go:147-176`, `178-207`, `225-253`); MarkAttempted (`287-314`) warns + continues per D-09:
```go
// Read calls (RETRY-03 exit-loudly):
pubkeys, err = retryDgraph(ctx, "GetStalePubkeys",
	func() (map[string]int64, error) {
		return dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, cfg.RelayFilterBatchSize)
	}, metrics, time.After)
if err != nil {
	log.Printf("Dgraph getting stale pubkeys failed: %v", err)
	break mainLoop
}

// MarkAttempted (D-09 best-effort): fatal/ctx-cancel → WARN + continue (NOT break)
if _, err := retryDgraph(ctx, "MarkAttempted",
	func() (struct{}, error) {
		return struct{}{}, dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix(), hitSet, backoffParams)
	}, metrics, time.After); err != nil {
	log.Printf("Warning: failed to mark batch attempted (best-effort): %v", err)
}
```

**Metrics line insertion** (`cmd/crawler/main.go:319`) — D-05/D-06/D-07. Add ONE line
immediately after the existing `Batch complete:` log. Cumulative since process start,
per call type, success-only:
```go
// EXISTING (line 319, keep):
log.Printf("Batch complete: queried %d pubkeys (%d had events) | %d stale remaining | %d total in DB",
	len(pubkeys), len(hitSet), staleRemaining, totalPubkeys)
// ADD (OBS-01): cumulative avg per call type, e.g.
log.Printf("Avg Dgraph call duration (cumulative): GetStalePubkeys=%v CountPubkeys=%v CountStalePubkeys=%v MarkAttempted=%v",
	metrics.avg("GetStalePubkeys"), metrics.avg("CountPubkeys"), metrics.avg("CountStalePubkeys"), metrics.avg("MarkAttempted"))
```

---

### `cmd/crawler/main_test.go` (test, request-response) — NEW

**Analog:** `pkg/dgraph/backoff_test.go` (table-driven verification of a geometric
backoff sequence — the closest existing test to "verify 1m→2m→4m→5m").

**Package convention:** internal-package tests in this module declare the package under
test directly (`package dgraph`, `package crawler`). Since the helper lives in `package main`,
the new test file MUST be `package main` to call the unexported `retryDgraph` and exercise
the unexported constants.

**Table-driven sequence assertion** (analog `pkg/dgraph/backoff_test.go:13-44`). Mirror this
structure to assert the captured sleep durations equal `[1m, 2m, 4m, 5m, 5m]`:
```go
// from pkg/dgraph/backoff_test.go — the pattern to copy:
func TestBackoffInterval(t *testing.T) {
	cases := []struct {
		missCount int
		want      time.Duration
	}{
		{0, 2 * time.Hour},
		{1, 4 * time.Hour},
		{2, 8 * time.Hour},
		// ...
		{7, 168 * time.Hour},   // capped
	}
	for _, tc := range cases {
		got := BackoffInterval(tc.missCount, base, ratio, cap_)
		if got != tc.want {
			t.Errorf("BackoffInterval(missCount=%d) = %v; want %v", tc.missCount, got, tc.want)
		}
	}
}
```

**Subtest naming** (analog `pkg/crawler/crawler_quorum_test.go:8-15`) — short, one-behavior-per-func
`TestX_Condition` style with `t.Fatal` on failure:
```go
func TestQuorumReached_BelowThreshold(t *testing.T) {
	if quorumReached(6, 10, 0.70) {
		t.Fatal("expected false when done=6 < ceil(0.70*10)=7")
	}
}
```

**No existing analog for the injected-clock / ctx-cancel mechanics** (see "No Analog Found").
The fake `sleepFn` and the mid-backoff cancellation test are Go-idiom new code. Sketch:
```go
// Fake clock: record requested delays, return a pre-closed channel so the
// select fires instantly — verifies the 1m→2m→4m→5m sequence with zero real wait.
func TestRetryDgraph_BackoffSequence(t *testing.T) {
	var slept []time.Duration
	fakeSleep := func(d time.Duration) <-chan time.Time {
		slept = append(slept, d)
		ch := make(chan time.Time, 1)
		ch <- time.Now() // fire immediately
		return ch
	}
	calls := 0
	fn := func() (int, error) {
		calls++
		if calls <= 5 {
			return 0, status.Error(codes.Unavailable, "transient") // force retries
		}
		return 42, nil
	}
	got, err := retryDgraph(context.Background(), "Test", fn, newCallMetrics(), fakeSleep)
	// assert err == nil, got == 42, slept == [1m, 2m, 4m, 5m, 5m]
}

// SHUTDOWN-01: ctx cancel interrupts an in-progress backoff.
func TestRetryDgraph_CtxCancelMidBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// fakeSleep returns a channel that NEVER fires; cancel() must win the select.
	never := func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	cancel() // pre-cancel; select's ctx.Done() branch returns ctx.Err()
	_, err := retryDgraph(ctx, "Test",
		func() (int, error) { return 0, status.Error(codes.Unavailable, "x") },
		newCallMetrics(), never)
	if err == nil { t.Fatal("expected ctx.Err() when cancelled mid-backoff") }
}
```

---

## Shared Patterns

### Backoff constants (package-level consts)
**Source:** `pkg/crawler/crawler.go:47-50`
**Apply to:** `cmd/crawler/main.go` backoff constants (keep as package consts; TUNE-01 config-driven is out of scope per CONTEXT D-04 / deferred).
```go
const (
	initialBackoff = 30 * time.Second
	maxBackoff     = 5 * time.Minute   // new dgraphRetryMax aligns at 5m
)
```
Doubling + cap clamp idiom (`pkg/crawler/crawler.go:168-171`):
```go
rs.backoff *= 2
if rs.backoff > maxBackoff {
	rs.backoff = maxBackoff
}
```

### Shutdown-safe wait (select over sleep + ctx.Done)
**Source:** `cmd/crawler/main.go:162-166` (also `pkg/crawler/crawler.go:530`)
**Apply to:** the new helper's inner wait — must select on `ctx.Done()` so SIGINT/SIGTERM
interrupts mid-backoff (SHUTDOWN-01, Success Criterion #4):
```go
select {
case <-time.After(retryDelay):
case <-ctx.Done():
	break mainLoop
}
```

### Logging style
**Source:** `cmd/crawler/main.go` throughout (`log.Printf` info / `Warning:` prefix)
**Apply to:** retry log + metrics line. Keep the literal `"… retrying in %v"` phrasing so the
1m/2m/4m/5m sequence is visible in the console (Success Criterion #2). Metrics = single
human-readable `log.Printf` line (no ticker goroutine, D-05).

### Error wrapping / classification
**Source:** `isDgraphTransient` (`cmd/crawler/main.go:37-51`) + gRPC `status.FromError`
**Apply to:** the helper's transient/fatal branch — reuse `isDgraphTransient` verbatim; do not duplicate the `codes.Unavailable/DeadlineExceeded/ResourceExhausted` switch.

## No Analog Found

| File / Concern | Role | Data Flow | Reason |
|----------------|------|-----------|--------|
| Injectable `sleepFn func(time.Duration) <-chan time.Time` | test seam | — | No existing test in the module injects a fake clock/sleep; all current tests (`backoff_test.go`, `crawler_quorum_test.go`, `crawler_filter_test.go`) test pure functions with no time dependency. New code follows Go idiom (function-typed dependency defaulting to `time.After`). |
| `context.WithCancel` mid-operation cancellation in a test | test | event-driven | No `*_test.go` in the module uses `context.WithCancel`/`cancel()`. The SHUTDOWN-01 cancel-mid-backoff test is new; pattern is standard Go (pre-cancel ctx + non-firing sleep channel → ctx.Done() wins the select). |
| Generic helper `retryDgraph[T any]` | utility | — | No existing generic helper in the module to copy a signature from; shape is specified directly in CONTEXT D-01. |
| `callMetrics` cumulative-average accumulator | utility | transform | No existing metrics/accumulator struct in `cmd/crawler`. Small new type (running sum + count per callName), planner/executor's discretion whether named type or inline (D-08, Claude's Discretion). |

## Metadata

**Analog search scope:** `cmd/crawler/`, `pkg/crawler/`, `pkg/dgraph/`, `pkg/config/` (all `*_test.go` in module)
**Files scanned:** `cmd/crawler/main.go`, `pkg/crawler/crawler.go`, `pkg/dgraph/backoff_test.go`, `pkg/crawler/crawler_quorum_test.go`, plus grep across 9 test files
**Pattern extraction date:** 2026-06-15

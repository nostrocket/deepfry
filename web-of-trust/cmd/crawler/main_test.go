package main

// Unit tests for the retryDgraph generic helper (TEST-01).
//
// All timing is deterministic via the injected sleepFn — no real time.Sleep.
// Tests must complete in well under a second regardless of the backoff constants.

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeSleep returns a sleepFn that appends the requested duration to *slept and
// fires the returned channel immediately (buffered, pre-loaded with time.Now()).
// This lets the select in retryDgraph proceed without any real wait so the full
// [1m, 2m, 4m, 5m, 5m] sequence is verified in microseconds.
func fakeSleep(slept *[]time.Duration) func(time.Duration) <-chan time.Time {
	return func(d time.Duration) <-chan time.Time {
		*slept = append(*slept, d)
		ch := make(chan time.Time, 1)
		ch <- time.Now() // fire immediately
		return ch
	}
}

// neverSleep returns a channel that is never closed/sent, so any select that
// uses it blocks unless the other case (ctx.Done()) fires.
func neverSleep(time.Duration) <-chan time.Time {
	return make(chan time.Time) // unbuffered, never written
}

// TestRetryDgraph_BackoffSequence verifies BACKOFF-01/02 and RETRY-01:
// a fn that returns codes.Unavailable five times then succeeds with 42 must
// produce delays [1m, 2m, 4m, 5m, 5m] (doubling, capped at dgraphRetryMax=5m).
func TestRetryDgraph_BackoffSequence(t *testing.T) {
	var slept []time.Duration
	calls := 0
	fn := func() (int, error) {
		calls++
		if calls <= 5 {
			return 0, status.Error(codes.Unavailable, "transient")
		}
		return 42, nil
	}

	got, err := retryDgraph(context.Background(), "BackoffTest", fn, newCallMetrics(), fakeSleep(&slept))
	if err != nil {
		t.Fatalf("expected nil error after transient retries, got: %v", err)
	}
	if got != 42 {
		t.Fatalf("expected value 42, got %d", got)
	}

	want := []time.Duration{
		1 * time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		5 * time.Minute, // hits cap
		5 * time.Minute, // stays capped
	}
	if len(slept) != len(want) {
		t.Fatalf("expected %d recorded delays, got %d: %v", len(want), len(slept), slept)
	}
	for i, w := range want {
		if slept[i] != w {
			t.Errorf("delay[%d] = %v; want %v", i, slept[i], w)
		}
	}
}

// TestRetryDgraph_CtxCancelMidBackoff verifies SHUTDOWN-01:
// a pre-cancelled context with a never-firing sleepFn must cause retryDgraph to
// return a non-nil error (ctx.Err()) instead of blocking forever.
func TestRetryDgraph_CtxCancelMidBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so ctx.Done() is ready immediately

	_, err := retryDgraph(ctx, "CancelTest",
		func() (int, error) { return 0, status.Error(codes.Unavailable, "transient") },
		newCallMetrics(), neverSleep)
	if err == nil {
		t.Fatal("expected non-nil error when context cancelled mid-backoff, got nil")
	}
}

// TestRetryDgraph_TransientOnCancelledCtx verifies WR-03: the most operationally-
// likely cancellation path. An in-flight Dgraph call cancelled at shutdown commonly
// surfaces as codes.Unavailable (classified transient). With fakeSleep (which fires
// immediately, racing ctx.Done()), the old code could log "retrying in 1m" and loop.
// The ctx.Err() short-circuit at the loop top must instead return a non-nil error
// within a bounded number of iterations and must NOT loop indefinitely.
func TestRetryDgraph_TransientOnCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: ctx.Err() is non-nil from the first iteration

	var slept []time.Duration
	calls := 0
	fn := func() (int, error) {
		calls++
		// Surface a transient code, as an interrupted in-flight call typically would.
		return 0, status.Error(codes.Unavailable, "transient during shutdown")
	}

	done := make(chan error, 1)
	go func() {
		_, err := retryDgraph(ctx, "CancelDuringCall", fn, newCallMetrics(), fakeSleep(&slept))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected non-nil error when ctx cancelled, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("retryDgraph did not return within bounded time; looped %d times", calls)
	}

	// The short-circuit returns before fn() is invoked, so fn must not run and
	// no backoff delay may be requested.
	if calls != 0 {
		t.Errorf("expected fn not to be called on a pre-cancelled ctx, got %d calls", calls)
	}
	if len(slept) != 0 {
		t.Errorf("expected 0 recorded delays on cancelled ctx, got %d: %v", len(slept), slept)
	}
}

// TestRetryDgraph_FatalPassthrough verifies RETRY-03:
// a fn returning a fatal code (codes.Unauthenticated) must be returned immediately
// with zero retries — the sleepFn must record zero delays.
func TestRetryDgraph_FatalPassthrough(t *testing.T) {
	var slept []time.Duration
	calls := 0
	fn := func() (int, error) {
		calls++
		return 0, status.Error(codes.Unauthenticated, "fatal")
	}

	_, err := retryDgraph(context.Background(), "FatalTest", fn, newCallMetrics(), fakeSleep(&slept))
	if err == nil {
		t.Fatal("expected fatal error to be returned, got nil")
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call on fatal error, got %d", calls)
	}
	if len(slept) != 0 {
		t.Errorf("expected 0 recorded delays on fatal passthrough, got %d: %v", len(slept), slept)
	}
}

// TestRetryDgraph_TransientThenSuccess verifies that success-only timing is
// recorded (OBS-01/D-07): one transient failure then success must record
// exactly one successful call, and exactly one delay must have been recorded.
// We assert on the recorded success count rather than avg > 0: the success
// duration is time.Since(start) over a no-op fn, which can truncate to 0ns on
// a fast machine, making an avg > 0 assertion non-deterministic (WR-04).
func TestRetryDgraph_TransientThenSuccess(t *testing.T) {
	var slept []time.Duration
	calls := 0
	fn := func() (int, error) {
		calls++
		if calls == 1 {
			return 0, status.Error(codes.Unavailable, "transient")
		}
		return 99, nil
	}

	m := newCallMetrics()
	got, err := retryDgraph(context.Background(), "X", fn, m, fakeSleep(&slept))
	if err != nil {
		t.Fatalf("expected nil error after one transient retry, got: %v", err)
	}
	if got != 99 {
		t.Fatalf("expected value 99, got %d", got)
	}
	if len(slept) != 1 {
		t.Errorf("expected exactly 1 recorded delay, got %d: %v", len(slept), slept)
	}
	if m.count["X"] != 1 {
		t.Errorf("expected exactly 1 recorded success for \"X\", got %d", m.count["X"])
	}
}

---
title: Retry transient Dgraph errors in the main crawl loop instead of exiting
area: crawler
status: resolved
created: 2026-06-13T12:44:51.805Z
resolved: 2026-06-15
resolution: Implemented as RESIL-01 in Phase 9 (09-02). isDgraphTransient() classifies Unavailable/DeadlineExceeded/ResourceExhausted; the three count/stale call sites retry 5x with 5s→2m backoff then exit loudly; MarkAttempted retries best-effort. Live-approved 2026-06-15.
source: 08-02 live-host verification (Phase 8)
---

# Retry transient Dgraph errors in the main crawl loop instead of exiting

## Context

During the Phase 8 (08-02) live-host verification run, the crawler exited on:

```
Error counting stale pubkeys: count stale pubkeys failed: rpc error: code = Unavailable desc = error reading from server: EOF
```

This is a **transient** Dgraph gRPC blip (connection `Unavailable` / `EOF`). The main loop
in `cmd/crawler/main.go` breaks on a `CountStalePubkeys` error, mirroring the **pre-existing**
break-on-error pattern already used for `CountPubkeys`. So the crawler relies on its
supervisor (systemd/docker restart policy) to bring it back up.

This is **not a Phase 8 defect** — `CountStalePubkeys` simply followed the established
convention. Logged here as optional robustness hardening.

## Proposed change

Make the main loop resilient to transient Dgraph errors rather than terminating:

- Wrap the per-batch `CountStalePubkeys` / `CountPubkeys` calls (and arguably the
  `GetStalePubkeys` / `MarkAttempted` calls) so a transient gRPC error
  (`codes.Unavailable`, EOF, deadline-exceeded) logs a WARN and retries with
  backoff instead of breaking the loop.
- Distinguish transient (retry) from fatal (exit) errors — e.g. via
  `google.golang.org/grpc/status` code inspection.
- Keep the existing exit behavior for genuinely fatal/unrecoverable errors so a
  misconfigured endpoint still surfaces loudly.

## Acceptance

- A simulated transient Dgraph disconnect (e.g. bounce the Dgraph container mid-run)
  causes a WARN + retry, and the crawl loop resumes once Dgraph is back — without a
  process restart.
- Genuinely fatal errors still terminate as before.

## Notes

- Affects `cmd/crawler/main.go` (loop) and possibly a small retry helper in `pkg/dgraph`.
- Consider doing this alongside any future relay/Dgraph resilience work rather than as
  a standalone micro-phase.

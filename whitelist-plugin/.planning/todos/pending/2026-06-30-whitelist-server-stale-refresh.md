---
created: 2026-06-30T06:08:44.714Z
title: Investigate stale whitelist refresh in long-running server container
area: whitelist-server
files:
  - pkg/whitelist/whitelist_refresher.go (periodic refresh loop)
  - cmd/server/main.go (refresher wiring + SetOnRefresh)
  - pkg/server/server.go (/stats last_refresh reporting)
---

## Problem

Observed during the v1.1 rebuild on 2026-06-30: the previously-running
`whitelist-server` container (image built 2026-06-26 from commit
`4e90731-dirty`, container "Up 3 days") was serving a **4-day-stale whitelist**.
`GET /stats` reported `last_refresh: 2026-06-26T09:03:47Z` — essentially the
startup load — even though the server is designed to rebuild the whitelist from
Dgraph every ~6h.

After `docker compose -f docker-compose.dgraph.yml up -d --build whitelist-server`
(rebuild + recreate), refresh advanced normally to `2026-06-30T06:00:00Z` and
`/stats` is current again. So the symptom only manifested in that specific
long-lived container instance.

Open question: was the periodic refresh **silently failing / dying** in the old
container (the real concern), or was the staleness an artifact unique to that
instance (e.g. a one-off Dgraph connectivity blip that the refresh loop never
recovered from)? If the refresh goroutine can die or get stuck without surfacing,
production relays fed by `/check` (and now the `/bloom` filter) would quietly
serve an ever-staler whitelist — newly-trusted pubkeys would be rejected
indefinitely until a manual restart.

This is a correctness/resilience concern, not a v1.1 blocker — the rebuilt
server refreshes fine. But it predates the bloom work and affects every consumer.

## Solution

**Part 1 — read the refresh loop (authoritative):** Review
`pkg/whitelist/whitelist_refresher.go` and its wiring in `cmd/server/main.go`.
Check specifically:
- Does a refresh error (Dgraph timeout / transient failure) **break the ticker
  loop** or get swallowed and retried on the next tick? A `return` on error
  inside the loop would permanently stop refreshes.
- Is the loop driven by a `time.Ticker` that keeps firing, or a
  `time.Sleep(interval)` chain that a panic/early-return could halt?
- Is there panic recovery around the refresh callback so a single panic doesn't
  kill the goroutine for the life of the process?
- Does `last_refresh` update only on **success**, or also on attempts? (If only
  on success, a stuck value is a strong signal refreshes were erroring.)

**Part 2 — observability:** there is currently no way to tell a healthy-but-stale
server from a fresh one except eyeballing `/stats`. Consider:
- log a WARN (stderr) on each refresh failure with the error,
- expose `last_refresh_attempt` / `consecutive_refresh_failures` in `/stats` or
  `/health`, and/or fail the healthcheck when `now - last_refresh > 2× interval`
  so Docker restarts a wedged server automatically. (Relates to deferred
  GATE-F2 — metrics/counters.)

**Part 3 — repro (best-effort):** if the loop looks robust, try to reproduce by
making Dgraph briefly unreachable (pause the `dgraph` container) across a refresh
tick and confirm the server recovers and resumes refreshing once Dgraph is back,
rather than latching stale.

**Done when:** (a) we know whether a refresh error can permanently stop the
refresh loop (and fix it with retry-in-loop + panic recovery if so), and
(b) a stuck/stale refresh is observable (log + a `/stats` or healthcheck signal)
instead of silent.

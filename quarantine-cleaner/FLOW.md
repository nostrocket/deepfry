# quarantine-cleaner — Execution Flow

## 0. Process starts

The CLI parses flags. If `--version` is passed, it prints version/commit/build-time and exits 0. Unknown flags exit non-zero with usage. SIGINT/SIGTERM are wired to a cancellation context that every later step respects.

## 1. Config load

Reads `~/deepfry/whitelist.yaml`.
- File exists but invalid (YAML parse error, unreadable) → exit non-zero immediately. No exec, no HTTP, no WS.
- File valid with `server_url` set → take `server_url` and `check_timeout` (default `2s`), ignore unknown keys.
- File missing, or valid but with no `server_url` → run the **discovery chain** to resolve `server_url`, in order. The whitelist server typically lives on a different host from the cleaner, so resolution is host-based:
  1. **Process env** — if `DGRAPH_HOST` is set, derive `server_url = http://<host>:8081`. Whitelist server and dgraph are co-deployed in the standard deployment, so the dgraph host is the whitelist host.
  2. **Repo `.env`** — locate via `$DEEPFRY_ROOT` or by walking up from cwd until a `.env` is found next to `docker-compose.strfry.yml`, then read `DGRAPH_HOST`. Standard `KEY=VALUE` `.env` syntax. Missing or unparseable file falls through.
  3. **TTY prompt** — if stdin is a TTY, prompt the operator for the URL (the prompt names the file that will be written).
  4. Otherwise (no env hint, no `.env`, non-TTY) → exit non-zero with a usage hint pointing at `~/deepfry/whitelist.yaml`.
  After resolution, write `server_url` back to `~/deepfry/whitelist.yaml` (creating the file if needed; preserving any existing keys owned by the live whitelist-plugin) before continuing. A failed write is fatal — exit non-zero before any preflight, exec, HTTP call, or WS dial.

Then the resolved config (every CLI flag value, the two YAML values, and the resolution source — `yaml` / `env` / `envfile` / `prompt`) is logged at INFO. Now the run's effective settings are visible.

## 2. Preflight

`GET <server_url>/health` with a 5s timeout.
- Healthy iff status is exactly `200` AND the body decodes to exactly `{"status":"ok"}`. Continue.
- Any other status (including the server's own `503 {"status":"loading", ...}` while it's still populating from Dgraph), transport error, timeout, decode error, or `status` field with any other value → exit non-zero. **Nothing else has happened yet.** This is intentional: a dead or still-loading whitelist server would otherwise look like "no pubkeys are whitelisted" and the tool would silently leave events stranded.
- Preflight runs even under `--dry-run`.

## 3. Phase 1 — Discover pubkeys from quarantine

Spawns `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> export`. Reads stdout line by line (buffer ≥ 1 MiB per line).

For each line:
- Empty → skip.
- JSON parse fails, or `pubkey` empty → WARN, skip, continue.
- Otherwise extract just the `pubkey` and add it to an in-memory map `pubkey → bool`, initialised to `false`. **Event bodies are not retained.** Phase 3 re-reads them from quarantine via `strfry scan` once we know which pubkeys we care about. This caps phase-1 memory at `O(distinct pubkeys)` regardless of how many events are sitting in quarantine.

Two stop conditions:
- `--limit N` successfully-parsed event lines reached (skipped lines do not count, per PRD FR-034) → break the read loop, terminate the exporter, wait for it to exit (non-zero exit is expected here), proceed to phase 2 with the pubkeys discovered so far. (The limit bounds discovery work, not the eventual forward set — phase 3 still publishes every event for the pubkeys that survived phase 2.)
- Exporter exits non-zero or scanner errors after EOF → abort the run, exit non-zero.

At end of phase 1: log INFO with `pubkeys_discovered=…` (distinct pubkeys), `events_exported=…` (non-empty lines read, including skipped, per FR-070).

## 4. Phase 2 — Filter pubkeys by whitelist

For each pubkey key in the map, fire `GET <server_url>/check/<pubkey>` with up to `--whitelist-concurrency` (default 8) calls in flight, each capped at `check_timeout`.

Per pubkey, whitelisted means status `200` AND body decodes to `{"whitelisted": true}` — both conditions, with `whitelisted` being the JSON literal `true`.
- Match → set the map entry to `true`.
- Anything else (transport error, non-200, decode error, timeout, missing `whitelisted` field, `whitelisted` not the JSON literal `true`) → WARN with the pubkey, leave the entry as `false`. **Fail-closed** — when in doubt, leave events in quarantine.

After all checks return, build a new slice of just the pubkeys whose value is `true` and drop the original map. Log INFO with `pubkeys_whitelisted=…`. (`events_scanned` is not yet known — it's the sum of per-pubkey scan counts that emerges during phase 3.)

## 5. Phase 3 — Forward events to main relay

The whitelisted-pubkey slice is the work queue. Spin up `--forward-concurrency` workers (default 4). Each worker:

1. (Non-dry-run only.) Dials its own WebSocket to `--main-relay` (timeout = `--publish-timeout`, default 5s) **before popping its first pubkey**. Dial failure → log WARN and exit; surviving workers drain the queue. If all workers fail to dial, every whitelisted pubkey is reported under `pubkeys_forward_failed` and re-attempted on the next run. Under `--dry-run` the dial is skipped entirely — workers go straight to popping a pubkey.
2. Pulls a pubkey off the queue.
3. Spawns `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> scan --reverse '{"authors":["<pubkey>"]}'` to stream that pubkey's events from the quarantine LMDB. The `--reverse` flag flips strfry's default newest-first scan into ascending `created_at` order, which is what we need (see why-oldest-first below). Reads the JSONL output line by line, retaining the **verbatim line bytes** for each event so the signature is preserved bit-for-bit on republish.
4. For each scan line: increment `events_scanned`, parse minimally (`id`, `kind`) when possible, then send `["EVENT", <verbatim raw bytes>]` and wait for `["OK", <id>, <bool>, <message>]` within `--publish-timeout`.
   - `OK true` → add id to `events_forwarded`.
   - `OK false`, timeout, scan-line decode failure, transport error, redial failure, ctx cancel → WARN with `pubkey` and error, plus `event_id`/`kind` when the scan-output line decoded; increment `events_forward_failed`. A successful redial is a precondition for sending the next event for this pubkey: if the socket was dropped, the worker MUST redial (timeout = `--publish-timeout`) before the next send. A failed redial counts the un-sent line under `events_forward_failed` and ends that worker — the end-of-pubkey verdict in step 6 then catches this pubkey under `pubkeys_forward_failed` because `failed > 0`; surviving workers continue draining the queue.
5. When the scan stream ends (process exit 0), decide whether to count this pubkey under `pubkeys_forward_failed` and take the next pubkey from the queue. Scan exit non-zero → WARN with the pubkey, but **don't** automatically count it — the verdict in step 6 still drives the counter.
6. End-of-pubkey verdict (drives `pubkeys_forward_failed`): the pubkey is added iff at least one of its events landed in `events_forward_failed`, or zero events were attempted (worker couldn't dial, or its `strfry scan` failed to start). A clean per-event run whose scan exited non-zero at EOF is logged but **not** counted.
7. Worker exits when the queue is empty; close the socket.

Memory bound: a worker holds at most one event's bytes at a time. Total in-flight memory is `O(distinct_pubkeys + forward_concurrency × max_event_size)` — the pubkey-set term covers the work queue carried over from phase 1, and the per-worker term is the event line currently in flight; neither scales with total events in quarantine or with per-pubkey event count.

**Why oldest-first per pubkey:** kinds 0, 3, 10000+, and 30000+ are last-write-wins on the relay. Publishing newest-first would let an older event clobber the newer one. Cross-pubkey parallelism is fine — only intra-pubkey order matters.

Under `--dry-run`: scans still run (that's how we count `events_scanned`) but workers do **not** dial the main relay and no `["EVENT", …]` is sent. The only external traffic in phase 3 is `docker exec` against the quarantine container. `events_forwarded=0` and `events_deleted=0` in the summary.

End of phase 3 (non-dry-run): `|events_forwarded| + events_forward_failed = events_scanned` — every scan line lands in exactly one outcome. Under `--dry-run` no publish is attempted, so `events_forwarded` is empty and `events_forward_failed` is 0 while `events_scanned` reflects the scan-line total; the count invariant doesn't apply in that mode. Log INFO with `events_scanned`, `events_forwarded`, `events_forward_failed`.

## 6. Phase 4 — Delete events from quarantine

Skipped entirely if `--dry-run` or if `events_forwarded` is empty.

Otherwise, batch the ids in `events_forwarded` into chunks of `--batch-size` (default 500). For each chunk:
- `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> delete --filter '{"ids":["…",…]}'`
- Exit 0 → all ids in chunk → `events_deleted`.
- Non-zero exit:
  - chunk size 1 → add the id to `events_delete_failed`, move on.
  - chunk size / 2 < 8 → try each id individually, recording per-id outcomes into `events_deleted` / `events_delete_failed`.
  - otherwise → halve and retry the failed chunk recursively.

Note: deletes are **only** by id (`{"ids":[…]}`), never by author or filter — a filter could match new events that arrived between phase 1 and phase 4.

End of phase 4: `events_deleted ∪ events_delete_failed = events_forwarded`, with no overlap. Log INFO with the counts.

## 7. Summary + exit

Emit one INFO record `cleaner summary` with all integer counters: `pubkeys_discovered`, `pubkeys_whitelisted`, `pubkeys_forward_failed`, `events_exported`, `events_scanned`, `events_forwarded`, `events_forward_failed`, `events_deleted`, `events_delete_failed`, `duration_ms`. `pubkeys_forward_failed` is the count of whitelisted pubkeys for which at least one event did not get `OK true` from the main relay — including pubkeys with zero attempted events (no worker dialled, or `strfry scan` failed to start). A clean per-event run whose scan happened to exit non-zero at EOF is WARN-logged but not counted here. Always 0 under `--dry-run`.

Release everything: WS sockets closed, exporter pipes drained, no goroutines/threads outliving the run. Exit 0 on a clean run, non-zero on any unrecoverable error or signal.

## What re-running looks like

If you run it again right after a successful run with no new events:
- Phase 1 discovers whatever pubkeys are still in quarantine (just the not-whitelisted ones from last time, plus any new ones).
- Phase 2 leaves them all `false` — they're still not whitelisted — so the whitelisted slice is empty.
- Phases 3 and 4 have no work. No scans, no publishes, no deletes.
- Summary: `events_forwarded=0`, `events_deleted=0`.

If a previous run was killed mid-phase-3, no deletes happened (phase 4 only sees that run's `events_forwarded`, which was discarded with the process). On the next run, the same pubkey is re-discovered, re-checked, re-scanned, and its events re-published — StrFry dedupes by id, so already-forwarded events are a no-op publish — and then deleted normally. No event is ever deleted from quarantine without a confirmed `OK true` from the main relay.

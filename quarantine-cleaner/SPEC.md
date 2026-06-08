# quarantine-cleaner — Specification

A short-lived CLI that rescues Nostr events from a quarantine StrFry relay
back into the main StrFry relay, but only for pubkeys that have since been
added to the whitelist.

## Why it exists

The main relay rejects writes from non-whitelisted pubkeys; rejected events
are routed to a sidecar quarantine relay. The whitelist refreshes from
Dgraph on a slow cadence (~6h), so a pubkey can be quarantined and then
become whitelisted shortly after, leaving recoverable events stranded. This
tool moves them.

## Boundaries

- One-shot process. Cron or a systemd timer drives invocation; the tool does
  not schedule itself.
- Never opens StrFry's LMDB directly. Uses `strfry export` / `strfry scan` /
  `strfry delete` via `docker exec`, plus NIP-01 over WebSocket.
- Never queries Dgraph. The whitelist HTTP server is the only source of
  truth for "is this pubkey allowed?"
- No persistent local state. Idempotent across runs.
- Read-only with respect to `~/deepfry/` once `server_url` is resolvable
  from `~/deepfry/whitelist.yaml`. On a run where the file is missing or
  has no `server_url`, the tool resolves the URL via the discovery chain
  in *Run sequence step 1* (process env → `.env` → TTY prompt) and
  persists it back to that file. Runs with `server_url` already resolved
  MUST NOT write under `~/deepfry/`.
- Memory bounded by `O(distinct pubkeys + forward_concurrency × max_event_size)`,
  never by total event count. Phase 1 keeps only pubkeys; phase 3 streams
  events per pubkey via `strfry scan` and publishes one at a time, so a
  quarantine of arbitrary size is processable on a small host.

## Run sequence

1. **Load config** — read `~/deepfry/whitelist.yaml`. Exit non-zero if the
   file exists but is invalid. If `check_timeout` is absent, fall back to
   `2s`. If `server_url` is absent (or the whole file is missing), run the
   discovery chain. The whitelist server typically lives on a different
   host from the cleaner, so resolution is host-based, not container-based:
   1. **Process env**: if `DGRAPH_HOST` is exported in the current
      environment, derive the whitelist URL as `http://<host>:8081`.
      Whitelist server and dgraph are co-deployed on the same host in
      the standard deployment, so the dgraph host is the whitelist host.
   2. **Repo `.env`**: probe the deepfry repo's `.env` (located via
      `$DEEPFRY_ROOT`, or by walking up from the working directory until
      a `.env` is found next to `docker-compose.strfry.yml`) for
      `DGRAPH_HOST`. The parser MUST accept standard `KEY=VALUE` `.env`
      syntax (one assignment per line, optional surrounding quotes,
      `#`-prefixed comments).
   3. Otherwise, if stdin is a TTY, prompt the operator for the URL. The
      prompt MUST state the path of the file that will be written
      (`~/deepfry/whitelist.yaml`); the path is not configurable.
   4. Otherwise (non-TTY, no env hint), exit non-zero with a usage hint
      pointing at the config file path.
   Persist the resolved `server_url` back to `~/deepfry/whitelist.yaml`,
   creating the file if needed and preserving any existing keys — the
   file is shared with the live whitelist-plugin, which writes
   `dgraph_graphql_url`, `server_listen_addr`, `refresh_interval`, and
   other plugin-only keys to the same file. The bootstrap write is
   one-time per host; a failure to write exits non-zero before any exec
   or HTTP call.
   Log the resolved config (all CLI flag values plus `server_url` and
   `check_timeout`, plus the resolution source — `yaml` / `env` /
   `envfile` / `prompt`) at INFO before doing anything else, so every
   run's effective settings are visible in the log stream.
2. **Preflight** — `GET /health` on the whitelist server (5s timeout).
   Healthy means status `200` AND body exactly `{"status":"ok"}`. Any
   other status (including the server's own `503` `{"status":"loading",...}`
   while it's still populating from Dgraph), transport error, decode
   error, or mismatched `status` value exits non-zero before touching
   anything else (including under `--dry-run`).
3. **Phase 1: Discover pubkeys from quarantine** — stream events from quarantine via
   `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> export` and parse
   each JSONL line for its `pubkey` only. Build a map `pubkey → bool`
   initialised to `false`. **Do not retain event bodies** — phase 3 re-reads
   them per pubkey via `strfry scan`. Skip malformed lines with a WARN;
   do not abort. Honour `--limit N` (caps lines read).
4. **Phase 2: Filter pubkeys by whitelist** — for each pubkey key, `GET /check/<pubkey>` with
   bounded concurrency. Set the value to `true` only if the response is
   status `200` AND the body decodes to `{"whitelisted": true}`. Anything
   else — transport error, non-200, decode error, timeout, missing
   `whitelisted` field, `whitelisted` not the JSON literal `true` —
   leaves the value `false` (fail-closed). After all checks, build a
   slice of just the `true` pubkeys and discard the original map.
5. **Phase 3: Forward events to main relay** — for each whitelisted pubkey, a worker spawns
   `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> scan --reverse '{"authors":["<pubkey>"]}'`
   to stream that pubkey's events oldest-first directly from the quarantine
   LMDB. Each scan line is published verbatim (raw bytes preserved) over the
   worker's dedicated WebSocket connection, one at a time, waiting for
   `["OK", …]` per event. Different pubkeys run in parallel; events for a
   given pubkey are strictly sequential. Track `events_forwarded` (a set
   of event ids — one per relay `OK true`) and `events_forward_failed`
   (a count of scan lines that did not get `OK true` — `OK false`,
   timeout, scan-line decode failure, transport error, redial failure,
   ctx cancel). A
   pubkey lands in
   `pubkeys_forward_failed` iff at least one of its events did not get
   `OK true`, including the degenerate case where zero events were
   attempted (worker couldn't dial, or this pubkey's `strfry scan`
   failed to start). A clean per-event run whose `strfry scan` happens
   to exit non-zero at EOF is WARN-logged but not counted as a failed
   pubkey. Under `--dry-run`, scans still run (to count
   `events_scanned`) but workers do **not** dial the main relay and no
   `EVENT` is sent — the only external traffic in dry-run is
   `docker exec` against the quarantine container; `pubkeys_forward_failed`
   is therefore always 0 in dry-run.
6. **Phase 4: Delete events from quarantine** — only for the ids in `events_forwarded`, run
   `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> delete --filter '{"ids":[…]}'`
   in batches. On batch failure: at size 1, record the failed id and
   continue; else if `current_batch_size / 2 < 8`, retry each id
   individually; else halve the failed batch and retry. Phase 4
   produces two disjoint sets, `events_deleted` and
   `events_delete_failed` (their union equals `events_forwarded`).
   Skip phase 4 entirely when `--dry-run` is set or when
   `events_forwarded` is empty.
7. **Summary** — emit one INFO log line `cleaner summary` with counts (see
   below).

## Why oldest-first matters

Replaceable kinds (0, 3, 10000+) and parameterised replaceable kinds
(30000+) are last-write-wins on the relay. Publishing newest-first would
let an older copy clobber a newer one. Per-pubkey ordering is therefore
load-bearing; cross-pubkey parallelism is fine.

## Configuration

`~/deepfry/whitelist.yaml`:

| Key | Default | Notes |
|---|---|---|
| `server_url` | *(resolved at runtime via discovery chain)* | Whitelist server base URL |
| `check_timeout` | `2s` | Per-`/check` HTTP timeout |

Unknown keys are ignored. A missing file or missing `server_url` triggers
the discovery chain in *Run sequence step 1*; on first run, the tool MAY
create or update this file solely to persist a freshly-resolved
`server_url` while preserving any existing keys. After that one-time
bootstrap write the file is read-only.

## CLI flags

| Flag | Default | Purpose |
|---|---|---|
| `--dry-run` | false | Run scans but don't publish or delete; report what would be moved |
| `--limit N` | 0 | Stop phase-1 export after N successfully-parsed event lines (0 = unlimited; negative values clamp to 0). Skipped/malformed lines do not count toward N. Caps discovery work; does not bound phase 3. |
| `--batch-size N` | 500 | IDs per `strfry delete` invocation |
| `--forward-concurrency N` | 4 | Parallel pubkeys forwarded |
| `--whitelist-concurrency N` | 8 | Parallel `/check` calls |
| `--main-relay URL` | `ws://localhost:7777` | Main StrFry WebSocket |
| `--quarantine-container NAME` | `strfry-quarantine` | Docker container name |
| `--quarantine-config PATH` | `/etc/strfry.conf` | StrFry config path inside container |
| `--publish-timeout DUR` | `5s` | Per-publish + dial timeout |
| `--log-level LEVEL` | `debug` | `debug`/`info`/`warn`/`error` |
| `--version` | false | Print version, commit, build time, exit 0 |

Values ≤ 0 for any concurrency/batch flag fall back to the default.
Negative values for `--limit` clamp to 0 (unlimited). Unknown flags
exit non-zero with usage text.

## External interfaces

- **Whitelist HTTP**:
  - `GET /health` (preflight): healthy iff status `200` AND body exactly
    `{"status":"ok"}`. Any other body or status (including the server's
    `503` `{"status":"loading",...}` startup state) is treated as
    unhealthy and aborts the run.
  - `GET /check/<pubkey>` → `{"whitelisted": <bool>}`. The pubkey is
    treated as whitelisted iff the response is status `200` AND the
    decoded body's `whitelisted` field is the JSON literal `true`.
    Pubkey is hex, passed verbatim — no lowercasing, no validation.
- **Quarantine container**: three subcommands invoked via `docker exec`:
  - `… /app/strfry --config=<quarantine-config> export` — JSONL stream of all events, used
    in phase 1 to discover pubkeys.
  - `… /app/strfry --config=<quarantine-config> scan --reverse '{"authors":["<pubkey>"]}'` —
    JSONL stream of one pubkey's events in ascending `created_at` order,
    used in phase 3.
  - `… /app/strfry --config=<quarantine-config> delete --filter '{"ids":[…]}'` — batched
    delete by id, used in phase 4.

  `docker` must be on `PATH` and the user must have permission to exec into
  the container.
- **Main relay**: NIP-01 WebSocket. Send `["EVENT", <event>]`, expect
  `["OK", <id>, <bool>, <message>]`. Only `OK true` counts as success — a
  closed connection or timeout before `OK` arrives is a per-event failure,
  not a silent success. After a transport-level drop the worker redials
  before publishing the next event for the same pubkey; a failed redial
  ends that worker (surviving workers continue to drain the queue). Under
  `--dry-run`, the main relay is never contacted: workers skip the dial
  entirely, and the only external traffic in phase 3 is `docker exec`
  against the quarantine container.
- **Deepfry repo `.env`** (best-effort, read-only): consulted only during
  the first-run config bootstrap to derive `server_url` from `DGRAPH_HOST`.
  The probe path is resolved via `$DEEPFRY_ROOT` or by walking up from the
  working directory to the directory containing `docker-compose.strfry.yml`.
  A missing or unparseable `.env` is not an error — it just falls through
  to the TTY prompt. The parser MUST accept standard `KEY=VALUE` `.env`
  syntax (one assignment per line, optional surrounding quotes,
  `#`-prefixed comments).

## Error handling

| Condition | Behaviour |
|---|---|
| Config YAML invalid | Exit non-zero before any side effect |
| Config YAML missing or `server_url` unset | Run discovery chain (process env → `.env` probe → TTY prompt). On any hit: persist to `~/deepfry/whitelist.yaml` and continue. If all three steps yield nothing (no env hint, no `.env` hint, non-TTY stdin): exit non-zero |
| Bootstrap write to `~/deepfry/whitelist.yaml` fails | WARN with path/error, exit non-zero before any exec/HTTP call |
| Whitelist preflight unhealthy (anything other than `200 {"status":"ok"}`, transport error, decode error, timeout) | Exit non-zero, no exec/publish/delete |
| `strfry export` fails to start | Abort run, exit non-zero |
| `strfry export` exits non-zero **after EOF** | Abort run, exit non-zero |
| `strfry export` exits non-zero **because the tool terminated it** (`--limit` reached or SIGINT/SIGTERM) | Not an error in itself; follow `--limit` / signal handling |
| Single export line malformed or missing `pubkey` | WARN, skip line |
| `strfry scan` fails to start for a pubkey | WARN; zero events are attempted for the pubkey, so it lands in `pubkeys_forward_failed`; continue with next pubkey |
| `strfry scan` exits non-zero **after the worker has finished reading its stdout** | WARN with the pubkey; the pubkey lands in `pubkeys_forward_failed` only if at least one of its events also failed to forward (a clean per-event run with a non-zero scan exit is logged but not counted as a failed pubkey); continue with next pubkey |
| `/check` does not return `200 {"whitelisted": true}` (transport error, non-200, decode error, timeout, missing/non-`true` `whitelisted` field) | WARN, treat as not-whitelisted (fail-closed) |
| Worker fails to dial main relay | WARN; the worker exits without popping any pubkey; surviving workers drain the queue. If all workers fail to dial, every whitelisted pubkey is reported under `pubkeys_forward_failed` and re-attempted on the next run. |
| Per-event publish failure (any of: `OK false`, timeout, scan-line decode failure, transport drop, redial failure, ctx cancel) | WARN with `pubkey` and error, plus `event_id`/`kind` when the scan-output line decoded; increment `events_forward_failed`, continue |
| `strfry delete` batch fails | At size 1, record the failed id and continue. Else if `current_batch_size / 2 < 8`, retry each id individually. Else halve the batch and retry. |
| SIGINT/SIGTERM | Cancel in-flight ops, exit non-zero, never delete an unconfirmed event |
| `--limit` reached during export | Stop reading, terminate the exporter, wait for it to exit (its non-zero exit is expected here), proceed with the pubkeys discovered so far |

## Logging

- Default log level is `debug`. The tool is operational tooling that runs
  on a slow cadence; verbosity is cheap and a quiet failure is expensive.
  Operators raise the level explicitly when they don't want the noise.
- All logs to stderr, JSON, one record per line, with a `level` field.
- Only ids, pubkeys, kinds, and counts. No event content, no secrets.
- Phase boundaries logged at INFO with their inputs/outputs.
- At DEBUG, log every meaningful step so a failed run can be diagnosed
  from logs alone:
  - each export line accepted (with `pubkey`) and each line skipped
    (with the reason).
  - each `/check` request and response (pubkey, status, decoded
    `whitelisted` value, elapsed ms).
  - per-worker WebSocket dial start/success/failure.
  - each `strfry scan` invocation start/exit per pubkey (with the
    line count produced and elapsed ms).
  - each `["EVENT", …]` send and the matching `["OK", …]` response
    (event id, ack bool, relay message, elapsed ms).
  - each `strfry delete` invocation (batch size, exit code, elapsed ms)
    and every halve-and-retry decision.
- Final line is always `cleaner summary` with these integer fields:
  `pubkeys_discovered`, `pubkeys_whitelisted`, `pubkeys_forward_failed`,
  `events_exported`, `events_scanned`, `events_forwarded`,
  `events_forward_failed`, `events_deleted`, `events_delete_failed`,
  `duration_ms`.

## Idempotency

Two consecutive runs against unchanged state must produce
`events_forwarded=0` and `events_deleted=0` on the second run. StrFry
dedupes by event id on publish and tolerates delete-of-missing, so
faithfully running phases 1–4 is sufficient.

A run killed mid-phase-3 must not delete anything; on re-run, already
forwarded events become a no-op publish and their deletion completes.

## Acceptance tests

End-to-end against a clean stack with both StrFry compose files up:

1. Pubkey `A` whitelisted, pubkey `B` not. Inject 3× kind-1 from each into
   quarantine.
2. `--dry-run` reports `pubkeys_whitelisted=1`, `events_scanned=3`,
   `events_forwarded=0`, `events_deleted=0`. Quarantine still has 6 events.
3. Real run reports `events_forwarded=3`, `events_deleted=3`,
   `events_forward_failed=0`. Main relay has 3 events for `A`; quarantine
   has 0 for `A` and still 3 for `B`.
4. Re-run reports `events_forwarded=0`, `events_deleted=0`.
5. Reject-path: with `B` whitelist-server-positive but the main relay
   still rejecting (e.g. plugin not yet refreshed), the cleaner logs
   per-event rejections for `B`'s events and `events_deleted` does
   not include any of `B`'s ids.
6. Two kind-0 events from `A` injected 100s apart: after rescue, the main
   relay returns the **newer** one for `{authors:[A], kinds:[0]}` —
   confirms oldest-first sequential publishing.
7. With the whitelist server down, the run exits non-zero and never invokes
   `strfry export`.
8. A non-JSON line in the export stream is skipped with a WARN; valid
   lines around it are processed normally.
9. If `strfry delete` fails for one specific id, every other id is still
   deleted and the failing id appears under `events_delete_failed`.
10. Memory cap: a quarantine of 1M events from 100 distinct pubkeys
    completes successfully on a host with limits set well below the
    total event payload size — confirmation that nothing in the flow
    buffers all events at once.
11. With no `~/deepfry/whitelist.yaml`, no process-env hint, and no
    `.env` hint available, an interactive (TTY) run prompts for the
    whitelist URL, writes the file, then proceeds normally; a
    non-interactive run with the same starting state exits non-zero
    before any `docker exec` or HTTP call.
12. With `.env` exposing `DGRAPH_HOST=10.0.0.5`, no process-env hint,
    and no `~/deepfry/whitelist.yaml`, the run resolves
    `server_url=http://10.0.0.5:8081` without prompting and persists
    it to the config file.
13. With no `~/deepfry/whitelist.yaml` and `DGRAPH_HOST=10.0.0.5`
    exported in the process environment, the run resolves
    `server_url=http://10.0.0.5:8081` without consulting `.env` or
    prompting and persists it to the config file.

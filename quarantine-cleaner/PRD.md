# quarantine-cleaner — PRD

Authoritative, language-agnostic behavioural specification for
`quarantine-cleaner`. Every requirement is assigned a stable ID (`FR-*`,
`NFR-*`, `IF-*`, `ER-*`, `AC-*`) so an implementation can be verified
requirement-by-requirement.

An implementation MUST satisfy every `MUST`/`MUST NOT` requirement.
`SHOULD` requirements MAY be relaxed when the target language or runtime
makes a literal expression awkward, provided observable behaviour matches.

---

## 1. Overview

### 1.1 Purpose

`quarantine-cleaner` is a one-shot host-side CLI that moves Nostr events from a
sidecar **quarantine** StrFry relay back into the **main** StrFry relay, but
only for pubkeys that have since been added to the live whitelist. It exists
because:

- The main relay's whitelist plugin rejects writes from non-whitelisted
  pubkeys; the router forwards rejected events to the quarantine relay.
- The whitelist refreshes from Dgraph on a long cadence (≈6 h). A pubkey can
  be quarantined at time T and become whitelisted shortly after, leaving
  recoverable events stranded.
- This tool periodically rescues those stranded events.

### 1.2 Goals

1. Move quarantined events for newly-whitelisted pubkeys into the main relay
   without losing newer replaceable-kind state.
2. Be safe to re-run (idempotent).
3. Never delete a quarantine event that did not successfully land on the main
   relay.
4. Never modify, fork, or directly write to StrFry's LMDB. Use only StrFry's
   own subcommands and its NIP-01 WebSocket interface.
5. Stay configuration-coherent with the live whitelist plugin: the cleaner's
   "whitelisted?" answer for a pubkey MUST equal the live plugin's answer at
   the same point in time.

### 1.3 Non-goals

- Long-running daemon or scheduler. Cron/systemd timer drives invocation.
- Modifying StrFry source or LMDB schema.
- Storing state between runs (no local DB, no checkpoint file).
- Querying Dgraph directly. The whitelist server is the single source of
  truth.
- Signature verification, content validation, or kind-specific business
  logic. The receiving relay's policy enforces those.

---

## 2. System Context

```
                                    ┌──────────────────────────┐
                                    │ whitelist server (HTTP)  │
                                    │  GET /health             │
                                    │  GET /check/{pubkey}     │
                                    └─────────────┬────────────┘
                                                  │ HTTP
                                                  ▼
┌─────────────────────────┐                ┌──────────────┐
│  quarantine StrFry      │  exec/stdout   │              │  WebSocket  ┌─────────────────────┐
│  (docker container)     │◄──────────────►│  cleaner CLI │────────────►│  main StrFry relay  │
│  /app/strfry            │                │              │  NIP-01     │  ws://…:7777        │
│   --config=/etc/        │  exec/stdin    │              │             └─────────────────────┘
│     strfry.conf         │◄──────────────►│              │
│   export | scan |       │                └──────────────┘
│   delete                │
└─────────────────────────┘
```

**Actors / dependencies** the port MUST be able to drive:

- `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> export`
  (streams JSONL on stdout — used in phase 1 to discover pubkeys)
- `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> scan --reverse '<filter>'`
  (streams JSONL on stdout — used in phase 3, one invocation per
  whitelisted pubkey, with `{"authors":["<pubkey>"]}`)
- `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> delete --filter <filter>`
  (one-shot, returns non-zero on failure — used in phase 4)
- HTTP `GET <whitelist_server>/health` and `GET <whitelist_server>/check/<pubkey>`
- Outgoing WebSocket connection speaking NIP-01 to a StrFry relay.
- A YAML reader/writer for `~/deepfry/whitelist.yaml` (read every run;
  write only on first-run bootstrap per FR-015, preserving keys owned
  by the live whitelist-plugin).
- A minimal `.env` reader for the deepfry repo root, used during FR-015
  step 2 to derive `server_url` from a dgraph-host variable when the
  process env yields nothing.
- A TTY-detection + line-prompt facility, used only during FR-015 step
  3 when no process env or `.env` hint is available.

---

## 3. Functional Requirements

### 3.1 Lifecycle and orchestration

- **FR-001 (MUST)** The tool runs as a single short-lived process: parse flags,
  perform phases 1–4 (or stop earlier per FR-051..FR-055), write a final
  summary, exit.
- **FR-002 (MUST)** On `--version` (or equivalent), print version, commit, and
  build timestamp populated at build time and exit 0 without other side
  effects.
- **FR-003 (MUST)** Exit 0 on success, non-zero on any unrecoverable error
  (config load failure, whitelist server unreachable, export start failure,
  fatal export error).
- **FR-004 (MUST)** On SIGINT or SIGTERM, abort the run cleanly: cancel
  in-flight HTTP/WebSocket/exec operations and exit non-zero. No partially
  forwarded event may be deleted from quarantine after a cancel.
- **FR-005 (MUST)** All log records go to stderr in JSON, one record per
  line, with a `level` field (`DEBUG`/`INFO`/`WARN`/`ERROR`).
- **FR-006 (SHOULD)** At DEBUG level, emit one record per meaningful
  step so a failed run can be diagnosed from logs alone:
  - each export line accepted (with `pubkey`) and each line skipped
    (with the reason).
  - each `/check` request and response (pubkey, status, decoded
    `whitelisted` value, elapsed ms).
  - per-worker WebSocket dial start, success, and failure.
  - each `strfry scan` invocation start/exit per pubkey (with the
    line count produced and elapsed ms).
  - each `["EVENT", …]` send and the matching `["OK", …]` response
    (event id, ack bool, relay message, elapsed ms).
  - each `strfry delete` invocation (batch size, exit code, elapsed
    ms) and every halve-and-retry decision.

### 3.2 Configuration loading

- **FR-010 (MUST)** Read `~/deepfry/whitelist.yaml` as the primary source for
  two keys: `server_url` (string) and `check_timeout` (duration string,
  e.g. `2s`, `500ms`). All other keys MUST be ignored, not rejected.
  When `server_url` is absent or the file is missing, FR-015's discovery
  chain produces the value.
- **FR-011 (MUST)** A missing file is no longer fatal nor silent — it
  triggers FR-015. An **invalid** file (YAML parse error, unreadable, etc.)
  MUST cause a non-zero exit before any other side effect.
- **FR-012 (MUST)** Defaults: `check_timeout=2s`. `server_url` has **no**
  static default — it MUST come from the config file, the FR-015 env
  probe, or the FR-015 TTY prompt.
- **FR-013 (MUST)** The file MUST NOT be auto-created or auto-rewritten,
  **except** as the first-run bootstrap write defined in FR-015. The
  bootstrap write only adds or updates the `server_url` key and MUST
  preserve any other keys already present in the file. Once `server_url`
  is resolvable from the file, FR-015 does not fire and the file remains
  read-only on every subsequent run; if the file or the `server_url` key
  is later removed, FR-015 re-fires and may write again.
- **FR-014 (MUST)** Log the resolved `server_url`, `check_timeout`, every
  CLI flag value, and the resolution source for `server_url`
  (`yaml` / `env` / `envfile` / `prompt`) once at INFO at startup,
  before any preflight, exec, HTTP, or WebSocket call. When `server_url`
  is read directly from `~/deepfry/whitelist.yaml`, the resolution
  source is `yaml`.
- **FR-015 (MUST)** **Discovery chain.** When `server_url` is unresolved
  after FR-010 (file missing, or present but lacking `server_url`), the
  tool MUST run the following steps, in order. Each step yields either a
  resolved `server_url` plus a resolution-source label (`env` /
  `envfile` / `prompt`) for log attribution, or falls through to the
  next step. The whitelist server typically runs on a different host
  from the cleaner, so discovery is host-based:
  1. **Process env**: read `DGRAPH_HOST` from the current process
     environment. If non-empty, derive `server_url = http://<host>:8081`.
     (Whitelist server and dgraph are co-located on the same host in
     the standard deployment, so the dgraph host is the whitelist host.)
  2. **Repo `.env`**: probe the deepfry repo's `.env` (located via
     `$DEEPFRY_ROOT`, or by walking up from the working directory until
     a `.env` is found adjacent to `docker-compose.strfry.yml`) for
     `DGRAPH_HOST`. The parser MUST accept the standard `KEY=VALUE`
     `.env` syntax (one assignment per line, optional surrounding
     quotes, `#`-prefixed comments). Missing or unparseable `.env` MUST
     fall through to step 3.
  3. **TTY prompt**: if stdin is a TTY, prompt the operator for the
     whitelist server URL. The prompt MUST include the path of the file
     that will be written.
  4. **Fail**: otherwise (no env hint anywhere, non-TTY stdin), exit
     non-zero with a usage hint pointing at `~/deepfry/whitelist.yaml`.
  After resolution, the tool MUST persist the value to
  `~/deepfry/whitelist.yaml` (creating the file if needed) before
  proceeding to preflight. A failure to write the file is fatal
  (see ER-15).
- **FR-016 (MUST)** The bootstrap write of FR-015 MUST preserve any other
  keys already present in `~/deepfry/whitelist.yaml`. The file is shared
  with the live whitelist plugin, which reads keys such as
  `dgraph_graphql_url`, `server_listen_addr`, `refresh_interval`,
  `refresh_retry_count`, `idle_conn_timeout`, `http_timeout`,
  `query_timeout`, and `debug` from the same file. The cleaner MUST
  ignore those keys on read (per FR-010) and preserve them on write.
  The write MUST be safe against partial failures (e.g.
  write-to-temp-then-rename, or equivalent).

### 3.3 Whitelist server preflight

- **FR-020 (MUST)** Before any export, exec, publish, or delete, perform a
  `GET /health` on the whitelist server with a 5-second timeout. The
  response is considered healthy iff the status is exactly `200` and the
  JSON body is exactly `{"status":"ok"}`. Any other status (including
  `503` with `{"status":"loading", ...}`), transport error, JSON decode
  error, or mismatched `status` value MUST cause a non-zero exit with no
  other side effects.
- **FR-021 (MUST NOT)** Skip preflight even when `--dry-run` is set. The point
  of preflight is to refuse to mass-skip due to a server outage.

### 3.4 Phase 1 — discover pubkeys from quarantine

Phase 1 is a **discovery pass**, not an event-collection pass. Event
bodies are not retained; phase 3 re-reads them per pubkey via
`strfry scan`. This caps memory at `O(distinct pubkeys)` regardless of
total event count.

- **FR-030 (MUST)** Stream events by spawning
  `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> export`
  and reading stdout line-by-line as JSONL.
- **FR-031 (MUST)** For each non-empty stdout line, parse the minimal subset
  needed to extract `pubkey`. Lines that fail JSON parse, or whose `pubkey`
  is empty, MUST be logged at WARN and skipped — they MUST NOT abort the
  run.
- **FR-032 (MUST NOT)** Retain event line bytes, ids, kinds, or
  `created_at` from phase 1. Phase 1 MUST emit a set of distinct pubkeys
  and a count of lines read; no event bodies, ids, kinds, or timestamps
  may be retained. Verbatim raw bytes are obtained later, in phase 3,
  from `strfry scan` output.
- **FR-033 (MUST)** Build an in-memory map `pubkey → bool` initialised to
  `false` for every distinct pubkey observed.
- **FR-034 (MUST)** Honour `--limit N`. Negative values MUST be clamped
  to `0` before use (treated as unlimited). When `N>0`, stop reading
  after the first `N` **successfully-parsed** event lines (lines skipped
  due to JSON parse error or missing `pubkey` do not count toward `N`).
  The export process MUST then be terminated and waited on. Partial
  pubkey sets are valid and proceed to phase 2. Note: `--limit` bounds
  discovery only; phase 3's per-pubkey scans are not limited.
- **FR-035 (MUST)** A non-zero exit from `strfry export` (after EOF) or a
  scanner error MUST surface as a phase-1 error and abort the run.
- **FR-036 (MUST)** The JSONL line scanner MUST tolerate lines up to at least
  1 MiB. Smaller hard buffer caps in the target language are not acceptable.
- **FR-037 (MUST NOT)** The tool MUST NOT open the quarantine LMDB, lock
  file, or any file under the quarantine container's data directory directly.

### 3.5 Phase 2 — filter pubkeys by whitelist

- **FR-040 (MUST)** For each unique pubkey collected in phase 1, call
  `GET <server_url>/check/<pubkey>` once and parse the JSON body
  `{"whitelisted": <bool>}`. A pubkey is considered whitelisted iff the
  response status is exactly `200` AND the decoded body's `whitelisted`
  field is exactly the JSON value `true`. Status alone (200 with a
  missing/false/non-boolean `whitelisted` field) is NOT sufficient.
- **FR-041 (MUST)** Treat any of the following as "not whitelisted"
  (fail-closed) and continue with the rest of the run: transport error,
  timeout, non-200 status, JSON decode error, missing `whitelisted`
  field, `whitelisted` field present but not the JSON literal `true`.
  The error MUST be logged at WARN with the offending pubkey.
- **FR-042 (MUST)** Run checks concurrently with bounded parallelism. Default
  parallelism = 8; configurable via `--whitelist-concurrency`. Values ≤ 0
  resolve to the default.
- **FR-043 (SHOULD)** Reuse outbound HTTP connections (pooled keep-alive)
  with a per-host idle/connection cap of at least 32. This prevents
  ephemeral-port exhaustion (`EADDRNOTAVAIL`) under burst.
- **FR-044 (MUST)** The HTTP timeout for each `/check` request equals the
  configured `check_timeout`.
- **FR-045 (MUST)** After phase 2, build a slice of pubkeys whose value is
  `true` (the `/check` call returned status `200` with body
  `{"whitelisted": true}` per FR-040). The original `pubkey → bool` map
  MUST be discarded. Pubkeys with value `false` are not deleted from
  quarantine and not forwarded.

### 3.6 Phase 3 — forward events to main relay

Events are not held in memory across phase 1. Instead, each worker
re-reads its assigned pubkey's events directly from the quarantine LMDB
via `strfry scan` and publishes them streaming, one at a time.

- **FR-050 (MUST)** For each whitelisted pubkey, the worker MUST spawn
  `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> scan --reverse '{"authors":["<pubkey>"]}'`
  and read its stdout line-by-line. The `--reverse` flag is required: it
  flips strfry's default newest-first scan into ascending `created_at`
  order. Each scan line MUST be published before the next is read, so the
  publish order is **oldest-first per pubkey**. Rationale: replaceable
  kinds (kind 0, 3, 10000+) and parameterised replaceable kinds (30000+)
  are last-write-wins on the relay; out-of-order publish lets an older
  copy clobber a newer one.
- **FR-051 (MUST)** Different pubkeys MAY be processed in parallel. The
  worker pool size defaults to 4 and is configurable via
  `--forward-concurrency`. Values ≤ 0 resolve to the default.
- **FR-052 (MUST)** Outside `--dry-run`, each worker MUST hold its own
  dedicated WebSocket connection to the main relay for the duration of
  phase 3. Under `--dry-run`, workers MUST NOT dial the main relay (see
  FR-058); the only external traffic in dry-run is `docker exec` against
  the quarantine container.
- **FR-053 (MUST)** Per-event publish has a timeout equal to
  `--publish-timeout` (default 5s). Same value caps the initial
  WebSocket dial.
- **FR-054 (MUST)** For each scan-output line, the worker MUST publish the
  **verbatim line bytes** as `["EVENT", <line-bytes>]` on the WebSocket
  and treat the relay's `["OK", <id>, <true|false>, <message>]` response
  as the per-event verdict. Bytes MUST NOT be re-serialised, so signatures
  remain bit-for-bit identical. Any of the following count as a per-event
  failure:
  - decode failure for the line (cannot extract `id`/`kind` for logging)
  - timeout
  - context cancellation
  - relay returns `OK … false …`
  - WebSocket transport error after connection
- **FR-055 (MUST)** A per-event failure MUST log at WARN with `pubkey`
  and the underlying error; `event_id` and `kind` MUST be included when
  the scan-output line decoded successfully, and MAY be omitted when the
  line itself failed to decode (in which case the WARN SHOULD include
  the byte length and a short hex prefix sufficient to triage). The
  failure MUST NOT abort the rest of phase 3 — the worker continues
  with the next scan line for the same pubkey on its WebSocket,
  reconnecting per §5.5 if the prior failure indicated a transport-level
  disconnect. The worker MUST NOT mark any never-acked event as success.
- **FR-056 (MUST)** Outside `--dry-run`, each worker MUST dial its
  WebSocket before popping its first pubkey from the queue. If the dial
  fails, the worker MUST log WARN and exit; remaining pubkeys are
  processed by surviving workers. After the worker pool joins, the
  orchestrator MUST add every pubkey still remaining in the queue to
  `pubkeys_forward_failed` (zero events attempted, per FR-069); this
  covers both the all-workers-failed-to-dial case and any tail of
  pubkeys that simply never got popped before the pool drained. Failed
  pubkeys are re-attempted on the next run. Under `--dry-run`, the dial
  step is skipped entirely (FR-052) and dial-failure cannot contribute
  to `pubkeys_forward_failed`.
- **FR-057 (MUST)** Phase 3 MUST track two disjoint outcomes per scan
  line: `events_forwarded` (a set of event ids — one per scan line that
  the relay acknowledged with `OK … true …`) and `events_forward_failed`
  (a count of scan lines that did not get `OK … true …` —
  `OK … false …`, timeout, scan-line decode failure, transport error,
  context cancellation). Decode-failure lines have no extractable id
  and contribute only to the count; for the other failure modes the
  implementation MAY retain the id for logging, but only the count is
  observable in the summary. Outside `--dry-run`, the invariant
  `|events_forwarded| + events_forward_failed = events_scanned` MUST
  hold. Under `--dry-run` no publish is attempted, so `events_forwarded`
  is empty and `events_forward_failed` is zero by FR-058 / FR-070 and
  the invariant does not apply in that mode. The names match the
  summary field names in FR-070.
- **FR-058 (MUST)** When `--dry-run` is set, the per-pubkey `strfry scan`
  invocations MUST still run (they're how `events_scanned` is counted),
  but no `["EVENT", …]` frame is sent and no WebSocket is opened to the
  main relay — workers MUST NOT dial under `--dry-run` (FR-052, FR-056).
  The only external traffic in dry-run is `docker exec` against the
  quarantine container. Report the cumulative scan-line total across all
  whitelisted pubkeys as `events_scanned`, with `events_forwarded=0` and
  `events_deleted=0`. (Outside `--dry-run`, `events_scanned` has the
  same definition — the cumulative scan-line total.)
- **FR-059 (MUST)** No event id may appear in both `events_forwarded` and
  `events_forward_failed`.
- **FR-068 (MUST)** A per-pubkey `strfry scan` that **fails to start**
  (e.g. `docker exec` cannot be spawned) MUST be logged at WARN with
  the pubkey and the failure context, and zero events are attempted for
  that pubkey. A scan that streams successfully but **exits non-zero
  after the worker has finished reading its stdout** MUST also be
  logged at WARN with the pubkey and the failure context, but is not
  by itself a forward failure — the determination of whether that
  pubkey lands in `pubkeys_forward_failed` is made by FR-069 from the
  per-event outcomes. In both cases the worker continues with the next
  pubkey; this MUST NOT abort phase 3.
- **FR-069 (MUST)** Membership in `pubkeys_forward_failed` is decided
  per pubkey at end-of-pubkey, from the per-event outcomes:
  - If at least one of the pubkey's scanned events landed in
    `events_forward_failed`, the pubkey MUST be added.
  - If zero events were attempted for the pubkey (worker dial failed
    before pop per FR-056, or the pubkey's scan failed to start per
    FR-068), the pubkey MUST be added.
  - Otherwise (every scanned event received `OK … true …`), the
    pubkey MUST NOT be added — even if its `strfry scan` exited
    non-zero after EOF. The scan-exit anomaly is still WARN-logged
    by FR-068.
  Under `--dry-run`, no events are attempted, so `pubkeys_forward_failed`
  is empty regardless of dial or scan outcomes (workers don't dial in
  dry-run per FR-052/FR-056, and counting "every event missing OK true"
  would otherwise mark every whitelisted pubkey).

### 3.7 Phase 4 — delete events from quarantine

- **FR-060 (MUST)** Delete only the ids in `events_forwarded` from phase 3.
- **FR-061 (MUST)** Delete by event id, never by author or filter that could
  match new events. Implementation: invoke
  `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> delete --filter <filter>`
  where `<filter>` is exactly `{"ids":["…","…",…]}`.
- **FR-062 (MUST)** Batch ids per delete invocation. Default batch size 500;
  configurable via `--batch-size`. Values ≤ 0 resolve to the default.
- **FR-063 (MUST)** On a non-zero exit from `strfry delete`:
  1. If the failed batch has size 1, record that single id as failed and
     continue with the next batch.
  2. Else if `current_batch_size / 2 < 8`, attempt each id individually;
     record per-id success/failure and continue.
  3. Else, halve the batch size and retry the failed batch with halved
     batches; recurse until rule 1 or 2 applies.
- **FR-064 (MUST)** Phase 4 MUST collect two disjoint sets of event ids:
  `events_deleted` (acknowledged by `strfry delete` exit 0) and
  `events_delete_failed` (everything that ultimately could not be
  deleted, including ids that failed at batch size 1 per FR-063). Their
  union MUST equal `events_forwarded`. The set names match the summary
  field names in FR-070.
- **FR-065 (MUST NOT)** Phase 4 MUST NOT run when `--dry-run` is set.
- **FR-066 (MUST NOT)** Phase 4 MUST NOT run when `events_forwarded` is empty.

### 3.8 Summary line

- **FR-070 (MUST)** At end of run (including early stops), emit exactly one
  INFO log record with message `cleaner summary` and the integer fields
  defined below. These names are also the canonical names for the
  in-memory sets/counters used in phases 1–4 (FR-057, FR-064); the
  prose, error matrix, and pseudocode all use them verbatim:
  - `pubkeys_discovered` = count of distinct pubkeys discovered in
    phase 1 (size of the `pubkey → bool` map built in FR-033, captured
    before the map is discarded in FR-045).
  - `pubkeys_whitelisted` = count of pubkeys whose phase-2 `/check`
    returned status `200` with body `{"whitelisted": true}`.
  - `pubkeys_forward_failed` = count of whitelisted pubkeys for which
    at least one event did not receive `OK … true …`, including the
    degenerate case of zero attempted events. Per FR-069: a pubkey is
    counted iff (a) any of its scanned events landed in
    `events_forward_failed`, or (b) zero events were attempted (no
    worker dialled per FR-056, or its scan failed to start per
    FR-068). A clean per-event run whose `strfry scan` happens to exit
    non-zero at EOF is WARN-logged (FR-068) but does not count here.
    Always 0 under `--dry-run` (FR-069).
  - `events_exported` = count of non-empty stdout lines read from
    `strfry export`, including lines skipped due to JSON parse error
    or missing `pubkey`. This is the exporter's raw throughput, not
    the count of successfully-discovered pubkeys.
  - `events_scanned` = cumulative scan-line total across all
    per-pubkey `strfry scan` invocations in phase 3 (FR-058). This
    field has the same definition under `--dry-run` and outside it,
    so it remains a non-zero count under `--dry-run` even though
    `events_forwarded` and `events_forward_failed` are both 0 in
    that mode. The union invariant
    `events_forwarded ∪ events_forward_failed = events_scanned`
    (FR-057) applies only outside `--dry-run`.
  - `events_forwarded` = count of event ids in the phase-3
    `events_forwarded` set (FR-057): events the main relay
    acknowledged with `OK … true …`. Always 0 under `--dry-run`.
  - `events_forward_failed` = phase-3 count (FR-057) of scan lines
    that did not receive `OK … true …` (relay rejection, timeout,
    scan-line decode failure, transport error, context cancellation).
    Always 0 under `--dry-run`.
  - `events_deleted` = count of event ids in the phase-4
    `events_deleted` set (FR-064): ids acknowledged by `strfry delete`
    exit 0. Always 0 under `--dry-run` and when `events_forwarded` is
    empty (FR-065, FR-066).
  - `events_delete_failed` = count of event ids in the phase-4
    `events_delete_failed` set (FR-064): ids that ultimately could
    not be deleted after the halve-and-retry of FR-063.
  - `duration_ms` = wall-clock milliseconds from process start to the
    moment this summary record is emitted.
- **FR-071 (SHOULD)** Phase boundaries SHOULD be logged at INFO with their
  inputs/outputs (e.g. `phase 1 complete pubkeys=… events=…`).

### 3.9 Idempotency and re-run safety

- **FR-080 (MUST)** Two consecutive runs against unchanged state must
  produce `events_forwarded=0` and `events_deleted=0` in the second run.
  StrFry dedupes by event id on publish and tolerates delete-of-missing,
  so the implementation only needs to faithfully follow phases 1–4.
- **FR-081 (MUST)** A run that crashes or is killed mid-phase 3 MUST NOT
  delete anything; on re-run, any successfully-forwarded events become a
  no-op publish but their deletion completes.

---

## 4. Non-Functional Requirements

- **NFR-001 (MUST)** Memory usage MUST scale with the number of distinct
  pubkeys only — `O(distinct pubkeys)` — and MUST NOT scale with the
  number of events in quarantine or with per-pubkey event counts.
  Phase 1 streams the export and retains only pubkeys (no event bytes,
  ids, kinds, or timestamps). Phase 3 streams `strfry scan` per pubkey
  and publishes one event at a time, so the in-flight footprint per
  worker is one event line. Total in-flight memory across the run is
  therefore `O(distinct_pubkeys + forward_concurrency × max_event_size)`.
- **NFR-002 (MUST)** No background goroutines, threads, or timers may
  outlive the `run` function. On exit, all spawned children, sockets, and
  files must be released.
- **NFR-003 (MUST)** No PII, no secret keys, and no full event content go
  into logs. Only ids, pubkeys, kinds, and counts.
- **NFR-004 (SHOULD)** Phase 3 SHOULD complete within `O(N · publish_timeout
  / forward_concurrency)` worst case. The implementation MUST NOT serialise
  phase 3 across all workers.
- **NFR-005 (MUST)** The deployable artefact for production is a single
  static Linux/amd64 binary (or equivalent self-contained bundle) that runs
  on the strfry host and shells out to `docker`. Container/runtime image
  size is unconstrained.
- **NFR-006 (MUST)** No write to `~/deepfry/` after the one-time bootstrap
  defined in FR-015. Bootstrap MAY create or update **only**
  `~/deepfry/whitelist.yaml` to persist a freshly-resolved `server_url`,
  preserving any other keys already present (FR-016). All other files
  under `~/deepfry/` are read-only at all times. After bootstrap, even
  `whitelist.yaml` is read-only for the remainder of the run and across
  all future runs.

---

## 5. Interfaces

### 5.1 CLI flags

| ID | Flag | Type | Default | Behaviour |
|---|---|---|---|---|
| IF-CLI-01 | `--dry-run` | bool | false | Skip publish + delete; phases 1–2 only. |
| IF-CLI-02 | `--limit N` | int | 0 | Stop export after N successfully-parsed event lines. 0 = unlimited; negative values clamp to 0 (FR-034). |
| IF-CLI-03 | `--batch-size N` | int | 500 | IDs per delete invocation. |
| IF-CLI-04 | `--forward-concurrency N` | int | 4 | Parallel pubkeys forwarded. |
| IF-CLI-05 | `--whitelist-concurrency N` | int | 8 | Parallel `/check` calls. |
| IF-CLI-06 | `--main-relay URL` | string | `ws://localhost:7777` | Main StrFry WebSocket. |
| IF-CLI-07 | `--quarantine-container NAME` | string | `strfry-quarantine` | Docker container. |
| IF-CLI-08 | `--quarantine-config PATH` | string | `/etc/strfry.conf` | Config inside container. |
| IF-CLI-09 | `--publish-timeout DUR` | duration | `5s` | Per-publish + dial timeout. |
| IF-CLI-10 | `--log-level LEVEL` | string | `debug` | One of `debug`, `info`, `warn`, `error`. Default is verbose because the tool runs on a slow cadence and a quiet failure is more expensive than noisy logs. |
| IF-CLI-11 | `--version` | bool | false | Print version info and exit. |

Unknown flags MUST cause a non-zero exit with usage text.

### 5.2 Config file

`~/deepfry/whitelist.yaml`, YAML, fields read:

| Key | Type | Default | Notes |
|---|---|---|---|
| `server_url` | string | *(no static default; resolved via FR-015)* | Whitelist server base URL. |
| `check_timeout` | duration | `2s` | Per-`/check` HTTP timeout. |

Other keys MUST be ignored. A missing file or missing `server_url` triggers
the FR-015 discovery chain rather than silent defaults. The file MAY be
written exactly once per host as part of FR-015's bootstrap, preserving any
other keys already present (FR-016).

### 5.3 Whitelist HTTP API (consumed)

| ID | Endpoint | Request | Success criterion | Used by |
|---|---|---|---|---|
| IF-HTTP-01 | `GET /health` | none | status `200` AND body `{"status":"ok"}` | preflight (FR-020) |
| IF-HTTP-02 | `GET /check/<pubkey>` | none | status `200` AND body `{"whitelisted": true}` | phase 2 (FR-040) |

Pubkey is hex (32-byte secp256k1 x-only), passed unmodified in the path.

### 5.4 External processes

| ID | Command | Stdin | Stdout | Stderr | Used by |
|---|---|---|---|---|---|
| IF-EXEC-01 | `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> export` | none | JSONL events | diagnostic | phase 1 (pubkey discovery) |
| IF-EXEC-02 | `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> delete --filter <filter>` | none | (ignored) | error context on non-zero exit | phase 4 (deleter) |
| IF-EXEC-03 | `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> scan --reverse '{"authors":["<pubkey>"]}'` | none | JSONL events oldest-first | diagnostic | phase 3 (forwarder) |

`<quarantine-container>` and `<quarantine-config>` are the values of the
`--quarantine-container` / `--quarantine-config` CLI flags. A `docker` binary
MUST be on `PATH` and the invoking user MUST have permission to exec into
`<quarantine-container>`.

### 5.5 Main relay WebSocket

NIP-01. Client → relay: `["EVENT", <signed-event-json>]`. Relay → client:
`["OK", <event-id>, <bool>, <message>]`. The implementation MUST:

- treat `OK true` as success.
- treat `OK false` as a per-event rejection (failure, no abort).
- treat connection close before `OK` arrives within the publish timeout as
  failure for that event. The worker MUST attempt to redial before sending
  the next event for the same pubkey, using the same `--publish-timeout`
  as the dial cap; a redial failure is logged and the worker exits
  (surviving workers continue to drain the queue). The tool MUST NOT
  silently mark a never-ack'd event as success.

Under `--dry-run`, the main relay is never contacted: no dial, no
`["EVENT", …]` send, no `["OK", …]` wait, no redial (FR-052, FR-056,
FR-058). Phase 3's only external interaction in dry-run is `docker exec`
against the quarantine container.

### 5.6 Environment probe (process env + deepfry repo `.env`)

Consulted during FR-015 steps 1 and 2. Read-only and best-effort.

**Convention.** `DGRAPH_HOST` is the canonical dgraph-host variable.
The deepfry repo's `.env.example` declares it; operators set it (typically
the host running the dgraph + whitelist-server pair) so FR-015 step 1 or
step 2 can resolve.

| Aspect | Behaviour |
|---|---|
| Path resolution | `$DEEPFRY_ROOT/.env` if `$DEEPFRY_ROOT` is set; otherwise walk up from the current working directory until a `.env` is found alongside `docker-compose.strfry.yml`. |
| Parser | Standard `KEY=VALUE` `.env` syntax: one assignment per line, optional surrounding quotes on the value, `#`-prefixed comments and blank lines ignored. |
| Variable lookup | `DGRAPH_HOST`. No fallback list of alternative names. |
| Derivation rule | Whitelist server is co-located with dgraph in the standard compose layout, so `server_url = http://<host>:8081`. |
| Failure modes | Missing file, parse error, unset variable: NOT fatal. Falls through to the FR-015 TTY prompt step. |
| Side effects | None on `.env`. The probe MUST NOT write to or modify `.env`. |

---

## 6. Error Handling Matrix

| ID | Condition | Behaviour |
|---|---|---|
| ER-01 | Whitelist YAML invalid | Exit non-zero before any other action. |
| ER-02 | Whitelist YAML missing or `server_url` unset | Run FR-015 discovery chain (process env → `.env` probe → TTY prompt → persist to `~/deepfry/whitelist.yaml`). Exit non-zero only if all three discovery steps yield nothing (no process-env hint, no `.env` hint, non-TTY stdin). |
| ER-15 | FR-015 bootstrap write to `~/deepfry/whitelist.yaml` fails | WARN with the path and OS error; exit non-zero before any `docker exec`, HTTP call, or WebSocket dial. |
| ER-03 | Whitelist preflight unhealthy: anything other than status `200` with body exactly `{"status":"ok"}` (including `503` `{"status":"loading", ...}`, transport error, timeout, JSON decode error, mismatched `status`) | Exit non-zero with no exec, no publish, no delete. |
| ER-04 | `docker exec … export` fails to start | Phase 1 error → exit non-zero. |
| ER-05 | `strfry export` exits non-zero after streaming | Phase 1 error → exit non-zero. (No partial forwarding from this run.) |
| ER-06 | Single export line malformed | WARN; skip line; continue. |
| ER-07 | Single export line missing `pubkey` | WARN; skip line; continue. |
| ER-08 | `/check/<pubkey>` does not return status `200` with body containing `{"whitelisted": true}` (transport error, non-200, decode error, timeout, missing `whitelisted` field, `whitelisted` not the JSON literal `true`) | WARN; treat pubkey as not-whitelisted; continue. |
| ER-09 | Worker fails to dial main relay | WARN; the worker exits without popping any pubkey; surviving workers drain the queue. If all workers fail to dial, every whitelisted pubkey ends up with zero attempted events and is therefore reported under `pubkeys_forward_failed` per FR-069. |
| ER-10 | Per-event publish fails (any cause enumerated in FR-054) | WARN with `event_id`, `pubkey`, `kind`; mark id failed; continue with next scan line for the same pubkey. |
| ER-11 | `strfry delete` batch fails | Halve-and-retry per FR-063; ultimately mark failed ids and continue. |
| ER-12 | SIGINT/SIGTERM mid-run | Cancel in-flight ops; abort run; exit non-zero. Do not delete events whose forward never confirmed. |
| ER-13 | `--limit` reached during export | Stop reading, terminate the exporter, wait for it, and treat its non-zero exit as expected. Proceed with the pubkeys discovered so far. |
| ER-14a | Per-pubkey `strfry scan` fails to start | WARN with the pubkey; zero events attempted for this pubkey, so it lands in `pubkeys_forward_failed` per FR-069; worker proceeds to the next pubkey; phase 3 does not abort. |
| ER-14b | Per-pubkey `strfry scan` exits non-zero after the worker has finished reading its stdout | WARN with the pubkey; whether the pubkey lands in `pubkeys_forward_failed` is determined by FR-069 from the per-event outcomes (clean per-event run → not counted; any per-event failure → counted); worker proceeds to the next pubkey; phase 3 does not abort. |

---

## 7. Acceptance Criteria

End-to-end criteria for accepting a port. Run on a clean stack with both
strfry compose files up.

- **AC-01** Seed: pubkey `A` whitelisted in Dgraph, refreshed; pubkey `B`
  not. `curl /check/A → true`, `curl /check/B → false`.
- **AC-02** Inject 3× kind-1 from `A` and 3× kind-1 from `B` into quarantine.
  `strfry … scan '{}'` reports 6 events.
- **AC-03** `quarantine-cleaner --dry-run` reports
  `pubkeys_whitelisted=1`, `events_scanned=3`, `events_forwarded=0`,
  `events_deleted=0`. Quarantine count remains 6.
- **AC-04** `quarantine-cleaner` reports `events_forwarded=3`,
  `events_deleted=3`, `events_forward_failed=0`.
- **AC-05** Main relay scan for `authors=[A]` returns 3.
- **AC-06** Quarantine scan for `authors=[A]` returns 0; `authors=[B]`
  returns 3.
- **AC-07** Idempotency: re-running yields `events_forwarded=0`,
  `events_deleted=0`.
- **AC-08** Reject-path: with `B` whitelist-server-positive but main relay
  still rejecting, the cleaner logs per-event rejections for `B`'s events
  and `events_deleted` does not include any of `B`'s ids.
- **AC-09** Replaceable-kind ordering: two kind-0 events from `A` injected
  100s apart (oldest first) into quarantine. After rescue, the main relay's
  `{"authors":[A], "kinds":[0]}` returns the **newer** profile (proves
  oldest-first sequential publishing is honoured per pubkey).
- **AC-10** Whitelist server down: rescue exits non-zero immediately,
  `strfry export` is never invoked.
- **AC-11** Malformed lines: synthesise an export with a non-JSON line
  followed by valid lines; the run completes and processes the valid lines
  only. (Use a fake `runner` in unit tests, per the existing exporter
  tests.)
- **AC-12** Poison id: cause `strfry delete` to fail for one specific id.
  All other ids are still deleted; that one id is reported under
  `events_delete_failed`.
- **AC-13** First-run bootstrap, TTY: with no `~/deepfry/whitelist.yaml`,
  no process-env hint, and no `.env` hint available, an interactive run
  prompts for the whitelist server URL, persists it to
  `~/deepfry/whitelist.yaml`, and proceeds normally. A non-interactive
  run with the same starting state exits non-zero before any
  `docker exec`, HTTP call, or WebSocket dial.
- **AC-14** First-run bootstrap, `.env` hint: with `.env` containing a
  dgraph-host variable (e.g. `DGRAPH_HOST=10.0.0.5`), no process-env
  hint set, and no `~/deepfry/whitelist.yaml`, the run resolves
  `server_url=http://10.0.0.5:8081` without prompting and persists it
  to the config file. A subsequent run reads the file directly and
  performs no probe and no prompt.
- **AC-15** First-run bootstrap, process env: with no
  `~/deepfry/whitelist.yaml` and a dgraph-host variable exported in the
  process environment (e.g. `DGRAPH_HOST=10.0.0.5`), the run resolves
  `server_url=http://10.0.0.5:8081` without consulting `.env` or
  prompting, persists it to the config file, and proceeds normally.
- **AC-16** Memory bound: with 1M kind-1 events from 100 distinct
  pubkeys injected into quarantine, the cleaner completes successfully
  on a host whose memory limits are set well below the total event
  payload size — confirmation that nothing in phase 1 or phase 3
  buffers all events at once (per NFR-001).

---

## 8. Pseudocode Sketches

These are language-agnostic sketches of the load-bearing algorithms. They
exist to disambiguate the prose, not to prescribe an implementation
shape — any structurally equivalent rendering is acceptable.

### 8.1 Discovery chain (FR-015)

```
function resolveServerURL(yamlPath, deepfryRoot, cwd, env, stdin):
  # step 1: process env
  host = env.get("DGRAPH_HOST")
  if host is non-empty:
    return ("http://" + host + ":8081", source="env")

  # step 2: repo .env
  envPath = locateRepoEnv(deepfryRoot, cwd)        # walks up to docker-compose.strfry.yml
  if envPath exists and parseable:
    host = parseDotenv(envPath).get("DGRAPH_HOST")
    if host is non-empty:
      return ("http://" + host + ":8081", source="envfile")

  # step 3: TTY prompt
  if stdin.isTTY():
    print("Enter whitelist server URL (will be saved to " + yamlPath + "):")
    url = readLine(stdin).trim()
    if url is non-empty:
      return (url, source="prompt")

  # step 4: fail
  exit(non-zero, "no server_url; set " + yamlPath + " or DGRAPH_HOST")
```

### 8.2 Halve-and-retry delete (FR-063)

```
function deleteBatch(ids):
  if execStrfryDelete(ids).exitCode == 0:
    record all ids as deleted
    return

  if len(ids) == 1:
    record ids[0] as failed
    return

  if len(ids) / 2 < 8:
    for id in ids:
      if execStrfryDelete([id]).exitCode == 0: record deleted
      else:                                       record failed
    return

  # else: split and recurse
  mid = len(ids) / 2
  deleteBatch(ids[:mid])
  deleteBatch(ids[mid:])
```

### 8.3 Phase 3 worker (FR-050..FR-058, FR-068, FR-069)

```
# publishTimeout caps both the initial dial and each per-event ack (FR-053).
function forwardWorker(queue, mainRelayURL, publishTimeout, dryRun):
  ws = null
  if not dryRun:                                       # no main-relay dial under --dry-run (FR-052/FR-056)
    ws = dial(mainRelayURL, timeout=publishTimeout)
    if ws is null:
      WARN("dial failed"); return                      # orchestrator drains any
                                                       # undrained pubkeys after
                                                       # the pool joins (FR-056).

  while pubkey = queue.pop():
    attempted = 0                                      # per-pubkey counters drive FR-069
    failed    = 0
    scan = spawn("docker exec ... strfry scan --reverse '{\"authors\":[\"" + pubkey + "\"]}'")
    if scan.startError != nil:
      WARN(pubkey, "scan failed to start")
      pubkeys_forward_failed.add(pubkey)              # zero attempted events (FR-069)
      continue

    for line in scan.stdout:
      events_scanned += 1                              # cumulative scan-line total (FR-058)
      if dryRun:
        continue                                       # no dial, no send, no recv
      if ws is null:                                   # prior failure dropped the socket
        ws = dial(mainRelayURL, timeout=publishTimeout)
        if ws is null:
          events_forward_failed += 1                   # un-sent line counts as failed (FR-057)
          failed += 1
          WARN(pubkey, "redial failed")
          if attempted == 0 or failed > 0:             # FR-069 verdict before exit
            pubkeys_forward_failed.add(pubkey)
          return                                        # worker exits per §5.5
      attempted += 1
      send(ws, ["EVENT", line])                        # verbatim bytes (FR-054)
      ack = recv(ws, timeout=publishTimeout)            # ["OK", id, bool, msg]
      if ack.ok == true:
        events_forwarded.add(ack.id)
      else:                                            # any cause in FR-054
        events_forward_failed += 1                     # FR-057: count; best-effort id in WARN
        failed += 1
        if ack.transportDropped:
          ws.close(); ws = null                        # next iteration redials per §5.5

    if scan.exitCode != 0:
      WARN(pubkey, "scan exited non-zero")             # WARN only (FR-068);
                                                       # FR-069 below decides the
                                                       # pubkeys_forward_failed verdict.

    if not dryRun and (attempted == 0 or failed > 0):  # FR-069
      pubkeys_forward_failed.add(pubkey)

  if ws is not null: ws.close()

# After the worker pool joins (orchestrator, FR-056):
if not dryRun:
  for pubkey in queue.remaining():                     # zero events attempted
    pubkeys_forward_failed.add(pubkey)
```

The set names above (`events_forwarded`, `events_forward_failed`,
`pubkeys_forward_failed`) match the summary field names in FR-070, so
phase-3 results map directly into the cleaner summary without renaming.

---

## 9. Open Questions

Decisions an implementation should make deliberately and document.

1. **Result aggregation shape**: the summary counters are what matter;
   any internal channel/queue/buffer scheme that produces correct counts
   is acceptable. Bounded result containers sized to the pubkey count are
   simplest; streaming aggregation is also fine.
2. **Strfry binary path**: hard-coded to `/app/strfry` inside the
   container. If a target deployment uses a different image with a
   different path, a future flag MAY be added; this is not currently in
   scope.
3. **Pubkey hex normalisation**: pubkeys from the export are passed
   verbatim to `/check/<pubkey>` and embedded verbatim into the
   `strfry scan` author filter. The implementation MUST NOT lowercase or
   validate. Pubkeys carried into `scan` MUST be JSON-encoded as a
   string in the filter; treat the pubkey as opaque hex and do not
   attempt re-validation. (Implication: if a malformed pubkey somehow
   enters phase 1, scan will simply return no events, and the worker
   will move on.)
4. **Race between phase 1 and phase 3**: events for a whitelisted pubkey
   that arrive in quarantine *after* phase 1 will still be picked up by
   phase 3's `scan` (since scan reads live LMDB at run time). This is
   desirable — it means a slow run rescues newer events too — but it
   also means `events_scanned` may exceed phase-1 expectations. An
   implementation MAY constrain phase 3 with `--until=<phase1_ts>` if
   exact-count determinism matters, but this is not currently required.
5. **`strfry scan --reverse` ordering guarantee**: the implementation
   relies on `strfry scan --reverse` emitting events in ascending
   `created_at` order (oldest-first). If the deployed strfry version
   changes the meaning of `--reverse` or stops guaranteeing total order
   on `created_at`, FR-050 is violated. Verify against the running
   strfry's documented scan ordering before deployment.
6. **`.env` variable name for the dgraph-host hint** (FR-015 steps 1–2):
   **Resolved.** `DGRAPH_HOST` is the canonical name; the deepfry repo's
   `.env.example` declares it. The cleaner reads exactly that key — no
   fallback list of alternative names.
7. **`.env` path resolution** (FR-015 step 2): prefer `$DEEPFRY_ROOT` if
   set, otherwise walk up from the working directory until a `.env` is
   found alongside `docker-compose.strfry.yml`. The cleaner runs on the
   strfry host, so `docker-compose.strfry.yml` is the marker that's
   reliably present and serves as the repo-root anchor. The same
   resolution should be shared with any other `.env`-driven path probe
   in the tool, so a single computation site governs all of them.

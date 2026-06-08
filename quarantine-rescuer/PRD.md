# quarantine-rescuer — PRD / Reimplementation Spec

Authoritative behavioural specification for re-implementing `quarantine-rescuer`
in a non-Go language. Every requirement is assigned a stable ID
(`FR-*`, `NFR-*`, `IF-*`, `ER-*`, `AC-*`). The traceability matrix at the end
maps each ID to the file and lines in the current Go reference implementation,
so a port can be verified line-by-line against the source of truth.

A second-language port MUST satisfy every `MUST`/`MUST NOT` requirement.
`SHOULD` requirements MAY be relaxed when the target language or runtime makes
a literal port awkward, provided observable behaviour matches.

---

## 1. Overview

### 1.1 Purpose

`quarantine-rescuer` is a one-shot host-side CLI that moves Nostr events from a
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
5. Stay configuration-coherent with the live whitelist plugin: the rescuer's
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
│  (docker container)     │◄──────────────►│  rescuer CLI │────────────►│  main StrFry relay  │
│  /app/strfry            │                │              │  NIP-01     │  ws://…:7777        │
│   --config=/etc/        │  exec/stdin    │              │             └─────────────────────┘
│     strfry.conf         │◄──────────────►│              │
│   export | delete       │                └──────────────┘
└─────────────────────────┘
```

**Actors / dependencies** the port MUST be able to drive:

- `docker exec <container> /app/strfry --config=<path> export`
  (streams JSONL on stdout)
- `docker exec <container> /app/strfry --config=<path> delete --filter <json>`
  (one-shot, returns non-zero on failure)
- HTTP `GET <whitelist_server>/health` and `GET <whitelist_server>/check/<pubkey>`
- Outgoing WebSocket connection speaking NIP-01 to a StrFry relay.
- A YAML reader for `~/deepfry/whitelist.yaml`.

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

### 3.2 Configuration loading

- **FR-010 (MUST)** Read `~/deepfry/whitelist.yaml` and consume two keys:
  `server_url` (string) and `check_timeout` (Go-style duration string,
  e.g. `2s`, `500ms`). All other keys MUST be ignored, not rejected.
- **FR-011 (MUST)** When the file is missing, fall back silently to defaults.
  An invalid file (parse error, etc.) MUST cause a non-zero exit.
- **FR-012 (MUST)** Defaults: `server_url=http://localhost:8081`,
  `check_timeout=2s`.
- **FR-013 (MUST)** The file MUST NOT be auto-created or auto-rewritten.
- **FR-014 (SHOULD)** Log the resolved `server_url` and `check_timeout` once
  at INFO at startup.

### 3.3 Whitelist server preflight

- **FR-020 (MUST)** Before any export, exec, publish, or delete, perform a
  `GET /health` on the whitelist server with a 5-second timeout. Any non-2xx
  status or transport error MUST cause a non-zero exit with no other side
  effects.
- **FR-021 (MUST NOT)** Skip preflight even when `--dry-run` is set. The point
  of preflight is to refuse to mass-skip due to a server outage.

### 3.4 Phase 1 — export from quarantine

- **FR-030 (MUST)** Stream events by spawning
  `docker exec <quarantine-container> /app/strfry --config=<quarantine-config> export`
  and reading stdout line-by-line as JSONL.
- **FR-031 (MUST)** For each non-empty stdout line, parse the minimal subset
  `{id, pubkey, kind, created_at}`. Lines that fail JSON parse, or whose `id`
  or `pubkey` is empty, MUST be logged at WARN and skipped — they MUST NOT
  abort the run.
- **FR-032 (MUST)** Retain the verbatim line bytes for re-publish; do not
  re-serialise. The forwarder must publish exactly the bytes the relay
  emitted, so signatures are preserved bit-for-bit.
- **FR-033 (MUST)** Group events by `pubkey` in memory: `pubkey → [events]`.
- **FR-034 (MUST)** Honour `--limit N`: when `N>0`, stop reading after the
  first `N` events are accepted (skipped lines do not count). The export
  process MUST then be terminated and waited on. Partial groupings are valid
  and proceed to phase 2.
- **FR-035 (MUST)** A non-zero exit from `strfry export` (after EOF) or a
  scanner error MUST surface as a phase-1 error and abort the run.
- **FR-036 (MUST)** The JSONL line scanner MUST tolerate lines up to at least
  1 MiB. Smaller hard buffer caps in the target language are not acceptable.
- **FR-037 (MUST NOT)** The tool MUST NOT open the quarantine LMDB, lock
  file, or any file under the quarantine container's data directory directly.

### 3.5 Phase 2 — whitelist filter

- **FR-040 (MUST)** For each unique pubkey collected in phase 1, call
  `GET <server_url>/check/<pubkey>` once and parse the JSON body
  `{"whitelisted": <bool>}`.
- **FR-041 (MUST)** Treat any of the following as "not whitelisted"
  (fail-closed) and continue with the rest of the run:
  request build error, transport error, non-200 status, JSON decode error.
  The error MUST be logged at WARN with the offending pubkey.
- **FR-042 (MUST)** Run checks concurrently with bounded parallelism. Default
  parallelism = 8; configurable via `--whitelist-concurrency`. Values ≤ 0
  resolve to the default.
- **FR-043 (SHOULD)** Reuse outbound HTTP connections (pooled keep-alive)
  with a per-host idle/connection cap of at least 32. This prevents
  ephemeral-port exhaustion (`EADDRNOTAVAIL`) under burst.
- **FR-044 (MUST)** The HTTP timeout for each `/check` request equals the
  configured `check_timeout`.
- **FR-045 (MUST)** After phase 2, retain only `pubkey → [events]` for
  pubkeys whose check returned `whitelisted=true`. All other groups MUST be
  dropped from memory (not deleted from quarantine, not forwarded).

### 3.6 Phase 3 — forward to main relay

- **FR-050 (MUST)** For each surviving pubkey, sort that pubkey's events by
  `created_at` ascending (stable sort) and publish them **sequentially in
  oldest-first order** on a single connection. Rationale: replaceable kinds
  (kind 0, 3, 10000+) and parameterised replaceable kinds (30000+) are
  last-write-wins on the relay; out-of-order publish lets an older copy
  clobber a newer one.
- **FR-051 (MUST)** Different pubkeys MAY be processed in parallel. The
  worker pool size defaults to 4 and is configurable via
  `--forward-concurrency`. Values ≤ 0 resolve to the default.
- **FR-052 (MUST)** Each worker MUST hold its own dedicated WebSocket
  connection to the main relay for the duration of phase 3.
- **FR-053 (MUST)** Per-event publish has a timeout equal to
  `--publish-timeout` (default 5s). Same value caps the initial
  WebSocket dial.
- **FR-054 (MUST)** Speak NIP-01: send `["EVENT", <event-json>]` on the
  WebSocket and treat the relay's `["OK", <id>, <true|false>, <message>]`
  response as the per-event verdict. Any of the following count as a
  per-event failure:
  - decode failure for the stored raw bytes (cannot construct a valid event)
  - timeout
  - context cancellation
  - relay returns `OK … false …`
  - WebSocket transport error after connection
- **FR-055 (MUST)** A per-event failure MUST log at WARN with `event_id`,
  `pubkey`, `kind`, and the underlying error and MUST NOT abort the rest of
  phase 3.
- **FR-056 (MUST)** If a worker fails to establish its initial connection,
  every event the worker is asked to handle MUST be reported as failed.
  Other workers MUST continue normally.
- **FR-057 (MUST)** Phase 3 MUST collect two disjoint sets:
  `success_ids` (events the relay acknowledged with `OK … true …`)
  and `failed_ids` (everything else). Their union equals the input.
- **FR-058 (MUST)** When `--dry-run` is set, skip phase 3 entirely. Report
  `events_to_forward` from phase 2 but `events_forwarded=0` and
  `events_deleted=0`.
- **FR-059 (MUST)** No event id may appear in both `success_ids` and
  `failed_ids`.

### 3.7 Phase 4 — delete from quarantine

- **FR-060 (MUST)** Delete only `success_ids` from phase 3.
- **FR-061 (MUST)** Delete by event id, never by author or filter that could
  match new events. Implementation: invoke
  `docker exec <container> /app/strfry --config=<config> delete --filter <json>`
  where `<json>` is exactly `{"ids":["…","…",…]}`.
- **FR-062 (MUST)** Batch ids per delete invocation. Default batch size 500;
  configurable via `--batch-size`. Values ≤ 0 resolve to the default.
- **FR-063 (MUST)** On a non-zero exit from `strfry delete`:
  1. If the failed batch has size 1, record that single id as failed and
     continue with the next batch.
  2. Else if `current_batch_size / 2 < 8`, attempt each id individually;
     record per-id success/failure and continue.
  3. Else, halve the batch size and retry the failed batch with halved
     batches; recurse until rule 1 or 2 applies.
- **FR-064 (MUST)** Result reporting: `deleted_ids` (acknowledged by
  `strfry delete` exit 0) and `failed_ids` (everything that ultimately
  could not be deleted).
- **FR-065 (MUST NOT)** Phase 4 MUST NOT run when `--dry-run` is set.
- **FR-066 (MUST NOT)** Phase 4 MUST NOT run when `success_ids` is empty.

### 3.8 Summary line

- **FR-070 (MUST)** At end of run (including early stops), emit exactly one
  INFO log record with message `rescue summary` and these integer fields:
  `pubkeys_seen`, `pubkeys_whitelisted`, `events_exported`,
  `events_to_forward`, `events_forwarded`, `events_failed_forward`,
  `events_deleted`, `events_failed_delete`, `duration_ms`.
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

- **NFR-001 (MUST)** Memory usage scales with the number of distinct
  pubkeys × average events per pubkey, since groups are buffered in memory.
  The implementation MUST stream phase 1 (do not buffer all stdout in
  memory before grouping); this is what enables `--limit` to short-circuit.
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
- **NFR-006 (MUST)** No write to `~/deepfry/` or any path under it. The
  rescuer is read-only with respect to deepfry config.

---

## 5. Interfaces

### 5.1 CLI flags

| ID | Flag | Type | Default | Behaviour |
|---|---|---|---|---|
| IF-CLI-01 | `--dry-run` | bool | false | Skip publish + delete; phases 1–2 only. |
| IF-CLI-02 | `--limit N` | int | 0 | Stop export after N events. 0 = unlimited. |
| IF-CLI-03 | `--batch-size N` | int | 500 | IDs per delete invocation. |
| IF-CLI-04 | `--forward-concurrency N` | int | 4 | Parallel pubkeys forwarded. |
| IF-CLI-05 | `--whitelist-concurrency N` | int | 8 | Parallel `/check` calls. |
| IF-CLI-06 | `--main-relay URL` | string | `ws://localhost:7777` | Main StrFry WebSocket. |
| IF-CLI-07 | `--quarantine-container NAME` | string | `strfry-quarantine` | Docker container. |
| IF-CLI-08 | `--quarantine-config PATH` | string | `/etc/strfry.conf` | Config inside container. |
| IF-CLI-09 | `--publish-timeout DUR` | duration | `5s` | Per-publish + dial timeout. |
| IF-CLI-10 | `--log-level LEVEL` | string | `info` | One of `debug`, `info`, `warn`, `error`. |
| IF-CLI-11 | `--version` | bool | false | Print version info and exit. |

Unknown flags MUST cause a non-zero exit with usage text.

### 5.2 Config file

`~/deepfry/whitelist.yaml`, YAML, fields read:

| Key | Type | Default | Notes |
|---|---|---|---|
| `server_url` | string | `http://localhost:8081` | Whitelist server base URL. |
| `check_timeout` | duration | `2s` | Per-`/check` HTTP timeout. |

Other keys MUST be ignored. Missing file MUST be silent.

### 5.3 Whitelist HTTP API (consumed)

| ID | Endpoint | Request | 2xx response | Used by |
|---|---|---|---|---|
| IF-HTTP-01 | `GET /health` | none | any 2xx body, status 200 | preflight (FR-020) |
| IF-HTTP-02 | `GET /check/<pubkey>` | none | `{"whitelisted": <bool>}` | phase 2 (FR-040) |

Pubkey is hex (32-byte secp256k1 x-only), passed unmodified in the path.

### 5.4 External processes

| ID | Command | Stdin | Stdout | Stderr | Used by |
|---|---|---|---|---|---|
| IF-EXEC-01 | `docker exec <C> /app/strfry --config=<P> export` | none | JSONL events | diagnostic | exporter |
| IF-EXEC-02 | `docker exec <C> /app/strfry --config=<P> delete --filter <JSON>` | none | (ignored) | error context on non-zero exit | deleter |

`<C>` and `<P>` come from `--quarantine-container` / `--quarantine-config`.
A `docker` binary MUST be on `PATH` and the invoking user MUST have permission
to exec into `<C>`.

### 5.5 Main relay WebSocket

NIP-01. Client → relay: `["EVENT", <signed-event-json>]`. Relay → client:
`["OK", <event-id>, <bool>, <message>]`. The implementation MUST:

- treat `OK true` as success.
- treat `OK false` as a per-event rejection (failure, no abort).
- treat connection close before `OK` arrives within the publish timeout as
  failure for that event and (if practical) reconnect for subsequent
  events on that worker. (A reconnect strategy is `SHOULD`, not `MUST`,
  but the tool MUST NOT silently mark a never-ack'd event as success.)

---

## 6. Error Handling Matrix

| ID | Condition | Behaviour |
|---|---|---|
| ER-01 | Whitelist YAML invalid | Exit non-zero before any other action. |
| ER-02 | Whitelist YAML missing | Use defaults; continue. |
| ER-03 | Whitelist server unreachable at preflight | Exit non-zero with no exec, no publish, no delete. |
| ER-04 | `docker exec … export` fails to start | Phase 1 error → exit non-zero. |
| ER-05 | `strfry export` exits non-zero after streaming | Phase 1 error → exit non-zero. (No partial forwarding from this run.) |
| ER-06 | Single export line malformed | WARN; skip line; continue. |
| ER-07 | Single export line missing `id`/`pubkey` | WARN; skip line; continue. |
| ER-08 | `/check/<pubkey>` transport/non-200/decode error | WARN; treat pubkey as not-whitelisted; continue. |
| ER-09 | Worker fails to dial main relay | WARN; mark all events the worker would handle as failed; other workers continue. |
| ER-10 | Per-event publish fails (timeout, OK=false, transport) | WARN with `event_id`, `pubkey`, `kind`; mark id failed; continue. |
| ER-11 | `strfry delete` batch fails | Halve-and-retry per FR-063; ultimately mark failed ids and continue. |
| ER-12 | SIGINT/SIGTERM mid-run | Cancel in-flight ops; abort run; exit non-zero. Do not delete events whose forward never confirmed. |
| ER-13 | `--limit` reached during export | Stop reading, terminate exporter, proceed with what was collected. |

---

## 7. Acceptance Criteria

End-to-end criteria for accepting a port. Run on a clean stack with both
strfry compose files up.

- **AC-01** Seed: pubkey `A` whitelisted in Dgraph, refreshed; pubkey `B`
  not. `curl /check/A → true`, `curl /check/B → false`.
- **AC-02** Inject 3× kind-1 from `A` and 3× kind-1 from `B` into quarantine.
  `strfry … scan '{}'` reports 6 events.
- **AC-03** `quarantine-rescue --dry-run` reports
  `pubkeys_whitelisted=1`, `events_to_forward=3`, `events_forwarded=0`,
  `events_deleted=0`. Quarantine count remains 6.
- **AC-04** `quarantine-rescue` reports `events_forwarded=3`,
  `events_deleted=3`, `events_failed_forward=0`.
- **AC-05** Main relay scan for `authors=[A]` returns 3.
- **AC-06** Quarantine scan for `authors=[A]` returns 0; `authors=[B]`
  returns 3.
- **AC-07** Idempotency: re-running yields `events_forwarded=0`,
  `events_deleted=0`.
- **AC-08** Reject-path: with `B` whitelist-server-positive but main relay
  still rejecting, the rescuer logs per-event rejections for `B`'s events
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
  `events_failed_delete`.

---

## 8. Reference Implementation Inventory

The current Go reference is in this repository. Use this list to find the
canonical behaviour for any ambiguity.

```
cmd/quarantine-rescue/main.go     # CLI entrypoint, flag parsing, orchestration
internal/whitelist/config.go      # ~/deepfry/whitelist.yaml loader
internal/whitelist/client.go      # /health + /check HTTP client
internal/exporter/exporter.go     # docker exec strfry export streamer
internal/forwarder/forwarder.go   # NIP-01 WebSocket publisher (oldest-first per pubkey)
internal/deleter/deleter.go       # docker exec strfry delete with halve-and-retry
internal/runner/runner.go         # os/exec abstraction (Stream + Output)
internal/event/event.go           # shared RawEvent type
```


Key third-party Go libs the port can substitute with idiomatic equivalents:

| Go lib | Role | Equivalent guidance |
|---|---|---|
| `github.com/nbd-wtf/go-nostr` | NIP-01 WebSocket client + event JSON | Any maintained Nostr client lib; or hand-roll NIP-01 over a vetted WS lib (`["EVENT", …]` / `["OK", …]`). MUST verify `OK true` for success. |
| `github.com/spf13/viper` | YAML config | Any YAML lib + manual default merge. |
| `log/slog` | structured JSON logs | Native structured-log lib; output JSON to stderr. |

---

## 9. Traceability Matrix

Every requirement maps to one or more authoritative source locations. Use
the line ranges as the diff target when verifying a port.

| Requirement | Source (file : lines) |
|---|---|
| FR-001, FR-070 | `cmd/quarantine-rescue/main.go:113-193`, `:252-264` |
| FR-002 | `cmd/quarantine-rescue/main.go:62`, `:84-87` |
| FR-003 | `cmd/quarantine-rescue/main.go:95-98` |
| FR-004 | `cmd/quarantine-rescue/main.go:92-93`; ctx propagation throughout `run` |
| FR-005, FR-014 | `cmd/quarantine-rescue/main.go:67-80`, `:120` |
| FR-010..FR-013 | `internal/whitelist/config.go:24-51` |
| FR-020, FR-021, ER-03 | `cmd/quarantine-rescue/main.go:122-128`; `internal/whitelist/client.go:48-62` |
| FR-030, IF-EXEC-01 | `internal/exporter/exporter.go:55-69`, `:62-63` |
| FR-031, ER-06, ER-07 | `internal/exporter/exporter.go:80-98`; tests `internal/exporter/exporter_test.go:57-77` |
| FR-032 | `internal/exporter/exporter.go:30-46`, `:86-101` |
| FR-033 | `cmd/quarantine-rescue/main.go:195-211` |
| FR-034, ER-13 | `cmd/quarantine-rescue/main.go:200-205` |
| FR-035, ER-04, ER-05 | `internal/exporter/exporter.go:64-69`, `:108-116`; `internal/runner/runner.go:30-48` |
| FR-036 | `internal/exporter/exporter.go:25`, `:77` |
| FR-037 | exporter is exec-only — no LMDB code path imported by `cmd/quarantine-rescue/main.go` |
| FR-040, IF-HTTP-02 | `internal/whitelist/client.go:64-91` |
| FR-041, ER-08 | `internal/whitelist/client.go:66-90`; tests `internal/whitelist/client_test.go:54-95` |
| FR-042 | `cmd/quarantine-rescue/main.go:213-250`, esp. `:215-216`, `:227-238` |
| FR-043 | `internal/whitelist/client.go:28-40` |
| FR-044 | `internal/whitelist/client.go:38` |
| FR-045 | `cmd/quarantine-rescue/main.go:242-249` |
| FR-050 | `internal/forwarder/forwarder.go:142-154` (sort + sequential per-pubkey) |
| FR-051 | `internal/forwarder/forwarder.go:42-58`, `:133-140` |
| FR-052 | `internal/forwarder/forwarder.go:89-101` (worker establishes its own relay, defers close) |
| FR-053 | `internal/forwarder/forwarder.go:42-58`, `:118-120`, `:161-165` |
| FR-054, FR-055 | `internal/forwarder/forwarder.go:103-129` |
| FR-056, ER-09 | `internal/forwarder/forwarder.go:90-100` |
| FR-057, FR-059 | `internal/forwarder/forwarder.go:78-87`, `:158` |
| FR-058 | `cmd/quarantine-rescue/main.go:164-168` |
| FR-060 | `cmd/quarantine-rescue/main.go:184-186` (passes `fwdRes.SuccessIDs` to deleter) |
| FR-061, IF-EXEC-02 | `internal/deleter/deleter.go:109-121` |
| FR-062 | `internal/deleter/deleter.go:23`, `:45-58`, `:71-78` |
| FR-063, ER-11 | `internal/deleter/deleter.go:79-106`; tests `internal/deleter/deleter_test.go:63-124` |
| FR-064 | `internal/deleter/deleter.go:31-34`, `:80-105` |
| FR-065 | `cmd/quarantine-rescue/main.go:164-168` (early return before phase 4) |
| FR-066 | `cmd/quarantine-rescue/main.go:178-181` |
| FR-070 | `cmd/quarantine-rescue/main.go:252-264` |
| FR-071 | `cmd/quarantine-rescue/main.go:133`, `:143`, `:150`, `:156-158`, `:171`, `:176-177`, `:184`, `:189` |
| FR-080 | README:158-162 (idempotency contract); behaviour follows from FR-040, FR-054, FR-061 |
| FR-081 | `cmd/quarantine-rescue/main.go:184-186` (delete only takes `SuccessIDs`); no checkpoint persistence anywhere |
| NFR-001 | `internal/exporter/exporter.go:71-117` (channel streaming); `cmd/quarantine-rescue/main.go:195-211` (early break on limit) |
| NFR-002 | `cmd/quarantine-rescue/main.go:92-93`; `internal/forwarder/forwarder.go:133-156` (wg.Wait before return) |
| NFR-003 | every `logger.*` call uses ids/pubkeys/kinds/counts only; no `Raw`/content fields |
| NFR-004 | `internal/forwarder/forwarder.go:65-159` (worker pool, parallel pubkeys) |
| NFR-005 | `Makefile:34-50` (`build-alpine`, `build-linux`) |
| NFR-006 | `internal/whitelist/config.go:24-51` (read-only viper, no write paths) |
| IF-CLI-01..11 | `cmd/quarantine-rescue/main.go:50-65` |
| IF-HTTP-01 | `internal/whitelist/client.go:48-62` |
| IF-HTTP-02 | `internal/whitelist/client.go:64-91` |
| IF-EXEC-01 | `internal/exporter/exporter.go:62-63` |
| IF-EXEC-02 | `internal/deleter/deleter.go:117-120` |
| ER-01 | `cmd/quarantine-rescue/main.go:116-119`; `internal/whitelist/config.go:40-49` |
| ER-02 | `internal/whitelist/config.go:40-44` |
| ER-03 | `cmd/quarantine-rescue/main.go:122-128` |
| ER-04 | `internal/exporter/exporter.go:64-69` |
| ER-05 | `internal/exporter/exporter.go:108-116` |
| ER-06 | `internal/exporter/exporter.go:91-93` |
| ER-07 | `internal/exporter/exporter.go:95-98` |
| ER-08 | `internal/whitelist/client.go:71-90` |
| ER-09 | `internal/forwarder/forwarder.go:90-100` |
| ER-10 | `internal/forwarder/forwarder.go:111-128` |
| ER-11 | `internal/deleter/deleter.go:79-106` |
| ER-12 | `cmd/quarantine-rescue/main.go:92-93`; `internal/exporter/exporter.go:100-106`; `internal/forwarder/forwarder.go:104-110` |
| ER-13 | `cmd/quarantine-rescue/main.go:200-205` |
| AC-01..AC-09 | `README.md:135-156` |
| AC-10 | `README.md:166-169`; `cmd/quarantine-rescue/main.go:122-128` |
| AC-11 | `internal/exporter/exporter_test.go:57-77` |
| AC-12 | `internal/deleter/deleter_test.go:103-124` |

---

## 10. Open Questions for the Port

These are choices the Go reference made implicitly. A port should make them
deliberately and document the decision.

1. **WebSocket reconnect inside a worker**: the Go reference relies on
   go-nostr's `Relay` keeping the connection open across a pubkey's events;
   a single dropped connection mid-pubkey-batch may surface as failures for
   the remainder of that batch. A port MAY add explicit reconnect on
   transient errors — but must still respect oldest-first ordering and must
   never mark an event success without seeing `OK true`.
2. **Bounded vs unbounded result channels**: the Go reference uses
   `len(pubkeys)`-sized result channels. A streaming port may prefer
   producer-side aggregation; observable counters in the summary line are
   what matter.
3. **Strfry binary path**: hard-coded to `/app/strfry` inside the container.
   If a target deployment uses a different image with a different path, a
   future flag MAY be added; this is not currently in scope.
4. **Pubkey hex normalisation**: pubkeys from the export are passed
   verbatim to `/check/<pubkey>`. The Go reference does not lowercase or
   validate — neither should a port, to remain bug-compatible.

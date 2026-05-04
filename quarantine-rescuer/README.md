# quarantine-rescuer

One-shot CLI that pulls events from the **quarantine** StrFry relay back into
the **main** StrFry relay when their author becomes whitelisted. Runs on the
host that owns both strfry containers.

## Why this exists

Main StrFry rejects writes from non-whitelisted pubkeys. The router plugin
forwards those rejected events to a sidecar quarantine relay
(see `quarantine/SPEC.md`). The whitelist refreshes from Dgraph every six
hours, so a pubkey can land in quarantine at time T and become whitelisted
shortly after — leaving recoverable events stranded. This tool periodically
moves them back.

## How it works

1. **Export** every event in the quarantine LMDB by exec'ing
   `strfry export` inside the quarantine container.
2. **Group** events by pubkey.
3. **Check** each unique pubkey against the live whitelist server (same
   endpoint the live plugin reads from `~/deepfry/whitelist.yaml`). The
   tool aborts up front if the server is unreachable rather than failing
   every check closed.
4. **Forward** each whitelisted pubkey's events to the main relay over
   `ws://localhost:7777` via go-nostr `Relay.Publish`. Events for one
   pubkey are sent **sequentially in oldest-first order** so replaceable
   kinds (kind 0 profile, kind 3 follows) end up with the newest version
   winning. Events for different pubkeys run on a small worker pool.
5. **Delete** only the events that successfully forwarded, by event id,
   via `strfry delete --filter '{"ids":[…]}'` exec'd in the quarantine
   container.

LMDB safety: the tool never opens the quarantine LMDB directly. All reads
go through `strfry export` (a read-only LMDB transaction in a separate
process — non-blocking next to the running relay), and all deletes go
through `strfry delete` (a separate writer that LMDB serialises against
the relay's writer via the lock file). This is the documented strfry
approach.

## Build

```bash
make build           # ./bin/quarantine-rescue
make build-alpine    # static linux/amd64 binary
make test
```

## Usage

```bash
./bin/quarantine-rescue --dry-run          # preview only
./bin/quarantine-rescue                    # execute
```

Common flags:

| flag | default | purpose |
|------|---------|---------|
| `--dry-run` | false | export + check, no publish, no delete |
| `--limit N` | 0 (unlimited) | stop after N events from the export |
| `--batch-size N` | 500 | event ids per `strfry delete` invocation |
| `--forward-concurrency N` | 4 | parallel pubkeys forwarded at once |
| `--whitelist-concurrency N` | 8 | parallel whitelist checks |
| `--main-relay` | `ws://localhost:7777` | main StrFry WebSocket |
| `--quarantine-container` | `strfry-quarantine` | docker container name |
| `--quarantine-config` | `/etc/strfry.conf` | config path inside that container |
| `--publish-timeout` | 5s | per-event publish timeout |
| `--log-level` | info | debug, info, warn, error |
| `--version` | | print build info and exit |

The whitelist server URL and check timeout come from
`~/deepfry/whitelist.yaml` (`server_url`, `check_timeout`) — the same file
the live plugin reads, so the rescuer always agrees with the relay.

## Configuration

### `~/deepfry/whitelist.yaml`

Only two keys are read; everything else in the file is ignored. Defaults
match the live plugin so an absent file is fine on a single-host setup.

| Key | Default | Purpose |
|---|---|---|
| `server_url` | `http://localhost:8081` | Whitelist server the rescuer queries. Set to a remote URL when the whitelist server runs on a different host. |
| `check_timeout` | `2s` | Per-pubkey HTTP timeout. |

### Container assumptions

The defaults assume the deepfry compose stack is running with the standard
service names (`strfry`, `strfry-quarantine`) and the binary path
`/app/strfry`. If you've renamed the quarantine container or use a
non-standard image, override with `--quarantine-container` and
`--quarantine-config`.

## Package layout

```
cmd/quarantine-rescue/main.go     # CLI entrypoint, flag parsing, orchestration
internal/whitelist/               # HTTP client + viper-backed config loader
internal/exporter/                # bufio.Scanner over `docker exec … strfry export`
internal/forwarder/               # go-nostr Relay.Publish, oldest-first per pubkey
internal/deleter/                 # batched `strfry delete --filter` with halve-and-retry
internal/runner/                  # os/exec abstraction so internal/* can be unit-tested
```

The `internal/whitelist/` package is a deliberate copy of
`whitelist-plugin/pkg/client` (just the two endpoints we need:
`/check/{pubkey}`, `/health`). Existing deepfry subsystems are independent
Go modules with no cross-imports; we follow that convention. Keep the two
client implementations behaviourally identical — if the live plugin
changes its fail-closed semantics or adds a new endpoint we depend on,
update both.

## Testing

```bash
make test                  # unit tests + coverage
go test ./... -short -v    # verbose
```

Unit-test coverage:

| Package | Coverage | Notes |
|---|---|---|
| `internal/exporter` | ~92% | Fake `runner.Runner`; tests parsing, malformed-line skipping, wait/start errors, context cancellation. |
| `internal/deleter` | ~83% | Fake runner; tests batching, halve-and-retry on batch failure, poison-id isolation, argv shape. |
| `internal/whitelist` | ~56% | `httptest` server; tests `/check` happy/sad paths and fail-closed behaviour on network errors. |
| `internal/forwarder` | ~59% | Tested for the unreachable-relay path (everything fails, nothing gets deleted). The actual NIP-01 publish path is **not** unit-tested — it requires a real or stubbed WS relay; covered by the manual end-to-end test below. |
| `internal/runner` | 0% | Thin `os/exec` wrapper; exercised transitively by integration. |
| `cmd/quarantine-rescue` | 0% | Wiring; covered by the manual end-to-end test. |

## End-to-end verification

Run on a clean dev stack with `docker compose -f docker-compose.dgraph.yml up -d` and `docker compose -f docker-compose.strfry.yml up -d`.

1. **Seed.** Generate two keypairs `A` and `B` (`nak key`). Add `A` to Dgraph (e.g. via the WoT crawler or a manual mutation), then trigger a whitelist refresh (or wait the configured `refresh_interval`). Confirm:
   ```
   curl http://localhost:8081/check/<A>   # → {"whitelisted":true}
   curl http://localhost:8081/check/<B>   # → {"whitelisted":false}
   ```
2. **Inject.** Publish 3 kind-1 events from each of `A` and `B` directly to the quarantine relay (which has no writePolicy):
   ```
   nak event -k 1 -c "msg" --sec <A>  ws://localhost:7778
   ```
   Confirm 6 events in quarantine:
   ```
   docker exec strfry-quarantine /app/strfry --config=/etc/strfry.conf scan '{}' | wc -l
   ```
3. **Dry run.** `./bin/quarantine-rescue --dry-run` — expect `pubkeys_whitelisted=1`, `events_to_forward=3`, `events_forwarded=0`. Quarantine count still 6.
4. **Real run.** `./bin/quarantine-rescue` — expect `events_forwarded=3`, `events_deleted=3`, `events_failed_forward=0`.
5. **Mainline check.** `docker exec strfry /app/strfry --config=/etc/strfry.conf scan '{"authors":["<A>"]}' | wc -l` → 3.
6. **Quarantine check.** `… scan '{"authors":["<A>"]}'` → 0; `… scan '{"authors":["<B>"]}'` → 3.
7. **Idempotency.** Re-run `./bin/quarantine-rescue` — expect `events_forwarded=0`, `events_deleted=0`.
8. **Reject path.** Force the whitelist server to claim `B` is whitelisted but leave mainline's view stale (e.g. by adding `B` to Dgraph but skipping the refresh on the live router's whitelist). The mainline plugin will reject `B`'s events; the rescuer must log per-event rejections and **not** delete those quarantine events.
9. **Replaceable-kind ordering.** Inject two kind-0 events from `A` to quarantine 100s apart, oldest first. After rescue, query `{"authors":["<A>"], "kinds":[0]}` on mainline and confirm the newer one wins (proves oldest-first sequential publishing).

## Idempotency and failure handling

- Re-runs are safe. Forwarding the same event twice is a no-op (StrFry
  dedupes on event id); deleting an already-deleted id is a no-op.
- A forward that returns an error (network failure, OK=false rejection
  from the main relay's plugin) does **not** trigger a delete — the
  event stays in quarantine for the next run.
- A failed delete batch is retried with halved batch size; below batch
  size 8, ids are deleted one at a time so a single poison id can't
  block progress.
- If the whitelist server is unreachable at start, the tool exits
  non-zero immediately rather than mass-skipping.

## Sample summary line

```json
{
  "level":"INFO","msg":"rescue summary",
  "pubkeys_seen":1247,"pubkeys_whitelisted":12,
  "events_exported":104221,"events_to_forward":318,
  "events_forwarded":316,"events_failed_forward":2,
  "events_deleted":316,"events_failed_delete":0,
  "duration_ms":18742
}
```

## Operational notes

- The tool requires `docker` on the host PATH and permission to exec into
  the quarantine container.
- It is **not** packaged inside the strfry Docker image — it calls
  `docker exec` and so must run on the host (or on something with the
  docker socket mounted).
- A reasonable cron cadence is hourly, aligned slightly after the
  whitelist server's 6h refresh window.

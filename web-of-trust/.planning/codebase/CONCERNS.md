# Codebase Concerns

**Analysis Date:** 2026-06-15

## Tech Debt

**Hardcoded pubkeys in whitelist repository**
- File: `whitelist-plugin/pkg/repository/dgraph_repository.go` (`getHardcodedPubkeys()`, lines 203–212)
- Five pubkeys (live forwarder, history forwarder, admin accounts) are hardcoded in source. If any of these keys are rotated, a code change and redeploy is required. There is no mechanism to detect stale hardcoded keys or to remove them via config.
- Fix: move to a `whitelist-server.yaml` config field or a separate admin-keys list; document key rotation procedure.

**Whitelist refresh silently retains stale data on sustained Dgraph outage**
- File: `whitelist-plugin/pkg/whitelist/whitelist_refresher.go` (lines 62–87)
- If all `retryCount+1` refresh attempts fail, the refresher logs one line and continues using the last successfully loaded whitelist. There is no alerting, no health-check downgrade, and no circuit-breaker. A Dgraph outage will silently age the whitelist indefinitely.
- Fix: expose a staleness metric on `/stats`, degrade `/health` to `{"status":"stale"}` after configurable max-age, or alert via structured log with staleness timestamp.

**6-hour whitelist refresh cadence is long**
- Config: `config/whitelist/whitelist-server.yaml` (`refresh_interval: 6h`)
- A newly-discovered pubkey can wait up to 6 hours before it is admitted to write events. Quarantine events during that window pile up. The `quarantine-rescuer`/`quarantine-cleaner` tools exist partly to paper over this gap.
- Fix: reduce refresh interval (adds Dgraph load) or add a manual trigger endpoint (`POST /refresh`).

**`quarantine-rescuer` reads LMDB directly — schema coupling**
- Files: `quarantine-rescuer/internal/lmdbreader/reader.go`, `dictcache.go`
- The rescuer opens strfry's `EventPayload` and `CompressionDictionary` LMDB sub-databases directly using `github.com/PowerDNS/lmdb-go`. This is coupled to strfry's undocumented internal schema (`golpe.yaml`). Any strfry upgrade that changes the LMDB schema or DB version silently produces corrupt reads.
- Mitigation in place: `DefaultMapSize` matches strfry's default. No runtime schema version check exists.
- Fix: add a startup check against `Meta.dbVersion` (as specified in `spam/spec.md` for LMDB2GraphQL) before reading events; refuse to proceed on version mismatch.

**Dgraph gRPC connection is unauthenticated and unencrypted**
- File: `web-of-trust/pkg/dgraph/dgraph.go` (line 43: `insecure.NewCredentials()`)
- All Dgraph gRPC traffic uses plaintext transport with no authentication. Acceptable in a single-host Docker network, but a misconfigured deployment that exposes port `9080` externally would allow unauthenticated writes.
- Fix: add network-policy docs or explicit `deepfry-net` isolation assertion; add mTLS or token auth if Dgraph is ever moved off-host.

**Dgraph HTTP/GraphQL endpoint has no authentication**
- Port `8080` exposed by `docker-compose.dgraph.yml`. The whitelist server queries it via plain HTTP. The Ratel UI (`8000`) is also exposed without auth.
- Fix: restrict `8080`/`9080`/`8000` to `deepfry-net` and never expose externally; document this explicitly.

**Two separate quarantine-recovery tools with overlapping scope**
- `quarantine-rescuer/` (implemented, reads LMDB directly) vs `quarantine-cleaner/` (spec-only, uses `docker exec strfry scan/delete`)
- Their purpose descriptions overlap: both move whitelisted pubkeys' events from quarantine to main relay. It is unclear which is the intended long-term path. The rescuer's LMDB-direct approach contradicts the "never modify StrFry's LMDB directly" principle stated in CLAUDE.md; the cleaner spec uses only the StrFry binary interface.
- Fix: decide which approach wins; deprecate or remove the other.

**No shared Go module / workspace**
- Each subsystem (`event-forwarder`, `whitelist-plugin`, `web-of-trust`, `quarantine-rescuer`) is an independent Go module with no `go.work`. Shared utilities (e.g. Nostr pubkey hex helpers) are duplicated across modules.
- Fix: introduce a `go.work` workspace or a `pkg/` shared module to reduce duplication.

**`discover-relays` command length**
- File: `web-of-trust/cmd/discover-relays/main.go` — 498 lines in a single `main.go`, mixing CLI arg parsing, HTTP fetching, WebSocket relay probing, latency testing, and config writing.
- Fix: extract relay-probe logic into `pkg/relayprobe` for testability.

**Spam module uses `.expect()` and `.unwrap()` in production code**
- Files: `spam/src/lmdb/scan.rs` (lines 234, 320, 439, 466, 532, 581 and more), `spam/src/lmdb/payload.rs` (line 280), `spam/src/lmdb/meta.rs` (lines 236, 251)
- Production LMDB scanning code uses `.unwrap()` for slice-to-array conversions (e.g., `value[0..8].try_into().unwrap()`) and `.last().unwrap()` on batch results. While these are guarded by preconditions (e.g., `batch.len() > 0`), an unexpected LMDB corruption or format change would panic the entire GraphQL server.
- Impact: `lev_id` extraction (line 439 and similar) is called per LMDB key during scan; a corrupted database would cause service failure without a graceful error.
- Fix: replace `.unwrap()` with `?` and structured error returns; add defensive checks for slice bounds and batch emptiness before unwrap sites.

**Spam module RwLock poison checks incomplete**
- File: `spam/src/lmdb/payload.rs` (lines 162, 190, 203, 212: `.expect("DictCache RwLock poisoned")`)
- The DictCache holds an RwLock over the decompression dictionary cache. Poison checks log a panic message but do not gracefully degrade; a poisoned lock (from a panicked reader/writer) will crash the entire HTTP server.
- Impact: any panic in dictionary access will propagate as a process crash.
- Fix: use `lock.read().unwrap_or_else(|e| e.into_inner())` or a TryLock-based fallback; document recovery strategy.

---

## Security Considerations

**Private keys via environment variables — no rotation mechanism**
- Keys (`STRFRY_PRIVATE_KEY`, `NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`) are passed to containers via Docker environment variables sourced from `.env`. Docker inspect reveals env vars to any user with Docker socket access.
- Mitigation in place: `.env` is gitignored; `.env.example` documents required vars without values.
- Risk: no documented key rotation procedure. No key-expiry detection. If a key is compromised, all affected containers must be manually re-deployed.

**Whitelist plugin fails open on Dgraph unavailability at startup**
- If Dgraph is unreachable when `whitelist-server` starts and all retries fail, `whitelist_refresher.go` logs the failure but the whitelist starts empty. The hardcoded 5 pubkeys are still admitted, but the entire web-of-trust list is missing — effectively open to anyone not checked by Dgraph (all events are rejected since the whitelist has only 5 entries). This is fail-closed for unknown pubkeys but may drop legitimate events.
- Mitigation: `whitelist-server` container has `depends_on: dgraph: condition: service_healthy` in `docker-compose.dgraph.yml`, reducing the window.
- Residual risk: Dgraph can become unreachable after startup (network partition, OOM, restart).

**Quarantine relay port exposed on host**
- `docker-compose.strfry.yml` exposes `7778:7778` for `strfry-quarantine`. Any Nostr client that knows the port can write events directly to quarantine, bypassing the router plugin.
- Fix: bind quarantine port to `127.0.0.1:7778` or remove host-side port mapping; quarantine is only needed within `deepfry-net`.

**No rate limiting or connection throttling on StrFry**
- `strfry.conf` sets `nofiles = 1000000` but has no per-IP connection limit or event-rate limit beyond `maxSubsPerConnection = 20`. The whitelist plugin rejects writes from non-whitelisted pubkeys, but read subscriptions (REQs) from arbitrary IPs are unlimited.
- Fix: add a reverse proxy (nginx/caddy) with rate limiting in front of port `7777`.

**Spam module GraphQL endpoint has no authentication**
- File: `spam/src/main.rs` (lines 84–94), `spam/src/graphql/resolvers.rs`
- The lmdb2graphql GraphQL server binds to `127.0.0.1:8080` by default (loopback safety, CR-01) but has zero authentication. If an operator configures a non-loopback bind address, the entire strfry corpus (all events, all metadata, full GraphQL introspection) is exposed unauthenticated to any network peer.
- Mitigation: config defaults to `127.0.0.1:8080`; a loud warn log fires on non-loopback bind. Operator must edit YAML to expose.
- Residual risk: no bearer token, mTLS, or API-key support; operators deploying spam module over a network or to a shared environment must rely entirely on network isolation.
- Fix: add JWT/token validation; support mTLS; document network-isolation requirements.

---

## Scalability & Performance

**Single Dgraph instance is the trust/whitelist single point of failure**
- All subsystems depend on one Dgraph standalone container. Dgraph standalone mode (`dgraph/standalone`) is not HA. An OOM, disk full, or container restart brings down both the whitelist-server refresh path and the web-of-trust crawler.
- Dgraph memory limit is set to 8 GB (`docker-compose.dgraph.yml`); with a large graph this cap will be hit.
- Fix: document Dgraph memory sizing; add monitoring/alerting on heap usage; plan migration to Dgraph cluster or alternative if graph exceeds ~10M nodes.

**gRPC message size raised to 256 MiB as a workaround**
- File: `web-of-trust/pkg/dgraph/dgraph.go` (line 39: `maxRecvMsgSize = 256 << 20`)
- The limit was raised because `GetStalePubkeys` returns response payloads over gRPC's default 4 MB cap. This is a scaling smell: as the graph grows, even 256 MiB may not be enough.
- Fix: paginate `GetStalePubkeys` server-side or stream results instead of returning a single large payload.

**Event-forwarder one-instance-per-relay creates N×M container sprawl**
- `docker-compose.evtfwd.yml` runs 6 containers (3 relays × 2 modes: live + history). Adding a new relay requires manually adding 2 more services to the compose file. There is no dynamic relay configuration.
- Fix: add a forwarder config-driven relay list with live/history mode per entry; run one container that manages multiple relay connections.

**StrFry LMDB grows unbounded**
- No retention policy is configured (`rejectEventsOlderThanSeconds = 315360000` accepts events up to 10 years old). The LMDB `data.mdb` on disk was already 2.3 GB at last snapshot (`data/strfry-db/data.mdb`). With 6 active forwarder streams, this will continue to grow.
- Fix: set a reasonable `rejectEventsOlderThanSeconds` and periodically run `strfry compact`; document backup procedure before compaction.

**Web-of-trust crawler uses a single DB mutex for all writes**
- File: `web-of-trust/pkg/crawler/crawler.go` (field `dbUpdateMutex sync.Mutex`)
- All Dgraph mutations are serialised behind a single mutex even when processing results from multiple relays concurrently. This limits write throughput as the graph grows.
- Fix: batch mutations from multiple relays before acquiring the mutex, or use per-pubkey sharding.

**Spam module scan loops can exceed MAX_ROUNDS budget**
- File: `spam/src/query/engine.rs` (const `MAX_ROUNDS = 8`)
- The query engine caps execution rounds at 8 per query to prevent DoS; once `emit_limit` events are collected, execution breaks. However, if a single timestamp holds more events than `emit_limit`, subsequent queries must resume from the cursor; this is correct but means a large burst of same-timestamp events could require many sequential queries to fully enumerate.
- Impact: moderate; the default `limit=100` and cursor model allow pagination. This is by design (D-11, CR-01).
- Mitigation: clients should follow cursor pagination; no known issue yet.

**Spam module test batch_size=2 vs production batch_size=256**
- File: `spam/.planning/research/` and memory notes
- Integration tests use `batch_size=2` for coverage granularity; production scans use `batch_size=256`. A code path exercised at size 2 may fail at size 256 if LMDB or cursor state behaves differently under bulk reads.
- Fix: add parameterized tests or a second test suite that runs against size-256 batches.

---

## Operational Risks

**Three separate docker-compose files with no orchestration layer**
- Startup order requires: `docker-compose.dgraph.yml` first (creates `deepfry-net`), then `docker-compose.strfry.yml` (uses external network), then `docker-compose.evtfwd.yml`. Getting this order wrong produces silent failures. `docker-compose.strfry.yml` declares `deepfry-net` as `external: true` and will fail if the dgraph compose hasn't been started yet.
- Fix: add a startup script or a root compose file with correct dependency ordering.

**No centralised logging or metrics**
- All subsystems log to stderr/stdout via `log.Printf` (Go) or `tracing::info!` (Rust). Log retention relies entirely on Docker's `json-file` log driver (`max-size: 10m, max-file: 3`). There is no centralised log aggregation, no metrics endpoint (Prometheus etc.), and no alerting.
- Fix: add structured JSON logging (e.g. `log/slog` with JSON handler in Go; tracing is already JSON in spam module) and a log aggregation sidecar or remote sink.

**Config files committed to repo include production Dgraph URLs**
- `config/whitelist/whitelist-server.yaml` contains `dgraph_graphql_url: "http://dgraph:8080/graphql"` and `config/whitelist/whitelist.yaml` contains `server_url: "http://whitelist-server:8081"`. These are committed defaults that work for the standard compose topology but will silently fail in non-standard deployments.
- Risk: an operator who forks or modifies the compose network name gets a broken deploy with no obvious error.

**No backup or disaster recovery procedure documented**
- The LMDB databases (`data/strfry-db/`, `data/strfry-quarantine-db/`) and Dgraph data (`data/dgraph/`) have no documented backup schedule or restore procedure. Loss of the Dgraph volume means loss of the entire web-of-trust graph.
- Fix: document backup/restore; add a scheduled `strfry compact` + snapshot job.

**Quarantine relay's DB guard is a shell script**
- File: `config/strfry/quarantine-db-guard.sh`
- The guard exits with code 4 if the mainline LMDB path is accidentally mounted in the quarantine container. This is a safety net but relies on correct Docker volume mapping. A future change to the compose file could silently break the guard if `QUARANTINE_EXPECTED_DB` or `MAINLINE_DB_PATH` env vars are not kept in sync.

**Web-of-trust crawler removes failed relays from config file**
- File: `web-of-trust/pkg/config/config.go` (`RemoveRelayURL`)
- After `maxConsecutiveFailures` (5), a relay is removed from `~/deepfry/web-of-trust.yaml`. This is irreversible without manual config editing. An extended network partition could silently drain the relay list, leaving the crawler with no sources.
- Fix: add a relay-recovery mechanism (re-add relays from a seed list on empty relay list); log prominently when the relay list drops below a threshold.

**Spam module startup gate chain has no timeout**
- File: `spam/src/main.rs` (steps 6–9)
- If the LMDB fixture fails to open, the Meta read hangs, or the comparator self-check deadlocks, the process will hang indefinitely waiting for the gate chain. There is no startup timeout; the process will be stuck forever without an explicit SIGKILL.
- Fix: add a configurable timeout (e.g., 60s) to the gate chain; fail-closed if exceeded.

**Spam module startup success is silent outside logs**
- No systemd health check or process manager integration. If a deploy watcher relies on exit code 0 but the process runs with an internal hang (e.g., LMDB open never returns), the deploy will succeed but the service will be dead.
- Fix: ensure /ready endpoint is checked by orchestration (Kubernetes readiness probe, systemd service, etc.).

---

## Incomplete/Placeholder Subsystems

**`embeddings-generator/`**
- Status: README only. No implementation.
- Purpose per README: generate embeddings from Nostr events for semantic search.
- Blocks: `search-plugin/`, semantic search capability.

**`embeddings/`**
- Status: `pipeline.md` and `PLAN.md` only. Not even a subsystem directory yet.
- Blocks: embeddings pipeline.

**`search-plugin/`**
- Status: README only. No implementation.
- Purpose: StrFry write-policy plugin that routes events through semantic search index.
- Blocks: full text / semantic search on the relay.

**`profile-builder/`**
- Status: README only. No implementation.
- Purpose: build enriched Nostr profile objects from raw events.

**`thread-inference/`**
- Status: README only. No implementation.
- Purpose: infer conversation threads from Nostr event graphs.

**`quarantine-cleaner/`**
- Status: `FLOW.md`, `PRD.md`, `SPEC.md` only — a very detailed behavioural spec (30+ FRs) but zero Go code.
- Impact: `quarantine-rescuer` (implemented) and `quarantine-cleaner` (specified but unimplemented) overlap in purpose.

---

## Missing Capabilities

**No semantic / full-text search**
- `search-plugin/`, `embeddings-generator/`, and `embeddings/` are all stubs. Nostr `REQ` filtering is the only query mechanism available.

**No monitoring or alerting**
- No Prometheus exporter, no Grafana dashboard, no on-call alerting. The `/stats` endpoint on the whitelist-server (`server.go`) provides basic counters but nothing consumes them. Spam module has no metrics endpoint.

**No automated key rotation for event forwarders**
- The `NOSTR_SYNC_SECKEY_LIVE` and `NOSTR_SYNC_SECKEY_HISTORY` keys are static. There is no key rotation flow, no expiry detection, and no key-compromise response playbook.

**No test coverage for quarantine-rescuer LMDB reader against real strfry DB**
- Unit tests for `lmdbreader` use synthetic fixtures. There is no integration test that runs against a real strfry LMDB to catch schema changes.

**No integration tests for web-of-trust in CI/CD**
- Per `web-of-trust/CLAUDE.md`: "No unit-test suite exists yet." Integration tests gate on a live Dgraph and are not run in CI.

**Spam module has no load testing / performance benchmarks**
- No benchmarks for query execution, LMDB scan throughput, or GraphQL resolver performance. Capacity planning for production deployment is guesswork.
- Fix: add benchmarks in `spam/benches/` or `spam/tests/perf_*.rs` to measure query latency under varied payload sizes and batch counts.

---

## Known Limitations (By Design)

**Spam module does not support event write filtering**
- The lmdb2graphql endpoint is read-only. Event ingestion and filtering is handled by the whitelist plugin and strfry's native plugin interface. Spam detection scores or reputation queries can be done via GraphQL but cannot block writes in real-time.
- Impact: spam/phishing events are accepted by strfry; clients must filter on read.
- Mitigation: future integration of spam scoring into the whitelist plugin's write-decision logic.

**Spam module LMDB access is read-only and non-transactional**
- File: `spam/src/lmdb/scan.rs`, `spam/src/lmdb/payload.rs`
- Snapshot isolation is provided by LMDB's read-only cursors. Concurrent writes from strfry and reads from lmdb2graphql can race; results may lag strfry by up to a few seconds (depending on LMDB flush cadence).
- Impact: GraphQL queries return eventual-consistent results, not real-time state.
- Mitigation: acceptable for spam analysis and reputation scoring, not for transactional consistency.

**Web-of-trust Dgraph writes are serialized by mutex**
- By design (see scalability section). Multiple relays feed pubkey updates concurrently but the database write is single-threaded.
- Justification: Dgraph standalone has limits; batch writes reduce contention.

---

*Concerns audit: 2026-06-15*

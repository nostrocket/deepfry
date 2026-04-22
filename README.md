# DeepFry
This provides deep fried nostr events for humble horse. The core problem we propose to solve is how to surface the most relevant signal in a sea of noise. 

The design uses StrFry for maximum performance and NIP compliance, while surrounding it with additional services for discovery, trust scoring, and search.

We welcome external contributors. 

---

## Overview

- **Relay Core**: Stock [StrFry](https://github.com/hoytech/strfry) for ultra-fast event storage and delivery (LMDB backend).
- **Plugin System**: Uses StrFry’s JSON stdin/stdout plugin interface to integrate custom capabilities without forking.
- **Subsystems**: Modular services for semantic search, embeddings, trust graph building, event forwarding, profile analytics, and thread reconstruction.
- **Data Model**:
  - **Canonical events**: Stored only in StrFry LMDB.
  - **Graphs**: Stored in Dgraph as **ID-only graphs** (WoT = pubkeys, Threads = event IDs).
  - **Vectors**: Stored in pgvector (initially) for semantic search.

---

## Subsystems

| #   | Subsystem                            | Purpose                                                                          | Key Storage                    | Key NIPs                           |
| --- | ------------------------------------ | -------------------------------------------------------------------------------- | ------------------------------ | ---------------------------------- |
| 1   | StrFry Relay (stock + search plugin) | Primary relay for ingest/distribution; full event storage; plugin handles NIP-50 | LMDB                           | 1, 2, 4, 9, 11, 22, 28, 40, 70, 77 |
| 2   | Search Plugin                        | Intercept NIP-50 REQ; delegate to Semantic Search Service                        | None (stateless)               | 50                                 |
| 3   | Event Forwarder                      | Subscribe to external relays and republish to StrFry                             | Replaceable event in StrFry    | 1                                  |
| 4   | Web of Trust Builder + Crawler       | Build trust graph from NIP-02 follows                                            | Dgraph (pubkey graph)          | 2                                  |
| 5   | Semantic Search Service              | Index/query events for semantic search                                           | Vector DB (TBA)                | 50                                 |
| 6   | Embeddings Generator                 | Produce embeddings for search                                                    | Vector DB (TBA)                | —                                  |
| 7   | Profile Builder                      | Aggregate actions: likes, mutes, replies, reposts, zaps                          | ID references in profile store | 18, 25, 51, 57                     |
| 8   | Thread Inference Engine              | Build thread graphs from replies/quotes                                          | Dgraph (event-id graph)        | 1                                  |

---

## Architecture Summary

- **StrFry Relay** is the single point of truth for events.
- **Event Forwarder** pulls events from upstream relays into StrFry.
- **Web of Trust Builder** in Dgraph maintains trust scores and allowlists.
- **Search Plugin** in StrFry handles NIP-50 requests and calls the **Semantic Search Service**.
- **Semantic Search** indexes events using **Embeddings Generator** and ranks them using semantic similarity, BM25, and trust scores.
- **Profile Builder** and **Thread Inference Engine** consume events from StrFry to build profiles and thread graphs.

---

## Quickstart

Get a single-machine DeepFry running in about five minutes.

**Prerequisites**
- Docker + Docker Compose (v2).
- Git.
- (Optional, for local development only) Go 1.24.1+.

**1. Clone and enter the repo**

```bash
git clone https://github.com/your-org/deepfry.git
cd deepfry
```

**2. Create `.env`**

Copy the template and generate a StrFry relay key. `.env` is gitignored and holds all machine-local overrides (secrets, on-disk paths, build labels).

```bash
cp .env.example .env
echo "STRFRY_PRIVATE_KEY=$(openssl rand -hex 32)" >> .env
```

If you plan to run the event forwarders, also generate two sync keys:

```bash
echo "NOSTR_SYNC_SECKEY_LIVE=$(openssl rand -hex 32)"    >> .env
echo "NOSTR_SYNC_SECKEY_HISTORY=$(openssl rand -hex 32)" >> .env
```

Want the on-disk databases on a different drive? See [Machine-specific paths](#machine-specific-paths).

**3. Start the stack**

Make sure Docker is running (on macOS: `open -a Docker`, then wait for it to finish starting). Bring up Dgraph + whitelist-server first, then StrFry (it needs the whitelist-server on the shared network), then optionally the forwarders:

```bash
docker compose -f docker-compose.dgraph.yml up -d
docker compose -f docker-compose.strfry.yml up -d
docker compose -f docker-compose.evtfwd.yml up -d   # optional
```

**4. Verify**

```bash
curl -sSf http://localhost:8081/health && echo " whitelist ok"
docker logs strfry --tail=20
open http://localhost:8000          # Dgraph UI (macOS; use xdg-open on linux)
# ws://localhost:7777                 -- point any Nostr client here
```

**5. What next**
- Tune the knobs in the [Configuration Reference](#configuration-reference).
- For split-machine deployments (Dgraph on one host, StrFry on another), see [Split-machine deployment](#split-machine-deployment).
- For the quarantine relay's safety model, see [`quarantine/SPEC.md`](quarantine/SPEC.md).

---

## Running it

The stack is split into separate compose files so each layer can be managed independently.

### Services

| Service | Container | Compose File | Port | Description |
|---------|-----------|-------------|------|-------------|
| Dgraph | `dgraph` | `docker-compose.dgraph.yml` | 8080 (HTTP/GraphQL), 9080 (gRPC) | Graph database for pubkey relationships |
| Dgraph Ratel | `dgraph-ratel` | `docker-compose.dgraph.yml` | 8000 | Dgraph web UI |
| Whitelist Server | `whitelist-server` | `docker-compose.dgraph.yml` | 8081 | Centralized pubkey whitelist cache (refreshes from Dgraph) |
| StrFry | `strfry` | `docker-compose.strfry.yml` | 7777 (WebSocket) | Nostr relay. The image ships two interchangeable writePolicy plugins (`/app/plugins/whitelist` and `/app/plugins/router`); `strfry.conf` selects which one runs. |
| StrFry Quarantine | `strfry-quarantine` | `docker-compose.strfry.yml` | 7778 (WebSocket) | Secondary StrFry for events the router plugin rejects from mainline. Separate LMDB, guarded against mounting the mainline DB. See [`quarantine/SPEC.md`](quarantine/SPEC.md). |
| Event Forwarders | `fwd-*` | `docker-compose.evtfwd.yml` | -- | Sync events from upstream relays |

### Shutdown

```bash
docker compose -f docker-compose.evtfwd.yml down
docker compose -f docker-compose.strfry.yml down
docker compose -f docker-compose.dgraph.yml down
```

### Machine-specific paths

By default, on-disk databases live under `./data/` in the repo. To put them elsewhere (e.g. a larger SSD), set the following in your `.env` file — it's gitignored and picked up automatically by `docker compose`:

```bash
STRFRY_DB_PATH=/mnt/ssd/strfry-db
STRFRY_QUARANTINE_DB_PATH=/mnt/ssd/strfry-quarantine-db
DGRAPH_DATA_PATH=/mnt/ssd/dgraph
```

Paths can be absolute or relative to the project directory. Unset variables fall back to the `./data/...` defaults. See `.env.example` for the full list.

`strfry-quarantine` is guarded by `config/strfry/quarantine-db-guard.sh`, which refuses to start the container if its configured DB path matches the mainline's. This is a hard safety boundary: mainline data cannot be corrupted by a misconfigured quarantine instance.

### Useful commands

```bash
# Stream from top 20 relays (uses tmux)
./stream-relays.sh          # start
./stream-relays.sh attach   # attach
./stream-relays.sh stop     # stop

# Whitelist server HTTP API
curl http://localhost:8081/health
curl http://localhost:8081/stats
curl http://localhost:8081/check/<64-char-hex-pubkey>
```

---

## Configuration Reference

DeepFry has three kinds of configuration:

- **`.env`** at the repo root — docker-compose variables (secrets, machine-specific paths, build labels). Gitignored. Template at `.env.example`.
- **`~/deepfry/*.yaml`** on the host — per-service runtime config, read by Go services via Viper. Auto-created with defaults on first run when missing.
- **`config/**/*`** in the repo — files mounted read-only into containers (StrFry conf, whitelist/router plugin YAML, Dgraph schema). Edited in-tree.

### `.env` variables

All optional unless marked required. Unset variables fall back to the defaults shown.

| Variable | Default | Purpose |
|---|---|---|
| `STRFRY_PRIVATE_KEY` | *(empty — required for signed responses)* | StrFry relay Nostr signing key (32-byte hex). |
| `NOSTR_SYNC_SECKEY_LIVE` | *(empty — required for live forwarders)* | Signs sync-progress events (kind 30078) for **live** forwarders. One key shared across all live instances. |
| `NOSTR_SYNC_SECKEY_HISTORY` | *(empty — required for history forwarders)* | Same idea, for the **history** forwarders. Must differ from the live key — the `d`-tag is derived from it and identifies each sync stream. |
| `STRFRY_DB_PATH` | `./data/strfry-db` | Host path for mainline StrFry LMDB. Can be absolute. |
| `STRFRY_QUARANTINE_DB_PATH` | `./data/strfry-quarantine-db` | Host path for the quarantine relay's LMDB. Must differ from the mainline — enforced by `config/strfry/quarantine-db-guard.sh`. |
| `DGRAPH_DATA_PATH` | `./data/dgraph` | Host path for Dgraph's data directory. |
| `FWD_VERSION` | `dev` | Label baked into the event-forwarder image. |
| `FWD_GIT_COMMIT` | `unknown` | Git SHA label for the forwarder image. |
| `FWD_BUILD_TIME` | `unknown` | Build-time label for the forwarder image. |

### `~/deepfry/whitelist.yaml` — whitelist client + server

One file, two consumers. The whitelist **server** (runs next to Dgraph) reads the server fields; the **plugin** (runs inside StrFry) reads the client fields. Unknown keys are ignored, so the file can hold both.

Server side (`ServerConfig` in `whitelist-plugin/pkg/config/config.go`):

| Key | Default | Purpose |
|---|---|---|
| `dgraph_graphql_url` | `http://localhost:8080/graphql` | Dgraph GraphQL endpoint the server queries on each refresh. |
| `refresh_interval` | `6h` | How often the server rebuilds its in-memory whitelist from Dgraph. |
| `refresh_retry_count` | `3` | Retries per refresh attempt on failure. |
| `idle_conn_timeout` | `90s` | HTTP keep-alive idle timeout. |
| `http_timeout` | `30s` | Per-request HTTP timeout. |
| `query_timeout` | `20m` | Dgraph query timeout (refreshes can pull millions of rows). |
| `server_listen_addr` | `:8081` | HTTP listen address for `/check/{pk}`, `/health`, `/stats`. |
| `debug` | `true` | Verbose logging. |

Client side (`ClientConfig`):

| Key | Default | Purpose |
|---|---|---|
| `server_url` | `http://localhost:8081` | Whitelist server the plugin calls to authorize writes. |
| `check_timeout` | `2s` | Per-pubkey lookup timeout. |

### `~/deepfry/router.yaml` — router plugin

The router plugin is the whitelist plugin's superset: same accept/reject decision, plus it forwards rejected events to the quarantine relay. Selected by `plugin = "/app/plugins/router"` at `config/strfry/strfry.conf:117`.

All keys honor the `ROUTER_` env-var prefix (e.g. `ROUTER_SERVER_URL`, `ROUTER_QUARANTINE_ENABLED`). Defaults in `whitelist-plugin/pkg/config/router_config.go`.

| Key | Default | Purpose |
|---|---|---|
| `server_url` | `http://localhost:8081` | Whitelist server (same as the client plugin). |
| `check_timeout` | `2s` | Per-pubkey lookup timeout. |
| `quarantine.enabled` | `true` | Turn the quarantine side-channel on/off. Rejections still happen when off; they're just not forwarded. |
| `quarantine.relay_url` | `ws://strfry-quarantine:7778` | WebSocket to the quarantine StrFry. |
| `quarantine.buffer_size` | `10000` | In-memory queue for events awaiting publish to quarantine. |
| `quarantine.publish_timeout` | `5s` | Per-event publish timeout. |
| `quarantine.metrics_interval` | `60s` | Cadence for metrics log lines. |

### `~/deepfry/web-of-trust.yaml` — WoT crawler

Defaults in `web-of-trust/pkg/config/config.go`.

| Key | Default | Purpose |
|---|---|---|
| `relay_urls` | `[damus.io, nos.lol, relay.nostr.band, nostr-pub.wellorder.net, relay.primal.net]` | Relays the crawler subscribes to (kind 3). |
| `dgraph_addr` | `localhost:9080` | Dgraph gRPC endpoint. |
| `pubkey` | *a default seed npub* | Starting point for graph traversal (npub or hex). |
| `timeout` | `30s` | gRPC query timeout. |
| `stale_pubkey_threshold` | `86400` (seconds) | Age after which a pubkey's follow list is re-fetched. |
| `forward_relay_url` | *(empty)* | Optional outbound relay for republishing. |
| `debug` | `false` | Verbose logging. |

### Event forwarder (per container)

Each `fwd-*` service in `docker-compose.evtfwd.yml` is configured via environment, set inline in the compose file. Defaults live in `event-forwarder/pkg/config/keys.go`; override them per service in the compose block.

| Env var | Default | Purpose |
|---|---|---|
| `SOURCE_RELAY_URL` | *(required)* | Upstream relay (e.g. `wss://relay.damus.io`). |
| `DEEPFRY_RELAY_URL` | *(required)* | Target (`ws://strfry:7777` in-network). |
| `NOSTR_SYNC_SECKEY` | *(required)* | Signs kind-30078 sync-progress events; in compose this is wired to `${NOSTR_SYNC_SECKEY_LIVE}` or `${NOSTR_SYNC_SECKEY_HISTORY}`. |
| `QUIET_MODE` | `false` | Disable the TUI (use for `docker logs`-style output). |
| `SYNC_WINDOW_SECONDS` | `5` | Per-window duration. History forwarders use `3600`. |
| `SYNC_MAX_BATCH` | `1000` | Events per window. History uses `5000`. |
| `SYNC_MAX_CATCHUP_LAG_SECONDS` | `10` | When behind by more than this, the forwarder compresses windows. History uses `86400`. |
| `SYNC_START_TIME` | *(empty → recent)* | RFC3339 start; history forwarders set `2020-01-01T00:00:00Z`. |
| `NETWORK_INITIAL_BACKOFF_SECONDS` | `1` | Reconnect backoff floor. |
| `NETWORK_MAX_BACKOFF_SECONDS` | `30` | Reconnect backoff ceiling. |
| `NETWORK_BACKOFF_JITTER` | `0.2` | Randomization factor (0.0–1.0). |
| `TIMEOUT_PUBLISH_SECONDS` | `10` | Per-event publish timeout. |
| `TIMEOUT_SUBSCRIBE_SECONDS` | `10` | REQ subscribe timeout. |

> One forwarder instance per (source relay, live/history) pair. Live and history must use **different** sync seckeys so their sync-progress events don't collide.

### StrFry relay (`config/strfry/strfry.conf`)

Stock StrFry config — consult [the StrFry docs](https://github.com/hoytech/strfry) for the full option list. Two things we set intentionally:

| Key | Value | Purpose |
|---|---|---|
| `writePolicy.plugin` | `/app/plugins/router` (line 117) | Selects the router plugin over the plain whitelist plugin. Both binaries ship in the image; flip this to `/app/plugins/whitelist` to disable quarantine routing. |
| `relay.port` | `7777` | WebSocket port (exposed on the host). |

The quarantine relay uses `config/strfry/strfry-quarantine.conf` with **no** writePolicy plugin and a separate DB path. The `quarantine-db-guard.sh` entrypoint enforces DB separation (exit codes 1–4 signal specific safety failures).

---

## Split-machine deployment

Running Dgraph on one machine and StrFry on another — use `switch-dgraph.sh`.

```bash
./switch-dgraph.sh remote                   # auto-discovers hosts via masscan, prompts to confirm
./switch-dgraph.sh remote --yes             # auto-discovers, skips prompts (prefers version-matched whitelist)
./switch-dgraph.sh remote --host <ip>       # skip discovery, use this host for all services (implies --yes)
./switch-dgraph.sh remote --subnet <cidr>   # scan this CIDR instead of the default-route subnet
./switch-dgraph.sh remote --verbose         # print raw masscan output + per-probe results for debugging
./switch-dgraph.sh status                   # shows current mode and the URLs each config points at
./switch-dgraph.sh local                    # restores from backups in .switch-dgraph-backups/
```

Discovery scans every non-loopback `/24` the host is attached to (so Docker-internal subnets and the real LAN both get covered) via `masscan` for Dgraph HTTP (8080), Dgraph gRPC (9080), StrFry (7777), and the whitelist server (8081). Use `--subnet 192.168.30.0/24` (comma/space-separated accepted) to narrow or redirect the scan. Each candidate is then probed over HTTP to confirm the service is actually there:

- Dgraph via `GET /health`
- StrFry via the NIP-11 relay info doc
- Whitelist via `GET /version` — the returned commit is compared against this checkout's `git rev-parse --short HEAD`. Servers built before `/version` existed, or servers that couldn't read their own commit, still show up as candidates but are labelled `[version unavailable]`.

When multiple whitelist candidates exist and `--yes` is set, the version-matched one wins. If only one whitelist candidate is found and it mismatches (or is unavailable), the script accepts it with a warning. If multiple non-matching candidates are found under `--yes`, the script drops back to an interactive prompt for safety.

`masscan` is installed on demand (first run): `brew install masscan` on macOS; `apt`/`dnf`/`yum`/`pacman`/`apk` on Linux. The scan itself requires `sudo`.

Files the script rewrites on `remote`:

| File | Field updated |
|---|---|
| `config/whitelist/whitelist.yaml` | `server_url` → `http://<whitelist>:8081` |
| `config/whitelist/whitelist-server.yaml` | `dgraph_graphql_url` → `http://<dgraph>:8080/graphql` |
| `config/whitelist/router.yaml` | `server_url` → `http://<whitelist>:8081` |
| `docker-compose.strfry.yml` | Renames `deepfry-net` → `strfry-net`, switches to a local bridge network. |
| `docker-compose.evtfwd.yml` | Same network rename. |
| `~/deepfry/web-of-trust.yaml` | `dgraph_addr` → `<dgraph>:9080`, `forward_relay_url` → `ws://<strfry>:7777` (only if the file exists). |

The whitelist server embeds its git commit automatically via Go's `-buildvcs=auto` (the Dockerfile copies `.git` into the build context). `docker compose -f docker-compose.dgraph.yml up -d --build whitelist-server` is all you need — no env vars.

Originals are backed up to `.switch-dgraph-backups/` and restored by `switch-dgraph.sh local`.

---

## Contributing

We merge all PRs that solve exactly one clearly stated problem and don't break any existing functionality. 

1. Fork the repository and create a branch.
2. Work on your feature or fix.
3. Submit a PR with a clear one line description of the problem being solved. 

---

## License

Mozilla Public License Version 2.0

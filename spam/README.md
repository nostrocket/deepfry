# LMDB2GraphQL

A **read-only GraphQL adapter over a [strfry](https://github.com/hoytech/strfry) Nostr relay's LMDB database.** It exposes strfry's events as a directly queryable GraphQL endpoint, letting you run rich queries the Nostr `REQ` protocol can't express — e.g. *"the latest 20 kind-1 events per pubkey"* — without going through the relay process.

It is a **query lens over strfry's live data, not a copy of it.** LMDB2GraphQL reads strfry's existing on-disk indexes directly (`MDB_RDONLY`, never a write txn) and never replicates event data into a separate store.

## Quick start

All you need is the compiled binary and a strfry LMDB database directory to point it at.

**1. Build the binary:**

```bash
cargo build --release
# → target/release/lmdb2graphql
```

**2. Point it at a database.** Config is read from `~/deepfry/lmdb2graphql.yaml`. The minimal config is the path to strfry's LMDB directory (the one containing `data.mdb`):

```bash
mkdir -p ~/deepfry
cat > ~/deepfry/lmdb2graphql.yaml <<'YAML'
strfry_db_path: /path/to/strfry-db

# Required (surfaced in `stats` / startup logs for drift detection — see Configuration):
pinned_strfry_version: "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
pinned_strfry_commit: "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
YAML
```

**3. Run it:**

```bash
./target/release/lmdb2graphql
```

It binds `127.0.0.1:8080`, opens the LMDB read-only, runs its startup gates, and starts serving.

**4. Query it:**

```bash
curl -s http://127.0.0.1:8080/graphql \
  -H 'content-type: application/json' \
  -d '{"query":"{ stats { eventCount dbVersion pinnedStrfryVersion } }"}'
```

Or open `http://127.0.0.1:8080/graphql` in a browser for the interactive GraphiQL playground.

> **What "startup gates" means:** before serving any data, the process opens the LMDB read-only, asserts the on-disk `Meta.dbVersion == 3` and that endianness matches the host, then runs a comparator self-check against committed golden vectors. If any gate fails it **exits non-zero** and `/ready` never returns 200 — fail-closed. This is why the database must be from a compatible strfry build (see below).

## Compatibility

LMDB2GraphQL reimplements strfry/golpe's custom LMDB comparators byte-for-byte and depends on strfry's internal index key formats. It is pinned to one strfry version (`dockurr/strfry@sha256:545555da…` / hoytech commit `f31a1b9d`) and treats strfry's internals as a private API. A database from a different strfry version may have incompatible indexes and will be rejected by the startup gate.

Building from source needs Rust (toolchain pinned via `rust-toolchain.toml`) and `lmdb.h` (`brew install lmdb` on macOS). If `cargo` reports "not available" on this machine, the toolchain needs explicit env:

```bash
export PATH="$HOME/.cargo/bin:$PATH"
export RUSTUP_TOOLCHAIN=stable-x86_64-apple-darwin
```

## Configuration

`~/deepfry/lmdb2graphql.yaml` (full reference at `config/lmdb2graphql.yaml.example`):

| Key | Required | Default | Purpose |
|-----|----------|---------|---------|
| `strfry_db_path` | **yes** | — | Directory containing strfry's `data.mdb` (the LMDB env dir). |
| `pinned_strfry_version` | **yes** | — | The strfry image ref this adapter targets. Surfaced in `stats`/logs so operators can spot drift if the parent image moves. |
| `pinned_strfry_commit` | **yes** | — | The corresponding hoytech/strfry git commit SHA. |
| `bind_address` | no | `127.0.0.1:8080` | HTTP listen address. |
| `map_size` | no | `10995116277760` (10 TiB) | LMDB map size in bytes — must be **≥** strfry's configured `dbParams.mapsize`. |

> 🔒 **`bind_address` defaults to loopback on purpose.** This endpoint is unauthenticated with full introspection and a GraphiQL playground. Binding a non-loopback address (e.g. `0.0.0.0:8080`) serves the entire strfry corpus to any host that can route to the box, so the process logs a loud `NON-LOOPBACK` warning. Only widen it deliberately.

## HTTP endpoints

| Method | Path | Behaviour |
|--------|------|-----------|
| `POST` | `/graphql` | GraphQL query execution. Returns **503** until startup gates pass (no data served while not ready). |
| `GET` | `/graphql` | GraphiQL playground (interactive query UI). |
| `GET` | `/health` | Liveness — **200** whenever the process is alive. |
| `GET` | `/ready` | Readiness — **200** only after the startup gates pass; **503** otherwise. |

Point "is it ready to serve traffic" checks at `/ready`; point process-restart healthchecks at `/health` (using `/ready` there would restart-loop during startup).

## GraphQL API

The schema is **query-only** (read-only adapter — no mutations). Three root queries:

### `events` — NIP-01-style filtered query with pagination

```graphql
query {
  events(
    filter: {
      authors: ["<64-char-lowercase-hex-pubkey>"]
      kinds: [1]
      since: 1700000000
      until: 1800000000
      tag: { name: "t", values: ["nostr"] }
    }
    limit: 100        # 1–500, default 100, silently clamped above 500
    after: null       # opaque cursor from the previous page's `endCursor`
  ) {
    events { id pubkey kind createdAt content tags sig raw }
    hasMore
    endCursor
  }
}
```

Paginate by passing the previous response's `endCursor` as `after` until `hasMore` is `false`.

### `latestPerAuthor` — latest N events per pubkey, grouped by author

```graphql
query {
  latestPerAuthor(
    kind: 1
    perAuthor: 20     # 1–500, silently clamped
    authors: ["<pubkey-hex>", "<pubkey-hex>"]   # capped per server limit
  ) {
    author
    events { id kind createdAt content }
  }
}
```

### `stats` — counts and version/drift info

```graphql
query {
  stats {
    eventCount
    maxLevId
    dbVersion              # detected on-disk strfry DB version
    pinnedStrfryVersion    # the version this adapter targets — compare to spot drift
  }
}
```

**Field reference**

- **Event**: `id`, `pubkey`, `kind`, `createdAt`, `content`, `sig`, `tags` (`[[String]]`), `raw` (the exact retained JSON bytes).
- **EventFilterInput**: `ids`, `authors`, `kinds`, `since`, `until`, `tag { name, values }`.

## Running as part of the DeepFry stack (Docker)

LMDB2GraphQL is one component of [DeepFry](../CLAUDE.md), the backend stack around an unmodified strfry relay. For that deployment it ships as a Docker sidecar — **this is a packaging choice, not a runtime requirement; the binary above runs fine standalone.**

The container deployment adds: a kernel-enforced read-only mount (`strfry-db:ro` — defense-in-depth on top of the code's `MDB_RDONLY`), co-location with strfry on one host, and a reproducible static Alpine build pinned to the strfry digest.

```bash
# one-time: the shared network used across DeepFry services
docker network create deepfry-net

# bring it up alongside strfry
docker compose -f docker-compose.strfry.yml \
               -f docker-compose.lmdb2graphql.yml up -d
```

Notes specific to the stack deployment:

- **Set `bind_address: "0.0.0.0:8080"` in the config.** Inside a container, `127.0.0.1` binds the container's loopback and is unreachable from elsewhere. The compose `127.0.0.1:8080:8080` publish rule is the actual host-level exposure control.
- **`deepfry-net`** is an external Docker network so *other stack services* can reach this GraphQL endpoint by container name (the host port is loopback-only). The adapter reads strfry's data through the `:ro` volume mount, **not** over the network — so `deepfry-net` is for inbound consumers, not for talking to strfry. Standalone runs don't need it.
- The compose healthcheck uses `/health` (not `/ready`).

## Development

```bash
cargo build              # compile
cargo test --all-targets # full test suite
```

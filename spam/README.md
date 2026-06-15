# LMDB2GraphQL

A **read-only GraphQL adapter over a [strfry](https://github.com/hoytech/strfry) Nostr relay's LMDB database.** It exposes strfry's events as a directly queryable GraphQL endpoint, letting you run rich queries the Nostr `REQ` protocol can't express — e.g. *"the latest 20 kind-1 events per pubkey"* — without going through the relay process.

It is a **query lens over strfry's live data, not a copy of it.** LMDB2GraphQL reads strfry's existing on-disk indexes directly (`MDB_RDONLY`, never a write txn) and never replicates event data into a separate store, honoring the DeepFry stack rule: *no event payloads outside strfry*.

> ⚠️ **Coupling & version gate.** This adapter reimplements strfry/golpe's custom LMDB comparators byte-for-byte and depends on strfry's internal index key formats. It is pinned to one strfry version and **refuses to start** unless the on-disk `Meta.dbVersion == 3` and endianness matches the host. Treat strfry's internals as a private API.

---

## Requirements

- A strfry LMDB database directory (the one containing `data.mdb`), readable by this process.
- The **pinned** strfry build: `dockurr/strfry@sha256:545555da…` (strfry 1.1.0 / hoytech commit `f31a1b9d`). A different strfry version may have incompatible indexes.
- To build from source: Rust (toolchain pinned via `rust-toolchain.toml`) and `lmdb.h` (`brew install lmdb` on macOS).

## Configuration

LMDB2GraphQL reads a single YAML file. Bare-metal runs read it from `~/deepfry/lmdb2graphql.yaml`; the Docker image reads it from `/root/deepfry/lmdb2graphql.yaml` (mounted). Copy the example and edit:

```bash
cp config/lmdb2graphql.yaml.example ~/deepfry/lmdb2graphql.yaml
```

| Key | Purpose |
|-----|---------|
| `strfry_db_path` | Directory containing strfry's `data.mdb` (the LMDB env dir). |
| `bind_address` | HTTP listen address. Use `127.0.0.1:8080` bare-metal; **`0.0.0.0:8080` inside Docker** (see note below). |
| `map_size` | LMDB map size in bytes — must be **≥** strfry's configured `dbParams.mapsize` (default 10 TiB). |
| `pinned_strfry_version` | The pinned strfry image ref (full `sha256` digest). Surfaced in `stats`/startup logs for drift detection. |
| `pinned_strfry_commit` | The pinned hoytech/strfry git commit SHA. |

> 🔒 **Non-loopback binds are loud on purpose.** This endpoint is unauthenticated with full introspection and a GraphiQL playground. Binding a non-loopback address logs a `NON-LOOPBACK` warning. Inside Docker, `0.0.0.0` only binds the container namespace — the compose `127.0.0.1:8080:8080` publish rule is the real host-level exposure control.

## Running

### Bare-metal / dev

```bash
# from the crate root (this directory)
cargo run --release
```

On startup it: loads config → binds the HTTP socket → runs the gate chain (open LMDB read-only → assert `dbVersion`/endianness → comparator self-check against committed golden vectors) → marks itself ready. **Fail-closed:** if any gate fails the process exits non-zero and `/ready` never reaches 200.

### Docker (DeepFry stack)

Packaged as a sidecar co-located with strfry, mounting `strfry-db` read-only.

```bash
# one-time: the shared network
docker network create deepfry-net

# bring it up alongside strfry
docker compose -f docker-compose.strfry.yml \
               -f docker-compose.lmdb2graphql.yml up -d
```

The compose service mounts the strfry LMDB at `/app/strfry-db:ro`, publishes the GraphQL endpoint on loopback only, and uses `/health` (not `/ready`) for its healthcheck.

## HTTP endpoints

| Method | Path | Behaviour |
|--------|------|-----------|
| `POST` | `/graphql` | GraphQL query execution. Returns **503** until startup gates pass (no data served while not ready). |
| `GET` | `/graphql` | GraphiQL playground (interactive query UI). |
| `GET` | `/health` | Liveness — **200** whenever the process is alive. Use this for container healthchecks. |
| `GET` | `/ready` | Readiness — **200** only after the LMDB env opens and the comparator self-check passes; **503** otherwise. |

`/health` vs `/ready`: point load balancers / "is it up" checks at `/ready`; point container restart healthchecks at `/health` (using `/ready` would restart-loop during startup).

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
    pinnedStrfryVersion    # the version this adapter was built against — compare to spot drift
  }
}
```

#### Field reference

- **Event**: `id`, `pubkey`, `kind`, `createdAt`, `content`, `sig`, `tags` (`[[String]]`), `raw` (the exact retained JSON bytes).
- **EventFilterInput**: `ids`, `authors`, `kinds`, `since`, `until`, `tag { name, values }`.

### Quick query from the shell

```bash
curl -s http://127.0.0.1:8080/graphql \
  -H 'content-type: application/json' \
  -d '{"query":"{ stats { eventCount dbVersion pinnedStrfryVersion } }"}'
```

## Development

```bash
cargo build              # compile
cargo test --all-targets # full test suite
```

If `cargo` reports "not available" on this machine, the toolchain needs explicit env (cargo isn't on PATH and the toolchain pin must be overridden):

```bash
export PATH="$HOME/.cargo/bin:$PATH"
export RUSTUP_TOOLCHAIN=stable-x86_64-apple-darwin
```

## How it fits the DeepFry stack

LMDB2GraphQL is one component of [DeepFry](../CLAUDE.md), the backend stack around an unmodified strfry relay. It never writes to strfry's database and never copies event payloads out of it — it only opens strfry's LMDB read-only and serves queries over the live indexes.

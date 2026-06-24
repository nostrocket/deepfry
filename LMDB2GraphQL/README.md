# LMDB2GraphQL

A **read-only GraphQL endpoint over a [strfry](https://github.com/hoytech/strfry) relay's LMDB database.** It reads strfry's on-disk data directly (never writes, never copies it) and answers rich queries the Nostr `REQ` protocol can't — e.g. *"the latest 20 kind-1 events per author."*

## Run it

```bash
# 1. Build
cargo build --release            # → target/release/lmdb2graphql

# 2. Configure: point it at strfry's LMDB dir (the one with data.mdb)
mkdir -p ~/deepfry
cat > ~/deepfry/lmdb2graphql.yaml <<'YAML'
strfry_db_path: /path/to/strfry-db
pinned_strfry_version: "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
pinned_strfry_commit: "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
YAML

# 3. Run (serves http://127.0.0.1:8080)
./target/release/lmdb2graphql
```

## Query it

```bash
curl -s http://127.0.0.1:8080/graphql -H 'content-type: application/json' \
  -d '{"query":"{ stats { eventCount dbVersion } }"}'
```

Or open `http://127.0.0.1:8080/graphql` in a browser for the GraphiQL playground.

Queries: `events` (filtered + paginated), `latestPerAuthor` (top-N per pubkey), `authors` (distinct pubkeys, paginated), `stats`. Full API, limits, and errors: see [`contract.md`](contract.md).

## Endpoints

| Path | Method | Purpose |
|------|--------|---------|
| `/graphql` | POST | Run queries (`503` until startup gates pass) |
| `/graphql` | GET | GraphiQL playground |
| `/health` | GET | Liveness — `200` while alive |
| `/ready` | GET | Readiness — `200` once gates pass |

## Good to know

- **Read-only & version-pinned.** Opens LMDB with `MDB_RDONLY` and reimplements strfry's index comparators byte-for-byte, so it's pinned to one strfry version (`f31a1b9d`). On startup it asserts `dbVersion == 3` + matching endianness and self-checks the comparators; **any mismatch exits non-zero** (fail-closed). A DB from a different strfry build will be rejected.
- **Loopback by default.** `bind_address` defaults to `127.0.0.1:8080` because the endpoint is unauthenticated with full introspection. Set `0.0.0.0:8080` only deliberately (it logs a warning). CORS is wildcard, so any browser origin can query it.
- **Config reference:** `config/lmdb2graphql.yaml.example`. Build needs Rust (pinned via `rust-toolchain.toml`) and `lmdb.h` (`brew install lmdb` on macOS).
- **DeepFry / Docker:** ships as a Docker sidecar in the [DeepFry](../CLAUDE.md) stack (read-only `strfry-db:ro` mount, co-located with strfry). Standalone runs need none of that — the binary above is enough.

## Develop

```bash
cargo build               # compile
cargo test --all-targets  # full test suite
```

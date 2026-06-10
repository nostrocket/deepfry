# Technology Stack

**Analysis Date:** 2026-06-10

## Languages & Runtimes

**Primary:**
- Go 1.24.1+ — all production subsystems (`event-forwarder`, `whitelist-plugin`, `web-of-trust`, `quarantine-rescuer`)
  - `event-forwarder/go.mod`: `go 1.24.1`, toolchain `go1.24.2`
  - `whitelist-plugin/go.mod`: `go 1.24.2`
  - `web-of-trust/go.mod`: `go 1.24.1`
  - `quarantine-rescuer/go.mod`: `go 1.24.2`

**Build target:**
- Static Linux/amd64 binaries (`CGO_ENABLED=0`, `-tags netgo`, `-extldflags '-static'`)
- Built for Alpine containers via multi-stage Docker builds

## Frameworks & Libraries

**Nostr Protocol:**
- `github.com/nbd-wtf/go-nostr v0.52.x` — NIP-01 WebSocket relay client, event signing, filter subscriptions; used in all four Go modules

**Graph Database Client:**
- `github.com/dgraph-io/dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d` — Dgraph gRPC client; `web-of-trust` only
- `google.golang.org/grpc v1.75.1` — gRPC transport for Dgraph; `web-of-trust` only

**Configuration:**
- `github.com/spf13/viper v1.21.0` — YAML config loading; `whitelist-plugin`, `quarantine-rescuer`
- `github.com/spf13/viper v1.18.2` — same library, older pin; `web-of-trust`

**JSON / Serialization:**
- `github.com/tidwall/gjson v1.18.0` — fast JSON path reads; transitive via go-nostr
- `github.com/json-iterator/go v1.1.12` — faster `encoding/json` drop-in; transitive
- `github.com/mailru/easyjson v0.9.0` — code-gen JSON; transitive

**WebSocket:**
- `github.com/coder/websocket v1.8.x` — WebSocket transport; transitive via go-nostr

**Cryptography:**
- `github.com/btcsuite/btcd/btcec/v2` — secp256k1 elliptic curve; transitive (Nostr key signing)
- `github.com/decred/dcrd/dcrec/secp256k1/v4` — secp256k1 primitives; transitive

**TUI (event-forwarder only):**
- `github.com/rivo/tview v0.42.0` — terminal UI
- `github.com/gdamore/tcell/v2 v2.8.1` — terminal cell library

**Concurrency:**
- `github.com/puzpuzpuz/xsync/v3` — sharded concurrent maps; transitive via go-nostr
- `github.com/sourcegraph/conc` — structured concurrency helpers; transitive via viper

## Build & Tooling

**Build system:**
- `make` — each subsystem has its own `Makefile` with targets: `build`, `test`, `lint`, `lint-fix`, `fmt`, `vet`, `tidy`, `clean`
- `event-forwarder` adds: `build-alpine`, `docker-build`, `test-integration`
- `whitelist-plugin` adds: `bench`, `build-alpine`
- `web-of-trust` adds: `build-crawler`, `build-pubkeys`

**Version injection:**
- ldflags at build: `Version`, `Commit`, `Built` injected into `pkg/version` package
- Whitelist server uses `-buildvcs=true` to stamp git metadata via `runtime/debug.ReadBuildInfo`

**Linting:**
- `golangci-lint` — configured per subsystem (warns, does not fail CI)

**Testing:**
- Standard `go test` with `-short` flag for unit tests
- Integration tests use `-tags=integration` build tag (`event-forwarder`)

## Infrastructure

**Containerization:**
- Docker with multi-stage builds (builder: `golang:1.24-alpine`, runtime: `alpine:latest` or `dockurr/strfry:latest`)
- `Dockerfile.strfry` — builds whitelist and router plugins, copies into `dockurr/strfry:latest`
- `Dockerfile.whitelist-server` — standalone HTTP server binary on `alpine:latest`
- `event-forwarder/Dockerfile` — standalone forwarder binary

**Orchestration:**
- Docker Compose (three compose files, split by concern):
  - `docker-compose.dgraph.yml` — Dgraph standalone + Ratel UI + whitelist-server
  - `docker-compose.strfry.yml` — mainline StrFry relay + quarantine StrFry relay
  - `docker-compose.evtfwd.yml` — six event-forwarder instances (3 live + 3 historical)
- Shared bridge network: `deepfry-net`

**Resource limits (per forwarder container):**
- CPU: 0.5 cores max, 0.1 reserved
- Memory: 128 MB max, 32 MB reserved

**Logging:**
- Docker `json-file` driver, 10 MB max per file, 3 files retained

**CI/CD:**
- No CI pipeline detected in repository

**Secrets:**
- Environment variables via `.env` file (`.env.example` present at repo root)
- Key env vars: `STRFRY_PRIVATE_KEY`, `NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`

---

*Stack analysis: 2026-06-10*

# Technology Stack

**Analysis Date:** 2026-06-09

## Languages

**Primary:**
- Go 1.24.1 - Core implementation language for all binaries (crawler, clusterscan, pubkeys exporter, relay discovery, healthcheck). Module declared in `go.mod`.

**Secondary:**
- POSIX shell - `Makefile` build/test orchestration (cross-platform with a Windows branch)

## Runtime

**Environment:**
- Go 1.24.1+ (minimum per `go.mod` line 3 and `CLAUDE.md`). Local toolchain observed: go1.26.1.
- Single statically-linkable binary per command under `cmd/`; no external runtime services bundled.

**Package Manager:**
- Go Modules
- Lockfile: `go.sum` present (sibling of `go.mod`)

## Frameworks

**Core:**
- `github.com/nbd-wtf/go-nostr` v0.52.0 - Nostr protocol client. Relay WebSocket connect/subscribe/publish (NIP-01). Used in `pkg/crawler/crawler.go`, plus `nip11` (`cmd/discover-relays/main.go`) and `nip19` (`pkg/config/config.go`).
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Dgraph gRPC client. Sole interface to the graph store; wrapped in `pkg/dgraph/dgraph.go`.
- `github.com/spf13/viper` v1.18.2 - YAML config loader/writer; reads and persists `~/deepfry/web-of-trust.yaml` (`pkg/config/config.go`).
- `google.golang.org/grpc` v1.75.1 - gRPC transport underlying the Dgraph client (`pkg/dgraph/dgraph.go`).

**Testing:**
- Go standard `testing` package - No third-party assertion/mock framework detected.
- Build-tagged integration tests via `//go:build integration` (e.g. `pkg/dgraph/dgraph_stale_test.go`), gated behind `make test-integration`.

**Build/Dev:**
- `make` - All build/test/lint targets in `Makefile`.
- `golangci-lint` - Optional linter; Makefile `lint`/`lint-fix` targets warn but never fail if absent.
- `go fmt` / `go vet` - Standard tooling via `make fmt` / `make vet`.

## Key Dependencies

**Critical:**
- `github.com/nbd-wtf/go-nostr` v0.52.0 - Only path to relay communication; must remain NIP-01 (WebSocket) compatible.
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Exclusive Dgraph client; all pubkey-graph mutations and queries flow through it via gRPC.
- `google.golang.org/grpc` v1.75.1 - Low-level transport for the Dgraph client.
- `google.golang.org/protobuf` v1.36.9 / `github.com/gogo/protobuf` v1.3.2 - Protobuf serialization for the Dgraph API.

**Infrastructure:**
- `gopkg.in/yaml.v3` v3.0.1 - YAML parsing in `cmd/discover-relays/main.go` (writes discovered relays back to config).
- `github.com/mitchellh/mapstructure` v1.5.0 - viper struct decoding (`mapstructure:"..."` tags on `config.Config`).
- `github.com/coder/websocket` v1.8.14 - Low-level WebSocket transport beneath go-nostr (transitive).
- `github.com/bytedance/sonic` v1.14.1 - High-performance JSON codec (transitive via go-nostr).
- Cryptography (Nostr key handling, transitive): `github.com/btcsuite/btcd/btcec/v2` v2.3.5, `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.4.0, `github.com/decred/dcrd/crypto/blake256` v1.1.0.
- Concurrency (transitive): `go.uber.org/atomic` v1.9.0, `github.com/sourcegraph/conc` v0.3.0, `github.com/puzpuzpuz/xsync/v3` v3.5.1.

## Configuration

**Environment:**
- No `.env` files in this subsystem; no `os.Getenv` config path observed. Configuration is file-based via YAML.
- Config file: `~/deepfry/web-of-trust.yaml` (auto-created with defaults on first run by `LoadConfig` in `pkg/config/config.go`). The `deepfry` directory is created with mode `0755` if missing.
- Key config values (`pkg/config/config.go`): `relay_urls`, `dgraph_addr` (default `localhost:9080`), `pubkey` (seed; accepts hex or npub), `timeout` (default `30s`), `stale_pubkey_threshold` (default 86400s), `forward_relay_url`, and clusterscan tuning (`seed_pubkeys`, `trust_k`, `cluster_depth`, `max_bridge_weight`, `min_cluster_size`).
- Both hex and npub pubkey formats accepted; npub decoded to hex via `nip19.Decode`.

**Build:**
- `Makefile` - Per-command build targets (`build-crawler`, `build-pubkeys`, `build-discover-relays`, `build-healthcheck`, `build-clusterscan`); output to `bin/`.
- Version injection via ldflags into `web-of-trust/pkg/version` (`Version`, `Commit`, `Built`) from `VERSION` env var, `git rev-parse --short HEAD`, and build timestamp.
- No `Dockerfile` or CI config inside this subsystem. Container/orchestration lives at the repo root (`docker-compose.dgraph.yml`, etc.).

## Platform Requirements

**Development:**
- Go 1.24.1+ toolchain
- POSIX shell (Makefile; Windows branch exists for cross-platform builds)
- Read/write access to `~/deepfry/`
- A reachable Dgraph gRPC endpoint (default `localhost:9080`) for integration tests and runtime
- Outbound WebSocket (wss://) connectivity to Nostr relays

**Production:**
- Long-running crawler process (or one-shot CLIs) executed on the StrFry host
- Connectivity to Dgraph gRPC (`localhost:9080`) and to public Nostr relays
- Static binary build is feasible (`CGO_ENABLED=0`) per parent `CLAUDE.md` Alpine guidance; no CGO dependencies observed

---

*Stack analysis: 2026-06-09*

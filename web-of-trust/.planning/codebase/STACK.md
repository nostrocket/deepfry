# Technology Stack

**Analysis Date:** 2026-06-09

## Languages

**Primary:**
- Go 1.24.1 - Core implementation language for all subsystems (crawler, clusterscan, pubkeys exporter, relay discovery, healthcheck)

## Runtime

**Environment:**
- Go 1.24.1+ (required per CLAUDE.md)

**Package Manager:**
- Go Modules (go.mod, go.sum)
- Lockfile: `go.sum` (present)

## Frameworks

**Core:**
- `github.com/nbd-wtf/go-nostr` v0.52.0 - Nostr protocol client library, WebSocket relay communication (NIP-01)

**Data & Storage:**
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Dgraph gRPC client for graph database access

**Configuration:**
- `github.com/spf13/viper` v1.18.2 - YAML configuration loader (reads `~/deepfry/web-of-trust.yaml`)

**RPC & Networking:**
- `google.golang.org/grpc` v1.75.1 - gRPC transport for Dgraph communication (native gRPC client)
- `google.golang.org/protobuf` v1.36.9 - Protocol Buffers support (Dgraph API)

## Key Dependencies

**Critical:**
- `github.com/nbd-wtf/go-nostr` v0.52.0 - Why it matters: only dependency for relay communication; must stay compatible with NIP-01 WebSocket protocol
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Why it matters: exclusive gRPC client for Dgraph backend; all pubkey graph mutations/queries flow through this

**Infrastructure:**
- `google.golang.org/grpc` v1.75.1 - Low-level gRPC transport; Dgraph depends on it
- `gopkg.in/yaml.v3` v3.0.1 - YAML parsing for config files
- `github.com/bytedance/sonic` v1.14.1 - High-performance JSON codec (via go-nostr transitive)

**Indirect (transitives):**
- Cryptography: `github.com/btcsuite/btcd/btcec/v2` v2.3.5, `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.4.0 (Nostr key handling)
- Protobuf support: `github.com/gogo/protobuf` v1.3.2 (Dgraph API serialization)
- Concurrency: `go.uber.org/atomic` v1.9.0, `sourcegraph/conc` v0.3.0
- WebSocket: `github.com/coder/websocket` v1.8.14 (low-level relay transport)

## Configuration

**Environment:**
- Config files: `~/deepfry/web-of-trust.yaml` (YAML format, auto-created if missing)
- Default Nostr relays: damus.io, nos.lol, relay.nostr.band, nostr-pub.wellorder.net, relay.primal.net
- Default Dgraph address: `localhost:9080` (gRPC)
- Default timeout: 30s

**Build:**
- Makefile targets: `make build`, `make build-crawler`, `make build-pubkeys`, `make build-discover-relays`, `make build-healthcheck`, `make build-clusterscan`
- Version injection: Git commit hash + build timestamp via ldflags
- Output directory: `bin/` (per subsystem)

## Platform Requirements

**Development:**
- Go 1.24.1+
- POSIX shell (for Makefile)
- Access to `~/deepfry/` config directory (auto-created on first load)

**Production:**
- Go 1.24.1+ runtime or static binary (Alpine-compatible via `CGO_ENABLED=0` if needed)
- Connectivity to Dgraph gRPC endpoint (default: `localhost:9080`)
- Connectivity to Nostr relays (WebSocket, wss://)
- Read/write access to `~/deepfry/web-of-trust.yaml`

---

*Stack analysis: 2026-06-09*

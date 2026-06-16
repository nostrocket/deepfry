# Technology Stack

**Analysis Date:** 2026-06-16

## Languages

**Primary:**
- Go 1.24.1 - Core implementation language for all subsystems (crawler, clusterscan, pubkeys exporter, relay discovery, healthcheck)

## Runtime

**Environment:**
- Go 1.24.1+ (required per CLAUDE.md)

**Package Manager:**
- Go Modules (go.mod)
- Lockfile: `go.sum` (present)

## Frameworks

**Core:**
- `github.com/nbd-wtf/go-nostr` v0.52.0 - Nostr protocol client library; WebSocket relay communication (NIP-01)
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Dgraph gRPC client for graph database access
- `github.com/spf13/viper` v1.18.2 - YAML configuration loader (reads `~/deepfry/web-of-trust.yaml`)
- `google.golang.org/grpc` v1.75.1 - gRPC transport for Dgraph communication (native gRPC client)

**Configuration:**
- `gopkg.in/yaml.v3` v3.0.1 - YAML parsing for config files and discovery relay list updates

## Key Dependencies

**Critical:**
- `github.com/nbd-wtf/go-nostr` v0.52.0 - Why it matters: only dependency for relay communication; must stay compatible with NIP-01 WebSocket protocol; used in crawler subscription, event parsing, and NIP-65 relay discovery
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Why it matters: exclusive gRPC client for Dgraph backend; all pubkey graph mutations/queries flow through this in `pkg/dgraph/`; max recv message size tuned to 256MB for large follow-list payloads

**Protocol & Cryptography:**
- `github.com/nbd-wtf/go-nostr/nip19` - Nostr public key format support (npub/hex conversion in `pkg/config/config.go`)
- `github.com/nbd-wtf/go-nostr/nip11` - NIP-11 relay info document fetching for relay discovery (`cmd/discover-relays/main.go`)
- `github.com/btcsuite/btcd/btcec/v2` v2.3.5 - Nostr key handling (secp256k1 signatures, via go-nostr transitive)
- `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.4.0 - Alternative secp256k1 implementation (via go-nostr transitive)

**Infrastructure:**
- `google.golang.org/protobuf` v1.36.9 - Protocol Buffers support for Dgraph gRPC API serialization
- `google.golang.org/grpc/codes` and `google.golang.org/grpc/status` - gRPC error handling in `cmd/crawler/main.go` for transient error classification

**JSON & Serialization:**
- `github.com/bytedance/sonic` v1.14.1 - High-performance JSON codec (via go-nostr transitive)
- `gopkg.in/yaml.v3` v3.0.1 - YAML unmarshalling/marshalling for config and relay discovery

**Concurrency & Utilities:**
- `go.uber.org/atomic` v1.9.0 - Atomic operations for relay failure counters in `pkg/crawler/crawler.go` (per-class counters: transport, filter_rejection, subscription_flap)
- `sourcegraph/conc` v0.3.0 - Structured concurrency utilities
- `github.com/coder/websocket` v1.8.14 - Low-level WebSocket transport for relay connections

**Configuration & Helpers:**
- `github.com/spf13/cast` v1.6.0 - Type casting for config values
- `github.com/spf13/pflag` v1.0.5 - Command-line flag parsing (used in discover-relays, clusterscan CLIs)
- `github.com/spf13/afero` v1.11.0 - Abstract filesystem interface (via viper transitive)
- `github.com/gogo/protobuf` v1.3.2 - Protobuf support (Dgraph API serialization)

## Configuration

**Environment:**
- Config files: `~/deepfry/web-of-trust.yaml` (YAML format, auto-created if missing via `pkg/config/config.go`)
- Relay URLs: Default Nostr relays (damus.io, nos.lol, relay.nostr.band, nostr-pub.wellorder.net, relay.primal.net) if not overridden
- Dgraph address: `localhost:9080` (gRPC default, configurable via `web-of-trust.yaml`)
- Default timeout: 15s (TIMEOUT-01 phase parameter, configurable)
- Relay filter batch size: 100 (configurable)
- Default stale pubkey threshold: 24 hours (86400 seconds, configurable)
- Relay ejection thresholds (Phase 7): transport=10, filter_rejection=3, subscription_flap=5 (configurable, safety-guarded against DoS)
- EOSE quorum (Phase 8): 70% of relays must reach EOSE or error before batch cancels (configurable, TIMEOUT-02)
- Miss-backoff parameters (Phase 8): base=2h, ratio=2, cap=168h (7 days), hit_refresh_cadence=24h (configurable, safety-guarded against starvation)

**Build:**
- Makefile targets: `make build`, `make build-crawler`, `make build-pubkeys`, `make build-discover-relays`, `make build-healthcheck`, `make build-clusterscan`
- Version injection: Git commit hash + build timestamp via ldflags (`-X 'web-of-trust/pkg/version.Version=$(VERSION)'`, `-X 'web-of-trust/pkg/version.Commit=$(GIT_COMMIT)'`, `-X 'web-of-trust/pkg/version.Built=$(BUILD_TIME)'`)
- Cross-platform support: Windows (.exe) and Unix binaries built via conditional logic in Makefile
- Output directory: `bin/` (per subsystem)

## Platform Requirements

**Development:**
- Go 1.24.1+
- POSIX shell (for Makefile)
- Access to `~/deepfry/` config directory (auto-created on first load)
- golangci-lint (optional, for linting; tool gracefully handles absence)

**Production:**
- Go 1.24.1+ runtime or static binary (Alpine-compatible via `CGO_ENABLED=0` if needed)
- Connectivity to Dgraph gRPC endpoint (default: `localhost:9080`, configurable)
- Connectivity to Nostr relays (WebSocket, wss://)
- Read/write access to `~/deepfry/web-of-trust.yaml`

---

*Stack analysis: 2026-06-16*

# Code Conventions

**Analysis Date:** 2026-06-10

## Style & Formatting

**Formatter:** `gofumpt` (stricter superset of `gofmt`) — configured in `event-forwarder/.golangci.yml`.

**Linting:** `golangci-lint` with these linters enabled:
- `errcheck` — all errors must be handled
- `govet`, `staticcheck`, `typecheck` — correctness
- `gosimple`, `ineffassign`, `unused` — dead code
- `gocyclo` — cyclomatic complexity guard
- `revive` — style enforcement

Lint runs warn but do not fail CI (`make lint` is advisory). `make lint-fix` auto-corrects where possible.

**Naming:**
- Packages: lowercase single word matching directory name (`forwarder`, `whitelist`, `nsync`, `testutil`)
- Exported types: PascalCase (`Forwarder`, `WhiteList`, `ConnectionManager`)
- Unexported fields/functions: camelCase (`syncTracker`, `connMgr`, `currentSyncMode`)
- Constants: PascalCase for exported (`SyncModeWindowed`, `RealtimeToleranceSeconds`), camelCase for unexported
- Test helpers: `makeKey`, `genKeys`, `newSilentLogger` — lowercase, concise, in-package

## Patterns & Idioms

**Constructor pattern:** `New(cfg, logger, deps...)` returns a pointer to the struct. Integration-injectable variant `NewWithRelays(cfg, logger, src, dst, telemetry)` accepts interface dependencies for testability. Pattern seen in:
- `event-forwarder/pkg/forwarder/forwarder.go` (`New`, `NewWithRelays`)
- `whitelist-plugin/pkg/whitelist/whitelist.go` (`NewWhiteList`)

**Interface-driven design:** Behaviour is always extracted to a named interface before injection. Concrete types satisfy interfaces without declaring it. Key interfaces:
- `relay.Relay` — `event-forwarder/pkg/relay/interface.go`
- `ConnectionManager`, `WindowManager`, `SyncStrategy`, `TelemetrySink` — `event-forwarder/pkg/forwarder/interfaces.go`
- `handler.Handler`, `repository.Repository` — `whitelist-plugin/pkg/handler/handler.go`, `whitelist-plugin/pkg/repository/repository.go`

**Error handling:**
- Errors are wrapped with context using `fmt.Errorf("action (key=value, ...): %w", err)`. Error messages include structured key=value fields inline: `"failed to query events from relay %s (window: %s to %s, batch_limit: %d): %w"`.
- Errors are never swallowed silently. All callsites check returned errors.
- No third-party error library (no `pkg/errors` or similar) — stdlib `errors` and `fmt.Errorf` only.

**Context threading:** `context.Context` is the first parameter on every method that performs I/O or could block. Cancellation is honoured at all blocking points (channel selects, relay calls, LMDB iteration).

**Struct field visibility:** Config/data structs exported where needed by other packages; internal orchestration structs (`Forwarder`, `ConnectionManager`) are unexported except the type name itself.

**Noop/stub pattern:** Noop implementations of interfaces live alongside the interface:
- `event-forwarder/pkg/telemetry/noop.go` — `NewNoopPublisher()`
- Tests use `testutil.NewCapturingPublisher()` for observable telemetry — `event-forwarder/pkg/testutil/telemetry_capture.go`

## Package Structure

Each subsystem is an independent Go module under its own directory:

```
<subsystem>/
├── cmd/<binary-name>/main.go   # Entry point, wires dependencies
├── pkg/<concern>/              # Exported packages (event-forwarder, whitelist-plugin)
├── internal/<concern>/         # Internal packages (quarantine-rescuer)
└── Makefile
```

`cmd/` packages are thin: parse flags, build config, construct dependencies, call `run(ctx, flags, logger)`. Business logic lives in `pkg/` or `internal/`.

`testutil` packages collect shared mock/stub/fixture types used across multiple test packages:
- `event-forwarder/pkg/testutil/` — `MockRelay`, `CapturingPublisher`, constants (`TestSKHex`, `TestPK`)

## Configuration

**event-forwarder:** `config.Config` struct populated from CLI flags + env vars via `pkg/config/` with a layered resolver (`sources.go`, `resolver.go`, `flags.go`). Validated in `pkg/config/validation.go`.

**whitelist-plugin / web-of-trust:** YAML config files read from `~/deepfry/` at startup using `github.com/spf13/viper`. Config structs live in `pkg/config/config.go` (web-of-trust) and `internal/whitelist/config.go` (quarantine-rescuer).

**Secrets:** Always via environment variables. Never logged raw. `.env` file loaded at Docker Compose level.

**Version injection:** Build-time `ldflags` inject `Version`, `Commit`, `Built` into a `version` package (e.g., `event-forwarder/pkg/version/version.go`).

## Logging & Observability

**Two logging styles coexist by subsystem:**

- `log.New(os.Stderr, "[prefix] ", log.LstdFlags)` — standard library logger, used in `event-forwarder` and `whitelist-plugin`. Logger is injected as `*log.Logger` through constructors; never used as a global.
- `log/slog` with JSON output to stderr — used in `quarantine-rescuer`. Logger configured with level from `--log-level` flag, set as default via `slog.SetDefault`. Structured fields via `slog.Logger.With` / key-value pairs.

**Telemetry:** `event-forwarder` publishes structured telemetry events (errors, window updates, relay events) via the `TelemetryPublisher` interface (`pkg/telemetry/`). Production uses a publisher; tests use `NoopPublisher` or `CapturingPublisher`. No external metrics system (no Prometheus/OpenTelemetry).

**No distributed tracing.** No metrics export. Observability is stderr logs only.

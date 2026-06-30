# Phase 4: Ops & Integration - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 5
**Analogs found:** 5 / 5

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `whitelist-plugin/Makefile` | build config | batch | existing `build-router*` targets (lines 41–96) | exact |
| `Dockerfile.strfry` | container image | batch | existing `/router` build + COPY block (lines 9–13, 17) | exact |
| `config/strfry/strfry.conf` | relay config | config | existing `writePolicy.plugin` line (line 117) | exact |
| `docker-compose.strfry.yml` | compose | config | existing `whitelist.yaml` volume mount (line 23) | exact |
| `whitelist-plugin/README.md` | docs | — | existing "Router Plugin" section (lines 214–290) | exact |

---

## Pattern Assignments

### `whitelist-plugin/Makefile` — add bloom build targets (OPS-01)

**Analog:** existing `router` targets in `whitelist-plugin/Makefile`

**Variable declaration pattern** (line 3):
```makefile
APP_ROUTER=router
```
Mirror with:
```makefile
APP_BLOOM=bloom
```

**.PHONY and `all` extension** (lines 28–30):
```makefile
.PHONY: all build build-server build-router run test fmt vet tidy clean help bench build-alpine build-linux build-server-alpine build-server-linux build-router-alpine build-router-linux

all: build build-server build-router
```
Add `build-bloom build-bloom-alpine build-bloom-linux` to `.PHONY` and `build-bloom` to `all`.

**Native build target** (lines 41–43):
```makefile
## Build the router plugin (quarantine-routing alternative to the whitelist plugin)
build-router:
	go build $(BUILD_FLAGS) -o bin/$(APP_ROUTER)$(BINARY_EXT) ./cmd/$(APP_ROUTER)
```

**Alpine static target** (lines 81–87):
```makefile
## Build static router plugin binary for Alpine Linux (musl libc)
build-router-alpine:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP_ROUTER)-alpine ./cmd/$(APP_ROUTER)
	@echo "Built static router binary for Alpine Linux: bin/$(APP_ROUTER)-alpine"
```

**Generic Linux static target** (lines 89–96):
```makefile
## Build static router plugin binary for generic Linux
build-router-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP_ROUTER)-linux ./cmd/$(APP_ROUTER)
	@echo "Built static router binary for Linux: bin/$(APP_ROUTER)-linux"
```

**help block rows** (lines 131–148, extract pattern):
```makefile
	@echo   build-router         - Build the router plugin
	@echo   build-router-alpine  - Build static router plugin binary for Alpine Linux
	@echo   build-router-linux   - Build static router plugin binary for generic Linux
```
Add corresponding `build-bloom`, `build-bloom-alpine`, `build-bloom-linux` rows in the same style.

---

### `/Users/g/git/deepfry/Dockerfile.strfry` — add bloom build step and COPY (OPS-02)

**Analog:** existing `/router` build step and COPY (lines 9–13, 17)

**Existing plugin-builder RUN blocks** (lines 4–13):
```dockerfile
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags "-w -s -extldflags '-static'" \
    -tags netgo \
    -o /whitelist ./cmd/whitelist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags "-w -s -extldflags '-static'" \
    -tags netgo \
    -o /router ./cmd/router
```
Add a third `RUN` block immediately after, mirroring exactly:
```dockerfile
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags "-w -s -extldflags '-static'" \
    -tags netgo \
    -o /bloom ./cmd/bloom
```

**Existing COPY lines** (lines 16–17):
```dockerfile
COPY --from=plugin-builder /whitelist /app/plugins/whitelist
COPY --from=plugin-builder /router /app/plugins/router
```
Add a third COPY immediately after:
```dockerfile
COPY --from=plugin-builder /bloom /app/plugins/bloom
```

---

### `/Users/g/git/deepfry/config/strfry/strfry.conf` — writePolicy plugin line (OPS-02)

**Analog:** the same file; no external analog needed.

**Current plugin selection line** (line 117):
```
plugin = "/app/plugins/router"
```

**Surrounding comment block for context** (lines 96–119):
```
writePolicy {
    # If non-empty, path to an executable script that implements the writePolicy plugin logic.
    #
    # Two plugin binaries ship in the image; pick one here:
    #
    #   /app/plugins/whitelist
    #     ...
    #
    #   /app/plugins/router  (default)
    #     ...
    #
    # Swapping between them is safe: restart strfry after changing this
    # line. The two plugins are behaviourally identical on the main-relay
    # accept/reject path; only the side-channel differs.
    plugin = "/app/plugins/router"
    # Number of seconds to search backwards for lookback events when starting the writePolicy plugin (0 for no lookback)
    lookbackSeconds = 0
}
```

**Plan action:** To activate bloom, change line 117 to `plugin = "/app/plugins/bloom"`. Extend the comment block to describe bloom as a third option. `whitelist` and `router` entries and their comments remain byte-identical.

Note: `config/strfry/strfry-quarantine.conf` uses a different plugin path (points at `/app/plugins/router`); per D-05/context, leave it unchanged.

---

### `/Users/g/git/deepfry/docker-compose.strfry.yml` — volume mounts and filter persistence (OPS-02)

**Analog:** existing `strfry` service volume block (lines 21–24).

**Current volume block** (lines 21–24):
```yaml
    volumes:
      - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db
      - ./config/strfry/strfry.conf:/etc/strfry.conf:ro
      - ./config/whitelist/whitelist.yaml:/root/deepfry/whitelist.yaml
      - ./config/whitelist/router.yaml:/root/deepfry/router.yaml
```

**Key observations for the plan:**
- `whitelist.yaml` is already bind-mounted at `/root/deepfry/whitelist.yaml` (line 23). Bloom reads its `bloom_`-prefixed config from this file (GATE-07 / D-05) — no new config mount is required.
- `/root/deepfry/` is a bind-mount into the container (lines 23–24 show files written there). However, the mount is per-file, not a directory mount. For bloom's persisted filter file (GATE-05), `/root/deepfry/` must itself be writable and survive restarts. The current mounts show no named volume or directory mount covering `/root/deepfry/`. The plan must add either a directory bind-mount (`./config/bloom:/root/deepfry`) or a named volume for `/root/deepfry/` so the bloom filter file persists across container restarts.
- Pattern for adding a named volume (follow the strfry-db pattern on line 21): add `- bloom-filter-data:/root/deepfry` under the `strfry` service volumes and declare `bloom-filter-data:` under a top-level `volumes:` key. Alternatively, a host-path bind (`${BLOOM_DATA_PATH:-./data/bloom}:/root/deepfry`) mirrors the `STRFRY_DB_PATH` pattern.

---

### `whitelist-plugin/README.md` — bloom plugin section (OPS-03)

**Analog:** "Router Plugin (optional)" section (lines 214–265), which is the closest structural match — it documents an opt-in alternative writePolicy plugin with its own config keys, behavior, and server integration.

**Section heading pattern** (line 214):
```markdown
## Router Plugin (optional)
```
Add `## Bloom Gate Plugin (optional)` at the same heading level, placed after the Router Plugin section.

**Plugin description pattern** (lines 214–231): one-paragraph description of what the plugin does, how it differs from the default, and how to activate it (single-line `strfry.conf` change).

**"How It Works" sub-section pattern** (lines 222–229):
```markdown
### How It Works

1. Receives an event from StrFry over stdin (same JSONL protocol as the whitelist plugin).
2. Calls the whitelist server (`GET /check/{pubkey}`).
3. Whitelisted → returns `accept`.
4. Not whitelisted → applies a **heuristic filter** ...
```
Mirror with bloom's numbered accept/reject/fetch flow.

**Configuration sub-section pattern** (lines 238–261):
```markdown
### Configuration

Config file: `~/deepfry/router.yaml` (auto-created with defaults if missing). Env overrides use the `ROUTER_` prefix ...

```yaml
server_url: "http://whitelist-server:8081"
...
```

| Field | Default | Description |
|-------|---------|-------------|
| ...   | ...     | ...         |
```
Mirror with bloom's `bloom_`-prefixed keys from `whitelist.yaml`, including `bloom_filter_url`, `bloom_filter_path`, `bloom_refresh_interval`, `bloom_conditional_get` (ETag), and the periodic-fetch + disk-persistence/resilience behaviour (GATE-05/06/07).

**Server endpoint docs pattern** (README lines 88–96 — HTTP API table):
```markdown
| `/bloom` | GET | Fetch current bloom filter (supports conditional GET / ETag) | `200` binary body or `304 Not Modified` |
```
Add this row to the existing HTTP API table in the Server section, or note it as a new endpoint in the bloom plugin section with a forward-reference.

**Docker Deployment config table pattern** (lines 293–299):
```markdown
| File | Mounted to | Used by |
|------|-----------|---------|
| `config/whitelist/whitelist.yaml` | `/root/deepfry/whitelist.yaml` in strfry | Client plugin |
```
Add a row for bloom's persisted filter file and note the `/root/deepfry/` directory persistence requirement.

**File Structure section pattern** (lines 302–341): Add `cmd/bloom/` and `pkg/bloomgate/` entries mirroring the `cmd/router/` and related `pkg/` entries.

**Build Commands section pattern** (lines 347–361): Add `make build-bloom`, `make build-bloom-alpine` rows mirroring the `build-router` rows.

**Requirements table pattern** (lines 411–428): Add GATE-01 through GATE-07 rows (or a summary reference) in the same `| ID | Description | Status |` format.

---

## Shared Patterns

### LDFLAGS version stamping
**Source:** `whitelist-plugin/Makefile` lines 22–26
**Apply to:** All new Makefile targets (build-bloom, build-bloom-alpine, build-bloom-linux)
```makefile
LDFLAGS=-X 'whitelist-plugin/pkg/version.Version=$(VERSION)' \
        -X 'whitelist-plugin/pkg/version.Commit=$(GIT_COMMIT)' \
        -X 'whitelist-plugin/pkg/version.Built=$(BUILD_TIME)'

BUILD_FLAGS=-ldflags "$(LDFLAGS)"
```
The bloom targets use `$(BUILD_FLAGS)` for native and inline `$(LDFLAGS)` for static targets — identical to how router does it.

### Static Alpine/Linux build flags
**Source:** `whitelist-plugin/Makefile` lines 46–50 (alpine pattern)
**Apply to:** `build-bloom-alpine` and `build-bloom-linux`
```makefile
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
    -tags netgo \
    -o bin/$(APP_BLOOM)-alpine ./cmd/$(APP_BLOOM)
```
These exact flags are used identically in `Dockerfile.strfry` — keep them in sync.

### Dockerfile static build flags
**Source:** `Dockerfile.strfry` lines 4–8 (whitelist build)
**Apply to:** new bloom `RUN` block in `Dockerfile.strfry`
```dockerfile
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags "-w -s -extldflags '-static'" \
    -tags netgo \
    -o /bloom ./cmd/bloom
```
Note: Dockerfile does NOT inject version LDFLAGS (no `VERSION`/`GIT_COMMIT` vars in the build stage) — this matches the existing whitelist/router pattern; do not diverge.

---

## No Analog Found

None. All five files have exact analogs within the whitelist-plugin codebase and deepfry monorepo root.

---

## Metadata

**Analog search scope:** `whitelist-plugin/Makefile`, `Dockerfile.strfry`, `config/strfry/strfry.conf`, `docker-compose.strfry.yml`, `whitelist-plugin/README.md`
**Files read:** 5 (all target files; no additional search needed — analogs are within the files themselves)
**Pattern extraction date:** 2026-06-30

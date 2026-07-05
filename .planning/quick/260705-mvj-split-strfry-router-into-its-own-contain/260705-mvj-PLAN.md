---
phase: 260705-mvj
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - docker-compose.strfry.yml
  - config/strfry/router-liveness.sh
  - README.md
autonomous: true
requirements:
  - SPLIT-01
  - SPLIT-02
  - SPLIT-03
must_haves:
  truths:
    - "The main strfry container runs ONLY ./strfry relay (no router sibling process)"
    - "A dedicated strfry-router container runs ONLY ./strfry router /app/strfry-router.config"
    - "The router container has restart: unless-stopped so an OOM-kill (ExitCode 137, OOMKilled=true) auto-restarts it"
    - "The router container has an explicit mem_limit so OOM is attributed to it, not VM-wide"
    - "The router container healthcheck fails (unhealthy) when the strfry router process is no longer alive"
    - "Both containers bind-mount the identical LMDB db dir and the identical strfry.conf (maxreaders=4096 preserved) so LMDB multi-process access is safe"
    - "The router container has the bloom plugin available at /app/plugins/bloom and its writable bloom-data dir mounted, preserving pluginDown gating"
  artifacts:
    - path: "docker-compose.strfry.yml"
      provides: "Split strfry (relay-only) + strfry-router (router-only) service definitions"
      contains: "strfry-router"
    - path: "config/strfry/router-liveness.sh"
      provides: "Ingestion-liveness healthcheck: asserts the strfry router process is alive"
      min_lines: 10
  key_links:
    - from: "docker-compose.strfry.yml (strfry-router service)"
      to: "config/strfry/strfry.conf"
      via: "bind-mount /etc/strfry.conf:ro — supplies the shared LMDB db path + maxreaders=4096"
      pattern: "strfry.conf:/etc/strfry.conf:ro"
    - from: "docker-compose.strfry.yml (strfry-router service)"
      to: "${STRFRY_DB_PATH}:/app/strfry-db"
      via: "identical db bind-mount shared with the relay container"
      pattern: "STRFRY_DB_PATH.*:/app/strfry-db"
    - from: "docker-compose.strfry.yml (strfry-router service)"
      to: "config/strfry/router-liveness.sh"
      via: "healthcheck test invokes the mounted liveness script"
      pattern: "router-liveness.sh"
---

<objective>
Split the `strfry router` ingestion process out of the shared `strfry` container into its own dedicated `strfry-router` container so a router OOM-kill is (a) attributed to a bounded container, (b) auto-restarted by Docker, and (c) surfaced as `unhealthy` by an ingestion-liveness healthcheck instead of hiding behind the relay's port probe.

Purpose: Harden against the confirmed production incident — the router was SIGKILLed by the OOM killer inside the shared container, the entrypoint only `wait`s on the relay pid so the router's death went unnoticed and unrestarted, and the container stayed `healthy` (relay port probe only). Result: ~5h silent zero-ingestion outage with an orphaned bloom plugin.

Output: A restructured `docker-compose.strfry.yml` (relay-only `strfry` + new router-only `strfry-router` service) and a small dependency-free `config/strfry/router-liveness.sh` healthcheck script. Repo changes only — deployment to the remote host is documented as a runbook, not executed.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@./CLAUDE.md
@docker-compose.strfry.yml
@config/strfry/strfry.conf
@gen-router-config.sh

# The router-conversion research (streams schema, pluginDown gating, ROUTER env / strfry.sh entrypoint semantics):
@.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-RESEARCH.md
</context>

<design_notes>
## What moves, what stays, what duplicates (READ BEFORE EDITING)

The current single `strfry` service runs BOTH `./strfry relay` and `./strfry router "$ROUTER"` via the dockur `strfry.sh` entrypoint (`ROUTER=/app/strfry-router.config`). The split:

**MAIN `strfry` service (relay-only) — CHANGES:**
- REMOVE the `ROUTER` env var entirely. `strfry.sh` only launches `./strfry router` when `ROUTER` is set; unsetting it makes the main container run ONLY `./strfry relay`. (Env is baked at container-create time → a recreate is required — noted in the runbook.)
- REMOVE the router-only bind-mounts from this service: `strfry-router.config` (only the router consumes it) and `router.yaml` (router-plugin config — belongs with the router).
- KEEP: db mount, `strfry.conf`, `whitelist.yaml`, bloom-data mount, `oom_score_adj: -300`, `restart: unless-stopped`, port `7777`, the existing relay healthcheck (`curl http://localhost:7777/` — correct for the relay; do NOT change it).

**NEW `strfry-router` service (router-only) — ADD:**
- Same image (`build:` from `Dockerfile.strfry`) — stock strfry, NOT forked. The bloom plugin is baked into the image at `/app/plugins/bloom`.
- Override `command:` to run ONLY the router: `["/app/strfry", "--config=/etc/strfry.conf", "router", "/app/strfry-router.config"]`. This is the same override pattern the quarantine service already uses. We do NOT rely on `strfry.sh` here because it always launches `./strfry relay` first and `wait`s on it — reusing it would start a second unwanted relay. Two relays binding :7777 in one Docker network is a conflict; the explicit `command:` avoids it. No `ports:` on the router container (it is a WS client to upstreams + a local LMDB writer; it exposes no server port).
- `restart: unless-stopped` (SPLIT-01): an OOM SIGKILL lands on THIS container → Docker records `OOMKilled=true` + `ExitCode=137` and auto-restarts it.
- `mem_limit` (SPLIT-02): an explicit ceiling so OOM is attributed to the router (deterministic cgroup OOM), not VM-wide killer roulette. Start at `512m` — the router holds ~174 upstream WS connections + per-connection buffers + the LMDB mmap page tables; 512 MiB is generous headroom for the connection set while still bounding a runaway. Tradeoff: too low → false OOM restart loop; too high → defeats attribution. Document that the operator should watch `docker stats strfry-router` and raise if it sits near the ceiling. (Compose v2 also honors `mem_limit` under the top-level `services:` short syntax used by the sibling services here.)
- `oom_score_adj`: do NOT copy the relay's `-300`. The relay holds canonical data and must be the LAST sacrifice; the router is restartable and disposable, so leave its score at the default (0) — it is a more acceptable OOM victim than the relay, and its own `mem_limit` gives it a deterministic local ceiling regardless.

**Shared LMDB (CRITICAL — must be identical on both):**
- The router WRITES down-events into the SAME LMDB the relay serves. Both containers MUST bind-mount the identical db dir (`${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db`) AND the identical `config/strfry/strfry.conf:/etc/strfry.conf:ro`. The `strfry.conf` supplies both the `db` path and `dbParams.maxreaders = 4096` (the prior MDB_READERS_FULL fix). If the two containers disagree on the db dir or maxreaders, LMDB multi-process access breaks (MDB_BAD_RSLOT / reader-table corruption). Do NOT migrate to a named volume (CLAUDE.md hard rule — LMDB stays at the host bind-mount path).

**pluginDown gating (MANDATORY):**
- The router config (`/app/strfry-router.config`) sets `pluginDown = /app/plugins/bloom`. The router container must therefore have (1) the bloom plugin binary at that path — baked into the image, present automatically — and (2) the writable bloom-data dir mounted exactly as the relay has it: `${BLOOM_DATA_PATH:-./data/bloom}:/root/deepfry/bloom-data`. The router config itself is bind-mounted `${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}:/app/strfry-router.config:ro`. `router.yaml` moves here too (`config/whitelist/router.yaml:/root/deepfry/router.yaml`) so the router plugin's config travels with the router. This preserves the quarantine side-channel wiring.

**Healthcheck design (SPLIT-03) — ingestion liveness:**
- Primary approach chosen: assert the `strfry router` process is alive via `ps`. Rationale: it is dependency-free (busybox `ps` + `grep` are always present in the image — no curl/jq needed), it directly detects the exact failure that caused the incident (router process gone / OOM-killed), and combined with `restart: unless-stopped` it yields auto-recovery: process dies → healthcheck fails → container marked unhealthy → restart policy brings it back. The freshness alternative (newest LMDB event younger than N min) was rejected as the primary check: during legitimate quiet gate periods (all authors non-whitelisted for a window) zero events are written, producing false `unhealthy` — a real risk given the bloom whitelist gate. Process-liveness has no such false-positive mode. The script lives at `config/strfry/router-liveness.sh` (mounted `:ro`, mirroring the quarantine-db-guard.sh pattern) and greps for the literal router subcommand while excluding the grep process itself.
</design_notes>

<tasks>

<task type="auto">
  <name>Task 1: Add the ingestion-liveness healthcheck script</name>
  <files>config/strfry/router-liveness.sh</files>
  <action>Create a POSIX sh script (shebang /bin/sh, set -eu) that exits 0 when the strfry router process is running and exits 1 otherwise. Use busybox ps (available in the dockur/strfry alpine image) piped to grep, matching the running router invocation while excluding the grep process itself. Detect by matching the process argv for the strfry binary running the router subcommand (the token that identifies "strfry ... router"); use a bracket-class trick or an explicit grep -v to avoid self-matching. Print a one-line ok/fail diagnostic to stdout so `docker inspect`/healthcheck logs are legible (mirror the diagnostic style of config/strfry/quarantine-db-guard.sh). Do NOT depend on curl, jq, awk-of-config, or any file read — process presence only. Make the file executable (chmod +x) so it can also be invoked directly, though the compose healthcheck will call it via `sh`.</action>
  <verify>
    <automated>test -x config/strfry/router-liveness.sh && sh -n config/strfry/router-liveness.sh && echo OK</automated>
  </verify>
  <done>config/strfry/router-liveness.sh exists, is executable, passes `sh -n` syntax check, greps `ps` for the strfry router process excluding itself, and prints an ok/fail diagnostic line. No dependency on curl/jq/config-file reads.</done>
</task>

<task type="auto">
  <name>Task 2: Split docker-compose.strfry.yml into relay-only strfry + router-only strfry-router</name>
  <files>docker-compose.strfry.yml</files>
  <action>Edit the existing `strfry` service to be relay-only (SPLIT preamble): remove the `ROUTER` environment variable (its removal makes strfry.sh run only `./strfry relay`; update the adjacent comment to explain the router now runs in its own service and that a recreate is required because ROUTER was baked at create time), and remove the two router-only bind-mounts from this service — the `strfry-router.config` mount and the `router.yaml` mount. Keep everything else on the relay service unchanged: db mount, strfry.conf mount, whitelist.yaml mount, bloom-data mount, oom_score_adj: -300, restart: unless-stopped, ports 7777, networks, and the existing relay healthcheck (leave the relay's `curl http://localhost:7777/` probe as-is — it is correct for the relay).

  Add a NEW `strfry-router` service (insert it after the `strfry` service, before `strfry-quarantine`). It uses the same `build:` (context ., dockerfile Dockerfile.strfry — stock image, bloom plugin baked at /app/plugins/bloom), container_name strfry-router, `restart: unless-stopped` (SPLIT-01), `mem_limit: ${STRFRY_ROUTER_MEM_LIMIT:-512m}` (SPLIT-02), working_dir /app, and an explicit `command: ["/app/strfry", "--config=/etc/strfry.conf", "router", "/app/strfry-router.config"]` so it runs ONLY the router (never strfry.sh, which would launch a relay). Give it NO `ports:` (it is a WS client + LMDB writer, no server port). Do NOT set oom_score_adj on it (default 0 — the router is the acceptable OOM victim; the relay's -300 keeps canonical data safest).

  Mounts on strfry-router (SPLIT preamble — identical LMDB + config as the relay, plus the router-owned files): the SAME db mount `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db`; the SAME `./config/strfry/strfry.conf:/etc/strfry.conf:ro` (supplies db path + maxreaders=4096 — must match the relay exactly for safe multi-process LMDB); `${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}:/app/strfry-router.config:ro` (the streams-only config with pluginDown=/app/plugins/bloom); `./config/whitelist/router.yaml:/root/deepfry/router.yaml` (moved from the relay); `./config/whitelist/whitelist.yaml:/root/deepfry/whitelist.yaml` (the bloom plugin reads bloom_path from here); and `${BLOOM_DATA_PATH:-./data/bloom}:/root/deepfry/bloom-data` (writable bloom persistence — pluginDown gating). Attach to the `deepfry-net` network.

  Add the SPLIT-03 healthcheck to strfry-router: mount `./config/strfry/router-liveness.sh:/usr/local/bin/router-liveness.sh:ro` and set `healthcheck.test: ["CMD-SHELL", "sh /usr/local/bin/router-liveness.sh"]`, interval 1m, timeout 5s, retries 2. Add a comment explaining this is an ingestion-liveness check (router process alive) rather than a port probe, and that a dead router → unhealthy → restart:unless-stopped auto-recovery.

  Add a top-of-file or in-service comment recording the incident rationale (router OOM-killed inside the shared container; entrypoint only waited on the relay pid; relay-port healthcheck hid the outage) so the split's motivation is discoverable in-tree.</action>
  <verify>
    <automated>docker compose -f docker-compose.strfry.yml config >/dev/null && grep -q 'strfry-router' docker-compose.strfry.yml && grep -q 'router-liveness.sh' docker-compose.strfry.yml && ! grep -q '^\s*ROUTER:' docker-compose.strfry.yml && echo OK</automated>
  </verify>
  <done>`docker compose -f docker-compose.strfry.yml config` validates. The `strfry` service no longer sets `ROUTER` and no longer mounts strfry-router.config or router.yaml, but keeps its relay healthcheck, oom_score_adj:-300, and db/conf/whitelist/bloom mounts. A new `strfry-router` service exists with restart:unless-stopped, mem_limit, an explicit `command:` running only the router subcommand, the identical db + strfry.conf mounts as the relay, the router.config + router.yaml + whitelist.yaml + bloom-data mounts, no ports, and the router-liveness healthcheck.</done>
</task>

<task type="auto">
  <name>Task 3: Document the split + deployment/verification runbook in README.md</name>
  <files>README.md</files>
  <action>Update the README router/ingestion section (around the "Ingestion runs as a single strfry router process inside the strfry container" note near line 150 and the `~/deepfry/strfry-router.config` bullet near line 183) to reflect that ingestion now runs in a DEDICATED `strfry-router` container (not inside the main strfry container), that the main strfry container runs relay-only, and that both containers share the same LMDB and strfry.conf. Add the `strfry-router` container to the services table near line 121 (alongside strfry / strfry-quarantine): image, purpose (ingestion router — pulls events from upstream relays into the shared LMDB via pluginDown=bloom gating), no exposed port, restart:unless-stopped + mem_limit + process-liveness healthcheck. Add a short "Deployment & verification runbook" subsection covering: (1) regenerate the router config host-side with ./gen-router-config.sh; (2) recreate the split services — note that because ROUTER was baked at create time the main strfry container must be recreated to stop launching the router, e.g. `docker compose -f docker-compose.strfry.yml up -d --force-recreate strfry strfry-router`; (3) confirm both containers healthy — `docker compose -f docker-compose.strfry.yml ps` shows strfry (healthy, relay port) and strfry-router (healthy, process-liveness) and `docker logs strfry-router` shows `Loading router config file:` → `New stream group [ingest]` → `Connecting to wss://...`; (4) confirm auto-restart on kill — `docker kill strfry-router` then observe `docker inspect strfry-router --format '{{.State.OOMKilled}} {{.State.ExitCode}} {{.RestartCount}}'` and that the container returns to running/healthy (this proves restart:unless-stopped recovers it; a real OOM shows OOMKilled=true / ExitCode 137); (5) confirm ingestion resumes — sample `docker exec strfry ./strfry scan --count '{}'` (or equivalent event-count query) before/after to see the count increase, confirming the router is writing into the shared LMDB. Keep it operator-facing prose; this is plan/documentation content, NOT an executed deployment step.</action>
  <verify>
    <automated>grep -q 'strfry-router' README.md && grep -qi 'runbook' README.md && grep -q 'force-recreate' README.md && echo OK</automated>
  </verify>
  <done>README.md documents the dedicated strfry-router container in the services table and ingestion section, and includes a Deployment & verification runbook covering config regen, force-recreate of the split services, health confirmation for both containers, docker-kill auto-restart verification (OOMKilled/exit-code/restart-count), and an event-count-delta ingestion check.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| upstream relays → router container | Untrusted Nostr events arrive over WS from ~174 upstream relays and are written into the canonical LMDB. |
| router container ↔ relay container (shared LMDB) | Two OS processes in separate containers open the same LMDB env (multi-process access via the lock file). |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-260705-01 | Denial of Service | untrusted upstream events bypassing whitelist on ingest | mitigate | Preserve `pluginDown = /app/plugins/bloom` on the router down-stream + mount the bloom plugin's writable bloom-data dir into strfry-router; the global writePolicy does NOT gate router-down events (verified in RESEARCH), so pluginDown is the only gate and must not be dropped in the split. |
| T-260705-02 | Tampering | shared LMDB reader table (MDB_BAD_RSLOT / corruption) | mitigate | Both containers bind-mount the identical db dir AND the identical strfry.conf carrying `dbParams.maxreaders = 4096`; mismatched maxreaders or separate db dirs cause reader-table corruption. Verify identical mounts in Task 2. |
| T-260705-03 | Denial of Service | router runaway memory → VM-wide OOM killing the canonical relay | mitigate | Explicit `mem_limit` on strfry-router bounds it to a deterministic cgroup OOM; the relay keeps oom_score_adj:-300 so it is the last sacrifice; the router has no oom_score_adj override so the kernel prefers it as the victim. |
| T-260705-04 | Denial of Service | silent router death (the incident) going unnoticed | mitigate | Router in its own container + restart:unless-stopped (auto-restart on SIGKILL/ExitCode 137) + process-liveness healthcheck (unhealthy = ingestion broken, not just relay-port up). |
| T-260705-SC | Tampering | image build / package installs | accept | No new packages installed; the stock dockur/strfry image (pinned by digest in Dockerfile.strfry) and the already-vendored Go plugins are reused unchanged. No new supply-chain surface. |
</threat_model>

<verification>
- `sh -n config/strfry/router-liveness.sh` passes; the script is executable and dependency-free (no curl/jq/awk-config).
- `docker compose -f docker-compose.strfry.yml config` validates the split.
- `strfry` service: no `ROUTER` env, no router.config/router.yaml mounts, relay healthcheck + oom_score_adj:-300 retained.
- `strfry-router` service: restart:unless-stopped, mem_limit, explicit router-only `command:`, identical db + strfry.conf mounts as the relay, router.config + router.yaml + whitelist.yaml + bloom-data mounts, no ports, process-liveness healthcheck.
- README documents the new container + a Deployment & verification runbook (config regen, force-recreate, dual health confirmation, docker-kill auto-restart proof, event-count-delta ingestion check).
- StrFry remains UNMODIFIED (stock image, no fork; router runs only via an explicit `command:` override — no entrypoint or binary changes). LMDB stays at the host bind-mount path (no named volume).
</verification>

<success_criteria>
- The router process is isolated in a bounded, auto-restarting `strfry-router` container whose healthcheck fails when ingestion is actually broken (router process gone), directly closing the three gaps that produced the silent OOM outage.
- No existing mount, env var, plugin path, or the maxreaders=4096 LMDB setting is lost; each moved/kept/duplicated item is explicit in the compose file and design notes.
- Repo-only change; deployment + live verification captured as an operator runbook in README, not executed by the executor.
</success_criteria>

<output>
Create `.planning/quick/260705-mvj-split-strfry-router-into-its-own-contain/260705-mvj-SUMMARY.md` when done.
</output>

# 02 — ROUTER= Entrypoint Wiring (source-cited confirmation)

**Confirmed:** 2026-07-02
**Purpose (D-04):** De-risk the highest unknown BEFORE touching compose — read the
ACTUAL source compiled into `dockurr/strfry:1.1.0` and confirm on paper exactly how
the `ROUTER=` entrypoint env invokes the router, so plan 02 can wire it once and
plan 03 has an unambiguous fallback trigger.
**Verdict:** CONFIRMED — the expectation holds. Setting `ROUTER=<path>` causes the
entrypoint to run `./strfry router "$ROUTER"` from cwd `/app`, passing the value as
the quoted positional `<routerConfigFile>`, in the SAME container as `./strfry relay`.
No contradiction found. Plan 02's single wiring (D-01/D-02) is well-founded.

## Sources read (exact refs)

The deployed image `dockurr/strfry:1.1.0` (Docker Hub, published 2026-04-26) is built
from **dockur's `v1.1.0` tag** — that tag's `Dockerfile` is the one carrying the
`strfry.sh` entrypoint + baked-in router sample (the `master` Dockerfile has since
reverted to `ENTRYPOINT ["/app/strfry"]` / `CMD ["relay"]` with no `strfry.sh`, so
`master` is NOT representative of the deployed image — read the TAG, not master).

- `github.com/dockur/strfry` @ **`v1.1.0`** — `strfry.sh` (entrypoint: `ROUTER`/`STREAMS`
  env wiring), `Dockerfile` (`WORKDIR /app`, `COPY strfry.sh /app/strfry.sh`,
  `COPY strfry-router.conf /etc/strfry-router.conf`, `CMD ["/app/strfry.sh"]`),
  `src/apps/mesh/cmd_router.cpp` (`Usage: router <routerConfigFile>`), and the shipped
  `strfry-router.conf` sample (`streams { friends { ... } }`, comma arrays).
- `github.com/hoytech/strfry` @ **`f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135`** (== tag
  `1.1.0`, GitHub compare = "identical") — `src/apps/mesh/cmd_router.cpp` cross-check:
  `USAGE` line 15-18 `router <routerConfigFile>`; `reconcileConfig()` line 299;
  `LI << "Loading router config file: " << routerConfigFile;` line 300;
  `loadRawTaoConfig(routerConfigFile)` line 303; `routerConfig.at("streams")` line 332;
  `LE << "Failed to parse router config: "` + `if (!firstConfigLoadSuccess) ::exit(1);`
  lines 350-351. Dockur's fork differs only by added compression/byte-count stats +
  `filterCompiled.addFilters()` — the router config schema and the positional-arg
  loading path are byte-identical (T-02-01 mitigation: cross-check done at the pinned
  commit; refs recorded above).

## Confirmed invocation

`strfry.sh` @ dockur `v1.1.0` (quoted source lines):

```
line  4:  : ${ROUTER:=''}
line  5:  : ${STREAMS:=''}
line 32:  cd /app
line 49:  ./strfry relay &
line 52:  if [ -f "$ROUTER" ]; then
line 54:    ./strfry router "$ROUTER" &
line 57:  if [[ "$ROUTER" == [Yy1]* ]]; then
line 59:    ./strfry router /etc/strfry-router.conf &
line 62:  for i in $(echo "$STREAMS" | sed "s/,/ /g")
line 66:    ./strfry stream wss://$i --dir down 2> /dev/null &
```

**The exact command the entrypoint runs for a path-valued `ROUTER`:**

```
cd /app
./strfry relay &                    # relay always starts (line 49)
# if ROUTER points at an existing file:
./strfry router "$ROUTER" &         # line 54 — value passed AS the positional arg
```

- **cwd is `/app`** (line 32 `cd /app`), so `./strfry` == `/app/strfry`. Confirmed by
  the Dockerfile `WORKDIR /app` (line 31) + `COPY --from=build /build/strfry strfry`
  (binary lands at `/app/strfry`).
- **The value is passed as the positional `<routerConfigFile>`**, matching
  `cmd_router.cpp` `USAGE` `router <routerConfigFile>` — docopt binds the single
  positional to `routerConfigFile`, which `reconcileConfig()` feeds to
  `loadRawTaoConfig(routerConfigFile)` and logs as `Loading router config file:`.
- **`"$ROUTER"` is quoted** (line 54), so a path containing spaces survives as one
  argument. Our path `/app/strfry-router.config` has no spaces regardless.
- **Same container** as the relay: both `./strfry relay &` (line 49) and
  `./strfry router "$ROUTER" &` (line 54) are backgrounded by the one `strfry.sh`
  process; the script `wait "$PID"`s on the relay. This is exactly D-01 (no separate
  router service).
- **Guard `[ -f "$ROUTER" ]` (line 52):** the path branch only fires when `ROUTER`
  names an EXISTING file inside the container. => the router config bind-mount
  (host `~/deepfry/strfry-router.config` → container `/app/strfry-router.config`,
  D-02) MUST be present and mounted before the container starts, or this branch is
  silently skipped and NO router runs (no error). This is a load-bearing prerequisite
  for plan 03: the file must exist at the container path `ROUTER=` points to.

## Config / db resolution

- The **global golpe `--config`** (supplying LMDB `db` + `relay.*`) is NOT passed
  explicitly by `strfry.sh` — the entrypoint runs a bare `./strfry router "$ROUTER"`
  (no `--config` flag). golpe therefore falls back to its search order:
  `--config` → `STRFRY_CONFIG` env → `/etc/strfry.conf` → `./strfry.conf`. In the
  container `/etc/strfry.conf` exists (the entrypoint copies
  `/etc/strfry.conf.default` → `/etc/strfry.conf` on first boot if absent; the
  Dockerfile `COPY strfry.conf /etc/strfry.conf.default` seeds it), so **`db` resolves
  from `/etc/strfry.conf`** — the same config the relay uses, the same LMDB env.
- The **router config file carries NO `db`/`relay`** (Phase 1 D-10). It is the
  positional `<routerConfigFile>` only, read by `reconcileConfig()` →
  `routerConfig.at("streams")`. golpe would silently ignore `db` if present in the
  router file anyway (unknown-top-level-key discard), and `db` in the golpe global
  file is the one that counts — so keeping the router file streams-only is correct
  and avoids the "two-files confusion" that caused the 260701-l5s zero-streams result.
- Net: the relay and the router open the SAME LMDB (from `/etc/strfry.conf`) inside
  the SAME container — consistent with the single-process reader-slot fix the project
  is chasing (one router env-opener instead of 174 `stream` env-openers).

## Why `ROUTER=<path>`, not `ROUTER=1`/`yes`, not `STREAMS=`

- **`ROUTER=1` / `ROUTER=yes` is FORBIDDEN (D-03).** Source: `strfry.sh` lines 57-59
  `if [[ "$ROUTER" == [Yy1]* ]]; then ./strfry router /etc/strfry-router.conf &`.
  That loads dockur's **baked-in sample** `/etc/strfry-router.conf` (Dockerfile line 51
  `COPY strfry-router.conf /etc/strfry-router.conf`), whose contents are a demo
  `streams { friends { dir="down"; pluginDown="/app/write-policy.py";
  urls=["wss://nos.lol","wss://soloco.nl"] } }` — the WRONG relay list, the WRONG
  plugin path, and NOT the bloom-gated 174-relay ingest we generate. Point `ROUTER=`
  at the explicit path `/app/strfry-router.config` (D-02) so the path branch (line 52)
  fires against OUR file, never the `[Yy1]*` branch.
  - Note: the two branches are independent `if`s, but a value like
    `/app/strfry-router.config` does not match glob `[Yy1]*`, so only the path branch
    runs. A value of literally `1`/`y`/`yes` is not a valid file path, so `[ -f ]`
    fails and only the sample branch runs. They do not both fire for our value.
- **`STREAMS=` is the WRONG legacy path.** Source: `strfry.sh` lines 62-66 iterate a
  comma-split `STREAMS` and spawn `./strfry stream wss://$i --dir down 2> /dev/null &`
  — one deprecated `strfry stream` PROCESS per relay, stderr suppressed. That is
  EXACTLY the 174-process fan-out (LMDB reader-slot exhaustion → `MDB_BAD_RSLOT`)
  this project replaces. Using `STREAMS=` would re-introduce the bug. Use `ROUTER=`
  (one router process), never `STREAMS=`.

## Confidence + D-05 fallback trigger (contingency-only)

**Confidence: HIGH.** The invocation, cwd, quoting, positional binding, and db
resolution are all read directly from the source that compiles into the deployed
`dockurr/strfry:1.1.0` (dockur `v1.1.0` tag) and cross-checked against hoytech
`f31a1b9`. The 260701-l5s failure is explained: raw `docker exec strfry /app/strfry
router <file>` failed because of a two-files/positional confusion, NOT because the
entrypoint path is broken. The `ROUTER=` env path is a distinct, source-correct
invocation that plan 02 should wire ONCE.

**Single observable fallback trigger:** After wiring `ROUTER=/app/strfry-router.config`
and bringing the service up on the host (plan 03), the log line

```
Loading router config file: /app/strfry-router.config
```

**never appears** in the `strfry` container logs (and no `New stream group [ingest]` /
`ingest: Connecting to wss://...` follow). ONLY then is the D-05 ladder considered.
(If instead the process dies immediately with `Failed to parse router config`, that is
a config-content bug — fix the generated file, not the wiring; not a D-05 trigger.)
Also watch the silent-skip case: if `[ -f "$ROUTER" ]` was false (bind-mount missing /
wrong container path), the router simply never starts — verify the file exists at
`/app/strfry-router.config` inside the container first.

**D-05 fallback ladder (build ONLY if the trigger above fires — do not pre-build):**

- **D-05(a) — explicit command override.** Override the service `command:`/entrypoint
  to invoke the binary directly with the global config explicit and the router file
  positional, from cwd `/app`:
  `/app/strfry --config=/etc/strfry.conf router /app/strfry-router.config`
  (mirror the `strfry-quarantine` service's `command: ["/app/strfry",
  "--config=/etc/strfry.conf", ...]` shape). This removes the `strfry.sh` wrapper as a
  variable while keeping the SAME LMDB + `/etc/strfry.conf`.
- **D-05(b) — dedicated router-only service.** A second compose service sharing the
  SAME LMDB bind-mount + `/etc/strfry.conf`, running only the router
  (`/app/strfry --config=/etc/strfry.conf router /app/strfry-router.config`), with the
  relay left in the original service. Last resort — still one router process, still
  bloom-gated per stream, LMDB unmoved (HARD RULE preserved).

## Scope note

Per the plan, **`docker-compose.strfry.yml` is NOT edited in this plan** — that is
plan 02, which consumes the confirmed env var name `ROUTER` + container path
`/app/strfry-router.config` recorded here. This artifact is on-paper confirmation only.

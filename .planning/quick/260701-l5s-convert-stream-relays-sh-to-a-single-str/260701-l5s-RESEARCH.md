# strfry `router` — Authoritative Spec (v1.1.0)

> ⚠️ **See the `CORRECTION (source-verified at f31a1b9 + dockur/strfry + hoytech/golpe)` section at the BOTTOM of this file first.** It supersedes any conflicting detail below. The `streams` schema documented here is CORRECT for the deployed build; the empirical "zero streams" failure was a two-files confusion (router `streams{}` block appended to the golpe `--config` file, where golpe silently discards unknown top-level keys), NOT a schema error.

**Researched:** 2026-07-01
**Domain:** strfry Nostr relay `router` subcommand (hoytech/strfry, tag `1.1.0`)
**Confidence:** HIGH
**Goal:** Replace N× deprecated `strfry stream <relay>` processes (174 of them) with ONE `strfry router` process that pulls all events from a relay list into the local LMDB, gated through `/app/plugins/bloom`.

## Summary

`strfry router` is a single long-running process that opens many nostr client connections at once, streaming events **up** (DB → remote), **down** (remote → DB), or **both**, per a taocpp::config file. It fully supersedes `strfry stream` for the multi-relay download use case.

Two config files are involved and they are **NOT** the same file:
1. **The global strfry config** (`/etc/strfry.conf`) — supplies the LMDB `db` path and every other golpe global param. It is located via the `--config` flag / `STRFRY_CONFIG` env / `/etc/strfry.conf` / `./strfry.conf` search order. `strfry router` opens the DB from **this** config.
2. **The router config** (positional arg to `router`) — supplies only the `streams { ... }` topology (connections, direction, filters, plugins). It does **not** contain `db`.

The critical gating fact: **the global `writePolicy` plugin does NOT run on router-`down` events.** Router-down events are gated ONLY by a per-stream `pluginDown`. To keep the bloom filter on downloaded events you MUST set `pluginDown = "/app/plugins/bloom"` on the down stream — omitting it is a silent whitelist regression. `[VERIFIED: src/apps/mesh/cmd_router.cpp @ tag 1.1.0]`

**Primary recommendation:** Run `strfry --config=/etc/strfry.conf router /app/strfry-router.config`, with the router config containing a single `down` stream that lists all 174 URLs, `filter = {}`, and `pluginDown = "/app/plugins/bloom"`.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| — | Convert 174× `strfry stream` procs → 1× `strfry router` | Confirmed router supports arbitrary URL count in one `urls` array under one `dir="down"` stream; hot-reloadable |
| — | Preserve bloom-plugin gating on downloaded events | `pluginDown` is the ONLY down-gate; global writePolicy does not apply to router (source-verified) |
</phase_requirements>

## Answers to the Five Questions

### 1. Exact router config file format

Top-level block is **`streams`** (NOT `connections`). taocpp::config format (config++ / like HOCON). `[VERIFIED: docs/router.md + cmd_router.cpp @ 1.1.0]`

```
connectionTimeout = 20      # optional, seconds, default 20
verbose = true              # optional, default true, hot-reloadable

streams {
    <arbitrary-name> {
        dir  = "down"       # REQUIRED
        urls = [ ... ]      # REQUIRED, non-empty array
        # optional: filter, pluginDown, pluginUp
    }
    <another-name> { ... }
}
```

**Per-stream keys:**

| Key | Required | Values / Syntax | Meaning |
|-----|----------|-----------------|---------|
| `dir` | **yes** | `"up"` \| `"down"` \| `"both"` | `down` = subscribe from remotes → store in local DB. `up` = watch local DB → upload new events to remotes. `both` = both (note: `both` echoes each downloaded event straight back to its source, which then rejects it as dup — inefficient). |
| `urls` | **yes** | Array of ws/wss URL strings. **Whitespace/newline separated — NO commas.** e.g. `urls = [ "wss://a" "wss://b" ]` | All URLs in the array receive the identical policy for this stream. |
| `filter` | no | NIP-01 filter object. Default `{}` = **match ALL events** (omitting pulls everything, not nothing). | For `down`/`both`: sent as the `REQ` subscription filter, with an implicit `"limit":0` appended (suppresses historical backfill — you only get events created after connect). For `up`/`both`: local events are only uploaded if they match. |
| `pluginDown` | no | Path or shell command to a [plugin](plugins.md). | Consulted before **storing** any received (down) event. This is the down-gate. |
| `pluginUp` | no | Path or shell command to a plugin. | Consulted before **transmitting** any (up) event. |

There is **no per-stream `groupName` key** — the stream section's *name* IS its group name internally (`ConnDesignator.groupName`). There are no other documented per-stream keys. `[VERIFIED: cmd_router.cpp — configure() reads only dir/filter/pluginDown/pluginUp/urls]`

### 2. Command-line invocation

`router` takes the router config as a **positional** argument (docopt `Usage: router <routerConfigFile>`):

```
strfry router /path/to/strfry-router.config
```

The **global** `--config` (for the main strfry.conf / db) is a golpe-level flag that comes **before** the subcommand:

```
strfry --config=/etc/strfry.conf router /path/to/strfry-router.config
```

`[VERIFIED: cmd_router.cpp USAGE + README "strfry --config /path/to/strfry.conf relay"]`

### 3. Relationship to the main strfry.conf

They are **two separate files, both loaded**:

- The `Router` constructor immediately does `env.txn_ro()` and reads `dbDir` — these come from the **global golpe config** (`db` is defined in `golpe.yaml`, default `./strfry-db/`), located via the standard search order:
  1. `--config <file>`
  2. `STRFRY_CONFIG` env var
  3. `/etc/strfry.conf`
  4. `./strfry.conf`
- The **router config** (positional arg) is loaded separately by `reconcileConfig()` via `loadRawTaoConfig(routerConfigFile)`, which logs `Loading router config file: <path>`. It must contain ONLY the `streams` block (and optional `connectionTimeout`/`verbose`). It must **NOT** contain `db`/`relay`.

**This explains your empirical observation.** You saw only `Loading config from file: /etc/strfry.conf` and zero events. That is the **global** config log line. The router file has its OWN distinct log line: `Loading router config file: <path>`. If you never saw that second line, one of:
- The router file failed to parse (bad taocpp syntax, or missing `streams` block) — on first-load parse failure the router logs `Failed to parse router config: ...` and **`::exit(1)`s**.
- OR the file was passed where the global `--config` flag expected it, so the DB came up from `/etc/strfry.conf` but no `streams` were ever loaded → zero activity.

Fix: keep them separate. `strfry --config=/etc/strfry.conf router /app/strfry-router.config`. You should see BOTH log lines, then `New stream group [<name>]` and `<name>: Connecting to wss://...`.

`[VERIFIED: cmd_router.cpp Router() reads env/dbDir from global golpe config; golpe.yaml defines db; README config search order]`

### 4. How incoming (down) events are gated

**The global `writePolicy { plugin = ... }` in strfry.conf does NOT apply to router-down events.** `[VERIFIED: cmd_router.cpp]`

Router-down events flow through `StreamGroup::incomingEvent()`, which calls:

```cpp
auto res = pluginDown.acceptEvent(pluginDownCmd, evJson, EventSourceType::Stream, url, ...);
if (res == PluginEventSifterResult::Accept) router->writer.write({ std::move(evJson) });
```

The write goes **directly** to `router->writer` (the WriterPipeline). The relay's `writePolicy` sifter is part of the `relay` app's ingestion path (`EventSourceType::Client`), which router never touches. So:

- If `pluginDown` is **unset** on a down stream → every downloaded event is stored, whitelist bypassed. **This is the regression you must avoid.**
- To preserve bloom gating → set `pluginDown = "/app/plugins/bloom"` on the down stream. The router invokes it exactly as the relay's writePolicy would (same `PluginEventSifter`, same JSON stdin/stdout protocol), just with `sourceType = "stream"` instead of `"client"`.

> Verify `/app/plugins/bloom` doesn't branch on `sourceType`. The plugin receives `{"type":"new","event":{...},"sourceType":"stream","sourceInfo":"<relay-url>",...}`. Relay writePolicy events arrive with `sourceType:"client"`. If the bloom plugin only accepts/rejects on pubkey (bloom membership) and ignores `sourceType`, it works unchanged. If it special-cases `sourceType`, add a `stream` branch.

### 5. Why `dir="down"` + `urls` produced zero activity in ~30s at verbosity 0

Most likely causes, in order:

1. **Router config never loaded** (see Q3) — the file was consumed as the global `--config`, or failed to parse and exited. No `streams` = no connections. This is the leading suspect given you only saw the `/etc/strfry.conf` log line.
2. **`limit:0` semantics — this is expected and correct, not a bug.** Router-down subscriptions get an implicit `"limit":0`, so you receive **only newly-published events after connect**, never historical backfill. On quiet relays, ~30s can legitimately yield zero events even when working. To backfill history use `strfry sync`/`strfry download`; `router` is for the live tail. A `filter` is **not** required to trigger the subscription — default `{}` subscribes to everything.
3. **Logging visibility.** Connection lifecycle lines (`Connecting to`, `Connected to`, `Disconnected from`, `New stream group`) are logged at `LI` (info). Per-event accept/commit logging is governed by `verbose` (default `true`). At the shell you observed, confirm you can see at least the `LI` connection lines — if the process is genuinely connecting you WILL see `Connecting to wss://...`. Absence of those lines confirms cause #1 (streams never loaded), not a silent-connection issue.

**Diagnostic:** run in foreground and grep stderr for `Loading router config file`, `New stream group`, and `Connecting to`. All three present + still zero events ⇒ cause #2 (quiet relays / limit:0), which is normal.

## Minimal WORKING router config

`/app/strfry-router.config` — pulls ALL events from the relay list into local DB, gated through bloom:

```
connectionTimeout = 20
verbose = true

streams {
    ingest {
        dir        = "down"
        pluginDown = "/app/plugins/bloom"
        filter     = {}

        urls = [
            "wss://relay.example-1.com"
            "wss://relay.example-2.com"
            # ... all 174 URLs, whitespace/newline separated, NO commas ...
        ]
    }
}
```

Notes on the 174 URLs: they all share one policy, so one `ingest` stream with all 174 in the `urls` array is correct and idiomatic — the router opens one WS connection per URL inside the single process. (You *may* split into multiple named streams if you ever want different filters/plugins per group, but for uniform pull, one stream is simplest.)

### Exact command line (db comes from /etc/strfry.conf)

```bash
strfry --config=/etc/strfry.conf router /app/strfry-router.config
```

- `--config=/etc/strfry.conf` → supplies LMDB `db` path (and it's the default search location anyway, so `strfry router /app/strfry-router.config` also works IF `/etc/strfry.conf` exists and has a valid `db`).
- positional `/app/strfry-router.config` → the `streams` topology.

Expected startup log (all three lines must appear):
```
Loading config from file: /etc/strfry.conf
Loading router config file: /app/strfry-router.config
New stream group [ingest]
ingest: Connecting to wss://relay.example-1.com
ingest: Connected to wss://relay.example-1.com
...
```

## Config Generation (replacing stream-relays.sh)

The old script spawned one process per relay by iterating a relay list. The replacement is a config generator: emit ONE `strfry-router.config` from the same relay list (heredoc/template the `urls` array), then launch a single process. Because the router **hot-reloads** on config-file change, you can add/remove relays by rewriting the file — no restart, live connections preserved. Prefer this over kill/respawn.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Multi-relay fan-in | 174 supervised `stream` processes | 1 `strfry router` | Deprecated; router does it in-process with reconnect + hot-reload |
| Reconnect/backoff loop | Custom respawn/systemd-per-relay | router built-in | Retries every `connectionTimeout*2`s forever, per-URL |
| Whitelist on ingest | Post-hoc DB scrub / rely on writePolicy | `pluginDown = "/app/plugins/bloom"` | writePolicy does NOT run on router-down; pluginDown is the only gate |

## Gotchas (must-read)

1. **`db` is NOT in the router config.** It comes from the global strfry.conf. Passing the router file as `--config` breaks everything (you get the DB but no streams). Keep them separate; router file = positional arg.
2. **`writePolicy` ≠ `pluginDown`.** Global writePolicy is bypassed by router. Set `pluginDown` explicitly or lose whitelist gating (silent regression).
3. **No backfill.** Implicit `limit:0` on every down subscription — you get the live tail only, never history. Zero events on quiet relays in 30s is normal, not a failure. Use `strfry sync`/`download` for historical events.
4. **URL array syntax:** whitespace/newline separated, **no commas** (taocpp array).
5. **First-load parse error = hard exit.** A malformed router config `::exit(1)`s on startup (logs `Failed to parse router config`). After a successful first load, later hot-reload parse errors are logged and the OLD config is kept.
6. **Avoid `dir="both"` for pure ingest.** `both` echoes each just-downloaded event back to its source (rejected as dup) — wasteful. Use `down` for one-way pull.
7. **Plugin path is inside the container** (`/app/plugins/bloom`) — the router process must be able to exec it with the same working assumptions as the relay's writePolicy invocation. It receives `sourceType:"stream"` (not `"client"`); verify the bloom plugin ignores `sourceType`.
8. **Config file is watched** (`file_change_monitor` on the router config AND on `<dbDir>/data.mdb`). Editing the router file live triggers a minimally-invasive reconcile; you do not need to restart to change the relay list.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `/app/plugins/bloom` ignores `sourceType` and gates purely on bloom membership | Q4 | If it branches on `sourceType`, stream-sourced events may be wrongly accepted/rejected; verify by reading the plugin. Not a strfry fact — project-specific. |
| A2 | The container's `strfry` binary is exactly tag `1.1.0` | all | Confirmed docs/router.md is byte-identical between master and tag 1.1.0; if the deployed binary is a beta or fork, re-verify `cmd_router.cpp`. |

## Sources

### Primary (HIGH confidence)
- `hoytech/strfry` @ tag **1.1.0** — `docs/router.md` (verified byte-identical to master), `src/apps/mesh/cmd_router.cpp` (full source read), `golpe.yaml` (db config def), `strfry.conf` (writePolicy/db), `README.md` (config search order, router section).

## Metadata

**Confidence breakdown:**
- Config format / keys: HIGH — read from source + docs at exact tag
- Invocation / db relationship: HIGH — source (`env`/`dbDir` from golpe) + README config search order
- writePolicy vs pluginDown gating: HIGH — direct read of `incomingEvent()` write path
- Bloom-plugin `sourceType` behavior: LOW — project-specific, not verified (see A1)

**Research date:** 2026-07-01
**Valid until:** stable (pinned to tag 1.1.0; re-verify only if binary version changes)

---

# CORRECTION (source-verified at f31a1b9 + dockur/strfry + hoytech/golpe)

**Added:** 2026-07-01 (after empirical contradiction on the deployed host)
**Deployed build:** `dockurr/strfry:1.1.0` (Docker Hub image from the **`dockur/strfry`** GitHub repo — a *repackaging fork* of hoytech/strfry that `COPY . .` builds from its own vendored tree), pinned to hoytech commit `f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135` (2026-03-17).

## TL;DR — the schema was RIGHT; the failure was a two-files confusion

I read the EXACT source that this binary is built from:
- `dockur/strfry` `src/apps/mesh/cmd_router.cpp` (its router). It differs from hoytech only by added **compression/byte-count stats** and `filterCompiled.addFilters()` — **the config schema is byte-identical**: top-level key `streams`, per-stream `dir`/`urls`/`filter`/`pluginDown`/`pluginUp`, `USAGE = "router <routerConfigFile>"` (positional).
- `hoytech/golpe` `config.cpp.tt` (the global config loader shared by ALL subcommands).

The `streams { }` top-level key **is correct** for this build. `connections { }` was never valid (that guess was wrong, and it "did nothing" for the same reason `streams{}`-in-the-wrong-file did nothing). The zero-streams behavior was NOT a schema problem — it was caused by putting the `streams` block in the **golpe global config file** (the `--config` file) instead of in the **separate positional router-config file**. `[VERIFIED: dockur/strfry@master src/apps/mesh/cmd_router.cpp; hoytech/golpe@master config.cpp.tt]`

## Root cause of "parses as 'successfully installed' but activates ZERO streams"

There are **two entirely separate config-load paths**, and they log almost-identical lines that are easy to conflate:

| Path | Loaded by | Log line | Reads `streams`? |
|------|-----------|----------|------------------|
| **Global golpe config** (db, relay.*, dbParams.*) | `loadConfig()` in golpe `config.cpp.tt`, via `--config` / `STRFRY_CONFIG` / `/etc/strfry.conf` / `./strfry.conf` | `CONFIG: Loading config from file: <f>` then `CONFIG: successfully installed` | **NO** |
| **Router config** (the `streams{}` topology) | `reconcileConfig()` in `cmd_router.cpp`, via the **positional** `<routerConfigFile>` | `Loading router config file: <f>` | **YES** (`routerConfig.at("streams")`) |

`loadConfig()` in golpe iterates ONLY over the declared config keys (`db`, `relay.*`, `dbParams.*`, `events.*`, …) generated from `golpe.yaml`. **It has no schema validation for unknown top-level keys — it silently ignores them.** So when you did `strfry --config=/tmp/full.conf router` with a `streams{}` block appended to `full.conf`:

1. golpe loads `/tmp/full.conf`, reads `db`/`relay`/etc., **ignores `streams`** (not a declared key) → logs `CONFIG: successfully installed`. ✅ parses.
2. `cmd_router.cpp`'s `reconcileConfig()` then runs `loadRawTaoConfig(routerConfigFile)` on the **positional** arg. In your `--config=... router` invocation there was **no positional `<routerConfigFile>`**, so it loaded an empty/absent path → `routerConfig.at("streams")` threw / found nothing → **zero stream groups created** → 0 connections, `pluginDown` never spawned, 0 events. ❌ inert.

That is the exact "successfully installed yet zero streams" you observed. The `streams` block was read by nobody: golpe discarded it (unknown key) and the router looked for it in the *other* file.

### Reconciling your finding #1 ("positional file ignored, only /etc/strfry.conf loaded")

The source cannot ignore the positional arg — `Router(routerConfigFile)` stores it and `reconcileConfig()` logs `Loading router config file: <positional>` and `::exit(1)`s on first-load parse failure. What you saw as "only /etc/strfry.conf loaded" is the **golpe global** line (`CONFIG: Loading config from file: /etc/strfry.conf`), which ALWAYS fires first (every subcommand needs the db). The router file has its OWN distinct line (`Loading router config file:`). Two things to check on the host that explain the symptom:
- If the positional file had **no `streams` block** (or a taocpp parse error), `reconcileConfig()` throws and, on first load, the process `::exit(1)`s — so you'd never reach the connection phase. Grep stderr for `Failed to parse router config`.
- The two log lines are near-identical ("...config from file..." vs "...router config file..."). It is very easy to see the golpe line and conclude the positional was ignored.

## The CORRECT recipe for this build

**Keep the two files separate.** The router config is a **standalone file containing ONLY `streams{}`**, passed as the **positional** argument. The `db` comes from the global config (found via the search order — in the container it's `/etc/strfry.conf`).

### Router config — `/app/strfry-router.conf` (positional file, streams ONLY, NO db/relay)

```
connectionTimeout = 20
verbose = true

streams {
    ingest {
        dir        = "down"
        pluginDown = "/app/plugins/bloom"
        filter     = {}

        urls = [
            "wss://relay.example-1.com",
            "wss://relay.example-2.com"
            # ...all 174 URLs...
        ]
    }
}
```

> **Array syntax note (corrects the base spec above):** dockur's shipped `strfry-router.conf` uses **comma-separated** URLs (`"wss://nos.lol",` `"wss://soloco.nl"`). taocpp::config accepts BOTH comma- and whitespace-separated arrays, so either works — but since your deployed image ships the comma style, use commas to match the reference and avoid doubt.

### Exact command line

```bash
# In the container (cwd /app, db path from /etc/strfry.conf):
/app/strfry router /app/strfry-router.conf

# Or explicit global config + positional router file:
/app/strfry --config=/etc/strfry.conf router /app/strfry-router.conf
```

`--config` (if given) is the GLOBAL golpe flag and comes BEFORE `router`. The router file is the POSITIONAL arg AFTER `router`. **Never** merge `streams{}` into the `--config` file — golpe silently drops it.

### Using dockur's built-in entrypoint instead of invoking directly

dockur's `strfry.sh` entrypoint already wires this up. Set the container env var `ROUTER` to the path of your router config; the entrypoint runs `./strfry relay &` then `./strfry router "$ROUTER" &`:

```yaml
environment:
  ROUTER: /etc/strfry-router.conf   # bind-mount your streams{} file here
```

(Setting `ROUTER=1`/`yes` makes it use the baked-in `/etc/strfry-router.conf`, which ships a sample `streams{ friends{...} }` block — replace it with your 174-relay `ingest` stream and point `pluginDown` at `/app/plugins/bloom`.) Note dockur also has a legacy `STREAMS=relay1,relay2` env that spawns one deprecated `strfry stream ... --dir down` per entry — that is exactly the 174-process pattern you are replacing; use `ROUTER`, not `STREAMS`. `[VERIFIED: dockur/strfry@master strfry.sh]`

## Expected healthy startup log (all must appear)

```
CONFIG: Loading config from file: /etc/strfry.conf
CONFIG: successfully installed
Loading router config file: /app/strfry-router.conf     <-- MUST see this
New stream group [ingest]                                <-- MUST see this
ingest: Connecting to wss://relay.example-1.com          <-- MUST see this
ingest: Connected to wss://relay.example-1.com
```

If you see the first two lines but NOT `Loading router config file:` / `New stream group` / `Connecting to`, the router config file was not supplied as the positional arg (or failed to parse). That is the failure you hit.

## Corrected gotchas (supersede/augment the base spec)

1. **Top-level key is `streams` — CONFIRMED for f31a1b9 / dockur build.** `connections` is never valid.
2. **The `streams` block MUST live in the positional router file, NOT the `--config` global file.** golpe silently ignores unknown top-level keys in the global config, so a misplaced `streams{}` "installs successfully" and does absolutely nothing. This is the #1 trap and the cause of your zero-stream result.
3. **db/relay settings must NOT be in the router file, and streams must NOT be in the global file.** One concern per file.
4. **`pluginDown = "/app/plugins/bloom"` is still mandatory** to gate downloaded events — the global `writePolicy` does NOT apply to router-down events (unchanged from base spec; re-verified in dockur's `incomingEvent()`, which writes straight to `router->writer` gated only by `pluginDown`).
5. **Still no historical backfill:** implicit `"limit":0` on every down subscription (`filterToSend["limit"] = 0`). 0 events in 30s on quiet relays is normal; use `strfry sync`/`download` for history.
6. **URL array:** commas OK (matches dockur's shipped file); whitespace also OK.
7. **First-load parse error = hard `::exit(1)`.** If the router process dies immediately, the positional file has a syntax error or missing `streams` — check stderr for `Failed to parse router config`.

## Corrected sources

- **`dockur/strfry`** @ `master` — `src/apps/mesh/cmd_router.cpp` (the ACTUAL router compiled into `dockurr/strfry:1.1.0`; diffed against hoytech@f31a1b9 — schema identical, only stats added), `strfry-router.conf` (shipped sample, `streams{}`, comma arrays), `strfry.sh` (entrypoint: `ROUTER`/`STREAMS` env wiring), `Dockerfile` (`COPY . .` fork build). — [github.com/dockur/strfry](https://github.com/dockur/strfry)
- **`hoytech/strfry`** @ `f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135` — `src/apps/mesh/cmd_router.cpp` (positional `router <routerConfigFile>`, `reconcileConfig()` reads `routerConfig.at("streams")`). — [github.com/hoytech/strfry](https://github.com/hoytech/strfry)
- **`hoytech/golpe`** @ `master` — `config.cpp.tt` (`loadConfig()` reads only declared keys, logs `CONFIG: Loading config from file:` + `CONFIG: successfully installed`, silently ignores unknown top-level keys; `loadRawTaoConfig()`). — [github.com/hoytech/golpe](https://github.com/hoytech/golpe)
- [dockurr/strfry on Docker Hub](https://hub.docker.com/r/dockurr/strfry)

**Correction confidence:** HIGH for schema, invocation, and root-cause (all read from the exact compiled source + golpe loader). LOW (unchanged) for whether `/app/plugins/bloom` special-cases `sourceType:"stream"` — verify the plugin.

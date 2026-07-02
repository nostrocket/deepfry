---
phase: 03-verify-gating-stability-on-host
plan: 01
verified: 2026-07-02T09:41:07Z
status: passed
score: 4/4 ROADMAP success criteria bound to captured host output
host: Gs-Mac-mini (Apple Silicon Mac mini, arm64) — the build/arch where 260701-l5s reproduces
soak_window_start: 2026-07-02T09:22:21Z
soak_window_end: 2026-07-02T09:41:07Z
container_started_at: 2026-07-02T06:55:19Z (banked ~2h46m uptime, no restart across the window)
d02_fallback_fired: false
behavior_unverified: 0
overrides_applied: 0
---

# Phase 3 Plan 01: Live Gating & Stability Proof — Verification Evidence

**Phase Goal:** Prove on the live arm64 deploy host that the ALREADY-RUNNING single
`strfry router` path is production-safe — whitelist gating actually GATES router-down
events (both accept AND reject reason=not_in_wot, with rejects proven not-stored), the
canonical relay stays stable (no `MDB_BAD_RSLOT`, no restarts) under the single-router
process, and the neighbor services stay healthy — recorded as the release-blocker
verification gate that must pass before Phase 4 cutover (D-10).

**Verified:** 2026-07-02T09:41:07Z on `Gs-Mac-mini` (arm64) via the operator's live SSH
ControlMaster (`/tmp/ssh-deepfry-host.sock`, `g@192.168.149.21`, docker at
`/usr/local/bin`).
**Status:** passed — all four ROADMAP Phase 3 success criteria bound to captured host output.
**D-02 inject fallback:** did NOT fire — organic `decision=reject reason=not_in_wot`
events flow continuously on the router-down (`sourceType="Stream"`) path, so no controlled
upstream inject was needed.

## Proof procedure

Verification ran IN PLACE over the existing SSH ControlMaster; the router was LEFT
running throughout (D-08 — no revert to the 174-proc `strfry stream` fan-out, no config /
image / compose change). Observation only: `docker logs` (bounded windows — the log holds
3.68M+ bloom decision lines so all scans are `--tail` / `--since` / single-pass fixed-string
counts), `docker exec strfry ./strfry scan` (by-id relay query), `docker inspect`,
`docker stats`, and `curl` against the relay + neighbor ports.

1. **Soak window bound (SC#2):** captured a start snapshot (09:22:21Z) and an end snapshot
   (09:41:07Z) of `docker inspect` (Status/Health/RestartCount/StartedAt) + `curl 7777`,
   with the already-banked uptime since the Phase 2 recreate (StartedAt 06:55:19Z) counting
   toward "multi-hour" (D-04).
2. **Gating proof (SC#1):** sampled bloom `decision=accept` and `decision=reject
   reason=not_in_wot` lines with their event ids from a bounded recent window; confirmed the
   router-down origin via the `Received line:` payloads carrying `"sourceType":"Stream"`;
   then proved not-stored by querying the relay by id for a rejected event (empty) with an
   accepted event as positive control (present) — D-03.
3. **Neighbor health (SC#3):** functional probes (not just healthcheck status) of `strfry`
   (curl 7777), `strfry-quarantine` (curl 7778), and `lmdb2graphql` (`/health`, `/ready`,
   and a LIGHT `{__typename}` GraphQL query — deliberately NO heavy `latestPerAuthor`, D-07).
4. **Host posture (SC#4):** confirmed arm64 host, one `strfry router` process / zero `strfry
   stream` processes, LMDB unmoved — evidence recorded before any Phase 4 cutover step.

## Observable Truths

| # | Truth (ROADMAP SC) | Status | Evidence (captured host output) |
|---|--------------------|--------|---------------------------------|
| 1 | Live bloom `decision=accept` / `decision=reject` lines flow for router-down events — the `pluginDown` process is spawned and gating downloaded events, not silently bypassed (SC #1, ING-02) | ✓ VERIFIED | Router-down (`"sourceType":"Stream"`) events gated: `decision=accept id=000250447072d935… pubkey=e7d1d7ee` AND `decision=reject id=e6e312c3f78831c8… pubkey=f8460992 reason=not_in_wot`. Rejected id queried by id → **EMPTY** (not stored); accepted id queried by id → **event returned** (stored). Last-300k-line tally: 17,202 accepts + 88,454 rejects(not_in_wot) = 107,814 decisions. |
| 2 | strfry stays up with no `MDB_BAD_RSLOT` under the single-router process — the reader-slot contention the 174 `strfry stream` env-openers caused is gone (SC #2, OPS-02) | ✓ VERIFIED | Soak window 09:22:21Z → 09:41:07Z. `MDB_BAD_RSLOT` + LMDB reader/txn error grep over last 300,000 log lines = **0** (and 0 over the full log, confirmed at 3,680,188 total decision lines). `RestartCount` **0 → 0** unchanged; `StartedAt` 06:55:19Z unchanged (no restart / crash-loop / OOM). `curl 7777` → **200** at start AND end. 107,814 decisions in the last 300k lines = ingest actively flowing (up while doing work). |
| 3 | `strfry`, `strfry-quarantine`, and `lmdb2graphql` all remain healthy alongside the running router (SC #3, OPS-02) | ✓ VERIFIED | `curl localhost:7777` → **200** (strfry). `curl localhost:7778` → **200** (strfry-quarantine, Up 57m, healthy, RestartCount=0). `lmdb2graphql`: `/health` **200**, `/ready` **200**, cheap `{__typename}` → `{"data":{"__typename":"Query"}}` **200** (Up 57m, healthy, RestartCount=0, 6.6 MiB / 768 MiB). NO heavy `latestPerAuthor` query issued (D-07). |
| 4 | Verification performed on the live host and evidence recorded before any cutover step is taken (SC #4, OPS-02, D-08) | ✓ VERIFIED | Host `Gs-Mac-mini`, `uname -m` = **arm64**. `docker exec strfry pgrep -f 'strfry router'` = **1**; `'strfry stream'` = **0** (fan-out NOT restored). LMDB unmoved (Phase 2 proof; no config/image/compose change made). This artifact records the evidence before any Phase 4 fan-out-retirement step. |

**Score:** 4/4 ROADMAP Phase 3 success criteria bound to captured runtime host evidence.

## Captured host evidence

### SC#1 — Bloom gating on the router-down path (`sourceType="Stream"`)

Router-down origin confirmed — the plugin's `Received line:` payload carries
`"sourceType":"Stream"` (a router stream, NOT a `"client"` write through `writePolicy`):

```
[bloom-plugin] 2026/07/02 09:26:53 Received line: {"event":{…,"id":"e6e312c3f78831c8b7e17a498415df027e31b9c77567094e400a9b11cf2a557b","kind":20001,"pubkey":"f84609921a141c66cc62af3ab14c73d2a7c2970ff8e6baa49005367dd7a75064",…},"receivedAt":1782984413,"sourceInfo":"wss://relay.islandbitcoin.com/zenith-ember-juliet","sourceType":"Stream","type":"new"}
[bloom-plugin] 2026/07/02 09:26:53 decision=reject id=e6e312c3f78831c8b7e17a498415df027e31b9c77567094e400a9b11cf2a557b pubkey=f8460992 reason=not_in_wot
```

Captured `decision=accept` excerpts (router-down):

```
[bloom-plugin] 2026/07/02 09:22:27 decision=accept id=15ef1adb6ddb47e8cd1a333d5eb8e388dd985fb33e45f9e50fa988c54e1d8558 pubkey=311f3b58
[bloom-plugin] 2026/07/02 09:26:53 decision=accept id=000250447072d935f3d2f6e980c60fadfbf1f3027fce0a902ff0e9f005362c0f pubkey=e7d1d7ee
```

Captured `decision=reject reason=not_in_wot` excerpts (router-down):

```
[bloom-plugin] 2026/07/02 09:26:53 decision=reject id=e6e312c3f78831c8b7e17a498415df027e31b9c77567094e400a9b11cf2a557b pubkey=f8460992 reason=not_in_wot
[bloom-plugin] 2026/07/02 09:27:30 decision=reject id=4e3159c601190e5438f6eae0775f73c6a78f64c8ec752365d8a6d482407ce127 pubkey=2c4097e7 reason=not_in_wot
```

**Not-stored proof (D-03).** A sampled rejected event id, queried against the live relay
by id, returns EMPTY (event absent from LMDB); an accepted event id is present as a positive
control:

```
# REJECTED id — expected EMPTY (not stored)
$ docker exec strfry ./strfry scan '{"ids":["e6e312c3f78831c8b7e17a498415df027e31b9c77567094e400a9b11cf2a557b"]}'
… CONFIG: successfully installed
2026-07-02 09:27:56.412 ( 37.422s) [main thread ]INFO| atexit
# (no event JSON emitted — the rejected event is NOT in the LMDB)

# ACCEPTED id — positive control, expected PRESENT (stored)
$ docker exec strfry ./strfry scan '{"ids":["000250447072d935f3d2f6e980c60fadfbf1f3027fce0a902ff0e9f005362c0f"]}'
… CONFIG: successfully installed
2026-07-02 09:28:01.977 ( 4.954s) [main thread ]INFO| atexit
{"content":"sp_4c43bd1d.…","created_at":1782984413,"id":"000250447072d935f3d2f6e980c60fadfbf1f3027fce0a902ff0e9f005362c0f","kind":1,"pubkey":"e7d1d7eebd293f6039ea60b0267024828a411cd0ec51075b6f3735d019fa4628",…}
```

The gate demonstrably ACCEPTS + STORES in-WoT events and REJECTS + DROPS out-of-WoT events
on the router-down path — not a silent accept-all bypass.

Decision tally over the last 300,000 log lines:

```
$ docker logs --tail 300000 strfry 2>&1 | grep -c -F 'decision='          -> 107814
$ docker logs --tail 300000 strfry 2>&1 | grep -c -F 'decision=accept'    -> 17202
$ docker logs --tail 300000 strfry 2>&1 | grep -F 'decision=reject' | grep -c -F 'reason=not_in_wot' -> 88454
```

### SC#2 — Stability soak (no MDB_BAD_RSLOT, no restarts, relay serving, ingest flowing)

Soak window **start 2026-07-02T09:22:21Z** → **end 2026-07-02T09:41:07Z**:

```
# START (09:22:21Z)
Status=running Health=healthy RestartCount=0 StartedAt=2026-07-02T06:55:19.21803425Z
curl http://localhost:7777/ -> http_code=200

# END (09:41:07Z)
Status=running Health=healthy RestartCount=0 StartedAt=2026-07-02T06:55:19.21803425Z
curl http://localhost:7777/ -> http_code=200
docker ps -> strfry Up 3 hours (healthy)
```

`RestartCount` is **0 at both ends** and `StartedAt` (06:55:19Z, the Phase 2 recreate) is
unchanged — no crash-loop, OOM, or restart across the window; the banked uptime counts
toward the multi-hour soak (D-04).

LMDB reader/txn error grep — **0 hits**:

```
# last 300,000 log lines
$ docker logs --tail 300000 strfry 2>&1 | grep -c -E 'MDB_BAD_RSLOT|MDB_READERS_FULL|MDB_MAP_FULL|MDB_TXN_FULL' -> 0
$ docker logs --tail 300000 strfry 2>&1 | grep -c -F 'MDB_BAD_RSLOT'                                            -> 0

# full log (single-pass fixed-string count; 3,680,188 total decision lines)
$ docker logs strfry 2>&1 | grep -c -F 'MDB_BAD_RSLOT' -> 0
```

The 174-`strfry stream`-env-opener reader-slot contention that produced `MDB_BAD_RSLOT` is
gone under the single router. Ingest kept flowing across the window — 107,814 gate decisions
in the last 300k lines (relay up AND doing work, not idle).

### SC#3 — Neighbor-service functional probes

```
$ curl -sS -o /dev/null -w '%{http_code}' http://localhost:7777/         -> 200   # strfry
$ curl -sS -o /dev/null -w '%{http_code}' http://localhost:7778/         -> 200   # strfry-quarantine
$ curl -sS -o /dev/null -w '%{http_code}' http://localhost:8080/health   -> 200   # lmdb2graphql
$ curl -sS -o /dev/null -w '%{http_code}' http://localhost:8080/ready    -> 200   # lmdb2graphql
$ curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{"query":"{__typename}"}' http://localhost:8080/graphql
  -> {"data":{"__typename":"Query"}}   http_code=200                              # lmdb2graphql (LIGHT)

docker inspect:
  strfry             Status=running Health=healthy RestartCount=0
  strfry-quarantine  Status=running Health=healthy RestartCount=0   (Up 57 minutes)
  lmdb2graphql       Status=running Health=healthy RestartCount=0   (Up 57 minutes)

docker stats (--no-stream):
  strfry             564.3 MiB / 5.787 GiB   9.52%
  strfry-quarantine    5.312 MiB / 5.787 GiB  0.09%
  lmdb2graphql         6.594 MiB / 768 MiB    0.86%
```

Per D-07, `lmdb2graphql` was probed with `/health` + `/ready` + a cheap `{__typename}`
query ONLY — no heavy `latestPerAuthor`-style query was issued, so the known heavy-query
capacity crash (cold-IO/RAM on the 318GB LMDB / 8GB host) was NOT manufactured.

### SC#4 — On-host posture (router left running, fan-out not restored)

```
$ hostname                                          -> Gs-Mac-mini
$ uname -m                                           -> arm64
$ docker exec strfry pgrep -f 'strfry router' | wc -l -> 1
$ docker exec strfry pgrep -f 'strfry stream' | wc -l -> 0
```

Exactly one `strfry router` process and zero `strfry stream` processes — the fan-out was
NOT restored. No config / image / compose change was made; the LMDB stays at its host
bind-mount path (unchanged from the Phase 2 proof). Evidence was recorded before any Phase 4
cutover step.

## Host state left after the proof

- `strfry` container: `Status=running Health=healthy`, RestartCount=0, one `strfry router`
  process, relay serving 7777, ingest flowing (107k+ decisions in the last 300k lines).
- The 174-proc `strfry stream` fan-out remains stopped — NOT restored (D-08). Retiring
  `stream-relays.sh` is a Phase 4 concern.
- `strfry-quarantine` and `lmdb2graphql` left running/healthy — probed for HEALTH only,
  never modified (CLAUDE.md project-boundary rule).
- LMDB unmoved at its host bind-mount path (`/Volumes/BACKUP/nostr/strfry_database`; DB
  location HARD RULE). No `~/deepfry/` config file touched.

---

_Verified: 2026-07-02T09:41:07Z on Gs-Mac-mini (arm64) via live SSH ControlMaster._
_Gate verdict: **PASSED (4/4)** — ING-02 (live pluginDown accept + reject + not-stored) and
OPS-02 (stable relay, no MDB_BAD_RSLOT, healthy neighbors, on-host verification) both proven.
Phase 4 cutover is UNBLOCKED by this gate. ROADMAP Phase 3 SC #1–4 bound to captured host
evidence (D-10)._

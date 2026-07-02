---
phase: 04-retire-the-fan-out
plan: 01
verified: 2026-07-02T10:59:26Z
status: passed
score: 3/3 Phase 4 ROADMAP success criteria bound to captured host output
host: Gs-Mac-mini (Apple Silicon Mac mini, arm64) — the build/arch where 260701-l5s reproduced
container_started_at: 2026-07-02T06:55:19Z (unchanged since the Phase 2 recreate — NO restart across retirement)
orphan_stop_performed: false
wot_list_altered: false
d05_service_change: false
behavior_unverified: 0
overrides_applied: 0
---

# Phase 4 Plan 01: Fan-Out Retirement — Verification Evidence

**Phase Goal:** Retire the legacy `stream-relays.sh` fan-out now that the single
`strfry router` path is proven (Phases 1–3). Close the resurrection path in the
repo (`git rm stream-relays.sh` + README update) and RE-CONFIRM fresh on the live
host that exactly ONE `strfry router` runs, ZERO `strfry stream` processes remain
(no orphans, no lingering `strfry-streams` tmux session), and the Web-of-Trust
relay list is unchanged and still driving the router config as the single source
of truth — recorded as a Phase 4 evidence artifact mirroring
`03-VERIFICATION-EVIDENCE.md` (D-04).

**Verified:** 2026-07-02T10:59:26Z on `Gs-Mac-mini` (arm64) via the operator's live
SSH ControlMaster (`/tmp/ssh-deepfry-host.sock`, `g@192.168.149.21`, docker at
`/usr/local/bin`).
**Status:** passed — all three Phase 4 ROADMAP success criteria bound to captured host output.
**D-05 (no service change):** honored — NO strfry restart/recreate/reconfigure was
performed; `StartedAt` (06:55:19Z, the Phase 2 recreate) is unchanged.
**D-06 (failure handling):** no orphan `strfry stream` process and no lingering
`strfry-streams` tmux session were found, so no orphan-stop action was needed; the
WoT relay list was found intact (not altered/missing), so no blocker was raised.

## Proof procedure

Retirement is a repo edit (delete script + README) plus a READ-ONLY host
re-verification. The host verification ran IN PLACE over the existing SSH
ControlMaster; the router was LEFT running throughout — observation only
(`docker exec … pgrep/ps`, tmux check, `grep`/`awk`/`diff` over the two host
config files, `docker inspect`, `curl`). No bind-mounted config and no
`~/deepfry/` file was modified.

1. **Repo change (SC #1, local):** `git rm stream-relays.sh` (commit `3d3c71c`) —
   the script is gone from tracking and the working tree; `gen-router-config.sh`
   left untouched (both its `stream-relays.sh` provenance comments intact).
   README's `./stream-relays.sh` block replaced with the router ingestion path
   (commit `a43581a`).
2. **Process posture (SC #1, SC #2):** in-container `pgrep` tallies for
   `strfry router` (expect 1) and `strfry stream` (expect 0), plus the full
   `ps -o pid,args` listing.
3. **No lingering fan-out session (SC #2):** tmux `strfry-streams` session check
   on the host.
4. **WoT list intact + still driving the config (SC #3):** confirm
   `~/deepfry/web-of-trust.yaml` exists with its `relay_urls:` list, count the
   list items, and prove set-equality against the `wss://` URLs in the live
   `~/deepfry/strfry-router.config` (empty `diff`).

## Observable Truths

| # | Truth (Phase 4 ROADMAP SC) | Status | Evidence (captured host / repo output) |
|---|-----------------------------|--------|----------------------------------------|
| 1 | `stream-relays.sh` is retired (deleted) — it no longer spawns per-relay `strfry stream` processes; no tracked operational file can respawn the fan-out (SC #1, OPS-03) | ✓ VERIFIED | `git ls-files stream-relays.sh` → **empty** (untracked); working-tree file **absent** (commit `3d3c71c`). On the host: `docker exec strfry pgrep -f 'strfry stream' \| wc -l` = **0**. `gen-router-config.sh` unchanged — `grep -c 'stream-relays.sh'` = **2** (both provenance comments preserved). README `grep -c 'stream-relays'` = **0**. |
| 2 | No orphaned `strfry stream` processes remain on the host after cutover — only the single `strfry router`; no lingering `strfry-streams` tmux session (SC #2, OPS-03) | ✓ VERIFIED | `docker exec strfry pgrep -f 'strfry router' \| wc -l` = **1**; `…'strfry stream' \| wc -l` = **0**. Process listing: `PID 1 bash /app/strfry.sh`, `PID 7 ./strfry relay`, `PID 29 ./strfry router /app/strfry-router.config`, two `/app/plugins/bloom` (the pluginDown gate) — no `strfry stream`. tmux is **not installed** on the host → the `strfry-streams` fan-out session cannot exist (equivalent proof no session lingers). |
| 3 | The Web-of-Trust relay list remains in place and unchanged, still driving `strfry-router.config` as the single source of truth (SC #3, OPS-03, CFG-01) | ✓ VERIFIED | `~/deepfry/web-of-trust.yaml` present (`-rw-r--r-- … 8649 28 Jun 11:59` — unchanged since before Phase 2), `relay_urls:` header at line 38 with **174** list items. Live `~/deepfry/strfry-router.config` has **174** `wss://` entries. `diff` of the sorted unique URL sets (174 vs 174) is **empty → IDENTICAL SET**. Neither file was modified/regenerated. |

**Score:** 3/3 Phase 4 ROADMAP success criteria bound to captured runtime host + repo evidence.

## Captured host evidence

### SC #1 — Script retired (repo) + zero stream procs (host)

Repo (local working tree, post-commit `3d3c71c`):

```
$ git ls-files stream-relays.sh
                                   # (empty — untracked)
$ test -e stream-relays.sh && echo YES || echo NO
NO
$ grep -c 'stream-relays.sh' gen-router-config.sh
2                                  # both provenance comments intact (line 7, line 36)
$ grep -c 'stream-relays' README.md
0
```

Host process count (no per-relay stream fan-out):

```
strfry stream count: 0
```

### SC #2 — On-host process posture + no fan-out session

```
$ hostname
Gs-Mac-mini
$ uname -m
arm64

$ docker exec strfry pgrep -f 'strfry router' | wc -l
1
$ docker exec strfry pgrep -f 'strfry stream' | wc -l
0

$ docker exec strfry ps -o pid,args
PID   COMMAND
    1 bash /app/strfry.sh
    7 ./strfry relay
   29 ./strfry router /app/strfry-router.config
   38 /app/plugins/bloom
  186 /app/plugins/bloom
```

Exactly ONE `strfry router` process, ZERO `strfry stream` processes. The two
`/app/plugins/bloom` procs are the mandatory per-stream `pluginDown` gate — not a
fan-out. No `strfry stream` children exist.

`strfry-streams` tmux session check:

```
$ command -v tmux || echo "tmux not installed"
tmux not installed — no session possible
```

tmux is not installed on the host, so the fan-out's `strfry-streams` tmux session
(the mechanism `stream-relays.sh` used) cannot exist — equivalent proof that no
lingering fan-out session remains.

### SC #3 — WoT relay list unchanged & still driving the router config

```
$ ls -l ~/deepfry/web-of-trust.yaml
-rw-r--r--@ 1 g  staff  8649 28 Jun 11:59 /Users/g/deepfry/web-of-trust.yaml
$ grep -n '^relay_urls:' ~/deepfry/web-of-trust.yaml
38:relay_urls:
$ awk '/^relay_urls:/{f=1;next} f&&/^[[:space:]]+-[[:space:]]/{c++;next} f&&/^[^[:space:]]/{f=0} END{print c}' ~/deepfry/web-of-trust.yaml
174                                 # relay_urls: list item count

$ ls -l ~/deepfry/strfry-router.config
-rw------- 1 g  staff  8610  2 Jul 14:54 /Users/g/deepfry/strfry-router.config
$ grep -c 'wss://' ~/deepfry/strfry-router.config
174                                 # urls entries in the live router config
```

Sample spot-check (first 3 + last 3 of `relay_urls:`) — each present in the config:

```
FOUND in config: wss://cfrelay.haorendashutest.workers.dev/jade-zenith-prism
FOUND in config: wss://test.nfrelay.app
FOUND in config: wss://nostr-01.yakihonne.com
FOUND in config: wss://christpill.nostr1.com/zulu
FOUND in config: wss://kraftig.nostr1.com/nexus
FOUND in config: wss://syndicate.basspistol.org/inbox/onyx
```

Set-equality (sorted unique `relay_urls:` vs sorted unique config `wss://` URLs):

```
wot unique:  174
cfg unique:  174
$ diff <sorted relay_urls> <sorted config urls>
                                   # (no output)
IDENTICAL SET
```

The WoT relay list (`~/deepfry/web-of-trust.yaml` `relay_urls:`) is unchanged and
the live `strfry-router.config` reflects exactly that same 174-relay set — the
single source of truth is intact. Neither file was modified or regenerated.

### Container status (context — no service change, D-05)

```
$ docker inspect strfry
Status=running Health=healthy RestartCount=0 StartedAt=2026-07-02T06:55:19.21803425Z
$ curl -sS -o /dev/null -w '%{http_code}' http://localhost:7777/
200
```

`StartedAt` is **06:55:19Z** — the Phase 2 recreate timestamp, unchanged; `RestartCount`
is **0**. No strfry restart/recreate/reconfigure was performed as part of retirement
(D-05); the live router state proven in Phase 3 is preserved.

## Host state left after the proof

- `strfry` container: untouched — `Status=running Health=healthy`, `RestartCount=0`,
  `StartedAt` unchanged (06:55:19Z), one `strfry router` process, relay serving 7777.
- No orphan `strfry stream` process and no `strfry-streams` tmux session existed, so
  nothing was stopped (D-06). No fan-out was restored.
- `~/deepfry/web-of-trust.yaml` and `~/deepfry/strfry-router.config` were read only —
  NOT modified or regenerated (T-04-01 mitigated; WoT list is the protected single
  source of truth).
- No bind-mounted config, no image/compose change, LMDB unmoved (DB-location HARD RULE;
  no relocation attempted).

---

_Verified: 2026-07-02T10:59:26Z on Gs-Mac-mini (arm64) via live SSH ControlMaster._
_Gate verdict: **PASSED (3/3)** — OPS-03 satisfied: `stream-relays.sh` retired (deleted,
no resurrection path in tracked operational files), no orphaned `strfry stream` fan-out
on the host (1 router / 0 stream, no `strfry-streams` session), and the WoT relay list
unchanged & still driving the router config as the single source of truth. All three
Phase 4 ROADMAP success criteria bound to captured host + repo evidence._

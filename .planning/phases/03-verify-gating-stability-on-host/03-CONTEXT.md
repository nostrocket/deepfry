# Phase 3: Verify Gating & Stability On-Host - Context

**Gathered:** 2026-07-02
**Status:** Ready for planning

<domain>
## Phase Boundary

On the live arm64 deploy host (`Gs-Mac-mini`), prove the **already-running**
single-`strfry router` path is production-safe **before any Phase 4 cutover**:

1. Bloom gating actually **gates** router-down events — both `decision=accept`
   AND `decision=reject reason=not_in_wot` flow on the `pluginDown` path (not a
   silent accept-all bypass).
2. The relay stays healthy under the single-router process — **no
   `MDB_BAD_RSLOT`** across a sustained window.
3. `strfry`, `strfry-quarantine`, and `lmdb2graphql` all stay healthy alongside
   the running router.
4. Verification is done on the live host and **evidence recorded** before any
   cutover step is taken.

This is a **verification / observation** phase — no new build. The router is
already live from the Phase 2 recreate (fan-out stopped); Phase 3 verifies that
live state and leaves it running.

**NOT in scope:**
- Retiring / rewriting `stream-relays.sh` and clearing orphaned procs → **Phase 4**.
- Any config rewrite or image change — verification only.

</domain>

<decisions>
## Implementation Decisions

### Gating proof — accept AND reject on the router-down path (ING-02, SC #1)
- **D-01:** Prove BOTH decisions flow. **Organic rejects are already confirmed
  live** — the operator observed `decision=reject reason=not_in_wot` for many
  events in the strfry console log (2026-07-02). Capture the actual reason
  string (`reason=not_in_wot`) as evidence; the organic branch of the proof is
  effectively satisfied, so the phase mostly **records + formalizes** it plus
  the not-stored confirmation below.
- **D-02:** Inject-via-upstream is a **FALLBACK only** (organic-first). If a
  deterministic, controlled reject tied to a known event id is wanted: publish a
  non-WoT test event to **one of the 174 upstream relays the router streams
  FROM**, so it arrives **router-down (`sourceType="stream"`)** and hits
  `pluginDown`. Do NOT publish directly to our relay as a client — client writes
  go through `writePolicy` (`sourceType="client"`), a **different path** that
  would not prove the router-down gate.
- **D-03:** Pass condition for a proven reject = **`decision=reject` line
  (`reason=not_in_wot`) AND the rejected event absent from LMDB** — sample a
  rejected event id and query the relay by id → no result. Log line + not-stored,
  not log line alone.

### Stability soak (OPS-02, SC #2)
- **D-04:** **Overnight / multi-hour soak** under real live ingest before calling
  it stable (the single router removes the 174 env-openers, but `MDB_BAD_RSLOT`
  is intermittent under sustained pressure — a controlled window is required).
  The already-banked uptime since the Phase 2 recreate counts toward this.
- **D-05:** "Stayed up clean" = ALL of, across the whole window:
  - **No `MDB_BAD_RSLOT`** (and no other LMDB reader/txn errors) in strfry logs.
  - **No container restarts** — strfry stays Up/healthy, no crash-loop / OOM /
    restart-count increment.
  - **Relay serving throughout** — `curl` 7777 → 200 at start and end (sampled
    during).
  - **Ingest kept flowing** — streams stayed connected and gate decisions kept
    accumulating (up while actually doing work, not idle).

### Neighbor-service health (OPS-02, SC #3)
- **D-06:** **Functional probes**, not just docker healthcheck status: actively
  probe `strfry` (`curl` 7777), `strfry-quarantine` (responds), and
  `lmdb2graphql` (endpoint reachable) alongside the running router.
- **D-07:** `lmdb2graphql` gets a **LIGHT / bounded probe only** — health
  endpoint + a cheap query to confirm it responds under the live router.
  **Deliberately avoid heavy `latestPerAuthor`-style queries** so verification
  never manufactures the known heavy-query capacity crash (cold-IO/RAM bound on
  the 318GB LMDB / 8GB host); a crash there would be unrelated to the router
  conversion.

### Verification posture (OPS-02, SC #4)
- **D-08:** **Verify in place, leave the router live** through to Phase 4
  cutover. The host is already router-only (fan-out stopped in Phase 2); the
  overnight soak IS the running state. Do NOT revert to the 174-proc `strfry
  stream` fan-out — that re-introduces the exact `MDB_BAD_RSLOT` risk this
  conversion escapes.
- **D-09:** **Failure handling (no auto-revert):** if the soak surfaces
  `MDB_BAD_RSLOT`, or the reject cannot be proven, or a neighbor service is
  unhealthy → verification **FAILS the gate and BLOCKS Phase 4 cutover**.
  Surface it as a blocker for an explicit remediation decision — do not silently
  restore the fan-out.
- **D-10:** **Record evidence** into a phase artifact before "verified" — mirror
  the Phase 2 pattern (`02-VERIFICATION-EVIDENCE.md`): bind each ROADMAP SC to
  actual captured host output (log excerpts, grep tallies, process/inspect
  output), not paraphrase. This is the verify-on-host discipline that correctly
  blocked `260701-l5s`.

### Claude's Discretion
- Exact grep / probe / `docker logs|exec|inspect` / relay-query commands; the
  precise soak window length within "overnight / multi-hour"; the evidence
  artifact's format and location (follow the `02-VERIFICATION-EVIDENCE.md`
  shape); whether the D-02 inject fallback is exercised at all (organic rejects
  already satisfy the reject proof); which upstream relay is used if inject is
  needed; the SSH/deploy sequence.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase 3 scope + requirements
- `.planning/ROADMAP.md` — Phase 3 section (the 4 success criteria this phase
  must bind to captured host evidence).
- `.planning/REQUIREMENTS.md` — **ING-02** (per-stream `pluginDown` gating,
  owned/verified live in Phase 3) and **OPS-02** (on-host verification of
  config-load, stream connect, live accept/reject, single process, stable relay).

### Phase 2 output — the live state Phase 3 verifies (read FIRST)
- `.planning/phases/02-wire-prove-config-loads/02-VERIFICATION-EVIDENCE.md` —
  the config-load proof already captured on `Gs-Mac-mini`: `Loading router config
  file:` fired, 174 streams (162 connected), ONE router process, LMDB unmoved,
  165,667 gate decisions + `decision=accept` observed. **The "Bloom pluginDown
  gating" section explicitly defers the LIVE accept/reject *gating* proof to
  Phase 3 (T-02-09).** Also the evidence-artifact format to mirror (D-10) and the
  live SSH ControlMaster access details.
- `.planning/phases/02-wire-prove-config-loads/02-CONTEXT.md` — D-09 fan-out
  handling, the `sourceType:"stream"` (router-down) vs `"client"` gotcha, the
  `git pull` bind-mount-truncation → recreate gotcha.

### The blocker + root cause (source-verified)
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-RESEARCH.md`
  — bloom `sourceType:"stream"` for router-down events, the `MDB_BAD_RSLOT`
  reader-slot root cause (174 env-openers), and why the single router fixes it.

### Files / assets this phase observes
- `docker-compose.strfry.yml` — the `strfry` service now carrying `ROUTER=` +
  the router bind-mount (from Phase 2). Also defines `strfry-quarantine` and
  the LMDB bind-mount.
- `gen-router-config.sh` — Phase 1 generator; `~/deepfry/strfry-router.config`
  (174 relays, `pluginDown = "/app/plugins/bloom"`) is the live config being
  verified.
- Bloom whitelist plugin at container path `/app/plugins/bloom` — the
  `pluginDown` process emitting `decision=accept` / `decision=reject
  reason=not_in_wot`.

### Project rules
- `CLAUDE.md` — DB-location HARD RULE (never move the LMDB / no named volume);
  StrFry stays stock; `~/deepfry/` config-dir protection; project-boundary rule
  (`lmdb2graphql`/`whitelist-plugin` etc. are sibling projects — only probed for
  health here, not modified).

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `02-VERIFICATION-EVIDENCE.md` — the template/shape for Phase 3's evidence
  artifact (per-SC observable-truths table bound to captured host output, grep
  tallies, process listing, `docker inspect` excerpts).
- Live SSH ControlMaster to the deploy host (`/tmp/ssh-strfry.sock`,
  `g@192.168.149.21`, `Gs-Mac-mini` arm64) — the same access Phase 2 used for
  autonomous on-host proof.

### Established Patterns
- Host-run operator verification over SSH: `docker logs strfry` grep tallies,
  `docker exec strfry pgrep/ps`, `docker inspect`, `curl localhost:7777`.
- The blocker is build/arch-specific — verification MUST run on the live arm64
  host; a local/CI check cannot substitute.

### Integration Points
- Router-down events → `pluginDown = /app/plugins/bloom` → `decision=` lines
  (`sourceType="stream"`). Client writes go through `writePolicy` instead.
- LMDB at host `/Volumes/BACKUP/nostr/strfry_database` → container
  `/app/strfry-db` (shared read pressure across strfry relay + router +
  `lmdb2graphql`).
- `lmdb2graphql` adapter at `192.168.149.21:8080` (`mem_limit 768m`) — probe
  lightly (D-07).

### Gotchas
- **`lmdb2graphql` heavy-query capacity bound** — heavy `latestPerAuthor`
  crashes it (cold-IO/RAM on 318GB LMDB / 8GB host). Do NOT trigger it during
  verification (D-07).
- **Router-down vs client path** — a client write to our relay does NOT prove
  the `pluginDown` gate; the injected-reject fallback must arrive via a stream
  (D-02).
- **`git pull` truncates single-file config bind-mounts** → recreate the
  container to fix (only relevant if any host-side change is delivered via git).

</code_context>

<specifics>
## Specific Ideas

- **Organic rejects already observed live (2026-07-02):** the operator sees
  `decision=reject reason=not_in_wot` for many events in the strfry console log.
  Combined with Phase 2's confirmed `decision=accept` + 165k decisions, both
  sides of the gate are already demonstrable on the host — Phase 3 primarily
  **records/formalizes** this (plus the not-stored confirmation and the soak),
  rather than having to manufacture a reject.
- Treat Phase 3 as the **release-blocker gate**: this is where "gating actually
  gates" and "relay stays stable" become recorded fact before Phase 4 retires
  the fan-out.

</specifics>

<deferred>
## Deferred Ideas

- **Retiring / rewriting `stream-relays.sh`** and ensuring no orphaned `strfry
  stream` procs remain → **Phase 4**.
- **Hot-reload-driven relay add/remove** (rewrite the config live, no restart) —
  an ops capability beyond the verification gate.

### Reviewed Todos (not folded)
None — no pending todos matched Phase 3.

</deferred>

---

*Phase: 3-verify-gating-stability-on-host*
*Context gathered: 2026-07-02*

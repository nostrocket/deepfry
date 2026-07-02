# Phase 3: Verify Gating & Stability On-Host - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-02
**Phase:** 3-verify-gating-stability-on-host
**Areas discussed:** Reject-proof method, Stability bar / soak window, 3-service health depth, Verification posture

---

## Reject-proof method

### Q1 — How to prove the gate rejects out-of-WoT events?
| Option | Description | Selected |
|--------|-------------|----------|
| Inject a test event | Publish from a known non-whitelisted pubkey via router-down; deterministic but synthetic | |
| Observe organic rejects | Grep live logs for natural `decision=reject`; zero-touch but non-deterministic | |
| Both: inject + organic | Organic first, inject to force a deterministic reject if none appear | ✓ |

### Q2 — Which inject vector exercises the router-down (pluginDown) path?
| Option | Description | Selected |
|--------|-------------|----------|
| Publish to an upstream relay | Publish to one of the 174 upstreams the router streams FROM → arrives router-down | ✓ |
| Temp upstream in config | Add a throwaway relay to the config for the test (config rewrite, revert after) | |
| You decide (planner picks) | Leave mechanism open, constraint: must arrive `sourceType="stream"` | |

### Q3 — Pass condition for a proven reject?
| Option | Description | Selected |
|--------|-------------|----------|
| Log line + not-stored | `decision=reject` line AND event absent from LMDB (query by id) | ✓ |
| Log line only | A `decision=reject` line is sufficient | |
| You decide | Leave the exact assertion to the planner | |

**User's choice:** Both (organic-first, inject-via-upstream fallback); pass = log line + not-stored.
**Notes:** Mid-discussion the operator reported seeing `decision=reject reason=not_in_wot` for many
events in the live strfry console log — so organic rejects are already confirmed on the host. This
demotes the inject-via-upstream step to a fallback and shifts Phase 3 toward recording/formalizing
the already-live gating plus the not-stored confirmation. Client writes go through `writePolicy`
(different path) and would NOT prove the router-down gate.

---

## Stability bar / soak window

### Q1 — How long must it run clean before "stable"?
| Option | Description | Selected |
|--------|-------------|----------|
| Overnight / multi-hour | Several hours under real ingest, zero MDB_BAD_RSLOT across the window | ✓ |
| Short soak (30–60 min) | Faster verdict; risks missing intermittent contention | |
| Point-in-time snapshot | Grep current logs since Phase 2 recreate; fastest | |

### Q2 — Which health signals must hold across the window? (multi-select)
| Option | Description | Selected |
|--------|-------------|----------|
| No MDB_BAD_RSLOT | Zero MDB_BAD_RSLOT / LMDB reader-txn errors | ✓ |
| No container restarts | strfry stays Up/healthy, no crash-loop/OOM | ✓ |
| Relay serving throughout | curl 7777 → 200 at start and end | ✓ |
| Ingest kept flowing | Streams connected + gate decisions accumulating | ✓ |

**User's choice:** Overnight/multi-hour soak; all four health signals required.
**Notes:** MDB_BAD_RSLOT is intermittent under load — a controlled sustained window is required;
uptime banked since the Phase 2 recreate counts toward it.

---

## 3-service health depth

### Q1 — How deep should the neighbor-service health check go?
| Option | Description | Selected |
|--------|-------------|----------|
| Functional probes | Actively probe each (curl 7777, l2g endpoint, quarantine responds) | ✓ |
| Docker healthcheck status | Rely on `docker inspect`/`compose ps` Health=healthy | |
| You decide | Planner picks per-service probes | |

### Q2 — How to probe lmdb2graphql given its heavy-query capacity bound?
| Option | Description | Selected |
|--------|-------------|----------|
| Light probe only | Health endpoint + cheap query; avoid heavy latestPerAuthor | ✓ |
| Include a representative query | Heavier query to prove it serves under router pressure; risks the known crash | |
| You decide | Planner picks query weight | |

**User's choice:** Functional probes on all three; lmdb2graphql gets a light/bounded probe only.
**Notes:** lmdb2graphql's heavy-query crash (cold-IO/RAM on 318GB LMDB / 8GB host) is unrelated to
the router conversion — verification must not manufacture it.

---

## Verification posture

### Q1 — What posture, given the router is already live and fan-out stopped?
| Option | Description | Selected |
|--------|-------------|----------|
| Verify in place, leave live | Verify the running router, leave it live through to Phase 4 | ✓ |
| Define a rollback trigger | Same, plus a pre-defined condition that restores the fan-out | |
| You decide | Planner decides posture + rollback handling | |

**User's choice:** Verify in place, leave live.
**Notes:** Reverting to the 174-proc fan-out would re-introduce the MDB_BAD_RSLOT risk. Captured
implied failure-handling: if the soak surfaces MDB_BAD_RSLOT or the reject can't be proven,
verification FAILS the gate and BLOCKS Phase 4 cutover — surfaced as a blocker, not an auto-revert.

---

## Claude's Discretion

- Exact grep / probe / `docker logs|exec|inspect` / relay-query commands.
- Precise soak window length within "overnight / multi-hour".
- Evidence artifact format/location (mirror `02-VERIFICATION-EVIDENCE.md`).
- Whether the inject-via-upstream fallback is exercised at all (organic rejects already satisfy it).
- Which upstream relay is used if inject is needed; the SSH/deploy sequence.

## Deferred Ideas

- Retiring / rewriting `stream-relays.sh` and clearing orphaned procs → Phase 4.
- Hot-reload-driven relay add/remove (live config rewrite, no restart) → beyond the verification gate.

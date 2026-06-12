# Phase 7: Relay Health Management - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-12
**Phase:** 7-relay-health-management
**Areas discussed:** Failure counting & decay, Classification & ejection policy, Filter-cap recovery, Log summary format

---

## Failure counting & decay

| Option | Description | Selected |
|--------|-------------|----------|
| Halve it | failures = failures / 2 on successful reconnect; matches roadmap wording; flapping relay trends upward | ✓ |
| Subtract 1 | Slower forgiveness; one bad day takes longer to clear | |
| No decay on reconnect | Only a successful query reduces the counter | |

**User's choice:** Halve it (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Reset to 0 | A completed query is real proof of health; only genuine work clears the record | ✓ |
| Halve it too | Same decay as reconnect; penalizes intermittent-but-valuable relays | |
| Reset only after N consecutive good queries | Success-streak tracking; extra state | |

**User's choice:** Reset to 0 on successful query (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Count as a failure | Failed reconnect increments transport class with backoff; ejection only via threshold — one unified path | ✓ |
| Keep immediate removal | Current behavior at crawler.go:226; aggressive | |
| Count double | Middle ground | |

**User's choice:** Count as a failure (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| In-memory only | Counters survive reconnects within a run, lost on restart; matches requirement scope | ✓ |
| Persist to config YAML | Survives restarts but mixes operational state into settings | |
| Persist to a separate state file | New file format + lifecycle | |

**User's choice:** In-memory only (Recommended)

---

## Classification & ejection policy

| Option | Description | Selected |
|--------|-------------|----------|
| Per-class counters | One counter per failure class on relayState; thresholds compare like-to-like | ✓ |
| Single counter + class tally | Simpler state but blended ejection totals | |

**User's choice:** Per-class counters (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Nested map, defaults 10/3/5 | relay_ejection_thresholds: {transport: 10, filter_rejection: 3, subscription_flap: 5} | ✓ |
| Nested map, uniform 5 | Behavior-preserving baseline | |
| Flat keys | Matches flat-key style at cost of three top-level keys | |

**User's choice:** Nested map, defaults 10/3/5 (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Move to ejected_relays list | Remove from relay_urls AND append URL to ejected_relays; discover-relays can skip; ops can rehabilitate | ✓ |
| Just remove (current behavior) | discover-relays would re-add bad relays | |
| Ejected list with metadata | Reason/timestamp/count in YAML; config becomes a structured log | |

**User's choice:** Move to ejected_relays list (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Map existing error paths | NOTICE/floor → filter-rejection; subscriptionError → flap; transportError → transport; deterministic, testable | ✓ |
| Connect-age heuristic | New timing window for flap detection | |
| You decide | Claude picks during planning | |

**User's choice:** Map existing error paths (Recommended)

---

## Filter-cap recovery

| Option | Description | Selected |
|--------|-------------|----------|
| Probe-up by doubling | After a success streak at cap C, next batch tries min(C*2, default); rejection re-halves via existing handling | ✓ |
| Slow additive decay | Cap drifts up per interval; noisier | |
| Timed full reset | Re-runs the cascade rarely | |

**User's choice:** Probe-up by doubling (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| 10 successful batches | ~6-7 min per doubling step; halved-to-10 relay recovers to 100 in ~30 min | ✓ |
| 25 successful batches | More conservative | |
| You decide | Claude picks streak length | |

**User's choice:** 10 successful batches (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Exempt probe rejections | Self-inflicted rejections halve cap + reset streak only; no ejection increment | ✓ |
| Count them | Healthy capped relays would slowly accumulate ejection points | |

**User's choice:** Exempt probe rejections (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| In-memory only | Consistent with counters; re-learn after restart with quiet logging | ✓ |
| Persist caps in YAML | Avoids one re-learn per restart at cost of continual config mutation | |

**User's choice:** In-memory only (Recommended)

---

## Log summary format

| Option | Description | Selected |
|--------|-------------|----------|
| Only on change | Sweep summary printed only when a relay reconnected, was ejected, or newly stayed dead | ✓ |
| Every sweep that attempts work | More heartbeat, more repetition | |
| Every sweep | Predictable cadence, steady noise | |

**User's choice:** Only on change (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Silent when stable | One line only when the cap changed this batch (learned / floor / probe-up) | ✓ |
| One line per capped relay per batch | At-a-glance cap visibility, dozens of identical lines | |

**User's choice:** Silent when stable (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Plain text, demote JSON to debug | Human-readable state-change lines; RELAY_ERROR JSON blobs become debug-only | ✓ |
| Plain text, keep JSON as-is | Duplicate-line problem partially survives | |
| All JSON | Machine-parseable but a bigger rewrite | |

**User's choice:** Plain text, demote JSON to debug (Recommended)

---

## Claude's Discretion

- Exact log line wording (required fields fixed: class, count, threshold, next retry)
- Success-streak counter placement; per-class counter representation and atomicity
- Probe-in-progress flagging mechanism for the ejection exemption
- `RemoveRelayURL` extension vs new `EjectRelayURL`
- Forward-relay ejection exemption (confirm during planning)
- Test layout for state-machine logic

## Deferred Ideas

- Relay state persistence across restarts (counters + caps) — decided against; revisit only if restart re-learning proves costly
- `ejected_relays` metadata in YAML — with DISC-01
- Structured JSON relay-event logging — with OBS-01
- discover-relays respecting `ejected_relays` — with DISC-01

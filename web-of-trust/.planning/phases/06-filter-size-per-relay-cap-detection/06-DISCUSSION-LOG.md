# Phase 6: Filter Size & Per-Relay Cap Detection - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-11
**Phase:** 06-filter-size-per-relay-cap-detection
**Areas discussed:** batchSize exposure, Cap storage & persistence, Small-cap relay query strategy, Connection-drop-on-REQ attribution

---

## batchSize exposure

| Option | Description | Selected |
|--------|-------------|----------|
| Config field | Add relay_filter_batch_size to YAML config, default 100. Low cost with Viper. | ✓ |
| Hardcoded constant | Keep const batchSize = 100 in main.go. YAGNI. | |

**User's choice:** Config field

**Follow-up — config key name:**

| Option | Description | Selected |
|--------|-------------|----------|
| batch_size: 100 | Top-level, matches snake_case convention | |
| relay_filter_batch_size: 100 | Explicit name, avoids ambiguity with other batch sizes | ✓ |

**Follow-up — dual purpose:**

| Option | Description | Selected |
|--------|-------------|----------|
| relay_filter_batch_size is both default batch and initial cap | Single value governs outbound batch size and relay cap starting point | ✓ |
| Separate max_relay_filter_authors constant | Separate knob for initial per-relay cap ceiling | |

---

## Cap storage & persistence

| Option | Description | Selected |
|--------|-------------|----------|
| In-memory in relayState | filterCap int in struct. Fast, simple, no config mutation. Rediscovered on restart. | ✓ |
| Persisted to web-of-trust.yaml | Survives restart. Adds config churn on every cap discovery. | |

**Follow-up — cap update value on NOTICE:**

| Option | Description | Selected |
|--------|-------------|----------|
| Half the current cap | current cap / 2. Simple, deterministic. | ✓ |
| Parse limit from NOTICE text | Precise but fragile — NOTICE formats not standardised. | |
| Fixed fallback: 10 | Conservative floor. Overshoots on most relays. | |

**Follow-up — minimum floor:**

| Option | Description | Selected |
|--------|-------------|----------|
| Floor at 10 authors | Halving stops at 10; below that, mark relay dead. | ✓ |
| Floor at 1 (never give up) | Halve to 1. Maximum coverage, degenerate queries for pathological relays. | |
| No floor | Let Phase 7 relay health handle ejection. | |

---

## Small-cap relay query strategy

| Option | Description | Selected |
|--------|-------------|----------|
| Chunked sub-REQs to that relay | Split authors into chunks of filterCap; send each as a sequential Subscribe. Full coverage. | ✓ |
| Cap all relays at smallest detected cap | Penalizes well-behaved relays for one weak relay. | |
| Skip relay for this batch | Coverage gap, zero complexity. | |

**Follow-up — concurrency:**

| Option | Description | Selected |
|--------|-------------|----------|
| Sequential within queryRelay | Loop over chunks in queryRelay. Simple, no extra goroutine complexity. | ✓ |
| Parallel goroutines per chunk | Faster for large lists but complicates cancellation and channel sizing. | |

---

## Connection-drop-on-REQ attribution

| Option | Description | Selected |
|--------|-------------|----------|
| Temporal heuristic: drop within N ms of Subscribe | Drop ≤500ms after Subscribe = filter rejection → halve cap + retry. Longer drops = transport failure → mark dead. | ✓ |
| No attribution — only NOTICE classifies caps | Connection drops always mark relay dead. Misses relays that silently drop without NOTICE. | |

**Follow-up — action after filter-rejection drop:**

| Option | Description | Selected |
|--------|-------------|----------|
| Halve cap and retry same authors at smaller cap | Immediate retry within queryRelay. Relay stays alive with new cap. | ✓ |
| Halve cap but don't retry — next batch gets smaller cap | Coverage gap for current batch, less complexity. | |

**Follow-up — attribution threshold:**

| Option | Description | Selected |
|--------|-------------|----------|
| 500ms | Conservative. Catches relays that send TCP RST or WebSocket close immediately on REQ. | ✓ |
| 200ms | Tighter. May miss relays that take a moment to parse REQ before rejecting. | |
| You decide | Leave threshold to Claude's discretion. | |

---

## Claude's Discretion

- Exact name/signature of NOTICE-matching helper (inline vs. extracted function)
- Timer mechanism for 500ms attribution window (time.Since vs. separate goroutine)
- Whether chunking logic is extracted into a helper or inlined in queryRelay
- Test file layout (shared file vs. split by requirement)

## Deferred Ideas

- **Cap persistence across restarts:** Save per-relay caps to web-of-trust.yaml. Deferred — in-memory rediscovery acceptable; Phase 7 may revisit config mutation patterns.
- **Parse exact limit from NOTICE:** Some relays embed their max filter size in NOTICE text. Deferred — not standardised; halving is sufficient.
- **Bloom filter per relay (DISC-02):** Skip querying relay X for pubkey Y if it has never returned events for similar pubkeys. Future milestone.

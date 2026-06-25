# Phase 2: GraphQL Client + Author Enumeration - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-25
**Phase:** 2-GraphQL Client + Author Enumeration
**Areas discussed:** Resume semantics, Phase-2 output, Resilience policy, Client & entry-point

---

## Resume semantics

| Option | Description | Selected |
|--------|-------------|----------|
| Auto latest unfinished | Resume the most recent run whose status isn't 'done', continuing from its stored last_cursor; start fresh if none. | ✓ |
| Explicit --run-id | User must pass --run-id N; fully deterministic but more typing. | |
| Single 'current run' pointer | Maintain one canonical in-progress run; needs extra bookkeeping. | |

**User's choice:** Auto latest unfinished
**Notes:** Leverages existing Phase-1 run schema (status + last_cursor); no new state.

---

## Phase-2 output

| Option | Description | Selected |
|--------|-------------|----------|
| Persist to pubkey table | INSERT OR IGNORE each pubkey as it walks; concrete artifact, satisfies FK needs. | |
| Count / no-op only | Prove pagination + resume state, report count; leaves no pubkey rows. | |
| Both: persist + report | Persist to pubkey table AND emit progress/count to stdout. | ✓ |

**User's choice:** Both: persist + report
**Notes:** Durable artifact plus observability; pubkey table must be populated eventually (score/signal FK to it).

---

## Resilience policy

| Option | Description | Selected |
|--------|-------------|----------|
| Exp backoff, then abort | Exponential backoff + jitter, bounded retries, then abort run (resumable). | |
| Fixed interval, infinite | Retry forever at fixed interval; can hang indefinitely. | |
| Fail fast | Few quick retries, then abort. Surfaces problems immediately. | ✓ |

**User's choice:** Fail fast
**Notes:** Small retry ceiling then abort with cursor preserved → --resume continues. Failure never advances cursor. Snapshot drift is record-and-continue (locked by criterion 4).

---

## Client & entry-point

| Option | Description | Selected |
|--------|-------------|----------|
| Hand-written + reusable module | Hand-written queries + serde structs; reusable graphql module Phase 3 extends; minimal binary now. | ✓ |
| Introspection codegen | Generate typed client from introspection; heavier toolchain for one query. | |
| Full clap CLI now | Stand up complete clap structure now; risks rework vs Phase 5. | |

**User's choice:** Hand-written + reusable module
**Notes:** Client designed so Phase 3 adds latestPerAuthor additively; full CLI deferred to Phase 5.

---

## Claude's Discretion

- Exact retry ceiling count and backoff base/cap (fail-fast spirit).
- `authors` page `limit` value (ceiling 500).
- HTTP client/runtime specifics and how the endpoint URL is supplied this phase.
- Internal module naming/layout within the reusable-client constraint.

## Deferred Ideas

- Live enumeration streaming directly into the fetch pipeline — Phase 3.
- Config-file-driven endpoint/parameter configuration (OPS-03) — Phase 4.
- Full `run`/`export` CLI — Phase 5.
- Direct `heed` LMDB reads to bypass the GraphQL hop (PERF-01) — v2, profiling-gated.
</content>

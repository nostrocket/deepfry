---
phase: 01-foundation-stats-dashboard
plan: 02
subsystem: graphql-explorer-frontend
tags: [transport, error-classifier, readiness-gate, cursor-pagination, vitest, walking-skeleton]
requires:
  - "GRAPHQL_URL / READY_URL / HEALTH_URL (src/transport/config.ts) — base-URL source (01-01)"
  - "StatsDocument + urql client (01-01)"
provides:
  - "classify(result) -> ApiError | null (src/transport/errors.ts) — single error boundary; 7-kind discriminated union"
  - "ApiError discriminated union (INVALID_CURSOR, TOO_MANY_AUTHORS, VALIDATION, INTERNAL, NOT_READY, PAYLOAD_TOO_LARGE, NETWORK)"
  - "waitForReady(signal?) (src/transport/readiness.ts) — /ready poll with 500ms->5000ms bounded backoff"
  - "accumulatePages(fetchPage, limit) (src/transport/paginate.ts) — opaque-cursor accumulation SCAFFOLD"
  - "npm test (vitest run) + vitest.config.ts"
affects:
  - "Phase 2 (events cursor pagination) first EXERCISES accumulatePages + classify"
  - "Phase 4 (authors enumeration, BATCH-04) reuses accumulatePages"
  - "Plan 01-03 wires classify() into the full StatsDashboard state treatments"
tech-stack:
  added:
    - "vitest@3.2.6 (devDependency) — Node-environment unit tests for transport logic"
  patterns:
    - "Single error-classifier boundary: branch on result.error / extensions.code before reading result.data (Pitfall 1)"
    - "Readiness gate: poll /ready, bounded exponential backoff (500ms->5000ms cap), distinct connecting state (Pitfall 8)"
    - "Opaque cursors: pass endCursor back verbatim, never parse/construct; INVALID_CURSOR restarts page 1"
    - "Explicit-limit-on-every-query convention recorded for Phases 2-4 (server clamps to [1,500])"
key-files:
  created:
    - src/transport/errors.ts
    - src/transport/errors.test.ts
    - src/transport/readiness.ts
    - src/transport/paginate.ts
    - vitest.config.ts
  modified:
    - src/App.tsx
    - package.json
    - package-lock.json
decisions:
  - "A2 RESOLVED: @urql/core@6.0.3 attaches the HTTP Response on result.error.response (sibling of networkError), NOT result.error.networkError.response — verified in makeErrorResult (urql-core-chunk.js). Classifier reads result.error.response.status; the RESEARCH example's networkError.response path was wrong for v6."
  - "vitest pinned at ^3.2.6 (current major) with environment: 'node' — transport logic is pure, no DOM/network in tests."
  - "INTERNAL kind carries NO message field by design (T-01-04); VALIDATION carries the server message verbatim (contract §7 says validation messages are user-safe)."
metrics:
  duration: ~10m
  completed: 2026-06-24
  tasks: 2
  files: 7
status: complete
---

# Phase 1 Plan 02: Transport Robustness Summary

Hardened the transport layer the walking skeleton proved: added the single `classify()` error boundary (errors-on-200 + `extensions.code` → a 7-kind discriminated union), the `/ready` readiness gate with bounded 503 backoff and a distinct info-blue "connecting to relay…" state, and scaffolded the opaque-cursor `accumulatePages` accumulator that Phases 2/4 will exercise — so the contract's failure semantics now live in exactly one place and a warming backend never reads as broken (FND-03).

## What Was Built

- **Task 1 — Error-classifier boundary + vitest** (`9f3a43e`): `src/transport/errors.ts` exports `ApiError` (exactly 7 kinds: `INVALID_CURSOR`, `TOO_MANY_AUTHORS`, `VALIDATION{message}`, `INTERNAL`, `NOT_READY`, `PAYLOAD_TOO_LARGE`, `NETWORK`) and `classify(result): ApiError | null`. It inspects the HTTP transport status (503/413) AND `graphQLErrors[0].extensions.code` BEFORE any caller reads `data`; returns `null` only on a clean result. A code-less `/internal error/i` message maps to a message-less `INTERNAL` (raw server string dropped, T-01-04); any other code-less GraphQL error → `VALIDATION` carrying its (user-safe) message. Added `vitest@3.2.6` devDependency, `vitest.config.ts` (Node environment), and `"test": "vitest run"`. `src/transport/errors.test.ts` has 10 cases — every kind + the clean-`null` case + an explicit assertion that an `INTERNAL` result does not carry the raw `lmdb … data.mdb` server string + transport-status-wins-over-graphQLErrors precedence.
- **Task 2 — Readiness gate + connecting state + cursor scaffold** (`f70ea29`): `src/transport/readiness.ts` exports `waitForReady(signal?)` — loops `fetch(READY_URL)` (imported from `config.ts`), returns on HTTP 200, otherwise backs off 500ms doubling to a **5000ms cap** (T-01-05); catches fetch rejection (backend not up yet) and keeps retrying; honors `AbortSignal` for clean unmount. `src/App.tsx` now awaits `waitForReady` in an effect (with an `AbortController` for StrictMode/unmount) and renders a distinct **info-blue** "Connecting to relay…" state (UI-SPEC copy verbatim) — a dot **and** heading **and** body carry the meaning so color is never the sole signal (`role="status"`, `aria-live="polite"`); the slice-1 raw stats render is preserved as the post-ready content (01-03 replaces it with `StatsDashboard`). `src/transport/paginate.ts` is a scaffold: `accumulatePages(fetchPage, limit)` loops pages, pushes `endCursor` back verbatim as the next `after`, stops on `!hasMore || endCursor === null`, treats cursors as opaque, and documents both the `INVALID_CURSOR` → restart-from-page-1 recovery and the explicit-`limit`-on-every-query convention — not wired to any live query this phase.

## Verification Results

- `npm test` → `errors.test.ts` 10/10 green (all 7 kinds + `null` + INTERNAL-no-leak + precedence).
- `npm run build` (`tsc -b && vite build`) exits 0 (42 modules, emits `dist/`).
- `grep` gates: `READY_URL` in `readiness.ts`, `waitForReady` in `App.tsx`, `paginate.ts` exists and contains `limit`, backoff cap `5000` present — all OK.
- Prohibition checks: no inline base URL / `192.168` / `127.0.0.1:8080` in the new transport/App code; no `credentials`; no Vite `server.proxy` (only negative-confirmation comments).
- **Live readiness proof:** derived the `/ready` URL from the configured base (`http://192.168.149.21:8080/ready` via gitignored `.env`) and `curl` returned `200` — the gate resolves correctly against the warm lens and would show the connecting state while it returns 503 / is down. (No hardcoded LAN address in committed source; default stays loopback.)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected the urql HTTP-status access path (A2 resolution)**
- **Found during:** Task 1
- **Issue:** The RESEARCH "Error classifier" code example reads the HTTP status at `result.error.networkError.response?.status`. On the installed `@urql/core@6.0.3` that path is `undefined` — `makeErrorResult` (`node_modules/@urql/core/dist/urql-core-chunk.js`) constructs the `CombinedError` with `networkError = new Error(response.statusText)` and attaches the raw Fetch `Response` on the error's **sibling** `response` property. Reading the wrong path would silently misclassify every 503/413 as `NETWORK`, defeating the readiness gate.
- **Fix:** `classify()` reads the status from `result.error.response.status` (defensively, since `CombinedError.response` is typed `any`), and the test fixtures mirror urql's real shape. Documented as Assumption A2 RESOLVED in `errors.ts`.
- **Files modified:** src/transport/errors.ts, src/transport/errors.test.ts
- **Commit:** 9f3a43e

### Planned install (not a deviation)

`vitest` was installed because Task 1 explicitly instructs "Add vitest as a devDependency" — a plan-authored, canonical first-party package (not a discovered/substituted install), so within task scope. Pinned `^3.2.6`.

## Threat Surface

No new surface beyond the plan's `<threat_model>`. Mitigations applied:
- **T-01-04** (Information Disclosure): `INTERNAL` is message-less; a unit test asserts the raw server `lmdb … data.mdb` string never appears in the classified value. Validation messages (user-safe per contract §7) are shown verbatim.
- **T-01-05** (self-inflicted DoS): readiness backoff is bounded (500ms → 5000ms cap), never unbounded; `/ready` is polled at seconds-scale.
- **T-01-06** (Tampering): `paginate.ts` treats `endCursor` as opaque — never parsed or constructed; `INVALID_CURSOR` restart-from-page-1 documented. Scaffold only this phase.

## Known Stubs

`src/transport/paginate.ts` is an intentional scaffold per the plan — `accumulatePages` is not wired to any live query this phase. It is first EXERCISED in Phase 2 (events pagination) and reused by the Phase 4 `authors` enumeration (BATCH-04). This is the plan's explicit scope, not an unintended stub. No stubs prevent the plan's goal.

## Self-Check: PASSED

- FOUND: src/transport/errors.ts, src/transport/errors.test.ts, src/transport/readiness.ts, src/transport/paginate.ts, vitest.config.ts
- FOUND commits: 9f3a43e, f70ea29

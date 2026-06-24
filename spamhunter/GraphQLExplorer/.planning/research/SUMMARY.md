# Project Research Summary

**Project:** GraphQL Explorer (Spam Investigation)
**Domain:** Read-only, author-centric spam-investigation SPA over the LMDB2GraphQL read-only GraphQL lens (Nostr corpus)
**Researched:** 2026-06-24
**Confidence:** HIGH

## Executive Summary

This is a single-analyst forensic UI, not a moderation queue and not a relay. The whole job is: **paste a suspect pubkey (or import up to 1000), then judge "spammer or not?" fast and defensibly.** It is read-only — it reads a strfry corpus through one GraphQL endpoint (`POST /graphql`) and renders investigation views; it never writes events. Experts build this kind of tool as a **layered SPA with a pure functional analytics core**: a thin transport layer fetches a bounded *event window* into memory, pure analyzer functions consume that window and emit signal results, and view components render those results. The analyzers never touch the network or React — that boundary is the project's load-bearing structural decision and the thing that makes the spam-signal logic unit-testable against fixtures with zero network.

The recommended approach is the pre-chosen stack — **React 19 + Vite + TypeScript + urql + GraphQL Codegen + nostr-tools** — which research confirms as the correct 2026 choice with no need to relitigate. urql's default document cache is exactly right for a read-only, author-keyed, polled app (no normalized cache needed). The build order falls directly out of a dependency graph: transport/error-handling foundation first (everything depends on it), then input/identity normalization, then the single-author drill-down with its four spam signals (timeline/burst, near-duplicate content, tag/mention aggregation, kind distribution), then batch triage and the polled stats dashboard. The four signals can be built as pure functions in parallel with the transport layer because they have zero transport dependency — that parallelization is the practical payoff of the pure core.

The key risks are mostly **silent failure modes baked into the backend contract**, not exotic engineering problems. Three stand out and must be mitigated structurally rather than bolted on later: (1) the **"happy 200" trap** — GraphQL errors arrive on HTTP 200 with `errors[]`, so the transport must branch on error before reading data; (2) the **window-honesty trap** — every analytic is computed only over fetched events, so a "0 duplicates over 50 events" screen reads as false exoneration unless a non-removable window-size denominator ships *with the first signal*; and (3) **author-claimed `createdAt`** poisoning burst detection — timestamps are forgeable, so burst-present is suspicious but burst-absent is never exoneration, and the spam score must never treat "low burst" as a negative (clearing) contribution. Two version/config traps round out the critical set: pin **`graphql@16` not 17** (codegen toolchain caps at `^16`), and use the **Vite dev proxy with a relative `/graphql` URL** because the backend sends no CORS headers.

## Key Findings

### Recommended Stack

The pre-chosen stack is confirmed. The refinements that matter are about *version pinning* and *one real compatibility trap*, not swapping libraries. urql's `urql` package is the React binding; it is graphql-version-agnostic (bundles `@0no-co/graphql.web`), so the graphql-16-vs-17 issue affects only the codegen build toolchain, not runtime. Use the modern `client-preset` (typed `graphql()` document + `TypedDocumentNode`), not the legacy `typescript-urql` hooks plugin. Charts and near-duplicate detection are both hand-rolled (CSS/SVG bars; client-side normalize + Jaccard) — no library needed at window scale (N ≤ 500). See `STACK.md`.

**Core technologies:**
- **React 19 + Vite + TypeScript 5.9** — plain SPA, no SSR/RSC; Vite owns the dev-proxy CORS fix. Prefer TS 5.9 (not 6.0) and Vite 7+plugin-react 5 for stability (Vite 8 acceptable, low blast radius).
- **urql 5 + @urql/core 6** — lightweight read-only client; default document cache, no normalized cache. Pin `@urql/core@6` explicitly.
- **graphql@16.14.2 (NOT 17)** — codegen `client-preset@6` and `graphql-request@7` cap at `^16`; `graphql@17` (2026-06-15) breaks the typed-client toolchain. Highest-value version trap.
- **@graphql-codegen/cli@7 + client-preset@6** — typed client from live introspection via the dev proxy (or directly in Node).
- **nostr-tools@2 (nip19 only)** — npub/note bech32 ↔ hex. No signing/relay/verify modules (sigs already verified by strfry; do not re-verify).

### Expected Features

This is an author-centric forensic tool: no content search, no firehose, no push — entry is always a *known* pubkey. Two workhorse queries drive everything: `events(filter, after, limit)` (single-author drill-down, cursor-paginated, limit clamped `[1,500]`) and `latestPerAuthor(kind, perAuthor, authors)` (batch triage, authors ≤1000, cost ≈ authors×perAuthor), plus `stats`. The single most important framing: **every analytic is "over the fetched window," and the window size must be visible.** See `FEATURES.md`.

**Must have (table stakes):**
- npub/hex input + normalization — humans paste npub; API speaks hex only.
- Single-author drill-down shell over one fetched event set (4 signal panels share it).
- Event timeline (newest-first) + **window-size indicator** — the honesty backbone.
- The four spam signals: posting-rate/burst, near-duplicate content, tag/mention (p/e/t) aggregation, kind histogram + raw-JSON inspector.
- Corpus stats dashboard, polled (`maxLevId` change probe).
- Robust API access: readiness gate, `errors[]` handling, cursor pagination, clamp awareness.
- Local-dev Vite proxy to `127.0.0.1:8080`.

**Should have (competitive — differentiate from "just use GraphiQL"):**
- Per-author transparent spam-score rollup (sortable, never a black box).
- Batch import (≤1000, chunked) + triage table.
- Near-dup *clustering*, burst sparkline, mention fan-out view.
- Evidence export / deep-link author URLs (`/author/:hex`).

**Defer (v2+):**
- Cross-author (ring) duplicate detection — heaviest client compute; needs batch + dedup mature.
- Tunable-threshold settings panel — ship sensible defaults first.
- "Events referencing this id" secondary lookup — tangential to spam judgment.

**Deliberately NOT built (anti-features):** global firehose, content search, realtime feed, auto-classify/ML verdict, writing NIP-56 reports/bans, signature re-verification, multi-relay aggregation, "fetch everything" triage, multi-user state.

### Architecture Approach

A layered SPA enforced by physical module separation: `analyzers/` (pure TS, zero deps on react/urql/fetch — an analyzer importing them is a code-review red flag), `transport/` (all HTTP/`errors[]`/cursor/readiness weirdness), `hooks/` (the only seam where transport, analyzers, and React meet), `identifier/` (pure nip19↔hex at the edge), and `views/` (dumb projections of `SignalResult`). The window flows one-directionally: hooks own fetching and accumulation, analyzers receive `Event[]` + `WindowMeta` and report over exactly that, views render. urql's default document cache holds page responses; the *accumulated window* is deliberately hook-local React state (opaque cursors make cross-page cache merging fragile and pointless). See `ARCHITECTURE.md`.

**Major components:**
1. **Transport (urql client)** — single same-origin `/graphql` client; owns the error classifier (`extensions.code`), `/ready` gating, opaque-cursor accumulation loop.
2. **Analyzer core (pure functions)** — the four signals + transparent spam-score rollup; the unit-testable heart, buildable in parallel with transport.
3. **Query hooks** — `useAuthorWindow` (paginate+accumulate+windowMeta), `useLatestPerAuthor` (chunk, merge by `author`), `useStatsPoll` (interval, maxLevId diff), `useAnalyzers` (memoized derive).
4. **Identifier module** — pure npub/note ↔ 64-char lowercase hex normalization, used by both entry paths.
5. **Signal views + shells** — drill-down shell (window indicator + 4 panels + raw inspector), batch triage table, stats dashboard.

### Critical Pitfalls

1. **GraphQL errors on HTTP 200 ("happy 200" trap)** — `200 OK` ships with `errors[]` and possibly `data: null`. Use urql, always branch on `result.error` before reading `result.data`, centralize a typed `extensions.code` discriminator (`INVALID_CURSOR`/`TOO_MANY_AUTHORS`/validation/internal). Phase A.
2. **Window-honesty trap** — analytics over a small fetched window read as global truth → false "clean" verdicts (the most dangerous failure for a forensic tool). Window-size indicator (`N events · hasMore · time range`) must be a non-removable element shipping *with the first signal*. Never show "0 duplicates"; show "0 duplicates in 50 fetched (hasMore: true)." Phase C.
3. **Author-claimed `createdAt` poisons burst detection** — timestamps are forgeable. Burst present = suspicious; burst absent ≠ clean. Persistent provenance caveat beside every rate chart; spam score must never let "low burst" subtract. Phase C.
4. **graphql@17 vs @16 / CORS without the proxy** — pin `graphql@16.14.2` exactly (Phase 0); client `url` must be relative `/graphql` proxied by Vite (no absolute URL, ever), `changeOrigin: true` on `/graphql`+`/ready`+`/health` (Phase 0/A).
5. **Silent limit clamp + 256 KiB body cap + opaque cursors** — `limit`/`perAuthor` silently clamp to 500 (drive completeness via pagination + `hasMore`, never a big limit); chunk batch on *both* ≤1000 authors AND <256 KiB body (`413` is a transport status); treat `endCursor` as opaque (never parse/build), reset on filter change, recover from `INVALID_CURSOR`. Match `latestPerAuthor` results by `author` key (empty groups omitted — never zip by index). Phase A/D.

Also notable: 503 readiness gating (distinct "connecting" state, not "error"), lazy-fetch `raw` (don't select it in list queries — payload bloat + complexity-2000 pressure), 64-bit `kind`/`createdAt` bounds-check (cheap guard for an adversarial tool), and XSS — render `content`/`raw`/tags as escaped plaintext, never HTML/markdown (this is literally a hostile spam corpus).

## Implications for Roadmap

Based on combined research, the suggested phase structure (derived from the FEATURES.md dependency graph and the ARCHITECTURE.md layering — both independently arrived at the same ordering):

### Phase 0: Scaffold + Dependency Pins + Dev Proxy
**Rationale:** Version traps and the CORS proxy are foundational invariants that everything else assumes; cheapest to get right at creation.
**Delivers:** Vite + React 19 + TS project; exact-pinned `graphql@16.14.2`; codegen wiring; Vite dev proxy (`/graphql`,`/ready`,`/health`, `changeOrigin`); urql client with relative `/graphql` URL; a same-origin `stats` query returning real data through the proxy.
**Addresses:** Local-dev Vite proxy requirement.
**Avoids:** Pitfall 4 (graphql@17 pin, CORS/absolute-URL), notes Pitfall 12 (64-bit `number` decision + bigint escape hatch).

### Phase A: Transport / API Foundation
**Rationale:** Every query depends on it; the FEATURES dependency graph makes error/readiness handling the root. Build before any signal.
**Delivers:** error classifier (`errors[]`-on-200 + `extensions.code`), `/ready` gating with 503-as-retry backoff, opaque-cursor accumulation loop, `413`/clamp awareness, codegen-typed `stats`.
**Uses:** urql `cacheExchange`+`fetchExchange`, codegen `client-preset` `TypedDocumentNode`.
**Avoids:** Pitfalls 1 (happy-200), 6 (cursors), 7 (clamp/413), 8 (503).

### Phase B: Input & Identity
**Rationale:** Required by *both* entry paths (single + batch) before any query runs; pure, no UI.
**Delivers:** single `toHexPubkey(input)` normalizer (trim, decode npub/note/nprofile, lowercase + length-check), batch-list parser; UI distinction between "couldn't parse" and "zero matches."
**Implements:** the pure `identifier/` module.
**Avoids:** Pitfall 5 (silent non-match from bad normalization → false "clean").

### Phase C: Single-Author Drill-Down + Four Signals + Window Indicator
**Rationale:** The core value ("paste a pubkey → judge it"). The analyzer core (pure functions, all four signals + types) can be built/tested in parallel with Phases A/B since it has zero transport dependency.
**Delivers:** `useAuthorWindow` (paginate, accumulate, windowMeta, loadMore); drill-down shell; the four signal panels (timeline+burst, content+near-dup, tag fan-out, kind histogram) + lazy raw inspector; the non-removable window-size indicator; transparent spam-score rollup (sequenced after signals).
**Addresses:** all four drill-down requirements + the honesty backbone.
**Avoids:** Pitfalls 2 (window honesty — ship indicator with first signal), 3 (author-claimed timestamps — asymmetric burst scoring + caveat), 9 (lazy `raw`, complexity), 12 (integer bounds-check), plus XSS (plaintext rendering).

### Phase D: Batch Import + Triage
**Rationale:** Analysts work suspect *lists*; depends on identifier (B), spam-score rollup (C), transport chunking (A). The P2 expansion after the single-author slice validates.
**Delivers:** batch import (paste/file, ≤1000, dual-axis chunking on authors AND body size), `useLatestPerAuthor` (merge by `author`), sortable triage table → drill-in.
**Avoids:** Pitfalls 7 (413/TOO_MANY_AUTHORS), 10 (cost blowup — small `perAuthor` 3–10, match by author not index).

### Phase E: Stats Dashboard + Polling
**Rationale:** Independent; can land anytime after Phase A. Cheapest possible change detection given no-push reality.
**Delivers:** `useStatsPoll` (interval seconds-not-ms, pause on hidden tab), `maxLevId`-diff "new data available" nudge that does NOT auto-refetch the analyst's window.
**Avoids:** Pitfall (aggressive polling), Anti-pattern (auto-refetch moving ground mid-investigation).

### Phase F: Differentiators (post-validation)
**Rationale:** Each extends an existing analyzer/view; build once the core proves out.
**Delivers:** near-dup clustering, burst sparkline, mention fan-out, evidence export, deep-link URLs; defer cross-author ring dedup and tunable-threshold panel to v2.

### Phase Ordering Rationale

- **Dependency-driven:** error/readiness handling is the root of the FEATURES dependency graph — nothing queries without it, so transport precedes every feature.
- **Parallelization win:** the pure `identifier/` (B) and `analyzers/` (C core) modules have zero transport dependency and can be built/tested in parallel with Phases A/B — the practical payoff of the pure-core architecture.
- **Honesty is structural, not late:** the window-size indicator must ship in the same phase as the first signal (C), never retrofitted — retrofitting it is a MEDIUM-cost rework that risks verdicts formed on misleading screens.
- **Vertical slice first:** Phases 0→A→B→C deliver the full core value (single author → verdict) before batch (D) broadens it.

### Research Flags

Phases likely needing deeper research during planning (`/gsd-plan-phase --research-phase <N>`):
- **Phase C (window honesty + burst provenance UX):** the honesty framing is the project's core integrity property and the timestamp-poisoning interpretation is MEDIUM-confidence (heuristic). Flag for a UX/heuristic review — how to present asymmetric scoring and forgeable-timestamp caveats so analysts internalize them.
- **Phase C/F (near-dup heuristic thresholds):** Jaccard ≥0.8, shingle size (char-4 / word-3), burst thresholds (>30/min) are sane defaults but data-dependent; flag for tune-to-data validation.

Phases with standard patterns (skip research-phase):
- **Phase 0 / A:** well-documented — STACK.md and the code-verified contract.md give exact recipes (proxy config, codegen shape, error codes). Mechanical.
- **Phase B:** nip19 conversion is a small, well-specified pure module.
- **Phase E:** poll-and-diff is a trivial, fully-specified pattern.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Versions verified live against npm registry + peerDependency cross-check; the one trap (graphql@16 vs 17) is concrete and code-grounded. |
| Features | HIGH (mapping) / MEDIUM (thresholds) | Feature-to-API mapping is code-verified against contract.md v1.0; heuristic thresholds corroborated across multiple sources but tune-to-data. |
| Architecture | HIGH | Component boundaries derive directly from the code-verified contract + confirmed stack + dependency-mapped features. |
| Pitfalls | HIGH (contract) / MEDIUM (interpretive) | Every pitfall is a deterministic property of the contract; MEDIUM only on window-honesty/timestamp-poisoning interpretation. |

**Overall confidence:** HIGH

### Gaps to Address

- **Heuristic threshold tuning (MEDIUM):** Jaccard threshold, shingle size, burst/rate cutoffs, regularity (CV) bounds are starting defaults, not validated against real spam data. Handle: ship sensible defaults, surface them in the UI, defer a tunable-settings panel to v2, validate against the live corpus during/after Phase C.
- **Burst-provenance UX (MEDIUM):** how to present forgeable-timestamp caveats and asymmetric scoring so analysts don't read "no burst" as clean. Handle: dedicated UX research flag on Phase C; persistent (not buried) caveats.
- **64-bit precision (LOW likelihood, cheap guard):** `kind`/`createdAt` are 64-bit-on-wire typed as `Int` → `number`. Fine for honest data; an adversarial value near 2^53 silently loses precision. Handle: bounds-check on ingest in Phase C (out-of-range is itself a spam tell); keep codegen `config.scalars` bigint mapping as a documented escape hatch.
- **Vite 7 vs 8 (LOW):** both acceptable; lean Vite 7 + plugin-react 5 for daily-driver stability. Decide at Phase 0; the only Vite feature this project leans on (dev proxy) is stable across both.

## Sources

### Primary (HIGH confidence)
- `contract.md` v1.0 (code-verified 2026-06-23) — authoritative interface: endpoints, schema, opaque cursors, silent `[1,500]` clamp, `errors[]`-on-200 + `extensions.code`, 503/`/ready`, 256 KiB/`413`, `TOO_MANY_AUTHORS`, empty-group omission, fixed ordering, author-claimed `createdAt`, 64-bit `kind`/`createdAt`, hex-only/nip19, depth-12/complexity-2000.
- `.planning/PROJECT.md` — scope, read-only constraint, local-dev-first, key decisions, out-of-scope.
- npm registry (live 2026-06-24) — verified current versions + peerDependencies (react 19.2.7, urql 5.0.3, @urql/core 6.0.3 graphql-independent, graphql 16.14.2 vs 17.0.1, codegen cli 7.1.3 / client-preset 6.0.1 capped at graphql ^16, nostr-tools 2.23.8, vite/plugin-react pairings).
- the-guild.dev/graphql/codegen — `client-preset` / `codegen.ts` shape, live-URL `schema` source.
- github.com/vitejs/vite-plugin-react README — React Compiler/babel peers optional; Fast Refresh needs react >=16.9.

### Secondary (MEDIUM confidence)
- Near-dup / Jaccard / shingling literature (Brenndoerfer MinHash/LSH; Kashnitsky practical near-dup — char-4 / 0.8 threshold; apxml >=0.8 near-dup) — heuristic defaults.
- Content-moderation / behavioral-pattern feature surveys (eugeneyan, getstream) — feature landscape framing.

### Tertiary (LOW confidence)
- Nostr spam-detection projects (blakejakopovic NaiveBayes/labeled dataset; KiPSOFT NIP-56) — confirm signal categories exist; details in notebooks. Used only to validate that the four chosen signals are the right ones, not for thresholds.

---
*Research completed: 2026-06-24*
*Ready for roadmap: yes*

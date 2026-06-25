# Spamhunter — Pubkey Spam Classifier

## What This Is

A fast, re-runnable Rust batch engine that iterates over **every pubkey** in the strfry corpus (via the read-only LMDB2GraphQL adapter), fetches each pubkey's recent events, and runs them through a stack of **tunable, weighted detection layers** to produce a per-pubkey spam score. Its output is a list of pubkeys **suspected of being spammers** — judged at the *pubkey* level, not the individual-event level — stored in SQLite for querying, tuning, and human review.

It complements the existing graph-only `spam-explorer` (which scores the follow graph structurally). This engine is the **content & behavioral** half: it looks at what pubkeys actually publish.

## Core Value

Produce an accurate, low-false-positive list of suspected spammer pubkeys **as fast as possible**, with every layer independently tunable and the whole system correctable from human-labeled false positives.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Enumerate all distinct pubkeys in the corpus via the LMDB2GraphQL `authors` query (cursor pagination)
- [ ] Fetch each pubkey's most-recent events (target: last 100) via LMDB2GraphQL
- [ ] Whitelist layer: query the whitelist (Dgraph `Profile` / whitelist-plugin) — **absence is a spam signal**, presence clears only this layer
- [ ] Multiple independent content/behavioral detection layers, each emitting a sub-score
- [ ] Weighted aggregation of layer sub-scores into a per-pubkey spam score
- [ ] Each layer independently tunable (weights + thresholds) to reduce false positives
- [ ] Persist per-pubkey scores, per-layer signals, and the suspected-spammer list to SQLite
- [ ] Human-labeled false-positive feedback captured in SQLite
- [ ] Re-optimize layer weights from the labeled set (logistic-regression-style "backpropagation")
- [ ] Re-runnable idempotent batch over the whole corpus

### Out of Scope

- **Local LLM / on-device model inference** — explicitly forbidden by the user: too slow for the speed goal.
- **Event-level spam verdicts as the deliverable** — detection aggregates to the *pubkey* level; per-event signals exist only as inputs.
- **Live enforcement / event rejection** — the deliverable is a *list*, not a relay gate. Enforcement (feeding whitelist/quarantine) is a possible later milestone, not v1.
- **Re-implementing structural graph spam detection** — that is `spam-explorer`'s job; this engine consumes content, not the follow graph.
- **Writing to strfry / mutating the corpus** — LMDB2GraphQL is read-only by design; this engine only reads.
- **Continuous/incremental service** — v1 is a re-runnable batch; an incremental service is a later milestone.

## Context

**Ecosystem.** DeepFry is a modular backend around a stock strfry Nostr relay (LMDB, port 7777). Canonical events live only in strfry's LMDB; Dgraph holds ID-only pubkey graphs. This engine reads events through **LMDB2GraphQL** (`contract.md` in this dir): a read-only GraphQL lens over strfry's live LMDB. Relevant queries:
- `authors(after, limit)` → distinct pubkeys, byte-ascending, O(distinct authors), cursor-paginated. Used to enumerate the corpus.
- `latestPerAuthor(kind, perAuthor, authors[≤1000])` → newest-N events per author for a single kind; batches up to 1000 authors.
- `events(filter, after, limit≤500)` → newest-first feed, supports per-author filtering across kinds.
- `stats { eventCount maxLevId }` → corpus size + monotonic change probe.
- Limits: body 256 KiB, `latestPerAuthor` authors ≤1000, page limits clamp to ≤500. Read-only, unauthenticated, wildcard CORS, default bind `127.0.0.1:8080` (Docker: container name on `deepfry-net`).

**Whitelist (corrected model).** The whitelist-plugin treats a pubkey as whitelisted if it is present as a `Profile` node in Dgraph (written by the web-of-trust crawler = "seen in the wild") plus 5 hardcoded admin/forwarder keys. Served O(1) via an HTTP server, 6h refresh, config `~/deepfry/whitelist.yaml`. **In this engine the whitelist is a scoring layer, not an exemption:** *not* being whitelisted is itself a spam signal; being whitelisted only passes that one layer and the pubkey still flows through every subsequent content/behavioral layer.

**Prior spam work (reference, not to duplicate).**
- `spam-explorer/` (Go) — active project scoring pubkeys by seed-relative valid-follower count over the Dgraph follow graph (BFS levels, weak-bridge collapse). Structural only.
- `web-of-trust/.planning/spikes/spam-clusters/` — completed spike: structural signals (follow-blast accounts, weakly-bridged pods, mutual-follow rings) on Dgraph. No content analysis, no enforcement.
- `quarantine/` — infrastructure to preserve rejected events; not detection.

**Candidate content/behavioral layers (to be validated in research):** near-duplicate clustering (SimHash/MinHash over content), posting-cadence/burst analysis, content entropy / templated-text detection, link & mention spam ratios, kind/tag fingerprinting, repeated-content ratio across a pubkey's own events. Final layer set and algorithms are a research deliverable, chosen on accuracy × execution speed.

## Constraints

- **Performance**: "As fast as possible" over potentially 100M+ events (≈all pubkeys × 100). Algorithms chosen for speed × accuracy; hot paths must stream, not buffer the whole corpus.
- **No local LLM**: model inference on-device is forbidden (too slow). Algorithmic / statistical detectors only.
- **Tech stack**: Rust (user decision). LMDB2GraphQL is Rust; could later read strfry LMDB directly to skip the GraphQL hop.
- **Read-only upstream**: only reads strfry via LMDB2GraphQL and the whitelist; never writes to the corpus.
- **Output store**: SQLite (scores, per-layer signals, suspected list, labeled feedback).
- **Layering**: every layer independently tunable (weight + threshold) to drive false positives down.
- **Correctability**: human-labeled false positives must re-tune layer weights ("backpropagation").
- **Monorepo placement**: independent subproject at `spamhunter/pubkey_iterator`; git root is the outer `deepfry` repo; do not cross into sibling projects without permission.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Rust for the engine | Max CPU throughput for content analysis (SimHash/MinHash, n-gram entropy); matches LMDB2GraphQL; path to direct LMDB reads | — Pending |
| SQLite as output store | Single queryable file holds scores, per-layer signals, suspected list, and labeled feedback; ideal substrate for the tuning loop | — Pending |
| Whitelist is a scoring layer, not an exemption | Absence from whitelist = spam signal; presence clears only that layer — still analyzed by later layers | — Pending |
| Weighted multi-layer score, tuned from labels | "Backpropagation" = logistic-regression-style re-optimization of layer weights from human-labeled false positives | — Pending |
| Re-runnable batch (not a service) | Simplest to reason about and tune; incremental service deferred to a later milestone | — Pending |
| Detect at pubkey level, aggregate event signals | Deliverable is a pubkey list; per-event signals are inputs, not outputs | — Pending |
| Complement, not replace, spam-explorer | spam-explorer = structural graph; this = content/behavioral. Disjoint signal sources | — Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-25 after initialization*

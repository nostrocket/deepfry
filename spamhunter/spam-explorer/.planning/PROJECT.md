# spam-explorer

## What This Is

A one-shot Go CLI tool for the DeepFry / Humble Horse stack that scores every pubkey in the web-of-trust follow graph by its **seed-relative valid-follower count**, then emits a JSONL file of pubkeys scoring below a threshold — the suspected spam / sybil candidates. It reads the ID-only follow graph that the web-of-trust crawler writes to Dgraph and turns the ad-hoc "weak bridge" intuition into a single, principled, reproducible metric.

## Core Value

Given a trusted seed pubkey, assign every reachable account a level equal to its follow-hop distance from the seed, then count a follower as **valid only if it sits on a strictly shallower level** (closer to the seed). This makes a dense spam cluster bridged by one weak edge collapse to a valid-follower count of ~1 regardless of its internal mutual following, while genuinely well-connected accounts keep high counts.

## Business Context

<!-- Internal infrastructure tool for the DeepFry relay stack. No direct revenue. -->

- **Customer**: DeepFry / Humble Horse relay operators (web-of-trust maintainers)
- **Success metric**: A low-valid-follower-count list that, on inspection, concentrates known spam-pod members near the top and keeps legitimate well-connected accounts out

## The Algorithm (locked semantics)

1. **Seed & BFS leveling.** Start from a user-provided seed pubkey at level 0. Traverse **outward along `follows` edges** (seed → accounts the seed follows = level 1 → accounts they follow = level 2, …). Every reachable account's level = shortest follow-hop distance from the seed.
2. **Valid-follower rule.** For a target pubkey `T` at level `L_T`, examine its followers (the reverse `~follows` edge). A follower `F` counts as **valid iff `level(F) < L_T`** — strictly upstream / closer to the seed. Followers at the same level or deeper are discarded as invalid "follow-backs."
3. **Structural consequence.** By construction every reachable non-seed node has ≥1 valid follower (its BFS-tree parent — the upstream node that first reached it). A score near 1 therefore means "reached only through a single weak bridge"; high scores mean genuine upstream connectivity.
4. **Output filter.** Emit JSONL `{pubkey, valid_follower_count}` for every scored pubkey with `valid_follower_count < N`, **excluding the seed's first `k` shells** (levels `1..k`), which are trusted by construction and would otherwise score artificially low.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Connect to the live web-of-trust Dgraph and stream the follow graph (paginated)
- [ ] BFS-level every account reachable from a user-provided seed along `follows` edges
- [ ] Compute each scored account's valid-follower count (followers strictly shallower than it)
- [ ] Exclude the seed's first `k` shells and emit JSONL of `{pubkey, valid_follower_count}` below threshold `N`
- [ ] Accept seed, `N`, `k`, Dgraph endpoint, and output path as CLI flags
- [ ] Detect and log (as errors) any expected-but-unreachable pubkeys — the seed network is assumed connected

### Out of Scope

- Cross-signal spam confirmation (burst-creation timing, mutual-follow rings, follow-blast out-degree) — that's the spike's "intersect signals" follow-up, a separate milestone; this tool delivers one clean signal
- Reading event content / Nostr payloads — this is structural inference on the ID-only graph only (canonical events stay in StrFry per the data-separation rule)
- Writing back to Dgraph, StrFry, or the whitelist — output is an analysis file; downstream enforcement is a separate concern
- A long-running service / live scoring — this is an offline, one-shot batch CLI
- Multi-seed scoring — single seed per run for v1

## Context

- **Upstream data model.** The web-of-trust crawler writes a Dgraph `Profile` graph: `pubkey` (`@id`), `follows`, `~follows` (reverse = followers), `follower_count` (indexed), `kind3CreatedAt`, `last_db_update`. Canonical events live only in StrFry's LMDB; Dgraph is ID-only relationships.
- **Scale.** Live production Dgraph holds ~1.54M `Profile` nodes (per the 2026-06-23 spam-clusters spike). BFS + scoring must stream paginated DQL rather than assume the whole graph fits comfortably in memory.
- **Prior art (web-of-trust spam-clusters spike, 2026-06-23).** Five DQL probes for structural spam signals. Most relevant: probe 04 "weakly-bridged / self-contained pods" — the same intuition this tool formalizes via levels instead of a trust-threshold on follower counts. The spike found a real 25-member spam pod co-followed by ~50 shared low-trust accounts. There is an existing `clusterscan` / `GetWeakBridges` notion in web-of-trust this tool supersedes with a cleaner metric.
- **Known caveat carried from the spike.** A pure isolation/weak-bridge signal pollutes with *legitimate newcomers* who simply aren't discovered by trusted nodes yet. The `k`-shell exclusion and threshold `N` mitigate but do not eliminate this; the metric is one signal, strongest in combination with others (out of scope here).
- **`follower_count` floor is 1, not 0** in this graph (~49% of nodes at fc==1). Any heuristic using `fc <= 1` matches half the graph — do not reuse that as a discriminator.

## Constraints

- **Tech stack**: Go (matches web-of-trust, which owns Dgraph access; reuse `github.com/dgraph-io/dgo/v210` and the established `Profile` schema). Go 1.24.1+.
- **Project boundary**: spam-explorer is an independent monorepo subdirectory. Read web-of-trust's schema/spike for reference, but do not modify web-of-trust without explicit permission.
- **Data separation**: structural inference only on the ID-only Dgraph graph; never pull event payloads.
- **Dgraph access**: paginated streaming (gRPC `localhost:9080` / HTTP `localhost:8080`); do not assume the full 1.54M-node graph loads into RAM in one query.
- **Determinism**: same seed + same graph snapshot ⇒ same scores; levels use shortest-path BFS (ties resolved by first-reached, which BFS guarantees).

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Language: Go | web-of-trust owns Dgraph access; reuse dgo/v210 client + `Profile` schema and Makefile conventions | — Pending |
| Dgraph access: paginated streaming | 1.54M nodes; avoid assuming the whole graph fits in memory at once | — Pending |
| BFS direction: outward along `follows` | Levels = follow-hop distance from seed; valid follower = strictly shallower (upstream) | — Pending |
| Unreachable pubkeys are an error | User asserts the seed network is connected; surface disconnected nodes as logged errors rather than silently skipping | — Pending |
| Params via CLI flags | One-shot batch tool (quarantine-rescuer style); run-specific seed/N/k belong on the command line | — Pending |
| Single seed per run (v1) | Keeps level semantics unambiguous; multi-seed deferred | — Pending |

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
*Last updated: 2026-06-23 after initialization*

# Spam-Cluster Probe — Index & Synthesis

**Date:** 2026-06-23
**Target:** live production Dgraph (`localhost:8080`), ~1.54M `Profile` nodes, ID-only follow graph.
**Method:** 5 independent DQL probes, one per structural spam signal, each run + interpreted in an isolated subagent. This is heuristic candidate discovery on an ID-only graph — no event content was available, so nothing here is proof, only ranked suspicion.

## Probes

| # | Signal | File | Flagged | Confidence |
|---|--------|------|---------|-----------|
| 01 | Sybil-follower hubs (purchased followers) | [`01-sybil-follower-hubs.md`](./01-sybil-follower-hubs.md) | 0 | LOW (no signal) |
| 02 | Follow-blast accounts (low-in / high-out) | [`02-follow-blast-accounts.md`](./02-follow-blast-accounts.md) | 643 | **HIGH** |
| 03 | Burst-created cohorts (timestamp spikes) | [`03-burst-created-cohorts.md`](./03-burst-created-cohorts.md) | ~22 (2 small clusters) | LOW–MOD |
| 04 | Weakly-bridged self-contained pods | [`04-weakly-bridged-pods.md`](./04-weakly-bridged-pods.md) | 242 @T200 / 472 @T500 | **HIGH** (one 25-pod) |
| 05 | Mutual-follow rings (reciprocity) | [`05-mutual-follow-rings.md`](./05-mutual-follow-rings.md) | 11 rings (1 strong) | **HIGH** (Ring #0) |

## Cross-cutting findings

**Methodological discovery (probe 01):** the graph's `follower_count` floor is **1, not 0** — ~49% of all nodes sit at exactly `fc==1`, only ~1% at `fc==0`. Any heuristic using `fc <= 1` matches half the graph and cannot discriminate. Future probes must use `fc == 0` or `count(follows)`-based signals, not `fc <= 1`.

**Strongest, most actionable clusters:**
- **Follow-blast population (02):** 643 accounts with `follower_count ∈ {0,1}` each following 501–1804 others. Out-degree values **repeat exactly across distinct pubkeys** (e.g. 1178 appears 8×) — templated/scripted contact lists, the cleanest coordination signal in the whole sweep.
- **25-member weak-bridge pod (04):** a self-contained island co-followed by ~50 shared low-trust accounts, with a uniform `max(inbound.fc) ≈ 30` ceiling — textbook spam pod with no edge into the trusted core.
- **10-member follow-for-follow ring (05):** mean reciprocity 0.93, nearly all members at `follower_count == 2`.

**False positives identified:**
- Probe 03's biggest timestamp spikes (Oct–Nov 2025, ~50/sec) are **crawler refresh batching**, not bot cohorts (0% follower-less, fc-capped at the crawl batch size). Temporal clustering alone is dominated by our own crawl artifacts.
- Probe 04's broad 242/472-flagged set over-includes new legit accounts (which also lack trusted followers).

## Recommended next step — intersect the high-confidence signals

The single most promising follow-up is a **cross-signal intersection**: pubkeys that appear in (02) follow-blast AND (04) a self-contained pod AND/OR (05) a reciprocity ring are multiply-confirmed spam candidates. Two-of-three agreement would be a high-precision seed set for an eventual spam-scoring or denylist feature — and a natural candidate for a future milestone.

Secondary follow-ups noted in the individual files: `fc==0`-only sybil re-run, `count(follows)==1` star-follower detection, and exact-out-degree-collision clustering on the 02 population.

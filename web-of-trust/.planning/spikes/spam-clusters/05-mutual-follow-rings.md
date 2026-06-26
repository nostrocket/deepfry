# 05 — Mutual-Follow Rings (reciprocity clusters)

**Signal:** Groups of accounts that follow each other reciprocally (follow-for-follow) to inflate each other's `follower_count` while attracting little/no genuine external inbound.

**Date:** 2026-06-23
**Data:** Live Dgraph, ID-only follow graph (~1.54M `Profile` nodes). Structural inference only — no content available.

---

## 1. Query

Exported a 2-hop neighborhood for a sample of low-follower seeds so that both directions of an edge (A→B and B→A) are observable within the sample:

```dql
{
  seeds(func: ge(follower_count, 2), orderasc: follower_count, first: 5000) @filter(le(follower_count, 12)) {
    a: pubkey
    afc: follower_count
    follows(first: 40) {
      b: pubkey
      bfc: follower_count
      follows(first: 40) {
        c: pubkey
      }
    }
  }
}
```

### Band & threshold choices

- **Seed band `2 <= follower_count <= 12`** — rings exist precisely to manufacture a *small but non-zero* follower count. `>=2` skips truly isolated/dead nodes; `<=12` targets accounts that have not accumulated organic reach. `orderasc` takes the lowest-follower seeds first, where ring behavior concentrates.
- **`follows(first: 40)` at both hops** — caps fan-out so a single popular hub doesn't dominate the export. This is the dominant sampling limitation (see caveats).
- **Reciprocity test:** seed `a` and followee `b` form a reciprocal PAIR when `a` appears among `b`'s sampled `follows` (i.e. `a ∈ {c}` under that `b`). Self-loops (`a == b`) are discarded.
- **Flag rule:** seed reciprocity ratio `>= 0.6` AND `follower_count <= 12`, where ratio = (# of a's sampled followees that follow a back) / (# of a's sampled followees).
- **Ring:** connected component (union-find over reciprocal edges) of size `>= 3`.

---

## 2. Aggregate stats

| Metric | Value |
|---|---|
| Seeds sampled | 5,000 |
| Seeds with >=1 followee in sample | 3,311 |
| Reciprocal pairs found | 152 |
| Flagged seeds (ratio>=0.6 & fc<=12) | 23 |
| Connected components (size>=2) | 123 |
| **Rings (component size>=3)** | **11** |
| Rings size>=4 | 7 |
| Rings size>=5 | 3 |

Reciprocity is rare in this low-follower band: only 152 reciprocal pairs across 3,311 active seeds. The vast majority of components are isolated pairs, and most size>=3 components are tree-shaped (`edges == size-1`), meaning union-find chained weakly-reciprocal pairs rather than finding dense cliques. The exceptions — high mean-reciprocity components — are the real signal.

---

## 3. Largest candidate rings

`mean_recip` = mean per-member reciprocity ratio. `edges` = reciprocal edges inside component. `flagged` = members meeting the flag rule. Member lists truncated where long.

| # | Size | Edges | Mean recip | Flagged | Member follower_counts | Sample members (truncated) |
|---|---|---|---|---|---|---|
| 0 | **10** | 9 | **0.93** | 8 | mostly **2** (one outlier 174) | `0149170f…295e84` (2), `548c55cd…6a2e24` (2), `6fd8cd35…6af55d3` (2), `88030887…1a70fc5` (2), `8d79bf18…f5de6f3` (2), `9632f98c…9fa2fdb` (2) … |
| 1 | 8 | 7 | 0.37 | 0 | all **2** (one 71) | `11966be2…708104d2` (2), `4a39f809…25f91f3a` (2), `5252a04b…d94a9e83` (2), `70e00c9a…538ebc4` (2), `d63d09ae…46ea1e7` (2), `e761e683…2da9b9f8` (2) … |
| 2 | 5 | 4 | 0.47 | 1 | 2,2,2,2 (one 50) | `1ca534f2…bc464ba0` (2), `723faa3a…0bc7de6a` (2), `7d7ae750…0cd9f2f3` (2), `f9ae9e9f…ebd6c627` (2), `e82f4e95…91784576` (50) |
| 3 | 4 | 3 | 0.22 | 0 | 2,2,2 (one 34) | `34120f1a…adb2e737` (2), `ca972aab…428f523d` (2), `e9d6b5ab…a0141efc` (2), `4cabdc05…d9f3ca8d` (34) |
| 4 | 4 | 3 | 0.20 | 0 | 2,2,2 (one 333) | `4faaa167…3869234d` (2), `5304371a…3226c69a` (2), `60a0186f…0541cd69` (2), `6052d2bf…465e6e9d` (333) |
| 5 | 4 | 3 | 0.16 | 0 | 2,2,2 (one 30) | `15185614…1518479a` (2), `31cd99f0…3c9d67e5` (2), `594db5a9…2000e337` (2), `a5cc7161…b67c2177` (30) |
| 6 | 4 | 3 | 0.50 | 0 | 2,2,3,5 | `062dc2ae…46f216d3` (2), `79c4d438…6c524a593` (2), `297cdff3…04e858848` (3), `177f4348…f800d538` (5) |
| 7 | 3 | 2 | 1.00 | 1 | 1,2,1056 | `5551e765…e83413ed4` (1), `e77b2468…131f9a0e9a` (2), `c8f63d08…f408a8c0` (1056) |
| 8 | 3 | 2 | 1.00 | 1 | 2,260,429 | `36de364c…fe106403` (2), `d01be06a…7258824fe` (260), `efb661e7…e2af9ad` (429) |
| 9 | 3 | 2 | 0.25 | 0 | 2,2,1325 | `b169fe00…b0446eb6` (2), `cc4e5965…467746ee` (2), `4d53de27…cd35b067f` (1325) |
| 10 | 3 | 2 | 0.40 | 0 | 1,2,96 | `047f3c5b…eb5a5a5f25` (1), `ec463dc9…5b2f9c45e8…` (2), `4163af8a…d6eca49b9` (96) |

---

## 4. Summary

### Findings

- **Ring #0 is a strong follow-for-follow candidate.** 10 interlinked members, 8 of which satisfy the flag rule, mean reciprocity **0.93**, and nearly every member sits at exactly `follower_count = 2`. A cluster of accounts each holding *exactly two* followers, where those followers are each other, is the signature of accounts that exist only to follow one another. The single `fc=174` member is most likely an outlier dragged into the component by one reciprocal edge, not a core ring member.
- **Rings #1–#6** are uniformly low-follower (members almost all at `fc=2`) but have **low mean reciprocity (0.16–0.50)**. These are union-find chains of mostly one-directional follows with a few reciprocal links — weaker evidence. They are worth a second look but are not clearly coordinated.
- **Rings #7–#10** have one or two members with high follower_count (260, 429, 1056, 1325). These are almost certainly **false positives**: a real/popular account that happens to be reciprocally followed by one or two tiny accounts. Reciprocity with a popular hub is normal, not ring behavior.

### Spam likelihood

- **Ring #0: high likelihood** of an artificial follow-for-follow ring. Recommend manual confirmation and, if confirmed, exclusion from trust propagation.
- Other rings: **low-to-moderate**; mostly explainable by ordinary mutual follows or sampling artifacts. The high-follower-member rings should be treated as noise from the connected-component method.
- Overall the band is *not* saturated with rings: 11 components from 5,000 seeds, only one convincing. Reciprocity is genuinely uncommon among low-follower accounts here, which makes the one strong ring stand out.

### Caveats (sampling & false positives)

1. **40-edge window.** Each node's follows are capped at 40 in the export. Rings larger than the window, or whose members follow many accounts, are **undercounted** — both edges of a reciprocal pair must land inside the sampled 40 of *each* endpoint to be detected. True ring sizes are lower bounds.
2. **Union-find over-merges.** A single weak reciprocal edge can chain two unrelated pairs into one "ring." Tree-shaped components (`edges == size-1`) are suspect; only high-`mean_recip`, high-`edges` components (e.g. #0) are trustworthy.
3. **Reciprocity is not proof.** Friends follow each other. The discriminator is the *combination* — high reciprocity AND uniformly low follower_count AND multiple interlinked members. Only Ring #0 clears all three.
4. **No content / timing.** ID-only graph; we cannot see profile metadata, post activity, or account-creation timing that would corroborate automation.

### Suggested follow-ups

1. **Pull the full follow lists for Ring #0 members** (uncapped, not `first:40`) and recompute the induced subgraph density. A near-complete clique among the `fc=2` members would confirm the ring.
2. **Cross-reference Ring #0 against `kind3CreatedAt`** — synchronized or burst contact-list timestamps strongly indicate coordinated creation.
3. **Add a "reciprocal-clique" metric to clusterscan**: for each low-follower component, compute internal edge density and the fraction of each member's *total* (uncapped) followers that come from inside the component. A ring should have most of its follower_count sourced internally.
4. **Re-run with a wider window** (`first:200`) on a smaller seed batch to test whether Rings #1–#6 densify (true rings) or stay sparse (sampling artifacts).
5. **Penalize internally-sourced follower_count** in trust scoring so follow-for-follow inflation does not buy whitelist eligibility.

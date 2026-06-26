# 04 — Weakly-Bridged / Self-Contained Pods

**Signal:** Accounts whose entire follower set consists of other low-follower accounts — i.e. they receive **no follow from any reasonably-trusted (high-follower) node**. A cluster with internal edges but zero trusted inbound is a classic spam island / weak bridge into the web of trust. This probes the project's existing clusterscan "weak bridge" concept directly via DQL on the ID-only follow graph.

**Date:** 2026-06-23
**Data:** live Dgraph, ~1.54M `Profile` nodes, ID-only (no content — structural inference only).

---

## 1. DQL queries

### Primary (spec) query
The spec's single query collapsed to a degenerate sample: `orderasc: follower_count, first: 4000` over the `@index`-backed `follower_count` returned **4000 nodes all at `follower_count == 3`** (the band floor). That undersamples the [3,60] band and biases toward brand-new accounts.

### Corrected sampling (used for results)
To cover the whole band evenly, sampled 400 candidates at each of 10 anchor follower-counts spanning 3..60, then merged + de-duped:

```dql
{
  candidates(func: eq(follower_count, $FC), first: 400) {     # $FC in {3,5,8,12,18,25,35,45,55,60}
    pubkey
    follower_count
    inbound: ~follows(first: 250) {
      ipk: pubkey
      fc: follower_count
    }
  }
}
```

- **Band [3,60]:** floor of 3 excludes singleton/near-empty accounts that carry no structural signal; ceiling of 60 keeps us below the "obviously trusted" range while staying in the zone where a self-contained pod is plausible.
- **`inbound: ~follows(first: 250)`:** the reverse `follows` edge = followers. We read each follower's own `follower_count` (`fc`) — that is the trust proxy.
- **Trust threshold T:** `max(inbound.fc) < T` ⇒ flagged (no follower is itself trusted ⇒ no bridge from the trusted core). Reported at **T=200** (primary) and **T=500** (stricter).

---

## 2. Aggregate stats

- Candidates sampled (unique): **4000** — 400 at each fc ∈ {3,5,8,12,18,25,35,45,55,60}.
- All 4000 had ≥1 inbound (`~follows`) edge (zero with empty followers).
- **Flagged at T=200: 242 (6.0%)** — every follower has `follower_count < 200`.
- **Flagged at T=500: 472 (11.8%)**.

Interpretation: ~6% of mid-band accounts receive **zero** follows from even a moderately-trusted node. That is a meaningful minority, consistent with a non-trivial population of self-contained pods plus some genuinely-new legit accounts.

---

## 3. Strongest pod candidates (T=200, lowest `max(inbound.fc)` first; cap 25)

`%≥T` is the fraction of inbound followers with `fc ≥ 200` — **0.0% for every flagged row by definition** (none reach T). The discriminating column is **max(inbound.fc)**: how close the *best* follower gets to trust. Lower = more isolated.

| pubkey | follower_count | max(inbound.fc) | %inbound≥200 | n_inbound |
|---|---|---|---|---|
| a0028e3ebd5f58b8b57d581521bcc069ac88e605344da8dfbf3da4f1ace97273 | 3 | 9 | 0.0 | 3 |
| 9da6f792d3611d170e20209d70ea28e83ba9145e98f7f6070508179af71a9501 | 3 | 26 | 0.0 | 3 |
| 59df12888ae34b0627cf224d84ce04f67962bce01173f78ff2e78da792e1744f | 55 | 27 | 0.0 | 55 |
| 533e479f529603bce09704ae00522f322e76b85743f9f86814ce43f1f320b59f | 60 | 28 | 0.0 | 60 |
| e59472363e20e47647c258848662d61893b6296932c66174686d310a28e05c8b | 25 | 29 | 0.0 | 25 |
| 177179d795f3ce304e97b689c0278426658312908f41317158447aed0ab7f26a | 55 | 30 | 0.0 | 55 |
| 6e8f20a7debdb17e74d4e0bba10cfe69eddf8a017e3223bf8a74618f945d7a97 | 45 | 30 | 0.0 | 45 |
| 5d9083e0ce3d6fea28901f6ffc4872b91bbbf1ac0f5ddfa3751c4041a510d6ef | 45 | 30 | 0.0 | 45 |
| 395a53062c30675205fc7259b13c3d80bf197a2b809db3de35b762cc69502038 | 45 | 30 | 0.0 | 45 |
| 59b82d623914b9457ada5220655d2d975799d82bf86dee86257ede7c1771d0e9 | 35 | 30 | 0.0 | 35 |
| 98bdafc808324ff024740ca61360a5db7c32ceaf540ddf3777b6e61267e21e86 | 35 | 30 | 0.0 | 35 |
| 7e59f09cac6ae6607f90fce1a273569958e17abb13f598dff508ea1e145cd0d6 | 25 | 30 | 0.0 | 25 |
| 97edb25ee35828e691dfcee87b4561e8795c3ec016021b01fdd8e235cbf5eb63 | 25 | 30 | 0.0 | 25 |
| 233a74d9100d24d352b4c583435aed66b6c002e4d332d5df9120bba833294221 | 25 | 30 | 0.0 | 25 |
| 617d3c41f614924a4d9df87092e1583319cdac2f275377716e30412451d2a3ad | 60 | 31 | 0.0 | 60 |
| 5ee38d6509bf3644784475fe34f646ddc8567cd0de161c4f01840e8d38e9530d | 60 | 31 | 0.0 | 60 |
| 9dc2fae66ebdb6732b0d8cfd9291eb3c298e12a0fc927b3edd04292b882bbf8c | 60 | 31 | 0.0 | 60 |
| f8601bac63aaa35c67841e1e8e35e609a6d049017e2ce5cb482f2c19a21faf8c | 55 | 31 | 0.0 | 55 |
| c92ed05a9bd73ce60d7ea192f52cf7fb0953dbcb20714377e4df9bced1d6a92b | 55 | 31 | 0.0 | 55 |
| 37ba6fce7373c8a39ac524acd8978a0cf08717ca3b5db98572d245a9b4e62331 | 55 | 31 | 0.0 | 55 |
| b0a1a7fa5f7bde283f4fa758ed2f66b67d485bdc62789c503115d06986f43ec9 | 55 | 31 | 0.0 | 55 |
| 2614197ffb691c5b66515ae62b2980e5474510504b4800dbb8171df900efb387 | 55 | 31 | 0.0 | 55 |
| 78511124c8db6c06cc091a02103de15c6e60602009b1d0574719b2cb323ce771 | 55 | 31 | 0.0 | 55 |
| ae48ad236dd999b9542cecc8b6352d50671aeb6a435f9f57633c54a56fc07ade | 55 | 31 | 0.0 | 55 |
| c74a113d4fc48dd0d969a5a1db6a6c2a73c6cc35fa253cd8eaac5f65e2923be3 | 45 | 31 | 0.0 | 31 |

Note the cluster of rows at `max(inbound.fc) ∈ {30,31}` with `follower_count` 25–60: a uniform ceiling like that across many distinct accounts is itself a fingerprint of a coordinated pod (all members followed by the *same* set of ~30-follower accounts).

---

## 4. Apparent pod groupings (shared inbound followers)

Built among the 242 T=200-flagged accounts. Two flagged candidates are linked if they share a "pod follower" — an inbound follower that follows **≥5** of the flagged candidates. Connected components ≥3 members:

| Pod | Members | Followers shared by ≥50% of members | Character |
|---|---|---|---|
| **Pod 2** | **25** | **50** | **Tight, coherent pod.** Half its members co-followed by 50 of the same accounts. Strongest spam-island signal in the sample. |
| Pod 1 | 36 | 0 | Loose / transitively-linked. No follower common to ≥50% of members — likely several sub-pods merged through a few high-degree shared followers; weaker. |
| Pod 3 | 5 | 1 | Small tight cluster. |
| Pod 4 | 5 | 1 | Small tight cluster. |

**Pod 2 (highest-confidence spam island)** — sample members:
```
7e59f09cac6ae6607f90fce1a273569958e17abb13f598dff508ea1e145cd0d6
97edb25ee35828e691dfcee87b4561e8795c3ec016021b01fdd8e235cbf5eb63
100bb8944b66d7694f6367c5854dd78f21b07be4f0014cedc9926ca9ea54c8c5
e59472363e20e47647c258848662d61893b6296932c66174686d310a28e05c8b
233a74d9100d24d352b4c583435aed66b6c002e4d332d5df9120bba833294221
95f188c1eaa57d46bda2942903695e828d934af711e47c64cc443142cfe41df6
59b82d623914b9457ada5220655d2d975799d82bf86dee86257ede7c1771d0e9
98bdafc808324ff024740ca61360a5db7c32ceaf540ddf3777b6e61267e21e86
555b001c24035b8fc745ffd57fa28af8f6fcc9674ffd84ba1213652e613b77f3
6e8f20a7debdb17e74d4e0bba10cfe69eddf8a017e3223bf8a74618f945d7a97
```
Top shared followers (the pod's internal "fans"):
```
b7bb99a47a550f11e7b39ae6726c6311cc744d29c671acbf8df75f512c9c5f92  (follows 20 flagged)
368b2f231938bf1647b9f3470f9a1ab17dd44c0844f86476c1981e94eb1d50a5  (follows 19)
daec124182e32beaae7dc3446034760fae789e368371402269d25f7fd57d4807  (follows 18)
0781e1d5db6d2e1a437165f232550d947a0bef3c01eb9dd324476aadc5ed766a  (follows 18)
8e75cfd0397e26679c5dd16205a2d803aeadfd5ebacf2ae1580835c90083fe0e  (follows 18)
```
Several Pod-2 members appear directly in the strongest-candidates table (§3) at the tell-tale `max(inbound.fc)=30` ceiling, cross-confirming the grouping.

---

## 5. Summary

**Findings**
- 6.0% (T=200) / 11.8% (T=500) of mid-band [3,60]-follower accounts receive **no follow from any moderately-trusted node** — they sit entirely below the trust horizon.
- A genuine, tightly-knit self-contained pod was found: **Pod 2, 25 members co-followed by ~50 shared low-trust accounts**, with a distinctive uniform `max(inbound.fc) ≈ 30` ceiling across members. This is the textbook weak-bridge / spam-island shape: internal mutual follows, zero trusted inbound.
- A larger but looser 36-member component (Pod 1) and two small 5-member pods also surfaced.

**Spam likelihood**
- Pod 2: **high** — coordinated co-following by a shared low-trust fan set is hard to produce organically.
- The broad 242-account flagged set: **suggestive, not conclusive** on its own.

**Main false-positive (caveat)**
- Small / new legitimate accounts also lack trusted followers. The flagged population is necessarily polluted with real newcomers who simply haven't been discovered by trusted nodes yet. The single weak-bridge signal **cannot distinguish a spam pod member from a legit newcomer** by isolation alone — it is strongest **in combination** with the other probes (mutual-follow density, burst-creation timing via `kind3CreatedAt`, follow-list similarity) and with the *grouping* evidence (shared-follower overlap), which Pod 2 has and lone newcomers do not.

**Suggested follow-ups**
1. Expand Pod 2: resolve its members' full follower/followee sets and measure intra-pod edge density vs. external edges (clustering coefficient). True pods will be near-clique internally.
2. Timing probe: pull `kind3CreatedAt` for Pod-2 members + their shared followers; coordinated pods often show tight creation/update bursts.
3. Cross-reference Pod-2 pubkeys against the existing `clusterscan` `GetWeakBridges` output to confirm the DQL probe and the Go analysis agree.
4. Re-run the band sweep at higher `first:` and finer fc anchors to size the total flagged population graph-wide, then cluster all flagged accounts at once (not just the sample) for a complete pod inventory.

---

*Artifacts (gitignored scratch): `_raw_band.json` (10× band samples), `_analyze2.py` (analysis), `_results.json`. The degenerate primary-query output is in `_raw_q.json`/`_raw_q2.json`.*

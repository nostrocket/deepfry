# Spike 01 — Sybil-Follower Hubs

**Signal:** Hub accounts whose follower base is dominated by zero/near-zero-follower
accounts — the structural signature of purchased or bot followers.
**Date:** 2026-06-23
**Graph:** ID-only follow graph, ~1.54M `Profile` nodes (live Dgraph, `localhost:8080`).

---

## 1. Queries run

### 1a. Primary query (as specified) — top hubs by follower_count

```dql
{
  hubs(func: ge(follower_count, 500), orderdesc: follower_count, first: 50) {
    pubkey
    follower_count
    sample_followers: ~follows(first: 1500) {
      fc: follower_count
    }
  }
}
```

### 1b. Threshold change — mid-tier band (descending → ascending)

The top-50 returned the legitimate Nostr "celebrity" core (jack, jb55, fiatjaf,
etc.) with a perfectly healthy follower mix (mean follower fc ~3000–4800, ~0%
near-zero). Purchased-follower hubs rarely climb to the absolute top of a 1.5M
graph, so I re-ran across the **bottom** of the hub band — accounts that just
crossed fc=500, the size where a bought burst would surface:

```dql
{
  hubs(func: ge(follower_count, 500), orderasc: follower_count, first: 600) {
    pubkey
    follower_count
    sample_followers: ~follows(first: 1500) {
      fc: follower_count
    }
  }
}
```
**Why:** `orderasc` + `first:600` walks the fc 500–560 floor where a sybil burst
would be most detectable; `first:50 desc` only sees the un-suspect head.

### 1c. Sanity check — distribution of low follower_count

```dql
{
  zero(func: eq(follower_count, 0)) { count(uid) }
  one(func: eq(follower_count, 1)) { count(uid) }
  le1(func: le(follower_count, 1)) { count(uid) }
  has_fc(func: has(follower_count)) { count(uid) }
}
```

---

## 2. Aggregate stats

| Metric | Top-50 head | Mid-tier (600) |
|---|---|---|
| Hubs analyzed | 50 | 600 |
| Followers sampled | 75,000 | 318,871 |
| Hubs flagged (>85% fc≤1, n≥200) | **0** | **0** |
| Mean / median fraction fc≤1 | 0.001 | median 0.025 |
| Max fraction fc≤1 (any hub) | 0.003 | 0.414 |

**Critical data-quality finding (query 1c):**

| follower_count | node count | % of graph |
|---|---|---|
| == 0 | 16,627 | ~1.1% |
| == 1 | 761,457 | ~49.4% |
| ≤ 1 | 778,084 | ~50.5% |
| has follower_count | 1,541,632 | 100% |

The crawler's **baseline `follower_count` is effectively 1, not 0** — roughly half
of all nodes sit at exactly fc==1. True zero-follower nodes are rare (16.6k).
This means `fc <= 1` is a near-universal property and a poor discriminator on this
dataset; `fc == 0` is the only clean "leaf" marker, and it is uncommon.

---

## 3. Top suspicious hubs

By the specified signal (>85% near-zero followers) **no hub qualifies** in either
band. For completeness, below are the hubs with the *highest* fraction of fc≤1
followers (mid-tier band) — i.e. the closest-to-suspicious, none rising to a
confident verdict.

| # | pubkey | follower_count | %fc≤1 | %fc==0 | mean follower fc | verdict |
|---|---|---|---|---|---|---|
| 1 | 4147451e52d3565b5067966bfcc8b134ece65201de7ad7d9c33e5238596a7645 | 524 | 41.4% | 0.0% | 89.7 | weak / watch |
| 2 | 7729790ce8aa278db90c3e4b213f1f41c83e678ecd2f93c6699f5e5750f3a772 | 506 | 36.0% | 4.7% | 327.7 | inconclusive |
| 3 | 7c746bf86db1bc8288c0d0c0191504ec8a77a4583ca47af5d22c15283ede9a72 | 504 | 34.1% | 0.8% | 27.6 | weak / watch |
| 4 | 6e710f260d9dd44b3724c7fa59160637aa0ae1f8cf9521d89cf27a9ac0042379 | 535 | 34.0% | 4.7% | 288.4 | inconclusive |
| 5 | 6d21157c48d75f8a979552d9b1160f66bed66b76dd47886ddb914011c2da1841 | 542 | 33.0% | 5.3% | 208.4 | inconclusive |
| 6 | 11d0d1b58d636848e4d19863970d36fa6b56f5e1cc0f376fa9ba049ea59125ee | 516 | 32.9% | 4.3% | 193.8 | inconclusive |
| 7 | 3deda05c236dd027b756007a21598db0fe890a70836748a08d52bffbf6f8fba8 | 509 | 32.6% | 0.0% | 11.4 | weak / watch (fc==1 cluster) |
| 8 | ac2250f83aaa7c4a8503f9c15c0cc11ac992315e5ac3e634541223a8deb6c09c | 550 | 32.4% | 4.7% | 241.5 | inconclusive |
| 9 | 052de869b320853c95ee860ece4a09b2b14f8a95ef34bdef027bcb1534e244c1 | 554 | 32.1% | 0.2% | 456.5 | inconclusive |
| 10 | 38a1c1ba22706094f844d53037b47a16cd3d23ac1b1e60a93f2b2ee09c624d4f | 548 | 31.8% | 2.4% | 301.4 | inconclusive |
| 11 | 79a6ef57c50cbc8421f5702cc61dd4352ca3b8aa6417704f26b156fb261faaed | 538 | 31.6% | 0.0% | 13.3 | weak / watch (fc==1 cluster) |
| 12 | 49d46f78b1e4a6aa2cea2c54206c739d9fe956c5a26a57da35252de5bfb35fb5 | 503 | 31.2% | 0.0% | 13.0 | weak / watch (fc==1 cluster) |
| 13 | e8c4546c4ca00775d06baa72d3cb75151ec6388d5eeb80bff30e37c7d5b9c085 | 543 | 30.9% | 0.0% | 9.3 | weak / watch (fc==1 cluster) |
| 14 | 972c69148c81e084abc97eb9bd928d2df2b764c70f91dd6646922a8f9088edcc | 536 | 29.9% | 0.0% | 11.0 | weak / watch (fc==1 cluster) |
| 15 | 703533c2c16ac7771efb1bdf60a85df74e42f8409a007900f402ba4684f99184 | 511 | 29.5% | 2.2% | 323.5 | inconclusive |
| 16 | d9b00be1e573a0f67b7da748550636099ac71ed90262f38ea0e609b9690fcda6 | 531 | 29.4% | 0.0% | 13.0 | weak / watch (fc==1 cluster) |
| 17 | 6d289286a7974d11033d9fed4471a489350340d88125803e31eebb14fe97b33e | 504 | 27.0% | 3.6% | 1008.0 | likely legit |
| 18 | 2d10243533fc008cfc2ebe0bf026e60f554059c19d1f93387351c2b27e1259d7 | 559 | 26.7% | 0.0% | 13.1 | weak / watch (fc==1 cluster) |
| 19 | da93192957495fb59f6ef1ce19e74947b0792b6eaa1b134a015c0326e4097d1a | 557 | 26.6% | 2.3% | 509.6 | inconclusive |
| 20 | c1aa0a2f0e1211dd3c46e285d64a411aca4f250bd372a0e85b98f7d6d03c9251 | 501 | 26.5% | 4.8% | 473.2 | inconclusive |
| 21 | 850605096dbfb50b929e38a6c26c3d56c425325c85e05de29b759bc0e5d6cebc | 509 | 26.5% | 1.6% | 661.6 | inconclusive |
| 22 | 359c70690e3e8d5ff034820fd3f44dd240ddf311b925ead5823d0a373cffdac9 | 559 | 26.4% | 3.0% | 393.5 | inconclusive |
| 23 | d6ec4b0572558def18d54167b77c5b0e8ec4ba1da945a92b8e391be28401d186 | 538 | 26.4% | 0.2% | 650.7 | inconclusive |
| 24 | de0c495539106b66a709351ce2766ee2d4ef65a6727b77a99a22d69468883a6b | 523 | 26.4% | 3.8% | 327.2 | inconclusive |
| 25 | 162b591922dabb671edc5ae8001b2eb1a982608c061863b5e9a5386f030a88ea | 537 | 26.3% | 0.0% | 12.8 | weak / watch (fc==1 cluster) |

---

## 4. Summary

**What was found:** Across 50 top hubs (75k sampled followers) and 600 mid-tier
hubs (319k sampled followers), **zero hubs** exhibit the purchased-follower
signature as specified (>85% of followers with fc≤1). The top of the graph is the
healthy Nostr core; the bottom of the hub band tops out at only ~41% near-zero
followers, with median ~2.5%.

**Spam likelihood from this signal:** **Low / inconclusive.** This query did not
surface a confident sybil-follower hub. The strongest candidates are the
"fc==1 cluster" hubs (ranks 7, 11–14, 16, 18, 25 — ~30% of followers at exactly
fc==1, p0≈0, mean follower fc ~9–13): their follower bases are unusually composed
of barely-connected accounts. That is *mildly* anomalous but well below a
confident threshold and is partly explained by the data-quality caveat below.

**Caveats (important):**
- **fc baseline is 1, not 0.** ~49% of all nodes have fc==1 and only ~1% have
  fc==0. The spec's `fc <= 1` test therefore matches roughly half the graph and
  cannot discriminate; only `fc == 0` is a clean leaf marker, and it is rare.
  The "expected" purchased-follower fingerprint (followers at fc==0) basically
  cannot manifest in this dataset as currently populated.
- This is an **ID-only structural** heuristic — no content, timing, or registration
  data. Absence of signal here is not proof of absence of spam.
- `~follows(first:1500)` returns an arbitrary (not random) follower slice; sampling
  bias is possible for the very largest hubs (1500 of 88k).
- `follower_count` is a stored value; if the crawler under-counts inbound edges
  for uncrawled accounts, real near-zero followers may be undercounted.

**Suggested follow-up queries:**
1. **Reciprocity / mutual-follow ratio** — bot rings follow the hub but the hub
   doesn't follow back, AND the bots follow *only* the hub. Test `count(follows)`
   of each follower: a hub whose followers each have `eq(count(follows), 1)` is a
   far stronger sybil signal than fc on this dataset.
2. **fc==0 concentration** — re-run the hub scan flagging on `fc == 0` only (the
   clean leaf marker), threshold ~30%, since ≤1 is saturated.
3. **Burst timing** — join follower `kind3CreatedAt`; purchased followers share a
   narrow creation/contact-list window. Flag hubs whose followers' kind3CreatedAt
   cluster in a tight interval.
4. **Tight fc==1 clusters** — investigate the ~10 "fc==1 cluster" hubs above for
   shared follower sets (do the same fc==1 accounts follow several of them?),
   which would indicate a coordinated low-cost follow farm.

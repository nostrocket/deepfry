# Spam Cluster Spike 02 — Follow-Blast Accounts

**Signal:** Accounts that *follow* large numbers of others but are followed by ~nobody — classic spam broadcasters / engagement bots / scrapers.
**Date:** 2026-06-23
**Graph:** Live Dgraph, ID-only follow graph (~1.54M `Profile` nodes). Inference is purely structural (no event content).

## Query

```dql
{
  blasters(func: le(follower_count, 1), first: 400000) @filter(gt(count(follows), 500)) {
    pubkey
    follower_count
    out_degree: count(follows)
  }
}
```

**Final threshold: `gt(count(follows), 500)`** with anchor `le(follower_count, 1)`.

**Why this threshold:** Threshold sweep on the prescribed anchor:

| out_degree threshold | rows flagged |
|---|---|
| `> 1500` | 2 |
| `> 1000` | 38 |
| `> 500` | **643** |

`>1500` and `>1000` returned too few rows to be useful as a population; `>500` yields a meaningful, still-bounded set (643) without ballooning into thousands. The query runs in ~7s server-side (well within the 220s budget), so no fallback to `eq(follower_count, 0)` was needed.

## Aggregate stats

- **Total flagged: 643** accounts (follower_count ≤ 1, out_degree > 500).
- **follower_count breakdown:** `1` → 574 accounts; `0` → 69 accounts.
- **out_degree distribution:** min 501, **median 506**, max 1804.

| percentile | out_degree |
|---|---|
| p10 | 501 |
| p25 | 503 |
| p50 | 506 |
| p75 | 511 |
| p90 | 890 |
| p95 | 1042 |
| p99 | 1357 |

| out_degree bucket | count |
|---|---|
| 500–1000 | 605 |
| 1000–1500 | 35 |
| 1500+ | 3 |

**Ratio out_degree/follower_count:** For the 574 accounts with `follower_count = 1`, the ratio equals their out_degree (501–1804 : 1). For the 69 accounts with `follower_count = 0`, the ratio is effectively infinite — they follow 500+ accounts and are followed by no one.

**Notable secondary signal — templated follow lists:** out_degree values are heavily clustered and frequently *identical* across distinct pubkeys (e.g. `1178` appears 8 times, `1221` appears 3 times in the top 25; the bulk of the population sits in a tight 501–511 band, median 506). Many distinct accounts following the *exact same number* of others is a strong fingerprint of coordinated / bot-generated contact lists rather than organic following behavior.

## Top 25 offenders (by out_degree)

| pubkey | follower_count | out_degree |
|---|---|---|
| `ac988b8b6caa68f1d5ef7b0cb264593b929debbaeddda05511a78e907fb6b8cc` | 1 | 1804 |
| `d955266e95e547b4b29df3b74f3eb55ba741596d2c5f3a3ce2aa0f4a9710c996` | 1 | 1802 |
| `ae311f0bf7550ca58749cd3ce76756966bb93e65aee1c347c707df0426bcba9a` | 1 | 1500 |
| `ef0e723c7cf47d46c13378ecf6d6b9dd3b9d942adbc455976eee816bcc3722d0` | 1 | 1497 |
| `004d4171c0c97c9e2d536495b83ef2172e713e0659c0e3b0321c4c72074efc39` | 1 | 1422 |
| `f21afc343af22d28be4d360d02cb46479094dabc7d8c29653a9ac54fe6325509` | 1 | 1363 |
| `5f09a017f7d9df813e0049f4e04cdddaee85343b56bf86910dd84dfa6258f538` | 1 | 1357 |
| `9854b872132c7dfa0ce02d6380c55d98cb95a5c4830db6ab8327df5f19abd424` | 1 | 1355 |
| `f653b27b53abf17ce3decdf52954e96ab18181cf0157c0aab059bcd278636f9a` | 1 | 1230 |
| `88e10c16122baf58b047a49142fca3334c1d31e4c8be4cb43b10b6b556d0d6f6` | 1 | 1230 |
| `f9c593d4a1626ea13d0af31537ffe2c2819f09040fc4fdc6cff6e3e508967680` | 1 | 1221 |
| `921986257dbb3b7b9a64ceaec9901ff448ce457915e87b03161df7891ceaaed0` | 1 | 1221 |
| `e68f25db3dce8cbd10b293f23a70d66142214691594ebc06a49bd6913e0840ab` | 1 | 1221 |
| `eb5c58fa9526d60f8eaa68087f9c4897e72d856a74338f836b011bf9c59ebfec` | 1 | 1220 |
| `2d5783092467f3aa058e1649028545db5435ce5fa9ca2c87191b54d85521527c` | 1 | 1178 |
| `476a450a8b737ed6657114cfc3998e61a1c721bb03f61bdae5d9af0345b6bdf7` | 1 | 1178 |
| `5a3ca38875feb832b1bf6755fad46dabf5b90441e506329fd13a74236507c4c3` | 1 | 1178 |
| `857d0e29fc7f644a5efa61402d0c96f0e835016d551f73006e7ca016de4cde90` | 1 | 1178 |
| `af4eaf19db1b97c55b5ac1e8b0ab6383664cd69c062fcfbd69226a0853dd0b4e` | 1 | 1178 |
| `068ae5eb25017a1e5b878204ba6925484fee91c2774b61c93956e629df7fb752` | 1 | 1178 |
| `2373449844daff942f5bcb960ae33b20d7472095556a9be468dca44e3d087c59` | 1 | 1178 |
| `76b8c933f5cfe4f811a1267fbd3d726c88883738f20166e5fd696530111b272b` | 1 | 1178 |
| `48f9f616f4aefa01b82440797fffb3d9f510783109145cd0b22e0edfce55dc42` | 1 | 1144 |
| `1814ad30a8a6c7067f008a4d4c16474867b04d04cf32f67fe9b4c8cc8927a202` | 1 | 1106 |
| `077b9070a1ba8ae952d67020a20279de251609a1f2087251463b6c9cd71cf752` | 1 | 1047 |

## Summary

**Findings.** 643 accounts follow 500+ others while being followed by at most one account; 69 of them have zero followers. The strongest individual offenders follow ~1800 accounts with a single follower (ratio ~1800:1). Beyond the per-account ratio, the population shows a striking *structural fingerprint*: out_degree values cluster tightly (median 506, p10–p75 all within 501–511) and repeat exactly across many distinct pubkeys (e.g. eight accounts each following precisely 1178), pointing to programmatically generated contact lists.

**Spam likelihood.** High for the population as a whole. A brand-new legitimate account can have 0–1 followers, but it will not immediately follow 500–1800 accounts — the high, often-identical out-degree paired with near-zero in-degree is the discriminating feature. The repeated-exact-out_degree clusters (1178×8, 1221×3, etc.) are very likely the same actor or toolkit and warrant treating those sub-clusters as coordinated.

**Caveats.**
- ID-only graph: no profile metadata, no event content, no NIP-05 — confirmation requires cross-checking against StrFry/relays.
- `follower_count` is the stored in-degree; if crawl coverage is incomplete, some accounts may have more real followers than recorded, slightly inflating the flagged set.
- A handful of legitimate aggregators/relays/bridges can also have high out-degree and low in-degree; treat the very lowest out_degree band (501–511) as weaker evidence than the 1000+ tail.
- The cluster anchors on `follower_count ≤ 1`; genuine blasters that have picked up 2–5 followers are excluded by design (see follow-up).

**Suggested follow-ups.**
1. Loosen the anchor to `le(follower_count, 5)` (or 10) to catch blasters that acquired a few reciprocal/bot followers, and compare population growth.
2. Group the flagged set by exact out_degree and inspect whether same-out_degree accounts follow the *same target set* — if so, collapse them into named coordinated clusters and score the whole cluster.
3. Reverse-pivot: examine *who* these blasters follow (`follows`) — common high-frequency targets may themselves be promoted spam / scam accounts.
4. Cross-reference flagged pubkeys against StrFry event volume and `kind3CreatedAt` recency to separate dormant scrapers from active broadcasters.
5. Feed the top tail (out_degree > 1000, 38 accounts) into the whitelist plugin as candidate denials and observe false-positive rate.

# 03 — Burst-Created Cohorts (temporal clustering)

**Signal:** Coordinated batches of accounts whose kind-3 contact lists share near-identical `kind3CreatedAt` timestamps — a signature of bots created/configured together in one scripted run.

**Date:** 2026-06-23

---

## 1. Queries used

**Step 1 — timestamp range (full graph, indexed):**
```dql
{
  mn(func: has(kind3CreatedAt), orderasc:  kind3CreatedAt, first: 1) { kind3CreatedAt }
  mx(func: has(kind3CreatedAt), orderdesc: kind3CreatedAt, first: 1) { kind3CreatedAt }
}
```

**Step 2 — sample for histogramming:**
```dql
{
  q(func: has(kind3CreatedAt), first: 250000) {
    pubkey
    kind3CreatedAt
    follower_count
  }
}
```

**Sample size:** 250,000 nodes.
**CAVEAT:** This is a *sample* (~16% of the ~1.54M `Profile` nodes), not the full graph, and Dgraph `first:` returns nodes in internal-UID order (not random), so bucket counts are lower bounds — a real burst may be much larger in the full data. All histogramming was done in `python3`; `kind3CreatedAt` is a client-set field.

---

## 2. Timestamp range & garbage timestamps

| Metric | Unix | Human (UTC) |
|---|---|---|
| Min `kind3CreatedAt` | 1641010324 | 2022-01-01T04:12:04 |
| Max `kind3CreatedAt` | 1782188344 | 2026-06-23T04:19:04 |

**Garbage-timestamp audit (in 250k sample):**

| Class | Count | Notes |
|---|---|---|
| Zero (`== 0`) | 0 | none |
| Negative | 0 | none |
| Beyond "today" (> ~2026-06-23) | 0 | max value is essentially *now*, not absurd future |

There are **no nonsensical timestamps** (no 0, no negatives, no far-future). Min 2022-01 and max 2026-06-23 are both physically plausible for a live Nostr graph. (Note: an earlier internal "future > 1.75e9" cutoff is a stale reference point = 2025-06-15; it flags ~31% of rows, but those are all legitimately in the past relative to today's 2026-06-23 and are not anomalies.)

---

## 3. Top spike buckets (exact-second)

Top buckets by count, with share of low-follower accounts (`follower_count <= 2`). Baseline low-follower share across the whole sample is **~32%**.

| Unix ts | Human (UTC) | Count | % low-foll | Example pubkeys |
|---|---|---|---|---|
| 1762067717 | 2025-11-02T07:15:17 | 50 | 0% | 792e6325c2903c2c…, 9fa2fa922beaa539… |
| 1762067687 | 2025-11-02T07:14:47 | 31 | 0% | 29cbb2cd70c95ea9…, d280719900d95134… |
| 1762067808 | 2025-11-02T07:16:48 | 29 | 0% | 9b1bd25f84e941b1…, f1cf56619c9adfef… |
| 1762067748 | 2025-11-02T07:15:48 | 29 | 0% | d22b463987d355ec…, 0af673286080022d… |
| 1762067718 | 2025-11-02T07:15:18 | 25 | 0% | b5583dfa985fe903…, b552a750a037930f… |
| 1762067868 | 2025-11-02T07:17:48 | 25 | 0% | 786eb628989e20d0…, ec572bc46f9515b9… |
| 1762067838 | 2025-11-02T07:17:18 | 21 | 0% | 2f59dd56ea067c7b…, e6a91a3bf3d185ec… |
| 1762067778 | 2025-11-02T07:16:18 | 21 | 0% | 3d12a29ddb60c96f…, 66cdc5236784b91a… |
| 1761814024 | 2025-10-30T08:47:04 | 19 | 0% | 01336c3844976341…, 0675d22f85cd73f7… |
| 1762067686 | 2025-11-02T07:14:46 | 19 | 0% | 90512c543a7f75e6…, f9d994e622b7c2f5… |
| 1726800227 | 2024-09-20T02:43:47 | 11 | **73%** | 133c972c3c2e3c47…, 57e6e22d008e4b3d… |
| 1726717802 | 2024-09-19T03:50:02 | 11 | **64%** | 74aa77ae288d4280…, 301f3f662e7dc0e5… |
| 1710269769 | 2024-03-12T18:56:09 | 10 | 20% | b0608259094d23fc…, c671d7123714da26… |
| 1761808105 | 2025-10-30T07:08:25 | 10 | 0% | 99d17a69196b6ecb…, 44e0ef8f126d96f8… |
| 1761807838 | 2025-10-30T07:03:58 | 10 | 0% | 0e6717944e1bd9d8…, 09c42d2f3ab73a4e… |
| 1761734839 | 2025-10-29T10:47:19 | 10 | 0% | 02a98fb0bd999cec…, f1e93de088a9ebe1… |
| 1761814201 | 2025-10-30T08:50:01 | 10 | 0% | 0424f3fb031ddf6b…, 1a6d4dd8e23ab356… |
| 1761808165 | 2025-10-30T07:09:25 | 10 | 0% | 0500c5a4521d65da…, a5ddc00f14620988… |
| 1761808066 | 2025-10-30T07:07:46 | 10 | 0% | 5100742dd7a3234b…, 6ffc0347b1a7f43d… |
| 1761814301 | 2025-10-30T08:51:41 | 10 | 0% | 03f39787fe60ff37…, 71b3679a700a0ab1… |
| 1761808285 | 2025-10-30T07:11:25 | 10 | 0% | 53dd741c4b2c8693…, de17be29f2773d73… |
| 1761734771 | 2025-10-29T10:46:11 | 10 | 0% | 08f79302fb0ad8dc…, 54293c2085be509c… |
| 1761807979 | 2025-10-30T07:06:19 | 10 | 0% | 0227d6c10ca08bda…, c2f7c35f571d0344… |
| 1761725552 | 2025-10-29T08:12:32 | 10 | 0% | 2c29ccc35a7607be…, 270ec387f3d42d79… |
| 1761808225 | 2025-10-30T07:10:25 | 10 | 0% | 844724fb93730670…, 2e8d229a56ab664f… |

**Top minute buckets:** 2025-11-02 07:15 (104), 07:14 (50), 07:16 (50), 2026-06-06 08:07 (49), 07:17 (46), 2026-06-06 08:08 (45).

---

## 4. Key cross-signal observation

The largest spikes — the 2025-10-29/30 and 2025-11-02 clusters — are **0% low-follower**. Inspecting the Nov-2 07:14–07:19 window (250 accounts in-sample) shows `follower_count` values pinned at exactly **249** for the entire cluster. 249 is a capped/sentinel value (the crawler appears to clamp `follower_count`), and these are well-connected accounts, not follower-less bots. A burst of *high-degree* accounts all timestamped within a few minutes is the signature of a **crawler refresh pass / batched re-write**, not a bot-creation event.

The genuinely *suspicious* buckets are smaller but cross-signal positive:

- **1726800227 (2024-09-20 02:43:47), 11 accts, 73% low-follower**
- **1726717802 (2024-09-19 03:50:02), 11 accts, 64% low-follower**

These pair a same-second timestamp with a follower-less majority — the actual bot-cohort signature this signal is hunting for. They are modest in the sample but, given the ~16% sampling and non-random scan order, could be materially larger in the full graph.

---

## 5. Summary

**Findings.** Timestamp range is clean (2022-01 → 2026-06-23) with **zero garbage timestamps** (no 0, negative, or impossible-future values) in the 250k sample. The dominant temporal spikes (Oct 29–Nov 2 2025, ~50/second peak) are **0% low-follower and follower-capped at 249**, which reads as crawler/refresh batching rather than coordinated bot creation. Two smaller same-second buckets on 2024-09-19/20 (~11 accounts each, 64–73% follower-less) are the only buckets that fit the burst-bot signature.

**Spam likelihood.** **Low–moderate, and the top spikes are mostly false positives.** Pure timestamp clustering is dominated here by crawler artifacts. The signal only becomes meaningful when **intersected with low `follower_count`** — and on that combined basis the evidence in this sample is thin (a couple of ~11-account buckets).

**Caveats.**
- `kind3CreatedAt` is **client-set** and can legitimately repeat — popular clients writing default lists, mass migrations, or relay-side normalization can all produce real same-second clusters.
- Results are from a **16% non-random sample**; counts are lower bounds. A real burst could be far larger in the full 1.54M.
- `follower_count` is **capped (≈249)**, so it cannot distinguish "well-connected" from "extremely well-connected," but low values remain reliable for spotting follower-less accounts.
- The big spikes co-locating with refresh timing strongly implicate crawler write-time, which is **not** the same as the Nostr event's true authored time on all relays.

**Suggested follow-ups.**
1. Re-run on the **full graph** (paginate, not `first:`) and rank buckets by *low-follower count within the bucket*, not raw size — to surface the 2024-09 type clusters that the refresh artifacts currently bury.
2. Cross-reference candidate cohorts with **`uncrawled`** and with follow-graph overlap (do the same-second accounts follow the same small target set? a hub-and-spoke follow pattern within a timestamp cohort is a much stronger bot signal).
3. Confirm whether the Oct/Nov 2025 0%-low clusters correspond to a known **crawler batch run** (check `~/deepfry/crawler-metrics.jsonl`); if so, exclude crawler-refresh windows from this signal entirely.
4. Tighten the low-follower threshold relative to the 249 cap and add a degree-overlap term before treating any cohort as actionable.

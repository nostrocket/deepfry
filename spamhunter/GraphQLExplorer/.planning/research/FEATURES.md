# Feature Research

**Domain:** Read-only, author-centric spam-investigation frontend over a Nostr relay (LMDB2GraphQL lens)
**Researched:** 2026-06-24
**Confidence:** HIGH (feature-to-API mapping is code-verified against `contract.md` v1.0); MEDIUM on heuristic thresholds (corroborated across multiple sources, but tune-to-data)

## Orientation

This is a single-analyst forensic UI, not a moderation queue and not a relay. The entire job is: **paste a suspect pubkey (or import up to 1000), then judge "spammer or not?" fast and defensibly.** Every feature below is scored against that core value and is hard-bounded by what the API can actually do.

The decisive design facts from `contract.md`:
- **No content search, no firehose, no push.** Entry is always a *known* pubkey. All "discovery" is the analyst's, not the tool's.
- **Two workhorse queries:** `events(filter, after, limit)` (single-author drill-down, cursor-paginated, `limit` clamped to `[1,500]`) and `latestPerAuthor(kind, perAuthor, authors)` (batch triage, `authors`≤1000, `perAuthor`≤500, cost ≈ `authors × perAuthor`). Plus `stats`.
- **Fixed ordering** `createdAt DESC, levId DESC` — no `orderBy`; any other order is client-side after fetch.
- **`createdAt` is author-claimed**, not relay-receive time. This is load-bearing for rate/burst analysis: a spammer can fabricate timestamps, so burst signals can be *spoofed flat* and the absence of a burst is not exoneration.
- **All client-side analytics** (dedup, rate buckets, tag tallies, kind histograms) run only over events already fetched into the browser — never over the whole corpus.

This shapes the single most important framing for the whole tool: **every analytic is "over the fetched window," and the window size must be visible.** A "0 duplicates" verdict over 50 events is meaningless if the author posted 5,000.

## Feature Landscape

### Table Stakes (Analyst Expects These)

Missing these = the tool fails its core promise ("judge a suspect fast").

| Feature | Why Expected | Complexity | API Mapping / Notes |
|---------|--------------|------------|---------------------|
| **npub/hex input + normalization** | Humans paste `npub` from clients; API speaks hex only. Reject/repair bad input. | LOW | nostr-tools `nip19`. Convert to hex for query, render npub for humans. Validate 64-char lowercase hex; reject mixed-case/wrong-length early (it silently won't match). |
| **Single-author drill-down shell** | The atomic unit of investigation. One pubkey → one workspace. | MEDIUM | `events(filter:{authors:[pk]}, limit:…)` paginated by `endCursor`. Tabs/panels for the 4 signals share one fetched event set. |
| **Event timeline, newest-first** | See *what* and *when* at a glance. | LOW | `events` is already `createdAt DESC`. Render rows; show `createdAt` as both absolute + relative. Flag author-claimed time. |
| **"Window size" / fetched-count indicator** | Every analytic is over fetched events only; a verdict without denominator is misleading. | LOW | Show "showing N events (hasMore: true/false)". This is the honesty backbone of the whole tool. |
| **Kind distribution histogram** | Spam often skews to one kind (1 notes, DMs, reactions); profile-only or reaction-flood authors look different. | LOW | Tally `kind` over fetched events. Cheap bar chart (CSS/SVG). Signal #4. |
| **Raw-JSON inspector** | `raw` is canonical; analysts must see exactly what strfry stored (tags, odd fields). | LOW | Per-event `raw` field (lazy-fetch; `raw` is large — don't pull it for every row up front). Signal #4. |
| **Content view with text** | Reading the actual posts is the irreducible human judgment. | LOW | `content` field. Render plaintext; do NOT render as HTML/markdown-execute (XSS + spammer payloads). |
| **Tag/mention aggregation (p/e/t)** | Mass-mention & hashtag-stuffing are top spam tells. | MEDIUM | Tally `tags` client-side: count distinct `p` (mentions), `e` (event refs), `t` (hashtags) per event and across window. Signal #3. |
| **Near-duplicate / repeated-content highlighting** | Copy-paste flooding is *the* classic spam signal. | HIGH | Client-side shingling + similarity over `content`. Signal #2. See Heuristics §. |
| **Posting-rate / burst indicators** | Bots post in machine-gun bursts; humans don't. | MEDIUM | Bucket `createdAt` deltas client-side. Signal #1. Caveat: author-claimed time. |
| **Corpus stats dashboard, polled** | Context ("how big is the corpus this verdict is relative to?") + change detection. | LOW | `stats{ eventCount maxLevId dbVersion pinnedStrfryVersion }`. Poll `maxLevId` on an interval (seconds) for "new data." |
| **GraphQL error + readiness handling** | `503`/`/ready` gating, `errors[]` on HTTP 200, `INVALID_CURSOR`, `TOO_MANY_AUTHORS`, `413`. | MEDIUM | Gate first query on `/ready`; always inspect `errors[]`; branch on `extensions.code`. Cross-cutting; nearly every feature depends on it. |
| **Batch import (paste/file, ≤1000)** | Analysts work suspect *lists*, not just singletons. | MEDIUM | Parse npub/hex list → hex; chunk at ≤1000 for `latestPerAuthor` (else `TOO_MANY_AUTHORS`); watch 256 KiB body cap. |
| **Batch triage table** | Scan many authors at once, sort by cheap signals, drill into the worst. | MEDIUM | `latestPerAuthor(kind, perAuthor, authors)`. Keep `perAuthor` small (3–10) for triage; cost ≈ authors×perAuthor. Empty groups omitted — match by `author`, never zip by index. |

### Differentiators (Make the Analyst Fast & Defensible)

Not strictly required, but this is where the tool earns its keep over "just use GraphiQL."

| Feature | Value Proposition | Complexity | API Mapping / Notes |
|---------|-------------------|------------|---------------------|
| **Per-author "spam score" rollup** | One glance summarizes 4 signals (dup ratio, burst score, mention/hashtag density, kind skew) into a sortable column. Speeds triage hugely. | MEDIUM | Pure client-side aggregate of signals already computed. MUST be transparent (show the contributing sub-scores), never a black box. |
| **Near-dup *clustering* (not just pairwise flag)** | "32 of 50 posts are 3 variants of the same shill text" is far more damning than scattered pairwise matches. | HIGH | Union-find / connected components over the similarity graph from Signal #2. Show cluster size + representative text. |
| **Burst visualization (rate sparkline / histogram of inter-post gaps)** | A picture of "200 posts in 4 minutes" is instantly legible. | MEDIUM | Histogram of `createdAt` deltas + posts-per-minute/hour buckets. Annotate with "author-claimed time" warning. |
| **Mention-target fan-out view** | Hashtag stuffing & mass-mention show as "same 1 post @-mentions 40 distinct pubkeys" or "uses 25 hashtags." | MEDIUM | Per-event tag counts + window-level distinct-target tally from Signal #3. Top-N targets table. |
| **"Load more / fetch deeper" with live re-compute** | Lets the analyst expand the window when 50 events is inconclusive, with analytics updating. | MEDIUM | Paginate `events` via `endCursor`; recompute client-side analytics incrementally. Ties directly to the window-size indicator. |
| **Cross-author duplicate detection (within a batch)** | Coordinated spam rings reuse identical text across many accounts — the strongest ring signal. | HIGH | Run dedup across the union of fetched events from `latestPerAuthor`. Only meaningful with small `perAuthor` per author. |
| **Copy-out / export of evidence** | Analysts need to record a verdict (pubkey + offending event ids + raw). | LOW | Client-side; copy hex ids / `raw` JSON to clipboard or download. No API write. |
| **Deep-link / shareable author URL** | Reopen an investigation by URL (`/author/<hex>`). | LOW | Route state from hex pubkey. Pure client routing. |
| **Kind-aware drill (e.g. only kind 1, or include reactions/DMs)** | Different spam lives in different kinds; let analyst pivot the filter. | LOW | `events(filter:{authors:[pk], kinds:[…]})`. Cheap; reuses the drill-down shell. |

### Anti-Features (Deliberately NOT Build)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| **Global firehose / "browse all events"** | Feels like a relay explorer. | API has no unfiltered-discovery use case the analyst wants; pulling pages of the global feed is heavy and off-mission (author-centric entry only). | Always enter by known pubkey. |
| **Content search ("find authors by text")** | Obvious moderation wish. | API exposes **no** text-search query — impossible without scanning the whole corpus client-side (infeasible). | Tag filters (`t`/`p`/`e`) are the only server-side "search." Entry is by pubkey. |
| **Realtime feed / live tail** | "Watch spam as it happens." | No subscriptions/WebSocket in the API; polling at ms cadence is abusive and pointless. | Poll `stats.maxLevId` on a sane interval (seconds) for a "new data available, refresh?" nudge. |
| **Auto-classify / ML verdict ("this IS spam")** | Everyone wants the button. | The tool's value is *assisting human judgment*, not replacing it; an opaque label invites both false bans and complacency. Author-claimed timestamps and small windows make automated verdicts unreliable. | Transparent per-signal scores the analyst interprets. Keep human-in-the-loop. |
| **Writing NIP-56 reports / labels / bans** | Natural "next step" after judging. | API is strictly read-only (no Mutation/Subscription). Building a write path means a *different* backend — scope explosion. | Export evidence; act in a separate tool/relay. |
| **Re-verifying signatures / multi-relay aggregation** | "Be thorough." | strfry already verified sigs on ingest; this is a single-relay corpus. Re-verify = wasted compute + false confidence. | Trust strfry's verification; state "single-relay view" plainly. |
| **Full thread/reply navigation as a primary view** | Threads are nice context. | Reply-graph traversal (`e`-tag chasing) is many round-trips and tangential to spam judgment. | Tag analysis (Signal #3) covers the spam-relevant `e`/`p` subset; offer "events referencing this id" as a secondary lookup at most. |
| **Server-side `perAuthor: 500 × 1000 authors` "fetch everything" triage** | "Just load it all." | Cost ≈ 500,000 index scans + huge payload + 256 KiB body risk. | Triage with small `perAuthor` (3–10); deepen only on drill-down. |
| **Persisting analyst state to a backend / multi-user** | Team workflows. | API is unauthenticated, single-local-analyst by design. | Local-only state (URL, localStorage). |

## Spam-Signal Heuristics (the four chosen signals, made concrete)

> Confidence: MEDIUM. Thresholds below are sane starting defaults from the dedup/burst literature; **all should be tunable in the UI** because optimal values are data-dependent. None should be presented as authoritative verdicts.

### Signal 1 — Timeline + posting-rate / burst (`events`, fields: `createdAt`)
- **Inter-post gap histogram:** compute `Δt` between consecutive `createdAt` over the fetched window. Bucket: `<1s`, `1–10s`, `10–60s`, `1–10m`, `>10m`. A heavy `<10s` lobe = bot cadence.
- **Posts-per-window rate:** posts/minute and posts/hour. Flag e.g. **>30 posts/min** or **>500 posts/hour** as burst (tunable).
- **Regularity / metronome detection:** near-constant Δt (low variance, e.g. coefficient of variation < ~0.1) suggests scheduled automation — humans are bursty-irregular.
- **CRITICAL caveat:** `createdAt` is author-claimed. A sophisticated spammer can spread fake timestamps to *hide* bursts, or cluster them. So: burst present = suspicious; burst absent ≠ clean. Surface this caveat in the UI next to the chart.

### Signal 2 — Near-duplicate / repeated content (`events`, field: `content`)
- **Exact-dup first (free win):** hash normalized `content` (trim, lowercase, collapse whitespace); identical hashes = exact repeats. Cheap, high-signal.
- **Near-dup via shingling + Jaccard:**
  - **Shingles:** word-level **k=3–5** n-grams for note-length text; **character k=4** for very short posts. (Char-4 / word-3 are common practical defaults.)
  - **Threshold:** **Jaccard ≥ 0.8** = near-duplicate for general text (≥0.6 only for very short strings; tune). Verify candidate pairs with exact Jaccard to keep precision high.
  - **Scale:** at the window sizes here (≤500 events × paginated), brute-force pairwise O(n²) Jaccard is fine; **MinHash/LSH is unnecessary** unless cross-author batch dedup over thousands of events. Document this so nobody over-engineers.
- **Output:** "dup ratio" (% of window that is a near-dup of another post) + cluster sizes (differentiator). High dup ratio over a *large* window is the single strongest content signal.

### Signal 3 — Tag/mention aggregation (`events`, field: `tags`)
- Parse `tags`: `tags[i][0]` = name, `tags[i][1]` = value.
- **Per-event counts:** distinct `p` (mentions), `e` (refs), `t` (hashtags). Flag **mass-mention** (e.g. ≥10 `p` tags in one event) and **hashtag-stuffing** (e.g. ≥10 `t` tags, tunable).
- **Window-level fan-out:** distinct mention targets across the window, top-N hashtags, top-N mentioned pubkeys. A few accounts mentioned thousands of times = targeted harassment/shill.
- **Repetition:** same hashtag set on every post pairs with Signal #2 (template spam).

### Signal 4 — Kind distribution + raw inspector (`events`, fields: `kind`, `raw`)
- **Kind histogram** over the window. Profiles of interest: 100% kind-1 floods; reaction floods (kind 7); DM spam (kind 4); zap-bait. A monoculture of one kind at high rate is suspicious.
- **Raw inspector:** on demand, show byte-exact `raw` for any event — reveals odd/extra tags, malformed fields, payloads not surfaced by typed fields. Lazy-load (`raw` is large; §9 best practices says skip it unless needed).

## Feature Dependencies

```
[Error/readiness handling] ──required by──> [everything that queries]

[npub/hex normalization]
    └──required by──> [Single-author drill-down]  and  [Batch import]

[Single-author drill-down shell]
    └──required by──> [Timeline] [Content view] [Tag aggregation] [Kind histogram] [Raw inspector]
    └──required by──> [Window-size indicator] ──enhances──> ALL four signals (honesty denominator)

[Content view] ──required by──> [Near-dup detection] ──required by──> [Near-dup clustering]
                                                       └──required by──> [Cross-author dup (batch)]

[Tag aggregation] ──required by──> [Mention fan-out view]

[Timeline] ──required by──> [Burst visualization]

[Four signals] ──feed──> [Per-author spam-score rollup] ──displayed in──> [Batch triage table]

[Batch import] ──required by──> [Batch triage table] ──required by──> [Cross-author dup]

[Load-more pagination] ──enhances──> ALL signals (expand window, re-compute)
```

### Dependency Notes
- **Error/readiness handling is foundational:** silent `limit` clamping, opaque cursors, `errors[]` on HTTP 200, and `503` gating touch every query. Build it first; everything else assumes it.
- **Window-size indicator must ship with the first signal,** not later — without it the analytics are misleading.
- **Clustering depends on pairwise near-dup;** don't build clustering before the similarity layer exists.
- **Cross-author dup depends on both batch import AND the dedup layer,** and only makes sense with small `perAuthor`.
- **Spam-score rollup depends on all four signals;** it is a *view* over them, sequenced last.

## MVP Definition

### Launch With (v1)
The vertical slice that delivers the core value ("paste a pubkey → judge it").
- [ ] Robust API access (readiness gate, `errors[]`, cursor pagination, clamp awareness) — nothing works without it
- [ ] npub/hex input + normalization — the entry point
- [ ] Single-author drill-down shell over one fetched event set
- [ ] Event timeline (newest-first) + **window-size indicator** — the honesty backbone
- [ ] Posting-rate / burst indicators (Signal 1) — inter-post gap buckets + posts/min
- [ ] Content view + near-duplicate highlighting (Signal 2) — exact-dup + Jaccard≥0.8 pairwise
- [ ] Tag/mention aggregation p/e/t (Signal 3) — per-event + window tallies
- [ ] Kind histogram + raw-JSON inspector (Signal 4)
- [ ] Corpus stats dashboard, polled (`stats`, `maxLevId` change probe)
- [ ] Local-dev Vite proxy to `127.0.0.1:8080` (the documented CORS fix)

### Add After Validation (v1.x)
- [ ] Batch import (≤1000, chunked) + triage table — trigger: analyst is processing lists, not singletons
- [ ] Per-author spam-score rollup — trigger: triage table exists and needs a sortable summary column
- [ ] Near-dup clustering + burst sparkline + mention fan-out — trigger: pairwise/flat versions prove too coarse in practice
- [ ] Evidence export / deep-link author URLs — trigger: analysts ask to record/share verdicts

### Future Consideration (v2+)
- [ ] Cross-author (ring) duplicate detection — defer: needs batch + dedup mature; heaviest client compute
- [ ] Tunable-threshold settings panel for all heuristics — defer: ship sensible defaults first, expose knobs once analysts disagree with them
- [ ] Secondary "events referencing this id" lookup — defer: tangential to spam judgment

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| API access / error & readiness handling | HIGH | MEDIUM | P1 |
| npub/hex normalization | HIGH | LOW | P1 |
| Single-author drill-down shell | HIGH | MEDIUM | P1 |
| Timeline + window-size indicator | HIGH | LOW | P1 |
| Near-duplicate content (Signal 2) | HIGH | HIGH | P1 |
| Posting-rate / burst (Signal 1) | HIGH | MEDIUM | P1 |
| Tag/mention aggregation (Signal 3) | HIGH | MEDIUM | P1 |
| Kind histogram + raw inspector (Signal 4) | MEDIUM | LOW | P1 |
| Corpus stats dashboard (polled) | MEDIUM | LOW | P1 |
| Batch import + triage table | HIGH | MEDIUM | P2 |
| Per-author spam-score rollup | HIGH | MEDIUM | P2 |
| Near-dup clustering | MEDIUM | HIGH | P2 |
| Burst sparkline / mention fan-out | MEDIUM | MEDIUM | P2 |
| Evidence export / deep-link | MEDIUM | LOW | P2 |
| Cross-author (ring) dedup | HIGH | HIGH | P3 |
| Tunable-threshold settings panel | MEDIUM | MEDIUM | P3 |

**Priority key:** P1 = launch · P2 = add after validation · P3 = future.

## Competitor / Prior-Art Feature Analysis

| Capability | Mainstream moderation tools | Nostr spam projects | Our approach |
|------------|-----------------------------|---------------------|--------------|
| Behavioral pattern detection (rapid posting, mass tagging) | Built-in, automated flags | Some (rate/burst rules) | Surface rate/burst + mention fan-out as *analyst-read* signals, not auto-flags |
| Duplicate/near-dup detection | Structural fingerprints, often server-side | MinHash/shingling in dedup pipelines | Client-side shingling + Jaccard≥0.8; brute-force at window scale, no LSH needed |
| Spam classification | AI/ML verdicts (NaiveBayes, deep learning, e.g. ~98% on Nostr datasets) | NIP-56 + NaiveBayes/FastAI models | Deliberately NOT a classifier — transparent signals, human verdict (anti-feature) |
| Triage queue / escalation | Dashboards, routing, audit logs | n/a | Batch triage table sorted by transparent spam-score |
| Acting on verdict (ban/label/report) | Core feature | NIP-56 reports | Out of scope — read-only API; export evidence instead |

## Sources

- [MinHash / Jaccard / LSH near-duplicate detection — Brenndoerfer](https://mbrenndoerfer.com/writing/minhash-algorithm-jaccard-similarity-lsh-deduplication) (MEDIUM)
- [Practical near-dup detection (shingle size, thresholds) — Kashnitsky](https://yorko.github.io/2023/practical-near-dup-detection/) (MEDIUM — char-4 shingles, 0.8 threshold for short text)
- [Near-Duplicate & Exact Duplicate Detection — apxml](https://apxml.com/courses/how-to-build-a-large-language-model/chapter-7-data-cleaning-preprocessing-pipelines/near-duplicate-exact-duplicate-detection) (MEDIUM — ≥0.8 near-dup threshold)
- [Nostr spam detection (NaiveBayes / labeled dataset) — blakejakopovic](https://github.com/blakejakopovic/nostr-spam-detection) (LOW — confirms spam/ham signal categories exist; feature details in notebooks)
- [Nostr NIP-56 spam detection — KiPSOFT](https://github.com/KiPSOFT/nostr-spam-detection) (LOW)
- [Content moderation patterns — eugeneyan](https://eugeneyan.com/writing/content-moderation/) (LOW)
- [Social media moderation tool features (behavioral pattern detection, triage) — getstream](https://getstream.io/blog/social-media-moderation/) (LOW)
- `contract.md` v1.0 (code-verified 2026-06-23) — authoritative API capabilities, limits, semantics (HIGH)
- `.planning/PROJECT.md` — scope, constraints, key decisions (HIGH)

---
*Feature research for: author-centric Nostr spam-investigation frontend*
*Researched: 2026-06-24*

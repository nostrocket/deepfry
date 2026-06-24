# Pitfalls Research

**Domain:** Read-only, author-centric spam-investigation frontend (React 19 + Vite + TS + urql + GraphQL Codegen + nostr-tools) over the LMDB2GraphQL read-only GraphQL lens
**Researched:** 2026-06-24
**Confidence:** HIGH (every pitfall is derived from code-verified `contract.md` v1.0 and version-verified `STACK.md`; failure modes are deterministic properties of the contract, not speculation). MEDIUM only where a pitfall depends on heuristic interpretation (window-honesty, timestamp poisoning).

> **Phase numbering note:** No `ROADMAP.md` exists yet — this research *informs* it. Phase references below use the FEATURES.md dependency order as the expected roadmap skeleton:
> - **Phase A — Transport/API foundation** (urql client, dev proxy, `/ready` gating, `errors[]` handling, cursor pagination, codegen) — built first, everything depends on it.
> - **Phase B — Input & identity** (npub/hex normalization, validation, batch import parsing).
> - **Phase C — Single-author drill-down + window indicator** (the four signals share one fetched set).
> - **Phase D — Batch triage** (`latestPerAuthor`, chunking, body-size).
> - **Phase E — Stats dashboard + polling.**
> - **Phase 0 — Project scaffold** (npm install / version pins / codegen wiring), which precedes all of the above.

---

## Critical Pitfalls

### Pitfall 1: GraphQL errors arriving on HTTP 200 (the "happy 200" trap)

**What goes wrong:**
The transport returns `200 OK` for any query that *reaches the resolver*, even when the body contains `errors[]` and `data: null` (or partial data). Code that treats `res.ok` / `res.status === 200` as success silently renders blank panels, `undefined` analytics, or a crash on `data.events.events` when `data` is null. `INVALID_CURSOR`, `TOO_MANY_AUTHORS`, and validation messages (`"kind must be a non-negative integer"`, `"since must be <= until"`) all arrive this way.

**Why it happens:**
It violates REST intuition. Developers wire `if (!res.ok) throw` and assume a 200 means a usable payload. urql actually surfaces this correctly (`result.error.graphQLErrors`), but hand-rolled `fetch` helpers (the contract's own example `fetch` snippet only checks `json.errors[0].message`) and "just get it working" code skip it.

**How to avoid:**
Use urql and **always branch on `result.error` before reading `result.data`** — never assume `data` is populated on a non-erroring fetch. Centralize this in one `useQuery`-wrapping hook / `errorExchange` so no call site reads `data` without first checking error state. Add a typed error-code discriminator that reads `extensions.code` and maps `INVALID_CURSOR` / `TOO_MANY_AUTHORS` / `null-code internal error` / validation strings to distinct UI states. Treat partial-data responses (`data` present **and** `errors` present) explicitly.

**Warning signs:**
Blank or `NaN`/`undefined` analytics with no error shown; a console `TypeError: cannot read 'events' of null`; the network tab shows 200 but the UI is empty; bugs that only reproduce against real data (a malformed cursor) not against mocks.

**Phase to address:** Phase A (transport foundation) — this is the keystone; build the error-aware client before any feature.

---

### Pitfall 2: Treating analytics over a small fetched window as global truth (the window-honesty trap)

**What goes wrong:**
Every signal (dup ratio, burst rate, kind histogram, mention fan-out) is computed *only over events already fetched into the browser* — bounded by `limit`/`perAuthor` ≤ 500 and whatever pages the analyst pulled. The UI shows "0 duplicates" or "no bursts" over 50 fetched events when the author posted 5,000, and an analyst reads it as exoneration. This produces **false negatives that get people un-flagged** — the most dangerous failure for a forensic tool.

**Why it happens:**
Client-side analytics naturally describe "the data I have." Without a denominator the numbers *look* authoritative. The fix is non-obvious and easy to defer ("add it later"), but by then verdicts have been formed on misleading screens.

**How to avoid:**
Make the **window-size indicator a first-class, non-removable element of every analytic surface**, not a footnote — show "computed over N events · hasMore: true/false · createdAt range [oldest…newest]". Phrase verdicts as ratios with explicit denominators ("32 of 50 fetched"). Never display "0 duplicates" — display "0 duplicates in 50 fetched (hasMore: true)". Ship the window indicator *in the same phase as the first signal*, never after. Make "load more / fetch deeper" recompute analytics live so the analyst can grow the window when inconclusive.

**Warning signs:**
Any signal label without an accompanying count; a "clean"/"spam" framing that doesn't reference how many events were examined; analysts forming verdicts on the first page; demo screenshots that show percentages with no N.

**Phase to address:** Phase C (drill-down) — the window indicator must ship with the *first* signal. Flag this phase for deeper UX research: the honesty framing is the project's core integrity property.

---

### Pitfall 3: Author-claimed `createdAt` poisoning burst/rate detection

**What goes wrong:**
`createdAt` is **what the author asserted**, not when strfry received the event. A spammer can fabricate timestamps: spread 5,000 posts across fake hours to *flatten* a real burst, or backdate to evade a `since` window. Rate/regularity/inter-post-gap analysis built on `createdAt` can be silently spoofed, so "no burst" is **not** exoneration, and a fabricated metronome could even frame a third party.

**Why it happens:**
`createdAt` is the only timestamp the API exposes (there is no relay-receive time / `levId` is opaque and not a wall clock). It's the obvious field for a timeline. Developers treat it as ground truth because in honest data it is — the adversarial case is exactly the one the tool exists for.

**How to avoid:**
Treat burst signals as **asymmetric**: burst *present* = suspicious; burst *absent* ≠ clean. Surface a persistent "timestamps are author-claimed and can be forged" caveat directly adjacent to every rate/burst chart, not buried in a help page. Never let the spam-score rollup treat "low burst score" as a *negative* (exonerating) contribution — only let positive signals add. Consider showing `levId`-derived insertion ordering as a cross-check where it disagrees with `createdAt` ordering (events whose claimed time contradicts insertion order are themselves a tell).

**Warning signs:**
A spam-score that *drops* when burst is absent; a timeline presented as authoritative chronology; rate charts with no provenance caveat; using `since`/`until` filters as if they bound real-world time.

**Phase to address:** Phase C (Signal 1 — burst/rate). MEDIUM-confidence interpretive area; flag for heuristic review.

---

### Pitfall 4: CORS surprises when run without the Vite dev proxy

**What goes wrong:**
The backend sends **no CORS headers** (the only HTTP middleware is the body-size limit). A browser app on `:5173` calling `http://127.0.0.1:8080/graphql` directly is blocked by same-origin policy — preflight fails, `Access-Control-Allow-Origin` is absent. Symptom: every query fails with an opaque browser CORS error and a network entry that never resolves, with *nothing* in the server logs (the request was blocked before reaching it).

**Why it happens:**
Two ways in: (a) someone hard-codes the absolute API URL (`http://127.0.0.1:8080/graphql`) in the urql client instead of the relative `/graphql`, bypassing the proxy; (b) the app is served from any origin other than the Vite dev server (a static build opened via `file://`, a different port, a teammate's `vite preview` without the proxy, or a future "just deploy it" attempt). The proxy only rewrites requests that go through Vite's dev server to a *relative* path.

**How to avoid:**
The urql client `url` **must** be the relative `/graphql` (never absolute) so the browser always sees one origin and Vite reverse-proxies server-to-server. Configure `server.proxy` for `/graphql` (and `/ready`, `/health`) with `changeOrigin: true`. Add a startup runtime check: if `window.location` origin ≠ the dev server, or a fetch to `/graphql` returns a CORS/network error, show a loud "are you running through `vite dev` with the proxy configured?" banner rather than silent failures. Document in README that there is no production/static-host path in v1 (PROJECT.md scopes it out) — anyone serving the built bundle cross-origin will hit this.

**Warning signs:**
Browser console `blocked by CORS policy`; network requests stuck "pending"/"(failed)" with empty response; works in `curl`/codegen (Node, no CORS) but not in the browser; works in `vite dev` but breaks on `vite preview`/static serve.

**Phase to address:** Phase 0 (scaffold — proxy + relative-URL client) and reinforced in Phase A (transport). Cross-cutting; verify the relative-URL invariant in code review.

---

### Pitfall 5: Hex-only API vs npub input — silent non-match on bad normalization

**What goes wrong:**
The API matches `ids`/`authors`/`e`/`p`-tag values as **64-char lowercase hex** against raw byte index keys. Mixed-case hex, wrong length, an `npub`/`note`/`nprofile` passed through un-decoded, whitespace, a `0x` prefix, or an uppercase pasted hex **does not error — it simply returns zero matches**. The analyst sees an empty drill-down and concludes "this author has no events / isn't in the corpus" when in fact the query never matched. A false "clean" verdict from a typo.

**Why it happens:**
Humans paste `npub1...` from Nostr clients; the API speaks hex only. The conversion (`nip19.decode`) is easy to get *mostly* right and silently wrong in edge cases (uppercase hex, `nprofile` which decodes to an object not a bare hex string, trailing newline from a file paste). Because the API returns an empty result rather than an error for a non-matching identifier, validation gaps are invisible.

**How to avoid:**
Centralize a single `toHexPubkey(input): {ok: hex} | {error}` normalizer used by *both* single-entry and batch import. Steps: trim; if it starts with `npub`/`nprofile`/`note`, `nip19.decode` and extract the hex (handle `nprofile` → `.data.pubkey`); else require `/^[0-9a-f]{64}$/` after **lowercasing** (and reject if the pre-lowercase form had uppercase only *after* deciding it isn't a checksummed bech32). **Reject and surface** anything that doesn't normalize — never pass it to the query. Distinguish in the UI: "invalid identifier (couldn't parse)" vs "valid identifier, zero events in corpus" — these are completely different conclusions and must look different.

**Warning signs:**
Empty drill-down for a pubkey the analyst is sure is active; the same pubkey works in one client's npub but not when pasted as hex from another; batch rows silently dropped; no distinction in the UI between "parse failed" and "no events".

**Phase to address:** Phase B (input & identity). The "couldn't parse" vs "zero matches" distinction is a verification criterion for this phase.

---

### Pitfall 6: Opaque cursor mishandling / hand-building cursors / no INVALID_CURSOR recovery

**What goes wrong:**
`endCursor` is opaque base64 of internal sort keys. Three failure modes: (a) code parses, slices, increments, or reconstructs a cursor → `INVALID_CURSOR`; (b) the cursor from one `filter` is reused after the analyst changes the filter (e.g. adds a kind), giving meaningless/empty pages — the contract warns cursors aren't meaningful across different filters; (c) an `INVALID_CURSOR` is hit and the loop dies or spins instead of restarting pagination from page 1.

**Why it happens:**
Cursors *look* like data you can manipulate. Offset-pagination habits (`after = after + limit`) leak in. Filter state and pagination state are often stored separately, so changing the filter without resetting the cursor is easy. The recovery path (`extensions.code === 'INVALID_CURSOR'` → drop cursor, restart) is an edge case rarely tested.

**How to avoid:**
Treat `endCursor` as an opaque token: store it, pass it back verbatim as `after`, **never inspect or build it**. Bind cursor state to its `filter` — when `filter` changes, reset `after` to `null` (model "filter + cursor" as one unit; changing filter invalidates cursor by construction). Stop pagination on `hasMore === false` / `endCursor === null` (use both, consistently). Implement explicit `INVALID_CURSOR` recovery: drop the cursor, restart from page 1, optionally log it as a client bug. Keep `limit`/`filter` constant across a pagination run.

**Warning signs:**
`INVALID_CURSOR` errors in logs; pagination that returns empty or duplicate pages after a filter change; any code that does string ops on `endCursor`; infinite "load more" that never sets `hasMore: false`.

**Phase to address:** Phase A (cursor handling in the transport layer), exercised by Phase C ("load more / fetch deeper"). Verify by changing a filter mid-investigation and confirming pagination resets.

---

### Pitfall 7: Silent `limit` clamping + 256 KiB body cap + TOO_MANY_AUTHORS on big lists

**What goes wrong:**
Two silent and one loud trap. (a) **Silent `limit` clamp**: `events.limit` and `latestPerAuthor.perAuthor` are clamped to `[1,500]` with no error — request `limit: 5000` and you silently get 500, so "fetch everything in one go" code thinks it has the whole set and the window-honesty trap (Pitfall 2) compounds. (b) **256 KiB body cap → `413`**: a big `authors`/`ids` array (batch import) blows the request body limit; `413` is a transport status, not a GraphQL error, so `errors[]`-only handling misses it. (c) **`TOO_MANY_AUTHORS`**: `latestPerAuthor` with >1000 authors returns a coded GraphQL error.

**Why it happens:**
The clamp is invisible by design (no error), so nobody notices the missing events. Batch import naturally produces large arrays; 1000 hex authors at 64 chars + JSON overhead approaches the body cap *before* you even hit the 1000-author count limit, so `413` can fire below `TOO_MANY_AUTHORS`. Developers handle one limit and forget the other interacts.

**How to avoid:**
Never request more than 500 per page; treat 500 as a hard ceiling in code, and *assume there may be more* (drive completeness via pagination + `hasMore`, never via a big `limit`). For batch: chunk the `authors` list at ≤1000 for `TOO_MANY_AUTHORS` **and** estimate request body size (≈ authors × ~70 bytes + query) and split further to stay well under 256 KiB — chunk on whichever limit binds first (body size often binds before 1000 authors). Handle `413` in the transport layer alongside `503` (HTTP-status branch) and `errors[]` (body branch). Merge chunked `latestPerAuthor` results client-side by `author`.

**Warning signs:**
Analytics that plateau at exactly 500 events; "complete" feeds that are missing recent items; `413` on large batch imports; `TOO_MANY_AUTHORS` when importing a file; results that change when you split a batch (proof you were truncated).

**Phase to address:** Phase A (clamp awareness, `413` handling), Phase D (batch chunking on both limits). Verify by importing >1000 authors and a list large enough to exceed 256 KiB.

---

### Pitfall 8: 503 startup-readiness gating ignored

**What goes wrong:**
`POST /graphql` returns `503` until startup gates pass (LMDB open, `dbVersion == 3`, endianness check, comparator self-check). An app that blasts queries on boot gets `503`s, and if `503` is treated as a generic failure the analyst sees "API error" on launch and assumes the tool is broken, when it just needed a moment.

**Why it happens:**
`503` is a transport status, easy to lump into "request failed." The readiness endpoint (`GET /ready`) is separate from liveness (`GET /health`) and the distinction (process-up vs can-query) is subtle. Boot-order races are intermittent and don't show in dev once the backend is already warm.

**How to avoid:**
Gate the first queries on `GET /ready` returning `200` (proxy `/ready` through Vite too). Treat any `503` from `/graphql` as "not ready, retry with backoff" — distinct UI state ("connecting to relay…") not "error". Add bounded retry-with-backoff in the transport layer. Don't poll `/ready` aggressively; a few seconds of backoff is fine.

**Warning signs:**
"API error" on cold start that resolves on refresh; intermittent failures only when backend and frontend start together; no distinct "connecting"/"not ready" UI state.

**Phase to address:** Phase A (readiness gating + backoff in transport). Verify by starting the frontend before the backend is ready.

---

### Pitfall 9: Over-fetching `raw` / breaching depth-12 / complexity-2000 limits

**What goes wrong:**
`raw` is the byte-exact stored JSON and can be large; pulling `raw` for every row in a 500-event page bloats payloads and slows the UI. Separately, the schema enforces **depth ≤ 12** and **complexity ≤ 2000**, and *many aliased top-level queries in one request count against complexity* — a "fetch all signals in one big aliased query" or a polling loop that batches many operations can trip the complexity limit and return a (200-wrapped) error.

**Why it happens:**
Codegen makes it trivial to over-select fields ("just grab everything"). `raw` looks like a normal string field. Complexity limits are invisible until you cross them, and aliasing multiple `events`/`latestPerAuthor` calls into one document (a natural batching optimization) silently accumulates cost.

**How to avoid:**
Select only rendered fields per view; **lazy-fetch `raw` on demand** (Pitfall: the raw inspector should fire a separate query for a single event's `raw`, not include it in list queries). Keep query documents shallow and single-purpose; avoid stuffing many aliased top-level calls into one request — prefer separate operations (urql dedups and caches them). Budget complexity mentally: page-of-500 with a handful of scalar fields is cheap; nested + aliased + `raw` is where you approach 2000. If you see a complexity error, split the document.

**Warning signs:**
Sluggish rendering / large response sizes correlated with `raw` in the selection set; a complexity/depth error after adding a "fetch everything" batched query; memory growth in the SPA from holding `raw` for hundreds of events.

**Phase to address:** Phase C (drill-down field selection, lazy `raw` for Signal 4 inspector). Verify the list queries do not include `raw`.

---

### Pitfall 10: `latestPerAuthor` cost blowup (authors × perAuthor)

**What goes wrong:**
`latestPerAuthor` cost ≈ `authors.length × perAuthor` index scans. A "load it all" triage of 1000 authors × `perAuthor: 500` is ~500,000 scans plus a huge payload — slow, and likely to brush the body cap and complexity limit. Even within the hard caps, an unconsidered `perAuthor` makes batch triage crawl.

**Why it happens:**
The two arguments multiply, but each individually looks "within limits" (1000 ≤ 1000, 500 ≤ 500). Developers set `perAuthor` to a comfortable "see plenty of events" value for triage when triage only needs 1–3.

**How to avoid:**
Keep `perAuthor` deliberately tiny for triage (3–10; the contract notes a follow-feed wall wants 1–3). Deepen per-author only on drill-down (Phase C), which fetches one author at a time via `events`. Surface the cost model in the batch UI (e.g. "this will scan ~N events") so analysts don't crank `perAuthor`. Remember empty groups are omitted — match results by `author`, never zip by index, or you'll misattribute one author's events to another after chunking/merging.

**Warning signs:**
Multi-second batch triage; large payloads; triage that fetches far more events than the table displays; `result.length !== authors.length` causing index-misaligned rows.

**Phase to address:** Phase D (batch triage). Verify `perAuthor` defaults small and results are matched by `author` key.

---

### Pitfall 11: GraphQL Codegen pinned to `graphql@17` instead of `graphql@16`

**What goes wrong:**
`graphql@17.0.0` shipped 2026-06-15. `@graphql-codegen/client-preset@6` peers cap at `graphql ^16`, and `graphql-request@7` at `14–16`. Installing `graphql@17` (what `npm install graphql` grabs by default now) produces peer-dependency warnings and subtle codegen breakage — the typed client either fails to generate or generates wrong, undermining the whole "typed from introspection" value proposition. urql itself is unaffected (it bundles `@0no-co/graphql.web`), which masks the problem at runtime and makes it look like a codegen-only mystery.

**Why it happens:**
`npm install graphql` resolves to latest (17). The runtime client (urql) works fine without `graphql`, so the only thing that breaks is the build-time codegen toolchain — a separate concern people don't immediately connect. This is the single highest-value version trap in STACK.md.

**How to avoid:**
Pin `graphql@16.14.2` explicitly in `package.json` (`"graphql": "16.14.2"`, not `^17`, not `*`). Add an install-time guard or a CI/`postinstall` check that fails if `graphql`'s resolved version is ≥17. Pin the rest per STACK.md: TypeScript 5.9.x (not 6.0), `@graphql-codegen/cli@7`, `client-preset@6`, urql@5 + `@urql/core@6`. Use `client-preset` (typed `graphql()`), not the legacy `typescript-urql` hooks plugin.

**Warning signs:**
`npm install` peer-dependency warnings mentioning `graphql`; codegen errors or empty/incorrect generated types in `src/gql/`; `graphql` resolving to 17.x in `package-lock.json`.

**Phase to address:** Phase 0 (scaffold / dependency pinning). Verify `package-lock.json` resolves `graphql` to 16.x and codegen produces typed output.

---

### Pitfall 12: 64-bit `kind`/`createdAt` treated as plain JS `Number` without guard

**What goes wrong:**
`kind` and `createdAt` are **64-bit** on the wire (mapped to a 64-bit scalar to avoid truncation) but typed as GraphQL `Int`, so codegen emits `number`. Real Nostr data stays within JS safe-integer range (`2^53`), so this is fine *today* — but a maliciously crafted or future event with a `kind`/`createdAt` near or beyond `2^53` would silently lose precision in JS `Number`, corrupting kind histograms (Signal 4), time math (Signal 1), and `since`/`until` comparisons. For a *spam/adversarial* tool specifically, "an attacker can craft a value that breaks your math" is a realistic concern, not hypothetical.

**Why it happens:**
The wire type says 64-bit; the GraphQL type says `Int`; codegen says `number`. Everyone uses `number` because it works for honest data. The truncation is silent (no error, just a wrong value).

**How to avoid:**
For v1, `number` is acceptable per STACK.md — but add a **defensive sanity check** on ingest: flag/clamp any `createdAt` or `kind` outside `[0, Number.MAX_SAFE_INTEGER]` rather than silently computing on it (an out-of-range value is itself a spam tell worth surfacing). Don't do arithmetic that assumes a sane range without bounds-checking. If real out-of-range values ever appear, the documented fix is a codegen `config.scalars` mapping to a bigint-safe type — keep that escape hatch noted. Treat timestamps as seconds (not ms) consistently to avoid a separate `×1000` class of bugs.

**Warning signs:**
A `createdAt` that renders as a nonsensical date; a kind bucket with an absurd key; values near `9007199254740991` in raw data; `Date(createdAt)` off by a factor of 1000 (seconds-vs-ms confusion).

**Phase to address:** Phase C (Signals 1 & 4 do the integer math) with the bounds-check; Phase 0 notes the `number` decision and the bigint escape hatch. LOW likelihood for honest data, but cheap to guard in an adversarial tool.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Hand-rolled `fetch` instead of urql + central error handling | One less dependency to learn early | Re-implements caching, dedup, and (critically) the `errors[]`-on-200 / `extensions.code` branching at every call site → Pitfall 1 everywhere | Never — urql is already the chosen client; use it from line one |
| Hard-coding `http://127.0.0.1:8080/graphql` in the client "to get it working" | Skips proxy setup for a quick test | CORS breakage (Pitfall 4) the moment it runs in a real browser; encourages an absolute-URL pattern that later defeats the proxy | Never in committed code — relative `/graphql` is an invariant |
| Big `limit`/`perAuthor` to "get all the data at once" | One query instead of a pagination loop | Silent clamp to 500 (Pitfall 7) → incomplete analytics presented as complete (Pitfall 2); cost blowup (Pitfall 10) | Never — completeness must come from pagination + `hasMore` |
| Window-size indicator "added later" | Ship a signal faster | Verdicts formed on misleading screens; reworking every analytic to thread the denominator through | Never — ship the indicator *with* the first signal |
| Including `raw` in list queries for convenience | One query covers list + inspector | Payload bloat, memory growth, complexity pressure (Pitfall 9) | Only for tiny windows during a spike; lazy-fetch in real impl |
| `^`/`*` version ranges on `graphql` | Less pinning ceremony | Picks up `graphql@17` → codegen breakage (Pitfall 11) | Never for `graphql`; exact-pin it |
| Treating "0 matches" and "couldn't parse" identically | Simpler input handling | False "clean" verdicts from typos (Pitfall 5) | Never — these are different conclusions |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| LMDB2GraphQL transport | Trusting HTTP 200 = success | Always inspect `errors[]` and `extensions.code` even on 200 (Pitfall 1) |
| LMDB2GraphQL readiness | Querying immediately on boot | Gate on `GET /ready`; treat `503` as "retry with backoff" (Pitfall 8) |
| LMDB2GraphQL pagination | Building/parsing/offsetting cursors; reusing across filters | Opaque token, pass verbatim, reset on filter change, recover from `INVALID_CURSOR` (Pitfall 6) |
| LMDB2GraphQL `latestPerAuthor` | Zipping results by index with the requested authors | Empty groups omitted — match by `author` field (Pitfalls 7, 10) |
| Vite dev proxy | Absolute API URL in client bypasses proxy | Relative `/graphql`; proxy `/graphql`,`/ready`,`/health` with `changeOrigin: true` (Pitfall 4) |
| nostr-tools `nip19` | Assuming `decode` always yields a bare hex string | `nprofile`/`nevent` decode to objects; extract `.data.pubkey`; lowercase + length-check hex (Pitfall 5) |
| GraphQL Codegen | `npm install graphql` → 17 | Exact-pin `graphql@16.14.2`; use `client-preset@6` (Pitfall 11) |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| `latestPerAuthor` authors × perAuthor blowup | Multi-second batch triage, huge payloads | Small `perAuthor` (3–10) for triage; deepen only on drill-down | Approaching 1000 authors × high `perAuthor` (e.g. >100) |
| `raw` in list selection sets | Large responses, SPA memory growth | Lazy-fetch `raw` per-event for the inspector only | Windows of hundreds of events with `raw` selected |
| O(n²) near-dup over too-large a window | UI jank during dedup compute | Brute-force Jaccard is fine for N≤500; do not exceed window scale; don't reach for MinHash/LSH prematurely | Cross-author batch dedup over thousands of pooled events |
| Aggressive polling of `stats`/`maxLevId` | Needless load, possible complexity pressure if batched | Poll on a sane interval (seconds, not ms); single small query | Sub-second intervals or many aliased ops per poll |
| Over-paginating the whole author history | Long "load all" loops, big in-memory event set | Page on demand; recompute incrementally; respect the window-honesty UX (analyst decides depth) | Authors with very large histories |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Rendering `content` as HTML/markdown-exec | XSS from attacker-controlled event content (this is literally a spam/abuse corpus) | Render `content` as plain text only; never `dangerouslySetInnerHTML`; escape everything |
| Rendering `raw` / tag values without escaping | Same XSS vector via raw JSON or crafted tag values | Display in a text/`<pre>` context with escaping; treat all event fields as hostile input |
| Re-verifying signatures client-side to "be safe" | Wasted compute + false confidence; not the tool's job | Trust strfry's ingest verification (contract §5: do not re-verify); state "single-relay view" |
| Exposing the unauthenticated API beyond loopback | API has no auth, full introspection, GraphiQL — anyone reachable can query everything | Keep `127.0.0.1` bind / loopback; v1 is local-dev only (PROJECT.md); any wider exposure needs a gateway (out of scope) |
| Treating author-claimed `createdAt` as forensic ground truth | False exoneration / framing via forged timestamps | Asymmetric burst interpretation + provenance caveat (Pitfall 3) |
| Acting on an out-of-range 64-bit value without bounds-check | Silent precision loss corrupting analytics on crafted events | Bounds-check `kind`/`createdAt` against safe-integer range (Pitfall 12) |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Signals shown without a window denominator | Analyst forms false "clean" verdict on partial data | Always show "N events · hasMore · time range" beside every signal (Pitfall 2) |
| "Invalid identifier" and "no events found" look the same | Typo reads as "author is clean / absent" | Two distinct UI states with distinct copy (Pitfall 5) |
| Burst/rate chart with no provenance caveat | Analyst trusts forgeable timestamps as chronology | Persistent "author-claimed, forgeable" note adjacent to the chart (Pitfall 3) |
| Generic "API error" on cold start (`503`) | Analyst thinks the tool is broken | Distinct "connecting to relay…" state with backoff (Pitfall 8) |
| Spam-score as an opaque single number / auto-verdict | Invites false bans + complacency; hides reasoning | Transparent per-signal sub-scores; human stays in the loop (anti-feature in FEATURES.md) |
| Silent truncation at 500 with no "more available" cue | Analyst believes they saw everything | Surface `hasMore` and the 500-page ceiling explicitly (Pitfalls 2, 7) |
| Mismatched batch rows after empty-group omission | One author's events attributed to another | Render rows keyed by `author`, show "0 events" for omitted authors explicitly (Pitfall 10) |

## "Looks Done But Isn't" Checklist

- [ ] **Transport client:** Often missing `errors[]`-on-200 + `extensions.code` branching — verify a malformed cursor and a >1000-author batch both render distinct, non-blank error states.
- [ ] **CORS/proxy:** Often missing the relative-URL invariant — verify the client uses `/graphql` (not absolute) and that `vite preview`/static-serve failure is explained, not silent.
- [ ] **Window honesty:** Often missing the denominator — verify *every* signal surface shows fetched count + `hasMore` + time range.
- [ ] **npub/hex input:** Often missing the "couldn't parse" vs "zero matches" distinction and `nprofile`/uppercase handling — verify both branches and edge identifiers.
- [ ] **Pagination:** Often missing cursor-reset-on-filter-change and `INVALID_CURSOR` recovery — verify changing a filter mid-investigation restarts cleanly.
- [ ] **Limits:** Often missing `413` handling and dual-axis batch chunking — verify a >256 KiB batch and a >1000-author batch both succeed via chunking.
- [ ] **Readiness:** Often missing `/ready` gating + backoff — verify launching frontend before backend shows "connecting" not "error".
- [ ] **`raw` handling:** Often missing lazy-fetch — verify list queries don't select `raw`.
- [ ] **Burst provenance:** Often missing the author-claimed caveat and asymmetric scoring — verify "no burst" never reduces a spam score.
- [ ] **Version pins:** Often missing exact `graphql@16` pin — verify `package-lock.json` and a clean codegen run.
- [ ] **64-bit guard:** Often missing bounds-check — verify an out-of-range crafted `createdAt`/`kind` is flagged, not silently mis-computed.
- [ ] **XSS:** Often missing — verify `content`/`raw`/tag values render as escaped text, never HTML.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| `INVALID_CURSOR` at runtime | LOW | Drop the cursor, restart pagination from page 1; log as client bug; audit for cursor manipulation |
| `graphql@17` installed | LOW | Pin `graphql@16.14.2`, delete `node_modules`/lockfile, reinstall, re-run codegen |
| CORS / absolute-URL leaked into client | LOW | Change client `url` to relative `/graphql`; confirm proxy config; add the origin runtime check |
| Window-honesty added too late | MEDIUM | Retrofit a window-context provider threaded into every analytic component; audit all signal labels for denominators |
| Errors-on-200 not handled (blank panels in prod) | MEDIUM | Centralize an `errorExchange`/wrapper; sweep all call sites to read `error` before `data` |
| Batch truncated by clamp/413/TOO_MANY_AUTHORS | MEDIUM | Implement dual-axis chunking (authors ≤1000 AND body <256 KiB), merge by `author`; re-run affected investigations |
| Verdicts formed on forged timestamps | HIGH | Re-educate on asymmetric interpretation; add provenance caveat + asymmetric scoring; re-review past verdicts that relied on "no burst" |
| Precision loss from 64-bit values | HIGH (if shipped) | Add codegen `config.scalars` bigint mapping; migrate analytics math; re-validate affected histograms |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| 11 — graphql@17 vs 16 pin | Phase 0 (scaffold) | `package-lock.json` resolves `graphql` 16.x; clean codegen output |
| 4 — CORS / relative-URL | Phase 0 + Phase A | Client uses `/graphql`; browser query succeeds via proxy; `vite preview` failure explained |
| 12 — 64-bit number guard | Phase 0 (decision) + Phase C (math) | Out-of-range `createdAt`/`kind` flagged, not mis-computed |
| 1 — errors on HTTP 200 | Phase A (transport) | Malformed cursor + over-limit batch render distinct error states |
| 6 — opaque cursors | Phase A + Phase C | Filter change resets pagination; `INVALID_CURSOR` recovers |
| 7 — clamp / 413 / TOO_MANY_AUTHORS | Phase A + Phase D | >500 limit, >256 KiB, >1000 authors all handled |
| 8 — 503 readiness | Phase A (transport) | Frontend-before-backend shows "connecting", not "error" |
| 5 — hex/npub normalization | Phase B (input) | "couldn't parse" vs "zero matches" distinct; `nprofile`/uppercase handled |
| 2 — window-honesty | Phase C (with first signal) | Every signal shows N + `hasMore` + time range |
| 3 — author-claimed timestamps | Phase C (Signal 1) | Provenance caveat present; "no burst" never lowers spam score |
| 9 — raw / depth / complexity | Phase C (drill-down) | List queries exclude `raw`; no complexity errors |
| 10 — latestPerAuthor cost | Phase D (batch) | Small default `perAuthor`; results matched by `author` |

## Sources

- `contract.md` v1.0 (code-verified 2026-06-23) — authoritative interface: CORS unconfigured (§3), `errors[]`-on-200 + `extensions.code` (§7), opaque cursors + `INVALID_CURSOR` (§6.1, §7), silent `[1,500]` clamp + 256 KiB/`413` + `TOO_MANY_AUTHORS` (§6, §7, §12), `503`/`/ready` gating (§2), depth-12/complexity-2000 + `raw` size (§9, §12), `latestPerAuthor` cost ≈ authors×perAuthor + empty-group omission (§6.2, §8), 64-bit `kind`/`createdAt` (§5, §8), author-claimed timestamps (§8), hex-only/nip19 (§8). **HIGH confidence** (authoritative, code-verified).
- `.planning/research/STACK.md` (2026-06-24) — `graphql@16` vs `@17` codegen pin (the highest-value version trap), urql graphql-version independence, TS 5.9 vs 6.0, lazy `raw`, hand-rolled dedup at window scale. **HIGH confidence** (npm-registry verified).
- `.planning/research/FEATURES.md` (2026-06-24) — window-honesty as the integrity backbone, asymmetric burst interpretation under forgeable timestamps, transparent-score / no-auto-verdict, match-by-author-not-index, dependency ordering. **HIGH confidence** (mapping); **MEDIUM** on heuristic thresholds.
- `.planning/PROJECT.md` (2026-06-24) — scope (local-dev-first, read-only, no auth/CORS infra in v1), constraints, key decisions. **HIGH confidence**.

---
*Pitfalls research for: author-centric Nostr spam-investigation frontend over LMDB2GraphQL*
*Researched: 2026-06-24*

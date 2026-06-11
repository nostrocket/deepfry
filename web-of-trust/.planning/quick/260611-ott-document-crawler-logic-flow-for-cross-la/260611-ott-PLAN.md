---
phase: quick-260611-ott
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - web-of-trust/fable_logic_flow.md
autonomous: true
requirements: [DOC-CRAWLER-FLOW]
must_haves:
  truths:
    - "A reader who has never seen this codebase can reimplement the crawler in another language from the document alone"
    - "Every database interaction is described as a named abstract operation with inputs/outputs/invariants, not as Dgraph/DQL specifics"
    - "The document reflects real current code including phase-06 filter-size per-relay cap detection (chunked sub-REQ loop, NOTICE halving with floor, drainSubscription)"
    - "The document is language- and database-agnostic (no Go syntax, no go-nostr/Dgraph API names presented as required)"
  artifacts:
    - path: "web-of-trust/fable_logic_flow.md"
      provides: "Complete cross-language/cross-database logic flow specification of the crawler"
      min_lines: 250
      contains: "GetStalePubkeys"
  key_links:
    - from: "fable_logic_flow.md storage-operations section"
      to: "pkg/dgraph/dgraph.go operations"
      via: "abstracted named operations (GetStalePubkeys, CountPubkeys, MarkAttempted, AddFollowers, TouchLastDBUpdate, EnsureSchema, ValidatePubkey)"
      pattern: "GetStalePubkeys|MarkAttempted|AddFollowers"
---

<objective>
Produce `web-of-trust/fable_logic_flow.md`: a complete, language- and database-agnostic specification of how the web-of-trust crawler operates, sufficient for a developer to reimplement it in any language against any storage backend.

Purpose: The current implementation is Go + go-nostr + Dgraph. This document decouples the *behavior* from those choices so the crawler can be ported. All storage interactions must be expressed as named abstract operations with semantic contracts (inputs, outputs, invariants), never as Dgraph/DQL details.

Output: A single Markdown file at the web-of-trust module root (`web-of-trust/fable_logic_flow.md`). This is documentation-only — no source code changes.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
</execution_context>

<context>
@.planning/STATE.md

# Read these source files and DERIVE the flow from the real code — do not invent behavior.
# Repo root is /Users/g/git/deepfry; the web-of-trust module is a subdirectory.
# Paths below are relative to the web-of-trust module (working directory).
@pkg/crawler/crawler.go
@cmd/crawler/main.go
@pkg/config/config.go
</context>

<tasks>

<task type="auto">
  <name>Task 1: Extract the crawler's real behavior from source</name>
  <files>web-of-trust/pkg/crawler/crawler.go, web-of-trust/cmd/crawler/main.go, web-of-trust/pkg/config/config.go, web-of-trust/pkg/dgraph/dgraph.go</files>
  <action>
Read the four source files end to end (use Grep + targeted Read for dgraph.go; it is large — only the signatures and doc comments of GetStalePubkeys, collectStale, MarkAttempted, AddFollowers, TouchLastDBUpdate, CountPubkeys, EnsureSchema, ValidatePubkey/isValidHexPubkey, and chunkSlice are needed, not the full DQL bodies).

Build an accurate mental model of every behavior the doc must cover. The following details are confirmed in the current code and MUST be represented faithfully:

- Main loop (cmd/crawler/main.go): context + SIGINT/SIGTERM signal handler that calls cancel(); load config; create dgraph client; optional interactive prompt for a forward relay; create crawler; record starting pubkey count; loop { check ctx.Done; GetStalePubkeys(now - StalePubkeyThreshold, RelayFilterBatchSize); CountPubkeys; if DB empty seed with cfg.SeedPubkey at created_at 0; break if no pubkeys; ReconnectRelays; FetchAndUpdateFollows; MarkAttempted(all queried keys, now); log batch summary }; final report on shutdown; wg.Wait.
- Seed bootstrap: only when total pubkey count == 0; seed pubkey added with kind3CreatedAt 0.
- Stale-pubkey feed is two-phase inside GetStalePubkeys: Phase 1 = uncrawled frontier (pubkeys with no prior attempt), Phase 2 = top up remaining slots with previously-attempted pubkeys older than the threshold, ordered oldest-attempt-first. Output is a map pubkey -> kind3CreatedAt.
- Relay pool: per-relay state = url, connection, alive flag, backoff duration, retryAt time, failures counter (atomic), filterCap (atomic). Constants: initialBackoff 30s, maxBackoff 5m, maxConsecutiveFailures 5.
- Connection lifecycle: on construction connect all relays; relays that fail to connect are dropped and reported via OnConnectFail (which removes them from config). Require at least one relay or fail.
- markRelayDead: close conn, set alive=false, increment failures; if failures >= 5 remove relay permanently via OnConnectFail; otherwise set retryAt = now + backoff and double backoff (capped at maxBackoff). Note current behavior: backoff doubles AFTER scheduling retryAt.
- ReconnectRelays: skip alive relays; skip dead relays whose retryAt is in the future; otherwise reconnect — on success reset backoff to initial, reset failures to 0, RESET filterCap to configured FilterBatchSize (phase-06 WR-03); on failure remove via OnConnectFail. Forward relay reconnect handled separately with its own backoff schedule.
- Forward relay: optional; events are republished to it; on publish failure it is closed, marked dead, and scheduled for backoff retry. Not required for crawl to function.
- Subscription handling (queryRelay): for each alive relay, query kind-3 events for the batch authors. Authors are split into chunks of size filterCap (current per-relay cap). For each chunk: subscribe with filter {kinds:[3], authors:chunk, limit:batch-size}, drain until EOSE, unsubscribe, advance to next chunk. This is the chunked sub-REQ loop.
- Filter-size per-relay cap detection (phase 06): two halving paths, both with floor = 10.
  1. NOTICE-based (handleFilterNotice): when a relay sends a NOTICE whose lowercased text contains both "filter" and "too large", halve filterCap via compare-and-swap loop, never below floor 10; log and return if already at floor.
  2. Connection-drop-on-REQ attribution (D-09): if Subscribe returns an error within 500ms AND the error text contains "not connected" or "failed to write", treat as a filter rejection — if cap > 10, halve it (floor 10), re-queue the same chunk, and retry; if cap already at floor 10, mark the relay dead (D-10) by returning a transport error.
- drainSubscription: reads events from the subscription; on each event checks signature-independent forwarding happens in the caller, so drain only forwards events to the events channel (guarding against blocking on a cancelled context); returns nil on EOSE, ctx.Err() on external cancellation, and a transport error if the subscription's own context is done (connection dropped). Caller owns Unsub() per chunk.
- Event handling (FetchAndUpdateFollows event loop): validate author pubkeys before building filter (ValidatePubkey: 64 lowercase hex chars). Query all alive relays concurrently, fan in via channels. For each received event: dedup by event ID; verify signature (CheckSignature) and skip on failure; forward event to forward relay; record pubkey as "had event"; VERSION CHECK — if event.created_at <= the stored kind3CreatedAt for that pubkey, do NOT rewrite the follow list, instead call TouchLastDBUpdate (mark we saw it, keep newer data) and continue; otherwise call the follow-list update operation. Errors classified as subscriptionError vs transportError; transport errors mark the relay dead; context-cancellation errors are ignored. Relay timeout (deadline exceeded) does NOT abort — already-received events are still processed.
- p-tag parsing + dedup (updateFollowsFromEvent): iterate event tags; for tags where tag[0]=="p" and len>=2, validate tag[1] as a pubkey, collect into a set (deduping); count duplicates; pass the unique follow set to the storage write operation. The write completely REPLACES the follower's follow list (kind-3 is a replaceable event).
- Follow-list chunking for large lists: the storage write operation (AddFollowers) internally batches the followee set into windows (chunkSlice; current window size is an internal constant) to stay under a message-size cap, inside a single all-or-nothing transaction. The crawler does NOT chunk follow-list writes itself — describe chunking as a property of the abstract write operation.
- Graceful shutdown: SIGINT/SIGTERM cancels the root context; the main loop breaks at its next ctx.Done check or when an in-flight operation returns a context error; connections and DB client are closed; a final report (starting vs ending pubkey count, new nodes, runtime) is printed; background WaitGroup is awaited.
- Config (pkg/config/config.go): relay_urls, dgraph_addr, pubkey (seed; accepts npub or hex), timeout (default 30s), stale_pubkey_threshold (default 86400s), relay_filter_batch_size (default 100; this is BOTH the stale-batch limit AND the initial per-relay filterCap), forward_relay_url. Config persists changes (forward relay save; relay removal). npub seeds are decoded to hex.

Do not write the document yet. The goal of this task is a complete and correct extraction; if any behavior above conflicts with the actual code as read, the CODE is authoritative — note the discrepancy and document the real behavior.
  </action>
  <verify>
    <automated>test -f web-of-trust/pkg/crawler/crawler.go &amp;&amp; grep -q "handleFilterNotice" web-of-trust/pkg/crawler/crawler.go &amp;&amp; grep -q "drainSubscription" web-of-trust/pkg/crawler/crawler.go &amp;&amp; grep -q "filterCap" web-of-trust/pkg/crawler/crawler.go</automated>
  </verify>
  <done>All four source files have been read; the behaviors listed in the action (main loop, relay pool, subscription/filter-cap, event handling, chunking, shutdown, config) are understood and verified against the real code, with any discrepancies noted.</done>
</task>

<task type="auto">
  <name>Task 2: Write the language- and database-agnostic logic flow document</name>
  <files>web-of-trust/fable_logic_flow.md</files>
  <action>
Write `web-of-trust/fable_logic_flow.md` using the Write tool. (Confirm the module root first with `git rev-parse --show-toplevel`: repo root is /Users/g/git/deepfry, so the file path is /Users/g/git/deepfry/web-of-trust/fable_logic_flow.md — the web-of-trust module directory, NOT the repo root.)

Hard constraints on the document:
- Language-agnostic: describe behavior in prose, pseudocode, and diagrams. No Go syntax. Do NOT present go-nostr or Dgraph API names as requirements. You MAY mention "the reference implementation uses Go + go-nostr + Dgraph" once in an intro note, but the body must describe portable concepts (e.g. "a Nostr relay WebSocket subscription", "a storage backend operation").
- Database-agnostic: EVERY storage interaction is a named abstract operation with a semantic contract. Do not describe DQL, upserts, nquads, transactions-as-Dgraph, or UIDs. Describe them as backend-neutral guarantees (e.g. "atomic", "idempotent", "must enforce uniqueness on pubkey").

Structure the document with these sections:

1. **Overview & Purpose** — what the crawler does (continuously expand a pubkey follow-graph by fetching kind-3 contact lists), the core value (expand, not just refresh), and the reference-stack note.
2. **Domain Concepts** — pubkey (64 lowercase hex chars), kind-3 contact-list event, follow edge, the follow-graph, "stale" vs "frontier" vs "aged" pubkeys, kind3CreatedAt (event version), last_attempt, last_db_update.
3. **Storage Operations (the database abstraction)** — the centerpiece. A subsection per operation, each with Inputs / Outputs / Invariants / Notes. Cover at minimum:
   - `EnsureSchema()` — ensures storage can hold profiles with pubkey (unique), follow edges, kind3CreatedAt, last_attempt, last_db_update.
   - `CountPubkeys() -> int` — total known pubkeys.
   - `GetStalePubkeys(olderThan, limit) -> map<pubkey, kind3CreatedAt>` — two-phase selection (uncrawled frontier first, then aged-out previously-attempted, oldest first), bounded by limit. Specify the invariant that frontier pubkeys (never attempted) take priority.
   - `MarkAttempted(pubkeys[], timestamp)` — stamp last_attempt; idempotent; invalid pubkeys are recovered (lowercased) or purged so they stop re-entering the frontier.
   - `AddFollowers(signerPubkey, kind3CreatedAt, followSet)` — REPLACE the signer's entire follow list atomically; version guard (no-op if incoming kind3CreatedAt <= stored); creates the signer and any unknown followees as nodes; MUST internally batch large follow sets to respect any backend message-size limit; all-or-nothing.
   - `TouchLastDBUpdate(pubkey)` — record that we saw the pubkey this cycle without changing its follow data (used when the incoming event is not newer).
   - `ValidatePubkey(pubkey) -> ok` — exactly 64 lowercase hex chars.
   State explicitly: any backend (SQL, graph, KV, document) can implement these as long as the contracts and invariants hold.
4. **Configuration** — the config keys and their roles, especially that relay_filter_batch_size is dual-purpose (stale-batch size AND initial per-relay filter cap), the stale threshold, timeout, seed pubkey (npub or hex), forward relay. Note config is persisted and self-healing (auto-created with defaults).
5. **Main Crawl Loop** — step-by-step flow including seed bootstrap (only when DB empty), the per-iteration sequence (select stale -> reconnect -> fetch -> mark attempted -> report), and termination conditions. Include a numbered-step or flowchart representation.
6. **Relay Pool Management** — per-relay state model; connection lifecycle (connect-all-at-startup, drop-and-deconfigure failures); alive/dead transitions; exponential backoff (initial 30s, cap 5m, double on each failure) and retry scheduling (retryAt); failure threshold (5 consecutive -> permanent removal); reconnection (resets backoff, failures, AND filter cap). Cover the forward relay's separate lifecycle.
7. **Subscription Handling** — the chunked sub-REQ loop: split batch authors into chunks of the current per-relay filter cap, subscribe per chunk for kind-3, drain to EOSE, unsubscribe, advance. Then **Filter-size per-relay cap detection** as its own subsection: (a) NOTICE-based halving — "filter" + "too large" in a NOTICE halves the cap with floor 10; (b) connection-drop-on-REQ attribution — a drop within 500ms with "not connected"/"failed to write" is treated as filter rejection: halve + re-queue chunk, or mark relay dead if already at the floor. Explain WHY (relays have undocumented per-filter author limits; this learns each relay's cap at runtime).
8. **Event Handling** — concurrent fan-in from all alive relays; dedup by event ID; signature verification (reject on failure); forwarding; the kind3CreatedAt version check (skip-but-touch when not newer); p-tag parsing with pubkey validation and follow-set dedup; the replace-whole-follow-list semantics; error classification (subscription vs transport; transport marks relay dead; timeout does not abort — process what arrived).
9. **Follow-List Chunking** — describe as an internal property of AddFollowers: large follow sets are split into windows to respect backend message limits, inside one atomic operation; the crawler layer is unaware of it.
10. **Graceful Shutdown** — signal-driven context cancellation; loop breaks at next checkpoint or in-flight context error; close connections + storage; final report (start/end counts, new nodes, runtime); await background work.
11. **Reimplementation Checklist** — a short bullet list of the invariants a port must preserve (frontier-first stale selection, version guard, per-relay cap learning with floor, atomic follow-list replacement, idempotent mark-attempted, expand-not-just-refresh).

Use Mermaid or ASCII diagrams for the main loop and the relay state machine. Use tables for the storage-operation contracts and config keys. Aim for a thorough document (target 300+ lines) that is genuinely sufficient for a clean-room reimplementation.

Do NOT include Go code blocks or Dgraph/DQL query text. Pseudocode for control flow is fine and encouraged.
  </action>
  <verify>
    <automated>test -f web-of-trust/fable_logic_flow.md &amp;&amp; L=$(grep -vc '^$' web-of-trust/fable_logic_flow.md) &amp;&amp; [ "$L" -ge 250 ] &amp;&amp; grep -q "GetStalePubkeys" web-of-trust/fable_logic_flow.md &amp;&amp; grep -q "AddFollowers" web-of-trust/fable_logic_flow.md &amp;&amp; grep -q "MarkAttempted" web-of-trust/fable_logic_flow.md &amp;&amp; grep -qi "filter" web-of-trust/fable_logic_flow.md &amp;&amp; grep -qi "floor" web-of-trust/fable_logic_flow.md &amp;&amp; ! grep -qiE '```go|nquad|dgraph\.|nostr\.Filter' web-of-trust/fable_logic_flow.md</automated>
  </verify>
  <done>web-of-trust/fable_logic_flow.md exists with all eleven sections; storage operations are documented as named abstract contracts; the chunked sub-REQ loop and both filter-cap halving paths (NOTICE + connection-drop, floor 10) are described; no Go code blocks and no Dgraph/DQL/go-nostr-API specifics appear in the body; the document is at least 250 non-blank lines.</done>
</task>

</tasks>

<verification>
- `web-of-trust/fable_logic_flow.md` exists at the web-of-trust module root.
- The document covers: main loop + seed bootstrap, relay pool (lifecycle/backoff/thresholds/removal), subscription handling (chunked sub-REQ loop + filter-cap NOTICE halving + connection-drop attribution + floor 10), event handling (signature, p-tags, dedup, version check), follow-list chunking, graceful shutdown.
- All DB interactions appear as named abstract operations with inputs/outputs/invariants, not Dgraph specifics.
- No Go syntax fences or Dgraph/DQL/go-nostr API requirements in the body.
</verification>

<success_criteria>
A developer unfamiliar with this repo can reimplement the crawler in a different language against a different database using only `fable_logic_flow.md`, preserving every behavioral invariant of the current code including phase-06 filter-size per-relay cap detection.
</success_criteria>

<output>
Create `.planning/quick/260611-ott-document-crawler-logic-flow-for-cross-la/260611-ott-SUMMARY.md` when done.
</output>

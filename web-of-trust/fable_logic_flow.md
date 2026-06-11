# Web-of-Trust Crawler — Logic Flow Specification

> Reference-stack note: the canonical implementation uses Go, a Nostr WebSocket client
> library, and a graph database (Dgraph). This document describes the **behavior** only.
> Any language and any storage backend that satisfies the abstract storage-operation contracts
> below can host a conforming port.

---

## Table of Contents

1. [Overview and Purpose](#1-overview-and-purpose)
2. [Domain Concepts](#2-domain-concepts)
3. [Storage Operations](#3-storage-operations)
4. [Configuration](#4-configuration)
5. [Main Crawl Loop](#5-main-crawl-loop)
6. [Relay Pool Management](#6-relay-pool-management)
7. [Subscription Handling](#7-subscription-handling)
8. [Event Handling](#8-event-handling)
9. [Follow-List Chunking](#9-follow-list-chunking)
10. [Graceful Shutdown](#10-graceful-shutdown)
11. [Reimplementation Checklist](#11-reimplementation-checklist)

---

## 1. Overview and Purpose

The crawler continuously **expands** a pubkey follow-graph by fetching kind-3 (contact-list)
events from Nostr relays. Its core value is growth — discovering and fetching contact lists for
newly-seen pubkeys — not just re-refreshing accounts it already knows.

Each kind-3 event contains a list of pubkeys the author follows. The crawler stores only the
graph structure (who follows whom), never the raw event payloads. The resulting follow-graph
feeds a trust-scoring system and a whitelist that decides which pubkeys may write events to a
relay.

**Lifecycle summary:**

```
Startup
  → connect to relay pool
  → enter main loop
      → select stale pubkeys from storage
      → reconnect dead relays
      → query relays for kind-3 events
      → update follow-graph in storage
      → mark batch attempted
      → repeat
  → on shutdown signal: drain, close, report
```

---

## 2. Domain Concepts

| Term | Definition |
|------|------------|
| **pubkey** | A 64-character lowercase hexadecimal string uniquely identifying a Nostr identity. The canonical form is always lowercase hex — no uppercase, no other encoding. |
| **kind-3 event** | A Nostr replaceable event where the author declares their follow list. Each event's `tags` contains zero or more `["p", <pubkey>]` entries. Because it is a replaceable event, a newer kind-3 always supersedes an older one for the same author. |
| **follow edge** | A directed graph edge from a follower (signer of the kind-3 event) to a followee (a pubkey listed in a `p` tag). |
| **follow-graph** | The complete set of nodes (pubkeys) and edges (follow relationships) accumulated from all processed kind-3 events. |
| **frontier pubkey** | A pubkey that exists in storage but has never been queried for a kind-3 event (no `last_attempt` timestamp). These are the highest-priority crawl targets. |
| **aged pubkey** | A pubkey that was previously queried but whose `last_attempt` is older than the configured stale threshold. |
| **stale pubkey** | Either a frontier pubkey or an aged pubkey — one that should be re-crawled in the next batch. |
| **kind3CreatedAt** | The `created_at` Unix timestamp of the most recently accepted kind-3 event for a given pubkey. Used as a version number to reject older events. |
| **last_attempt** | Unix timestamp of the most recent time the crawler queried relays for this pubkey's kind-3 event. Set regardless of whether an event was found. |
| **last_db_update** | Unix timestamp of the most recent time the pubkey's graph data was touched (either a follow-list write or a `TouchLastDBUpdate` call). |

---

## 3. Storage Operations

All storage interactions are expressed as named abstract operations. Any backend (relational DB,
graph DB, key-value store, document store) can satisfy a conforming implementation as long as
the contracts and invariants below hold.

### 3.1 EnsureSchema

**Purpose:** Idempotently create or verify all storage structures the crawler requires.

| | |
|--|--|
| **Inputs** | none |
| **Outputs** | error if schema cannot be created |
| **Invariants** | Each pubkey must be unique in storage (enforce uniqueness). A node holds at minimum: `pubkey` (string, unique key), `kind3CreatedAt` (integer, nullable), `last_attempt` (integer, nullable), `last_db_update` (integer, nullable), and the `follows` relationship (directed edges to other nodes). Called once at startup before any read/write. |

### 3.2 CountPubkeys

**Purpose:** Return the total number of pubkey nodes in storage.

| | |
|--|--|
| **Inputs** | none |
| **Outputs** | integer count |
| **Invariants** | Count includes all nodes regardless of whether they have follow edges or attempt timestamps. A count of 0 means the database is empty — triggers seed bootstrap. |

### 3.3 GetStalePubkeys

**Purpose:** Select up to `limit` pubkeys that need (re)crawling. Returns a map of
`pubkey -> kind3CreatedAt` for each selected pubkey (kind3CreatedAt may be 0 or absent if
never seen).

| | |
|--|--|
| **Inputs** | `olderThan` (Unix timestamp), `limit` (integer) |
| **Outputs** | map(pubkey -> kind3CreatedAt) |
| **Invariants** | Two-phase selection, described below. Total results bounded by `limit`. |

**Two-phase selection:**

```
Phase 1 — Uncrawled frontier (higher priority)
  SELECT pubkeys WHERE last_attempt IS NULL
  LIMIT limit
  → collect into result

Phase 2 — Aged-out previously-attempted (fill remaining slots)
  remaining = limit - len(result from Phase 1)
  IF remaining > 0:
    SELECT pubkeys WHERE last_attempt < olderThan
    ORDER BY last_attempt ASC  (oldest-attempt first)
    LIMIT remaining
    → merge into result
```

**Critical invariant:** Never use `ORDER BY last_attempt ASC` to surface frontier (null) nodes.
In many storage engines, null values sort last under `ASC` ordering, which causes the frontier
to be invisible when the aged set is large. Always select frontier nodes with an explicit
`IS NULL` filter in a separate query phase.

### 3.4 MarkAttempted

**Purpose:** Record that the crawler queried relays for each pubkey in the batch, regardless of
whether an event was returned.

| | |
|--|--|
| **Inputs** | `pubkeys` (list of strings), `timestamp` (Unix integer) |
| **Outputs** | error |
| **Invariants** | Sets `last_attempt = timestamp` for every valid pubkey. Idempotent (safe to call multiple times with the same pubkey). Invalid pubkeys trigger inline recovery: if a pubkey is 64 hex chars but mixed-case (recoverable), attempt to lowercase it and merge with any existing canonical node; if it is unrecoverable garbage, purge it from storage so it stops re-entering the frontier. |

**Inline recover-or-purge logic:**

```
FOR each pk in pubkeys:
  IF isValidPubkey(pk):
    stamp last_attempt = timestamp on pk's node
  ELSE IF isUppercaseHex64(pk):
    lower = toLower(pk)
    IF lower already exists in storage:
      DELETE the garbage (uppercase) node   // canonical already present
    ELSE:
      UPDATE the node's pubkey field to lower  // rename garbage to canonical
  ELSE:
    DELETE the garbage node from storage
```

### 3.5 AddFollowers

**Purpose:** Atomically replace the entire follow list for a given signer pubkey with the
provided follow set. This is the single write path for a kind-3 event.

| | |
|--|--|
| **Inputs** | `signerPubkey` (string), `kind3CreatedAt` (integer), `followSet` (set of pubkey strings) |
| **Outputs** | error |
| **Invariants** | Detailed below. |

**Contract:**

```
1. VALIDATE signerPubkey — reject with error if invalid (64 lowercase hex)
2. VERSION GUARD — if stored kind3CreatedAt for signer >= incoming kind3CreatedAt:
     no-op (do not modify follow edges or timestamps)
     return success
3. DELETE all existing follow edges from signerPubkey
4. FOR each followee in followSet (in batches to respect backend message limits):
     IF followee does not exist in storage: CREATE stub node for followee
     CREATE follow edge: signerPubkey → followee
5. UPDATE signerPubkey.kind3CreatedAt = kind3CreatedAt
6. UPDATE signerPubkey.last_db_update = now
7. COMMIT atomically — either all changes land or none do
```

**Internal batching:** The follow set is split into windows (see Section 9) to prevent
exceeding backend message-size limits. This is an internal implementation detail; the caller
passes the full follow set as a single operation. Invalid followee pubkeys within the set are
skipped (logged), so one bad entry never aborts the rest of the list.

### 3.6 TouchLastDBUpdate

**Purpose:** Record that the crawler processed this pubkey in the current cycle without
changing its follow data.

| | |
|--|--|
| **Inputs** | `pubkey` (string) |
| **Outputs** | error |
| **Invariants** | Sets `last_db_update = now` only. Does NOT modify `kind3CreatedAt` or follow edges. Called when an incoming event exists but is not newer than the stored version (version guard in the caller has already determined no update is needed). |

### 3.7 ValidatePubkey

**Purpose:** Determine whether a string is a valid pubkey.

| | |
|--|--|
| **Inputs** | `pubkey` (string) |
| **Outputs** | boolean or error |
| **Invariants** | Valid iff the string matches exactly 64 lowercase hexadecimal characters (`[0-9a-f]{64}`). Uppercase hex, npub-encoded keys, relay URLs, and partial keys all fail. This is the single canonical validation function; every pubkey-acceptance or rejection site must use it. |

---

## 4. Configuration

Configuration lives in a YAML file at `~/deepfry/web-of-trust.yaml`. If the file does not
exist at startup it is created automatically with the defaults shown below.

| Key | Type | Default | Role |
|-----|------|---------|------|
| `relay_urls` | list of strings | five popular Nostr relays | WebSocket URLs of Nostr relays to subscribe to. Entries are removed automatically when a relay fails beyond the ejection threshold. |
| `dgraph_addr` | string | `localhost:9080` | Address of the storage backend gRPC endpoint. |
| `pubkey` | string | hardcoded hex | Seed pubkey for bootstrapping an empty database. Accepts npub or hex; npub is decoded to hex at load time. |
| `timeout` | duration | `30s` | Per-batch relay query timeout. A deadline-exceeded result from relays does NOT abort event processing — events already received are still written. |
| `stale_pubkey_threshold` | integer (seconds) | `86400` (24 hours) | A pubkey whose `last_attempt` is older than `now - stale_pubkey_threshold` is eligible for re-crawl. |
| `relay_filter_batch_size` | integer | `100` | **Dual-purpose:** (1) the maximum number of pubkeys selected per stale batch (`limit` argument to `GetStalePubkeys`), and (2) the initial per-relay filter cap (maximum authors per REQ subscription chunk). |
| `forward_relay_url` | string | empty | Optional URL of a relay to which received events are re-published. Can be set interactively at startup or via config. Persisted on save. |
| `debug` | boolean | `false` | Enables verbose per-event and per-relay logging. |

Config is self-healing: if the file is absent it is written with the defaults. Changes made at
runtime (relay removal, forward relay save) are persisted back to disk immediately.

---

## 5. Main Crawl Loop

### 5.1 Startup Sequence

```
1. Create cancellable root context
2. Register SIGINT / SIGTERM handler → cancels root context
3. Load configuration (fatal on error)
4. Connect to storage backend (fatal on error)
5. Run EnsureSchema (fatal on error)
6. [Optional] Interactive prompt for forward relay URL if not in config
7. Connect to all configured relays (relays that fail are dropped + deconfigured)
   → Fatal error if zero relays connect
8. Record: startingPubkeyCount = CountPubkeys()
9. Enter main loop
```

### 5.2 Main Loop

```
LOOP:
  IF context is cancelled → BREAK (shutdown)

  pubkeys = GetStalePubkeys(now - stalePubkeyThreshold, relayFilterBatchSize)
  totalCount = CountPubkeys()

  IF totalCount == 0:
    pubkeys[seedPubkey] = 0        // seed bootstrap (kind3CreatedAt = 0)
    log "Database is empty, starting with seed pubkey"

  IF len(pubkeys) == 0:
    log "No stale pubkeys found, work complete"
    BREAK

  ReconnectRelays(context)          // retry dead relays whose backoff has expired

  hadEvents, err = FetchAndUpdateFollows(context, pubkeys)
  IF err != nil AND context is cancelled → BREAK
  IF err != nil → log error, BREAK

  MarkAttempted(all keys in pubkeys batch, now)

  log batch summary (queried, had events, total in DB)

END LOOP
```

### 5.3 Flowchart

```
                    ┌─────────────────────┐
                    │       Startup        │
                    │  (connect, schema)   │
                    └──────────┬──────────┘
                               │
                    ┌──────────▼──────────┐
              ┌────►│  Check ctx.Done?    ├─── YES ──► SHUTDOWN
              │     └──────────┬──────────┘
              │                │ NO
              │     ┌──────────▼──────────┐
              │     │  GetStalePubkeys()   │
              │     └──────────┬──────────┘
              │                │
              │     ┌──────────▼──────────┐
              │     │  CountPubkeys == 0? ├─ YES ─► seed[seedPubkey]=0
              │     └──────────┬──────────┘
              │                │
              │     ┌──────────▼──────────┐
              │     │  len(pubkeys) == 0? ├─ YES ─► BREAK → SHUTDOWN
              │     └──────────┬──────────┘
              │                │ NO
              │     ┌──────────▼──────────┐
              │     │   ReconnectRelays()  │
              │     └──────────┬──────────┘
              │                │
              │     ┌──────────▼──────────┐
              │     │ FetchAndUpdateFollows│
              │     └──────────┬──────────┘
              │                │
              │     ┌──────────▼──────────┐
              │     │   MarkAttempted()    │
              │     └──────────┬──────────┘
              │                │
              │     ┌──────────▼──────────┐
              │     │    Log batch stats   │
              └─────┤    (loop again)      │
                    └─────────────────────┘
```

### 5.4 Seed Bootstrap

The seed bootstrap fires only when `CountPubkeys() == 0` — meaning the storage backend is
completely empty. The seed pubkey is inserted into the stale batch with `kind3CreatedAt = 0`,
so when the crawler fetches its kind-3 event any real `created_at` value will be accepted (the
version guard compares `event.created_at > stored`, and 0 is always less than any real
timestamp).

---

## 6. Relay Pool Management

### 6.1 Per-Relay State

Each relay in the pool carries:

| Field | Type | Initial Value |
|-------|------|---------------|
| `url` | string | configured value |
| `connection` | WebSocket handle | connected on startup |
| `alive` | boolean | `true` after successful connect |
| `backoff` | duration | `initialBackoff` (30 seconds) |
| `retryAt` | timestamp | zero |
| `failures` | integer (atomic) | `0` |
| `filterCap` | integer (atomic) | `relay_filter_batch_size` from config |

Constants:

| Constant | Value |
|----------|-------|
| `initialBackoff` | 30 seconds |
| `maxBackoff` | 5 minutes |
| `maxConsecutiveFailures` | 5 |

### 6.2 Connection Lifecycle

**At construction (New):**

```
FOR each relay URL in config:
  CREATE relay state with backoff = initialBackoff
  SET filterCap = relay_filter_batch_size
  REGISTER notice handler (see Section 7.2)
  CONNECT to relay WebSocket
  IF connect fails:
    LOG warning
    CALL OnConnectFail(url)  → removes relay from persistent config
    SKIP this relay (do not add to pool)
  ELSE:
    SET alive = true
    ADD to pool

IF pool is empty after all attempts:
  CLOSE storage connection
  FATAL error — cannot proceed without at least one relay
```

**Forward relay (optional, separate lifecycle):**

```
IF forward_relay_url is configured:
  CONNECT to forward relay WebSocket
  IF connect fails:
    LOG warning + "will retry later"
    SET alive = false (retries scheduled later)
  ELSE:
    SET alive = true
```

### 6.3 Relay State Machine

```
              ┌──────────┐
   connect    │          │  connection drop
   success    │  alive   │──────────────────────────┐
  ──────────► │  (alive  │                          │
              │  = true) │◄──────────────────────── │
              └──────────┘    reconnect success      │
                                                     ▼
                                              ┌──────────────┐
                                              │   markDead   │
                                              │ (alive=false)│
                                              └──────┬───────┘
                                                     │
                                          failures < maxConsecutiveFailures?
                                              │               │
                                             YES             NO
                                              │               │
                                    ┌─────────▼──────┐  ┌────▼──────────────┐
                                    │  Backoff wait   │  │ OnConnectFail(url)│
                                    │ (retryAt set,   │  │ remove from config│
                                    │  backoff *=2,   │  │ remove from pool  │
                                    │  cap maxBackoff)│  └───────────────────┘
                                    └─────────┬───────┘
                                              │ retryAt expires
                                    ┌─────────▼───────┐
                                    │ ReconnectRelays()│
                                    │ attempts connect │
                                    └─────────┬───────┘
                                              │
                                  success?────┤────failure?
                                      │            │
                          ┌───────────▼──┐   ┌─────▼──────────────┐
                          │  alive=true  │   │  OnConnectFail(url) │
                          │  backoff     │   │  remove from pool   │
                          │  = initial   │   └────────────────────┘
                          │  failures=0  │
                          │  filterCap   │
                          │  = batchSize │
                          └─────────────┘
```

### 6.4 markRelayDead

Called when a transport-level failure is detected (connection dropped, write failed,
filter-cap floor reached).

```
FIND relay by url in pool
CLOSE connection (if open)
SET conn = nil
SET alive = false
INCREMENT failures atomically

IF failures >= maxConsecutiveFailures:
  LOG "removing from config"
  CALL OnConnectFail(url)
  REMOVE relay from pool entirely
  RETURN

SET retryAt = now + current backoff
LOG "marked dead, failure N/5, next retry in Xs"
DOUBLE backoff (cap at maxBackoff)
```

Note: the backoff doubling happens AFTER `retryAt` is set, so the first retry window uses the
original backoff value, and subsequent retries use progressively doubled values.

### 6.5 ReconnectRelays

Called once per main loop iteration, before `FetchAndUpdateFollows`.

```
FOR each relay in pool:
  IF alive: keep, skip
  IF now < retryAt: keep, skip (still in backoff window)

  ATTEMPT connect (with notice handler registered)
  IF success:
    SET alive = true
    RESET backoff = initialBackoff
    RESET failures = 0
    RESET filterCap = relay_filter_batch_size   // cap resets on reconnect
    LOG "Reconnected"
  ELSE:
    LOG warning
    CALL OnConnectFail(url)
    REMOVE relay from pool

FOR forward relay (if present and dead):
  IF now < retryAt: skip
  ATTEMPT connect
  IF success:
    SET alive = true
    RESET backoff = initialBackoff
  ELSE:
    SET retryAt = now + backoff
    DOUBLE backoff (cap at maxBackoff)
```

Important: reconnecting a crawl relay resets `filterCap` back to the configured
`relay_filter_batch_size`. This is intentional — after a reconnect, any per-relay cap that was
learned from NOTICE messages or drop attribution is discarded, and the cap learns itself again
fresh on the new connection.

---

## 7. Subscription Handling

### 7.1 Chunked sub-REQ Loop

The relay query iterates the batch authors in chunks sized to the current per-relay filter cap
(`filterCap`). For each chunk:

```
authors = validated pubkeys from batch (all that pass ValidatePubkey)
WHILE authors is not empty:
  batchCap = filterCap.load()   // current per-relay cap (may have been halved)
  IF batchCap <= 0: batchCap = 10  // safety guard

  chunk = authors[0 : min(batchCap, len(authors))]
  authors = authors[batchCap:]

  recordSubscribeStart = now

  sub, err = relay.Subscribe(filter{kinds:[3], authors:chunk, limit:batchSize})

  IF err:
    → see Section 7.3 (connection-drop attribution)
    or → classify as subscriptionError or transportError (see Section 8.5)

  drain sub until EOSE or error (see Section 7.4)
  UNSUBSCRIBE sub
  advance to next chunk
```

Each chunk generates one WebSocket REQ message. The connection is kept open across chunks; only
the subscription (REQ/CLOSE pair) is created and torn down per chunk.

### 7.2 NOTICE-Based Filter-Cap Halving

Each relay connection is created with a NOTICE handler registered. When the relay sends a
NOTICE message whose **lowercased text contains both "filter" and "too large"**:

```
handleFilterNotice(relay, notice, minCap=10):
  lower = toLower(notice)
  IF "filter" in lower AND "too large" in lower:
    LOOP (compare-and-swap):
      old = filterCap.load()
      IF old <= minCap:
        LOG "cap already at floor, ignoring"
        RETURN
      newVal = old / 2
      IF newVal < minCap: newVal = minCap
      IF compareAndSwap(filterCap, old, newVal):
        LOG "halved cap to newVal"
        RETURN
      // CAS failed: another goroutine updated filterCap concurrently → retry
```

The compare-and-swap loop is required because the NOTICE handler runs in a goroutine separate
from the subscription loop, creating a concurrent-write scenario on the cap value. The loop
retries until it successfully writes a new cap value.

Floor: the cap is never reduced below **10**. At floor, further NOTICE messages are logged but
produce no change.

### 7.3 Connection-Drop-on-REQ Attribution

If `relay.Subscribe()` returns an error and both of these conditions hold:

- The error occurred within **500 milliseconds** of the subscribe call (fast failure), AND
- The error text contains `"not connected"` or `"failed to write"`

...then the error is treated as a filter-size rejection (the relay disconnected the WebSocket
to signal REQ overload):

```
IF subscribeStart elapsed < 500ms AND ("not connected" OR "failed to write" in error):
  old = filterCap.load()
  IF old > 10:
    newVal = max(old / 2, 10)
    filterCap.store(newVal)
    LOG "filter rejection drop, halved cap to newVal; retrying chunk"
    authors = prepend(chunk, authors)  // re-queue this chunk at the front
    CONTINUE loop (retry with smaller cap)
  ELSE:
    // already at floor, relay cannot serve any filter → mark dead
    LOG "filter cap floor reached, marking dead"
    RETURN transportError
```

WHY: Nostr relays have undocumented per-filter author limits. Some relays close the connection
instead of sending a NOTICE. This path detects that signal and learns the relay's cap at
runtime without any external configuration.

### 7.4 drainSubscription

Reads events from a subscription until one of:

- **EOSE** (End of Stored Events): return nil (success)
- **External context cancellation** (shutdown or timeout): return the context error
- **Subscription context done** (connection dropped mid-stream): return a transport error

```
LOOP:
  SELECT:
    CASE event from sub.Events:
      IF event is not nil:
        send event to shared events channel
          (non-blocking w.r.t. external cancellation: if channel send would block
           and context is cancelled, return context error instead of blocking)
    CASE sub.EndOfStoredEvents:
      RETURN nil
    CASE sub.Context.Done (relay connection dropped):
      RETURN transportError(relay connection dropped)
    CASE ctx.Done (external cancellation):
      RETURN ctx.Err()
```

The caller owns `Unsub()` and calls it after `drainSubscription` returns, regardless of
error. This keeps the per-chunk lifecycle explicit: subscribe → drain → unsubscribe.

---

## 8. Event Handling

### 8.1 Concurrent Fan-In

All alive relays are queried concurrently. Each relay runs in its own goroutine and sends
events into a shared buffered channel. The main event loop reads from this shared channel and
processes events under a mutex-protected section (one event at a time through storage writes).

```
FOR each alive relay:
  LAUNCH goroutine: queryRelay(ctx, relay, filter, eventsChan)

LAUNCH goroutine: WAIT for all relay goroutines, THEN CLOSE eventsChan and errorsChan

LOOP:
  SELECT:
    CASE event from eventsChan: → process event (Section 8.2–8.4)
    CASE error from errorsChan: → classify error (Section 8.5)
    CASE relayContext.Done (timeout): → if deadline exceeded, continue processing already-received events
    CASE mainContext.Done (shutdown): → return with context error
```

### 8.2 Event Deduplication

A set of processed event IDs is maintained for the duration of the batch call. If the same
event ID is received from multiple relays, only the first copy is processed. Subsequent copies
are silently skipped.

### 8.3 Signature Verification

Before any storage operation, the event signature is verified against the author pubkey:

```
ok, err = event.CheckSignature()
IF NOT ok:
  LOG warning (invalid signature, pubkey, event ID)
  record metric: signature_valid=false
  SKIP event (continue loop)
```

Events with invalid signatures are never written to storage and never forwarded.

### 8.4 Version Check and Follow-List Update

After deduplication and signature verification:

```
FORWARD event to forward relay (Section 6, forward relay lifecycle)
RECORD pubkey as "had event this batch"

IF event.created_at <= stored kind3CreatedAt for event.PubKey:
  // Incoming event is not newer than what we have
  CALL TouchLastDBUpdate(event.PubKey)
  CONTINUE (skip to next event)

// Incoming event is newer → update follow list
CALL updateFollowsFromEvent(event)
```

### 8.5 p-Tag Parsing and Follow-Set Deduplication

```
updateFollowsFromEvent(event):
  followSet = empty set
  rawCount = 0

  FOR each tag in event.Tags:
    IF tag[0] == "p" AND len(tag) >= 2:
      pubkey = tag[1]
      IF NOT isValidPubkey(pubkey): SKIP (log if debug)
      rawCount++
      followSet.add(pubkey)  // set deduplicates automatically

  duplicates = rawCount - len(followSet)

  CALL AddFollowers(event.PubKey, event.created_at, followSet)
```

Key points:
- Only `p` tags with at least two elements are processed.
- Each pubkey is validated (64 lowercase hex chars) before being added to the set.
- The follow set is deduplicated (a pubkey listed multiple times counts once).
- `AddFollowers` **replaces** the entire follow list — kind-3 is a replaceable event.

### 8.6 Error Classification

Errors returned from relay goroutines are classified:

| Error type | Meaning | Action |
|------------|---------|--------|
| `subscriptionError` | The REQ subscription itself failed (protocol-level, non-transport) | Log warning; relay stays alive |
| `transportError` | The connection dropped (WebSocket dead) | Log warning; call `markRelayDead(relay_url)` |
| Context cancellation / deadline exceeded from relay query | Expected during shutdown or timeout | Suppress (do not mark relay dead) |
| Other | Unknown relay error | Log warning; relay stays alive |

### 8.7 Timeout Behavior

The relay query runs under a context with a deadline (`timeout` config, default 30 seconds).
When the deadline is exceeded:

- The relay goroutines are interrupted via context cancellation.
- **The main event loop does NOT abort.** It continues draining the shared events channel for
  any events that were received before the timeout.
- Only external cancellation (SIGINT/SIGTERM) causes an immediate return.

This means a slow or silent relay does not block processing of events from fast relays.

---

## 9. Follow-List Chunking

Large follow sets are handled **inside** `AddFollowers` as an internal implementation detail.
The crawler layer is entirely unaware of this batching.

When `AddFollowers` receives a large follow set, it splits the set into fixed-size windows.
For each window it:
1. Resolves which followees already exist in storage (query window).
2. Creates stub nodes for any followees that do not yet exist (write window).
3. Creates follow edges from the signer to each followee in the window (write window).

All windows are executed within a single all-or-nothing transaction. A failure at any window
aborts the entire operation — no partial follow lists are persisted.

The chunking exists because storage backends typically impose limits on the size of a single
query or mutation message (in the reference implementation, the gRPC layer imposes a ~4 MB
cap). At roughly 10,000 followees, even the query string to resolve followee nodes exceeds
this limit if not chunked. The window size is a single internal tuning constant — not surfaced
in configuration.

**Caller contract:** Pass the full follow set. Do not chunk before calling. The operation is
atomic end-to-end.

---

## 10. Graceful Shutdown

### 10.1 Signal Handling

A background goroutine listens for `SIGINT` and `SIGTERM`. On receipt, it cancels the root
context. The main loop checks the context at the top of each iteration:

```
LOOP:
  SELECT:
    CASE ctx.Done: BREAK mainLoop
    DEFAULT: continue
```

In-flight `FetchAndUpdateFollows` calls return when their internal context operations detect
cancellation. Event processing under the mutex completes the current event before returning.

### 10.2 Shutdown Sequence

```
1. Root context cancelled (signal received)
2. Main loop breaks at next ctx.Done check or in-flight context error
3. crawler.Close():
   FOR each relay: close WebSocket connection
   Close forward relay connection (if present)
   Close storage client connection
4. Storage client deferred close (in main)
5. Generate final report (see 10.3)
6. wg.Wait() — await signal-handler goroutine
7. Log "Shutdown complete"
```

### 10.3 Final Report

Printed at the end of every run (graceful or error-driven break):

```
Seed pubkey: <seed>
Pubkeys in DB: <startingCount> at start, <endingCount> at end (<delta> new)
Total runtime: <duration>
Crawler shutdown gracefully
```

---

## 11. Reimplementation Checklist

A port must preserve all of these behavioral invariants to be conforming:

- **Frontier-first stale selection.** Never-attempted pubkeys are always selected before
  aged-out pubkeys, using an explicit `IS NULL` filter. Do not rely on ascending sort of
  `last_attempt` to surface null-value nodes — they sort last in most engines.

- **Version guard before follow-list write.** Never overwrite a follow list when the incoming
  event's `created_at` is not strictly greater than the stored `kind3CreatedAt`. Use
  `TouchLastDBUpdate` instead.

- **Atomic follow-list replacement.** The entire follow list is replaced in a single
  all-or-nothing operation. Partial writes must not be possible. The operation must be
  idempotent (safe to retry).

- **Per-relay filter-cap learning with floor.** Each relay starts at the configured batch size.
  The cap halves on NOTICE "filter too large" and on fast connection-drop-on-REQ. The floor is
  10 — the cap never goes below this. The cap resets to the configured value on relay
  reconnect.

- **Connection-drop-on-REQ attribution.** A drop within 500 ms of Subscribe with a
  "not connected" or "failed to write" error is treated as a filter-size rejection, not a dead
  relay — unless the cap is already at the floor, in which case the relay is marked dead.

- **Idempotent MarkAttempted with inline garbage collection.** Stamping `last_attempt` on
  invalid pubkeys triggers recover-or-purge inline, preventing garbage pubkeys from
  re-entering the frontier on every cycle.

- **Expand, not just refresh.** New pubkeys discovered as followees become stub nodes in
  storage and enter the frontier immediately. The crawler must visit them in subsequent
  iterations.

- **Signature verification before any storage write.** Events with invalid signatures are
  discarded without touching storage.

- **Timeout does not abort.** A relay query timeout stops receiving new events from relays but
  does not discard already-received events — they are still written to storage.

- **Backoff doubles after retryAt is set (not before).** The first retry window uses the
  current backoff value; the next retry window uses 2x that value.

- **filterCap is concurrency-safe.** The NOTICE handler runs concurrently with the subscription
  loop. Any load/store of the filter cap must use atomic operations with compare-and-swap for
  the halving path to prevent lost updates.

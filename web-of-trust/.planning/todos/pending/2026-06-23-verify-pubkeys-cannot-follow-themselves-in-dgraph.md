---
created: 2026-06-23T12:29:54.349Z
title: Verify pubkeys cannot follow themselves in Dgraph
area: dgraph
files:
  - pkg/dgraph/dgraph.go (AddFollowers)
  - pkg/crawler/crawler.go (kind-3 P-tag parsing)
---

## Problem

We need to confirm the crawler never writes a self-follow edge — a `Profile` node that lists its own pubkey in its `follows` set (A → A). Kind-3 contact lists occasionally include the author's own pubkey (client bug, or deliberate self-reference), and if we pass that straight through to Dgraph it pollutes the graph:

- inflates the node's own `follower_count` by 1 (the stored in-degree maintained in `AddFollowers`),
- creates a 1-node self-loop that can distort clusterscan / weak-bridge and trust-propagation logic,
- is also a minor spam/malformed-event signal worth knowing about.

Two open questions:
1. **Write-path guard:** does P-tag parsing in `pkg/crawler/crawler.go` (and/or `AddFollowers` in `pkg/dgraph/dgraph.go`) drop a followee equal to the signer's own pubkey before the upsert?
2. **Existing data:** are there already self-follow edges in the live 1.54M-node Dgraph from before any such guard existed?

## Solution

**Part 1 — static (authoritative answer):** Read the kind-3 handler in `pkg/crawler/crawler.go` where P-tags are parsed/de-duped, and `AddFollowers` in `pkg/dgraph/dgraph.go`. Confirm there is an explicit `if followee == signerPubkey { continue }` (or equivalent) guard. If absent, that's the fix: filter self-references at parse time so they never reach the chunked upsert, and add a unit/integration test.

**Part 2 — live data check (Dgraph, `localhost:8080`):** detect existing self-loops. Caveat: DQL uid vars are *sets*, so a naive `follows @filter(uid(u))` where `u as uid` is bound at the parent matches the whole candidate set, not the specific parent — it does NOT cleanly isolate self-loops. Safer path: export a bounded sample of `{ uid, pubkey, follows { pubkey } }` and check in code whether a node's own pubkey appears in its own follows list; or use a recurse/expand pattern and post-filter. Quantify how many self-loops exist, then decide whether a one-time cleanup mutation (remove self-edge + decrement `follower_count`) is warranted.

**Done when:** (a) we know whether the write path guards against self-follow (and add the guard + test if not), and (b) we have a count of pre-existing self-loops in live Dgraph and a decision on cleanup.

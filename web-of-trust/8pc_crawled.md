# Web-of-Trust crawler only ever crawls ~8% of known pubkeys

**Status:** root cause confirmed, fix specified below.
**Module root for all paths in this doc:** `web-of-trust/` (each path is relative to it, e.g. `pkg/dgraph/dgraph.go`).
**Affected binary:** `cmd/crawler` (built via `make build-crawler`).

---

## 1. Symptom

The crawler has run for months, yet the Dgraph follow-graph contains 189,201 pubkey
nodes of which only **15,226 (8%) have ever had their kind-3 (contact list) fetched**.
The other **173,975 (92%) are bare "stubs"** — nodes created only because some crawled
account follows them. The web of trust never grows past the original seed neighbourhood.

A node is a **stub** when it was created as a *followee* but its own kind-3 was never
ingested. The discriminator is the `kind3CreatedAt` / `last_db_update` predicates: they
are written **only** when an account's own kind-3 is processed (see `AddFollowers`,
`pkg/dgraph/dgraph.go:137-142` and `:171-174`). A stub has neither.

### Reproduce the measurement (read-only DQL against the live Dgraph)

```bash
# total nodes
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/query \
  -d '{ x(func: has(pubkey)) { c: count(uid) } }'
# crawled nodes (have kind3CreatedAt)
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/query \
  -d '{ x(func: has(kind3CreatedAt)) { c: count(uid) } }'
# stubs (no kind3CreatedAt, no follows)
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/query \
  -d '{ v as var(func: has(pubkey)) @filter(NOT has(kind3CreatedAt) AND NOT has(follows)) x(func: uid(v)) { c: count(uid) } }'
```

---

## 2. What was ruled out (with evidence)

| Hypothesis | Verdict | Evidence |
|---|---|---|
| Crawled nodes are being overwritten back into stubs | ❌ ruled out | `AddFollowers` only creates a stub for a followee **not found** by `eq(pubkey)` (`pkg/dgraph/dgraph.go:240-254`); an older incoming event is rejected (`:157`); `pubkey` is `@unique` in the live schema; **0** nodes have `last_db_update` without `kind3CreatedAt`. |
| Stubs are genuine leaf accounts with no contact list | ❌ ruled out | Sampled 40 stubs against the crawler's own configured relays: **34/40 (85%) have a fetchable kind-3**. |
| The configured relays are dead / wrong | ❌ ruled out | 123–131 of 136 configured relays connected and returned data; they carry the events. |
| **The selection query never returns stubs** | ✅ **root cause** | See §3. The default `GetStalePubkeys` query returns 1000 rows, **0 of which are stubs**. A 5-minute live debug run did **0 stub→crawled conversions**. |

---

## 3. Root cause

### 3.1 The selection query starves stubs

`GetStalePubkeys` (`pkg/dgraph/dgraph.go:430-468`) is the function the crawler uses to
choose which pubkeys to fetch next. Its query is:

```go
// pkg/dgraph/dgraph.go:434-441
query := fmt.Sprintf(`
{
    stale(func: has(pubkey), orderasc: last_db_update) 
    @filter(NOT has(last_db_update) OR lt(last_db_update, %d)) {
        pubkey
        kind3CreatedAt
    }
}`, olderThanUnix)
```

Two defects combine here:

1. **`orderasc: last_db_update` sorts stubs last.** Stubs have **no** `last_db_update`
   predicate. In Dgraph, nodes missing the sort predicate are ordered **after** all nodes
   that have it. So every crawled-but-aged account (all of which *have* `last_db_update`)
   comes before the first stub.

2. **No explicit `first:` → Dgraph caps the sorted result at its default of 1000 rows.**
   The `@filter` genuinely matches 187,584 nodes, but the sorted+capped result only ever
   returns the **1000 crawled accounts with the oldest `last_db_update`**.

Net effect: the query returns 1000 already-crawled accounts and **never a single stub**.

#### Proof

```bash
NOW=$(python3 -c "import time;print(int(time.time()))"); THRESH=$((NOW-3600))

# Exact GetStalePubkeys query (WITH orderasc, no explicit first) → returns 1000
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/query -d "{
  stale(func: has(pubkey), orderasc: last_db_update) @filter(NOT has(last_db_update) OR lt(last_db_update, $THRESH)) { c: count(uid) }
}"
# => 1000

# Same filter WITHOUT orderasc → 187584  (the true match count)
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/query -d "{
  stale(func: has(pubkey)) @filter(NOT has(last_db_update) OR lt(last_db_update, $THRESH)) { c: count(uid) }
}"
# => 187584

# Of the default (orderasc, 1000) result, how many are stubs?
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/query -d "{
  stale(func: has(pubkey), orderasc: last_db_update) @filter(NOT has(last_db_update) OR lt(last_db_update, $THRESH)) { pubkey kind3CreatedAt }
}" | python3 -c "import sys,json;d=json.load(sys.stdin)['data']['stale'];print('stubs:',sum(1 for n in d if not n.get('kind3CreatedAt')),'of',len(d))"
# => stubs: 0 of 1000
```

### 3.2 Downstream confirmation (live debug run)

Run the crawler with `debug: true` and watch one cycle: each batch queries 500 of the
selected (all-crawled) pubkeys, and ~97% of received events hit the "already have newer
event" no-op path (`pkg/crawler/crawler.go:387-394`). Over 5 minutes: 7 completed batches,
7,018 events processed, **0 `New pubkey added to graph (signer)`**, stub count went
*up* by 8 (new followees), **0 stub→crawled conversions**. The crawler simply re-crawls
the same ~15k accounts forever, refreshing their `last_db_update` (which sends them to the
back of the `orderasc` queue) and cycling.

### 3.3 Secondary defect — no "attempted" marker

`last_db_update` is written **only when a kind-3 event is received** — in `AddFollowers`
(`pkg/dgraph/dgraph.go:171-174`) or `TouchLastDBUpdate` (called at
`pkg/crawler/crawler.go:391`). When a relay returns nothing for a pubkey
(`queryRelay` just returns on `EndOfStoredEvents`, `pkg/crawler/crawler.go:476-480`), that
pubkey is left untouched.

Consequence: even after §3.1 is fixed so stubs *are* selected, any pubkey whose kind-3 is
not retrievable (≈15% of stubs, plus invalid pubkeys) will **never** get a timestamp, will
stay in the "never attempted" set, and will be re-selected every cycle — re-clogging the
frontier. The crawler cannot distinguish "not yet tried" from "tried, nothing there", so
the stale set can never converge. **Both fixes are required.**

---

## 4. The fix

Two coordinated changes plus a one-time backfill. They introduce a new predicate
`last_attempt` (when we last *tried* a pubkey) so it is distinct from `last_db_update`
(when we last *successfully* ingested a follow list). Selection keys on `last_attempt`.

### Fix A — add the `last_attempt` predicate to the schema

`EnsureSchema`, `pkg/dgraph/dgraph.go:54-66`. Add the indexed predicate and list it in the
`Profile` type.

```go
// pkg/dgraph/dgraph.go — EnsureSchema (replace the schema string)
schema := `pubkey: string @index(exact) @upsert @unique .
kind3CreatedAt: int @index(int) .
last_db_update: int @index(int) .
last_attempt: int @index(int) .
follows: [uid] @reverse .

type Profile {
  pubkey
  follows
  kind3CreatedAt
  last_db_update
  last_attempt
}`
```

### Fix B — rewrite `GetStalePubkeys` to select the frontier first

Replace the whole function at `pkg/dgraph/dgraph.go:430-468` with the version below. Key
changes: (1) a new `limit int` parameter; (2) **Phase 1** explicitly selects never-attempted
nodes (`NOT has(last_attempt)`) with an explicit `first:` — this is the uncrawled frontier;
(3) **Phase 2** tops the batch up with previously-attempted nodes that have aged out,
ordered by `last_attempt` and bounded by an explicit `first:`. No query ever relies on a
sort to surface missing-value nodes, and no query is left to the default 1000 cap.

```go
// GetStalePubkeys returns up to `limit` pubkeys that need (re)crawling, as a map
// of pubkey -> kind3CreatedAt. It prioritises the uncrawled frontier (pubkeys
// never attempted) and only then tops up with previously-attempted pubkeys
// whose last_attempt is older than olderThanUnix.
//
// IMPORTANT: never-attempted nodes are selected by an explicit `NOT has(last_attempt)`
// query with an explicit `first:`. Do NOT use `orderasc: last_attempt` to surface
// them — missing-value nodes sort last and Dgraph caps an unbounded sorted query at
// 1000 rows, which is the bug this function previously had (it returned only
// already-crawled accounts and never a single stub).
func (c *Client) GetStalePubkeys(
	ctx context.Context,
	olderThanUnix int64,
	limit int,
) (map[string]int64, error) {
	out := make(map[string]int64, limit)

	// Phase 1: the uncrawled frontier — pubkeys we have never attempted.
	frontierQuery := fmt.Sprintf(`
	{
		frontier(func: has(pubkey), first: %d) @filter(NOT has(last_attempt)) {
			pubkey
			kind3CreatedAt
		}
	}`, limit)
	if err := c.collectStale(ctx, frontierQuery, "frontier", out); err != nil {
		return nil, err
	}

	// Phase 2: top up with previously-attempted pubkeys that have aged out.
	if remaining := limit - len(out); remaining > 0 {
		agedQuery := fmt.Sprintf(`
		{
			aged(func: has(last_attempt), first: %d, orderasc: last_attempt)
			@filter(lt(last_attempt, %d)) {
				pubkey
				kind3CreatedAt
			}
		}`, remaining, olderThanUnix)
		if err := c.collectStale(ctx, agedQuery, "aged", out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// collectStale runs a stale-selection query whose root block is named `block`
// and merges its {pubkey -> kind3CreatedAt} rows into out.
func (c *Client) collectStale(
	ctx context.Context,
	query, block string,
	out map[string]int64,
) error {
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)

	resp, err := txn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query stale pubkeys (%s) failed: %w", block, err)
	}

	var parsed map[string][]struct {
		Pubkey         string `json:"pubkey"`
		Kind3CreatedAt int64  `json:"kind3CreatedAt"`
	}
	if err := json.Unmarshal(resp.Json, &parsed); err != nil {
		return fmt.Errorf("unmarshal stale pubkeys (%s) failed: %w", block, err)
	}
	for _, n := range parsed[block] {
		out[n.Pubkey] = n.Kind3CreatedAt
	}
	return nil
}
```

### Fix C — stamp `last_attempt` for every queried pubkey

Add this helper to `pkg/dgraph/dgraph.go`. It reuses `ResolvePubkeysToUIDs`, which already
exists in the same package at `pkg/dgraph/clusterscan.go` (returns `map[pubkey]uid`). All
queried pubkeys already exist as nodes (they came from `GetStalePubkeys`, which only selects
`has(pubkey)`), so this only sets a predicate; it never needs to create nodes.

```go
// MarkAttempted stamps last_attempt = ts on every given pubkey. It records that
// the crawler tried to fetch the pubkey's kind-3, regardless of whether an event
// came back, so that un-fetchable pubkeys age out of the stale frontier instead
// of being retried every loop.
func (c *Client) MarkAttempted(ctx context.Context, pubkeys []string, ts int64) error {
	if len(pubkeys) == 0 {
		return nil
	}

	uids, err := c.ResolvePubkeysToUIDs(ctx, pubkeys) // map[pubkey]uid, in clusterscan.go
	if err != nil {
		return fmt.Errorf("resolve pubkeys for mark-attempted failed: %w", err)
	}

	var nquads strings.Builder
	for _, uid := range uids {
		nquads.WriteString(fmt.Sprintf("<%s> <last_attempt> \"%d\" .\n", uid, ts))
	}
	if nquads.Len() == 0 {
		return nil
	}

	txn := c.dg.NewTxn()
	defer txn.Discard(ctx)
	mu := &api.Mutation{SetNquads: []byte(nquads.String()), CommitNow: true}
	if _, err := txn.Mutate(ctx, mu); err != nil {
		return fmt.Errorf("mark attempted failed: %w", err)
	}
	return nil
}
```

### Fix D — call the new API from the crawler loop

In `cmd/crawler/main.go`:

1. **Pass a limit** to `GetStalePubkeys` and drop the now-redundant manual 500-cap.

   Current code, `cmd/crawler/main.go:108-146`:
   ```go
   // Get stale pubkeys to process
   pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold)
   ...
   // Limit batch size to avoid overload
   totalStale := len(pubkeys)
   if totalStale > 500 {
       limitedPubkeys := make(map[string]int64)
       count := 0
       for pk, timestamp := range pubkeys {
           if count >= 500 { break }
           limitedPubkeys[pk] = timestamp
           count++
       }
       pubkeys = limitedPubkeys
   }
   ```
   Replace with:
   ```go
   const batchSize = 500
   pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, batchSize)
   if err != nil {
       log.Printf("Error getting stale pubkeys: %v", err)
       break
   }
   // (delete the old "Limit batch size" block — GetStalePubkeys now bounds it)
   ```

2. **Stamp the batch as attempted** immediately after fetching. Add right after the
   `FetchAndUpdateFollows` call (`cmd/crawler/main.go:152`), before the "Batch complete" log:
   ```go
   hadEvents, err := crawler.FetchAndUpdateFollows(ctx, pubkeys)
   if err != nil { /* existing error handling at :153-160 */ }

   // Mark every queried pubkey as attempted so un-fetchable ones age out of the
   // frontier instead of being re-selected every cycle.
   batchKeys := make([]string, 0, len(pubkeys))
   for pk := range pubkeys {
       batchKeys = append(batchKeys, pk)
   }
   if err := dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix()); err != nil {
       log.Printf("Warning: failed to mark batch attempted: %v", err)
   }
   ```

> Note: `cmd/crawler/main.go` already holds a `dgraphClient` (created at
> `cmd/crawler/main.go:48`) separate from the one inside the crawler, so no new wiring is
> needed.

### Fix E — one-time backfill (run once, before/at first start after deploy)

Existing crawled nodes have `last_db_update` but no `last_attempt` (new predicate). Without
a backfill, Phase 1 (`NOT has(last_attempt)`) would treat all 189k nodes as frontier and
re-crawl the known 15k before reaching real stubs. Seed `last_attempt` from `last_db_update`
so already-crawled accounts are not re-prioritised:

```bash
curl -s -H 'Content-Type: application/dql' -X POST localhost:8080/mutate?commitNow=true \
  -H 'Content-Type: application/rdf' --data-binary @- <<'EOF'
upsert {
  query { nodes as var(func: has(last_db_update)) { ldu as last_db_update } }
  mutation { set { uid(nodes) <last_attempt> val(ldu) . } }
}
EOF
```
(If your Dgraph build rejects the inline upsert over HTTP, run the equivalent upsert via the
crawler at startup or through Ratel. The predicate `last_attempt` must exist first — apply
Fix A / run `EnsureSchema` before the backfill.)

### Optional tuning — `stale_pubkey_threshold`

The live config uses `stale_pubkey_threshold: 3600` (1 hour), so crawled accounts re-enter
Phase 2 hourly and compete with the frontier. After the fix, frontier work is prioritised
(Phase 1 fills first), so this is no longer fatal, but consider raising it (e.g. `86400`,
the code default in `pkg/config/config.go:55`) to spend more budget expanding the graph and
less re-refreshing known accounts. Config lives at `~/deepfry/web-of-trust.yaml`
(**do not edit it for testing — use a temp `HOME`**, see §6).

---

## 5. Regression test

Add to `pkg/dgraph/` (e.g. `dgraph_stale_test.go`). It asserts the property the old code
violated: **stubs (no `last_attempt`) are returned by `GetStalePubkeys`.** This needs a live
Dgraph; gate it like the other integration tests in this package (`-tags=integration`,
run via `make test-integration`).

```go
//go:build integration

package dgraph

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestGetStalePubkeysIncludesFrontier(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient("localhost:9080")
	if err != nil { t.Fatal(err) }
	defer c.Close()
	if err := c.EnsureSchema(ctx); err != nil { t.Fatal(err) }

	// A crawled node (has last_attempt + kind3CreatedAt) and a pure stub (neither).
	stub := fmt.Sprintf("%064x", time.Now().UnixNano())          // unique fake pubkey
	crawled := fmt.Sprintf("%064x", time.Now().UnixNano()+1)
	now := time.Now().Unix()
	mustMutate(t, c, fmt.Sprintf(`
		_:s <pubkey> %q . _:s <dgraph.type> "Profile" .
		_:c <pubkey> %q . _:c <dgraph.type> "Profile" .
		_:c <kind3CreatedAt> "%d" . _:c <last_db_update> "%d" . _:c <last_attempt> "%d" .
	`, stub, crawled, now, now, now))

	got, err := c.GetStalePubkeys(ctx, now-3600, 100000)
	if err != nil { t.Fatal(err) }

	if _, ok := got[stub]; !ok {
		t.Fatalf("frontier stub %s was NOT selected — regression of the orderasc/1000-cap bug", stub)
	}
	// The freshly-attempted crawled node must NOT be stale yet.
	if _, ok := got[crawled]; ok {
		t.Errorf("freshly-attempted node %s should not be stale", crawled)
	}
}

// mustMutate is a tiny helper; implement with c.dg.NewTxn().Mutate(... CommitNow:true).
```

A cheaper non-integration guard (always runs): assert the query string built by
`GetStalePubkeys`'s Phase 1 contains `NOT has(last_attempt)` and an explicit `first:`, and
does **not** contain `orderasc` in Phase 1 — refactor the query strings into named consts
or a builder so the unit test can inspect them without a database.

---

## 6. Verification plan

All steps are runnable on the strfry host. Do **not** edit `~/deepfry/web-of-trust.yaml`;
to enable debug, copy it to a temp `HOME` and flip `debug: true`:

```bash
mkdir -p /tmp/fakehome/deepfry
sed 's/^debug:.*/debug: true/' ~/deepfry/web-of-trust.yaml > /tmp/fakehome/deepfry/web-of-trust.yaml
```

1. **Build:** `cd web-of-trust && make build-crawler`.
2. **Apply schema + backfill:** start the crawler once (it calls `EnsureSchema` on startup,
   `pkg/crawler/crawler.go:76`), then run the Fix E backfill.
3. **Baseline:** record the stub count (DQL in §1) — expect ~173,975.
4. **Run past startup, then crawl:** the relay-connect phase is sequential and takes
   ~4 minutes for 136 relays; wait until the log prints `Querying relay ... for N pubkeys`,
   snapshot the stub count, then let it crawl ~5 minutes:
   ```bash
   cd web-of-trust && HOME=/tmp/fakehome ./bin/crawler < /dev/null > /tmp/crawler.log 2>&1 &
   CPID=$!
   until grep -q "Querying relay" /tmp/crawler.log; do sleep 3; done
   # snapshot stub count here, then:
   sleep 300; kill -INT $CPID
   ```
5. **Assert progress (this is the pass/fail):**
   - `grep -c 'New pubkey added to graph (signer)' /tmp/crawler.log` should be **> 0**
     (was **0** before the fix).
   - The stub count from step 3/4 should **decrease** materially (was *increasing* before).
   - `grep 'Batch complete' /tmp/crawler.log` lines should show the count of "had events"
     dropping over successive batches as the frontier (un-propagated stubs) is reached,
     rather than steady re-crawls of the same ~1,657 accounts.
6. **Convergence check (longer soak):** over a multi-hour run, `has(kind3CreatedAt)` count
   should climb well past 15,226 and the stub fraction should fall.

---

## 7. Change checklist

- [ ] `pkg/dgraph/dgraph.go` — `EnsureSchema` (`:54-66`): add `last_attempt` predicate + type field.
- [ ] `pkg/dgraph/dgraph.go` — replace `GetStalePubkeys` (`:430-468`) with the frontier-first version; add `collectStale` helper.
- [ ] `pkg/dgraph/dgraph.go` — add `MarkAttempted` (uses `ResolvePubkeysToUIDs` from `pkg/dgraph/clusterscan.go`).
- [ ] `cmd/crawler/main.go` — pass `batchSize` to `GetStalePubkeys` (`:109`); delete the manual 500-cap block (`:133-146`); call `MarkAttempted` after `FetchAndUpdateFollows` (`:152`).
- [ ] Run Fix E backfill once (after schema is applied).
- [ ] Add regression test in `pkg/dgraph/` (§5); run `make test-integration`.
- [ ] Verify per §6.
- [ ] (Optional) raise `stale_pubkey_threshold` in `~/deepfry/web-of-trust.yaml`.

### Callers to double-check

`GetStalePubkeys` gains a parameter. Grep before building:
```bash
cd web-of-trust && grep -rn "GetStalePubkeys" --include=*.go .
```
At time of writing the only caller is `cmd/crawler/main.go:109`.

# LMDB2GraphQL — Frontend Interface Contract

**Audience:** frontend engineers building UIs on top of LMDB2GraphQL.
**What this is:** the complete, code-verified contract for the interface this service exposes — every endpoint, every GraphQL type and argument, limits, error behavior, data semantics, and best practices. Verified against the implementation (`src/graphql/`, `src/server.rs`, `src/query/`, `src/config.rs`), not just the design spec.
**Status:** v1.0 — backend is feature-complete. Query-only (read-only). No mutations, no subscriptions.

---

## 1. What you're building against

LMDB2GraphQL is a **read-only GraphQL query lens** over a [strfry](https://github.com/hoytech/strfry) Nostr relay's live LMDB database. It lets you ask questions the Nostr `REQ` protocol can't express — e.g. *"the latest 20 kind-1 events per author"* — over the relay's real on-disk data.

Key facts that shape your frontend:

- **It is a query surface, not a relay and not a copy.** Reads are live against strfry's on-disk indexes. There is no separate derived store, no replication lag, and no write path.
- **Read-only.** The schema has no `Mutation` and no `Subscription` type. You cannot publish, edit, or delete events through it. For writing events, talk to a Nostr relay directly (out of scope here).
- **No realtime push.** There are no GraphQL subscriptions and no WebSocket. To show "new events," poll (see [§9 Best practices](#9-best-practices)).
- **Unauthenticated.** There is no auth, no API key, no per-user state. Whoever can reach the endpoint can run any query. Access is controlled purely by network placement (see [§3](#3-connection-transport-cors)).

---

## 2. Endpoints

| Method | Path | Purpose | Notes |
|--------|------|---------|-------|
| `POST` | `/graphql` | **Execute GraphQL queries.** This is the only data endpoint. | Returns `503` until startup gates pass. |
| `GET`  | `/graphql` | GraphiQL playground (interactive HTML query UI). | Great for exploring the schema by hand; serves no data itself. |
| `GET`  | `/health` | Liveness — always `200` while the process is alive. | Use for "is the process up." |
| `GET`  | `/ready` | Readiness — `200` only after startup gates pass, else `503`. | Use this to gate "can I query yet." |

Default bind address: **`127.0.0.1:8080`** (configurable; see [§10](#10-deployment-facts-that-affect-the-frontend)). So the default base URL is:

```
http://127.0.0.1:8080
```

### Startup readiness

Before serving data the process opens LMDB read-only, asserts `Meta.dbVersion == 3`, checks host endianness, and runs a comparator self-check. **While this is in progress, `POST /graphql` returns `503` and no data is read.** If you control app boot order, poll `GET /ready` until `200` before issuing queries. Otherwise, treat a `503` from `/graphql` as "not ready yet, retry shortly."

---

## 3. Connection, transport, CORS

### Request format

Standard GraphQL-over-HTTP. Send a `POST` to `/graphql` with `Content-Type: application/json` and a body of:

```json
{
  "query": "<graphql document>",
  "variables": { },
  "operationName": null
}
```

`variables` and `operationName` are optional. Responses follow the GraphQL spec: a top-level `data` object and/or an `errors` array.

### ⚠️ CORS is NOT configured — read this before building a browser app

The server applies **no CORS headers**. The only HTTP middleware is a request-body-size limit. This means:

- A browser frontend served from a **different origin** than the API (e.g. your app on `http://localhost:5173`, the API on `http://localhost:8080`) **will be blocked by the browser's same-origin policy.** Preflight/`Access-Control-Allow-Origin` responses are absent.
- **Workarounds (pick one):**
  1. **Reverse proxy (recommended).** Serve your frontend and proxy `/graphql` through the same origin (nginx, Caddy, Vite/Next dev proxy). The browser then sees one origin.
  2. **Dev proxy.** In Vite: `server.proxy = { '/graphql': 'http://127.0.0.1:8080' }`. In Next.js: a rewrite. This sidesteps CORS in development.
  3. **Backend-for-frontend.** Have your own server call LMDB2GraphQL server-side and expose results to the browser.
- A **server-side** consumer (Node, another service, `curl`) is unaffected — CORS is a browser policy only.

If same-origin proxying is not an option for your deployment, request that a CORS layer be added to the service (it's a small `tower-http::cors` addition) rather than working around it client-side.

### Introspection

Full schema **introspection is enabled** (that's how the GraphiQL playground works). You can point GraphQL Codegen / Apollo / urql / Relay tooling at `POST /graphql` to generate typed clients directly.

---

## 4. The schema at a glance

```graphql
type Query {
  events(filter: EventFilterInput, after: String, limit: Int): EventsPage!
  latestPerAuthor(kind: Int!, perAuthor: Int!, authors: [String!]!): [AuthorGroup!]!
  stats: StatsResult!
}

type Event {
  id: String!
  pubkey: String!
  kind: Int!          # 64-bit integer (see §8 — not 32-bit)
  createdAt: Int!     # 64-bit Unix seconds
  content: String!
  sig: String!
  tags: [[String!]!]!
  raw: String!
}

type EventsPage {
  events: [Event!]!
  endCursor: String   # null when there is no next page
  hasMore: Boolean!
}

type AuthorGroup {
  author: String!
  events: [Event!]!
}

type StatsResult {
  eventCount: Int!
  maxLevId: Int!
  dbVersion: Int!
  pinnedStrfryVersion: String!
}

input EventFilterInput {
  ids: [String!]
  authors: [String!]
  kinds: [Int!]
  since: Int
  until: Int
  tag: TagFilterInput
}

input TagFilterInput {
  name: String!
  values: [String!]!
}
```

> **Field naming:** the Rust source uses `snake_case`; async-graphql auto-renames to `camelCase` in the GraphQL schema. So you query `createdAt`, `endCursor`, `hasMore`, `eventCount`, `maxLevId`, `dbVersion`, `pinnedStrfryVersion`, `perAuthor`.

---

## 5. Types — field reference

### `Event`

| Field | GraphQL type | Description |
|-------|--------------|-------------|
| `id` | `String!` | 32-byte event id as a **64-char lowercase hex** string (NIP-01). |
| `pubkey` | `String!` | 32-byte author public key as **64-char lowercase hex**. |
| `kind` | `Int!` | Nostr event kind (e.g. `0` profile, `1` text note, `3` contacts). 64-bit — see [§8](#8-data-semantics--gotchas). |
| `createdAt` | `Int!` | Author-claimed Unix timestamp in **seconds**. 64-bit. |
| `content` | `String!` | Raw event content; interpretation depends on `kind`. |
| `sig` | `String!` | 64-byte Schnorr signature as **128-char lowercase hex**. Already verified by strfry on ingest — do not re-verify on the client. |
| `tags` | `[[String!]!]!` | Nested array. For each tag, `tags[i][0]` is the tag name (e.g. `"e"`, `"p"`, `"t"`), `tags[i][1..]` the values. |
| `raw` | `String!` | The **byte-exact JSON** strfry stored for this event. Use this if you need canonical bytes (e.g. to hash, re-sign-check, or hand to a Nostr library). Do **not** reconstruct it from the typed fields — key order and whitespace differ. |

> **`raw` vs typed fields:** the typed fields are parsed conveniences. `raw` is the source of truth strfry persisted. If you only need to render, use the typed fields. If you need to round-trip the event through Nostr tooling or verify integrity, use `raw`.

### `EventsPage`

| Field | GraphQL type | Description |
|-------|--------------|-------------|
| `events` | `[Event!]!` | The events on this page, ordered **`createdAt` DESC, then `levId` DESC** (newest first). |
| `endCursor` | `String` | Opaque pagination cursor. Pass it back as `after` to get the next page. `null` ⇒ no more pages. |
| `hasMore` | `Boolean!` | `true` if a next page exists (i.e. `endCursor` is non-null). |

### `AuthorGroup`

| Field | GraphQL type | Description |
|-------|--------------|-------------|
| `author` | `String!` | The author pubkey (64-char lowercase hex) you asked for. |
| `events` | `[Event!]!` | That author's matching events, **newest-first**, capped at `perAuthor`. |

> Authors with **zero** matching events are **omitted** from the result array — there is no empty group. Do not assume `result.length === authors.length`.

### `StatsResult`

| Field | GraphQL type | Description |
|-------|--------------|-------------|
| `eventCount` | `Int!` | Total number of events in strfry's `EventPayload` store. |
| `maxLevId` | `Int!` | Largest internal `levId` (monotonic insert counter). `0` when DB is empty. Useful as a cheap "has anything been added?" probe. |
| `dbVersion` | `Int!` | strfry on-disk DB version actually detected. Always `3` for a running instance (startup gate enforces it). |
| `pinnedStrfryVersion` | `String!` | The strfry image reference this adapter was built/configured against. Compare against your relay's actual version to detect drift. |

### `EventFilterInput` (all fields optional; combined with AND)

| Field | GraphQL type | Semantics |
|-------|--------------|-----------|
| `ids` | `[String!]` | Match any of these event ids (OR within the list). 64-char lowercase hex. |
| `authors` | `[String!]` | Match any of these author pubkeys (OR within the list). 64-char lowercase hex. |
| `kinds` | `[Int!]` | Match any of these kinds (OR within the list). Must be **non-negative**. |
| `since` | `Int` | Only events with `createdAt >= since` (Unix seconds). Must be non-negative. |
| `until` | `Int` | Only events with `createdAt <= until` (Unix seconds). Must be non-negative. |
| `tag` | `TagFilterInput` | A single tag predicate (v1 supports one tag filter only). |

**Cross-field semantics:** distinct fields are **AND**ed; values within one list field are **OR**ed. Example: `{ authors: [A, B], kinds: [1] }` ⇒ "(author A OR author B) AND kind 1".

### `TagFilterInput`

| Field | GraphQL type | Description |
|-------|--------------|-------------|
| `name` | `String!` | Single-letter tag name, e.g. `"e"` (event ref), `"p"` (pubkey ref), `"t"` (hashtag). |
| `values` | `[String!]!` | Values to match against `tags[i][1]` (OR within the list). |

> **Tag value encoding:** for `e`/`p` tags the value must be **64-char lowercase hex** (it is matched against strfry's raw 32-byte index key). For other tags (e.g. `t`) the value is matched as raw UTF-8. Mixed-case or wrong-length hex for `e`/`p` simply won't match.

> **v1 limit:** only **one** `tag` filter per query. Multi-tag AND (e.g. require both an `#e` and a `#p`) is not exposed in v1.

---

## 6. Queries — full reference with examples

All examples use `http://127.0.0.1:8080/graphql`. Adjust the host for your deployment / proxy.

### 6.1 `events` — filtered feed with cursor pagination

**Arguments**

| Arg | Type | Default | Notes |
|-----|------|---------|-------|
| `filter` | `EventFilterInput` | none (matches everything) | Omit entirely for a global newest-first feed. |
| `after` | `String` | `null` | Opaque cursor from a previous page's `endCursor`. |
| `limit` | `Int` | `100` | Max events per page. **Clamped to `[1, 500]`** silently (values >500 become 500; ≤0 become 1). |

**Returns** `EventsPage!` — events ordered `createdAt` DESC, `levId` DESC.

**Example — latest 20 kind-1 notes from two authors since a timestamp:**

```graphql
query Feed($after: String) {
  events(
    filter: {
      authors: ["79be667e...16f81798", "c6047f94...5c709ee5"]
      kinds: [1]
      since: 1700000000
    }
    limit: 20
    after: $after
  ) {
    events { id pubkey kind createdAt content tags }
    hasMore
    endCursor
  }
}
```

**`curl`:**

```bash
curl -s http://127.0.0.1:8080/graphql \
  -H 'content-type: application/json' \
  -d '{
    "query": "query($after:String){ events(filter:{kinds:[1]}, limit:20, after:$after){ events{ id createdAt content } hasMore endCursor } }",
    "variables": { "after": null }
  }'
```

**`fetch` (server-side or same-origin browser):**

```js
async function getEvents(variables) {
  const res = await fetch("/graphql", {            // same-origin path; see §3 CORS
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      query: `query($filter: EventFilterInput, $after: String, $limit: Int) {
        events(filter: $filter, after: $after, limit: $limit) {
          events { id pubkey kind createdAt content tags }
          hasMore
          endCursor
        }
      }`,
      variables,
    }),
  });
  const json = await res.json();
  if (json.errors) throw new Error(json.errors[0].message);
  return json.data.events;
}
```

**Pagination loop:**

```js
let after = null;
const all = [];
do {
  const page = await getEvents({ filter: { kinds: [1] }, limit: 100, after });
  all.push(...page.events);
  after = page.endCursor;
} while (after); // stop when endCursor is null (equivalently: hasMore === false)
```

> **Cursor rules:** treat `endCursor` as **opaque** (it is base64 of internal sort keys — never parse or construct it). Pass it back verbatim. A malformed cursor returns an error with code `INVALID_CURSOR` (see [§7](#7-errors)). Cursors are stable for stable data but are not guaranteed meaningful across very different filters — always paginate with the *same* `filter` you started with.

### 6.2 `latestPerAuthor` — top-N per author, grouped

The signature query the Nostr `REQ` protocol cannot express.

**Arguments**

| Arg | Type | Default | Notes |
|-----|------|---------|-------|
| `kind` | `Int!` | (required) | Single kind to fetch. Must be non-negative. |
| `perAuthor` | `Int!` | (required) | Max events per author. **Clamped to `[1, 500]`** silently. |
| `authors` | `[String!]!` | (required) | Author pubkeys (64-char lowercase hex). **Max 1000 authors** — more returns `TOO_MANY_AUTHORS`. |

**Returns** `[AuthorGroup!]!` — one group per author that has matches (authors with no matches are omitted). Each group's `events` are newest-first.

```graphql
query {
  latestPerAuthor(kind: 1, perAuthor: 20, authors: ["<pk1>", "<pk2>"]) {
    author
    events { id createdAt content }
  }
}
```

```bash
curl -s http://127.0.0.1:8080/graphql \
  -H 'content-type: application/json' \
  -d '{"query":"{ latestPerAuthor(kind:1, perAuthor:5, authors:[\"79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798\"]){ author events{ id createdAt } } }"}'
```

> **Cost awareness:** total work ≈ `authors.length × perAuthor` index scans. The author count is capped at **1000** and `perAuthor` at **500**, but a 1000×500 request is still heavy. Keep both as small as your UI needs (a follow-feed wall typically wants `perAuthor: 1`–`3`).

### 6.3 `stats` — counts, version, drift

```graphql
query {
  stats { eventCount maxLevId dbVersion pinnedStrfryVersion }
}
```

Use `eventCount` for a corpus-size indicator and `maxLevId` as a cheap change-detection probe (it strictly increases as events are added). `pinnedStrfryVersion` vs your relay's real version surfaces compatibility drift.

---

## 7. Errors

The response is always HTTP `200` for a query that *reaches* the resolver (GraphQL convention) — application errors arrive in the top-level `errors` array, not the HTTP status. Non-200 statuses come from the transport layer.

### GraphQL-level errors (`errors[]` in the body)

| `extensions.code` | When | What the frontend should do |
|-------------------|------|-----------------------------|
| `INVALID_CURSOR` | `after` cursor is malformed / wrong length. | Drop the cursor and restart pagination from page 1. Usually a client bug — never hand-build cursors. |
| `TOO_MANY_AUTHORS` | `latestPerAuthor` got more than **1000** authors. | Batch the author list into chunks of ≤1000 and merge results client-side. |
| *(no code)* `"internal error"` | An LMDB/decode error occurred server-side. Details are deliberately **not** leaked. | Show a generic failure; retry with backoff. Check server logs for the real cause. |
| *(no code)* validation messages | e.g. `"kind must be a non-negative integer"`, `"since must be <= until"`, `"since/until must be a non-negative Unix timestamp"`. | Fix the input. These are caught before any DB work. |

Errors carry a human-readable `message`; machine-actionable ones also set `extensions.code`. Example error body:

```json
{ "errors": [ { "message": "invalid cursor: expected 16 bytes, got 3",
                "extensions": { "code": "INVALID_CURSOR" } } ] }
```

### Transport-level statuses

| HTTP status | Meaning | Frontend action |
|-------------|---------|-----------------|
| `503 Service Unavailable` | Startup gates not yet passed (schema not ready). | Wait and retry; gate on `GET /ready`. |
| `413 Payload Too Large` | Request body exceeded **256 KiB**. | Shrink the query — usually a giant `authors`/`ids` array. Split into multiple requests. |
| `200 OK` | Query reached the engine (may still contain `errors`). | Inspect `errors` before trusting `data`. |

Always check `json.errors` even on `200`.

---

## 8. Data semantics & gotchas

- **Hex everywhere, lowercase.** `id`, `pubkey`, `sig`, and `e`/`p` tag values are lowercase hex of fixed length (64/64/128/64). If your UI handles `npub`/`nsec`/`note` bech32 forms, convert to hex before querying and convert back for display. This service speaks hex only.
- **64-bit integers.** `kind` and `createdAt` are mapped to a 64-bit integer scalar (not GraphQL's 32-bit `Int`) to avoid truncation. In JS, values stay within safe-integer range for real Nostr data, but if you use codegen, treat them as numbers/bigint-safe.
- **Ordering is fixed.** `events` is always `createdAt` DESC then internal `levId` DESC. There is **no `orderBy` argument** in the implemented API. If you need ascending or other orders, sort client-side after fetching.
- **Timestamps are author-claimed.** `createdAt` is what the event author asserted, not when the relay received it. Two events can share a `createdAt`; the `levId` tiebreaker keeps ordering and pagination stable.
- **Live data, query-time snapshot.** Each query reads a short, consistent snapshot of strfry's current on-disk state. Deleted/replaced events that strfry has actually removed won't appear. There's no stale derived copy. Between two queries the corpus may change (new events, deletions) — design pagination to tolerate that (cursors are anchored to sort keys, not offsets).
- **Expired events are filtered.** NIP-40 expired events (`expiration` tag in the past) are dropped at query time. You won't see them.
- **Replaceable events (kinds 0, 3, 10000–19999; parameterized 30000–39999):** you see whatever strfry physically retains. strfry enforces replacement on its write path, so normally only the current version is present.
- **`raw` is canonical, typed fields are derived.** See [§5](#5-types--field-reference). Never reconstruct `raw` from typed fields.
- **Empty groups omitted.** `latestPerAuthor` skips authors with no matches — match results back to your requested list by `author`, don't zip by index.

---

## 9. Best practices

1. **Proxy to avoid CORS.** Serve the UI and the API from one origin (reverse proxy or dev proxy). See [§3](#3-connection-transport-cors).
2. **Gate first queries on `/ready`** (or treat `503` as "retry soon"). Don't blast `/graphql` during boot.
3. **Always pass `limit`.** Default is 100; the hard ceiling is 500. Request the smallest page your UI needs — large pages cost more LMDB work and bigger payloads.
4. **Paginate with `endCursor`, never offsets.** Loop until `hasMore` is `false` / `endCursor` is `null`. Keep `filter` constant across pages.
5. **Keep `latestPerAuthor` lean.** Cost ≈ `authors × perAuthor`. Chunk author lists at ≤1000 (the hard cap) and prefer small `perAuthor`.
6. **Select only the fields you render.** Skip `raw` (and even `content`/`tags`) when you don't need them — `raw` can be large. The schema has a depth limit of 12 and a complexity limit of 2000, and many aliased top-level queries in one request count against complexity.
7. **Poll for "new," don't expect push.** There's no subscription. To detect new events cheaply, poll `stats.maxLevId` (or your feed's first page) on an interval; re-fetch when it changes. Use a sensible interval (seconds, not milliseconds).
8. **Handle `errors` on every response,** even HTTP `200`. Branch on `extensions.code` for `INVALID_CURSOR` / `TOO_MANY_AUTHORS`; show generic UI for `"internal error"`.
9. **Cap request size.** Body limit is 256 KiB — split huge `ids`/`authors` arrays across requests.
10. **Generate a typed client.** Introspection is on; point GraphQL Codegen / urql / Apollo at `/graphql` for end-to-end types.
11. **Treat the service as a single-relay corpus.** It reflects exactly one strfry DB. It is not aggregating multiple relays.

---

## 10. Deployment facts that affect the frontend

- **Default bind:** `127.0.0.1:8080` (loopback only) — intentionally not public, because the endpoint is unauthenticated with full introspection and a GraphiQL playground. Operators must opt into a wider `bind_address` (e.g. `0.0.0.0:8080`), and the process logs a loud warning when they do.
- **In Docker (DeepFry stack):** the container sets `bind_address: 0.0.0.0:8080`; host exposure is controlled by the compose publish rule (`127.0.0.1:8080:8080` by default). Other stack services reach it by container name over the `deepfry-net` network.
- **Healthchecks:** stack compose uses `GET /health`. For your own "can I query" logic use `GET /ready`.
- **No auth and no rate limiting at the app layer.** If your frontend is public, put the service behind your own gateway (auth, CORS, rate limiting, TLS). Don't expose `0.0.0.0` directly to untrusted networks.

---

## 11. Quick UI recipes

- **Global firehose (newest-first):** `events(limit: 50)` with no filter; paginate with `endCursor`.
- **Single author's notes:** `events(filter: { authors: [pk], kinds: [1] }, limit: 50)`.
- **Profile cards for a follow list:** `latestPerAuthor(kind: 0, perAuthor: 1, authors: [...])` → newest profile (kind 0) per author.
- **Follow-feed wall:** `latestPerAuthor(kind: 1, perAuthor: 3, authors: [...])` then merge + sort client-side by `createdAt`.
- **Thread replies (events referencing an event):** `events(filter: { tag: { name: "e", values: [eventIdHex] } }, limit: 100)`.
- **Hashtag stream:** `events(filter: { tag: { name: "t", values: ["nostr"] } }, limit: 50)`.
- **Mentions of a user:** `events(filter: { tag: { name: "p", values: [pubkeyHex] } } })`.
- **Corpus dashboard:** `stats { eventCount maxLevId dbVersion pinnedStrfryVersion }`, polled for `maxLevId` changes.

---

## 12. Summary of limits & defaults (cheat sheet)

| Thing | Value | Where enforced |
|-------|-------|----------------|
| `events.limit` default | `100` | resolver |
| `events.limit` ceiling | `500` (silent clamp; ≤0 → 1) | resolver |
| `latestPerAuthor.perAuthor` ceiling | `500` (silent clamp; ≤0 → 1) | resolver |
| `latestPerAuthor.authors` max | `1000` → else `TOO_MANY_AUTHORS` | resolver |
| Request body max | `256 KiB` → else `413` | HTTP layer |
| Query depth max | `12` | schema |
| Query complexity max | `2000` | schema |
| Ordering | `createdAt` DESC, `levId` DESC (fixed; no `orderBy`) | engine |
| Auth | none | — |
| CORS | **none configured** (proxy required for cross-origin browsers) | — |
| Realtime/subscriptions | none (poll instead) | — |
| Mutations | none (read-only) | — |

---

*Generated from the implemented code (`src/graphql/{types,resolvers,schema}.rs`, `src/server.rs`, `src/query/{engine,filter}.rs`, `src/config.rs`) on 2026-06-23. If the service is upgraded, re-verify limits, types, and CORS status against the source — this contract reflects v1.0.*

# Frontend Specification — LMDB2GraphQL Explorer

**Audience:** A frontend engineer building a basic web UI that exercises **every** feature of
the LMDB2GraphQL adapter.
**Status:** Build-ready. Every type, argument, limit, and error below was read from the live
Rust source (`src/graphql/*.rs`, `src/server.rs`, `src/config.rs`) — not invented.
**Last verified:** 2026-06-16 against strfry DB version 3.

---

## 1. What you are building

LMDB2GraphQL is a **read-only** GraphQL adapter over a strfry Nostr relay's on-disk LMDB
database. It exposes rich queries the Nostr `REQ` protocol cannot express (e.g. "latest 20
kind-1 events per author"). There are **no mutations, no subscriptions, no auth** — it is a
query surface.

Your job: a small single-page app — call it the **LMDB2GraphQL Explorer** — that lets a human
drive all three queries the backend exposes (`events`, `latestPerAuthor`, `stats`), with
pagination, filtering, and the error states the backend can return. "Basic" = correct and
complete coverage, not polished product design.

---

## 2. Backend contract (read this first)

### 2.1 HTTP surface

| Method & Path   | Purpose                                                              |
|-----------------|---------------------------------------------------------------------|
| `POST /graphql` | GraphQL queries. Returns `503` until the backend finishes startup gates. |
| `GET /graphql`  | **GraphiQL playground** (HTML). Use this to introspect the live schema. |
| `GET /health`   | Liveness — always `200`. (Not needed by the UI, but useful.)        |
| `GET /ready`    | Readiness — `200` when ready, `503` during startup.                 |

- **Default bind address:** `127.0.0.1:8080` (loopback). Configurable via
  `~/deepfry/lmdb2graphql.yaml` → `bind_address`.
- **Request body limit:** 256 KiB. A query/variables document over this returns `413`.
- **Introspection:** enabled. `GET /graphql` gives you GraphiQL — treat that SDL as the
  ultimate source of truth and sanity-check this doc against it.
- **Query guards:** depth limited to 12, complexity limited to 2000. A normal Explorer query
  is nowhere near these; don't auto-generate deeply nested or many-aliased queries.

### 2.2 ⚠️ CORS — the one thing that will block you

**The backend ships NO CORS headers** (`tower-http` is compiled with the `limit` feature only;
there is no `CorsLayer`). A browser app served from any origin other than the backend's own
`http://127.0.0.1:8080` will have its `POST /graphql` requests **blocked by the browser**.

You have two supported ways around this — **do not ask the backend team to add CORS just for
dev**:

1. **Vite dev proxy (recommended for development).** Proxy `/graphql` to the backend so the
   browser only ever talks to the Vite origin (same-origin → no CORS):

   ```ts
   // vite.config.ts
   import { defineConfig } from "vite";
   import react from "@vitejs/plugin-react";

   export default defineConfig({
     plugins: [react()],
     server: {
       proxy: {
         "/graphql": {
           target: "http://127.0.0.1:8080",
           changeOrigin: true,
         },
       },
     },
   });
   ```

   Then point the GraphQL client at the **relative** path `/graphql`.

2. **Reverse proxy (production).** In the DeepFry deployment, serve the built static assets
   and reverse-proxy `/graphql` to the adapter from the same origin (nginx/Caddy/Traefik).
   Same-origin again → no CORS needed.

Make the GraphQL endpoint URL a single env var (`VITE_GRAPHQL_URL`, default `/graphql`) so dev
(proxy) and prod (reverse proxy) both work without code changes.

### 2.3 No authentication

The endpoint is unauthenticated and serves the entire relay corpus. Don't build login screens.
Don't store secrets. Assume the deployment restricts network access (loopback / compose network).

### 2.4 Running the backend locally

```bash
# 1. Create ~/deepfry/lmdb2graphql.yaml (first interactive run prompts for the strfry DB path),
#    or copy the example:
cp config/lmdb2graphql.yaml.example ~/deepfry/lmdb2graphql.yaml
#    then edit strfry_db_path to point at a directory containing data.mdb

# 2. Run it
cargo run --release
# Serves on http://127.0.0.1:8080 — open http://127.0.0.1:8080/graphql for GraphiQL
```

If you have no strfry DB, the repo ships a tiny fixture at `tests/fixture/` (11 events) — the
backend team can point a local instance at it so you have real data to render.

---

## 3. GraphQL schema reference (authoritative)

This is the exact contract. Field names are the camelCase names async-graphql emits in the SDL.

```graphql
type Query {
  # Filtered event feed with opaque cursor pagination.
  events(filter: EventFilterInput, after: String, limit: Int): EventsPage!

  # Latest N events per author, for a single kind. Grouped by author.
  latestPerAuthor(kind: Int!, perAuthor: Int!, authors: [String!]!): [AuthorGroup!]!

  # Corpus statistics + version info.
  stats: StatsResult!
}

type Event {
  id: String!            # 32-byte event id, 64-char lowercase hex (NIP-01)
  pubkey: String!        # 32-byte author pubkey, 64-char lowercase hex
  kind: Int!             # Nostr kind (64-bit int scalar; e.g. 0 metadata, 1 note, 3 contacts)
  createdAt: Int!        # Unix seconds the author claims (64-bit int scalar)
  content: String!       # arbitrary content; meaning depends on kind
  sig: String!           # 64-byte Schnorr sig, 128-char lowercase hex
  tags: [[String!]!]!    # nested list; tags[i][0] = tag name, tags[i][1..] = values
  raw: String!           # byte-exact JSON strfry stored — render verbatim, never re-encode
}

type EventsPage {
  events: [Event!]!
  endCursor: String      # opaque base64 cursor; null when no further pages
  hasMore: Boolean!      # true when another page exists
}

type AuthorGroup {
  author: String!        # pubkey (64-char lowercase hex)
  events: [Event!]!      # newest-first, bounded by perAuthor
}

type StatsResult {
  eventCount: Int!           # total events in the DB
  maxLevId: Int!             # largest internal levId (monotonic insert counter); 0 if empty
  dbVersion: Int!            # strfry Meta.dbVersion — always 3 (startup gate enforces)
  pinnedStrfryVersion: String! # configured strfry image ref (drift detection)
}

input EventFilterInput {
  ids: [String!]         # OR within list; 64-char lowercase hex
  authors: [String!]     # OR within list; 64-char lowercase hex
  kinds: [Int!]          # OR within list; must be >= 0
  since: Int             # createdAt >= since (Unix seconds, >= 0)
  until: Int             # createdAt <= until (Unix seconds, >= 0)
  tag: TagFilterInput    # single tag filter (multi-tag is a future expansion)
}

input TagFilterInput {
  name: String!          # tag letter, e.g. "e", "p", "t"
  values: [String!]!     # OR within list
}
```

> **Number sizes:** `kind`, `createdAt`, `eventCount`, `maxLevId` are 64-bit on the wire but
> arrive as JSON numbers. All realistic values are well within JavaScript's safe-integer range
> (2^53), so plain `number` is fine. Don't truncate; don't assume 32-bit.

### 3.1 Argument rules & limits (enforced server-side)

| Query             | Arg          | Rule                                                                 |
|-------------------|--------------|----------------------------------------------------------------------|
| `events`          | `limit`      | Optional. Default **100**. Silently clamped to **[1, 500]**.         |
| `events`          | `after`      | Opaque cursor from a previous page's `endCursor`. Malformed → error. |
| `events`          | `filter`     | All sub-fields optional; combined with **AND** across dimensions.    |
| `latestPerAuthor` | `kind`       | **Required.** Must be `>= 0`.                                        |
| `latestPerAuthor` | `perAuthor`  | **Required.** Silently clamped to **[1, 500]**.                      |
| `latestPerAuthor` | `authors`    | **Required.** Max **1000** authors, else `TOO_MANY_AUTHORS` error.   |

**Filter semantics:** within a single list (e.g. `authors`) values are OR'd; across different
filter dimensions (e.g. `authors` AND `kinds` AND `since`) they are AND'd. This matches NIP-01.

### 3.2 Errors the backend can return

GraphQL errors arrive in the standard `errors[]` array. Handle these explicitly:

| Trigger                                        | Message / `extensions.code`                     |
|------------------------------------------------|-------------------------------------------------|
| Bad `after` cursor                             | `"invalid cursor: …"`, `code: "INVALID_CURSOR"` |
| `latestPerAuthor` authors > 1000               | `"too many authors: max 1000"`, `code: "TOO_MANY_AUTHORS"` |
| Negative `kind` (filter or `latestPerAuthor`)  | `"kind must be a non-negative integer"`         |
| Negative `since`/`until`                       | `"… must be a non-negative Unix timestamp"`     |
| `since > until`                                | `"since must be <= until"`                      |
| Internal LMDB / decode failure                 | `"internal error"` (opaque by design — do not expect detail) |
| Backend still starting up                      | HTTP **503** on `POST /graphql` (not a GraphQL error) |
| Request body > 256 KiB                         | HTTP **413**                                    |

The UI should: prefer the `extensions.code` when present (stable), fall back to the message,
and special-case the `503`/`413` HTTP statuses with a "backend not ready / request too large"
message.

---

## 4. Feature coverage matrix

Every adapter capability must be reachable from the UI. This is the acceptance checklist.

| # | Backend feature                                  | Query / field exercised                          | UI surface (section below) |
|---|--------------------------------------------------|--------------------------------------------------|----------------------------|
| 1 | Corpus stats + version/drift info                | `stats { eventCount maxLevId dbVersion pinnedStrfryVersion }` | §6.1 Stats bar |
| 2 | Filter by event ids                              | `events(filter: { ids: [...] })`                 | §6.2 Events Explorer |
| 3 | Filter by authors                                | `events(filter: { authors: [...] })`             | §6.2 |
| 4 | Filter by kinds                                  | `events(filter: { kinds: [...] })`               | §6.2 |
| 5 | Time-range filter (since/until)                  | `events(filter: { since, until })`               | §6.2 |
| 6 | Single tag filter                                | `events(filter: { tag: { name, values } })`      | §6.2 |
| 7 | Per-page limit (1–500, default 100)              | `events(limit:)`                                 | §6.2 |
| 8 | Cursor pagination (load more)                    | `events(after:)` + `endCursor`/`hasMore`         | §6.2 |
| 9 | Full event display incl. raw JSON + tags         | `Event { …, tags, raw }`                          | §6.4 Event detail |
| 10| Latest-N-per-author (the query REQ can't express)| `latestPerAuthor(kind, perAuthor, authors)`      | §6.3 Latest-per-author |
| 11| `TOO_MANY_AUTHORS` guard surfaced                | error handling                                   | §6.3 + §7 |
| 12| `INVALID_CURSOR` guard surfaced                  | error handling                                   | §6.2 + §7 |
| 13| Validation errors (negative kind/ts, since>until)| error handling                                   | §6.2 + §7 |
| 14| 503-not-ready handling                           | HTTP status                                      | §6.1 + §7 |

If all 14 rows are demonstrably exercisable in the UI, the milestone is complete.

---

## 5. Recommended stack

Keep it lean. This is a tool, not a product.

| Concern        | Choice                                  | Notes |
|----------------|-----------------------------------------|-------|
| Build/dev      | **Vite** + **React** + **TypeScript**   | Fast; the dev proxy (§2.2) is essential. |
| UI components  | **shadcn/ui** (Radix + Tailwind)        | **Use existing shadcn components only — do not author new bespoke components.** |
| GraphQL client | **graphql-request** + **@tanstack/react-query** | Tiny. `useInfiniteQuery` maps 1:1 onto the `after`/`endCursor`/`hasMore` cursor model. (urql/Apollo are fine if preferred, but heavier.) |
| Toasts/errors  | shadcn **Sonner** (toast)               | Surface GraphQL/HTTP errors here. |
| Forms          | Native inputs + shadcn `Input`/`Select`/`Button` | No form library needed for this scope. |

shadcn components you'll reuse (all already part of the shadcn catalog — install, don't invent):
`Card`, `Table`, `Tabs`, `Input`, `Textarea`, `Button`, `Badge`, `Select`, `Dialog`/`Sheet`,
`Skeleton`, `Sonner` (toast), `Tooltip`, `Accordion`, `Separator`, `ScrollArea`.

---

## 6. UI specification

Single page, three primary sections under a persistent stats bar. Use shadcn `Tabs` to switch
between **Events** and **Latest per Author**.

### 6.1 Stats bar (always visible, top)

- On load, run `stats`. Render in a shadcn `Card` (or a row of small `Card`s / `Badge`s):
  - `eventCount` (format with thousands separators)
  - `maxLevId`
  - `dbVersion` — render a green `Badge` if `=== 3`, red otherwise (it is always 3 in practice;
    a non-3 means a misconfigured/incompatible backend).
  - `pinnedStrfryVersion` — show truncated with a `Tooltip` revealing the full digest.
- A small **status dot**: poll `GET /ready` (or treat a `503` from any `POST /graphql` as
  "not ready"). Green = ready, amber = starting up. If not ready, show a `Skeleton` + a
  "Backend starting…" message instead of empty results.
- A manual **Refresh** `Button`.

### 6.2 Events Explorer (default tab)

**Filter panel** (a `Card` with a grid of inputs — all optional):

| Control                | Maps to                | Validation (client-side, before sending) |
|------------------------|------------------------|-------------------------------------------|
| `ids` (multi)          | `filter.ids`           | each 64-char lowercase hex |
| `authors` (multi)      | `filter.authors`       | each 64-char lowercase hex |
| `kinds` (multi-number) | `filter.kinds`         | integers ≥ 0 |
| `since` (datetime)     | `filter.since`         | convert to Unix **seconds**; ≥ 0 |
| `until` (datetime)     | `filter.until`         | ≥ 0; if both set, enforce `since ≤ until` and warn early |
| tag `name` + `values`  | `filter.tag`           | name = single letter typical; values OR'd |
| `limit` (number)       | `limit`                | default 100; hint that >500 is clamped to 500 |

- For multi-value inputs use a simple "type + Enter to add a chip" pattern with shadcn `Badge`
  chips (removable). Don't build a heavyweight tag editor.
- **Apply** `Button` runs the query. **Reset** clears all filters (→ `events` with no filter =
  newest events, default limit 100).
- Empty filter is valid and expected — it returns the newest events.

**Results** (shadcn `Table`):

- Columns: `createdAt` (render as human date + raw seconds in a `Tooltip`), `kind` (`Badge`,
  with a friendly label for common kinds: 0=Metadata, 1=Note, 3=Contacts, 7=Reaction), short
  `id` (first 12 hex + ellipsis, copy-on-click), short `pubkey`, a `content` preview (truncated),
  tag count `Badge`, and a "Details" button (→ §6.4).
- **Pagination = "Load more"** (cursor model, NOT page numbers — see §6.5). Show a
  `Button` that is enabled only when `hasMore`; clicking fetches the next page with `after:
  endCursor` and **appends** to the table. Show total loaded count.
- Loading state: shadcn `Skeleton` rows on first fetch; spinner on the Load-more button for
  subsequent pages.
- Empty state: "No events match this filter."

### 6.3 Latest per Author (tab)

This showcases the query the Nostr `REQ` protocol cannot express — make it prominent.

**Inputs** (a `Card`):

- `authors` — multi (chips); **required**, ≥ 1. Show a live count and **block submission at
  > 1000** with an inline message ("max 1000 authors") so you never round-trip a guaranteed
  `TOO_MANY_AUTHORS` error (but still handle that error if it comes back — §7).
- `kind` — single number; **required**; ≥ 0. A `Select` with common kinds (0/1/3/7) plus a
  free number input.
- `perAuthor` — number; **required**; default e.g. 5; note clamp to [1, 500].

**Results:**

- One collapsible group per `AuthorGroup` using shadcn `Accordion`: header = short pubkey +
  event count `Badge`; body = the author's events (newest-first) in the same compact row layout
  as §6.2, each with a Details button.
- Note: authors with no matching events simply won't appear in the result — show a note if the
  returned group count is fewer than the requested author count.
- Empty state: "No events for these authors / kind."

### 6.4 Event detail (Dialog or Sheet)

Triggered from any "Details" button. shadcn `Dialog` (or `Sheet`) containing:

- All typed fields: `id`, `pubkey`, `kind`, `createdAt` (formatted + raw), `sig`.
- **Tags**: render `tags` (`[[String!]!]`) as a readable list/table — tag name in a `Badge`,
  values after it. Handle empty tags.
- **Raw JSON**: show `raw` verbatim in a monospaced `ScrollArea` (`<pre>`), with a **Copy**
  button. ⚠️ Render `raw` exactly as received — **do not** `JSON.parse` then re-stringify it
  for display; the backend deliberately returns byte-exact bytes and key order/whitespace
  matter. (You may `JSON.parse` a *separate* copy if you want pretty-printing as an optional
  toggle, but always offer the verbatim view.)
- Copy-to-clipboard on `id` and `pubkey`.

### 6.5 Pagination model (important — read carefully)

The `events` query uses **opaque forward cursors**, not offset/page numbers:

1. First fetch: `events(filter, limit)` → `{ events, endCursor, hasMore }`.
2. Next page: `events(filter, limit, after: <previous endCursor>)`.
3. Stop when `hasMore === false` (and `endCursor` will be `null`).

Rules:
- **Forward-only.** There is no "previous page". A "Load more / append" UX is the correct fit
  (TanStack `useInfiniteQuery` with `getNextPageParam: (last) => last.events.endCursor ?? undefined`).
- **The cursor is tied to the filter.** If the user changes any filter or the limit, **discard
  the cursor and start fresh** from page 1. Never carry an `endCursor` across a filter change.
- Treat the cursor as opaque — never parse, construct, or mutate it. A bad cursor returns
  `INVALID_CURSOR`.

---

## 7. Error handling spec

Centralize this in the GraphQL client wrapper:

- **HTTP 503** (POST /graphql) → "Backend is still starting up." Show the not-ready state in the
  stats bar; offer retry. Do not surface as a scary error.
- **HTTP 413** → "Query too large (max 256 KiB)." (Only reachable with absurd filter lists.)
- **GraphQL `errors[]`**: read `errors[0].extensions.code` first:
  - `INVALID_CURSOR` → toast "Pagination cursor expired/invalid — reloading from start." Then
    reset the cursor and re-fetch page 1.
  - `TOO_MANY_AUTHORS` → inline error on the authors input.
  - No code (validation/internal) → toast the message verbatim (e.g. "since must be <= until",
    "kind must be a non-negative integer", "internal error").
- **Partial data:** GraphQL can return both `data` and `errors`. If `data` is present, render it
  and still surface the error toast.

---

## 8. Project setup (handoff checklist for the engineer)

```bash
npm create vite@latest lmdb2graphql-explorer -- --template react-ts
cd lmdb2graphql-explorer
npm i graphql graphql-request @tanstack/react-query
# Tailwind + shadcn/ui init (follow shadcn docs), then add the components listed in §5.
npx shadcn@latest add card table tabs input textarea button badge select dialog sheet \
  skeleton sonner tooltip accordion separator scroll-area
```

- Add the Vite proxy from §2.2.
- `VITE_GRAPHQL_URL` env var, default `/graphql`.
- Single `gqlClient` module wrapping `graphql-request` with the centralized error handling (§7).
- Optionally generate types from the live schema (`graphql-codegen` pointed at
  `http://127.0.0.1:8080/graphql`) — recommended since introspection is on.

---

## 9. Acceptance criteria (definition of done)

- [ ] Stats bar shows `eventCount`, `maxLevId`, `dbVersion` (3 = green), `pinnedStrfryVersion`.
- [ ] Not-ready (`503`) state is handled gracefully (no crash, clear message).
- [ ] Events Explorer can filter by **each** of: ids, authors, kinds, since/until, single tag —
      individually and in combination (AND across dimensions).
- [ ] `limit` is adjustable; default 100 behavior confirmed; >500 visibly clamps.
- [ ] "Load more" cursor pagination works and appends; stops at `hasMore === false`; resets on
      filter change.
- [ ] Changing a filter discards the old cursor (no stale-cursor bugs).
- [ ] Event detail shows typed fields, tags rendered readably, and `raw` JSON **verbatim** with copy.
- [ ] Latest-per-author works with required `kind` + `perAuthor` + `authors`; results grouped per author.
- [ ] `> 1000` authors is blocked client-side AND a returned `TOO_MANY_AUTHORS` is handled.
- [ ] `INVALID_CURSOR`, negative-kind, negative-timestamp, and `since > until` errors are surfaced clearly.
- [ ] Works through the Vite proxy with the backend on loopback (no CORS errors in console).

---

## 10. Out of scope

- Authentication / user accounts (endpoint is unauthenticated by design).
- Writing/publishing events, deletions, reactions (no mutations exist).
- Real-time updates / subscriptions (none exist; request/response only).
- Backward pagination / jump-to-page (cursors are forward-only).
- Multi-tag AND filtering (`filter.tag` is single-tag in this version).
- Signature verification or Nostr crypto (strfry already verified on ingest).
```

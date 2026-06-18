# Phase 4: GraphQL API - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-13
**Phase:** 4-GraphQL API
**Areas discussed:** Event representation, Pagination contract, Limit ceiling & guards, latestPerAuthor shape

---

## Event representation

### Event shape
| Option | Description | Selected |
|--------|-------------|----------|
| Typed fields + raw escape hatch | Typed id/pubkey/kind/createdAt/content/sig fields PLUS a `raw` JSON-string field | ✓ |
| Typed fields only | Structured fields only; clients never see strfry's exact bytes | |
| Raw JSON only | Single `raw` JSON-string field; clients parse themselves | |

**User's choice:** Typed fields + raw escape hatch.

### Tags type
| Option | Description | Selected |
|--------|-------------|----------|
| Nested list [[String!]!] | Native GraphQL nested list, fully typed, selectable | ✓ |
| JSON scalar | Opaque custom scalar — flexible, but loses schema typing | |
| Defer to raw only | No typed tags field; read from `raw` | |

**User's choice:** Nested list `[[String!]!]`.

---

## Pagination contract

| Option | Description | Selected |
|--------|-------------|----------|
| Simple page object | `{ events, endCursor, hasMore }` + `after` arg; maps 1:1 to engine return | ✓ |
| Relay Connection | Full edges/node/pageInfo spec | |

**User's choice:** Simple page object.
**Notes:** `hasMore` derives from the engine's `Option<PageCursor>`; consumer passes the opaque cursor back as `after`.

---

## Limit ceiling & guards

### Ceiling + default
| Option | Description | Selected |
|--------|-------------|----------|
| Cap 500 / default 100 | Max 500/page (silent cap), default 100 when omitted | ✓ |
| Cap 1000 / default 100 | Higher ceiling; more hydration under load | |
| Cap 256 / default 256 | Align with engine DEFAULT_WINDOW_SIZE | |

**User's choice:** Cap 500 / default 100. Capped silently (API-05: capped, not rejected).

### DoS guards
| Option | Description | Selected |
|--------|-------------|----------|
| Yes — depth + complexity caps | Enable async-graphql depth/complexity limits | |
| No — ceiling is enough | Rely on limit ceiling + engine MAX_ROUNDS bound | ✓ |

**User's choice:** No — ceiling is enough.
**Notes:** Schema is shallow, so depth abuse is limited. Flagged to revisit if exposed to untrusted public traffic.

---

## latestPerAuthor shape

### Result shape
| Option | Description | Selected |
|--------|-------------|----------|
| List of author groups | `[{ author, events }]` — preserves D-12 grouping | ✓ |
| Flat list of events | One `[Event!]!`; loses grouping | |

**User's choice:** List of author groups.

### Bounds
| Option | Description | Selected |
|--------|-------------|----------|
| Cap perAuthor + cap author count | Bound both for total-work bound | |
| Cap perAuthor only | Bound perAuthor; accept any number of authors | ✓ |
| You decide the numbers | Two-cap approach, numbers TBD in planning | |

**User's choice:** Cap perAuthor only.
**Notes:** Author-count fan-out left uncapped by explicit choice; flagged as an observability concern for Phase 5 / v2.

---

## Claude's Discretion

- GraphQL/HTTP module layout within the locked stack.
- `QueryError` → GraphQL error mapping.
- HTTP endpoint path / bind / router shape (config-driven).
- `spawn_blocking` for synchronous heed calls.
- Sharing `heed::Env` + `DictCache` into resolvers (async-graphql `Data`/context).
- `Event` field nullability details.
- `stats` resolver internals (count via `mdb_stat`, max levId = last key, dbVersion from `Meta`).
- GraphiQL playground + schema introspection on/off (default: enabled for read-only service).

## Deferred Ideas

- async-graphql depth/complexity limits (revisit for untrusted public traffic).
- Bounding `latestPerAuthor` author-count fan-out (observability — Phase 5 / v2).
- Relay Connection pagination (future API revision if tooling compat needed).
- Subscriptions / REST facade / Prometheus metrics (v2).
- Doc-sync: stale rusqlite/SQLite wording in CLAUDE.md (not Phase 4 code work).

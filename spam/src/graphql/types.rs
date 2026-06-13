/// types.rs — GraphQL output and input type definitions for lmdb2graphql.
///
/// Implements the GraphQL schema type contracts for Phase 4 (API-01..API-06).
/// All types here are purely structural — no query logic, no resolver wiring.
/// Resolvers that call the engine live in Plan 04-02 (resolvers.rs).
///
/// Decisions implemented here:
///   D-01: Typed fields + `raw: String!` escape hatch on Event
///   D-02: tags as [[String!]!] native nested list (no JSON scalar)
///   D-03: Simple page object (events, endCursor, hasMore)
///   D-07: latestPerAuthor returns [{ author, events }]
///   D-09: stats returns { eventCount, maxLevId, dbVersion }
///   D-11: opaque cursor = PageCursor::encode() (used by EventsPage.end_cursor)
///
/// Security (T-04-INT): u64 → i64 casts for kind/created_at (Pitfall 2 — GraphQL Int is 32-bit;
/// i64 maps to the 64-bit integer scalar). Nostr kinds/timestamps stay within i64 range.
/// Security (T-04-RAW): raw field deliberately exposes exact bytes strfry stores publicly (D-01).
use async_graphql::{InputObject, SimpleObject};

use crate::lmdb::types::DecodedEvent;

/// A single Nostr event as returned by the GraphQL API (D-01/D-02).
///
/// Fields `kind` and `created_at` are `i64` (not `u64`) because GraphQL's `Int`
/// is 32-bit signed; `i64` maps to a 64-bit integer scalar in async-graphql
/// (Pitfall 2 — avoids silent truncation for large kind values or future timestamps).
///
/// `tags` is `Vec<Vec<String>>` which async-graphql maps to `[[String!]!]` automatically
/// (D-02 — native nested list, no JSON scalar).
///
/// `raw` carries the byte-exact JSON strfry stored, converted via `from_utf8_lossy`
/// (D-01, Pitfall 5 — never re-serialize `NostrEvent`; key order and whitespace differ).
///
/// async-graphql auto-renames snake_case fields to camelCase in the SDL:
///   `created_at` → `createdAt`
#[derive(SimpleObject)]
pub struct Event {
    /// 32-byte event id as a 64-char lowercase hex string (NIP-01). Maps to API-01.
    pub id: String,

    /// 32-byte author public key as a 64-char lowercase hex string (NIP-01).
    pub pubkey: String,

    /// Nostr event kind (u64 stored as i64 for GraphQL 64-bit integer compat — Pitfall 2).
    /// e.g. 0 = metadata, 1 = text note, 3 = contacts.
    pub kind: i64,

    /// Unix timestamp (seconds) the author claims the event was created at.
    /// u64 stored as i64 for GraphQL 64-bit integer compat (Pitfall 2).
    /// Renamed to `createdAt` in GraphQL schema by async-graphql.
    pub created_at: i64,

    /// Arbitrary event content (interpretation depends on `kind`).
    pub content: String,

    /// 64-byte Schnorr signature as a 128-char lowercase hex string (NIP-01).
    pub sig: String,

    /// Event tags as a nested list (D-02). Maps to `[[String!]!]` in GraphQL.
    /// `tags[i][0]` is the tag name, `tags[i][1..]` the tag values.
    pub tags: Vec<Vec<String>>,

    /// Byte-exact JSON passthrough from `DecodedEvent.raw_json` (D-01, T-04-RAW).
    /// Uses `String::from_utf8_lossy` — never panics, and strfry events are always valid UTF-8.
    /// NEVER re-serialize `NostrEvent` here — re-serializing changes key order and whitespace.
    pub raw: String,
}

/// Page object returned by `events()` (D-03, API-05).
///
/// Maps 1:1 to the engine's `(Vec<DecodedEvent>, Option<PageCursor>)` return:
///   - `events` = decoded + mapped events
///   - `end_cursor` = `cursor.map(PageCursor::encode)` — None when no next page
///   - `has_more` = `cursor.is_some()`
///
/// async-graphql auto-renames:
///   `end_cursor` → `endCursor`
///   `has_more` → `hasMore`
///
/// Consumer passes `endCursor` back as the `after: String` arg to fetch the next page.
/// Cursor is opaque base64 (D-11) and fail-closed on malformed input (T-03-CUR).
#[derive(SimpleObject)]
pub struct EventsPage {
    /// The events on this page.
    pub events: Vec<Event>,

    /// Opaque pagination cursor (base64). `None` = no further pages. Renamed to `endCursor`.
    pub end_cursor: Option<String>,

    /// `true` if a next page exists. Renamed to `hasMore`.
    pub has_more: bool,
}

/// One author's events from a `latestPerAuthor()` query (D-07, API-03).
///
/// `latestPerAuthor` surfaces the engine's `HashMap<String, Vec<DecodedEvent>>` as a list
/// of these groups — preserving the per-author grouping the engine deliberately keeps
/// (Phase-3 D-12). One entry per requested author that has matching events.
#[derive(SimpleObject)]
pub struct AuthorGroup {
    /// The author's public key (32-byte pubkey as 64-char lowercase hex).
    pub author: String,

    /// The latest events for this author (newest-first, bounded by `perAuthor`).
    pub events: Vec<Event>,
}

/// Result of the `stats` query (D-09, API-04, OPS-04).
///
/// - `event_count`: total events in the `EventPayload` sub-DB (via `mdb_stat`).
/// - `max_lev_id`: largest levId key in `EventPayload` (last key — monotonic).
/// - `db_version`: strfry `Meta.dbVersion` (must be 3; verified at startup gate).
/// - `pinned_strfry_version`: strfry image reference from config (OPS-04).
///
/// async-graphql auto-renames snake_case fields to camelCase in the SDL:
///   `event_count`           → `eventCount`
///   `max_lev_id`            → `maxLevId`
///   `db_version`            → `dbVersion`
///   `pinned_strfry_version` → `pinnedStrfryVersion`
#[derive(SimpleObject)]
pub struct StatsResult {
    /// Total event count from `EventPayload` LMDB stat (renamed to `eventCount`).
    pub event_count: i64,

    /// Largest levId in `EventPayload` — 0 when the DB is empty (renamed to `maxLevId`).
    /// u64 stored as i64 for GraphQL 64-bit integer compat (Pitfall 2).
    pub max_lev_id: i64,

    /// strfry `Meta.dbVersion`. Must be 3; verified by startup gate (renamed to `dbVersion`).
    /// u32 stored as i32 (practical values are small; no overflow risk).
    pub db_version: i32,

    /// Pinned strfry image reference from config (OPS-04, renamed to `pinnedStrfryVersion`).
    ///
    /// Populated from `AppState.pinned_strfry_version` (set from `cfg.pinned_strfry_version`
    /// in main.rs). Surfaces the configured image reference alongside the detected on-disk
    /// `dbVersion` so operators can spot version drift if the parent `dockurr/strfry` image
    /// moves (T-05-02 — internal operational data, not a secret; loopback default governs exposure).
    pub pinned_strfry_version: String,
}

/// Input filter for the `events()` query (API-01/02).
///
/// All fields are optional. Omitting a field means "no filter on that dimension".
/// Multiple fields are combined with AND semantics (NIP-01).
///
/// `tag` is a single optional tag filter for v1 (API-02). Multi-tag AND is deferred to v2
/// (CONTEXT Open Question 2 — expose `tag: Option<TagFilterInput>` single for now).
///
/// async-graphql auto-renames snake_case fields to camelCase in the SDL for input objects as well.
#[derive(InputObject, Default)]
pub struct EventFilterInput {
    /// Filter by event ids (64-char lowercase hex). OR within the list. API-01.
    pub ids: Option<Vec<String>>,

    /// Filter by author pubkeys (64-char lowercase hex). OR within the list. API-01.
    pub authors: Option<Vec<String>>,

    /// Filter by event kinds. OR within the list. API-01.
    pub kinds: Option<Vec<i64>>,

    /// Only return events with `created_at >= since` (Unix seconds). API-01.
    pub since: Option<i64>,

    /// Only return events with `created_at <= until` (Unix seconds). API-01.
    pub until: Option<i64>,

    /// Single optional tag filter (name + values). API-02.
    /// Multi-tag AND is a v2 expansion.
    pub tag: Option<TagFilterInput>,
}

/// Tag filter for a single tag dimension within an `EventFilterInput` (API-02).
///
/// `name` is the tag letter (e.g. "e", "p", "t").
/// `values` is the list of tag values to match (OR semantics within the list).
///
/// Example: `{ name: "p", values: ["<pubkey1>", "<pubkey2>"] }` matches events
/// that reference either pubkey.
#[derive(InputObject)]
pub struct TagFilterInput {
    /// Tag name/letter (e.g. "e" for event refs, "p" for pubkey refs).
    pub name: String,

    /// Tag values to match (OR semantics within this list).
    pub values: Vec<String>,
}

/// Map a `DecodedEvent` to the GraphQL `Event` type (D-01, D-02).
///
/// This is the canonical conversion used by every resolver that returns events.
///
/// - `kind` and `created_at` are cast from `u64` to `i64` (Pitfall 2 — GraphQL 64-bit int).
/// - `raw` is set from `raw_json` via `String::from_utf8_lossy` — safe, never panics,
///   and strfry events are guaranteed valid UTF-8 JSON (Pitfall 5).
/// - `tags` is moved directly — no re-mapping needed (`Vec<Vec<String>>` both sides).
/// - NEVER call `serde_json::to_string` on the `NostrEvent` struct — re-serializing
///   does not produce byte-identical output (key order and whitespace differ from what
///   strfry stored). Always use `raw_json` for the `raw` field (D-01, Phase-2 D-01).
pub fn decoded_event_to_gql(d: DecodedEvent) -> Event {
    Event {
        id: d.event.id,
        pubkey: d.event.pubkey,
        kind: d.event.kind as i64,          // u64 → i64 (Pitfall 2 — GraphQL Int is 32-bit)
        created_at: d.event.created_at as i64, // u64 → i64 (Pitfall 2)
        content: d.event.content,
        sig: d.event.sig,
        tags: d.event.tags,                 // Vec<Vec<String>> → [[String!]!] (D-02)
        // D-01 / Pitfall 5: use raw_json passthrough, NEVER re-serialize NostrEvent
        raw: String::from_utf8_lossy(&d.raw_json).into_owned(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::types::NostrEvent;

    fn make_decoded_event(kind: u64, created_at: u64, raw: &str) -> DecodedEvent {
        DecodedEvent {
            event: NostrEvent {
                id: "aabbcc".to_string(),
                pubkey: "ddeeff".to_string(),
                created_at,
                kind,
                tags: vec![vec!["e".to_string(), "deadbeef".to_string()]],
                content: "hello".to_string(),
                sig: "sig".to_string(),
            },
            raw_json: raw.as_bytes().to_vec(),
        }
    }

    /// decoded_event_to_gql: u64 kind and created_at are cast to i64 (Pitfall 2).
    #[test]
    fn test_decoded_event_to_gql_i64_cast() {
        let d = make_decoded_event(3, 1_700_000_000, r#"{"id":"aabbcc"}"#);
        let ev = decoded_event_to_gql(d);
        assert_eq!(ev.kind, 3_i64);
        assert_eq!(ev.created_at, 1_700_000_000_i64);
    }

    /// decoded_event_to_gql: raw field is byte-exact from raw_json, not re-serialized (D-01).
    #[test]
    fn test_decoded_event_to_gql_raw_passthrough() {
        let raw = r#"{"id":"aabbcc","kind":3,"created_at":1700000000}"#;
        let d = make_decoded_event(3, 1_700_000_000, raw);
        let ev = decoded_event_to_gql(d);
        assert_eq!(ev.raw, raw, "raw must be byte-exact from raw_json (D-01)");
    }

    /// decoded_event_to_gql: tags are moved directly (D-02, Vec<Vec<String>>).
    #[test]
    fn test_decoded_event_to_gql_tags() {
        let d = make_decoded_event(1, 1, r#"{"id":"x"}"#);
        let ev = decoded_event_to_gql(d);
        assert_eq!(ev.tags, vec![vec!["e".to_string(), "deadbeef".to_string()]]);
    }

    /// decoded_event_to_gql: all string fields are correctly mapped.
    #[test]
    fn test_decoded_event_to_gql_string_fields() {
        let d = make_decoded_event(0, 0, r#"{}"#);
        let ev = decoded_event_to_gql(d);
        assert_eq!(ev.id, "aabbcc");
        assert_eq!(ev.pubkey, "ddeeff");
        assert_eq!(ev.content, "hello");
        assert_eq!(ev.sig, "sig");
    }
}

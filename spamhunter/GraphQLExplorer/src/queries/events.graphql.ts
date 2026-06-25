import { graphql } from '../gql'

// EventsDocument — the author-window events query (contract §4 Query.events,
// §6 pagination). Mirrors stats.graphql.ts: a graphql() document so the generated
// types make data.events.events[].createdAt / .kind typed `number`.
//
// Field selection (contract §9.6, A4): select the six fields the window now needs —
// id, pubkey, kind, createdAt, content, and (Phase 3) tags, which feeds the tag /
// mention fan-out aggregation. The large canonical bytes payload remains deliberately
// EXCLUDED: it would inflate every page for no list render and is fetched lazily, for a
// single event at a time, by the inspector's dedicated query document (DRILL-04).
//
// Ordering is FIXED server-side: createdAt DESC, levId DESC (contract §3) — there
// is no orderBy argument, so the timeline renders the server order verbatim
// (newest-first) and never re-sorts. The opaque endCursor is passed back verbatim
// as the next page's `after` (contract §6.1 / §6.4); it is never parsed.
export const EventsDocument = graphql(`
  query Events($filter: EventFilterInput, $after: String, $limit: Int) {
    events(filter: $filter, after: $after, limit: $limit) {
      events {
        id
        pubkey
        kind
        createdAt
        content
        tags
      }
      endCursor
      hasMore
    }
  }
`)

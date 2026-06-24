import { graphql } from '../gql'

// EventsDocument — the author-window events query (contract §4 Query.events,
// §6 pagination). Mirrors stats.graphql.ts: a graphql() document so the generated
// types make data.events.events[].createdAt / .kind typed `number`.
//
// Field selection (contract §9.6, A4): select ONLY the five fields the timeline
// renders — id, pubkey, kind, createdAt, content. The large canonical payload field
// and the signature/tag fields are deliberately omitted: they would inflate every
// page for no Phase-2 render, and the tag/kind histogram work lands in Phase 3.
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
      }
      endCursor
      hasMore
    }
  }
`)

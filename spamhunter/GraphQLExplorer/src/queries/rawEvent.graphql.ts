import { graphql } from '../gql'

// RawEventDocument — the lazy single-event raw-bytes query (DRILL-04). This is the
// ONLY place the large canonical `raw` payload is ever selected: the window query
// (events.graphql.ts) deliberately omits it so it never inflates a page, and the raw
// inspector fetches it on demand, one event at a time, when an analyst clicks "View raw".
//
// Single-event path: there is no `event(id)` query in the schema — the canonical
// per-event lookup is the list `events` query filtered to a single id
// (EventFilterInput.ids: [String!]) with `limit: 1`. The result is read as
// `data.events.events[0].raw` (undefined ⇒ zero-match — a valid-but-absent fact).
//
// SECURITY (T-03-08 / T-03-05): `raw` is author-controlled bytes; the inspector renders
// it ONLY as an escaped JSX text node in a <pre>, never as HTML. Keeping `raw` lazy and
// out of the window query also bounds the per-page payload (T-03-08).
export const RawEventDocument = graphql(`
  query RawEvent($filter: EventFilterInput, $limit: Int) {
    events(filter: $filter, limit: $limit) {
      events {
        id
        raw
      }
    }
  }
`)

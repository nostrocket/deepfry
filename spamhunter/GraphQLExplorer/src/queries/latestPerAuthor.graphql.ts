import { graphql } from '../gql'

// LatestPerAuthorDocument — the batch-triage fan-out query (contract §6.2; schema
// latestPerAuthor(kind, perAuthor, authors): [AuthorGroup!]!). Mirrors events.graphql.ts:
// a graphql() document so the generated types make events[].createdAt / .kind typed `number`
// and tags typed `string[][]`.
//
// Field selection: the SAME six fields events.graphql.ts selects — id, pubkey, kind,
// createdAt, content, tags. `tags` feeds the tag / mention fan-out indicator and `content`
// feeds the near-duplicate indicator; both are REQUIRED (omitting them silently disables a
// signal). The large canonical bytes payload is deliberately NOT selected — it would inflate
// every chunk for no list render and is unused by the triage indicators.
//
// The lens omits authors with zero matching events (contract §5/§8): the response is merged
// back against the full input set by author key (mergeByAuthor), never positionally zipped.
export const LatestPerAuthorDocument = graphql(`
  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {
    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {
      author
      events {
        id
        pubkey
        kind
        createdAt
        content
        tags
      }
    }
  }
`)

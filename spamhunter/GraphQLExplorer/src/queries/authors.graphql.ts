import { graphql } from '../gql'

// AuthorsDocument — the corpus author-enumeration query (contract §6.4; schema
// authors(after: String, limit: Int): AuthorsPage!, AuthorsPage { authors endCursor hasMore }).
// Mirrors events.graphql.ts: a graphql() document so the generated AuthorsPage type makes
// hasMore typed `boolean` and authors typed `string[]`.
//
// Cursor discipline (contract §6.4): `endCursor` is OPAQUE — it is passed back verbatim as
// the next page's `after` and is NEVER parsed, constructed, or cross-used with the events
// cursor. The enumeration loop (useAuthorEnumeration) pages until hasMore is false.
export const AuthorsDocument = graphql(`
  query Authors($after: String, $limit: Int) {
    authors(after: $after, limit: $limit) {
      authors
      endCursor
      hasMore
    }
  }
`)

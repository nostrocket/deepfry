import { Client, cacheExchange, fetchExchange } from '@urql/core'
import { GRAPHQL_URL } from './config'

// Direct, cross-origin urql client. The url comes from config.ts (the single
// base-URL source) — no inline literal here, no relative path, no credentials
// (the API is unauthenticated and wildcard CORS won't honor them — contract §3).
//
// preferGetMethod: false — force POST for queries. The lens serves the GraphiQL
// IDE (HTML) on `GET /graphql` and only executes the API on `POST /graphql`
// (contract §1). urql's GET-for-queries default therefore lands on GraphiQL and
// the HTML response fails to parse, surfacing as a spurious NETWORK error. POST
// is the only correct method against this lens.
export const client = new Client({
  url: GRAPHQL_URL,
  preferGetMethod: false,
  exchanges: [cacheExchange, fetchExchange],
})

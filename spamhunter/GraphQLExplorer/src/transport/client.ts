import { Client, cacheExchange, fetchExchange } from '@urql/core'
import { GRAPHQL_URL } from './config'

// Direct, cross-origin urql client. The url comes from config.ts (the single
// base-URL source) — no inline literal here, no relative path, no credentials
// (the API is unauthenticated and wildcard CORS won't honor them — contract §3).
export const client = new Client({
  url: GRAPHQL_URL,
  exchanges: [cacheExchange, fetchExchange],
})

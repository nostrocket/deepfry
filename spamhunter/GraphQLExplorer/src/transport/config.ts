// Single source of truth for where the lens lives (FND-02 invariant).
//
// This is the ONLY module in the app that names a base-URL literal. The urql
// client and any HTTP probe import GRAPHQL_URL / READY_URL / HEALTH_URL from
// here — never inline a URL at a call site, never use a relative `/graphql`
// path (there is no proxy in this architecture; the browser calls the lens
// directly cross-origin over the lens's wildcard CORS — contract §3).
//
// SECURITY (V14, T-01-02): the default is loopback. Overriding VITE_GRAPHQL_URL
// to a public/untrusted host means the app sends queries to, and trusts
// responses from, that origin. v1 is local-dev only (contract §10).
const DEFAULT_GRAPHQL_URL = 'http://127.0.0.1:8080/graphql'

// Treat an explicitly-empty or whitespace-only VITE_GRAPHQL_URL as unset.
// Vite injects `VITE_GRAPHQL_URL=` as `''` (not undefined), which is not nullish,
// so a bare `??` would keep `''` and make `new URL('')` throw at module load —
// a blank-screen crash before any error/connecting state can render (WR-01).
const raw = import.meta.env.VITE_GRAPHQL_URL?.trim()
export const GRAPHQL_URL: string = raw && raw.length > 0 ? raw : DEFAULT_GRAPHQL_URL

// /ready and /health live on the same origin/base as /graphql.
// Fail with a clear, actionable message on a genuinely malformed override.
let base: URL
try {
  base = new URL(GRAPHQL_URL)
} catch {
  throw new Error(
    `VITE_GRAPHQL_URL is not a valid absolute URL: "${GRAPHQL_URL}". ` +
      `Expected e.g. http://127.0.0.1:8080/graphql`,
  )
}
export const READY_URL: string = new URL('/ready', base).toString()
export const HEALTH_URL: string = new URL('/health', base).toString()

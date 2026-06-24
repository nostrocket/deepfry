// Single source of truth for where the lens lives (FND-02 invariant).
//
// This is the ONLY module that resolves the base URL. The urql client and any
// HTTP probe import GRAPHQL_URL / READY_URL / HEALTH_URL from here — never
// inline a URL at a call site, never use a relative `/graphql`
// path (there is no proxy in this architecture; the browser calls the lens
// directly cross-origin over the lens's wildcard CORS — contract §3).
//
// The lens endpoint is REQUIRED and supplied ONLY via the VITE_GRAPHQL_URL env
// var (see .env / .env.example). There is no hardcoded fallback URL: a missing,
// empty, or malformed value fails loudly at startup rather than silently
// targeting a wrong host.
//
// SECURITY (V14, T-01-02): point VITE_GRAPHQL_URL only at a trusted lens — the
// app sends queries to, and trusts responses from, that origin. v1 is local-dev
// only (contract §10).
//
// Vite injects an explicitly-empty `VITE_GRAPHQL_URL=` as `''` (not undefined),
// so `.trim()` + the falsy check below treat empty/whitespace as "not set".
const raw = import.meta.env.VITE_GRAPHQL_URL?.trim()
if (!raw) {
  throw new Error(
    'VITE_GRAPHQL_URL is not set. Define it in .env (see .env.example), ' +
      'e.g. VITE_GRAPHQL_URL=http://<lens-host>:8080/graphql',
  )
}
export const GRAPHQL_URL: string = raw

// /ready and /health live on the same origin/base as /graphql.
// Fail with a clear, actionable message on a genuinely malformed override.
let base: URL
try {
  base = new URL(GRAPHQL_URL)
} catch {
  throw new Error(
    `VITE_GRAPHQL_URL is not a valid absolute URL: "${GRAPHQL_URL}". ` +
      'Expected e.g. http://<lens-host>:8080/graphql',
  )
}
export const READY_URL: string = new URL('/ready', base).toString()
export const HEALTH_URL: string = new URL('/health', base).toString()

// hashRouter — a tiny hash-based router for the app's first routing (RESEARCH §
// Pattern 5). Analog: useStatsPoll's effect + addEventListener + cleanup-remove
// shape (the visibilitychange wiring), here listening for 'hashchange'.
//
// The Route is a discriminated union (same discipline as ApiError / ParseResult):
// callers switch on `name` and never parse the raw hash themselves.
//
// SECURITY (Pitfall 6 / T-02-06): the author matcher accepts LOWERCASE 64-hex ONLY
// (/^#\/a\/([0-9a-f]{64})$/). Navigation only ever sets the hash AFTER
// parseIdentifier has normalized to lowercase hex, so a non-matching hash (uppercase,
// npub, junk, wrong length) resolves to `notfound` — never a silent zero-match
// drill-down against an un-normalized identifier.
import { useEffect, useState } from 'react'

export type Route =
  | { name: 'home' }
  | { name: 'batch' }
  | { name: 'author'; hex: string }
  | { name: 'notfound' }

// Lowercase 64-hex ONLY. Uppercase, npub, or any other shape falls through to notfound.
const AUTHOR_HASH = /^#\/a\/([0-9a-f]{64})$/

/** Pure hash → Route mapping (no React) so the matcher is trivially testable. */
export function parseHash(hash: string): Route {
  if (hash === '' || hash === '#' || hash === '#/') return { name: 'home' }
  // Exact match — the batch view is a single top-level route (no params).
  if (hash === '#/batch') return { name: 'batch' }
  const m = AUTHOR_HASH.exec(hash)
  if (m) return { name: 'author', hex: m[1] }
  return { name: 'notfound' }
}

export function useHashRoute(): Route {
  const [route, setRoute] = useState<Route>(() =>
    parseHash(typeof window !== 'undefined' ? window.location.hash : ''),
  )

  useEffect(() => {
    const onHashChange = () => setRoute(parseHash(window.location.hash))
    // Sync once on mount in case the hash changed before the listener attached.
    onHashChange()
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  return route
}

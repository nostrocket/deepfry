// Readiness gate (FND-03) — Source: contract §2 (startup readiness, 503 until
// gates pass), RESEARCH § "Readiness gate with backoff" / Pattern 3, Pitfall 8.
//
// Before the first query, poll GET <base>/ready until it returns 200. While it
// returns 503 (or the fetch rejects because the backend is not up yet) we keep
// retrying with BOUNDED backoff — starting at 500ms and doubling to a 5000ms cap.
// The app shows a distinct "connecting to relay…" state (NOT a generic error)
// until this resolves; lumping 503 into "API error" makes a healthy-but-warming
// backend look broken (Pitfall 8).
//
// SECURITY (V7, T-01-05 — self-inflicted DoS): backoff is capped at 5000ms and
// never grows unbounded; /ready is not polled aggressively (seconds-scale).
//
// Direct cross-origin fetch — no proxy. READY_URL is derived from the single
// configurable base URL in config.ts (FND-02). The lens answers the CORS OPTIONS
// preflight before the readiness gate (contract §3), so the cross-origin probe
// succeeds even while POST /graphql would still 503.
import { READY_URL } from './config'

const INITIAL_DELAY_MS = 500
const MAX_DELAY_MS = 5000

/**
 * Resolve once GET <base>/ready returns HTTP 200. Retries on 503 and on fetch
 * rejection (backend not up yet) with bounded exponential backoff (500ms → 5000ms
 * cap). Pass an AbortSignal to cancel (e.g. on component unmount); aborting rejects
 * the in-flight fetch and the returned promise.
 */
export async function waitForReady(signal?: AbortSignal): Promise<void> {
  let delay = INITIAL_DELAY_MS
  for (;;) {
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    try {
      const res = await fetch(READY_URL, { signal }) // direct, cross-origin (wildcard CORS)
      if (res.status === 200) return
      // Any non-200 (notably 503) → not ready yet; fall through to backoff.
    } catch (err) {
      // A genuine abort must propagate; everything else is "backend not up yet".
      if (err instanceof DOMException && err.name === 'AbortError') throw err
      // network not up yet — keep retrying.
    }
    await sleep(delay, signal)
    delay = Math.min(delay * 2, MAX_DELAY_MS) // bounded backoff — never unbounded
  }
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

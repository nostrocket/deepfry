import { useEffect, useState } from 'react'
import { waitForReady } from './transport/readiness'
import { StatsDashboard } from './views/StatsDashboard'
import { ConnectingShell } from './views/ConnectingShell'

// Gate the first query on the readiness probe (FND-03). Before mounting the
// dashboard we await waitForReady() (poll GET /ready with 503 bounded backoff)
// and render a DISTINCT "connecting to relay…" state — info-blue, never an error
// (Pitfall 8 / UI-SPEC State Treatments). Once /ready returns 200 we mount the full
// StatsDashboard (slice 3) — the four scalars, the complete distinct-state set,
// seconds-scale polling, hidden-tab pause, and the corpus-changed nudge.
//
// The connecting copy/markup lives in the shared ConnectingShell so App (the cold-start
// owner) and the dashboard's initial-load branch cannot drift (WR-03 / IN-02).

export function App() {
  const [ready, setReady] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    waitForReady(controller.signal)
      .then(() => setReady(true))
      .catch(() => {
        // Aborted on unmount (StrictMode double-invoke / teardown) — ignore.
      })
    return () => controller.abort()
  }, [])

  if (!ready) return <ConnectingShell />
  return <StatsDashboard />
}

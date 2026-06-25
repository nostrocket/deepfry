import { useEffect, useState } from 'react'
import { waitForReady } from './transport/readiness'
import { useHashRoute } from './router/hashRouter'
import { StatsDashboard } from './views/StatsDashboard'
import { AuthorDrillDown } from './views/AuthorDrillDown'
import { BatchImport } from './views/BatchImport'
import { SuspectEntryBar } from './views/SuspectEntryBar'
import { ConnectingShell } from './views/ConnectingShell'
import styles from './App.module.css'

// Gate the first query on the readiness probe (FND-03). Before mounting the app we
// await waitForReady() (poll GET /ready with 503 bounded backoff) and render a DISTINCT
// "connecting to relay…" state — info-blue, never an error (Pitfall 8 / UI-SPEC). Once
// /ready returns 200 we mount the app shell.
//
// The shell (02-02) hosts the SuspectEntryBar on EVERY route (so a new suspect can be
// entered from the drill-down too) and switches on useHashRoute(): home → StatsDashboard,
// author → AuthorDrillDown(hex), notfound → a neutral not-found block. The readiness gate
// + shared ConnectingShell are preserved verbatim (WR-03 / IN-02).

function NotFound() {
  return (
    <div className={styles.notFound}>
      <h1 className={styles.notFoundHeading}>That's not a recognized view.</h1>
      <button type="button" className={styles.notFoundButton} onClick={() => (window.location.hash = '')}>
        Back to corpus stats
      </button>
    </div>
  )
}

export function App() {
  const [ready, setReady] = useState(false)
  const route = useHashRoute()

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

  return (
    <div className={styles.shell}>
      <header className={styles.shellHeader}>
        <div className={styles.entrySlot}>
          <SuspectEntryBar />
        </div>
        {/* Neutral nav to the batch view — NOT accent (accent stays on the two "go"
            submits: "Inspect author" and the batch "Triage"). UI-SPEC App-shell nav. */}
        <nav className={styles.shellNav}>
          <button
            type="button"
            className={styles.navLink}
            onClick={() => (window.location.hash = '#/batch')}
          >
            Batch triage
          </button>
        </nav>
      </header>
      <div className={styles.routeOutlet}>
        {route.name === 'home' && <StatsDashboard />}
        {route.name === 'batch' && <BatchImport />}
        {route.name === 'author' && <AuthorDrillDown hex={route.hex} />}
        {route.name === 'notfound' && <NotFound />}
      </div>
    </div>
  )
}

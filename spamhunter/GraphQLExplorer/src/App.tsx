import { useEffect, useState } from 'react'
import { waitForReady } from './transport/readiness'
import { StatsDashboard } from './views/StatsDashboard'

// Gate the first query on the readiness probe (FND-03). Before mounting the
// dashboard we await waitForReady() (poll GET /ready with 503 bounded backoff)
// and render a DISTINCT "connecting to relay…" state — info-blue, never an error
// (Pitfall 8 / UI-SPEC State Treatments). Once /ready returns 200 we mount the full
// StatsDashboard (slice 3) — the four scalars, the complete distinct-state set,
// seconds-scale polling, hidden-tab pause, and the corpus-changed nudge.

function ConnectingState() {
  // UI-SPEC "Connecting (cold start)": centered shell, info-blue indicator PAIRED
  // with text — color is never the sole signal (color-blind / screenshot safe).
  return (
    <main
      style={{
        minHeight: '100vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 'var(--space-md)',
        padding: 'var(--space-3xl)',
        textAlign: 'center',
        fontFamily: 'var(--font-sans)',
      }}
      role="status"
      aria-live="polite"
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--space-sm)',
          color: 'var(--connecting)',
        }}
      >
        {/* Shape (dot) + label both carry the meaning, not color alone. */}
        <span
          aria-hidden="true"
          style={{
            width: 10,
            height: 10,
            borderRadius: '50%',
            backgroundColor: 'var(--connecting)',
            display: 'inline-block',
          }}
        />
        <h1 style={{ margin: 0, fontSize: 20, fontWeight: 600, color: 'var(--connecting)' }}>
          Connecting to relay…
        </h1>
      </div>
      <p style={{ margin: 0, maxWidth: 420, fontSize: 16, color: 'var(--text-muted)' }}>
        Waiting for the relay to report ready. This can take a moment on cold start.
      </p>
    </main>
  )
}

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

  if (!ready) return <ConnectingState />
  return <StatsDashboard />
}

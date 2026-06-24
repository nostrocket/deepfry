// ConnectingShell — the SINGLE owner of the "Connecting to relay…" cold-start
// state (WR-03 / IN-02). Both the App readiness gate and the StatsDashboard
// initial-load branch render THIS component, so the UI-SPEC copy and markup live
// in exactly one place and cannot drift between the two call sites.
//
// UI-SPEC "Connecting (cold start)": centered shell, info-blue indicator PAIRED
// with text — color is never the sole signal (color-blind / screenshot safe).
// The dot (shape) + label both carry the meaning, not color alone.
export function ConnectingShell() {
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

// StatsDashboard — the polished, honest corpus-stats dashboard (STATS-01, STATS-02).
//
// Renders the four StatsResult scalars (contract §5) as a 2×2 card grid (≥ md),
// driven by useStatsPoll (seconds-scale poll, hidden-tab pause, maxLevId-diff nudge).
// Implements the COMPLETE distinct, non-blank state set from the approved UI-SPEC
// State Treatments table — every classifier ApiError kind plus the loaded / empty /
// poll-paused / corpus-changed states — using the UI-SPEC copy strings VERBATIM.
//
// Color discipline (UI-SPEC §Color): the teal accent appears in EXACTLY two places —
// the corpus-changed nudge and the live-poll active dot (both live in the CSS module).
// Semantic colors (connecting blue / recoverable amber / hard-fail red) are bound to
// state meaning and always paired with a label and/or shape — color is never the sole
// signal. eventCount === 0 is a CALM FACT (neutral), never an error.
//
// SECURITY: pinnedStrfryVersion (a backend String!) is rendered as escaped plaintext
// via normal JSX interpolation — React escapes by default; raw-HTML injection is never
// used (T-01-07). The INTERNAL state shows the generic UI-SPEC copy, never raw server
// internals (T-01-08).
import type { ApiError } from '../transport/errors'
import { useStatsPoll, type Stats } from '../hooks/useStatsPoll'
import { ConnectingShell } from './ConnectingShell'
import styles from './StatsDashboard.module.css'

// Large integers use locale grouping so they stay legible in the mono Display slot.
const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

// ── Header (title + live-poll status + Refresh CTA) ─────────────────────────
function Header({
  isPaused,
  onRefresh,
}: {
  isPaused: boolean
  onRefresh: () => void
}) {
  return (
    <header className={styles.header}>
      <div className={styles.titleRow}>
        <h1 className={styles.title}>Corpus stats</h1>
      </div>
      <div className={styles.headerActions}>
        {/* Live-poll status: accent dot when polling + tab visible; dimmed/neutral
            when paused. Dot + caption both carry the meaning (not color alone). */}
        <span
          className={styles.pollStatus}
          role="status"
          aria-live="polite"
          title={isPaused ? 'Polling paused while the tab is hidden' : 'Polling for changes'}
        >
          <span
            aria-hidden="true"
            className={`${styles.dot} ${isPaused ? styles.dotPaused : styles.dotLive}`}
          />
          {isPaused ? 'Paused (tab hidden)' : 'Live'}
        </span>
        {/* The ONLY CTA this phase — re-pulls stats on demand + acknowledges the nudge. */}
        <button type="button" className={styles.refreshButton} onClick={onRefresh}>
          Refresh stats
        </button>
      </div>
    </header>
  )
}

// ── Corpus-changed nudge (accent use #1) ────────────────────────────────────
function CorpusChangedNudge({ onRefresh }: { onRefresh: () => void }) {
  return (
    <div className={styles.nudge} role="status" aria-live="polite">
      <span className={styles.nudgeText}>Corpus changed — refresh to update.</span>
      {/* Clicking refresh (here or in the header) is the ONLY thing that re-pulls —
          the nudge NEVER auto-refetches. */}
      <button type="button" className={styles.nudgeDismiss} onClick={onRefresh}>
        Refresh stats
      </button>
    </div>
  )
}

// ── Four stat cards ─────────────────────────────────────────────────────────
function StatCards({ stats }: { stats: Stats }) {
  const cards: { label: string; value: string }[] = [
    { label: 'Event count', value: formatInt(stats.eventCount) },
    { label: 'Max levId', value: formatInt(stats.maxLevId) },
    { label: 'DB version', value: formatInt(stats.dbVersion) },
    // pinnedStrfryVersion is a String! — escaped plaintext via JSX interpolation.
    { label: 'Pinned strfry version', value: stats.pinnedStrfryVersion },
  ]
  return (
    <div className={styles.grid}>
      {cards.map((c) => (
        <div key={c.label} className={styles.card}>
          <span className={styles.cardLabel}>{c.label}</span>
          <span className={styles.cardValue}>{c.value}</span>
        </div>
      ))}
    </div>
  )
}

// ── Inline note (recoverable/amber or hard-fail/red) shown above the cards ──
function InlineNote({
  tone,
  children,
}: {
  tone: 'recoverable' | 'hardFail'
  children: string
}) {
  const toneClass = tone === 'recoverable' ? styles.recoverable : styles.hardFail
  return (
    <div className={`${styles.note} ${toneClass}`} role="status" aria-live="polite">
      <span aria-hidden="true" className={styles.stateDot} />
      <span>{children}</span>
    </div>
  )
}

// ── Full-shell error state (used before any data has loaded) ────────────────
function ErrorShell({
  tone,
  heading,
  body,
}: {
  tone: 'recoverable' | 'hardFail'
  heading: string
  body: string
}) {
  const toneClass = tone === 'recoverable' ? styles.recoverable : styles.hardFail
  return (
    <main className={styles.stateShell} role="status" aria-live="polite">
      <div className={`${styles.stateRow} ${toneClass}`}>
        <span aria-hidden="true" className={styles.stateDot} />
        <h1 className={styles.stateHeading}>{heading}</h1>
      </div>
      <p className={styles.stateBody}>{body}</p>
    </main>
  )
}

// VERBATIM UI-SPEC copy for every classifier kind. Maps an ApiError to its tone +
// message. INTERNAL is generic (no server internals); VALIDATION shows the (user-safe)
// server message verbatim per contract §7.
function errorTreatment(error: ApiError): {
  tone: 'recoverable' | 'hardFail'
  message: string
} {
  switch (error.kind) {
    case 'INVALID_CURSOR':
      return { tone: 'recoverable', message: 'Pagination expired — reloading from the top.' }
    case 'TOO_MANY_AUTHORS':
      return {
        tone: 'recoverable',
        message: 'Too many authors in one request — narrowing the batch.',
      }
    case 'NOT_READY':
      return { tone: 'recoverable', message: 'Relay is warming up — retrying…' }
    case 'PAYLOAD_TOO_LARGE':
      return { tone: 'recoverable', message: 'Request too large — trimming and retrying.' }
    case 'VALIDATION':
      // Code-less validation message — user-safe per contract §7, shown verbatim.
      return { tone: 'recoverable', message: error.message }
    case 'INTERNAL':
      // Generic — NEVER echo raw server internals (T-01-08).
      return {
        tone: 'hardFail',
        message: 'Something went wrong reading the corpus. Retrying shortly.',
      }
    case 'NETWORK':
      // Direct-connection wording (CONTEXT.md / contract v1.2): asks whether the
      // relay is up on 127.0.0.1:8080 and VITE_GRAPHQL_URL points at it. No proxy mention.
      return {
        tone: 'hardFail',
        message:
          "Can't reach the relay. Is the relay up on 127.0.0.1:8080 (and VITE_GRAPHQL_URL pointing at it)?",
      }
  }
}

export function StatsDashboard() {
  const { stats, error, loading, hasNewData, isPaused, refresh } = useStatsPoll()

  // Initial load, no data and no error yet → connecting/info shell (distinct, non-blank).
  // Covers the post-ready transient gap (a 200 on /ready does not strictly guarantee the
  // very next POST /graphql resolves before this first tick). Uses the SHARED ConnectingShell
  // so the copy/markup is owned in one place and cannot drift from App's gate (WR-03 / IN-02).
  if (loading && !stats && !error) {
    return <ConnectingShell />
  }

  // Error before any successful load → full error shell (no cards to keep on screen).
  if (error && !stats) {
    const { tone, message } = errorTreatment(error)
    if (tone === 'hardFail' && error.kind === 'NETWORK') {
      return (
        <ErrorShell
          tone="hardFail"
          heading="Can't reach the relay"
          body={message}
        />
      )
    }
    if (tone === 'hardFail') {
      return <ErrorShell tone="hardFail" heading="Something went wrong" body={message} />
    }
    return <ErrorShell tone="recoverable" heading="Relay is warming up…" body={message} />
  }

  // We have at least one good load. Render the dashboard with cards; any current
  // error is shown as a non-blocking inline note above the (last-good) cards.
  const isEmptyCorpus = !!stats && stats.eventCount === 0
  const noteTreatment = error ? errorTreatment(error) : null

  return (
    <main className={styles.dashboard}>
      <Header isPaused={isPaused} onRefresh={refresh} />

      {/* Corpus-changed nudge (accent) — passive, dismissible, NEVER auto-refetches. */}
      {hasNewData && <CorpusChangedNudge onRefresh={refresh} />}

      {/* Transient/recoverable or hard-fail note while last-good data stays on screen. */}
      {noteTreatment && <InlineNote tone={noteTreatment.tone}>{noteTreatment.message}</InlineNote>}

      {stats && <StatCards stats={stats} />}

      {/* Empty corpus — eventCount === 0 is a CALM FACT, neutral styling, not an error. */}
      {isEmptyCorpus && (
        <div className={styles.emptyCaption}>
          <h2 className={styles.emptyCaptionHeading}>No events in corpus yet</h2>
          <p className={styles.emptyCaptionBody}>
            The relay is reachable and reporting zero stored events. Nothing to investigate yet.
          </p>
        </div>
      )}
    </main>
  )
}

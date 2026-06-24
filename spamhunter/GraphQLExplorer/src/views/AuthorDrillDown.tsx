// AuthorDrillDown — the second view: an author's event window against an honest,
// non-removable window-size denominator (ID-02 / ID-03 / DRILL-05 / DRILL-06).
//
// Scope note (DRILL-01): the posting-rate / burst panel and the forgeable-createdAt
// caveat are the NEXT slice (02-03). This plan ships the timeline + window indicator +
// single-page pagination first. The window indicator already frames N as a denominator,
// not a verdict.
//
// Source: UI-SPEC § Drill-down view + Copywriting Contract (strings VERBATIM);
// 02-PATTERNS (analog: StatsDashboard — view anatomy, errorTreatment switch, connecting
// → error-shell → loaded gating order, emptyCaption calm-fact treatment).
//
// ID-02: the identity header shows BOTH forms — the bech32 npub AND the 64-char hex —
// each labeled, each mono, each escaped plaintext.
// ID-03: a valid-but-zero-match author lands the neutral calm empty state HERE (a parse
// failure never reaches this view — it stays on the dashboard via the entry bar), WITH
// the window indicator still present (N=0). Never amber, never red.
//
// SECURITY: event content / createdAt / hex / npub are rendered as escaped plaintext via
// JSX (React default escaping); no raw-HTML injection (attacker-controlled content).
// INTERNAL errors show generic copy (no server internals); NETWORK echoes only the
// configured endpoint.
//
// COLOR: no teal highlight anywhere in this view — Load more, rows, back, and copy are
// all neutral chrome. The single accent action lives in the shell entry bar.
import { parseIdentifier } from '../identifier/identifier'
import { GRAPHQL_URL } from '../transport/config'
import { useAuthorWindow, type WindowEvent } from '../hooks/useAuthorWindow'
import type { ApiError } from '../transport/errors'
import { ConnectingShell } from './ConnectingShell'
import { WindowIndicator } from './WindowIndicator'
import styles from './AuthorDrillDown.module.css'

// Render an author-claimed epoch (seconds) as human UTC, trimmed to seconds + 'Z'.
function utc(epochSeconds: number): string {
  return new Date(epochSeconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z')
}

function goHome() {
  window.location.hash = ''
}

// ── Identity header (both forms, ID-02) ─────────────────────────────────────
function IdentityHeader({ hex, npub }: { hex: string; npub: string }) {
  return (
    <header className={styles.identity}>
      <div className={styles.identityTop}>
        <h1 className={styles.title}>Author</h1>
        <button type="button" className={styles.backButton} onClick={goHome}>
          Back to corpus stats
        </button>
      </div>
      <div className={styles.idRow}>
        <span className={styles.idLabel}>npub</span>
        <span className={styles.idValue}>{npub}</span>
        <button
          type="button"
          className={styles.copyButton}
          aria-label="Copy pubkey"
          onClick={() => void navigator.clipboard?.writeText(npub)}
        >
          Copy
        </button>
      </div>
      <div className={styles.idRow}>
        <span className={styles.idLabel}>hex</span>
        <span className={styles.idValue}>{hex}</span>
        <button
          type="button"
          className={styles.copyButton}
          aria-label="Copy pubkey"
          onClick={() => void navigator.clipboard?.writeText(hex)}
        >
          Copy
        </button>
      </div>
    </header>
  )
}

// ── Inline note (recoverable/amber or hard-fail/red) above the list ─────────
function InlineNote({ tone, children }: { tone: 'recoverable' | 'hardFail'; children: string }) {
  const toneClass = tone === 'recoverable' ? styles.recoverable : styles.hardFail
  return (
    <div className={`${styles.note} ${toneClass}`} role="status" aria-live="polite">
      <span aria-hidden="true" className={styles.stateDot} />
      <span>{children}</span>
    </div>
  )
}

// ── Full-shell error state (before any events have loaded) ──────────────────
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

// VERBATIM UI-SPEC copy per classifier kind — drill-down phrasing. INTERNAL is generic
// (no server internals); VALIDATION verbatim (user-safe per contract §7); NETWORK echoes
// the configured GRAPHQL_URL only.
function errorTreatment(error: ApiError): { tone: 'recoverable' | 'hardFail'; message: string } {
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
      return { tone: 'recoverable', message: error.message }
    case 'INTERNAL':
      return {
        tone: 'hardFail',
        message: 'Something went wrong reading this author’s events. Retrying shortly.',
      }
    case 'NETWORK':
      return {
        tone: 'hardFail',
        message: `Can't reach the relay at ${GRAPHQL_URL}. Is it running and is VITE_GRAPHQL_URL pointing at it?`,
      }
  }
}

// ── One timeline row ────────────────────────────────────────────────────────
function TimelineRow({ event }: { event: WindowEvent }) {
  return (
    <div className={styles.row}>
      <span className={styles.cellKind}>{event.kind}</span>
      <span className={styles.cellTime}>
        {utc(event.createdAt)} · {event.createdAt}
      </span>
      <span className={styles.cellContent}>{event.content}</span>
    </div>
  )
}

export function AuthorDrillDown({ hex }: { hex: string }) {
  const { events, windowMeta, error, loading, hasMore, loadMore } = useAuthorWindow(hex)

  // Derive the display npub from the normalized hex via the single identifier module.
  // The hex came from the router's lowercase-64hex matcher, so this parse always
  // succeeds; fall back to the raw hex defensively if it somehow does not.
  const parsed = parseIdentifier(hex)
  const npub = parsed.ok ? parsed.npub : hex

  // Connecting (first load) — distinct, non-blank, reuses the shared shell.
  if (loading && events.length === 0 && !error) {
    return <ConnectingShell />
  }

  // Error before any events loaded → full error shell (no rows to keep on screen).
  if (error && events.length === 0) {
    const { tone, message } = errorTreatment(error)
    if (tone === 'hardFail' && error.kind === 'NETWORK') {
      return <ErrorShell tone="hardFail" heading="Can't reach the relay" body={message} />
    }
    if (tone === 'hardFail') {
      return <ErrorShell tone="hardFail" heading="Something went wrong" body={message} />
    }
    return <ErrorShell tone="recoverable" heading="Relay is warming up…" body={message} />
  }

  const noteTreatment = error ? errorTreatment(error) : null
  const isZeroMatch = !loading && !error && events.length === 0

  return (
    <main className={styles.view}>
      <IdentityHeader hex={hex} npub={npub} />

      {/* Transient error WITH existing rows → non-blocking inline note above the list. */}
      {noteTreatment && events.length > 0 && (
        <InlineNote tone={noteTreatment.tone}>{noteTreatment.message}</InlineNote>
      )}

      {isZeroMatch ? (
        // ID-03 valid-but-zero-match — neutral calm empty state, WITH the indicator (N=0).
        <section className={styles.timelineSurface}>
          <WindowIndicator meta={windowMeta} />
          <div className={styles.emptyCaption}>
            <h2 className={styles.emptyCaptionHeading}>No events for this author</h2>
            <p className={styles.emptyCaptionBody}>
              This is a valid pubkey, but the corpus holds zero events for it. A valid identifier
              with no events is not a clean author — it is simply absent here.
            </p>
          </div>
        </section>
      ) : (
        <section className={styles.timelineSurface}>
          {/* DRILL-05 — always present on the timeline surface. */}
          <WindowIndicator meta={windowMeta} />

          <div className={styles.timeline}>
            <div className={styles.headerRow}>
              <span className={styles.cellKind}>Kind</span>
              <span className={styles.cellTime}>Time (createdAt)</span>
              <span className={styles.cellContent}>Content</span>
            </div>
            {/* Server order (createdAt DESC) — never re-sorted. */}
            {events.map((e) => (
              <TimelineRow key={e.id} event={e} />
            ))}
          </div>

          {/* DRILL-06 — single page per click; muted end caption when exhausted. */}
          {hasMore ? (
            <button
              type="button"
              className={styles.loadMore}
              onClick={loadMore}
              disabled={loading}
            >
              {loading ? 'Loading…' : 'Load more'}
            </button>
          ) : (
            <p className={styles.endCaption}>End of available events — this is the full window.</p>
          )}
        </section>
      )}
    </main>
  )
}

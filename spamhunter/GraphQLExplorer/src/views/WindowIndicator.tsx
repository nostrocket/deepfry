// WindowIndicator — the NON-REMOVABLE window-size denominator (DRILL-05).
//
// Source: UI-SPEC § "Window-size honesty indicator" + Copywriting Contract (the three
// strings are VERBATIM); 02-PATTERNS (analog: StatsDashboard InlineNote). Presentational
// only — takes { meta } and renders the denominator line.
//
// Honesty contract (load-bearing): there is NO dismiss prop and NO hidden branch — it
// renders in EVERY case, including meta.count === 0. When the window is PARTIAL
// (hasMore), the "more available — partial window" segment is emphasized with the
// recoverable amber treatment so a partial window can never read as exoneration.
//
// SECURITY: all values (N, the UTC timestamps) are rendered as escaped plaintext via
// JSX interpolation — React default escaping only; no raw-HTML injection.
import type { WindowMeta } from '../hooks/useAuthorWindow'
import styles from './WindowIndicator.module.css'

// Large integers use locale grouping, consistent with StatsDashboard's formatInt.
const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

// Render an author-claimed epoch (seconds) as human UTC, trimmed to seconds + 'Z'.
function utc(epochSeconds: number): string {
  return new Date(epochSeconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z')
}

export function WindowIndicator({ meta }: { meta: WindowMeta }) {
  // N = 0 — distinct verbatim string; no time range.
  if (meta.count === 0) {
    return (
      <div className={styles.indicator} role="status" aria-live="polite">
        Computed over 0 fetched events · no events in window
      </div>
    )
  }

  const n = formatInt(meta.count)
  // oldest/newest are non-null here (count > 0).
  const range = `${utc(meta.oldest as number)} → ${utc(meta.newest as number)}`

  // Full window — neutral muted, stated as a fact.
  if (!meta.hasMore) {
    return (
      <div className={styles.indicator} role="status" aria-live="polite">
        Computed over {n} fetched events · full window · {range}
      </div>
    )
  }

  // Partial window — the "more available — partial window" segment is amber so the
  // reader cannot miss that the window is incomplete (never reads as a clean bill).
  return (
    <div className={styles.indicator} role="status" aria-live="polite">
      Computed over {n} fetched events ·{' '}
      <span className={styles.recoverable}>
        <span className={styles.stateDot} />
        more available — partial window
      </span>{' '}
      · {range}
    </div>
  )
}

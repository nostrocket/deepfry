// KindsPanel — the event-kind distribution signal surface (DRILL-04).
//
// Source: UI-SPEC § "Kind-distribution panel" State Treatments + Copywriting Contract
// (strings VERBATIM: "Event kinds", "(unknown kind)", the out-of-range flag, the N=0
// fact); 03-PATTERNS (analog: RatePanel — panel shell, hand-rolled CSS bars, co-located
// WindowIndicator, amber flagged note). Consumes the slice-03-01 pure analyzer
// `analyzeKinds` + the KIND_NAMES NIP lookup.
//
// ASYMMETRY / COLOR: kinds are NEUTRAL data — the bars are always --text-muted and NEVER
// pick up the amber tint (a kind is neither "good" nor "bad"). The ONLY amber here is the
// out-of-range flagged note (paired with a dot + a text label), for events with a
// forged/out-of-range kind or createdAt — counted and flagged, never silently dropped.
// No green, no teal, no success token.
//
// SECURITY: kind numbers, names, and counts render as escaped JSX text nodes (React
// default); no raw-HTML injection sink is used.
import { analyzeKinds } from '../analysis/kinds'
import { KIND_NAMES } from '../analysis/kindNames'
import type { WindowEvent, WindowMeta } from '../hooks/useAuthorWindow'
import { WindowIndicator } from './WindowIndicator'
import styles from './KindsPanel.module.css'

const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

export function KindsPanel({
  events,
  windowMeta,
}: {
  events: WindowEvent[]
  windowMeta: WindowMeta
}) {
  // Re-derives every render — Load more widens the analysis. analyzeKinds is linear, so it
  // stays plain (only the O(n²) nearDup is memoized).
  const kinds = analyzeKinds(events.map((e) => ({ kind: e.kind, createdAt: e.createdAt })))
  const n = windowMeta.count
  const maxCount = kinds.bins.reduce((m, b) => (b.count > m ? b.count : m), 0)

  return (
    <section className={styles.panel} aria-label="Event kinds">
      <div className={styles.head}>
        <h2 className={styles.title}>Event kinds</h2>
      </div>

      {/* DRILL-05 — non-removable denominator, co-located, even at N=0. */}
      <WindowIndicator meta={windowMeta} />

      {n === 0 ? (
        <p className={styles.fact}>Computed over 0 fetched events — no kinds to show.</p>
      ) : (
        <>
          {/* Hand-rolled CSS bars (no chart lib). Kinds are neutral data — bars stay
              --text-muted always; no amber tint on the bars. */}
          {kinds.bins.length > 0 && (
            <div className={styles.bars}>
              {kinds.bins.map((bin) => {
                const heightPct = maxCount > 0 ? Math.max(4, (bin.count / maxCount) * 100) : 4
                const name = KIND_NAMES[bin.kind]
                return (
                  <div key={bin.kind} className={styles.barColumn}>
                    <span className={styles.barCount}>{formatInt(bin.count)}</span>
                    <span className={styles.barSlot} title={`${bin.count}`}>
                      <span className={styles.bar} style={{ height: `${heightPct}%` }} />
                    </span>
                    <span className={styles.barLabel}>
                      {name ? (
                        <>
                          <span className={styles.kindName}>{name}</span>
                          <span className={styles.kindNum}>{bin.kind}</span>
                        </>
                      ) : (
                        <>
                          <span className={styles.kindNum}>{bin.kind}</span>
                          <span className={styles.kindUnknown}>(unknown kind)</span>
                        </>
                      )}
                    </span>
                  </div>
                )
              })}
            </div>
          )}

          {kinds.outOfRangeCount > 0 && (
            <p className={styles.outOfRangeNote}>
              <span aria-hidden="true" className={styles.stateDot} />
              {formatInt(kinds.outOfRangeCount)} event
              {kinds.outOfRangeCount === 1 ? '' : 's'} with out-of-range kind/timestamp
            </p>
          )}
        </>
      )}
    </section>
  )
}

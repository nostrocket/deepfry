// RatePanel — the posting-rate / burst signal surface (DRILL-01 + DRILL-05).
//
// Source: UI-SPEC § "Posting-rate / burst panel" + "Rate/burst-panel color rule" +
// Copywriting Contract (strings VERBATIM: "Posting rate", "burst", the forgeable
// caveat); 02-PATTERNS (analog: StatsDashboard StatCards map-render; the SVG bars are
// new — hand-rolled, no chart lib).
//
// ASYMMETRY (load-bearing honesty contract):
//   - bars are NEUTRAL by default (--text-muted on a --border baseline);
//   - a detected burst is marked with the amber (--recoverable) tint AND the text label
//     "burst" AND the spike shape — color is NEVER the sole signal;
//   - there is NO green / no success color / no teal accent anywhere in this panel;
//     quiet bars must NOT read as "clean".
// The permanent forgeable caveat is ALWAYS rendered beside the chart (non-dismissible),
// and the WindowIndicator is co-located on this surface too (DRILL-05 — every signal
// surface carries the denominator).
//
// SECURITY: every value is escaped plaintext via JSX interpolation (React default);
// no raw-HTML injection sink is used (event content / timestamps are attacker-controlled).
import { analyzeRate } from '../analysis/rate'
import type { WindowEvent, WindowMeta } from '../hooks/useAuthorWindow'
import { WindowIndicator } from './WindowIndicator'
import styles from './RatePanel.module.css'

// Locale-grouped integers, consistent with the rest of the app.
const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

export function RatePanel({ events, windowMeta }: { events: WindowEvent[]; windowMeta: WindowMeta }) {
  // Re-derives every render — widening the window via Load more re-runs the analyzer.
  const rate = analyzeRate(events.map((e) => e.createdAt))
  const maxCount = rate.bins.reduce((m, b) => (b.count > m ? b.count : m), 0)

  return (
    <section className={styles.panel} aria-label="Posting rate">
      <div className={styles.head}>
        <h2 className={styles.title}>Posting rate</h2>
        {rate.burstDetected && (
          // Amber + explicit text label — color paired with a word + the spike shape.
          <span className={styles.burstBadge}>
            <span aria-hidden="true" className={styles.stateDot} />
            burst
          </span>
        )}
      </div>

      {/* DRILL-05 — the non-removable denominator on this signal surface too. */}
      <WindowIndicator meta={windowMeta} />

      {/* Hand-rolled CSS bars (no chart lib). Neutral by default; the whole chart picks
          up the amber burst treatment only when a burst is present (paired with the
          label above + the bar heights). Quiet bars stay neutral — never "clean". */}
      {rate.bins.length > 0 ? (
        <div
          className={`${styles.bars} ${rate.burstDetected ? styles.barsBurst : ''}`}
          role="img"
          aria-label={
            rate.burstDetected
              ? `Posting-rate bars over ${formatInt(rate.analyzedCount)} events — burst detected`
              : `Posting-rate bars over ${formatInt(rate.analyzedCount)} events — no burst detected (inconclusive)`
          }
        >
          {rate.bins.map((bin) => {
            const heightPct = maxCount > 0 ? Math.max(4, (bin.count / maxCount) * 100) : 4
            return (
              <span key={bin.start} className={styles.barSlot} title={`${bin.count}`}>
                <span className={styles.bar} style={{ height: `${heightPct}%` }} />
              </span>
            )
          })}
        </div>
      ) : (
        <p className={styles.sparseNote}>
          Not enough timestamps in this window to chart a posting rate.
        </p>
      )}

      {rate.rejectedCount > 0 && (
        <p className={styles.rejectedNote}>
          {formatInt(rate.rejectedCount)} timestamp
          {rate.rejectedCount === 1 ? '' : 's'} out-of-range — excluded from this rate, not
          mis-computed.
        </p>
      )}

      {/* PERSISTENT forgeable caveat — always beside the chart, non-dismissible. */}
      <p className={styles.caveat}>
        Timing is author-claimed and forgeable — a burst suggests suspicious automation, but
        quiet timing does not prove the author is clean.
      </p>
    </section>
  )
}

// TagsPanel — the tag / mention fan-out signal surface (DRILL-03).
//
// Source: UI-SPEC § "Tag / mention fan-out panel" State Treatments + Copywriting Contract
// (strings VERBATIM: "Tags & mentions", "Most-mentioned pubkeys", "Top hashtags",
// "{count} event references", the amber labels "high mention fan-out" / "hashtag stuffing"
// / "high tag count", the zero-result fact, the asymmetry note); 03-PATTERNS (analog:
// RatePanel — panel shell, co-located WindowIndicator, amber badge, persistent caveat).
// Consumes the slice-03-01 pure analyzer `analyzeTags`.
//
// ASYMMETRY (load-bearing honesty contract):
//   - the top-N lists are NEUTRAL by default;
//   - a per-event outlier is amber (--recoverable), ALWAYS paired with a text label
//     (driven by the analyzer's massMention / stuffing / highTagCount flags) AND the dot
//     shape — color is never the sole signal; an entry may carry more than one badge;
//   - zero tags reads as a NEUTRAL muted fact, never green/clean;
//   - malformed tag rows are COUNTED (parity with RatePanel's rejectedNote), never hidden;
//   - the persistent asymmetry note is always rendered.
//
// SECURITY: pubkeys, hashtags, and counts render as escaped JSX text nodes (React default);
// no raw-HTML injection sink is used. Hashtags and pubkeys are author-controlled.
import { analyzeTags } from '../analysis/tags'
import { TAGS } from '../analysis/thresholds'
import type { WindowEvent, WindowMeta } from '../hooks/useAuthorWindow'
import { WindowIndicator } from './WindowIndicator'
import styles from './TagsPanel.module.css'

const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

// Truncate a 64-char hex pubkey first/last-8, mirroring the identity-header treatment.
function truncPubkey(pk: string): string {
  return pk.length > 20 ? `${pk.slice(0, 8)}…${pk.slice(-8)}` : pk
}

// An amber badge: a dot (shape) + a text label. Color is never the sole signal.
function Flag({ label }: { label: string }) {
  return (
    <span className={styles.flagBadge}>
      <span aria-hidden="true" className={styles.stateDot} />
      {label}
    </span>
  )
}

export function TagsPanel({ events, windowMeta }: { events: WindowEvent[]; windowMeta: WindowMeta }) {
  // Re-derives every render — Load more widens the analysis. analyzeTags is linear, so it
  // stays plain (only the O(n²) nearDup is memoized).
  const tags = analyzeTags(events.map((e) => ({ id: e.id, tags: e.tags })))
  const n = windowMeta.count
  const hasAnyTags =
    tags.topMentions.length > 0 || tags.topHashtags.length > 0 || tags.eventRefCount > 0

  return (
    <section className={styles.panel} aria-label="Tags & mentions">
      <div className={styles.head}>
        <h2 className={styles.title}>Tags &amp; mentions</h2>
      </div>

      {/* DRILL-05 — non-removable denominator, co-located, even at N=0. */}
      <WindowIndicator meta={windowMeta} />

      {n === 0 ? (
        <p className={styles.fact}>Computed over 0 fetched events — no tags to aggregate.</p>
      ) : !hasAnyTags ? (
        <p className={styles.fact}>No p/e/t tags among the {formatInt(n)} fetched events.</p>
      ) : (
        <>
          <div className={styles.lists}>
            {tags.topMentions.length > 0 && (
              <div className={styles.list}>
                <h3 className={styles.listHeading}>Most-mentioned pubkeys</h3>
                <ul className={styles.rows}>
                  {tags.topMentions.map((m) => (
                    <li key={m.value} className={styles.row}>
                      <span className={styles.rowValue} title={m.value}>
                        {truncPubkey(m.value)}
                      </span>
                      <span className={styles.rowCount}>{formatInt(m.count)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {tags.topHashtags.length > 0 && (
              <div className={styles.list}>
                <h3 className={styles.listHeading}>Top hashtags</h3>
                <ul className={styles.rows}>
                  {tags.topHashtags.map((h) => (
                    <li key={h.value} className={styles.row}>
                      <span className={styles.rowValue} title={h.value}>
                        {h.value}
                      </span>
                      <span className={styles.rowCount}>{formatInt(h.count)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>

          <p className={styles.refLine}>{formatInt(tags.eventRefCount)} event references</p>

          {tags.outlierEvents.length > 0 && (
            <ul className={styles.outliers}>
              {tags.outlierEvents.map((o) => (
                <li key={o.id} className={styles.outlierRow}>
                  <span className={styles.outlierId} title={o.id}>
                    {truncPubkey(o.id)}
                  </span>
                  <span className={styles.outlierFlags}>
                    {/* Amber labels driven by the analyzer flags — label + shape, never
                        color-only; an entry may carry more than one badge. highTagCount is
                        derived from the entry's tagCount against the same threshold the
                        analyzer used to include it. */}
                    {o.tagCount > TAGS.highTagCount && <Flag label="high tag count" />}
                    {o.massMention && <Flag label="high mention fan-out" />}
                    {o.stuffing && <Flag label="hashtag stuffing" />}
                  </span>
                </li>
              ))}
            </ul>
          )}

          {tags.malformedTagRows > 0 && (
            <p className={styles.fact}>
              {formatInt(tags.malformedTagRows)} malformed tag row
              {tags.malformedTagRows === 1 ? '' : 's'} skipped — counted, not mis-computed.
            </p>
          )}
        </>
      )}

      {/* PERSISTENT asymmetry note — always rendered, never dismissible. */}
      <p className={styles.caveat}>
        High mention/hashtag fan-out is suspicious; low fan-out does not clear this author.
      </p>
    </section>
  )
}

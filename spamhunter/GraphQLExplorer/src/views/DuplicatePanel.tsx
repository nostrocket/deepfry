// DuplicatePanel — the near-duplicate content signal surface (DRILL-02).
//
// Source: UI-SPEC § "Near-duplicate content panel" State Treatments + Copywriting
// Contract (strings VERBATIM: "Duplicate content", the "X of N fetched" summary,
// "exact duplicate" / "near-duplicate" badges, the zero-result fact, the asymmetry note);
// 03-PATTERNS (analog: RatePanel — panel shell, co-located WindowIndicator, amber badge,
// persistent caveat). Consumes the slice-03-01 pure analyzer `nearDup`.
//
// ASYMMETRY (load-bearing honesty contract):
//   - the result is ALWAYS framed against the window denominator ("X of N fetched") —
//     never a bare "0 duplicates" verdict;
//   - a detected cluster is marked with the amber (--recoverable) badge AND a text label
//     ("near-duplicate" / "exact duplicate") AND the dot shape — color is never alone;
//   - zero clusters reads as a NEUTRAL muted fact, never green, never "clean";
//   - the persistent asymmetry note is always rendered (absence ≠ exoneration).
//
// PERF / SELF-DoS BOUND: nearDup is O(n²) over the window. It is wrapped in useMemo keyed
// on the `events` array identity so it only recomputes when the window actually widens
// (Load more), not on unrelated parent re-renders.
//
// SECURITY: every value — counts and escaped single-line content previews — is rendered as
// a JSX text node (React default escaping); no raw-HTML injection sink is used.
import { useMemo, useState } from 'react'
import { nearDup } from '../analysis/nearDup'
import type { WindowEvent, WindowMeta } from '../hooks/useAuthorWindow'
import { WindowIndicator } from './WindowIndicator'
import styles from './DuplicatePanel.module.css'

const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

// Collapse a content preview to a single escaped line for cluster member listing.
function preview(content: string): string {
  const oneLine = content.replace(/\s+/g, ' ').trim()
  return oneLine.length > 140 ? `${oneLine.slice(0, 140)}…` : oneLine
}

function ClusterGroup({
  cluster,
  events,
}: {
  cluster: { kind: 'exact' | 'near'; memberIds: string[]; count: number }
  events: WindowEvent[]
}) {
  const [open, setOpen] = useState(false)
  const label = cluster.kind === 'exact' ? 'exact duplicate' : 'near-duplicate'
  const byId = new Map(events.map((e) => [e.id, e]))

  return (
    <div className={styles.cluster}>
      <button
        type="button"
        className={styles.clusterHead}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <span className={styles.clusterBadge}>
          <span aria-hidden="true" className={styles.stateDot} />
          {label}
        </span>
        <span className={styles.clusterCount}>{formatInt(cluster.count)} events</span>
      </button>
      {open && (
        <ul className={styles.memberList}>
          {cluster.memberIds.map((id) => {
            const ev = byId.get(id)
            return (
              <li key={id} className={styles.memberRow}>
                <span className={styles.memberKind}>{ev ? ev.kind : '?'}</span>
                <span className={styles.memberContent}>{ev ? preview(ev.content) : id}</span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

export function DuplicatePanel({
  events,
  windowMeta,
}: {
  events: WindowEvent[]
  windowMeta: WindowMeta
}) {
  // O(n²) — memoized on the events array identity so it only re-runs when the window
  // widens (Load more appends a new array), not on unrelated re-renders.
  const dup = useMemo(() => nearDup(events.map((e) => ({ id: e.id, content: e.content }))), [events])
  const n = windowMeta.count

  return (
    <section className={styles.panel} aria-label="Duplicate content">
      <div className={styles.head}>
        <h2 className={styles.title}>Duplicate content</h2>
      </div>

      {/* DRILL-05 — non-removable denominator, co-located, even at N=0. */}
      <WindowIndicator meta={windowMeta} />

      {n === 0 ? (
        <p className={styles.fact}>Computed over 0 fetched events — no content to compare.</p>
      ) : dup.clusters.length === 0 ? (
        <p className={styles.fact}>No near-duplicates among the {formatInt(n)} fetched events.</p>
      ) : (
        <>
          <p className={styles.summary}>
            {formatInt(dup.duplicateCount)} of {formatInt(n)} fetched are near-duplicates across{' '}
            {formatInt(dup.clusters.length)} cluster
            {dup.clusters.length === 1 ? '' : 's'}
          </p>
          <div className={styles.clusters}>
            {dup.clusters.map((cluster, i) => (
              <ClusterGroup key={`${cluster.kind}-${i}`} cluster={cluster} events={events} />
            ))}
          </div>
        </>
      )}

      {/* PERSISTENT asymmetry note — always rendered, never dismissible. */}
      <p className={styles.caveat}>
        Duplicate content is one spam signal among several — its absence does not clear this
        author.
      </p>
    </section>
  )
}

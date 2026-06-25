// TriageTable — the batch results surface (BATCH-02/03). A sortable, hand-rolled <table>
// (NO table/grid library) of transparent per-signal indicators over a deliberately tiny
// per-author window, with two batch window-honesty denominators, per-chunk error+retry,
// and a whole-row drill-in to the EXISTING #/a/<hex> view.
//
// Source: UI-SPEC § Sortable triage table + Copywriting Contract (all column captions,
// signal labels, the framing line, the batch denominators, and the chunk-error strings are
// VERBATIM); 04-PATTERNS (analog: AuthorDrillDown — table/rows render, WindowIndicator
// co-location, errorTreatment switch, row→hash nav).
//
// HONESTY CONTRACT (load-bearing, carried from Phases 2–3 at batch scale):
//  - NO spam-score column and NO clean/ok/safe verdict column. Each signal is its own
//    transparent column; absence of a tripped signal is NEVER a positive verdict.
//  - A tripped indicator is an AMBER chip + dot + the verbatim label ("burst" / "near-dup"
//    / "high fan-out"); an untripped one is a neutral "—". No green/teal anywhere.
//  - An author with zero matching events renders an explicit muted "0 events" cell — data,
//    not omission; distinct from a quiet non-empty row; never amber, never green.
//  - The persistent first-pass-screen framing + the non-removable "triaged N of M" batch
//    denominator are always shown, so a partial batch / quiet row can't read as a clean bill.
//
// SECURITY: the author hex is rendered as escaped plaintext via JSX (React default
// escaping) — never dangerouslySetInnerHTML (T-04-06). Per-chunk hard errors render the
// generic INTERNAL copy, never the server message (T-04-08).
import { useEffect, useMemo, useState } from 'react'
import { useLatestPerAuthor } from '../hooks/useLatestPerAuthor'
import { triageAuthor, type TriageIndicators } from '../analysis/triage'
import type { TriageRow } from '../analysis/mergeByAuthor'
import { TRIAGE } from '../analysis/thresholds'
import { ConnectingShell } from './ConnectingShell'
import styles from './BatchTriage.module.css'

// Locale-grouped integers, consistent with WindowIndicator's formatInt.
const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

// Truncate a 64-hex pubkey to first/last 8 for the Author cell (full hex on title hover),
// mirroring the Phase-3 mentioned-pubkey treatment.
function truncHex(hex: string): string {
  return hex.length <= 18 ? hex : `${hex.slice(0, 8)}…${hex.slice(-8)}`
}

type SortColumn = 'author' | 'events' | 'burst' | 'nearDup' | 'tagFanOut'
type SortDir = 'asc' | 'desc'

// A row paired with its computed indicators (triageAuthor is pure + order-insensitive).
interface ComputedRow {
  row: TriageRow
  ind: TriageIndicators
}

// Verbatim column captions + their a11y sort labels (UI-SPEC Copywriting Contract).
const COLUMNS: { key: SortColumn; caption: string }[] = [
  { key: 'author', caption: 'Author' },
  { key: 'events', caption: 'Events' },
  { key: 'burst', caption: 'Burst' },
  { key: 'nearDup', caption: 'Near-dup' },
  { key: 'tagFanOut', caption: 'Tag fan-out' },
]

// Inline sort caret (no icon library — UI-SPEC). Neutral; direction shown by glyph.
function SortCaret({ active, dir }: { active: boolean; dir: SortDir }) {
  if (!active) {
    return (
      <svg className={styles.sortCaret} width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
        <path d="M2 4 L5 1 L8 4 M2 6 L5 9 L8 6" fill="none" stroke="currentColor" strokeWidth="1" />
      </svg>
    )
  }
  return (
    <svg className={styles.sortCaret} width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
      {dir === 'asc' ? (
        <path d="M2 6 L5 2 L8 6" fill="none" stroke="currentColor" strokeWidth="1.5" />
      ) : (
        <path d="M2 4 L5 8 L8 4" fill="none" stroke="currentColor" strokeWidth="1.5" />
      )}
    </svg>
  )
}

// A single signal cell: amber chip + dot + verbatim label when tripped; neutral "—" when not.
function SignalCell({ tripped, label }: { tripped: boolean; label: string }) {
  if (!tripped) {
    return <span className={styles.absent}>—</span>
  }
  return (
    <span className={styles.signalChip}>
      <span aria-hidden="true" className={styles.stateDot} />
      {label}
    </span>
  )
}

// The batch "triaged N of M authors" denominator — non-removable while the table is shown,
// amber-on-partial (N < M). Reuses WindowIndicator's mono + amber-partial treatment.
function BatchIndicator({ n, m }: { n: number; m: number }) {
  if (m === 0) {
    return (
      <div className={styles.batchIndicator} role="status" aria-live="polite">
        Triaged 0 of 0 authors
      </div>
    )
  }
  if (n >= m) {
    return (
      <div className={styles.batchIndicator} role="status" aria-live="polite">
        Triaged {formatInt(m)} of {formatInt(m)} authors
      </div>
    )
  }
  // Partial — amber emphasis so a partial batch can't read as a complete picture.
  return (
    <div className={styles.batchIndicator} role="status" aria-live="polite">
      <span className={`${styles.note} ${styles.recoverable}`}>
        <span aria-hidden="true" className={styles.stateDot} />
        Triaged {formatInt(n)} of {formatInt(m)} authors — partial batch
      </span>
    </div>
  )
}

// Per-chunk recoverable-error copy (VERBATIM UI-SPEC). INTERNAL/unknown → generic hard copy
// that NEVER echoes the server message (T-04-08).
function chunkErrorTreatment(kind: string): { tone: 'recoverable' | 'hardFail'; message: string } {
  switch (kind) {
    case 'PAYLOAD_TOO_LARGE':
      return { tone: 'recoverable', message: 'That chunk was too large — shrinking and retrying.' }
    case 'NOT_READY':
      return { tone: 'recoverable', message: 'Relay is warming up — retrying this chunk…' }
    case 'INVALID_CURSOR':
    case 'NETWORK':
    case 'VALIDATION':
    case 'TOO_MANY_AUTHORS':
      return { tone: 'recoverable', message: 'Couldn’t load this chunk — Retry.' }
    default:
      return { tone: 'hardFail', message: 'Couldn’t triage these authors in this chunk.' }
  }
}

export function TriageTable({ inputHexes }: { inputHexes: string[] }) {
  const { rows, triagedCount, totalCount, loading, chunkErrors, run, retryChunk } =
    useLatestPerAuthor()

  // Start (or restart) the batch when the input set changes. The hook stale-drops a prior
  // run's late resolvers via its runId token, so a re-run is safe.
  useEffect(() => {
    run(inputHexes)
  }, [inputHexes, run])

  const [sortColumn, setSortColumn] = useState<SortColumn>('events')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // Compute indicators once per row, then sort PURELY in memory (no refetch — UI-SPEC).
  const computed: ComputedRow[] = useMemo(
    () => rows.map((row) => ({ row, ind: triageAuthor(row.events) })),
    [rows],
  )

  const sorted = useMemo(() => {
    const copy = [...computed]
    const factor = sortDir === 'asc' ? 1 : -1
    copy.sort((a, b) => {
      switch (sortColumn) {
        case 'author':
          return a.row.author.localeCompare(b.row.author) * factor
        case 'events':
          return (a.ind.eventCount - b.ind.eventCount) * factor
        case 'burst':
          return (Number(a.ind.burst) - Number(b.ind.burst)) * factor
        case 'nearDup':
          return (Number(a.ind.nearDup) - Number(b.ind.nearDup)) * factor
        case 'tagFanOut':
          return (Number(a.ind.tagFanOut) - Number(b.ind.tagFanOut)) * factor
      }
    })
    return copy
  }, [computed, sortColumn, sortDir])

  function onSort(column: SortColumn) {
    if (column === sortColumn) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortColumn(column)
      // Default a freshly-picked column to descending (most-signal / most-events first).
      setSortDir('desc')
    }
  }

  function drillIn(author: string) {
    // Reuse the EXISTING #/a/<hex> drill-down — author is normalized lowercase hex.
    window.location.hash = '#/a/' + author
  }

  // Persistent first-pass-screen framing — always rendered, non-dismissible.
  const framing = (
    <p className={styles.framing}>
      This table is a first-pass screen over the latest {TRIAGE.perAuthor} events per author —
      quiet rows are not cleared authors. Drill in for the full picture.
    </p>
  )

  // Connecting (first chunk, nothing on screen yet) — distinct info-blue shell.
  if (loading && rows.length === 0 && chunkErrors.length === 0) {
    return <ConnectingShell />
  }

  return (
    <section className={styles.tableSurface}>
      <BatchIndicator n={triagedCount} m={totalCount} />
      {framing}

      {chunkErrors.length > 0 && (
        <div className={styles.chunkErrors}>
          {chunkErrors.map(({ index, error }) => {
            const { tone, message } = chunkErrorTreatment(error.kind)
            const toneClass = tone === 'recoverable' ? styles.recoverable : styles.hardFail
            return (
              <div className={styles.chunkErrorRow} key={index} role="status" aria-live="polite">
                <span className={`${styles.note} ${toneClass}`}>
                  <span aria-hidden="true" className={styles.stateDot} />
                  {message}
                </span>
                <button
                  type="button"
                  className={styles.neutralButton}
                  onClick={() => retryChunk(index)}
                >
                  Retry
                </button>
              </div>
            )
          })}
        </div>
      )}

      {sorted.length === 0 ? (
        <div className={styles.emptyState}>No authors triaged yet.</div>
      ) : (
        <table className={styles.table}>
          <thead>
            <tr>
              {COLUMNS.map((col) => (
                <th key={col.key} className={styles.th} scope="col" aria-sort={
                  sortColumn === col.key ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'
                }>
                  <button
                    type="button"
                    className={styles.sortHeader}
                    onClick={() => onSort(col.key)}
                    aria-label={`Sort by ${col.caption}`}
                  >
                    {col.caption}
                    <SortCaret active={sortColumn === col.key} dir={sortDir} />
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sorted.map(({ row, ind }) => (
              <tr
                key={row.author}
                className={styles.row}
                tabIndex={0}
                role="link"
                aria-label="Open this author's drill-down"
                onClick={() => drillIn(row.author)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    drillIn(row.author)
                  }
                }}
              >
                <td className={styles.td}>
                  <span className={styles.authorHex} title={row.author}>
                    {truncHex(row.author)}
                  </span>
                </td>
                <td className={styles.td}>
                  {ind.eventCount === 0 ? (
                    <span className={styles.zeroEvents}>0 events</span>
                  ) : (
                    <span className={styles.eventCount}>{formatInt(ind.eventCount)}</span>
                  )}
                </td>
                <td className={styles.td}>
                  <SignalCell tripped={ind.burst} label="burst" />
                </td>
                <td className={styles.td}>
                  <SignalCell tripped={ind.nearDup} label="near-dup" />
                </td>
                <td className={styles.td}>
                  <SignalCell tripped={ind.tagFanOut} label="high fan-out" />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}

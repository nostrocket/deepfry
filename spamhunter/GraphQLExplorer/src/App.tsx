import { useQuery } from 'urql'
import { StatsDocument } from './queries/stats.graphql'

// Raw end-to-end proof (slice 1): issue the typed Stats query and render the
// four live scalars as plain text. This is NOT the polished dashboard — plan
// 01-03 builds StatsDashboard with full state treatments, empty-corpus copy,
// polling, and the nudge. No polling / readiness gating / nudge here.
export function App() {
  const [result] = useQuery({ query: StatsDocument })
  const { data, fetching, error } = result

  // GraphQL errors arrive on HTTP 200 (contract §7) — inspect error/fetching
  // before reading data. The full classifier (transport/errors.ts) is plan 01-02;
  // a minimal inline guard is sufficient for the skeleton.
  if (fetching) return <p>Connecting to relay…</p>
  if (error) return <p>Query failed: {error.message}</p>
  if (!data) return <p>No data.</p>

  const { eventCount, maxLevId, dbVersion, pinnedStrfryVersion } = data.stats

  // pinnedStrfryVersion is a backend String! — rendered as escaped plaintext via
  // normal JSX interpolation (React escapes by default). Never dangerouslySetInnerHTML (T-01-01).
  return (
    <main style={{ padding: 'var(--space-lg)', fontFamily: 'var(--font-mono)' }}>
      <h1 style={{ fontFamily: 'var(--font-sans)' }}>Corpus stats</h1>
      <dl>
        <dt>eventCount</dt>
        <dd>{eventCount}</dd>
        <dt>maxLevId</dt>
        <dd>{maxLevId}</dd>
        <dt>dbVersion</dt>
        <dd>{dbVersion}</dd>
        <dt>pinnedStrfryVersion</dt>
        <dd>{pinnedStrfryVersion}</dd>
      </dl>
    </main>
  )
}

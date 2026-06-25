// RawInspector — the lazy, on-demand canonical-bytes viewer for ONE event (DRILL-04).
//
// Source: UI-SPEC § "Lazy raw-JSON inspector" State Treatments + Copywriting Contract
// (strings VERBATIM: "View raw", "Loading raw…", "Close", the JSON / non-JSON captions,
// the retryable / hard error notes, the zero-match note); 03-PATTERNS (analog:
// useAuthorWindow's imperative classify-gated fetch + RatePanel.module.css <pre> styling).
//
// LAZY (load-bearing): the canonical bytes are NEVER fetched on mount — only when the
// analyst clicks "View raw". This is why the imperative client.query(...) pattern is used
// instead of the declarative urql hook (which fetches on mount). The throw-guard +
// classify-before-data discipline is copied verbatim from useAuthorWindow:128-152.
//
// SECURITY (T-03-05 / T-03-06 / T-03-07): the bytes are author-controlled. They render
// ONLY as an escaped JSX text node inside <pre> (React default escaping) — NEVER via a
// raw-HTML injection sink. JSON.parse is wrapped in try/catch (a non-JSON / huge payload
// falls back to verbatim, never crashes). The <pre> uses pre-wrap + break-all so a long
// line can't break layout. Fetch errors are mapped through the shared errorTreatment
// switch — generic copy, no server internals leaked.
import { useState } from 'react'
import { client } from '../transport/client'
import { classify } from '../transport/errors'
import { RawEventDocument } from '../queries/rawEvent.graphql'
import { errorTreatment } from './AuthorDrillDown'
import styles from './RawInspector.module.css'

type InspectorState =
  | { phase: 'idle' }
  | { phase: 'fetching' }
  | { phase: 'loaded'; body: string; isJson: boolean }
  | { phase: 'zeroMatch' }
  | { phase: 'error'; tone: 'recoverable' | 'hardFail'; message: string }

export function RawInspector({ id }: { id: string }) {
  const [state, setState] = useState<InspectorState>({ phase: 'idle' })

  async function fetchRaw() {
    setState({ phase: 'fetching' })

    // MANDATORY throw-guard (copied from useAuthorWindow:135): a rejected exchange
    // promise must classify as NETWORK, never kill the fetch silently.
    const result = await client
      .query(RawEventDocument, { filter: { ids: [id] }, limit: 1 }, { requestPolicy: 'network-only' })
      .toPromise()
      .catch(() => 'THREW' as const)

    if (result === 'THREW') {
      const { tone, message } = errorTreatment({ kind: 'NETWORK' })
      setState({ phase: 'error', tone, message })
      return
    }

    // classify() BEFORE reading data (errors arrive on HTTP 200 — contract §7).
    const apiError = classify(result)
    if (apiError) {
      const { tone, message } = errorTreatment(apiError)
      setState({ phase: 'error', tone, message })
      return
    }

    const raw = result.data?.events?.events?.[0]?.raw
    if (raw === undefined || raw === null) {
      setState({ phase: 'zeroMatch' })
      return
    }

    // Pretty-print only if it parses as JSON (Pitfall 4); verbatim otherwise.
    let body: string
    let isJson: boolean
    try {
      body = JSON.stringify(JSON.parse(raw), null, 2)
      isJson = true
    } catch {
      body = raw
      isJson = false
    }
    setState({ phase: 'loaded', body, isJson })
  }

  if (state.phase === 'idle') {
    return (
      <button type="button" className={styles.trigger} onClick={() => void fetchRaw()}>
        View raw
      </button>
    )
  }

  if (state.phase === 'fetching') {
    return (
      <button type="button" className={styles.trigger} disabled>
        Loading raw…
      </button>
    )
  }

  if (state.phase === 'error') {
    const toneClass = state.tone === 'recoverable' ? styles.recoverable : styles.hardFail
    return (
      <div className={`${styles.note} ${toneClass}`} role="status" aria-live="polite">
        <span aria-hidden="true" className={styles.stateDot} />
        <span>
          {state.tone === 'recoverable'
            ? 'Couldn’t load raw bytes — retrying.'
            : 'Couldn’t load the raw bytes for this event.'}
        </span>
      </div>
    )
  }

  if (state.phase === 'zeroMatch') {
    return (
      <div className={styles.zeroMatch}>
        <p className={styles.caption}>No canonical bytes returned for this event id.</p>
        <button
          type="button"
          className={styles.trigger}
          onClick={() => setState({ phase: 'idle' })}
        >
          Close
        </button>
      </div>
    )
  }

  // loaded — escaped <pre>, pretty (JSON) or verbatim (non-JSON).
  return (
    <div className={styles.raw}>
      <div className={styles.rawToolbar}>
        <span className={styles.caption}>
          {state.isJson
            ? 'Canonical bytes — escaped, not executed.'
            : 'Raw bytes shown verbatim (not valid JSON).'}
        </span>
        <button
          type="button"
          className={styles.trigger}
          onClick={() => setState({ phase: 'idle' })}
        >
          Close
        </button>
      </div>
      {/* Escaped text node ONLY — never a raw-HTML injection sink. */}
      <pre className={styles.pre}>{state.body}</pre>
    </div>
  )
}

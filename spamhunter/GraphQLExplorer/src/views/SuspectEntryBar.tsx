// SuspectEntryBar — the shell paste bar that takes an analyst from any view into an
// author drill-down (ID-01 / ID-02 / ID-03).
//
// Source: UI-SPEC § Suspect entry + Copywriting Contract (placeholder, CTA label, and
// the parse-failure string are VERBATIM); 02-PATTERNS (analogs: StatsDashboard Header
// control-row + InlineNote). The single sanctioned input handler is parseIdentifier
// (02-01) — this bar never re-implements nip19 decoding.
//
// ID-03 (load-bearing two-state distinction): a PARSE FAILURE shows an inline amber
// note and STAYS on the page — it never navigates and never shows an empty timeline.
// Only on a successful parse do we navigate by setting window.location.hash to the
// normalized lowercase hex (#/a/<hex>); "valid identifier, zero events" is decided
// downstream by the query, never here.
//
// SECURITY: a REJECTED_NSEC (a pasted secret key) is mapped to the SAME generic
// parse-failure note as NOT_RECOGNIZED — the UI never reveals that the input was a
// secret key, and the secret is never normalized, navigated to, or stored. All copy is
// escaped plaintext via JSX (React default escaping); no raw-HTML injection.
//
// COLOR: the "Inspect author" submit is the app's ONE accent action (UI-SPEC accent
// reservation #3). The input itself stays neutral (--surface / --border).
import { useState } from 'react'
import { parseIdentifier } from '../identifier/identifier'
import styles from './SuspectEntryBar.module.css'

export function SuspectEntryBar() {
  const [value, setValue] = useState('')
  const [parseError, setParseError] = useState(false)

  const trimmed = value.trim()
  const submitDisabled = trimmed.length === 0

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    const r = parseIdentifier(value)
    if (r.ok) {
      // Navigate to the normalized lowercase-hex route; clear any prior error.
      setParseError(false)
      window.location.hash = '#/a/' + r.hex
      return
    }
    // EMPTY can't reach here (submit is disabled when blank); NOT_RECOGNIZED and
    // REJECTED_NSEC both surface the same generic note — stay on the page.
    setParseError(true)
  }

  return (
    <form className={styles.bar} onSubmit={onSubmit} role="search">
      <div className={styles.controlRow}>
        <input
          className={styles.input}
          type="text"
          value={value}
          onChange={(e) => {
            setValue(e.target.value)
            if (parseError) setParseError(false)
          }}
          placeholder="Paste an npub, note, nprofile, or 64-char hex pubkey"
          aria-label="Suspect identifier"
          autoComplete="off"
          spellCheck={false}
        />
        <button type="submit" className={styles.submit} disabled={submitDisabled}>
          Inspect author
        </button>
      </div>
      {parseError && (
        <div className={`${styles.note} ${styles.recoverable}`} role="status" aria-live="polite">
          <span aria-hidden="true" className={styles.stateDot} />
          <span>Not a valid npub / note / nprofile or 64-char hex.</span>
        </div>
      )}
    </form>
  )
}

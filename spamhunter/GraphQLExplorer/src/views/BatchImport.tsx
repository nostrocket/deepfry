// BatchImport — the batch-triage entry view at #/batch (BATCH-01/04). Three import sources
// feed ONE deduped lowercase-hex set: a paste textarea, a .txt/.csv file upload, and an
// "enumerate corpus" action; an import summary reports valid / duplicates / unparseable
// (with the unparseable tokens LISTED as escaped plaintext, never silently dropped); a
// non-blocking large-set warning precedes a very large run; and a single accent "Triage"
// submit launches the TriageTable.
//
// Source: UI-SPEC § Batch-import view + Copywriting Contract (every copy string is
// VERBATIM); 04-PATTERNS (analog: SuspectEntryBar — the form + parseIdentifier + accent
// submit + escaped JSX + the neutral-input / accent-submit split).
//
// ACCENT RESERVATION (UI-SPEC): the "Triage" submit is the app's ONE new accent action this
// phase (the multi-author analog of "Inspect author"). The textarea, file picker, drop
// zone, "enumerate corpus", and Stop are all NEUTRAL chrome.
//
// SECURITY: every pasted/uploaded/enumerated token + every listed unparseable token is
// rendered as escaped plaintext via JSX interpolation — never dangerouslySetInnerHTML
// (T-04-06). The file is read in-browser via the FileReader platform API and is NEVER sent
// anywhere; TRIAGE.maxFileBytes is enforced before reading (T-04-07).
import { useMemo, useRef, useState } from 'react'
import { parseIdentifier } from '../identifier/identifier'
import { useAuthorEnumeration } from '../hooks/useAuthorEnumeration'
import { chunkAuthors, chunkSize } from '../analysis/chunk'
import { TRIAGE } from '../analysis/thresholds'
import { TriageTable } from './TriageTable'
import styles from './BatchTriage.module.css'

const NUMBER_FORMAT = new Intl.NumberFormat()
const formatInt = (n: number): string => NUMBER_FORMAT.format(n)

// The result of tokenizing + normalizing free text into a deduped lowercase-hex set.
interface ParsedTokens {
  /** Deduped lowercase-hex pubkeys, in first-seen order. */
  valid: string[]
  /** Count of duplicate valid tokens removed (valid occurrences beyond the first). */
  duplicates: number
  /** The raw tokens that failed to parse, verbatim (rendered as escaped plaintext). */
  unparseable: string[]
}

// Split free text on whitespace/commas, route EVERY token through the single sanctioned
// parseIdentifier (hex/npub/nprofile accepted; note/nsec/junk rejected), dedupe valid hexes
// into a Set, and count duplicates + collect the unparseable tokens. Never silently drops.
function parseBatchInput(text: string): ParsedTokens {
  const tokens = text.split(/[\s,]+/).filter((t) => t.length > 0)
  const seen = new Set<string>()
  const valid: string[] = []
  const unparseable: string[] = []
  let duplicates = 0
  for (const token of tokens) {
    const r = parseIdentifier(token)
    if (r.ok) {
      if (seen.has(r.hex)) {
        duplicates += 1
      } else {
        seen.add(r.hex)
        valid.push(r.hex)
      }
    } else {
      // EMPTY can't occur (empty tokens were filtered); NOT_RECOGNIZED + REJECTED_NSEC are
      // both "the analyst should see and fix this" — listed verbatim, never hex-normalized.
      unparseable.push(token)
    }
  }
  return { valid, duplicates, unparseable }
}

// Merge two deduped hex sets in first-seen order (paste/file ∪ enumerated), de-duplicating.
function mergeHexSets(a: string[], b: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const hex of [...a, ...b]) {
    if (!seen.has(hex)) {
      seen.add(hex)
      out.push(hex)
    }
  }
  return out
}

export function BatchImport() {
  // Paste/file source: the parsed tokens from the textarea + the most recent file read.
  const [pasteText, setPasteText] = useState('')
  const [fileTokens, setFileTokens] = useState<ParsedTokens | null>(null)
  const [fileError, setFileError] = useState<string | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Enumerate source.
  const enumeration = useAuthorEnumeration()

  // The active run: when set, render the TriageTable over this collected deduped set.
  const [runHexes, setRunHexes] = useState<string[] | null>(null)

  const pasteParsed = useMemo(() => parseBatchInput(pasteText), [pasteText])

  // The combined import summary across paste + file sources (sum the counts; union the hexes).
  const validHexes = useMemo(
    () => mergeHexSets(pasteParsed.valid, fileTokens?.valid ?? []),
    [pasteParsed.valid, fileTokens],
  )
  const duplicates = pasteParsed.duplicates + (fileTokens?.duplicates ?? 0)
  const unparseable = useMemo(
    () => [...pasteParsed.unparseable, ...(fileTokens?.unparseable ?? [])],
    [pasteParsed.unparseable, fileTokens],
  )

  // The full collected set fed to triage = paste/file valid ∪ enumerated authors.
  const collected = useMemo(
    () => mergeHexSets(validHexes, enumeration.authors),
    [validHexes, enumeration.authors],
  )

  const validCount = collected.length
  const hasInput = pasteText.trim().length > 0 || fileTokens !== null || enumeration.authors.length > 0
  const submitDisabled = validCount < 1

  // Large-set warning (non-blocking): how many chunked queries the run would issue.
  const showLargeSetWarning = validCount > TRIAGE.largeSetWarn
  const chunkCount = showLargeSetWarning ? chunkAuthors(collected, chunkSize()).length : 0

  function onFileSelected(file: File | null) {
    setFileError(null)
    if (!file) return
    if (file.size > TRIAGE.maxFileBytes) {
      // Amber inline note — no hard block; paste still works.
      setFileError(`it exceeds the ${formatInt(TRIAGE.maxFileBytes)}-byte limit`)
      return
    }
    const reader = new FileReader()
    reader.onerror = () => setFileError('it could not be read')
    reader.onload = () => {
      const text = typeof reader.result === 'string' ? reader.result : ''
      setFileTokens(parseBatchInput(text))
    }
    reader.readAsText(file)
  }

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (submitDisabled) return
    // Lift the collected deduped hex set into a fresh array reference so the TriageTable's
    // effect re-runs on each Triage submit (even if the contents are identical).
    setRunHexes([...collected])
  }

  return (
    <main className={styles.view}>
      <h1 className={styles.title}>Batch triage</h1>
      <p className={styles.framing}>
        This is a first-pass screen over the latest {TRIAGE.perAuthor} events per author —
        quiet rows are not cleared authors. Drill in for the full picture.
      </p>

      <form className={styles.card} onSubmit={onSubmit}>
        {/* ── Source 1: paste (neutral) ─────────────────────────────────────── */}
        <div className={styles.sourceBlock}>
          <label className={styles.sourceLabel} htmlFor="batch-paste">
            Paste pubkeys
          </label>
          <textarea
            id="batch-paste"
            className={styles.textarea}
            value={pasteText}
            onChange={(e) => setPasteText(e.target.value)}
            placeholder="Paste npub / nprofile / 64-char hex pubkeys — one per line, or separated by spaces or commas"
            aria-label="Paste pubkeys"
            spellCheck={false}
            autoComplete="off"
          />
        </div>

        {/* ── Source 2: file upload (neutral) ───────────────────────────────── */}
        <div className={styles.sourceBlock}>
          <span className={styles.sourceLabel}>Upload a .txt or .csv of pubkeys</span>
          <div
            className={`${styles.dropZone} ${dragOver ? styles.dropZoneOver : ''}`}
            onDragOver={(e) => {
              e.preventDefault()
              setDragOver(true)
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={(e) => {
              e.preventDefault()
              setDragOver(false)
              onFileSelected(e.dataTransfer.files?.[0] ?? null)
            }}
          >
            <span className={styles.dropHint}>Drop a .txt / .csv here, or choose a file</span>
            <input
              ref={fileInputRef}
              className={styles.visuallyHidden}
              type="file"
              accept=".txt,.csv,text/plain,text/csv"
              onChange={(e) => onFileSelected(e.target.files?.[0] ?? null)}
            />
            <button
              type="button"
              className={styles.neutralButton}
              onClick={() => fileInputRef.current?.click()}
            >
              Choose a file
            </button>
            {fileError && (
              <span className={`${styles.note} ${styles.recoverable}`} role="status" aria-live="polite">
                <span aria-hidden="true" className={styles.stateDot} />
                Couldn’t read that file — {fileError}.
              </span>
            )}
          </div>
        </div>

        {/* ── Source 3: enumerate corpus (neutral) ──────────────────────────── */}
        <div className={styles.sourceBlock}>
          <span className={styles.sourceLabel}>Enumerate corpus authors</span>
          <div className={styles.enumRow}>
            {!enumeration.enumerating ? (
              <button
                type="button"
                className={styles.neutralButton}
                onClick={() => enumeration.start()}
              >
                Enumerate corpus authors
              </button>
            ) : (
              <>
                <span className={styles.connecting}>
                  <span aria-hidden="true" className={styles.stateDot} />
                  Enumerating… <span className={styles.enumCount}>{formatInt(enumeration.runningCount)}</span>{' '}
                  distinct authors
                </span>
                <button type="button" className={styles.neutralButton} onClick={() => enumeration.stop()}>
                  Stop
                </button>
              </>
            )}
          </div>
          {!enumeration.enumerating && enumeration.complete && (
            <span className={styles.summaryLine} role="status" aria-live="polite">
              <span className={styles.summaryCount}>{formatInt(enumeration.runningCount)}</span> distinct
              authors as of this fetch
            </span>
          )}
          {!enumeration.enumerating && enumeration.stopped && (
            <span className={`${styles.note} ${styles.recoverable}`} role="status" aria-live="polite">
              <span aria-hidden="true" className={styles.stateDot} />
              Snapshot — stopped early at {formatInt(enumeration.runningCount)} authors (incomplete)
            </span>
          )}
          {!enumeration.enumerating && enumeration.error && (
            <span className={`${styles.note} ${styles.recoverable}`} role="status" aria-live="polite">
              <span aria-hidden="true" className={styles.stateDot} />
              {enumeration.error.kind === 'INVALID_CURSOR'
                ? 'Enumeration cursor expired — restarting from the top.'
                : 'Relay is warming up — retrying enumeration…'}
            </span>
          )}
        </div>

        {/* ── Import summary (neutral, amber on the unparseable segment) ─────── */}
        {hasInput && (
          <div className={styles.summary}>
            <div className={styles.summaryLine}>
              <span>
                <span className={styles.summaryCount}>{formatInt(validCount)}</span>{' '}
                <span className={styles.summaryLabel}>valid</span>
              </span>
              <span aria-hidden="true">·</span>
              <span>
                <span className={styles.summaryCount}>{formatInt(duplicates)}</span>{' '}
                <span className={styles.summaryLabel}>duplicates removed</span>
              </span>
              <span aria-hidden="true">·</span>
              <span className={styles.unparseableSeg}>
                <span aria-hidden="true" className={styles.stateDot} />
                <span className={styles.summaryCount}>{formatInt(unparseable.length)}</span>{' '}
                <span className={styles.summaryLabel}>unparseable</span>
              </span>
            </div>

            {unparseable.length > 0 && (
              <>
                <p className={styles.unparseableHeading}>
                  Couldn’t parse these {formatInt(unparseable.length)} — fix or remove them:
                </p>
                <div className={styles.unparseableList}>
                  {unparseable.map((token, i) => (
                    // Escaped plaintext via JSX interpolation — NEVER dangerouslySetInnerHTML.
                    <span className={styles.unparseableToken} key={`${i}:${token}`} title={token}>
                      {token}
                    </span>
                  ))}
                </div>
              </>
            )}

            {validCount === 0 && (
              <p className={styles.zeroValid}>
                0 valid pubkeys — paste or upload npub / nprofile / 64-char hex.
              </p>
            )}
          </div>
        )}

        {/* ── Submit row: the SINGLE accent action + the non-blocking warning ── */}
        <div className={styles.submitRow}>
          <button type="submit" className={styles.triageSubmit} disabled={submitDisabled}>
            Triage
          </button>
          {showLargeSetWarning && (
            <span className={`${styles.note} ${styles.recoverable}`} role="status" aria-live="polite">
              <span aria-hidden="true" className={styles.stateDot} />
              Triaging {formatInt(validCount)} authors runs {formatInt(chunkCount)} chunked queries.
              Continue?
            </span>
          )}
        </div>
      </form>

      {runHexes !== null && <TriageTable inputHexes={runHexes} />}
    </main>
  )
}

// Burst-analysis tunables — the SINGLE tunable home for the rate/burst analyzer
// (CONTEXT discretion: "where burst constants live"), mirroring the
// POLL_INTERVAL_MS single-named-tunable convention in useStatsPoll.ts.
//
// These are sane, literature-grounded DEFAULTS, not corpus-validated values:
// tight interarrival clustering is the strongest single temporal automation signal,
// and these starting points come from bot-detection literature (RESEARCH § Pattern 3).
// Corpus-validation of the exact numbers is explicitly DEFERRED to Phase 3 (STATE).
//
// The honesty posture (an honest denominator + a permanent forgeable caveat, with NO
// "clean" verdict) holds regardless of how these thresholds are tuned — the numbers
// only move where the suspicious-when-present line sits, never the epistemics.
export const BURST = {
  windowSec: 60, // a burst = enough events inside this sliding window of seconds
  minEvents: 5, // that many events within windowSec flags a burst worth investigating
  binSec: 3600, // fixed-width display bins (seconds) for the hand-rolled rate bars
} as const

// Near-duplicate detection tunables (DRILL-02) — same posture as BURST above.
// k=3 word shingles + Jaccard 0.8 are standard textbook defaults for near-dup text
// clustering (RESEARCH § Pattern 1, A2). Corpus-validation of the exact numbers is
// explicitly DEFERRED to the Phase-3 corpus pass (STATE blocker); the honesty posture
// (an honest "X of N fetched" denominator with NO "clean" verdict) is threshold-
// independent — these numbers only move where the suspicious-when-present line sits.
export const NEAR_DUP = {
  k: 3, // word-shingle size for stage-2 similarity
  jaccard: 0.8, // shingle-set Jaccard at/above this groups two posts as near-duplicates
} as const

// Tag/mention aggregation tunables (DRILL-03) — conservative discretion defaults
// (RESEARCH A3), same posture as BURST/NEAR_DUP. These are deliberately high so a flag
// means a genuinely unusual fan-out / hashtag load, not normal threading. Corpus-
// validation is DEFERRED to Phase 3 (STATE); the honesty posture holds regardless — a
// tripped flag is a suspicious-when-present signal, never a "clean" verdict when absent.
export const TAGS = {
  highTagCount: 20, // total tag rows on a single event above this = a high-tag outlier
  massMention: 20, // p-mention (fan-out) count on one event above this = mass-mention
  stuffing: 15, // t-hashtag count on one event above this = hashtag stuffing
} as const

// Batch-triage tunables (BATCH-01..04) — same single-tunable-home + honesty posture as
// BURST/NEAR_DUP/TAGS above. These are Claude's-discretion defaults (RESEARCH A1–A5), not
// corpus-validated values; they govern WHERE the chunking / first-pass-screen lines sit,
// never the epistemics. perAuthor=5 is a deliberately tiny first-pass window — a quiet or
// zero-event author is INCONCLUSIVE, never "clean", regardless of how these numbers move.
export const TRIAGE = {
  kind: 1, // text notes — the spam-bearing kind screened in a batch pass (BATCH-02)
  perAuthor: 5, // deliberately tiny per-author window — a FIRST-PASS screen, not a verdict
  chunkAuthors: 500, // conservative static chunk; the <=1000-author cap binds before 256 KiB
  largeSetWarn: 1000, // non-blocking warning threshold above this many input authors
  enumLimit: 500, // authors() page-size ceiling (contract §12)
  maxFileBytes: 5 * 1024 * 1024, // in-browser upload bound (V12) — files are never sent anywhere
  maxEnumPages: 10000, // WR-03 hard page ceiling — bound the enumeration loop so a stuck
  // cursor / pathological backend cannot spin forever. At enumLimit=500 this admits up to
  // ~5,000,000 distinct authors before bailing — far above any realistic corpus, while still
  // guaranteeing termination. The no-progress detector (an empty page with hasMore:true, or a
  // non-advancing cursor) bails much sooner; this ceiling is the last-resort backstop.
} as const

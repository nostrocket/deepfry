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

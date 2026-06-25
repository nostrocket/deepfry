// NIP event-kind number → human name lookup (DRILL-04). Pure static data, no I/O.
//
// Known kinds get a human label in the histogram; unknown kinds render the raw number
// alone (UI-SPEC). This is Claude's-discretion lookup content (CONTEXT) — a small,
// commonly-deployed subset, not an exhaustive registry.
//
// [CITED: github.com/nostr-protocol/nips README "Event Kinds"]
export const KIND_NAMES: Record<number, string> = {
  0: 'Metadata',
  1: 'Short Text Note',
  3: 'Follows',
  4: 'Encrypted DM',
  5: 'Deletion Request',
  6: 'Repost',
  7: 'Reaction',
  1984: 'Reporting',
  9735: 'Zap',
  10002: 'Relay List',
  30023: 'Long-form',
}

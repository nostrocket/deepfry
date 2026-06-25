// Two-stage near-duplicate content detector (DRILL-02) — a PURE function over the
// fetched window's author-claimed content. No React, no network I/O of any kind.
//
// ASYMMETRY (RESEARCH § Pattern 1, Anti-pattern; mirrors RateResult): a near-duplicate
// cluster is suspicious-when-present — repeated/templated text is a strong manual-spam
// signal worth investigating. But the ABSENCE of duplicates proves nothing (an author
// can post unique spam), so NearDupResult carries NO clean/ok/safe field by construction;
// the absence is structural, not an oversight. Framing is always "X of N fetched".
//
// TWO STAGES:
//   1. Exact bucket — group by normalizeContent(content) (NFC + lowercase + whitespace
//      collapse + trim). Byte-identical-after-normalization posts form an 'exact' cluster.
//   2. Near bucket — among the remaining posts, word-shingle (k) Jaccard >= NEAR_DUP.jaccard
//      unions pairs via union-find (disjoint set), giving deterministic, order-independent
//      transitive clusters (A≈B, B≈C → one cluster even if A≉C). Greedy first-match would
//      be order-dependent; union-find is the honest, reproducible choice.
//
// OVER-MERGE GUARD (CONTEXT): normalizeContent PRESERVES URLs / mentions / punctuation
// verbatim — spam frequently varies only in those, and removing them over-merges
// distinct posts. Normalization is case + whitespace + Unicode-form only.
// SELF-DoS BOUND (Pitfall 1 / T-03-03): stage-2 is O(n²); each shingle Set is precomputed
// ONCE and a size-disparity short-circuit skips pairs that cannot reach the cutoff.
import { NEAR_DUP } from './thresholds'

/**
 * Exact-hash stage key: NFC + lowercase + collapse internal whitespace + trim.
 * Deliberately PRESERVES URLs / mentions / punctuation verbatim (CONTEXT over-merge guard).
 *
 * HOSTILE INPUT (WR-04, parity with analyzeTags): `content` is author-supplied and reaches
 * this analyzer via an unchecked `page.events as WindowEvent[]` cast — a partial-error
 * payload can deliver `null`/`undefined`/non-string `content` the type checker cannot see.
 * A non-string is coerced to '' (it normalizes to an empty key + empty shingle Set, so it
 * only ever exact-buckets with other empties and never near-matches a substantive post),
 * never thrown — mirroring the defensive posture tags.ts already has.
 */
export function normalizeContent(s: string): string {
  if (typeof s !== 'string') return ''
  return s.normalize('NFC').toLowerCase().replace(/\s+/g, ' ').trim()
}

/**
 * Word-shingle Set of size k over normalized content. Posts shorter than k words fall
 * back to a single whole-text shingle (not an empty Set) so an identical short post can
 * still match; empty content yields an empty Set.
 */
export function shingles(normalized: string, k: number): Set<string> {
  const words = normalized.split(' ').filter(Boolean)
  const out = new Set<string>()
  if (words.length < k) {
    if (words.length > 0) out.add(words.join(' '))
    return out
  }
  for (let i = 0; i + k <= words.length; i++) out.add(words.slice(i, i + k).join(' '))
  return out
}

/** Jaccard similarity of two shingle Sets (|A∩B| / |A∪B|). Two empty Sets → 1. */
export function jaccard(a: Set<string>, b: Set<string>): number {
  if (a.size === 0 && b.size === 0) return 1
  let inter = 0
  for (const x of a) if (b.has(x)) inter++
  const union = a.size + b.size - inter
  return union === 0 ? 0 : inter / union
}

/** Disjoint-set (union-find) with path-halving find and union-by-attach. */
class DSU {
  private parent: number[]
  constructor(n: number) {
    this.parent = Array.from({ length: n }, (_, i) => i)
  }
  find(x: number): number {
    while (this.parent[x] !== x) {
      this.parent[x] = this.parent[this.parent[x]]
      x = this.parent[x]
    }
    return x
  }
  union(a: number, b: number): void {
    const ra = this.find(a)
    const rb = this.find(b)
    if (ra !== rb) this.parent[ra] = rb
  }
}

export interface NearDupResult {
  /** Denominator — total events fed to the analysis. */
  analyzedCount: number
  /**
   * Detected duplicate/near-duplicate clusters (each has >= 2 members). 'exact' when all
   * members share one stage-1 normalized bucket, else 'near'. No singleton clusters.
   */
  clusters: { kind: 'exact' | 'near'; memberIds: string[]; count: number }[]
  /** Total member count across all clusters — the "X" in "X of N fetched". */
  duplicateCount: number
}

/**
 * Group exact-duplicate and near-duplicate posts over the fetched window. Asymmetric (see
 * file header): NO clean field. Inconclusive empty result for < 2 events (nothing to
 * compare), no crash.
 */
export function nearDup(events: { id: string; content: string }[]): NearDupResult {
  const n = events.length

  // < 2 events → nothing to compare. Inconclusive, no crash.
  if (n < 2) {
    return { analyzedCount: n, clusters: [], duplicateCount: 0 }
  }

  // (1) Precompute the normalized key and shingle Set for each event ONCE (Pitfall 1).
  const keys: string[] = new Array(n)
  const sets: Set<string>[] = new Array(n)
  for (let i = 0; i < n; i++) {
    keys[i] = normalizeContent(events[i].content)
    sets[i] = shingles(keys[i], NEAR_DUP.k)
  }

  const dsu = new DSU(n)

  // (2) Stage 1 — union events sharing an identical normalized key (exact duplicates).
  //     Empty-normalized content keys on '' and only unions with other empties; it does
  //     NOT near-match substantive posts in stage 2 (its shingle Set is empty).
  const buckets = new Map<string, number[]>()
  for (let i = 0; i < n; i++) {
    const list = buckets.get(keys[i])
    if (list) list.push(i)
    else buckets.set(keys[i], [i])
  }
  for (const members of buckets.values()) {
    for (let j = 1; j < members.length; j++) dsu.union(members[0], members[j])
  }

  // (3) Stage 2 — pairwise word-shingle Jaccard over the precomputed Sets. Skip a pair
  //     early when the size disparity makes the cutoff impossible (Pitfall 1 bound). An
  //     empty shingle Set (empty content) never near-matches a substantive post.
  const cutoff = NEAR_DUP.jaccard
  const maxDisparity = 1 - cutoff
  for (let i = 0; i < n; i++) {
    const a = sets[i]
    if (a.size === 0) continue
    for (let j = i + 1; j < n; j++) {
      const b = sets[j]
      if (b.size === 0) continue
      if (dsu.find(i) === dsu.find(j)) continue // already unioned (e.g. exact bucket)
      const larger = Math.max(a.size, b.size)
      if (Math.abs(a.size - b.size) / larger > maxDisparity) continue
      if (jaccard(a, b) >= cutoff) dsu.union(i, j)
    }
  }

  // (4) Collect members by root; a cluster needs >= 2 members. Tag 'exact' when every
  //     member shares one normalized key, else 'near'.
  const groups = new Map<number, number[]>()
  for (let i = 0; i < n; i++) {
    const root = dsu.find(i)
    const list = groups.get(root)
    if (list) list.push(i)
    else groups.set(root, [i])
  }

  const clusters: NearDupResult['clusters'] = []
  let duplicateCount = 0
  for (const members of groups.values()) {
    if (members.length < 2) continue
    const firstKey = keys[members[0]]
    const allSameKey = members.every((m) => keys[m] === firstKey)
    clusters.push({
      kind: allSameKey ? 'exact' : 'near',
      memberIds: members.map((m) => events[m].id),
      count: members.length,
    })
    duplicateCount += members.length
  }

  return { analyzedCount: n, clusters, duplicateCount }
}

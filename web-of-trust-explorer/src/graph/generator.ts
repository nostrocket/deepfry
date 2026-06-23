/**
 * Barabási–Albert synthetic graph generator (linear preferential attachment).
 *
 * Writes straight into flat typed arrays with ZERO per-node heap objects so the
 * same code scales from ~1,000 nodes (Plan 01 walking skeleton) to 5,000,000
 * nodes / 30,000,000 edges (Plan 02 GPU-ceiling spike) inside the 18 GB pool.
 *
 * Algorithm (01-RESEARCH.md § Synthetic Power-Law Generator):
 *  - Each new node attaches `m` out-edges to existing nodes.
 *  - Targets are chosen by the BA *edge-copying* trick: a flat "target pool"
 *    Uint32Array holds each node id once per in-edge it has received. Picking a
 *    random pool slot yields a target with probability proportional to its
 *    current degree, in O(1) per edge — no cumulative-sum rebuild.
 *  - A seeded mulberry32 PRNG makes runs reproducible (no Math.random).
 *
 * Result: power-law degree distribution (γ≈3), a few mega-hubs, long leaf tail.
 */

const SPACE_SIZE = 8192;

/** Deterministic, fast 32-bit PRNG. Returns floats in [0,1). */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return function next(): number {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface GeneratedGraph {
  /** Node positions, interleaved [x0,y0,…]. length = nodeCount*2. */
  positions: Float32Array;
  /** Directed edges as exact uint32 index pairs [src0,tgt0,…]. length = edgeCount*2. */
  links: Uint32Array;
  nodeCount: number;
  edgeCount: number;
}

/**
 * Generate a Barabási–Albert graph.
 *
 * @param nodeCount total number of nodes (N)
 * @param m         out-edges added per new node (default 6 → ~N*m edges)
 * @param seed      PRNG seed for reproducibility (default 1)
 */
export function generateBA(nodeCount: number, m = 6, seed = 1): GeneratedGraph {
  if (nodeCount < 1) {
    return { positions: new Float32Array(0), links: new Uint32Array(0), nodeCount: 0, edgeCount: 0 };
  }
  const rand = mulberry32(seed);

  // Seed clique: the first m0 nodes form a small fully-connected core so early
  // nodes have something to attach to. Use m0 = m (capped at nodeCount).
  const m0 = Math.min(m, nodeCount);

  // Positions: random seed across the layout space (cosmos.gl rescales by default).
  const positions = new Float32Array(nodeCount * 2);
  for (let i = 0; i < nodeCount; i++) {
    positions[i * 2] = rand() * SPACE_SIZE;
    positions[i * 2 + 1] = rand() * SPACE_SIZE;
  }

  // Upper bound on edges: seed-clique directed edges + m per node after the clique.
  const seedEdges = m0 * (m0 - 1); // directed both ways within the clique
  const maxEdges = seedEdges + (nodeCount - m0) * m;
  const links = new Uint32Array(maxEdges * 2);

  // Target pool: each node id appears once per in-edge received. Clique edges
  // push 1 entry each; each non-clique edge pushes 2 (target + source). Pre-size
  // to that upper bound (+1 for the degenerate-clique seed entry).
  const pool = new Uint32Array(seedEdges + (nodeCount - m0) * m * 2 + 1);
  let poolLen = 0;
  let edgeCount = 0;

  // Build the seed clique (directed edges in both directions).
  for (let i = 0; i < m0; i++) {
    for (let j = 0; j < m0; j++) {
      if (i === j) continue;
      links[edgeCount * 2] = i;
      links[edgeCount * 2 + 1] = j;
      edgeCount++;
      pool[poolLen++] = j; // j received an in-edge
    }
  }
  // If the clique is degenerate (m0 < 2 → no edges), seed the pool with node 0
  // so preferential attachment has a target.
  if (poolLen === 0) {
    pool[poolLen++] = 0;
  }

  // Attach each subsequent node with m preferential-attachment out-edges.
  for (let i = m0; i < nodeCount; i++) {
    // Pick m distinct targets from the pool (allow repeats across nodes; avoid
    // self-loops and intra-node duplicates).
    const chosen = new Set<number>();
    let attempts = 0;
    while (chosen.size < m && attempts < m * 8) {
      attempts++;
      const tgt = pool[(rand() * poolLen) | 0]!;
      if (tgt === i || chosen.has(tgt)) continue;
      chosen.add(tgt);
    }
    for (const tgt of chosen) {
      links[edgeCount * 2] = i;
      links[edgeCount * 2 + 1] = tgt;
      edgeCount++;
      pool[poolLen++] = tgt; // tgt gained an in-edge → more likely to be chosen
      pool[poolLen++] = i; // i is now in the network too (gives leaves some weight)
    }
  }

  // Trim the link buffer to the exact edge count (pool over-allocated for safety).
  const trimmedLinks = links.subarray(0, edgeCount * 2);
  return {
    positions,
    links: new Uint32Array(trimmedLinks), // detach from the over-sized backing buffer
    nodeCount,
    edgeCount,
  };
}

/**
 * In-degree (follower count) per node in ONE O(E) pass over the link buffer.
 *
 * cosmos.gl needs node-degree for sizing/coloring, and the WoT graph cares about
 * followers (in-degree) as influence. We derive it directly from the edge buffer
 * — a single loop incrementing `inDegree[tgt]` — rather than issuing a separate
 * `followers` query or building per-node objects (D-08; 01-RESEARCH.md
 * anti-patterns). At 5M nodes this is one Uint32Array (~20 MB) and one linear
 * scan of the ~60M-entry link buffer.
 *
 * @param links     interleaved [src0,tgt0,src1,tgt1,…] edge buffer (length edgeCount*2)
 * @param nodeCount number of nodes (length of the returned array)
 * @param edgeCount number of directed edges
 * @returns Uint32Array of length nodeCount; sum of all entries === edgeCount
 */
export function computeInDegree(
  links: Uint32Array,
  nodeCount: number,
  edgeCount: number,
): Uint32Array {
  const inDegree = new Uint32Array(nodeCount);
  for (let e = 0; e < edgeCount; e++) {
    const tgt = links[e * 2 + 1]!;
    inDegree[tgt]!++;
  }
  return inDegree;
}

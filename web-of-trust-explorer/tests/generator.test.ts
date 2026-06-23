import { describe, it, expect } from 'vitest';
import { generateBA, computeInDegree } from '../src/graph/generator';

/**
 * DATA-03: the Barabási–Albert generator must yield exactly N nodes, ~N*m
 * directed edges, typed-array buffers with the correct invariants, a skewed
 * (power-law) degree distribution, and be deterministic given a fixed seed.
 */
describe('BA synthetic generator', () => {
  const N = 1000;
  const M = 6;

  it('returns exactly N nodes and a Uint32Array link buffer of length edgeCount*2', () => {
    const g = generateBA(N, M, 42);
    expect(g.nodeCount).toBe(N);
    expect(g.links).toBeInstanceOf(Uint32Array);
    expect(g.links.length).toBe(g.edgeCount * 2);
  });

  it('produces ~N*m directed edges', () => {
    const g = generateBA(N, M, 42);
    // Each new node beyond the seed clique adds m out-edges. Allow a small band
    // around N*m to accommodate the seed-clique edges at the start.
    expect(g.edgeCount).toBeGreaterThanOrEqual(N * M - M * M);
    expect(g.edgeCount).toBeLessThanOrEqual(N * M + M * M);
  });

  it('returns a Float32Array positions buffer of length nodeCount*2', () => {
    const g = generateBA(N, M, 42);
    expect(g.positions).toBeInstanceOf(Float32Array);
    expect(g.positions.length).toBe(N * 2);
  });

  it('produces a skewed degree distribution (max in-degree >> mean)', () => {
    const g = generateBA(N, M, 42);
    const inDegree = new Uint32Array(N);
    for (let e = 0; e < g.edgeCount; e++) {
      const tgt = g.links[e * 2 + 1]!;
      inDegree[tgt]!++;
    }
    const max = Math.max(...inDegree);
    const mean = g.edgeCount / N;
    // Hub skew: the biggest hub should dwarf the mean.
    expect(max).toBeGreaterThan(mean * 5);
  });

  it('is deterministic given a fixed seed (mulberry32)', () => {
    const a = generateBA(N, M, 12345);
    const b = generateBA(N, M, 12345);
    expect(a.edgeCount).toBe(b.edgeCount);
    expect(Array.from(a.links)).toEqual(Array.from(b.links));
    expect(Array.from(a.positions)).toEqual(Array.from(b.positions));
  });

  it('produces different graphs for different seeds', () => {
    const a = generateBA(N, M, 1);
    const b = generateBA(N, M, 2);
    expect(Array.from(a.links)).not.toEqual(Array.from(b.links));
  });

  it('keeps all target indices within node bounds (valid edges)', () => {
    const g = generateBA(N, M, 7);
    for (let i = 0; i < g.links.length; i++) {
      expect(g.links[i]!).toBeGreaterThanOrEqual(0);
      expect(g.links[i]!).toBeLessThan(N);
    }
  });
});

/**
 * Plan 02: the generator must hold all its invariants at MID scale (not just the
 * ~1k walking-skeleton size) so we can trust the 5M runtime path. We test at 50k
 * — large enough to exercise the SoA/pool code paths, small enough to run fast in
 * CI (NEVER generate the full 5M inside the test suite; that path is exercised
 * only at runtime via `npm run dev` — Plan 02 memory-caution).
 */
describe('BA synthetic generator at mid scale (50k, m=6)', () => {
  const N = 50_000;
  const M = 6;

  it('yields exactly N nodes, a Uint32Array link buffer, and ~N*m edges', () => {
    const g = generateBA(N, M, 42);
    expect(g.nodeCount).toBe(N);
    expect(g.links).toBeInstanceOf(Uint32Array);
    expect(g.links.length).toBe(g.edgeCount * 2);
    expect(g.edgeCount).toBeGreaterThanOrEqual(N * M - M * M);
    expect(g.edgeCount).toBeLessThanOrEqual(N * M + M * M);
  });

  it('returns a Float32Array positions buffer of length nodeCount*2', () => {
    const g = generateBA(N, M, 42);
    expect(g.positions).toBeInstanceOf(Float32Array);
    expect(g.positions.length).toBe(N * 2);
  });

  it('is deterministic under a fixed seed at mid scale', () => {
    const a = generateBA(N, M, 99);
    const b = generateBA(N, M, 99);
    expect(a.edgeCount).toBe(b.edgeCount);
    // Compare the whole link buffer cheaply without materialising two huge arrays.
    expect(a.links.length).toBe(b.links.length);
    let mismatch = -1;
    for (let i = 0; i < a.links.length; i++) {
      if (a.links[i] !== b.links[i]) {
        mismatch = i;
        break;
      }
    }
    expect(mismatch).toBe(-1);
  });

  it('produces a power-law-skewed degree distribution at mid scale', () => {
    const g = generateBA(N, M, 42);
    const inDegree = computeInDegree(g.links, g.nodeCount, g.edgeCount);
    let max = 0;
    for (let i = 0; i < inDegree.length; i++) if (inDegree[i]! > max) max = inDegree[i]!;
    const mean = g.edgeCount / N;
    // The biggest hub should dwarf the mean by a wide margin (power-law tail).
    expect(max).toBeGreaterThan(mean * 20);
  });
});

/**
 * The O(E) in-degree pass: a single loop over the link buffer producing a
 * Uint32Array of length nodeCount where inDegree[tgt]++ for every edge. No
 * `followers` query, no per-node objects (D-08). The total must equal edgeCount.
 */
describe('computeInDegree (O(E) pass)', () => {
  it('counts in-degree per node from a known small edge buffer', () => {
    // Edges: 0→2, 1→2, 0→3, 2→3  (links interleaved [src,tgt,...]).
    const links = new Uint32Array([0, 2, 1, 2, 0, 3, 2, 3]);
    const inDegree = computeInDegree(links, 4, 4);
    expect(inDegree).toBeInstanceOf(Uint32Array);
    expect(inDegree.length).toBe(4);
    expect(Array.from(inDegree)).toEqual([0, 0, 2, 2]);
  });

  it('sums to exactly edgeCount', () => {
    const g = generateBA(2000, 6, 7);
    const inDegree = computeInDegree(g.links, g.nodeCount, g.edgeCount);
    expect(inDegree.length).toBe(g.nodeCount);
    let total = 0;
    for (let i = 0; i < inDegree.length; i++) total += inDegree[i]!;
    expect(total).toBe(g.edgeCount);
  });

  it('returns an all-zero array for a graph with no edges', () => {
    const inDegree = computeInDegree(new Uint32Array(0), 5, 0);
    expect(inDegree.length).toBe(5);
    expect(Array.from(inDegree)).toEqual([0, 0, 0, 0, 0]);
  });
});

import { describe, it, expect } from 'vitest';
import { generateBA } from '../src/graph/generator';

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

import { describe, it, expect } from 'vitest';
import { buildGraphBuffers } from '../src/transport/GraphTransport';
import { MAX_NODE_INDEX } from '../src/types';

/**
 * DATA-03: building the Float32 link view must assert nodeCount < MAX_NODE_INDEX
 * (2^24 = 16,777,216) and throw if exceeded — the Float32 24-bit-mantissa
 * integer-precision ceiling (01-RESEARCH.md § Pitfall 1). Without this guard,
 * node indices ≥ 2^24 would silently corrupt edges at high indices.
 */
describe('Float32 index-precision guard', () => {
  it('exports MAX_NODE_INDEX = 16_777_216 (2^24)', () => {
    expect(MAX_NODE_INDEX).toBe(16_777_216);
    expect(MAX_NODE_INDEX).toBe(2 ** 24);
  });

  it('allows a node count safely below the ceiling', () => {
    const nodeCount = 1000;
    const positions = new Float32Array(nodeCount * 2);
    const links = new Uint32Array(2); // one edge 0→0
    expect(() => buildGraphBuffers(positions, links, nodeCount, 1)).not.toThrow();
  });

  it('throws when nodeCount reaches the Float32 ceiling', () => {
    const positions = new Float32Array(0);
    const links = new Uint32Array(0);
    expect(() => buildGraphBuffers(positions, links, MAX_NODE_INDEX, 0)).toThrow();
  });

  it('throws when nodeCount exceeds the Float32 ceiling', () => {
    const positions = new Float32Array(0);
    const links = new Uint32Array(0);
    expect(() => buildGraphBuffers(positions, links, MAX_NODE_INDEX + 1, 0)).toThrow(
      /MAX_NODE_INDEX|precision|16777216|16_777_216/i,
    );
  });
});

import { describe, it, expect } from 'vitest';
import { generateBA } from '../src/graph/generator';
import { buildGraphBuffers } from '../src/transport/GraphTransport';
import type { GraphBuffers } from '../src/types';

/**
 * DATA-01: anything that satisfies GraphTransport resolves to a GraphBuffers
 * whose typed-array invariants hold and whose nodeCount/edgeCount agree with
 * the buffer lengths. We assert the buffer contract directly (the Worker-driven
 * SyntheticTransport.load() is exercised manually in the Task 5 browser gate —
 * Web Workers don't run under the node test environment).
 */
function assertGraphBuffersInvariants(buffers: GraphBuffers): void {
  expect(buffers.positions).toBeInstanceOf(Float32Array);
  expect(buffers.links).toBeInstanceOf(Float32Array);
  expect(buffers.positions.length).toBe(buffers.nodeCount * 2);
  expect(buffers.links.length).toBe(buffers.edgeCount * 2);
}

describe('GraphTransport buffer contract', () => {
  it('buildGraphBuffers turns generator output into a valid GraphBuffers', () => {
    const g = generateBA(1000, 6, 42);
    const buffers = buildGraphBuffers(g.positions, g.links, g.nodeCount, g.edgeCount);
    assertGraphBuffersInvariants(buffers);
    expect(buffers.nodeCount).toBe(1000);
    expect(buffers.edgeCount).toBe(g.edgeCount);
  });

  it('produces a Float32Array link view (cosmos.gl requirement)', () => {
    const g = generateBA(500, 6, 1);
    const buffers = buildGraphBuffers(g.positions, g.links, g.nodeCount, g.edgeCount);
    // Links must be Float32Array for cosmos.gl setLinks, and values preserved.
    expect(buffers.links).toBeInstanceOf(Float32Array);
    for (let i = 0; i < buffers.links.length; i++) {
      expect(buffers.links[i]).toBe(g.links[i]);
    }
  });
});

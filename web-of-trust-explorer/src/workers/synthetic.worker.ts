/// <reference lib="webworker" />
import { generateBA } from '../graph/generator';
import { MAX_NODE_INDEX } from '../types';

/**
 * Synthetic BA-generator worker. Generates a graph entirely off the main thread
 * and transfers the typed-array buffers back zero-copy (Transferables), so the
 * UI never blocks. The generator is parameterized by nodeCount, so this exact
 * worker scales to 5M in Plan 02; SyntheticTransport here drives it with ~1000.
 */

export interface SyntheticRequest {
  nodeCount: number;
  m?: number;
  seed?: number;
}

export interface SyntheticResult {
  type: 'result';
  /** Interleaved Float32 [x,y] positions. */
  positions: Float32Array;
  /** Interleaved Float32 [src,tgt] link indices (cosmos.gl format). */
  links: Float32Array;
  nodeCount: number;
  edgeCount: number;
}

const ctx = self as unknown as DedicatedWorkerGlobalScope;

ctx.onmessage = (ev: MessageEvent<SyntheticRequest>) => {
  const { nodeCount, m = 6, seed = 1 } = ev.data;

  const g = generateBA(nodeCount, m, seed);

  // Float32 precision guard (Pitfall 1): assert before producing the Float32
  // link view cosmos.gl requires.
  if (g.nodeCount >= MAX_NODE_INDEX) {
    throw new Error(
      `nodeCount ${g.nodeCount} reaches/exceeds MAX_NODE_INDEX ${MAX_NODE_INDEX}; Float32 links would corrupt.`,
    );
  }

  // Convert exact uint32 link indices to the Float32Array cosmos.gl wants.
  const links = new Float32Array(g.links.length);
  for (let i = 0; i < g.links.length; i++) links[i] = g.links[i]!;

  const result: SyntheticResult = {
    type: 'result',
    positions: g.positions,
    links,
    nodeCount: g.nodeCount,
    edgeCount: g.edgeCount,
  };

  // Transfer the underlying ArrayBuffers (zero-copy).
  ctx.postMessage(result, [g.positions.buffer, links.buffer]);
};

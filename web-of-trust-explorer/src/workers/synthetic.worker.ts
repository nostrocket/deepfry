/// <reference lib="webworker" />
import { generateBA, computeInDegree } from '../graph/generator';
import { MAX_NODE_INDEX } from '../types';

/**
 * Synthetic BA-generator worker. Generates a graph entirely off the main thread
 * and transfers the typed-array buffers back zero-copy (Transferables), so the
 * UI never blocks. The generator is parameterized by nodeCount, so this exact
 * worker scales from the ~1k walking skeleton (Plan 01) to the fixed
 * 5,000,000-node / ~30,000,000-edge GPU-ceiling target (Plan 02; D-01/D-02).
 *
 * Memory shape at 5M/30M (01-RESEARCH.md § Memory & Parse Budgeting):
 *   positions Float32  ~40 MB · target pool Uint32 ~240 MB ·
 *   links Float32      ~240 MB · in-degree Uint32 ~20 MB.
 * All pre-sized SoA typed arrays, no per-node heap objects, transferred zero-copy.
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
  /** In-degree (follower count) per node index, derived in one O(E) pass. length = nodeCount. */
  inDegree: Uint32Array;
  nodeCount: number;
  edgeCount: number;
}

const ctx = self as unknown as DedicatedWorkerGlobalScope;

ctx.onmessage = (ev: MessageEvent<SyntheticRequest>) => {
  const { nodeCount, m = 6, seed = 1 } = ev.data;

  const g = generateBA(nodeCount, m, seed);

  // Float32 precision guard (Pitfall 1): assert before producing the Float32
  // link view cosmos.gl requires. 5M < 2^24 (16.7M) is safe.
  if (g.nodeCount >= MAX_NODE_INDEX) {
    throw new Error(
      `nodeCount ${g.nodeCount} reaches/exceeds MAX_NODE_INDEX ${MAX_NODE_INDEX}; Float32 links would corrupt.`,
    );
  }

  // In-degree in one O(E) pass over the exact uint32 link buffer (D-08): no
  // followers query, no per-node objects. Done on the uint32 links before the
  // Float32 conversion so counting stays exact-integer.
  const inDegree = computeInDegree(g.links, g.nodeCount, g.edgeCount);

  // Convert exact uint32 link indices to the Float32Array cosmos.gl wants.
  const links = new Float32Array(g.links.length);
  for (let i = 0; i < g.links.length; i++) links[i] = g.links[i]!;

  const result: SyntheticResult = {
    type: 'result',
    positions: g.positions,
    links,
    inDegree,
    nodeCount: g.nodeCount,
    edgeCount: g.edgeCount,
  };

  // Transfer the underlying ArrayBuffers (zero-copy): positions, links, in-degree.
  ctx.postMessage(result, [g.positions.buffer, links.buffer, inDegree.buffer]);
};

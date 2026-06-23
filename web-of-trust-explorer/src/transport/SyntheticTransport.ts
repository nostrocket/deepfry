import type { GraphBuffers } from '../types';
import type { GraphTransport, LoadProgress } from './GraphTransport';
import type { SyntheticRequest, SyntheticResult } from '../workers/synthetic.worker';

/**
 * GraphTransport backed by the in-browser Barabási–Albert generator worker
 * (D-01: synthetic data proves the GPU side; produces no real JSON wire).
 *
 * Plan 01 drove it with a SMALL graph (~1,000 nodes) to prove the spine; Plan 02
 * scales it to the fixed 5,000,000-node / ~30,000,000-edge power-law target
 * (D-01/D-02) — the GPU-ceiling spike. The worker hands back Float32 positions +
 * Float32 links + a Uint32 in-degree array zero-copy, so this transport just
 * wraps them as GraphBuffers.
 */
export class SyntheticTransport implements GraphTransport {
  private readonly nodeCount: number;
  private readonly m: number;
  private readonly seed: number;

  /**
   * @param nodeCount default 5,000,000 — the fixed GPU-ceiling target (D-02). The
   *   worker/generator are parameterized, so tests/dev can pass a smaller N.
   * @param m out-edges per new node; m=6 → ~30M directed edges at 5M nodes (D-03).
   */
  constructor(nodeCount = 5_000_000, m = 6, seed = 1) {
    this.nodeCount = nodeCount;
    this.m = m;
    this.seed = seed;
  }

  load(onProgress: (p: LoadProgress) => void): Promise<GraphBuffers> {
    return new Promise<GraphBuffers>((resolve, reject) => {
      const worker = new Worker(new URL('../workers/synthetic.worker.ts', import.meta.url), {
        type: 'module',
      });

      onProgress({ stage: 'layout', edgesSoFar: 0 });

      worker.onmessage = (ev: MessageEvent<SyntheticResult>) => {
        const { positions, links, inDegree, nodeCount, edgeCount } = ev.data;
        onProgress({ stage: 'layout', edgesSoFar: edgeCount });
        worker.terminate();
        resolve({ positions, links, inDegree, nodeCount, edgeCount });
      };

      worker.onerror = (err) => {
        worker.terminate();
        reject(new Error(`SyntheticTransport worker failed: ${err.message}`));
      };

      const request: SyntheticRequest = {
        nodeCount: this.nodeCount,
        m: this.m,
        seed: this.seed,
      };
      worker.postMessage(request);
    });
  }
}

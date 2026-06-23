import type { GraphBuffers } from '../types';
import type { GraphTransport, LoadProgress } from './GraphTransport';
import type { SyntheticRequest, SyntheticResult } from '../workers/synthetic.worker';

/**
 * GraphTransport backed by the in-browser Barabási–Albert generator worker
 * (D-01: synthetic data proves the GPU side; produces no real JSON wire).
 *
 * Plan 01 drives it with a SMALL graph (~1,000 nodes) to prove the spine; the
 * same worker/generator scale to 5M in Plan 02. The worker hands back Float32
 * positions + Float32 links zero-copy, so this transport just wraps them as
 * GraphBuffers.
 */
export class SyntheticTransport implements GraphTransport {
  private readonly nodeCount: number;
  private readonly m: number;
  private readonly seed: number;

  constructor(nodeCount = 1000, m = 6, seed = 1) {
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
        const { positions, links, nodeCount, edgeCount } = ev.data;
        onProgress({ stage: 'layout', edgesSoFar: edgeCount });
        worker.terminate();
        resolve({ positions, links, nodeCount, edgeCount });
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

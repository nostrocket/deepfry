import type { GraphBuffers } from '../types';
import type { GraphTransport, LoadProgress } from './GraphTransport';
import { DEFAULT_DGRAPH_URL, DEFAULT_PAGE_SIZE } from './dgraphPaging';
import { mulberry32 } from '../graph/generator';
import type { DgraphRequest, DgraphResult, DgraphProgress } from '../workers/dgraph.worker';

/**
 * Real-wire GraphTransport — the JSON-wire feasibility verdict path (Plan 03,
 * DATA-01/DATA-03, D-08). Swappable one-for-one with SyntheticTransport behind
 * the GraphTransport interface, so main.ts selects between "prove the GPU"
 * (synthetic) and "prove the wire" (Dgraph) without touching the render side.
 *
 * It spawns src/workers/dgraph.worker.ts, which pages the entire `follows` graph
 * from the dev Dgraph via read-only DQL after-cursor paging (never a mutation,
 * never an inverse-edge query), parses chunked with the drop-page-string memory
 * discipline, remaps hex → dense uint32, derives in-degree O(E), and transfers
 * the SoA buffers back zero-copy. This transport forwards `onProgress` to the
 * loader UI, seeds initial node positions (cosmos.gl then runs the live force
 * layout), and resolves the GraphBuffers — including `hexByIndex` for the
 * hover tooltip (D-14) and `encodingNs` for the verdict readout (D-10).
 */

const SPACE_SIZE = 8192;

export class DgraphTransport implements GraphTransport {
  private readonly baseUrl: string;
  private readonly pageSize: number;
  private readonly seed: number;

  /**
   * Captured from the most recent successful load so the verdict readout can
   * report the accumulated server-side encode cost (D-10). 0 until a load runs.
   */
  public lastEncodingNs = 0;

  /**
   * @param baseUrl  Dgraph HTTP base; default http://localhost:8080 (PATTERNS § :8080).
   * @param pageSize outer-node page size; default 50000 (halve if heap spikes, D-08).
   * @param seed     PRNG seed for the initial random positions (reproducible layout start).
   */
  constructor(baseUrl = DEFAULT_DGRAPH_URL, pageSize = DEFAULT_PAGE_SIZE, seed = 1) {
    this.baseUrl = baseUrl;
    this.pageSize = pageSize;
    this.seed = seed;
  }

  load(onProgress: (p: LoadProgress) => void): Promise<GraphBuffers> {
    return new Promise<GraphBuffers>((resolve, reject) => {
      const worker = new Worker(new URL('../workers/dgraph.worker.ts', import.meta.url), {
        type: 'module',
      });

      worker.onmessage = (ev: MessageEvent<DgraphResult | DgraphProgress>) => {
        const data = ev.data;
        if (data.type === 'progress') {
          onProgress(data.progress);
          return;
        }
        // type === 'result'
        const { links, inDegree, hexByIndex, nodeCount, edgeCount, encodingNs } = data;
        this.lastEncodingNs = encodingNs;

        // Seed random initial positions; cosmos.gl rescales and runs the live
        // force layout from here (the wire carries no coordinates).
        const positions = this.seedPositions(nodeCount);

        onProgress({ stage: 'layout', edgesSoFar: edgeCount });
        worker.terminate();
        resolve({ positions, links, inDegree, nodeCount, edgeCount, hexByIndex });
      };

      worker.onerror = (err) => {
        worker.terminate();
        reject(new Error(`DgraphTransport worker failed: ${err.message}`));
      };

      const request: DgraphRequest = { baseUrl: this.baseUrl, pageSize: this.pageSize };
      worker.postMessage(request);
    });
  }

  /** Random [x,y] seed positions across the layout space (no per-node objects). */
  private seedPositions(nodeCount: number): Float32Array {
    const rand = mulberry32(this.seed);
    const positions = new Float32Array(nodeCount * 2);
    for (let i = 0; i < nodeCount; i++) {
      positions[i * 2] = rand() * SPACE_SIZE;
      positions[i * 2 + 1] = rand() * SPACE_SIZE;
    }
    return positions;
  }
}

/// <reference lib="webworker" />
import {
  loadPagedGraph,
  buildDqlPageQuery,
  DEFAULT_PAGE_SIZE,
  DEFAULT_DGRAPH_URL,
  type PageFetcher,
} from '../transport/dgraphPaging';
import type { DqlEnvelope, LoadProgress } from '../transport/GraphTransport';
import { MAX_NODE_INDEX } from '../types';

/**
 * Real-wire Dgraph loader worker — the JSON-wire feasibility spike (Plan 03,
 * D-08). Pages the entire `follows` graph from the dev Dgraph via read-only DQL
 * after-cursor paging, parses each page chunked, remaps hex uid → dense uint32,
 * derives in-degree client-side, and transfers the SoA buffers back zero-copy.
 *
 * Memory discipline (01-RESEARCH.md § Memory & Parse Budgeting; T-01-06):
 *   - one page in flight at a time; the response text + parsed page object are
 *     dropped before the next fetch (no whole-graph string co-resident)
 *   - edges accumulate as compact uint32 pairs (4 bytes vs ~64-byte hex strings)
 *   - the Float32 link view cosmos.gl wants is built once at hand-off
 *   - buffers cross to the main thread as Transferables (no second copy)
 *
 * Read-only invariant (DeepFry data-separation rule; T-01-07): the worker only
 * ever issues `application/dql` read queries built by `buildDqlPageQuery`
 * (`has(follows)` + `follows { uid }`). It NEVER constructs a write/admin request,
 * never builds a mutation body, and never queries the inverse follower edge —
 * in-degree is derived client-side via computeInDegree.
 */

export interface DgraphRequest {
  /** Dgraph HTTP base, default http://localhost:8080 (PATTERNS § :8080). */
  baseUrl?: string;
  /** Outer-node page size, default 50000 (halve if heap spikes, D-08). */
  pageSize?: number;
}

export interface DgraphResult {
  type: 'result';
  /** Interleaved Float32 [src,tgt] link indices (cosmos.gl format). */
  links: Float32Array;
  /** In-degree (follower count) per node index, derived in one O(E) pass. */
  inDegree: Uint32Array;
  /** uint32 index → hex pubkey, for the hover tooltip (D-14). */
  hexByIndex: string[];
  nodeCount: number;
  edgeCount: number;
  /** Accumulated server-side encode cost across all pages (verdict metric, D-10). */
  encodingNs: number;
}

export interface DgraphProgress {
  type: 'progress';
  progress: LoadProgress;
}

const ctx = self as unknown as DedicatedWorkerGlobalScope;

ctx.onmessage = (ev: MessageEvent<DgraphRequest>) => {
  const baseUrl = ev.data.baseUrl ?? DEFAULT_DGRAPH_URL;
  const pageSize = ev.data.pageSize ?? DEFAULT_PAGE_SIZE;

  /**
   * Fetch + parse exactly one page. The browser sets `Accept-Encoding: gzip`
   * automatically; we POST the read DQL with `Content-Type: application/dql` to
   * `/query`. `text` and the parsed `envelope` go out of scope when this resolves,
   * so the page string is dropped before the next fetch (Pitfall 2).
   */
  const fetchPage: PageFetcher = async (cursor: string): Promise<DqlEnvelope> => {
    const query = buildDqlPageQuery(cursor, pageSize);
    const res = await fetch(`${baseUrl}/query`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/dql' },
      body: query,
    });
    if (!res.ok) {
      throw new Error(`Dgraph /query failed: ${res.status} ${res.statusText}`);
    }
    const text = await res.text();
    return JSON.parse(text) as DqlEnvelope;
  };

  loadPagedGraph(fetchPage, {
    pageSize,
    onProgress: (progress) => {
      const msg: DgraphProgress = { type: 'progress', progress };
      ctx.postMessage(msg);
    },
  })
    .then((paged) => {
      // Float32 precision guard (Pitfall 1): assert before producing the Float32
      // link view. Real dev DB << 2^24 (16.7M) is safe; loud failure if not.
      if (paged.nodeCount >= MAX_NODE_INDEX) {
        throw new Error(
          `nodeCount ${paged.nodeCount} reaches/exceeds MAX_NODE_INDEX ${MAX_NODE_INDEX}; ` +
            `Float32 links would corrupt. Move to a uint32/binary link path before this scale.`,
        );
      }

      // Convert exact uint32 link indices to the Float32Array cosmos.gl wants.
      const links = new Float32Array(paged.links.length);
      for (let i = 0; i < paged.links.length; i++) links[i] = paged.links[i]!;

      const result: DgraphResult = {
        type: 'result',
        links,
        inDegree: paged.inDegree,
        hexByIndex: paged.hexByIndex,
        nodeCount: paged.nodeCount,
        edgeCount: paged.edgeCount,
        encodingNs: paged.encodingNs,
      };
      // Transfer the underlying ArrayBuffers (zero-copy): links + in-degree.
      ctx.postMessage(result, [links.buffer, paged.inDegree.buffer]);
    })
    .catch((err: unknown) => {
      // Surface to DgraphTransport via the worker error channel.
      throw err instanceof Error ? err : new Error(String(err));
    });
};

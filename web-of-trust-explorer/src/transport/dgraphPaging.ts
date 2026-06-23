import {
  createHexRemap,
  parseDqlEnvelope,
  extractEncodingNs,
  type DqlEnvelope,
  type LoadProgress,
} from './GraphTransport';
import { computeInDegree } from '../graph/generator';

/**
 * The read-only DQL after-cursor paging core that backs DgraphTransport.
 *
 * Split out from the Worker so the cursor/termination/remap logic is unit-tested
 * in the node test env (Web Workers and real `fetch` don't run there); the Worker
 * (src/workers/dgraph.worker.ts) injects a real fetch+JSON.parse `fetchPage` and
 * applies the drop-page-string memory discipline (D-08). Both paths share this
 * one loop so the tested behavior is the shipped behavior.
 *
 * Read-only invariant (DeepFry data-separation rule; PATTERNS § Read-Only): the
 * only query this module ever builds is `has(follows)` + `follows { uid }`. It
 * NEVER constructs a mutation and NEVER queries the inverse follower edge —
 * in-degree is derived client-side via computeInDegree (01-RESEARCH.md § ID-only dump).
 */

/** Default outer-node page size (01-RESEARCH.md § Bulk-Load — tune in the spike, halve if heap spikes). */
export const DEFAULT_PAGE_SIZE = 50000;

/** Default dev Dgraph HTTP endpoint (PATTERNS § :8080 convention; hardcode/env is fine for v1). */
export const DEFAULT_DGRAPH_URL = 'http://localhost:8080';

/**
 * Build one read-only DQL page query. `cursor` is `0x0` for the first page, then
 * the last uid returned by the prior page; `first` pages the stable uid-ordered
 * outer node set (01-RESEARCH.md § after-cursor semantics).
 */
export function buildDqlPageQuery(cursor: string, pageSize: number): string {
  return `{ q(func: has(follows), first: ${pageSize}, after: ${cursor}) { uid follows { uid } } }`;
}

/** Fetch+parse one page body into the DQL envelope. Injected by the Worker (real fetch) or tests (stub). */
export type PageFetcher = (cursor: string) => Promise<DqlEnvelope>;

export interface PagedGraphResult {
  /** Exact uint32 edge pairs [src0,tgt0,…]. length = edgeCount*2. */
  links: Uint32Array;
  /** In-degree per dense node index, derived in one O(E) pass (no inverse-edge query). */
  inDegree: Uint32Array;
  /** uint32 index → hex pubkey, for the hover tooltip (D-14). */
  hexByIndex: string[];
  nodeCount: number;
  edgeCount: number;
  /** Accumulated server-side encode cost across all pages (verdict metric, D-10). */
  encodingNs: number;
}

export interface PagedGraphOptions {
  pageSize?: number;
  onProgress?: (p: LoadProgress) => void;
}

/**
 * Run the after-cursor paging loop to completion and assemble the SoA buffers.
 *
 * Loop: cursor `0x0` → fetch page → emit `fetch` progress → parse the page into
 * dense [src,tgt] pairs via the shared remap → emit `parse` progress → advance
 * the cursor to the last uid of the page → stop on an empty or short page (a page
 * with fewer than `pageSize` outer nodes is terminal). Edge pairs accumulate in a
 * chunk list and are concatenated once at the end (avoids repeated geometric
 * reallocation); the Worker drops each page string before the next fetch.
 */
export async function loadPagedGraph(
  fetchPage: PageFetcher,
  opts: PagedGraphOptions = {},
): Promise<PagedGraphResult> {
  const pageSize = opts.pageSize ?? DEFAULT_PAGE_SIZE;
  const onProgress = opts.onProgress;
  const remap = createHexRemap();

  const chunks: Array<[number, number]>[] = [];
  let edgesSoFar = 0;
  let encodingNs = 0;
  let cursor = '0x0';

  for (;;) {
    onProgress?.({ stage: 'fetch', edgesSoFar });
    const envelope = await fetchPage(cursor);
    const nodes = envelope.data?.q ?? [];

    encodingNs += extractEncodingNs(envelope);

    const pageEdges = parseDqlEnvelope(envelope, remap);
    if (pageEdges.length > 0) {
      chunks.push(pageEdges);
      edgesSoFar += pageEdges.length;
    }
    onProgress?.({ stage: 'parse', edgesSoFar });

    // Terminal: empty page, or a short page (fewer outer nodes than requested).
    if (nodes.length === 0 || nodes.length < pageSize) break;

    // Advance the cursor to the last uid of this page (O(1) skip next page).
    cursor = nodes[nodes.length - 1]!.uid;
  }

  const edgeCount = edgesSoFar;
  const nodeCount = remap.size;
  const links = new Uint32Array(edgeCount * 2);
  let w = 0;
  for (const chunk of chunks) {
    for (const [src, tgt] of chunk) {
      links[w++] = src;
      links[w++] = tgt;
    }
  }

  // In-degree derived client-side in one O(E) pass — no inverse-edge query.
  const inDegree = computeInDegree(links, nodeCount, edgeCount);
  const hexByIndex = remap.toHexArray();

  return { links, inDegree, hexByIndex, nodeCount, edgeCount, encodingNs };
}

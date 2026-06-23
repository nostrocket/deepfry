import { MAX_NODE_INDEX, type GraphBuffers } from '../types';

export type { GraphBuffers } from '../types';

/**
 * Staged load progress for the loader UI (D-09). `edgesSoFar` ticks the live
 * counter; there is deliberately no percentage (cursor paging has no honest
 * upfront total).
 *
 * Stages:
 *  - `'fetch'`  — request issued, waiting on / receiving the response start.
 *  - `'parse'`  — legacy DgraphTransport JSON-wire parse stage (kept for that path).
 *  - `'receive'`— GoBridgeTransport binary bytes flowing in over the stream
 *                 (no parse; the load-bearing PERF-01 win, D-08).
 *  - `'layout'` — buffers built, handed to cosmos.gl for the force layout.
 */
export interface LoadProgress {
  stage: 'fetch' | 'parse' | 'receive' | 'layout';
  edgesSoFar: number;
  /**
   * Raw bytes received off the stream so far (GoBridgeTransport `'receive'`
   * stage only). Lets the loader show a bytes counter + a MB/s rate for the
   * binary path; absent on the JSON-wire (`'parse'`) path which counts edges,
   * not bytes (D-09).
   */
  bytesSoFar?: number;
}

/**
 * The single swappable data path. Both the synthetic generator (GPU-ceiling
 * spike) and the real Dgraph loader (JSON-wire verdict, Plan 03) implement this,
 * so JSON-direct → Go-binary-stream is a one-file swap later (PERF-01).
 * 01-RESEARCH.md § Pattern 1.
 */
export interface GraphTransport {
  load(onProgress: (p: LoadProgress) => void): Promise<GraphBuffers>;
}

/**
 * Dense, stable, collision-free hex→uint32 remap.
 *
 * First sighting of a hex uid assigns the next dense index (0,1,2,…); repeat
 * sightings return the same index. Edges are stored as compact uint32 pairs
 * rather than ~64-byte hex strings (01-RESEARCH.md § Memory & Parse Budgeting
 * point 2; DATA-03). Plan 03's dgraph.worker imports this for the real wire.
 */
export interface HexRemap {
  /** Returns the dense index for a hex, assigning a new one on first sighting. */
  indexOf(hex: string): number;
  /** Number of distinct hexes seen so far. */
  readonly size: number;
  /** uint32 index → hex, for the hover tooltip (D-14). */
  toHexArray(): string[];
}

export function createHexRemap(): HexRemap {
  const map = new Map<string, number>();
  return {
    indexOf(hex: string): number {
      let idx = map.get(hex);
      if (idx === undefined) {
        idx = map.size;
        map.set(hex, idx);
      }
      return idx;
    },
    get size(): number {
      return map.size;
    },
    toHexArray(): string[] {
      const out = new Array<string>(map.size);
      for (const [hex, idx] of map) out[idx] = hex;
      return out;
    },
  };
}

/** Minimal shape of a Dgraph DQL response node we care about. */
interface DqlNode {
  uid: string;
  follows?: Array<{ uid: string }>;
}

/** The `{ data: { q: [...] }, extensions: {...} }` DQL response envelope. */
export interface DqlEnvelope {
  data?: { q?: DqlNode[] };
  extensions?: { server_latency?: { encoding_ns?: number } };
}

/**
 * Parse one DQL `after`-cursor page into [srcIdx, tgtIdx] edge pairs via the
 * shared remap. Nodes with no `follows` are leaves and emit no edges — they are
 * still discovered as edge *targets* (01-RESEARCH.md § response shape; DATA-01).
 */
export function parseDqlEnvelope(envelope: DqlEnvelope, remap: HexRemap): Array<[number, number]> {
  const nodes = envelope.data?.q ?? [];
  const edges: Array<[number, number]> = [];
  for (const node of nodes) {
    const src = remap.indexOf(node.uid);
    const follows = node.follows;
    if (!follows) continue;
    for (const f of follows) {
      const tgt = remap.indexOf(f.uid);
      edges.push([src, tgt]);
    }
  }
  return edges;
}

/**
 * Extract the server-side encode cost (`extensions.server_latency.encoding_ns`)
 * from a DQL response page, defaulting to 0 when absent. Dgraph can take multiple
 * seconds to encode a huge result; this is a first-class verdict metric (D-10),
 * accumulated across pages (01-RESEARCH.md § response shape).
 */
export function extractEncodingNs(envelope: DqlEnvelope): number {
  return envelope.extensions?.server_latency?.encoding_ns ?? 0;
}

/**
 * Build the GraphBuffers handed to cosmos.gl, enforcing the Float32 integer
 * ceiling. cosmos.gl requires the link buffer as a Float32Array; Float32 only
 * represents integers exactly below 2^24, so we assert
 * `nodeCount < MAX_NODE_INDEX` and throw before producing a corrupt view
 * (01-RESEARCH.md § Pitfall 1; DATA-03 precision guard).
 *
 * @param positions interleaved Float32 [x,y] positions (length nodeCount*2)
 * @param links     exact Uint32 edge pairs [src,tgt] (length edgeCount*2)
 */
export function buildGraphBuffers(
  positions: Float32Array,
  links: Uint32Array,
  nodeCount: number,
  edgeCount: number,
  hexByIndex?: string[],
): GraphBuffers {
  if (nodeCount >= MAX_NODE_INDEX) {
    throw new Error(
      `nodeCount ${nodeCount} reaches/exceeds MAX_NODE_INDEX ${MAX_NODE_INDEX} (2^24): ` +
        `Float32 link indices would lose precision. Move to a uint32/binary link path before this scale.`,
    );
  }
  // Float32 view of the exact uint32 link indices (safe below 2^24).
  const float32Links = new Float32Array(links.length);
  for (let i = 0; i < links.length; i++) float32Links[i] = links[i]!;
  return {
    positions,
    links: float32Links,
    nodeCount,
    edgeCount,
    ...(hexByIndex ? { hexByIndex } : {}),
  };
}

/**
 * Structure-of-arrays (SoA) typed-buffer shapes for the whole-graph data spine.
 *
 * The entire graph is held as flat typed arrays — NEVER per-node heap objects
 * (`{id,x,y}[]`), which would blow the 18 GB unified-memory budget at 5M nodes
 * (01-RESEARCH.md § Memory & Parse Budgeting, Anti-Patterns). cosmos.gl 3.0.0's
 * API consumes exactly these SoA buffers via setPointPositions / setLinks.
 */

/**
 * Float32 24-bit-mantissa integer-precision ceiling.
 *
 * cosmos.gl requires the link buffer as a `Float32Array`. Float32 represents
 * integers exactly only up to 2^24 = 16,777,216. Node indices at or above this
 * value lose precision and would connect edges to the WRONG node.
 *
 * At the 5M-node target this is safe (5M < 16.7M), but we assert
 * `nodeCount < MAX_NODE_INDEX` before ever building a Float32 link view so the
 * failure is loud rather than silent corruption (01-RESEARCH.md § Pitfall 1).
 */
export const MAX_NODE_INDEX = 16_777_216;

/**
 * The graph as structure-of-arrays typed buffers.
 *
 * Positions and links are interleaved pairs so they can be transferred
 * zero-copy across the worker boundary and handed straight to cosmos.gl.
 */
export interface GraphBuffers {
  /** Node positions, interleaved [x0,y0, x1,y1, …]. length = nodeCount * 2. */
  positions: Float32Array;
  /** Directed edges, interleaved [src0,tgt0, src1,tgt1, …]. length = edgeCount * 2. cosmos.gl wants Float32Array. */
  links: Float32Array;
  /** Number of nodes. */
  nodeCount: number;
  /** Number of directed edges. */
  edgeCount: number;
  /** In-degree (follower count) per node index, derived in one O(E) pass over `links`. length = nodeCount. */
  inDegree?: Uint32Array;
  /**
   * Out-degree (follows count) per node index. length = nodeCount.
   *
   * Provenance: the Go bridge computes this server-side in one O(E) pass and
   * ships it in the binary frame (D-04). Optional/additive so SyntheticTransport
   * and DgraphTransport — which do not provide it — keep compiling.
   */
  outDegree?: Uint32Array;
  /**
   * Louvain community id per node index, for cluster coloring (OVER-02).
   * length = nodeCount.
   *
   * Provenance: computed server-side by the bridge's array Louvain pass and
   * shipped in the frame (D-04). Optional/additive — other transports omit it.
   */
  community?: Uint32Array;
  /**
   * Per-node `created_at` of the kind-3 follow list, unix seconds as i32
   * (Assumption A3; fits to 2038 for a dev tool). length = nodeCount.
   *
   * Provenance: bridge-prepared from Dgraph and shipped in the frame (D-04).
   * Optional/additive — other transports omit it.
   */
  kind3CreatedAt?: Int32Array;
  /**
   * Per-node `last_db_update` timestamp, unix seconds as i32. length = nodeCount.
   *
   * Provenance: bridge-prepared from Dgraph and shipped in the frame (D-04).
   * Optional/additive — other transports omit it.
   */
  lastDbUpdate?: Int32Array;
  /** Optional uint32-index → hex-pubkey map for the hover tooltip (D-14). Omitted for synthetic data. */
  hexByIndex?: string[];
}

import type { GraphBuffers } from '../types';
import { buildGraphBuffers, type GraphTransport, type LoadProgress } from './GraphTransport';
import { decodeFrame, HEADER_BYTES, PUBKEY_BYTES } from './wireFormat';
import { mulberry32 } from '../graph/generator';

/**
 * The binary-stream GraphTransport — the PERF-01 drop-in swap.
 *
 * It fetches the Go bridge's `/graph.bin` frame (same-origin via the Vite
 * `server.proxy`, Plan 03 task 3), reads it incrementally with
 * `resp.body.getReader()`, accumulates the raw bytes, and decodes them into SoA
 * typed arrays with ZERO single-shot JSON text decoding (D-08, RESEARCH
 * Pattern 5) — the memory-doubling text-decode that FAILED the Phase 1 verdict
 * is gone entirely. cosmos.gl + the SoA render/layout pipeline behind the
 * `GraphTransport` seam are untouched.
 *
 * Decode strategy: accumulate-then-view. We collect chunks, copy them once into
 * a single offset-0 `ArrayBuffer`, then `decodeFrame` exposes typed-array VIEWS
 * over it (no further copies; every u32 section is 4-byte aligned by frame
 * construction — Pitfall 4). One transient copy, never a parse-doubling. A
 * streaming-into-preallocated-buffer optimization is deferred to the Plan 03
 * verdict, which measures peak heap and decides whether it is warranted; the
 * RESEARCH guidance is "start with accumulate-then-view, optimize only if peak
 * heap is a problem."
 *
 * The wire carries packed 32-byte binary pubkeys (half the size of hex on the
 * wire); the browser hex-encodes them once into `hexByIndex` for the hover
 * tooltip (D-04/D-14). The wire carries NO coordinates, so we seed random
 * initial positions exactly like DgraphTransport and let cosmos.gl run the live
 * force layout from there.
 */

const SPACE_SIZE = 8192;
const DEFAULT_GRAPH_BIN_URL = '/graph.bin';

/** Lower-case hex lookup table, byte → 2 hex chars (avoids per-byte toString). */
const HEX = Array.from({ length: 256 }, (_, b) => b.toString(16).padStart(2, '0'));

export class GoBridgeTransport implements GraphTransport {
  private readonly url: string;
  private readonly seed: number;

  /**
   * @param url  same-origin path to the bridge frame; default `/graph.bin`
   *             (proxied to the bridge by Vite, dodging COEP/CORP).
   * @param seed PRNG seed for reproducible initial positions.
   */
  constructor(url = DEFAULT_GRAPH_BIN_URL, seed = 1) {
    this.url = url;
    this.seed = seed;
  }

  async load(onProgress: (p: LoadProgress) => void): Promise<GraphBuffers> {
    onProgress({ stage: 'fetch', edgesSoFar: 0 });

    const resp = await fetch(this.url);
    const body = resp.body;
    if (!body) {
      throw new Error(`GoBridgeTransport: response for ${this.url} has no readable body`);
    }

    // --- Receive: accumulate raw bytes off the stream. No text decode. ---
    const reader = body.getReader();
    const chunks: Uint8Array[] = [];
    let received = 0;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value) continue;
      chunks.push(value);
      received += value.byteLength;
      // edgesSoFar is a cosmetic byte-derived estimate; the header gives the
      // exact count once decoded (Assumption A5). Each directed edge is 2×u32 = 8B.
      const edgesEstimate =
        received > HEADER_BYTES ? Math.floor((received - HEADER_BYTES) / 8) : 0;
      // bytesSoFar drives the loader's bytes counter + MB/s rate for the binary
      // path; the exact byte total is unknown upfront (chunked, no Content-Length),
      // so the loader derives a rate rather than a percentage (D-09).
      onProgress({ stage: 'receive', edgesSoFar: edgesEstimate, bytesSoFar: received });
    }

    // --- Single transient copy into one offset-0 buffer (guarantees 4-byte
    // alignment of every u32 view; concat of stream chunks may not be aligned). ---
    const frame = new Uint8Array(received);
    let off = 0;
    for (const c of chunks) {
      frame.set(c, off);
      off += c.byteLength;
    }

    // --- Decode: typed-array views over the frame. MAGIC/VERSION guard throws
    // before any view is built on a malformed body (T-01.1-04). ---
    const decoded = decodeFrame(frame.buffer);

    // Convert the exact uint32 links view to the cosmos.gl Float32 link buffer,
    // keeping the MAX_NODE_INDEX 2^24 precision assertion (DATA-03).
    const seeded = this.seedPositions(decoded.nodeCount);
    const base = buildGraphBuffers(
      seeded,
      decoded.links,
      decoded.nodeCount,
      decoded.edgeCount,
      this.hexEncode(decoded.pubkeys, decoded.nodeCount),
    );

    onProgress({ stage: 'layout', edgesSoFar: decoded.edgeCount });

    return {
      ...base,
      inDegree: decoded.inDegree,
      outDegree: decoded.outDegree,
      community: decoded.community,
      kind3CreatedAt: decoded.kind3CreatedAt,
      lastDbUpdate: decoded.lastDbUpdate,
    };
  }

  /** Hex-encode the packed 32-byte pubkeys into one string per node (D-04/D-14). */
  private hexEncode(pubkeys: Uint8Array, nodeCount: number): string[] {
    const out = new Array<string>(nodeCount);
    for (let i = 0; i < nodeCount; i++) {
      const start = i * PUBKEY_BYTES;
      let s = '';
      for (let j = 0; j < PUBKEY_BYTES; j++) s += HEX[pubkeys[start + j]!];
      out[i] = s;
    }
    return out;
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

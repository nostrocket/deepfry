/**
 * The browser-side mirror of the Go bridge's binary frame layout
 * (`bridge/internal/wire/encode.go`). This module is the SINGLE source of
 * frame-layout truth on the TS side — `GoBridgeTransport` imports the constants
 * and `decodeFrame` here rather than re-deriving offsets, so the encoder and
 * decoder cannot drift (D-08).
 *
 * The frame is self-describing and entirely LITTLE-ENDIAN (host order on the
 * target x86/ARM machines), so a browser `new Uint32Array(buffer, offset, n)`
 * view is zero-cost — no per-element byte swap, and crucially NO `JSON.parse`
 * (the load-bearing PERF-01 win — the entire point of the phase).
 *
 * Frame layout (mirrors encode.go exactly):
 *
 *   [ MAGIC u32 "WOTB" ][ VERSION u32 ][ nodeCount u32 ][ edgeCount u32 ]   # 16-byte header
 *   [ edges:          u32 × edgeCount*2  ]   # src,tgt dense-index pairs
 *   [ inDeg:          u32 × nodeCount    ]
 *   [ outDeg:         u32 × nodeCount    ]
 *   [ community:      u32 × nodeCount    ]
 *   [ kind3CreatedAt: i32 × nodeCount    ]
 *   [ lastDbUpdate:   i32 × nodeCount    ]
 *   [ pubkeyBytes:    32 × nodeCount     ]   # packed 32-byte binary, LAST so all
 *                                            # u32 sections stay 4-byte aligned (Pitfall 4)
 */

/**
 * Frame magic: ASCII "WOTB" read as a little-endian u32. Mirrors Go
 * `wire.MagicWOTB = 0x42544F57` ('W'=0x57,'O'=0x4F,'T'=0x54,'B'=0x42 LE).
 * `decodeFrame` rejects any body whose first u32 != this (truncated/incompatible
 * frame guard, threat T-01.1-04).
 */
export const MAGIC = 0x42544f57;

/** Frame format version; bump in lockstep with Go `wire.Version` on any layout change. */
export const VERSION = 1;

/** Fixed header size: MAGIC + VERSION + nodeCount + edgeCount (4 × u32). Mirrors Go `wire.HeaderBytes`. */
export const HEADER_BYTES = 16;

/** Packed pubkey width in bytes (32-byte binary, hex-encoded lazily for the tooltip). */
export const PUBKEY_BYTES = 32;

/** Bytes per 32-bit element (u32 / i32). */
const U32_BYTES = 4;

/** Decoded views over a complete frame buffer. All typed arrays are VIEWS (no copies). */
export interface DecodedFrame {
  /** Number of nodes from the header. */
  nodeCount: number;
  /** Number of directed edges from the header. */
  edgeCount: number;
  /** Edge pairs [src0,tgt0,…] as exact uint32 indices. length = edgeCount*2. */
  links: Uint32Array;
  /** In-degree (follower count) per node. length = nodeCount. */
  inDegree: Uint32Array;
  /** Out-degree (follows count) per node. length = nodeCount. */
  outDegree: Uint32Array;
  /** Louvain community id per node. length = nodeCount. */
  community: Uint32Array;
  /** kind-3 created_at unix seconds (i32) per node. length = nodeCount. */
  kind3CreatedAt: Int32Array;
  /** last_db_update unix seconds (i32) per node. length = nodeCount. */
  lastDbUpdate: Int32Array;
  /** Packed 32-byte pubkeys, nodeCount × 32 bytes, in dense-index order. */
  pubkeys: Uint8Array;
}

/**
 * Decode a complete frame buffer into typed-array VIEWS with ZERO `JSON.parse`.
 *
 * The header is read with a `DataView` (which tolerates any alignment); the u32
 * / i32 sections are exposed as `Uint32Array` / `Int32Array` views over the same
 * buffer. Every such view's `byteOffset` is a multiple of 4 by construction —
 * the header is 16 bytes and all 32-bit sections precede the 32-byte pubkey
 * table, so each section starts on a 4-byte boundary (Pitfall 4). The caller is
 * responsible for handing in a buffer whose backing store starts at offset 0
 * (e.g. a freshly-allocated `Uint8Array`), so view offsets are absolute.
 *
 * @param buffer the full frame as an ArrayBuffer (offset 0).
 * @throws RangeError on MAGIC/VERSION mismatch or a buffer too short for the header.
 * @throws RangeError if the declared counts exceed the buffer length (truncated frame).
 */
export function decodeFrame(buffer: ArrayBuffer): DecodedFrame {
  if (buffer.byteLength < HEADER_BYTES) {
    throw new RangeError(
      `frame too short: ${buffer.byteLength} bytes < ${HEADER_BYTES}-byte header`,
    );
  }

  const header = new DataView(buffer, 0, HEADER_BYTES);
  const magic = header.getUint32(0, true);
  if (magic !== MAGIC) {
    throw new RangeError(
      `bad frame MAGIC 0x${magic.toString(16)} (expected 0x${MAGIC.toString(16)} "WOTB")`,
    );
  }
  const version = header.getUint32(4, true);
  if (version !== VERSION) {
    throw new RangeError(`unsupported frame VERSION ${version} (expected ${VERSION})`);
  }
  const nodeCount = header.getUint32(8, true);
  const edgeCount = header.getUint32(12, true);

  // Walk the section offsets in wire order; each u32/i32 section is 4-byte
  // aligned because every preceding section's byte length is a multiple of 4.
  let offset = HEADER_BYTES;

  const links = new Uint32Array(buffer, offset, edgeCount * 2);
  offset += edgeCount * 2 * U32_BYTES;

  const inDegree = new Uint32Array(buffer, offset, nodeCount);
  offset += nodeCount * U32_BYTES;

  const outDegree = new Uint32Array(buffer, offset, nodeCount);
  offset += nodeCount * U32_BYTES;

  const community = new Uint32Array(buffer, offset, nodeCount);
  offset += nodeCount * U32_BYTES;

  const kind3CreatedAt = new Int32Array(buffer, offset, nodeCount);
  offset += nodeCount * U32_BYTES;

  const lastDbUpdate = new Int32Array(buffer, offset, nodeCount);
  offset += nodeCount * U32_BYTES;

  const pubkeyBytes = nodeCount * PUBKEY_BYTES;
  if (offset + pubkeyBytes > buffer.byteLength) {
    throw new RangeError(
      `truncated frame: need ${offset + pubkeyBytes} bytes for declared counts ` +
        `(nodeCount=${nodeCount}, edgeCount=${edgeCount}) but buffer is ${buffer.byteLength}`,
    );
  }
  const pubkeys = new Uint8Array(buffer, offset, pubkeyBytes);

  return {
    nodeCount,
    edgeCount,
    links,
    inDegree,
    outDegree,
    community,
    kind3CreatedAt,
    lastDbUpdate,
    pubkeys,
  };
}

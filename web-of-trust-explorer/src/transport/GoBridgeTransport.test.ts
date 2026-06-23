import { describe, it, expect, vi, afterEach } from 'vitest';
import { GoBridgeTransport } from './GoBridgeTransport';
import { MAGIC, VERSION, HEADER_BYTES, PUBKEY_BYTES } from './wireFormat';
import type { LoadProgress } from './GraphTransport';

/**
 * Build a synthetic binary frame matching the Go encoder layout
 * (bridge/internal/wire/encode.go), all little-endian:
 *   [MAGIC][VERSION][nodeCount][edgeCount] header (16 bytes)
 *   edges u32×edgeCount*2, inDeg/outDeg/community u32×nodeCount,
 *   kind3CreatedAt/lastDbUpdate i32×nodeCount, pubkeys 32×nodeCount.
 */
function buildFrame(opts: {
  magic?: number;
  version?: number;
  links: number[]; // flat [src,tgt,...]
  inDeg: number[];
  outDeg: number[];
  community: number[];
  kind3: number[];
  lastDb: number[];
  pubkeys: Uint8Array[]; // each 32 bytes
}): ArrayBuffer {
  const nodeCount = opts.inDeg.length;
  const edgeCount = opts.links.length / 2;
  const u32Sections = opts.links.length + nodeCount * 5; // edges + inDeg+outDeg+community + kind3+lastDb
  const totalBytes = HEADER_BYTES + u32Sections * 4 + nodeCount * PUBKEY_BYTES;

  const buf = new ArrayBuffer(totalBytes);
  const dv = new DataView(buf);
  dv.setUint32(0, opts.magic ?? MAGIC, true);
  dv.setUint32(4, opts.version ?? VERSION, true);
  dv.setUint32(8, nodeCount, true);
  dv.setUint32(12, edgeCount, true);

  let off = HEADER_BYTES;
  const putU32 = (arr: number[]) => {
    for (const x of arr) {
      dv.setUint32(off, x >>> 0, true);
      off += 4;
    }
  };
  const putI32 = (arr: number[]) => {
    for (const x of arr) {
      dv.setInt32(off, x, true);
      off += 4;
    }
  };
  putU32(opts.links);
  putU32(opts.inDeg);
  putU32(opts.outDeg);
  putU32(opts.community);
  putI32(opts.kind3);
  putI32(opts.lastDb);

  const bytes = new Uint8Array(buf);
  for (const pk of opts.pubkeys) {
    bytes.set(pk, off);
    off += PUBKEY_BYTES;
  }
  return buf;
}

/** A 32-byte pubkey filled with a repeating byte for easy hex assertion. */
function pk(fill: number): Uint8Array {
  return new Uint8Array(PUBKEY_BYTES).fill(fill);
}

/** Stub global fetch to stream the given bytes back via a ReadableStream. */
function stubFetch(frame: ArrayBuffer, chunkSize = 7): void {
  const all = new Uint8Array(frame);
  vi.stubGlobal('fetch', () =>
    Promise.resolve({
      body: new ReadableStream<Uint8Array>({
        start(controller) {
          for (let i = 0; i < all.length; i += chunkSize) {
            controller.enqueue(all.slice(i, i + chunkSize));
          }
          controller.close();
        },
      }),
    } as unknown as Response),
  );
}

const THREE_NODE_FRAME = () =>
  buildFrame({
    links: [0, 1, 1, 2, 2, 0],
    inDeg: [1, 1, 1],
    outDeg: [1, 1, 1],
    community: [0, 0, 1],
    kind3: [1000, 2000, 3000],
    lastDb: [1111, 2222, 3333],
    pubkeys: [pk(0xab), pk(0xcd), pk(0xef)],
  });

describe('GoBridgeTransport', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('decodes a synthetic frame into counts and Float32 link pairs', async () => {
    stubFetch(THREE_NODE_FRAME());
    const buffers = await new GoBridgeTransport().load(() => {});

    expect(buffers.nodeCount).toBe(3);
    expect(buffers.edgeCount).toBe(3);
    expect(buffers.links).toBeInstanceOf(Float32Array);
    expect(buffers.links.length).toBe(6);
    expect(Array.from(buffers.links)).toEqual([0, 1, 1, 2, 2, 0]);
  });

  it('populates all attribute arrays with lengths == nodeCount', async () => {
    stubFetch(THREE_NODE_FRAME());
    const b = await new GoBridgeTransport().load(() => {});

    expect(Array.from(b.inDegree!)).toEqual([1, 1, 1]);
    expect(Array.from(b.outDegree!)).toEqual([1, 1, 1]);
    expect(Array.from(b.community!)).toEqual([0, 0, 1]);
    expect(Array.from(b.kind3CreatedAt!)).toEqual([1000, 2000, 3000]);
    expect(Array.from(b.lastDbUpdate!)).toEqual([1111, 2222, 3333]);
    expect(b.inDegree!.length).toBe(b.nodeCount);
    expect(b.community!).toBeInstanceOf(Uint32Array);
    expect(b.kind3CreatedAt!).toBeInstanceOf(Int32Array);
  });

  it('hex-encodes the 32-byte packed pubkeys into hexByIndex', async () => {
    stubFetch(THREE_NODE_FRAME());
    const b = await new GoBridgeTransport().load(() => {});

    expect(b.hexByIndex).toBeDefined();
    expect(b.hexByIndex!.length).toBe(3);
    expect(b.hexByIndex![0]).toBe('ab'.repeat(32));
    expect(b.hexByIndex![1]).toBe('cd'.repeat(32));
    expect(b.hexByIndex![2]).toBe('ef'.repeat(32));
  });

  it('seeds positions (length == nodeCount*2)', async () => {
    stubFetch(THREE_NODE_FRAME());
    const b = await new GoBridgeTransport().load(() => {});
    expect(b.positions).toBeInstanceOf(Float32Array);
    expect(b.positions.length).toBe(6);
  });

  it('emits onProgress receive at least once and layout last', async () => {
    stubFetch(THREE_NODE_FRAME(), 7);
    const stages: LoadProgress['stage'][] = [];
    await new GoBridgeTransport().load((p) => stages.push(p.stage));

    expect(stages).toContain('receive');
    expect(stages[stages.length - 1]).toBe('layout');
  });

  it('throws on a wrong MAGIC before building typed arrays', async () => {
    stubFetch(buildFrame({
      magic: 0xdeadbeef,
      links: [0, 1],
      inDeg: [1, 0],
      outDeg: [1, 0],
      community: [0, 0],
      kind3: [0, 0],
      lastDb: [0, 0],
      pubkeys: [pk(0x01), pk(0x02)],
    }));
    await expect(new GoBridgeTransport().load(() => {})).rejects.toThrow(/MAGIC/);
  });
});

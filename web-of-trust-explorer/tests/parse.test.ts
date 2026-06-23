import { describe, it, expect } from 'vitest';
import {
  createHexRemap,
  parseDqlEnvelope,
  extractEncodingNs,
} from '../src/transport/GraphTransport';

/**
 * DATA-01: the DQL-envelope parser turns the documented Dgraph response shape
 * `{ data: { q: [{ uid, follows: [{ uid }] }] }, extensions: {...} }` into
 * correct [srcIdx, tgtIdx] edge pairs using the dense hex remap
 * (01-RESEARCH.md § response shape; 01-PATTERNS.md § Profile schema).
 */
describe('DQL envelope parser', () => {
  it('parses a single page into the correct edge pairs via the remap', () => {
    const remap = createHexRemap();
    const envelope = {
      data: {
        q: [
          { uid: '0x1', follows: [{ uid: '0x2' }, { uid: '0x5' }] },
          { uid: '0x2', follows: [{ uid: '0x5' }] },
        ],
      },
      extensions: { server_latency: { encoding_ns: 1234567 } },
    };
    const edges = parseDqlEnvelope(envelope, remap);
    // 0x1→0, 0x2→1, 0x5→2 in first-sighting order.
    // Edges: (0x1,0x2)=(0,1), (0x1,0x5)=(0,2), (0x2,0x5)=(1,2)
    expect(edges).toEqual([
      [0, 1],
      [0, 2],
      [1, 2],
    ]);
  });

  it('reuses indices across nodes (stable remap)', () => {
    const remap = createHexRemap();
    const envelope = {
      data: {
        q: [
          { uid: '0xaa', follows: [{ uid: '0xbb' }] },
          { uid: '0xbb', follows: [{ uid: '0xaa' }] },
        ],
      },
    };
    const edges = parseDqlEnvelope(envelope, remap);
    // 0xaa→0, 0xbb→1; reciprocal edges
    expect(edges).toEqual([
      [0, 1],
      [1, 0],
    ]);
    expect(remap.size).toBe(2);
  });

  it('handles nodes with no follows (leaves) without emitting edges', () => {
    const remap = createHexRemap();
    const envelope = {
      data: {
        q: [
          { uid: '0x1', follows: [{ uid: '0x2' }] },
          { uid: '0x3' }, // no follows field
          { uid: '0x4', follows: [] },
        ],
      },
    };
    const edges = parseDqlEnvelope(envelope, remap);
    expect(edges).toEqual([[0, 1]]);
  });

  it('returns no edges for an empty page', () => {
    const remap = createHexRemap();
    const edges = parseDqlEnvelope({ data: { q: [] } }, remap);
    expect(edges).toEqual([]);
  });

  it('surfaces extensions.server_latency.encoding_ns (verdict metric, D-10)', () => {
    const envelope = {
      data: { q: [{ uid: '0x1', follows: [{ uid: '0x2' }] }] },
      extensions: { server_latency: { encoding_ns: 1234567 } },
    };
    expect(extractEncodingNs(envelope)).toBe(1234567);
  });

  it('returns 0 encoding_ns when the extensions block is absent', () => {
    expect(extractEncodingNs({ data: { q: [] } })).toBe(0);
    expect(extractEncodingNs({ data: { q: [] }, extensions: {} })).toBe(0);
    expect(
      extractEncodingNs({ data: { q: [] }, extensions: { server_latency: {} } }),
    ).toBe(0);
  });
});

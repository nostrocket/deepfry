import { describe, it, expect } from 'vitest';
import { generateBA } from '../src/graph/generator';
import { buildGraphBuffers } from '../src/transport/GraphTransport';
import type { DqlEnvelope, LoadProgress } from '../src/transport/GraphTransport';
import { loadPagedGraph, buildDqlPageQuery } from '../src/transport/dgraphPaging';
import type { GraphBuffers } from '../src/types';

/**
 * DATA-01: anything that satisfies GraphTransport resolves to a GraphBuffers
 * whose typed-array invariants hold and whose nodeCount/edgeCount agree with
 * the buffer lengths. We assert the buffer contract directly (the Worker-driven
 * SyntheticTransport.load() is exercised manually in the Task 5 browser gate —
 * Web Workers don't run under the node test environment).
 */
function assertGraphBuffersInvariants(buffers: GraphBuffers): void {
  expect(buffers.positions).toBeInstanceOf(Float32Array);
  expect(buffers.links).toBeInstanceOf(Float32Array);
  expect(buffers.positions.length).toBe(buffers.nodeCount * 2);
  expect(buffers.links.length).toBe(buffers.edgeCount * 2);
}

describe('GraphTransport buffer contract', () => {
  it('buildGraphBuffers turns generator output into a valid GraphBuffers', () => {
    const g = generateBA(1000, 6, 42);
    const buffers = buildGraphBuffers(g.positions, g.links, g.nodeCount, g.edgeCount);
    assertGraphBuffersInvariants(buffers);
    expect(buffers.nodeCount).toBe(1000);
    expect(buffers.edgeCount).toBe(g.edgeCount);
  });

  it('produces a Float32Array link view (cosmos.gl requirement)', () => {
    const g = generateBA(500, 6, 1);
    const buffers = buildGraphBuffers(g.positions, g.links, g.nodeCount, g.edgeCount);
    // Links must be Float32Array for cosmos.gl setLinks, and values preserved.
    expect(buffers.links).toBeInstanceOf(Float32Array);
    for (let i = 0; i < buffers.links.length; i++) {
      expect(buffers.links[i]).toBe(g.links[i]);
    }
  });
});

/**
 * DATA-01: the DQL after-cursor paging loop that backs DgraphTransport. The loop
 * is exercised here with a stubbed page-fetcher (Web Workers / real fetch don't
 * run under the node test env, per the buffer-contract comment above), proving:
 * the query shape is read-only DQL, the cursor advances to the last uid each
 * page, paging terminates on a short/empty page, and the result satisfies the
 * GraphBuffers contract with hexByIndex for the tooltip.
 */
describe('DQL after-cursor paging loop (DgraphTransport core)', () => {
  it('builds a read-only DQL page query with first/after over has(follows)', () => {
    const q = buildDqlPageQuery('0x0', 50000);
    expect(q).toMatch(/func:\s*has\(follows\)/);
    expect(q).toMatch(/first:\s*50000/);
    expect(q).toMatch(/after:\s*0x0/);
    expect(q).toMatch(/follows\s*\{\s*uid\s*\}/);
    // Read-only invariant: never a mutation.
    expect(q).not.toMatch(/mutation|set\s*\{|delete\s*\{/i);
    // Never query followers (in-degree is derived client-side).
    expect(q).not.toMatch(/followers/);
  });

  it('pages until a short page, advancing the cursor to the last uid each page', async () => {
    // Two full pages of 2 nodes then a short final page → stop.
    const pages: DqlEnvelope[] = [
      {
        data: {
          q: [
            { uid: '0x1', follows: [{ uid: '0x2' }] },
            { uid: '0x2', follows: [{ uid: '0x3' }] },
          ],
        },
        extensions: { server_latency: { encoding_ns: 100 } },
      },
      {
        data: {
          q: [
            { uid: '0x3', follows: [{ uid: '0x4' }] },
            { uid: '0x4', follows: [{ uid: '0x1' }] },
          ],
        },
        extensions: { server_latency: { encoding_ns: 200 } },
      },
      {
        data: { q: [{ uid: '0x5', follows: [{ uid: '0x1' }] }] }, // short page → terminal
        extensions: { server_latency: { encoding_ns: 50 } },
      },
    ];
    const cursors: string[] = [];
    let call = 0;
    const fetchPage = async (cursor: string): Promise<DqlEnvelope> => {
      cursors.push(cursor);
      return pages[call++]!;
    };
    const progress: LoadProgress[] = [];
    const result = await loadPagedGraph(fetchPage, {
      pageSize: 2,
      onProgress: (p) => progress.push(p),
    });

    // Cursor starts at 0x0, then advances to the last uid of each prior page.
    expect(cursors).toEqual(['0x0', '0x2', '0x4']);
    // 5 distinct uids discovered: 0x1..0x5 → dense 0..4.
    expect(result.nodeCount).toBe(5);
    // Edges: (0x1,0x2)(0x2,0x3)(0x3,0x4)(0x4,0x1)(0x5,0x1) = 5 directed edges.
    expect(result.edgeCount).toBe(5);
    expect(result.links.length).toBe(result.edgeCount * 2);
    // In-degree derived client-side (no followers query). 0x1 is followed by 0x4 and 0x5 → 2.
    expect(result.inDegree[0]).toBe(2); // 0x1 → index 0
    // encoding_ns accumulates across pages.
    expect(result.encodingNs).toBe(350);
    // hexByIndex maps dense index → hex for the tooltip.
    expect(result.hexByIndex[0]).toBe('0x1');
    // Progress was emitted with a growing edge count.
    expect(progress.length).toBeGreaterThan(0);
    expect(progress[progress.length - 1]!.edgesSoFar).toBe(5);
  });

  it('terminates immediately on an empty first page', async () => {
    let call = 0;
    const fetchPage = async (): Promise<DqlEnvelope> => {
      call++;
      return { data: { q: [] } };
    };
    const result = await loadPagedGraph(fetchPage, { pageSize: 2 });
    expect(call).toBe(1);
    expect(result.nodeCount).toBe(0);
    expect(result.edgeCount).toBe(0);
  });
});

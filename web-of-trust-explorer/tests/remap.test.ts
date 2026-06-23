import { describe, it, expect } from 'vitest';
import { createHexRemap } from '../src/transport/GraphTransport';

/**
 * DATA-03: the hex→uint32 remap must be dense, stable, and collision-free.
 * First hex seen → 0, second → 1, …; repeated hex returns the SAME index;
 * distinct hexes never collide.
 */
describe('hex→uint32 remap', () => {
  it('assigns dense indices 0..n-1 in first-sighting order', () => {
    const remap = createHexRemap();
    expect(remap.indexOf('0xaa')).toBe(0);
    expect(remap.indexOf('0xbb')).toBe(1);
    expect(remap.indexOf('0xcc')).toBe(2);
    expect(remap.size).toBe(3);
  });

  it('returns the same index for a repeated hex (stable)', () => {
    const remap = createHexRemap();
    const first = remap.indexOf('0xdeadbeef');
    const again = remap.indexOf('0xdeadbeef');
    expect(again).toBe(first);
    expect(remap.size).toBe(1);
  });

  it('never collides across distinct hexes and stays dense', () => {
    const remap = createHexRemap();
    const hexes = Array.from({ length: 1000 }, (_, i) => `0x${i.toString(16)}`);
    const indices = hexes.map((h) => remap.indexOf(h));
    // dense 0..999, no gaps, no collisions
    const unique = new Set(indices);
    expect(unique.size).toBe(1000);
    expect(Math.min(...indices)).toBe(0);
    expect(Math.max(...indices)).toBe(999);
    expect(remap.size).toBe(1000);
  });

  it('interleaves new and repeated hexes correctly', () => {
    const remap = createHexRemap();
    expect(remap.indexOf('0x1')).toBe(0);
    expect(remap.indexOf('0x2')).toBe(1);
    expect(remap.indexOf('0x1')).toBe(0); // repeat
    expect(remap.indexOf('0x3')).toBe(2); // new continues the dense sequence
    expect(remap.size).toBe(3);
  });
});

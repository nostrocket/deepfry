import type { Graph } from '@cosmos.gl/graph';

/**
 * Auto-freeze motion sampler (D-12).
 *
 * The GPU force layout auto-starts on load (D-11) and runs until the hairball
 * settles into terrain. Rather than make the user babysit the Run/Pause toggle,
 * this sampler watches the layout and calls `graph.pause()` once node motion
 * drops below a threshold — the map freezes by itself when stable.
 *
 * Scale discipline (01-RESEARCH.md Pitfall 5): `getPointPositions()` returns the
 * FULL flat [x0,y0,x1,y1,…] array — 10M numbers at 5M nodes. We never diff all of
 * it per tick. Instead we pick a FIXED random subset of ~10k node indices once,
 * and each window compares only those nodes' positions against the previous
 * window. Mean per-node displacement over the subset is a cheap, stable proxy for
 * "is the whole layout still moving".
 *
 * Settle rule: when mean displacement < `thresholdUnits` (in spaceSize units) for
 * `windowsToSettle` consecutive windows, pause and emit "settled". `pause()` is
 * reversible (the Run toggle calls `unpause()`); we deliberately do not `stop()`.
 */

export interface AutoFreezeOptions {
  /** How many nodes to sample (capped at nodeCount). Default 10_000 (Pitfall 5). */
  sampleSize?: number;
  /** Sampling period in ms. Default 500. */
  intervalMs?: number;
  /** Mean per-node displacement (spaceSize units) below which a window counts as "still". Default 0.5. */
  thresholdUnits?: number;
  /** Consecutive sub-threshold windows required before freezing. Default 3. */
  windowsToSettle?: number;
  /** PRNG seed for choosing the fixed sample (deterministic subset). Default 1. */
  seed?: number;
  /** Called once when the layout is judged settled and paused. */
  onSettled?: () => void;
}

export interface AutoFreezeSampler {
  /** Begin sampling. Idempotent. */
  start(): void;
  /** Stop sampling (does not unpause the graph). Idempotent. */
  stop(): void;
  /** True while the interval timer is active. */
  readonly running: boolean;
}

/** Local deterministic PRNG so the sampled subset is reproducible. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return function next(): number {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Pick `sampleSize` distinct node indices in [0, nodeCount) deterministically.
 * For small graphs this is just every node; for large graphs it is a fixed random
 * subset chosen once.
 */
function pickSampleIndices(nodeCount: number, sampleSize: number, seed: number): Uint32Array {
  const n = Math.min(sampleSize, nodeCount);
  if (n === nodeCount) {
    const all = new Uint32Array(nodeCount);
    for (let i = 0; i < nodeCount; i++) all[i] = i;
    return all;
  }
  const rand = mulberry32(seed);
  const chosen = new Set<number>();
  // Rejection sampling: n << nodeCount here, so collisions are rare.
  while (chosen.size < n) {
    chosen.add((rand() * nodeCount) | 0);
  }
  return Uint32Array.from(chosen);
}

/**
 * Create a motion sampler that freezes the layout when it settles.
 *
 * @param graph     the cosmos.gl Graph instance
 * @param nodeCount total node count (to size the fixed sample)
 * @param opts      thresholds and the onSettled callback
 */
export function createAutoFreezeSampler(
  graph: Graph,
  nodeCount: number,
  opts: AutoFreezeOptions = {},
): AutoFreezeSampler {
  const {
    sampleSize = 10_000,
    intervalMs = 500,
    thresholdUnits = 0.5,
    windowsToSettle = 3,
    seed = 1,
    onSettled,
  } = opts;

  const sampleIdx = pickSampleIndices(nodeCount, sampleSize, seed);
  // Previous sampled positions, flat [x0,y0,x1,y1,…] aligned to sampleIdx order.
  let prev: Float32Array | null = null;
  let stillWindows = 0;
  let timer: ReturnType<typeof setInterval> | null = null;
  let settled = false;

  function tick(): void {
    const positions = graph.getPointPositions();
    // positions is the full flat array; read only the sampled indices.
    const cur = new Float32Array(sampleIdx.length * 2);
    for (let s = 0; s < sampleIdx.length; s++) {
      const node = sampleIdx[s]!;
      cur[s * 2] = positions[node * 2] ?? 0;
      cur[s * 2 + 1] = positions[node * 2 + 1] ?? 0;
    }

    if (prev) {
      let sum = 0;
      for (let s = 0; s < sampleIdx.length; s++) {
        const dx = cur[s * 2]! - prev[s * 2]!;
        const dy = cur[s * 2 + 1]! - prev[s * 2 + 1]!;
        sum += Math.hypot(dx, dy);
      }
      const meanDisplacement = sum / sampleIdx.length;

      if (meanDisplacement < thresholdUnits) {
        stillWindows++;
        if (stillWindows >= windowsToSettle && !settled) {
          settled = true;
          graph.pause();
          stop();
          onSettled?.();
        }
      } else {
        stillWindows = 0;
      }
    }
    prev = cur;
  }

  function start(): void {
    if (timer !== null) return;
    settled = false;
    stillWindows = 0;
    prev = null;
    timer = setInterval(tick, intervalMs);
  }

  function stop(): void {
    if (timer === null) return;
    clearInterval(timer);
    timer = null;
  }

  return {
    start,
    stop,
    get running(): boolean {
      return timer !== null;
    },
  };
}

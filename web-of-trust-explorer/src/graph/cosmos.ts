import { Graph } from '@cosmos.gl/graph';
import type { GraphBuffers } from '../types';

/**
 * cosmos.gl render adapter — the GPU node-link map.
 *
 * cosmos.gl owns the canvas imperatively; the app shell is read-only on it
 * (.claude/CLAUDE.md; 01-RESEARCH.md § Pattern 2). This adapter only feeds the
 * SoA buffers and starts the live force simulation. Run/Pause, auto-freeze, the
 * Fit button, and hover tooltips are Plan 02/03 — NOT here. The Graph instance
 * is returned so those plans can attach to it.
 *
 * cosmos.gl 3.0.0 verified API (01-RESEARCH.md § cosmos.gl Verified API):
 *   new Graph(div, config) → setPointPositions(Float32Array)
 *     → setLinks(Float32Array) → create() → start(alpha?)
 */
export function renderGraph(container: HTMLDivElement, buffers: GraphBuffers): Graph {
  const graph = new Graph(container, {
    spaceSize: 8192, // larger than the 4096 default to reduce overlap as scale grows
    enableSimulation: true, // live GPU force layout auto-starts (D-11)
    enableDrag: true, // pan via drag; wheel/pinch zoom come free from bundled d3-zoom
    fitViewOnInit: true, // auto-fit the whole map on first layout (REND-04 baseline)
  });

  // Structure-of-arrays buffers — exactly what this phase produces.
  graph.setPointPositions(buffers.positions);
  graph.setLinks(buffers.links);
  graph.create(); // apply pending data
  graph.start(); // begin the GPU force simulation (D-11)

  return graph;
}

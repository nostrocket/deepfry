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
 * cosmos.gl 3.0.0 API — verified against the INSTALLED source
 * (node_modules/@cosmos.gl/graph/{README.md,dist/index.d.ts}), which overrides
 * 01-RESEARCH.md's MEDIUM-confidence guess where they conflict:
 *   new Graph(div, config) → setPointPositions(Float32Array)
 *     → setLinks(Float32Array) → render()
 *
 * Why render(), not create()+start():
 *   - render() is what actually starts the GPU draw/frame loop AND, because the
 *     simulation auto-starts by default (config.enableSimulation), kicks off the
 *     live force layout — exactly the README's canonical example (setPointPositions
 *     → setLinks → render()).
 *   - create() only "applies pending data changes WITHOUT calling render()"
 *     (index.d.ts:573). It never starts the frame loop, so nothing draws AND the
 *     points module's picking framebuffer (hoveredFbo, created inside the draw
 *     cycle) is never allocated — which made cosmos.gl's built-in mousemove hover
 *     picking crash (readPixelsToArray on an undefined framebuffer). render() fixes
 *     both: the blank canvas and the hover crash.
 *   - All public methods queue internally until the async WebGL device is ready
 *     (README "Async initialization"; constructor returns immediately and each
 *     method goes through ensureDevice), so this synchronous call order is safe —
 *     no `await graph.ready` needed for these data/render setters.
 */
export function renderGraph(container: HTMLDivElement, buffers: GraphBuffers): Graph {
  const graph = new Graph(container, {
    spaceSize: 8192, // larger than the 4096 default to reduce overlap as scale grows
    enableSimulation: true, // live GPU force layout auto-starts on render() (D-11)
    enableDrag: true, // pan via drag; wheel/pinch zoom come free from bundled d3-zoom
    fitViewOnInit: true, // auto-fit the whole map on first layout (REND-04 baseline)
  });

  // Structure-of-arrays buffers — exactly what this phase produces.
  graph.setPointPositions(buffers.positions);
  graph.setLinks(buffers.links);
  graph.render(); // start the GPU draw loop; simulation auto-starts (D-11)

  return graph;
}

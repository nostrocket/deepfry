import { Graph } from '@cosmos.gl/graph';
import type { GraphBuffers } from '../types';

/**
 * Hover event surfaced to the shell. `index` is the uint32 node index under the
 * cursor; `x`/`y` are client (screen) coordinates for positioning the tooltip.
 */
export interface HoverEvent {
  index: number;
  x: number;
  y: number;
}

/**
 * The render adapter the app shell drives. cosmos.gl owns the canvas; the shell
 * is read-only on it (Pattern 2) and only calls these imperative controls:
 *   - run()/pause() — Run/Pause toggle (D-12) and auto-freeze (D-12)
 *   - fit()         — Fit/Reset to the whole map (REND-04/D-13)
 *   - onHover/onHoverOut — drive the index tooltip (D-14)
 * The underlying Graph is exposed for the auto-freeze sampler (getPointPositions).
 */
export interface CosmosAdapter {
  /** Resume the force simulation (Run). */
  run(): void;
  /** Pause the force simulation (Pause / auto-freeze). Reversible via run(). */
  pause(): void;
  /** Fit/Reset the view to the whole map. */
  fit(): void;
  /** Register the hover handler; replaces any previous one. */
  onHover(handler: (e: HoverEvent) => void): void;
  /** Register the hover-out handler; replaces any previous one. */
  onHoverOut(handler: () => void): void;
  /** Underlying cosmos.gl Graph (for the auto-freeze sampler's getPointPositions). */
  readonly graph: Graph;
}

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
export function renderGraph(container: HTMLDivElement, buffers: GraphBuffers): CosmosAdapter {
  // Mutable hover handlers the shell registers after construction.
  let hoverHandler: ((e: HoverEvent) => void) | null = null;
  let hoverOutHandler: (() => void) | null = null;

  const graph = new Graph(container, {
    spaceSize: 8192, // larger than the 4096 default to reduce overlap as scale grows
    enableSimulation: true, // live GPU force layout auto-starts on render() (D-11)
    enableDrag: true, // pan via drag; wheel/pinch zoom come free from bundled d3-zoom
    fitViewOnInit: true, // auto-fit the whole map on first layout (REND-04 baseline)
    // Built-in GPU hit-testing surfaces the hovered node index (D-14). cosmos.gl
    // reads the picking framebuffer (allocated by render()) on mousemove.
    onPointMouseOver: (index, _pointPosition, event) => {
      const me = event as MouseEvent | undefined;
      hoverHandler?.({
        index,
        x: me?.clientX ?? 0,
        y: me?.clientY ?? 0,
      });
    },
    onPointMouseOut: () => {
      hoverOutHandler?.();
    },
  });

  // Structure-of-arrays buffers — exactly what this phase produces.
  graph.setPointPositions(buffers.positions);
  graph.setLinks(buffers.links);
  graph.render(); // start the GPU draw loop; simulation auto-starts (D-11)

  return {
    run: () => graph.unpause(), // resume the paused simulation (D-12)
    pause: () => graph.pause(), // reversible freeze, NOT stop() (RESEARCH recommendation)
    fit: () => graph.fitView(250, 0.1), // animate to fit the whole map (REND-04/D-13)
    onHover: (handler) => {
      hoverHandler = handler;
    },
    onHoverOut: (handler) => {
      hoverOutHandler = handler;
    },
    graph,
  };
}

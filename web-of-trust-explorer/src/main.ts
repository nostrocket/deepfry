import { SyntheticTransport } from './transport/SyntheticTransport';
import type { GraphTransport, LoadProgress } from './transport/GraphTransport';
import { renderGraph } from './graph/cosmos';
import { createAutoFreezeSampler } from './graph/autofreeze';
import { mountControls } from './ui/controls';

/**
 * App entry — the GPU-ceiling spike wiring (Plan 02).
 *
 * Generates the fixed 5,000,000-node / ~30,000,000-edge synthetic power-law graph
 * in a Worker (D-01/D-02), hands the SoA typed buffers to the cosmos.gl GPU
 * render (which auto-starts the live force layout, D-11), then:
 *   - starts the auto-freeze sampler so the layout freezes itself when settled (D-12)
 *   - mounts the Run/Pause toggle + Fit button + hover tooltip control shell
 *   - routes cosmos's hover-index emit to the tooltip (D-14)
 * Data is reached ONLY through the GraphTransport interface — main.ts never
 * fetches or generates directly (swappable transport; Plan 03 swaps in Dgraph).
 */
async function main(): Promise<void> {
  const container = document.querySelector<HTMLDivElement>('#graph');
  if (!container) throw new Error('#graph container not found');
  const controlsMount =
    document.querySelector<HTMLElement>('#controls') ?? document.body;

  // Plan 03 swaps this for DgraphTransport behind the same interface. Default
  // constructor → 5,000,000 nodes / m=6 (~30M directed edges), the GPU target.
  const transport: GraphTransport = new SyntheticTransport();

  const buffers = await transport.load((progress: LoadProgress) => {
    // Minimal progress logging for the spike; the staged loader UI is Plan 03 (D-09).
    console.log(`[load] ${progress.stage}: ${progress.edgesSoFar} edges`);
  });

  console.log(
    `[render] ${buffers.nodeCount} nodes / ${buffers.edgeCount} edges; ` +
      `crossOriginIsolated=${self.crossOriginIsolated}`,
  );

  const adapter = renderGraph(container, buffers);

  // Control shell: Run/Pause toggle, Fit, hover tooltip.
  const controls = mountControls({
    mount: controlsMount,
    adapter,
    ...(buffers.hexByIndex ? { hexByIndex: buffers.hexByIndex } : {}),
  });

  // Route cosmos's GPU hit-test hover index → tooltip (D-14).
  adapter.onHover((e) => controls.showTooltip(e));
  adapter.onHoverOut(() => controls.hideTooltip());

  // Auto-freeze: sample a fixed ~10k-node subset every 500ms; when motion settles
  // pause the layout and flip the toggle to "Run" so the user can resume (D-12).
  const autoFreeze = createAutoFreezeSampler(adapter.graph, buffers.nodeCount, {
    onSettled: () => {
      controls.notifySettled();
      console.log('[autofreeze] layout settled — paused');
    },
  });
  autoFreeze.start();
}

void main();

import { SyntheticTransport } from './transport/SyntheticTransport';
import type { GraphTransport, LoadProgress } from './transport/GraphTransport';
import { renderGraph } from './graph/cosmos';

/**
 * App entry — the Walking Skeleton wiring.
 *
 * Generates a small (~1,000-node) synthetic graph in a Worker, hands the SoA
 * typed buffers to the cosmos.gl GPU render, which the user can pan (drag) and
 * zoom (wheel/pinch). Data is reached ONLY through the GraphTransport interface
 * — main.ts never fetches or generates directly (D — swappable transport).
 */
async function main(): Promise<void> {
  const container = document.querySelector<HTMLDivElement>('#graph');
  if (!container) throw new Error('#graph container not found');

  // Plan 03 swaps this for DgraphTransport behind the same interface.
  const transport: GraphTransport = new SyntheticTransport(1000, 6, 1);

  const buffers = await transport.load((progress: LoadProgress) => {
    // Minimal progress logging for the skeleton; the staged loader UI is Plan 03 (D-09).
    console.log(`[load] ${progress.stage}: ${progress.edgesSoFar} edges`);
  });

  console.log(
    `[render] ${buffers.nodeCount} nodes / ${buffers.edgeCount} edges; ` +
      `crossOriginIsolated=${self.crossOriginIsolated}`,
  );

  renderGraph(container, buffers);
}

void main();

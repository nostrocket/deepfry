import { SyntheticTransport } from './transport/SyntheticTransport';
import { DgraphTransport } from './transport/DgraphTransport';
import type { GraphTransport, LoadProgress } from './transport/GraphTransport';
import { renderGraph } from './graph/cosmos';
import { createAutoFreezeSampler } from './graph/autofreeze';
import { mountControls } from './ui/controls';
import { mountLoader } from './ui/loader';
import { measurePeakHeap, renderVerdict, type VerdictMetrics } from './ui/verdict';

/**
 * App entry — the data spine wiring.
 *
 * The data path is SELECTABLE behind the single GraphTransport interface:
 *   - default / `?transport=synthetic` → SyntheticTransport: the fixed
 *     5,000,000-node / ~30,000,000-edge BA graph in a Worker (GPU-ceiling spike,
 *     Plan 02; D-01/D-02).
 *   - `?transport=dgraph` → DgraphTransport: the real `follows` graph bulk-loaded
 *     from the dev Dgraph via read-only DQL after-cursor paging (JSON-wire
 *     feasibility verdict, Plan 03; D-08). Mounts the staged loader (D-09) and,
 *     on completion, renders the verdict readout (D-10).
 *
 * Either way main.ts reaches data ONLY through GraphTransport — it never fetches
 * or generates directly — so JSON-direct → Go-binary-stream is a one-file swap
 * later (PERF-01).
 */

function selectTransport(): { transport: GraphTransport; isDgraph: boolean } {
  const which = new URLSearchParams(location.search).get('transport');
  if (which === 'dgraph') {
    return { transport: new DgraphTransport(), isDgraph: true };
  }
  return { transport: new SyntheticTransport(), isDgraph: false };
}

async function main(): Promise<void> {
  const container = document.querySelector<HTMLDivElement>('#graph');
  if (!container) throw new Error('#graph container not found');
  const controlsMount = document.querySelector<HTMLElement>('#controls') ?? document.body;

  const { transport, isDgraph } = selectTransport();

  // Staged loader (D-09) is the real-wire measurement instrument; mount it only
  // for the Dgraph path. The synthetic path keeps the lightweight console log.
  const loader = isDgraph ? mountLoader(controlsMount) : null;

  // Verdict timing (D-10): split fetch vs parse by watching the stage flip.
  const loadStart = performance.now();
  let firstParseAt: number | null = null;

  const buffers = await transport.load((progress: LoadProgress) => {
    if (progress.stage === 'parse' && firstParseAt === null) {
      firstParseAt = performance.now();
    }
    loader?.update(progress);
    console.log(`[load] ${progress.stage}: ${progress.edgesSoFar} edges`);
  });
  const loadEndMs = performance.now() - loadStart;

  console.log(
    `[render] ${buffers.nodeCount} nodes / ${buffers.edgeCount} edges; ` +
      `crossOriginIsolated=${self.crossOriginIsolated}`,
  );

  const adapter = renderGraph(container, buffers);
  loader?.done();
  const layoutReadyMs = performance.now() - loadStart;

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

  // Real-wire verdict readout (D-10): surface fetch/parse/layout ms + peak heap.
  if (isDgraph) {
    const fetchMs = firstParseAt !== null ? firstParseAt - loadStart : loadEndMs;
    const parseMs = Math.max(0, loadEndMs - fetchMs);
    const { bytes, source } = await measurePeakHeap();
    const metrics: VerdictMetrics = {
      fetchMs,
      parseMs,
      layoutReadyMs,
      nodeCount: buffers.nodeCount,
      edgeCount: buffers.edgeCount,
      encodingNs: (transport as DgraphTransport).lastEncodingNs,
      peakHeapBytes: bytes,
      peakHeapSource: source,
    };
    renderVerdict(controlsMount, metrics);
  }
}

void main();

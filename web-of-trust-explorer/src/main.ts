import { SyntheticTransport } from './transport/SyntheticTransport';
import { DgraphTransport } from './transport/DgraphTransport';
import { GoBridgeTransport } from './transport/GoBridgeTransport';
import type { GraphTransport, LoadProgress } from './transport/GraphTransport';
import { renderGraph } from './graph/cosmos';
import { createAutoFreezeSampler } from './graph/autofreeze';
import { mountControls } from './ui/controls';
import { mountLoader } from './ui/loader';
import {
  measurePeakHeap,
  renderVerdict,
  renderBridgeVerdict,
  type VerdictMetrics,
  type BridgeVerdictMetrics,
} from './ui/verdict';

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
 *   - `?transport=bridge` → GoBridgeTransport: the real graph decoded from the Go
 *     bridge's binary frame at /graph.bin (same-origin via the Vite proxy), with
 *     zero JSON.parse — the PERF-01 drop-in swap (D-08). This task wires only
 *     selection + render via the existing path; the bridge-specific loader stages
 *     and PASS verdict are Plan 03.
 *
 * Either way main.ts reaches data ONLY through GraphTransport — it never fetches
 * or generates directly — so JSON-direct → Go-binary-stream is a one-file swap
 * later (PERF-01).
 */

function selectTransport(): {
  transport: GraphTransport;
  isDgraph: boolean;
  isBridge: boolean;
} {
  const which = new URLSearchParams(location.search).get('transport');
  if (which === 'dgraph') {
    return { transport: new DgraphTransport(), isDgraph: true, isBridge: false };
  }
  if (which === 'bridge') {
    // The binary-wire PERF-01 path. It mounts the loader (binary stages) and, on
    // completion, records the bridge verdict (server/stream/decode ms + counts +
    // peak heap) — NOT the DgraphTransport lastEncodingNs/30M extrapolation, so
    // isDgraph stays false and isBridge drives the bridge verdict instead (D-09).
    return { transport: new GoBridgeTransport(), isDgraph: false, isBridge: true };
  }
  return { transport: new SyntheticTransport(), isDgraph: false, isBridge: false };
}

async function main(): Promise<void> {
  const container = document.querySelector<HTMLDivElement>('#graph');
  if (!container) throw new Error('#graph container not found');
  const controlsMount = document.querySelector<HTMLElement>('#controls') ?? document.body;

  const { transport, isDgraph, isBridge } = selectTransport();

  // Staged loader (D-09) is the real-wire measurement instrument; mount it for
  // both real-wire paths (JSON Dgraph + binary bridge). The synthetic path keeps
  // the lightweight console log.
  const loader = isDgraph || isBridge ? mountLoader(controlsMount) : null;

  // Verdict timing (D-09/D-10):
  //  - JSON path: split fetch vs parse on the 'parse' stage flip.
  //  - Bridge path: server-compute = loadStart → first 'receive' (first byte);
  //    stream = first 'receive' → last 'receive'; decode = last 'receive' → 'layout'.
  const loadStart = performance.now();
  let firstParseAt: number | null = null;
  let firstReceiveAt: number | null = null;
  let lastReceiveAt: number | null = null;
  let layoutStageAt: number | null = null;

  const buffers = await transport.load((progress: LoadProgress) => {
    const now = performance.now();
    if (progress.stage === 'parse' && firstParseAt === null) {
      firstParseAt = now;
    }
    if (progress.stage === 'receive') {
      if (firstReceiveAt === null) firstReceiveAt = now;
      lastReceiveAt = now;
    }
    if (progress.stage === 'layout' && layoutStageAt === null) {
      layoutStageAt = now;
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

  // Bridge (binary-wire) verdict readout (D-09/D-10): the PERF-01 re-verdict.
  // server-compute = loadStart→first byte; stream = first→last byte; decode =
  // last byte→buffers built. No 30M extrapolation — the bridge loads the real
  // graph directly. The qualitative PASS (usable / no swap / 60fps holds) is
  // recorded by the operator at the human-verify checkpoint; left undefined here
  // so the panel reads PENDING-HUMAN-VERIFY until then.
  if (isBridge) {
    const serverComputeMs =
      firstReceiveAt !== null ? firstReceiveAt - loadStart : loadEndMs;
    const streamMs =
      firstReceiveAt !== null && lastReceiveAt !== null
        ? Math.max(0, lastReceiveAt - firstReceiveAt)
        : 0;
    const decodeMs =
      lastReceiveAt !== null && layoutStageAt !== null
        ? Math.max(0, layoutStageAt - lastReceiveAt)
        : Math.max(0, loadEndMs - serverComputeMs - streamMs);
    const { bytes, source } = await measurePeakHeap();
    const metrics: BridgeVerdictMetrics = {
      serverComputeMs,
      streamMs,
      decodeMs,
      layoutReadyMs,
      nodeCount: buffers.nodeCount,
      edgeCount: buffers.edgeCount,
      peakHeapBytes: bytes,
      peakHeapSource: source,
    };
    renderBridgeVerdict(controlsMount, metrics);
  }
}

void main();

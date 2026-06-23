/**
 * JSON-wire feasibility verdict readout (D-10) — the recorded artifact of the
 * Plan 03 spike. This is the measurement instrument: it captures the real-wire
 * timings + peak JS heap on the dev Dgraph, renders an on-screen breakdown panel
 * AND `console.table`s it, so the verdict (PASS ≤~30s extrapolated, or PERF-01
 * trigger) can be read off and recorded.
 *
 * Peak heap is a co-equal verdict metric to time (D-08): the 18 GB unified pool
 * is a real ceiling. We prefer `performance.measureUserAgentSpecificMemory()`
 * (most accurate, async, needs the COOP/COEP cross-origin isolation already set
 * in vite.config.ts) and fall back to Chrome's `performance.memory.usedJSHeapSize`
 * (D-07 reference browser) when it is unavailable.
 */

export interface VerdictMetrics {
  /** ms spent fetching all pages over the wire. */
  fetchMs: number;
  /** ms spent parsing + remapping + building buffers. */
  parseMs: number;
  /** ms from load start to layout-ready (graph rendered + first settle). */
  layoutReadyMs: number;
  nodeCount: number;
  edgeCount: number;
  /** Accumulated server-side encode cost across all pages (ns). */
  encodingNs: number;
  /** Peak JS heap in bytes (measureUserAgentSpecificMemory or usedJSHeapSize). */
  peakHeapBytes: number;
  /** Which API produced peakHeapBytes (for honesty in the recorded verdict). */
  peakHeapSource: 'measureUserAgentSpecificMemory' | 'usedJSHeapSize' | 'unavailable';
}

interface MeasuredMemory {
  bytes: number;
}
interface PerfMemory {
  usedJSHeapSize: number;
}

/**
 * Measure peak JS heap, preferring the accurate cross-agent API and falling back
 * to Chrome's coarse synchronous metric (D-07). Returns the bytes + which source
 * produced them so the recorded verdict is honest about its instrument.
 */
export async function measurePeakHeap(): Promise<{ bytes: number; source: VerdictMetrics['peakHeapSource'] }> {
  const perf = performance as Performance & {
    measureUserAgentSpecificMemory?: () => Promise<MeasuredMemory>;
    memory?: PerfMemory;
  };
  if (typeof perf.measureUserAgentSpecificMemory === 'function') {
    try {
      const m = await perf.measureUserAgentSpecificMemory();
      return { bytes: m.bytes, source: 'measureUserAgentSpecificMemory' };
    } catch {
      // fall through to the coarse fallback
    }
  }
  if (perf.memory && typeof perf.memory.usedJSHeapSize === 'number') {
    return { bytes: perf.memory.usedJSHeapSize, source: 'usedJSHeapSize' };
  }
  return { bytes: 0, source: 'unavailable' };
}

/** Linear extrapolation of total load time to the 30M-edge target (D-04/D-05). */
const TARGET_EDGES = 30_000_000;
const PASS_THRESHOLD_MS = 30_000;

export interface VerdictExtrapolation {
  edgesPerMs: number;
  projectedLoadMsAt30M: number;
  pass: boolean;
}

/**
 * Extrapolate the real-run throughput linearly to 30M edges and call PASS/FAIL.
 * PASS = projected total load ≤ ~30s (D-05); worse triggers pulling PERF-01
 * (the Go binary-streaming bridge) forward to v1.
 */
export function extrapolateVerdict(m: VerdictMetrics): VerdictExtrapolation {
  const edgesPerMs = m.edgeCount > 0 && m.layoutReadyMs > 0 ? m.edgeCount / m.layoutReadyMs : 0;
  const projectedLoadMsAt30M = edgesPerMs > 0 ? TARGET_EDGES / edgesPerMs : Infinity;
  return {
    edgesPerMs,
    projectedLoadMsAt30M,
    pass: projectedLoadMsAt30M <= PASS_THRESHOLD_MS,
  };
}

const PANEL_STYLE = `
  position: fixed; bottom: 12px; right: 12px; z-index: 25;
  background: #1b1b24ee; color: #e6e6f0;
  border: 1px solid #33334a; border-radius: 6px;
  padding: 10px 14px; max-width: 340px;
  font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: pre;
`;

function fmtBytes(b: number): string {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(2)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(1)} KB`;
  return `${b} B`;
}

/**
 * Render the verdict on-screen AND `console.table` it (D-10). Returns the panel
 * element so callers can remove it if needed.
 */
export function renderVerdict(mount: HTMLElement, m: VerdictMetrics): HTMLElement {
  const ex = extrapolateVerdict(m);

  // Console.table for the recorded artifact.
  console.table({
    fetchMs: Math.round(m.fetchMs),
    parseMs: Math.round(m.parseMs),
    layoutReadyMs: Math.round(m.layoutReadyMs),
    nodeCount: m.nodeCount,
    edgeCount: m.edgeCount,
    encodingMs: Math.round(m.encodingNs / 1e6),
    peakHeap: fmtBytes(m.peakHeapBytes),
    peakHeapSource: m.peakHeapSource,
    edgesPerMs: Math.round(ex.edgesPerMs),
    projectedLoadAt30M_s: Number((ex.projectedLoadMsAt30M / 1000).toFixed(1)),
    verdict: ex.pass ? 'PASS' : 'FAIL → PERF-01',
  });

  const panel = document.createElement('div');
  panel.setAttribute('style', PANEL_STYLE);
  panel.textContent = [
    'JSON-wire feasibility verdict (D-10)',
    `fetch        ${Math.round(m.fetchMs)} ms`,
    `parse        ${Math.round(m.parseMs)} ms`,
    `layout-ready ${Math.round(m.layoutReadyMs)} ms`,
    `server enc.  ${Math.round(m.encodingNs / 1e6)} ms`,
    `nodes/edges  ${m.nodeCount} / ${m.edgeCount}`,
    `peak heap    ${fmtBytes(m.peakHeapBytes)} (${m.peakHeapSource})`,
    `throughput   ${Math.round(ex.edgesPerMs)} edges/ms`,
    `→ 30M proj.  ${(ex.projectedLoadMsAt30M / 1000).toFixed(1)} s`,
    `VERDICT      ${ex.pass ? 'PASS (≤30s)' : 'FAIL → trigger PERF-01'}`,
  ].join('\n');

  mount.append(panel);
  return panel;
}

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

/**
 * Bridge-path (binary-wire) feasibility verdict (D-09/D-10) — the recorded
 * artifact that resolves the 01-03 JSON-wire FAIL.
 *
 * The JSON path's linear-extrapolate-to-30M / ≤30s gate above does NOT apply
 * here: the Go bridge loads the real graph directly (no extrapolation), so the
 * verdict is qualitative per the Claude's-discretion PASS bar — usable on the
 * real dev DB, no swap, render holds 60fps after load — recorded alongside the
 * measured server / stream / decode split, the real node+edge counts, and peak
 * JS heap (D-08's co-equal ceiling metric, measured the same way as the JSON
 * path via measurePeakHeap()).
 */
export interface BridgeVerdictMetrics {
  /**
   * ms the server-side fetch+remap+degree+community pass took, surfaced as
   * fetch-issued → first-byte (the bridge holds the connection open while it
   * reads Dgraph and computes, then starts streaming).
   */
  serverComputeMs: number;
  /** ms first-byte → last-byte (the binary stream transfer itself). */
  streamMs: number;
  /** ms last-byte → buffers built (concat + view + buildGraphBuffers; no parse). */
  decodeMs: number;
  /** ms from load start to layout-ready (graph rendered + first settle). */
  layoutReadyMs: number;
  nodeCount: number;
  edgeCount: number;
  /** Peak JS heap in bytes (measureUserAgentSpecificMemory or usedJSHeapSize). */
  peakHeapBytes: number;
  /** Which API produced peakHeapBytes (for honesty in the recorded verdict). */
  peakHeapSource: VerdictMetrics['peakHeapSource'];
  /**
   * The qualitative PASS judgement per the Claude's-discretion bar. Set by the
   * operator at the human-verify checkpoint (usable / no swap / 60fps holds).
   * Left undefined until a human records it — the panel shows PENDING then.
   */
  pass?: boolean;
  /** Free-text note from the checkpoint (e.g. "no swap; 60fps held; Louvain undirected"). */
  note?: string;
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

/**
 * Render the BRIDGE (binary-wire) verdict on-screen AND `console.table` it
 * (D-09/D-10). Unlike `renderVerdict`, there is NO 30M/30s extrapolation gate —
 * the bridge loads the real graph directly, so the verdict is the measured split
 * (server / stream / decode ms), the real counts, peak heap, and a qualitative
 * PASS recorded by the operator (usable / no swap / 60fps holds). Until a human
 * records `pass`, the verdict reads PENDING-HUMAN-VERIFY. Returns the panel.
 */
export function renderBridgeVerdict(mount: HTMLElement, m: BridgeVerdictMetrics): HTMLElement {
  const verdictLabel =
    m.pass === undefined ? 'PENDING-HUMAN-VERIFY' : m.pass ? 'PASS' : 'FAIL';

  // Console.table for the recorded artifact.
  console.table({
    serverComputeMs: Math.round(m.serverComputeMs),
    streamMs: Math.round(m.streamMs),
    decodeMs: Math.round(m.decodeMs),
    layoutReadyMs: Math.round(m.layoutReadyMs),
    nodeCount: m.nodeCount,
    edgeCount: m.edgeCount,
    peakHeap: fmtBytes(m.peakHeapBytes),
    peakHeapSource: m.peakHeapSource,
    verdict: verdictLabel,
    note: m.note ?? '',
  });

  const panel = document.createElement('div');
  panel.setAttribute('style', PANEL_STYLE);
  panel.textContent = [
    'Bridge feasibility verdict (PERF-01)',
    `server cmp.  ${Math.round(m.serverComputeMs)} ms`,
    `stream       ${Math.round(m.streamMs)} ms`,
    `decode       ${Math.round(m.decodeMs)} ms`,
    `layout-ready ${Math.round(m.layoutReadyMs)} ms`,
    `nodes/edges  ${m.nodeCount} / ${m.edgeCount}`,
    `peak heap    ${fmtBytes(m.peakHeapBytes)} (${m.peakHeapSource})`,
    `VERDICT      ${verdictLabel}`,
    m.note ? `note         ${m.note}` : '',
  ]
    .filter(Boolean)
    .join('\n');

  mount.append(panel);
  return panel;
}

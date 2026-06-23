import type { LoadProgress } from '../transport/GraphTransport';

/**
 * Staged load overlay (D-09). This is a measurement instrument, not decoration:
 * it shows which stage the load is in and a live counter.
 *
 * Two transport paths drive it:
 *   - JSON wire (DgraphTransport): `Fetching from Dgraph…` → `Parsing edges…` →
 *     `Building layout…`, counting edges as `LoadProgress.edgesSoFar` grows.
 *   - Binary wire (GoBridgeTransport): `Fetching…` → `Receiving bytes (MB/s)…` →
 *     `Building layout…`, counting bytes received with a live MB/s rate. There is
 *     deliberately NO `Parsing JSON` stage on this path — the binary frame is
 *     decoded into typed-array views with zero single-shot text decode, which is
 *     the load-bearing PERF-01 win (D-08/D-09).
 *
 * There is deliberately NO percentage bar on either path — neither cursor paging
 * (no edge total until the last page) nor the chunked binary stream (no
 * Content-Length) has an honest upfront total, so a progress bar would be a lie
 * (01-RESEARCH.md / 01-CONTEXT.md D-09).
 */

const STAGE_LABELS: Record<LoadProgress['stage'], string> = {
  fetch: 'Fetching…',
  parse: 'Parsing edges…',
  // 'receive' is the GoBridgeTransport binary-stream stage (bytes flowing, no
  // text decode) — the label carries the MB/s rate the readout below ticks.
  receive: 'Receiving bytes (MB/s)…',
  layout: 'Building layout…',
};

const OVERLAY_STYLE = `
  position: fixed; inset: 0; z-index: 30;
  display: flex; flex-direction: column;
  align-items: center; justify-content: center; gap: 10px;
  background: #0a0a0fcc; backdrop-filter: blur(2px);
  color: #e6e6f0;
  font: 14px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace;
`;
const STAGE_STYLE = `font-size: 16px; letter-spacing: 0.02em;`;
const COUNT_STYLE = `color: #9a9ab8;`;

export interface LoaderHandle {
  /** Update the overlay from a LoadProgress message. */
  update(p: LoadProgress): void;
  /** Remove the overlay (call once the graph is rendered). */
  done(): void;
}

/** Format an edge count compactly (e.g. 12.3M, 845k, 73). */
function formatEdges(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return `${n}`;
}

/** Format a byte count compactly (e.g. 1.23 GB, 845.0 MB, 12.3 KB). */
function formatBytes(b: number): string {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(2)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(1)} KB`;
  return `${b} B`;
}

/**
 * Mount the staged loader overlay. Returns a handle the load loop drives via
 * `update(progress)` and tears down with `done()`.
 */
export function mountLoader(mount: HTMLElement): LoaderHandle {
  const overlay = document.createElement('div');
  overlay.setAttribute('style', OVERLAY_STYLE);

  const stageEl = document.createElement('div');
  stageEl.setAttribute('style', STAGE_STYLE);
  stageEl.textContent = STAGE_LABELS.fetch;

  const countEl = document.createElement('div');
  countEl.setAttribute('style', COUNT_STYLE);
  countEl.textContent = '0 edges';

  overlay.append(stageEl, countEl);
  mount.append(overlay);

  // Receive-stage rate tracking: first byte-bearing 'receive' update marks the
  // stream start; the MB/s rate is bytes / elapsed (no upfront total → rate, not
  // a percentage; D-09).
  let receiveStartMs: number | null = null;

  return {
    update(p: LoadProgress): void {
      stageEl.textContent = STAGE_LABELS[p.stage];
      if (p.stage === 'receive' && p.bytesSoFar !== undefined) {
        // Binary path: bytes received + a live MB/s rate.
        const now = performance.now();
        if (receiveStartMs === null) receiveStartMs = now;
        const elapsedS = Math.max(1e-3, (now - receiveStartMs) / 1000);
        const mbPerS = p.bytesSoFar / (1 << 20) / elapsedS;
        countEl.textContent = `${formatBytes(p.bytesSoFar)} · ${mbPerS.toFixed(1)} MB/s`;
      } else {
        // JSON-wire path (and fetch/layout stages): live edge count, no percentage.
        countEl.textContent = `${formatEdges(p.edgesSoFar)} edges`;
      }
    },
    done(): void {
      overlay.remove();
    },
  };
}

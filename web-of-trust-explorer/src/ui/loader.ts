import type { LoadProgress } from '../transport/GraphTransport';

/**
 * Staged load overlay for the real-wire Dgraph path (D-09). This is a
 * measurement instrument, not decoration: it shows which stage the load is in
 * (`Fetching from Dgraph…` → `Parsing edges…` → `Building layout…`) and a live
 * edge counter ticking up as `LoadProgress.edgesSoFar` grows.
 *
 * There is deliberately NO percentage bar — cursor paging has no honest upfront
 * total (we don't know the edge count until the last page lands), so a progress
 * bar would be a lie (01-RESEARCH.md / 01-CONTEXT.md D-09).
 */

const STAGE_LABELS: Record<LoadProgress['stage'], string> = {
  fetch: 'Fetching from Dgraph…',
  parse: 'Parsing edges…',
  // 'receive' is the GoBridgeTransport binary-stream stage (bytes flowing, no parse).
  receive: 'Receiving binary frame…',
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

  return {
    update(p: LoadProgress): void {
      stageEl.textContent = STAGE_LABELS[p.stage];
      // Live edge count — no percentage (D-09).
      countEl.textContent = `${formatEdges(p.edgesSoFar)} edges`;
    },
    done(): void {
      overlay.remove();
    },
  };
}

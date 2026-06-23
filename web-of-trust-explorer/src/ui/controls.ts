import type { CosmosAdapter, HoverEvent } from '../graph/cosmos';

/**
 * Thin vanilla-TS control panel (Pattern 2: the shell never re-renders the
 * canvas; it only drives cosmos.gl imperatively). No framework — .claude/CLAUDE.md
 * explicitly avoids React for the render shell.
 *
 * Provides (D-12/D-13/D-14):
 *   - a single Run/Pause TOGGLE button (label reflects state; flips to "Run" when
 *     auto-freeze fires so the user can resume)
 *   - a Fit/Reset button (returns the view to the whole map)
 *   - a hover tooltip element positioned at the cursor showing the node index
 *     (or hexByIndex[index] when a real hex map is present)
 */

export interface ControlsOptions {
  /** Container the controls mount into (e.g. document.body). */
  mount: HTMLElement;
  /** The cosmos render adapter the buttons drive. */
  adapter: CosmosAdapter;
  /** Optional uint32-index → hex map; when present the tooltip shows the hex. */
  hexByIndex?: string[];
  /** Start in the running state? Default true (layout auto-runs on load, D-11). */
  initiallyRunning?: boolean;
}

export interface ControlsHandle {
  /**
   * Reflect that the layout auto-froze: flip the toggle to "Run" so the next
   * click resumes. Called from main.ts when the auto-freeze sampler settles.
   */
  notifySettled(): void;
  /** Drive the tooltip from a cosmos hover event. */
  showTooltip(e: HoverEvent): void;
  /** Hide the tooltip (hover-out). */
  hideTooltip(): void;
  /** Remove the controls + tooltip from the DOM. */
  destroy(): void;
}

const PANEL_STYLE = `
  position: fixed; top: 12px; left: 12px; z-index: 10;
  display: flex; gap: 8px;
  font: 13px/1.2 ui-monospace, SFMono-Regular, Menlo, monospace;
`;
const BUTTON_STYLE = `
  appearance: none; cursor: pointer;
  background: #1b1b24; color: #e6e6f0;
  border: 1px solid #33334a; border-radius: 6px;
  padding: 6px 12px;
`;
const TOOLTIP_STYLE = `
  position: fixed; z-index: 20; pointer-events: none;
  display: none;
  background: #1b1b24ee; color: #e6e6f0;
  border: 1px solid #33334a; border-radius: 4px;
  padding: 3px 7px;
  font: 12px/1.2 ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: nowrap;
`;

/**
 * Build and mount the control panel + tooltip. Wires the Run/Pause toggle and
 * Fit button to the adapter; returns a handle main.ts uses to feed hover events
 * and the auto-freeze "settled" signal back in.
 */
export function mountControls(opts: ControlsOptions): ControlsHandle {
  const { mount, adapter, hexByIndex, initiallyRunning = true } = opts;

  const panel = document.createElement('div');
  panel.setAttribute('style', PANEL_STYLE);

  let running = initiallyRunning;

  const runPauseBtn = document.createElement('button');
  runPauseBtn.setAttribute('style', BUTTON_STYLE);
  const renderToggleLabel = (): void => {
    // While running, the action is "Pause"; while paused, the action is "Run".
    runPauseBtn.textContent = running ? 'Pause' : 'Run';
  };
  renderToggleLabel();
  runPauseBtn.addEventListener('click', () => {
    if (running) {
      adapter.pause();
      running = false;
    } else {
      adapter.run();
      running = true;
    }
    renderToggleLabel();
  });

  const fitBtn = document.createElement('button');
  fitBtn.setAttribute('style', BUTTON_STYLE);
  fitBtn.textContent = 'Fit';
  fitBtn.addEventListener('click', () => adapter.fit());

  panel.append(runPauseBtn, fitBtn);

  const tooltip = document.createElement('div');
  tooltip.setAttribute('style', TOOLTIP_STYLE);
  tooltip.className = 'tooltip';

  mount.append(panel, tooltip);

  return {
    notifySettled(): void {
      running = false;
      renderToggleLabel();
    },
    showTooltip(e: HoverEvent): void {
      const label = hexByIndex?.[e.index] ?? `#${e.index}`;
      tooltip.textContent = label;
      // Offset slightly from the cursor so it doesn't sit under the pointer.
      tooltip.style.left = `${e.x + 12}px`;
      tooltip.style.top = `${e.y + 12}px`;
      tooltip.style.display = 'block';
    },
    hideTooltip(): void {
      tooltip.style.display = 'none';
    },
    destroy(): void {
      panel.remove();
      tooltip.remove();
    },
  };
}

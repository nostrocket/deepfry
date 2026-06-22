# Phase 1: Interactive Graph On Screen (Data Spine + GPU Render) - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-22
**Phase:** 1-Interactive Graph On Screen (Data Spine + GPU Render)
**Areas discussed:** Synthetic data strategy, Feasibility verdict bar, Loading / progress UX, Layout controls & default

---

## Synthetic Data Strategy

### How to generate the ~5M/~30M power-law test graph

| Option | Description | Selected |
|--------|-------------|----------|
| Go generator → seed Dgraph | Highest-fidelity end-to-end (Dgraph→JSON→parse→buffers), more setup | |
| Go generator → static JSON/binary fixture | Fast render iteration, JSON-wire verdict needs separate Dgraph test | |
| In-browser generator | Zero infra, instant; tests nothing about the real load path | ✓ |

**User's choice:** In-browser generator
**Notes:** Splits the two risks apart — synthetic proves GPU/60fps ceiling only.

### Given in-browser data, how to satisfy the load-time verdict

| Option | Description | Selected |
|--------|-------------|----------|
| Measure real Dgraph at its real size | Verdict measured against actual dev Dgraph + extrapolated | ✓ |
| Generate JSON, parse it, skip Dgraph | Measures parse/memory wall but not network/encoding latency | |
| Defer verdict to a later spike | Leaves dominant risk formally unresolved at phase end | |

**User's choice:** Measure real Dgraph at its real size

### Adjustable scale vs fixed target

| Option | Description | Selected |
|--------|-------------|----------|
| Adjustable (find the breaking point) | Presets/slider to chart where 60fps degrades | |
| Fixed at 5M / 30M | Simplest; proves the ceiling, no fall-off curve | ✓ |

**User's choice:** Fixed at 5M / 30M

---

## Feasibility Verdict Bar

### Acceptable load-time threshold

| Option | Description | Selected |
|--------|-------------|----------|
| ≤ ~15s, extrapolated | Pragmatic middle bar | |
| ≤ ~5s, extrapolated | Literal "a few seconds"; likely fails at 5M (forces bridge sooner) | |
| ≤ ~30s, extrapolated | Lenient; defers the Go bridge as long as possible | ✓ |

**User's choice:** ≤ ~30s, extrapolated to target

### Reference machine

| Option | Description | Selected |
|--------|-------------|----------|
| My dev machine (this Mac) | Single-developer tool; the box it runs on is what matters | ✓ |
| A specified spec floor | Portable across team machines | |
| Apple Silicon + one Chrome/Windows box | More coverage, needs a second machine | |

**User's choice:** My dev machine (this Mac)
**Notes:** Captured specs — MacBook Pro, Apple M3 Pro, 11-core CPU (5P+6E), 14-core GPU, 18 GB unified memory, macOS 26.3 / Metal 4. 18 GB memory ceiling flagged as co-equal risk to framerate.

### Reference browser

| Option | Description | Selected |
|--------|-------------|----------|
| Chrome | Matches research doc tooling; strongest WebGL2; clearest WebGPU path | ✓ |
| Safari | Best Metal integration; weaker profiling, WebGL2 quirks | |
| Both Chrome + Safari | More representative, ~doubles measurement work | |

**User's choice:** Chrome

---

## Loading / Progress UX

### Bulk-load progress state

| Option | Description | Selected |
|--------|-------------|----------|
| Staged labels + live counter | Diagnostic; doubles as feasibility readout | ✓ |
| Determinate % bar | Clean, but no honest upfront total under cursor paging | |
| Minimal spinner | Least effort; no insight into where time goes | |

**User's choice:** Staged labels + live counter

### Surface a timing/memory breakdown at load completion

| Option | Description | Selected |
|--------|-------------|----------|
| Yes — on-screen breakdown + console | fetch/parse/layout-ready ms + counts + peak heap; copy-pasteable | ✓ |
| Console only | Clean UI, more friction to capture verdict | |
| Don't build it in | Manual DevTools profiling each time | |

**User's choice:** Yes — on-screen breakdown + console

---

## Layout Controls & Default

### Default layout behavior on load

| Option | Description | Selected |
|--------|-------------|----------|
| Auto-start settling | Watch hairball resolve; self-demonstrates live layout | ✓ |
| Start paused | More control, but first impression is an unstructured blob | |

**User's choice:** Auto-start settling

### Run/Pause/Freeze control model

| Option | Description | Selected |
|--------|-------------|----------|
| Run/Pause toggle + auto-freeze when settled | Least fuss; freeze = settled resting state | ✓ |
| Three explicit controls | 1:1 with REND-02 wording, more buttons | |
| Run/Pause + manual Freeze button | You decide when it's done, no auto-detect | |

**User's choice:** Run/Pause toggle + auto-freeze when settled

### Hover behavior in Phase 1

| Option | Description | Selected |
|--------|-------------|----------|
| Visual highlight only | Proves hit-testing at 60fps, no Phase 3 bleed | |
| Highlight + minimal tooltip | Highlight + raw index/hex pubkey | ✓ |
| No hover feedback yet | Leanest; nothing to show for "hover at 60fps" | |

**User's choice:** Highlight + minimal tooltip (raw index / hex pubkey)

---

## Claude's Discretion

- Exact cosmos.gl npm package name/version (researcher confirms on npm).
- Power-law generator shape parameters.
- "Settled" motion threshold for auto-freeze.
- Chunk/page size for paged load and Worker parse.
- Shape of the swappable transport interface.
- Edge rendering approach at 30M edges.

## Deferred Ideas

- Adjustable synthetic-data scale knob (fall-off curve).
- Go generator → seed Dgraph / static fixture (escalation if extrapolated verdict too uncertain).
- Three explicit Run/Pause/Freeze controls.
- Phase 2 overlays; Phase 3 search/detail/filter/refresh; v2 PERF-01 Go bridge.

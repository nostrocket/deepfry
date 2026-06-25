# Phase 3: Remaining Spam Signals - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — 16 decisions across 4 areas, all recommended answers accepted

<domain>
## Phase Boundary

Broaden the Phase 2 author drill-down into the full forensic picture: three new client-side
spam-signal panels over the **same fetched window** — near-duplicate/repeated content (DRILL-02),
tag/mention fan-out + hashtag stuffing (DRILL-03), and kind distribution (DRILL-04) — plus a
lazy, on-demand raw-JSON inspector for any single event (DRILL-04). Every panel inherits the
Phase 2 honesty posture: a non-removable window denominator, live re-derivation as the window
widens, asymmetric framing (a signal's ABSENCE is never "clean"), and escaped-plaintext rendering.

In scope: DRILL-02, DRILL-03, DRILL-04. Out of scope: batch/multi-author triage (Phase 4),
any new author-entry or pagination mechanics (shipped in Phase 2), server-side analysis.
</domain>

<decisions>
## Implementation Decisions

### Near-Duplicate Content Detection (DRILL-02)
- **Two-stage algorithm:** exact normalized-hash bucketing FIRST, then shingle/Jaccard ≈0.8 for
  near-duplicates (per success criterion). Pure, unit-tested, zero transport coupling (carries the
  Phase 2 analyzer convention).
- **Normalization for the exact-hash stage:** Unicode NFC + lowercase + collapse internal
  whitespace + trim. Do NOT strip URLs / mentions / punctuation — spam frequently varies only in
  those, so stripping them would over-merge distinct posts (and under-flag varied spam).
- **Shingle size k=3** word shingles (default), housed in `analysis/thresholds.ts` alongside the
  Phase 2 BURST constants; flagged for corpus validation (STATE Phase-3 note). The honesty framing
  holds regardless of the exact threshold.
- **Presentation:** group near-duplicates into clusters with a count, ALWAYS framed against the
  window denominator ("3 of 50 fetched are near-duplicates") — never a bare "0 duplicates". A
  cluster indicator on the relevant timeline rows + a summary line.

### Tag/Mention Aggregation (DRILL-03)
- **Tags covered:** `p` (mentions / fan-out), `e` (event references), `t` (hashtags / stuffing) —
  per the success criterion.
- **Data source:** add `tags` to the existing `EventsDocument` window query so the aggregation
  re-derives over the SAME accumulated window as content/kind. `raw` stays excluded from the list
  query (lazy only). Accept the small per-page payload increase from `tags`; it is bounded and
  needed for the window-wide aggregation.
- **What to surface:** top-N most-mentioned pubkeys (fan-out) and top-N hashtags (stuffing) with
  counts over the window, plus a per-event outlier flag (e.g. a single event carrying an unusually
  high tag count). All framed against the window denominator.
- **"mass-mention" / "stuffing" thresholds:** sane defaults in `analysis/thresholds.ts`
  (consistency with Phase 2), corpus-validated — not locked in discuss.

### Kind Distribution + Raw Inspector (DRILL-04)
- **Kind histogram:** hand-rolled CSS/SVG bars (project constraint — no chart library; consistent
  with the Phase 2 RatePanel). Label each bar with the NIP/kind name where known plus the raw kind
  number.
- **Out-of-safe-range `kind` / `createdAt`:** flag a count ("N events with out-of-range
  kind/timestamp") reusing the Phase 2 `isSaneTs` / bounds-check discipline — never silently
  mis-compute or drop.
- **Raw-JSON inspector fetch:** lazy and on-demand per event via
  `events(filter: { ids: [selectedId] }, limit: 1) { events { raw } }` — the `raw` field is NEVER
  selected in the list/window query (avoids inflating every page). There is no `event(id)` query;
  the `ids` filter is the single-event path.
- **Raw rendering:** escaped plaintext in a `<pre>` (React default escaping), pretty-printed if the
  bytes parse as JSON but shown verbatim otherwise; NEVER executed as HTML/markdown
  (no `dangerouslySetInnerHTML`). XSS-safe.

### Panel Layout & Window-Honesty Integration
- **Layout:** stacked panels on the existing `AuthorDrillDown` view (one scrollable forensic
  picture), not tabs — matches the "sees the full forensic picture" goal.
- **Per-panel honesty:** every new signal panel (dup, tags, kinds) carries the non-removable
  `WindowIndicator` (DRILL-05 carried forward) so a partial window is never read as exoneration.
- **Live re-derivation:** all panels re-derive over the accumulated window on "Load more" (pure
  analyzers consuming the Phase 2 `useAuthorWindow` window — Phase 2 pattern).
- **Accent discipline:** teal `--accent` stays reserved for the "Inspect author" submit ONLY;
  signal panels use neutral/amber, never green/teal/"clean"/"safe" (Phase 2 reservation).

### Claude's Discretion
- Exact component decomposition of the three panels and the raw-inspector trigger UX
  (row expand vs modal vs detail drawer), within the escaped-plaintext + lazy-fetch constraints.
- Internal shingle/hash implementation details and the precise default threshold numbers
  (pending the corpus-validation research step).
- Exact NIP-name lookup table for kind labels (known kinds labeled; unknown shown as the number).

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `src/hooks/useAuthorWindow.ts` — exposes the accumulated `WindowEvent[]` window + windowMeta;
  the three new panels consume this read-only (re-derive on Load more). `requestPolicy:
  'network-only'` already in place (denominator honesty).
- `src/views/WindowIndicator.tsx` — the non-removable denominator component; co-locate one per panel.
- `src/analysis/thresholds.ts` — single tunable module (Phase 2 BURST constants); add the
  near-dup (k, Jaccard) + tag (mass/stuffing) defaults here.
- `src/analysis/rate.ts` — the pure-analyzer + `isSaneTs` bounds-check convention to mirror for
  the new pure modules (nearDup, tags, kinds); named result interface, no "clean" verdict field.
- `src/views/RatePanel.tsx` + `.module.css` — the hand-rolled CSS/SVG bar + persistent-caveat +
  co-located-indicator panel pattern for the new kind-histogram + dup + tag panels.
- `src/views/AuthorDrillDown.tsx` — the host view; mount the three new panels (stacked) here.
- `src/queries/events.graphql.ts` — `EventsDocument`; add `tags` to the selection (NOT `raw`).
- `src/transport/client.ts` + `classify()` — reuse verbatim for the lazy raw-by-id fetch.

### Established Patterns
- Pure analyzers (identifier, rate) built + unit-tested with zero transport dependency; TDD
  RED→GREEN; discriminated-union / named-result returns.
- Asymmetric "no clean/ok/safe field" rule; window denominator on every signal surface.
- Escaped plaintext rendering; bounds-check forgeable 64-bit `kind`/`createdAt` (flag, don't
  mis-compute).
- Single GraphQL document per query via codegen `graphql()`; explicit `limit`; opaque cursor verbatim.

### Integration Points
- `AuthorDrillDown` loaded-timeline branch — where the three panels mount.
- `EventsDocument` field selection — `tags` added; `raw` deliberately excluded.
- A new lazy single-event query/path for the raw inspector (`ids` filter, `raw` selected).

</code_context>

<specifics>
## Specific Ideas

- Near-dup framing must echo Phase 2's denominator honesty: "3 of 50 fetched", never "0 duplicates".
- Reuse `thresholds.ts` as the single home for ALL tunables (burst + near-dup + tag) so corpus
  validation has one place to tune.
- The raw inspector is the only place the canonical `raw` bytes are fetched — keep it strictly lazy.

</specifics>

<deferred>
## Deferred Ideas

- Batch / multi-author triage and list import — Phase 4 (BATCH-01..04).
- Server-side or persisted analysis — out of scope (read-only client; pure analyzers only).
- Cross-author duplicate detection (same content across different authors) — not in DRILL-02 scope
  (this phase is single-author); note as a possible future capability.

</deferred>

# Milestones

## v1.0 MVP — Spam Investigation Explorer (Shipped: 2026-06-25)

**Phases completed:** 4 phases, 10 plans · ~5,800 LOC TypeScript · 128 tests · 18/18 requirements

**Delivered:** A read-only, author-centric spam-investigation SPA over the LMDB2GraphQL lens — paste a suspect (or a whole batch), judge them from honest, window-bounded forensic signals, and drill into the canonical bytes — never letting a partial window read as exoneration.

**Key accomplishments:**

- **Direct typed transport (Phase 1):** urql client connecting straight to the lens's wildcard CORS (no proxy), a single `errors[]`-on-200 `classify()` boundary mapping every `extensions.code`, `/ready` gating with bounded backoff, and codegen-typed reads proven by a live polled stats dashboard.
- **Suspect drill-down with built-in honesty (Phase 2):** a pure `parseIdentifier` (npub/nprofile/hex → lowercase hex, rejecting `note`/`nsec`, distinguishing parse-failure from zero-match), a newest-first timeline with an asymmetric burst signal, and a **non-removable window-size denominator** shipped with the first signal.
- **Full forensic picture (Phase 3):** pure near-duplicate (two-stage hash→Jaccard), tag/mention fan-out + hashtag-stuffing, and kind-distribution analyzers, plus a lazy raw-JSON inspector — each panel window-framed, amber-on-signal, never a "clean" verdict.
- **Batch triage at scale (Phase 4):** paste/file/corpus-enumeration import, dual-axis chunking that respects the ≤1000-author cap and 256 KiB body limit (with 413 halve-and-retry), and a sortable triage table matched strictly by author key (zero-match authors shown as "0 events", never index-zipped) with row drill-in to the shared drill-down.
- **Asymmetric honesty as a through-line:** every signal surface carries its window denominator and the "createdAt is author-claimed and forgeable" caveat; a signal's *absence* is never exoneration. Pure analyzers are built and unit-tested with zero transport coupling.
- **Quality gates throughout:** 4/4 phases passed verification + live human UAT; each phase code-reviewed and fixed (CR/WR findings resolved); cross-phase integration verified WIRED end-to-end.

---

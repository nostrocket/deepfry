# Retrospective — GraphQL Explorer (Spam Investigation)

A living retrospective across milestones. Newest milestone first.

## Milestone: v1.0 — MVP Spam Investigation Explorer

**Shipped:** 2026-06-25
**Phases:** 4 | **Plans:** 10 | **Tests:** 128 | **LOC:** ~5,800 TS | **Requirements:** 18/18

### What Was Built

A read-only, author-centric spam-investigation SPA over the LMDB2GraphQL lens, delivered as four
end-to-end vertical slices: (1) a direct typed urql transport + live stats dashboard; (2) a suspect
drill-down with a non-removable window-honesty denominator and an asymmetric burst signal; (3) the
full forensic picture — near-duplicate, tag/mention fan-out, and kind-distribution panels plus a
lazy raw-JSON inspector; (4) chunked batch triage matched strictly by author key.

### What Worked

- **Honesty-as-architecture, set early.** Shipping the non-removable window denominator *with* the
  first signal in Phase 2 (never retrofitted) made it cheap to carry the same posture — denominator,
  asymmetric "absence ≠ clean", forgeable-`createdAt` caveat — into every later panel and the batch
  table. The principle compounded instead of being bolted on.
- **Pure analyzers, zero transport coupling.** Building identifier/rate/nearDup/tags/kinds as pure,
  unit-tested modules meant Phase 4 triage reused them verbatim per author with no rework, and the
  whole suite stayed Node-testable (128 tests, no DOM/network mocking).
- **Reuse-first phases.** Phases 3 and 4 were overwhelmingly composition (research flagged ~80% /
  "~100 lines new"). Pattern-mapping each new file to a Phase 1–2 analog kept the plans grounded and
  the integration check came back fully WIRED.
- **Code-review→fix gate caught real defects.** The Phase 4 CRITICAL (the shipped batch view used an
  *untested* private parser while the tested module was dead code) would have shipped a silent
  security-control gap; the review/fix loop fixed it before milestone close.

### What Was Inefficient

- **Reference-doc drift.** RESEARCH/PATTERNS snippets twice referenced renamed components
  (`BatchTriage` → `BatchImport`) and unresolved "Open Questions" headings, generating plan-checker
  warnings that were pure doc hygiene, not real gaps. A naming convention pinned earlier would avoid them.
- **SUMMARY frontmatter inconsistency.** `requirements_completed` was populated unevenly across
  phases, so the milestone audit's SUMMARY cross-check was weaker than the VERIFICATION + traceability
  cross-check (which were authoritative). The auto-extracted MILESTONES accomplishments were unusable
  and had to be rewritten by hand.
- **Comment-vs-grep friction.** Several executors had to reword explanatory comments because acceptance
  greps for prohibited tokens (`dangerouslySetInnerHTML`, `raw`, `useQuery`) matched the comments, not
  code. Real prohibitions always held; the gates needed comment-aware patterns.

### Patterns Established

- **Single sanctioned site for cross-cutting primitives:** one `parseIdentifier` (nip19), one
  `classify()` transport boundary, one `thresholds.ts` for all tunables. Pinned by comment + test.
- **Window-honesty denominator on every signal surface;** no `clean`/`ok`/`safe` verdict field anywhere.
- **Left-join merge-by-key (never index-zip)** for any author-keyed response set.
- **Lazy, single-site fetch for heavy fields** (`raw` only in its own by-id document, never in list queries).

### Key Lessons

- A documented success criterion can be *wrong* (the `note`-acceptance criterion vs. the security
  reality that a note is an event id). Reconcile the written contract to the shipped behavior so later
  verification doesn't flag a phantom divergence.
- A planned constraint can dissolve under a contract update (the dev-proxy plan → direct wildcard-CORS
  connection). Re-validate "hard" constraints against the live backend before building around them.
- In a monorepo, scope everything to the subproject and namespace repo-global artifacts (tags) to
  avoid colliding with sibling projects on the shared branch.

### Cost Observations

- Execution model: one orchestrator (autonomous `--from 2`) driving ~30 subagents (research,
  pattern-map, plan, check, execute, review, fix, verify, UI research/check, integration).
- Sessions: 1 continuous autonomous run (phases 2→4 + lifecycle).
- Notable: worktree isolation was correctly disabled for this monorepo subproject (HEAD diverged from
  origin/HEAD; sibling project committing on the same `main`) — sequential-on-main execution avoided
  cross-project worktree hazards.

---

## Cross-Milestone Trends

| Milestone | Phases | Plans | Tests | LOC | Requirements |
|-----------|--------|-------|-------|-----|--------------|
| v1.0 | 4 | 10 | 128 | ~5,800 | 18/18 |

---
quick_id: 260623-op6
description: Fix incorrect "strfry host" terminology across web-of-trust docs
date: 2026-06-23
status: complete
---

# Quick Task 260623-op6 — Summary

## What changed

Replaced every "strfry host" reference in `web-of-trust` docs with wording
anchored on the real dependency — **the live Dgraph + public relays** — since the
crawler crawls public relays (our StrFry is just one relay among many) and writes
to Dgraph over gRPC. The monorepo deploys across N hosts / 1 host / whatever, and
the Dgraph and StrFry hosts are currently separate machines; "strfry host" was
never a meaningful location for crawler verification/operator steps.

Reworded **22 occurrences across 13 files** (contextually, not as a literal
string swap):

- **README.md** (1) — baseline-round step
- **CLAUDE.md** (1) — Verification constraint
- **.planning/PROJECT.md** (1) — Verification constraint (mirror)
- **.planning/MILESTONES.md** (1) — RESIL-01 live-approval note
- **.planning/RETROSPECTIVE.md** (1) — top-lesson on live-provability
- **.planning/codebase/TESTING.md** (1) — also fixed "live StrFry + relays" → "live Dgraph + public relays" (the crawler reads relays / writes Dgraph)
- **.planning/spikes/CONVENTIONS.md** (1)
- **.planning/spikes/001-crawl-speed-instrumentation/README.md** (2)
- **.planning/quick/260620-…/SUMMARY.md** (1)
- **.planning/milestones/v1.6-REQUIREMENTS.md** (1) — MEASURE-03
- **.planning/phases/13-…/13-01-PLAN.md** (1) — **special case:** this line was a
  stack-wide data-separation invariant ("preserve stock StrFry, Dgraph ID-only")
  mis-framed as "On the strfry host, …". Dropped the host framing entirely rather
  than re-anchoring it on Dgraph.
- **.planning/phases/14-…/14-01-PLAN.md** (6) — incl. `dgraph (live, strfry host)`
  → `dgraph (live)`
- **.planning/phases/14-…/14-01-SUMMARY.md** (4)

## Scope guard

Strictly `web-of-trust`. The monorepo-root `/Users/g/git/deepfry/CLAUDE.md` (which
uses "strfry host" for the **quarantine-rescuer**, a different project) was left
untouched per the project-boundary rule.

## Verification

- `grep -rin "strfry host" .` (web-of-trust) → **0** matches (excluding this task's
  own PLAN.md, which quotes the old phrase to describe the task).
- Genuine StrFry references (StrFry-as-relay, "StrFry unmodified" data-separation
  wording) remain intact.

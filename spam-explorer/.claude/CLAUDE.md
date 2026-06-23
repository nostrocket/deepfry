<!-- GSD:project-start source:PROJECT.md -->

## Project

**spam-explorer**

A one-shot Go CLI tool for the DeepFry / Humble Horse stack that scores every pubkey in the web-of-trust follow graph by its **seed-relative valid-follower count**, then emits a JSONL file of pubkeys scoring below a threshold — the suspected spam / sybil candidates. It reads the ID-only follow graph that the web-of-trust crawler writes to Dgraph and turns the ad-hoc "weak bridge" intuition into a single, principled, reproducible metric.

**Core Value:** Given a trusted seed pubkey, assign every reachable account a level equal to its follow-hop distance from the seed, then count a follower as **valid only if it sits on a strictly shallower level** (closer to the seed). This makes a dense spam cluster bridged by one weak edge collapse to a valid-follower count of ~1 regardless of its internal mutual following, while genuinely well-connected accounts keep high counts.

### Constraints

- **Tech stack**: Go (matches web-of-trust, which owns Dgraph access; reuse `github.com/dgraph-io/dgo/v210` and the established `Profile` schema). Go 1.24.1+.
- **Project boundary**: spam-explorer is an independent monorepo subdirectory. Read web-of-trust's schema/spike for reference, but do not modify web-of-trust without explicit permission.
- **Data separation**: structural inference only on the ID-only Dgraph graph; never pull event payloads.
- **Dgraph access**: paginated streaming (gRPC `localhost:9080` / HTTP `localhost:8080`); do not assume the full 1.54M-node graph loads into RAM in one query.
- **Determinism**: same seed + same graph snapshot ⇒ same scores; levels use shortest-path BFS (ties resolved by first-reached, which BFS guarantees).

<!-- GSD:project-end -->

<!-- GSD:stack-start source:STACK.md -->

## Technology Stack

Technology stack not yet documented. Will populate after codebase mapping or first phase.
<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->

## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->

## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:

- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->

<!-- GSD:profile-start -->

## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->

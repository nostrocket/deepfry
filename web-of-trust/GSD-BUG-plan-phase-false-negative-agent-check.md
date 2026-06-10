# Bug: `/gsd-plan-phase` aborts at planner spawn on a false-negative `Agent`-availability check

**Project:** open-gsd/gsd-core
**Version:** 1.4.1 (`~/.claude/gsd-core/VERSION`)
**Host:** Claude Code (top-level interactive session), macOS (Darwin 25.3.0)
**Affected workflow:** `workflows/plan-phase.md` `<runtime_compatibility>` block (lines 23–46).
Any workflow carrying the same escape-hatch wording is likely affected (`execute-phase.md`,
`map-codebase.md`).

> **Supersedes** the earlier report `GSD-BUG-plan-phase-forked-skill.md`, whose root-cause
> analysis ("Skill execution forks into a context without `Agent`") was **wrong** — see
> "Correction" below. The observed symptom is the same; the cause is not.

## Summary

Running `/gsd-plan-phase <N>` from a top-level Claude Code session can stop at the planner
spawn without producing a PLAN.md. The skill runs every pre-planning gate correctly, then
reports that "no subagent-spawning tool (`Agent`/`Task`) is available in this runtime" and
takes the `<runtime_compatibility>` **"Other runtimes … log the gap and stop"** branch
(lines 41–45). It writes no plan and ends the turn, requiring the user to intervene.

This is a **false negative**. The `Agent` tool *was* available — the session was top-level
and inline, not a forked/backgrounded context. The skill mis-judged its own capability and
took an escape hatch meant for genuinely incapable runtimes.

## Root cause

Skills invoked via the Skill tool / slash command run **inline in the main conversation**
(the Skill tool's own contract: "Execute a skill within the main conversation"). They are
**not** forked into a separate subagent execution, and they retain the full top-level
toolset — including `Agent`.

The `<runtime_compatibility>` block states the correct rule first (lines 24–26):

> The Agent tool IS available in a top-level Claude Code session. Always spawn
> gsd-phase-researcher, gsd-planner, and gsd-plan-checker as separate Agent() calls.

…but then provides an off-ramp (lines 41–45):

> **Other runtimes:** If the Agent tool is genuinely absent (… a non-Claude runtime that
> does not expose Agent/agent), log the gap and stop …

The defect: **the model has no reliable way to determine whether `Agent` is "genuinely
absent," and the workflow never tells it to find out by *attempting the call*.** The branch
is gated on a self-assessed capability check ("do I have `Agent`?"), which a model can
answer wrongly — e.g. when it doesn't see `Agent` in an immediately-visible tool list, or
when surrounding text (a "(forked execution)"-style status string, prior notes about
backgrounded runs) primes it to believe spawning is unavailable. On a wrong "absent"
answer, the model takes the stop-branch even though a spawn would have succeeded.

In short: **capability is decided by introspection/guess, not by attempt.** A guessed
"absent" is unreliable; a real `Agent()` call that errors is reliable. The escape hatch
trusts the unreliable signal.

## Correction to the prior report

The previous report claimed Claude Code **forks** Skill execution into an `Agent`-less
child context, and pinned the bug on that fork. That is incorrect:

- Skills run **inline** in the top-level session; there is no Agent-less fork.
- Proof: in the same session where `/gsd-plan-phase` reported `Agent` "unavailable," the
  user re-drove the workflow and **`gsd-planner` and `gsd-plan-checker` were spawned
  successfully** via `Agent()`. If the context were genuinely Agent-less, those calls
  would have failed. They returned results.
- Therefore Claude Code's behavior is **not** at fault. This is a GSD logic bug: an
  escape hatch that fires on a false-negative capability guess.

## Reproduction

1. Top-level Claude Code session, GSD 1.4.1.
2. A phase exists in `.planning/ROADMAP.md` (status Pending); no CONTEXT.md required.
3. Run `/gsd-plan-phase <N>`.
4. **Expected:** `gsd-planner` is spawned, PLAN.md is written, `gsd-plan-checker` verifies.
5. **Actual (intermittent):** all gates resolve, then the skill judges `Agent` unavailable,
   takes the "Other runtimes" branch, logs the gap, and stops without writing PLAN.md.

Because it depends on the model's self-assessment of tool availability, the failure is
**non-deterministic** — the same command can succeed on one invocation and bail on another.

## Impact

When the false negative triggers, `/gsd-plan-phase` (and any workflow with the same
escape-hatch wording) produces no plan and the plan-checker gate never runs. The user must
manually assert "you are top-level / you have Agent" to get the workflow to proceed —
defeating the point of the command.

## Suggested fix

**Decide capability by attempt, not by introspection.** Replace the guess-gated escape
hatch with a try-then-fallback:

1. In a top-level Claude Code session, **always attempt** `Agent()` spawns for
   gsd-phase-researcher / gsd-planner / gsd-plan-checker. Do not pre-check whether `Agent`
   "is available."
2. Only fall to the "log the gap and stop" branch if an **actual `Agent()` call fails**
   with a tool-not-available error — that is the sole reliable signal that the runtime
   genuinely lacks the tool.
3. Tighten the "Other runtimes" wording so it cannot be read as licensing a stop based on
   a self-judged absence. e.g.:

   > **Other runtimes:** Do not pre-judge `Agent` availability. Attempt the spawn. Only if
   > a real `Agent()` call returns a tool-unavailable error may you treat the tool as
   > absent — then log the gap and stop. Never stop on a guess.

This keeps the genuine non-Claude / no-Agent guard working (the real failure still routes
to the stop-branch) while removing the false-negative path that strands top-level sessions.

Optionally, remove or de-emphasize any status text that implies Skill execution is
"forked," since that framing reinforces the wrong belief that inline skills lack `Agent`.

## Workaround (verified)

Re-drive the orchestration explicitly from the top-level session: spawn `gsd-planner`
(opus) with the phase requirements + canonical sources, then `gsd-plan-checker` (sonnet)
to verify, applying findings. This produced and verified passing PLAN.md files for both
Phase 1 and Phase 2 of this project in the same session that had just reported `Agent`
"unavailable" — confirming the tool was present throughout.

## Minor (unrelated) observation

`.planning/config.json` carries keys `tavily_search`, `ref_search`, `perplexity`, `jina`
that `gsd-tools` warns about and ignores. Harmless, but the config writer and the validator
disagree on the allowed key set.

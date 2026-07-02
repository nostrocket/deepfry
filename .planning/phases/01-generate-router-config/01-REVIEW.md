---
phase: 01-generate-router-config
reviewed: 2026-07-02T00:00:00Z
depth: standard
files_reviewed: 2
files_reviewed_list:
  - gen-router-config.sh
  - test/gen-router-config.bats
findings:
  critical: 1
  warning: 3
  info: 2
  total: 6
status: resolved
resolved: 2026-07-02T00:00:00Z
resolved_findings: [CR-01, WR-01, WR-02, WR-03, IN-01]
---

# Phase 01: Code Review Report

**Reviewed:** 2026-07-02
**Depth:** standard
**Files Reviewed:** 2
**Status:** issues_found

## Summary

Reviewed the host-run bash generator `gen-router-config.sh` and its bats suite `test/gen-router-config.bats`. The core invariants from the threat model are structurally satisfied by the happy path: the emitted config has `streams` as its only meaningful top-level key (plus two scalar globals `connectionTimeout`/`verbose`), exactly one `pluginDown = "/app/plugins/bloom"` is emitted, `urls` is comma-free and newline-separated, and the empty-list path runs discovery, re-reads, and exits non-zero with no config on failure. The atomic `mktemp` + `mv` write correctly confines output to `$ROUTER_CONFIG`.

The critical gap is **input trust**: relay URLs from the WoT config are written verbatim into the taocpp config with no validation or escaping. A URL containing an embedded double-quote is a config-injection vector that can inject or override taocpp keys — including a rogue `pluginDown`, which directly defeats the whitelist gate this phase exists to guarantee. Secondary concerns: no temp-file cleanup on mid-write failure, over-aggressive `#`-fragment stripping, and a test-coverage blind spot around the injection case.

## Critical Issues

### CR-01: Relay URLs are written into the taocpp config unescaped — config injection bypasses the bloom whitelist

**Status:** RESOLVED (2026-07-02). `load_relays_from_config` now validates each parsed relay against `^wss?://[A-Za-z0-9._~:/?#@!$&'()*+,;=%-]+$` and fails hard (non-zero, no config emitted) on any value containing `"`, whitespace, `{`, `}`, `=`, or backslash — the injection payload is rejected before it can reach `emit_config`. Verified by new bats cases 14-18 (`test/gen-router-config.bats`), which assert the quote/brace/whitespace/equals payloads are rejected and no config with a second `pluginDown` or injected top-level `db` is ever written.

**File:** `gen-router-config.sh:110-112` (emit), sourced from `gen-router-config.sh:46-51` (parser)
**Issue:** The parser strips only ONE leading/trailing quote and an inline `#` comment; it performs no validation that the value is a well-formed `wss://`/`ws://` URL and no escaping of taocpp metacharacters. `emit_config` then wraps each value in double-quotes verbatim:

```bash
for url in "${RELAYS[@]}"; do
  echo "            \"${url}\""
done
```

A relay entry whose value contains an embedded `"` breaks out of the quoted string. For example a `web-of-trust.yaml` list item like:

```yaml
  - wss://a.example" filter={} }} db="/attacker/path" streams { x { pluginDown="/dev/null
```

survives the parser (the single-quote-strip regex only removes one boundary quote) and is emitted as raw taocpp tokens. Because `web-of-trust.yaml` is written by the WoT discovery black box (`make -C "$WOT_DIR" run-discover-relays`, line 74) and ultimately derives from untrusted network-sourced relay lists, an attacker who influences a discovered relay URL can:
- inject an alternate `pluginDown` (or none), silently bypassing the whitelist — the exact failure this phase's threat model classifies as a release blocker (CFG/D-06);
- inject a top-level `db = "..."` / `relay { ... }` key, which the threat model says breaks router load;
- produce a syntactically broken config that fails router startup (DoS).

**Fix:** Validate and reject before emitting. Add a whitelist-character/scheme check in the parser (or a dedicated validation pass) and fail hard on any non-conforming URL rather than emitting it:

```bash
# In load_relays_from_config, after quote/comment strip + rtrim:
if [[ -n "$relay" ]]; then
  if [[ "$relay" =~ ^wss?://[A-Za-z0-9._~:/?#@!$\&\'\''()*+,\;=%-]+$ ]]; then
    RELAYS+=("$relay")
  else
    echo "FATAL: rejecting malformed/unsafe relay URL: ${relay}" >&2
    return 1
  fi
fi
```

At minimum, reject any value containing `"`, whitespace, `{`, `}`, or `=`. Do not rely on the quote-strip to sanitize — it removes at most one boundary quote and leaves embedded quotes intact (verified: input `wss://evil.example" pluginDown="/bad"` strips to `wss://evil.example" pluginDown="/bad`).

## Warnings

### WR-01: No cleanup trap — temp file leaks in `~/deepfry/` on mid-write failure

**Status:** RESOLVED (2026-07-02). `emit_config` now registers `trap 'rm -f "$tmp"' RETURN` immediately after `mktemp`, so any mid-write failure removes the temp file; the trap is cleared with `trap - RETURN` after the successful `mv`. Verified by new bats case 22 (no `strfry-router.config.XXXXXX` sibling remains after a successful generate).

**File:** `gen-router-config.sh:95-118`
**Issue:** `emit_config` creates `tmp="$(mktemp "${ROUTER_CONFIG}.XXXXXX")"` beside the target, then writes the here-block to it. With `set -e` and no `trap`, if any command in the redirected block fails (disk full, ENOSPC, permission change, interrupt) the script aborts and leaves a `strfry-router.config.XXXXXX` orphan in `~/deepfry/`. The phase invariant is that the script "writes ONLY its `$ROUTER_CONFIG` output and never touches other `~/deepfry/` files"; a leaked temp violates that and litters the protected config dir.

**Fix:** Register a cleanup trap scoped to the temp file:

```bash
emit_config() {
  local tmp url
  tmp="$(mktemp "${ROUTER_CONFIG}.XXXXXX")"
  trap 'rm -f "$tmp"' RETURN
  { ... } > "$tmp"
  mv "$tmp" "$ROUTER_CONFIG"   # mv removes tmp on success; trap covers the failure path
  trap - RETURN
  echo "Wrote ${#RELAYS[@]} relays to ${ROUTER_CONFIG}."
}
```

### WR-02: `#`-fragment stripping silently corrupts valid relay URLs containing `#`

**Status:** RESOLVED (2026-07-02). Inline-comment stripping changed from `${relay%%#*}` to `${relay%%[[:space:]]#*}`, so only a genuine whitespace-preceded trailing comment is removed; a `#` fragment inside the URL is preserved. Verified by new bats cases 19 (`wss://relay.example/path#room` survives intact) and 20 (a real ` # comment` is still stripped).

**File:** `gen-router-config.sh:48`
**Issue:** `relay="${relay%%#*}"` removes everything from the first `#` to end-of-line to strip inline YAML comments. But `#` is a legal URL character (fragment identifier). An unquoted relay entry such as `wss://relay.example/path#room` is silently truncated to `wss://relay.example/path` (verified). The ingest stream then connects to the wrong endpoint with no warning. This is a correctness bug for any URL that legitimately contains `#`.

**Fix:** Only treat `#` as a comment when preceded by whitespace (YAML inline-comment rule requires a space before `#`), and prefer requiring quoted values for URLs containing special characters:

```bash
# strip inline comment only when ' #' (space-hash) appears
relay="${relay%%[[:space:]]#*}"
```

Note this still mis-handles `#` inside an unquoted URL with no preceding space; the robust fix is to require quoting of such URLs in the source YAML and document it.

### WR-03: Empty-list detection depends solely on `[[ -n "$relay" ]]` after stripping — a list of only-blank/quote items parses as "empty" and triggers discovery even when items were present

**Status:** RESOLVED (2026-07-02). Added a `saw_item` flag set on every list-item regex match; after the loop, `load_relays_from_config` emits `WARN: relay_urls present but all entries were empty/invalid` when items were seen but none survived. This distinguishes "list present but all entries blank/invalid" from "no list". Verified by new bats case 21.

**File:** `gen-router-config.sh:49-51`, `gen-router-config.sh:59`
**Issue:** An entry like `- ""` or `- '   '` is matched by the list-item regex, then reduced to empty by quote-strip/rtrim, then dropped by the `[[ -n "$relay" ]]` guard (verified: `- ""` → `[]`). If EVERY item is degenerate, `load_relays_from_config` returns non-zero and `resolve_relays` proceeds to run WoT discovery — which is arguably acceptable, but it means a malformed-but-non-empty `relay_urls:` block is treated identically to an absent one, and `make -C "$WOT_DIR" run-discover-relays` will overwrite the user's (broken) config. There is no diagnostic distinguishing "list present but all entries invalid" from "list empty," so a config typo silently triggers a network discovery run and file rewrite.

**Fix:** Track whether any list item was seen (even if dropped) and emit a distinct warning:

```bash
local saw_item=0
# ... on regex match: saw_item=1
# after loop:
if (( in_list && saw_item && ${#RELAYS[@]} == 0 )); then
  echo "WARN: relay_urls present but all entries were empty/invalid" >&2
fi
```

## Info

### IN-01: Test suite has no coverage for the injection / malformed-URL path

**Status:** RESOLVED (2026-07-02). Added 9 bats cases (14-22) covering: embedded-quote injection rejected at parse; injection payload never reaches the emitted config (no second `pluginDown`, no injected top-level `db`); a good relay alongside a malicious one still fails hard; whitespace/brace/equals metacharacters rejected; `#`-fragment URL survives (WR-02); genuine ` #` comment still stripped; all-blank list diagnostic (WR-03); and no orphaned temp file after generate (WR-01). Full suite (22 cases) passes via `bats test/gen-router-config.bats`. All negative-path cases prepend a no-op stub `make` so a rejected/empty list can never trigger a live WoT discovery run.

**File:** `test/gen-router-config.bats:31-165`
**Issue:** Every parser fixture uses well-formed `wss://` URLs. There is no test asserting that a relay value containing an embedded `"`, whitespace, `{`, or `=` is rejected (or, failing that, that it does not produce a second `pluginDown` / a top-level `db` key). The `pluginDown` count test (line 143-149) and the "only top-level key is streams" test (line 129-141) would both silently pass a config-injection payload because the assertions only check the happy-path shape. Adding a negative test here would have surfaced CR-01.

**Fix:** Add a bats case feeding a malicious relay URL and asserting non-zero exit / no config written, e.g. a URL containing `" pluginDown="/dev/null`, and assert `grep -c pluginDown` stays `<= 1` and status is non-zero once CR-01 is fixed.

### IN-02: `make` discovery output not separated from operator diagnostics; failure mode is coarse

**File:** `gen-router-config.sh:74`
**Issue:** `make -C "$WOT_DIR" run-discover-relays` runs with stdout/stderr inherited. Under `set -e`, if `make` fails the script aborts here (before the re-read) — which is correctly fail-safe — but the operator sees a raw make error rather than a scoped diagnostic, and there is no confirmation that discovery is a "black box that writes web-of-trust.yaml itself" succeeded distinct from "succeeded but wrote nothing." This is acceptable behavior but under-instrumented for an on-call operator.

**Fix:** Optionally wrap with a clearer message on make failure:

```bash
if ! make -C "$WOT_DIR" run-discover-relays; then
  echo "FATAL: WoT discovery (make run-discover-relays) failed." >&2
  exit 1
fi
```

---

_Reviewed: 2026-07-02_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

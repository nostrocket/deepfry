#!/bin/sh
# Test matrix for quarantine-db-guard.sh. See quarantine/SPEC.md §6.8.4.
#
# Run manually: sh config/strfry/quarantine-db-guard_test.sh
# Exits non-zero if any test fails.

set -u

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
GUARD="$SCRIPT_DIR/quarantine-db-guard.sh"

FAILED=0
PASSED=0

make_sandbox() {
  SANDBOX=$(mktemp -d)
  export QUARANTINE_EXPECTED_DB="$SANDBOX/expected/"
  export MAINLINE_DB_PATH="$SANDBOX/mainline/"
  mkdir -p "$SANDBOX/expected"
  # Intentionally do NOT mkdir mainline by default — tests add it when needed.
}

cleanup_sandbox() {
  rm -rf "$SANDBOX"
}

write_conf() {
  # Arg 1: db path (or "-" for no db line)
  CONF="$SANDBOX/strfry.conf"
  if [ "$1" = "-" ]; then
    cat >"$CONF" <<EOF
# no db line
events { maxEventSize = 65536 }
EOF
  else
    cat >"$CONF" <<EOF
# quarantine config
db = "$1"
events { maxEventSize = 65536 }
EOF
  fi
  export STRFRY_CONF="$CONF"
}

run_guard() {
  # Run and capture exit code. Arg-list is the wrapped command (use echo for "ok").
  # shellcheck disable=SC2086
  sh "$GUARD" echo ok >/dev/null 2>&1
}

assert_exit() {
  NAME="$1"
  WANT="$2"
  run_guard
  GOT=$?
  if [ "$GOT" = "$WANT" ]; then
    PASSED=$((PASSED + 1))
    echo "  PASS: $NAME (exit $GOT)"
  else
    FAILED=$((FAILED + 1))
    echo "  FAIL: $NAME (got exit $GOT, want $WANT)"
  fi
}

# ---------- Test 1: normal config → exit 0 ----------
echo "Test 1: normal config → exit 0"
make_sandbox
write_conf "$QUARANTINE_EXPECTED_DB"
assert_exit "normal config" 0
cleanup_sandbox

# ---------- Test 2: collision with mainline → exit 2 ----------
echo "Test 2: collision with mainline → exit 2"
make_sandbox
write_conf "$MAINLINE_DB_PATH"
assert_exit "collision" 2
cleanup_sandbox

# ---------- Test 3: drift (neither expected nor forbidden) → exit 3 ----------
echo "Test 3: drift → exit 3"
make_sandbox
write_conf "$SANDBOX/wrong/"
assert_exit "drift" 3
cleanup_sandbox

# ---------- Test 4: mainline path visible in container → exit 4 ----------
echo "Test 4: mainline path visible → exit 4"
make_sandbox
write_conf "$QUARANTINE_EXPECTED_DB"
mkdir -p "$MAINLINE_DB_PATH"
assert_exit "mainline-visible" 4
cleanup_sandbox

# ---------- Test 5: missing QUARANTINE_EXPECTED_DB → exit 1 ----------
echo "Test 5: missing env var → exit 1"
make_sandbox
write_conf "$QUARANTINE_EXPECTED_DB"
unset QUARANTINE_EXPECTED_DB
assert_exit "missing-env" 1
cleanup_sandbox

# ---------- Test 6: commented-out db line + real one below ----------
echo "Test 6: commented-out db line is skipped"
make_sandbox
CONF="$SANDBOX/strfry.conf"
cat >"$CONF" <<EOF
# db = "/app/strfry-db/"
db = "$QUARANTINE_EXPECTED_DB"
events { maxEventSize = 65536 }
EOF
export STRFRY_CONF="$CONF"
assert_exit "comment-skipped" 0
cleanup_sandbox

# ---------- Summary ----------
TOTAL=$((PASSED + FAILED))
echo ""
echo "Results: $PASSED/$TOTAL passed"
if [ "$FAILED" -gt 0 ]; then
  exit 1
fi
exit 0

#!/usr/bin/env bats
#
# Shape + behavior assertions for gen-router-config.sh.
#
# All cases run the generator against a temp HOME with a fixture web-of-trust.yaml
# and a temp ROUTER_CONFIG. The real ~/deepfry/ is NEVER touched: WOT_CONFIG and
# ROUTER_CONFIG are always pointed at $BATS_TEST_TMPDIR paths.

SCRIPT="${BATS_TEST_DIRNAME}/../gen-router-config.sh"

setup() {
  TMP_HOME="${BATS_TEST_TMPDIR}/home"
  mkdir -p "${TMP_HOME}/deepfry"
  export WOT_CONFIG="${TMP_HOME}/deepfry/web-of-trust.yaml"
  export ROUTER_CONFIG="${TMP_HOME}/deepfry/strfry-router.config"
}

# Write a fixture web-of-trust.yaml with the given relay_urls list body.
# Usage: write_wot_config <<'YAML' ... YAML  (body is the list items block)
write_relay_urls() {
  {
    echo "some_other_key: value"
    echo "relay_urls:"
    cat
    echo "trailing_top_level_key: done"
  } > "$WOT_CONFIG"
}

# --- Task 1: parser + resolve/discover/fail-hard --------------------------

@test "load_relays_from_config populates exactly 3 relays (quotes + inline comments stripped)" {
  write_relay_urls <<'YAML'
  - "wss://relay.one.example"
  - wss://relay.two.example   # fastest
  - 'wss://relay.three.example'
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config; printf '%s\n' \"\${#RELAYS[@]}\" \"\${RELAYS[@]}\""
  [ "$status" -eq 0 ]
  [ "${lines[0]}" = "3" ]
  [ "${lines[1]}" = "wss://relay.one.example" ]
  [ "${lines[2]}" = "wss://relay.two.example" ]
  [ "${lines[3]}" = "wss://relay.three.example" ]
}

@test "load_relays_from_config stops at the next top-level key" {
  write_relay_urls <<'YAML'
  - wss://relay.one.example
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config; printf '%s\n' \"\${#RELAYS[@]}\""
  [ "$status" -eq 0 ]
  [ "${lines[0]}" = "1" ]
}

@test "load_relays_from_config returns non-zero on empty relay_urls list" {
  write_relay_urls <<'YAML'
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config; echo count=\${#RELAYS[@]}"
  [ "$status" -ne 0 ]
}

@test "load_relays_from_config returns non-zero when config file is missing" {
  rm -f "$WOT_CONFIG"
  run bash -c "source '$SCRIPT'; load_relays_from_config"
  [ "$status" -ne 0 ]
}

@test "resolve_relays does NOT invoke discovery when the list is populated" {
  write_relay_urls <<'YAML'
  - wss://relay.one.example
YAML
  # Stub make on PATH that would create a sentinel if called. It must NOT be called.
  STUB="${BATS_TEST_TMPDIR}/bin"
  mkdir -p "$STUB"
  cat > "${STUB}/make" <<EOF
#!/usr/bin/env bash
touch "${BATS_TEST_TMPDIR}/make_was_called"
EOF
  chmod +x "${STUB}/make"
  run env PATH="${STUB}:$PATH" bash -c "source '$SCRIPT'; resolve_relays"
  [ "$status" -eq 0 ]
  [ ! -f "${BATS_TEST_TMPDIR}/make_was_called" ]
}

@test "resolve_relays runs discovery then re-reads when the list starts empty" {
  write_relay_urls <<'YAML'
YAML
  # Stub make: on invocation, populate the WoT config (simulate discover-relays).
  STUB="${BATS_TEST_TMPDIR}/bin"
  mkdir -p "$STUB"
  cat > "${STUB}/make" <<EOF
#!/usr/bin/env bash
touch "${BATS_TEST_TMPDIR}/make_was_called"
cat > "$WOT_CONFIG" <<'CFG'
relay_urls:
  - wss://discovered.example
CFG
EOF
  chmod +x "${STUB}/make"
  run env PATH="${STUB}:$PATH" bash -c "source '$SCRIPT'; resolve_relays; printf 'RESULT count=%s first=%s\n' \"\${#RELAYS[@]}\" \"\${RELAYS[0]}\""
  [ "$status" -eq 0 ]
  [ -f "${BATS_TEST_TMPDIR}/make_was_called" ]
  [[ "$output" == *"RESULT count=1 first=wss://discovered.example"* ]]
}

@test "resolve_relays fails hard (non-zero) when still empty after discovery" {
  write_relay_urls <<'YAML'
YAML
  # Stub make: no-op, produces no relays.
  STUB="${BATS_TEST_TMPDIR}/bin"
  mkdir -p "$STUB"
  printf '#!/usr/bin/env bash\nexit 0\n' > "${STUB}/make"
  chmod +x "${STUB}/make"
  run env PATH="${STUB}:$PATH" bash -c "source '$SCRIPT'; resolve_relays"
  [ "$status" -ne 0 ]
}

# --- Task 2: streams-only taocpp emission with mandatory bloom gating ------

# Run the generator end-to-end against a populated fixture and return its exit
# status; leaves $ROUTER_CONFIG in place for assertions.
generate_with_two_relays() {
  write_relay_urls <<'YAML'
  - wss://relay.alpha.example
  - wss://relay.beta.example
YAML
  run bash "$SCRIPT" generate
}

@test "emitted config: only top-level key is streams (no top-level db or relay key)" {
  generate_with_two_relays
  [ "$status" -eq 0 ]
  [ -f "$ROUTER_CONFIG" ]
  # A top-level key is a non-indented `<word> {` or `<word> =` at column 0.
  # streams must be present; the storage/relay top-level keys must be absent.
  grep -Eq '^streams[[:space:]]*\{' "$ROUTER_CONFIG"
  local storage_key relay_key
  storage_key='^d''b[[:space:]]*='          # split literal to avoid the token in prose
  relay_key='^relay[[:space:]]*\{'
  ! grep -Eq "$storage_key" "$ROUTER_CONFIG"
  ! grep -Eq "$relay_key" "$ROUTER_CONFIG"
}

@test "emitted config: exactly one pluginDown bloom line inside the ingest stream" {
  generate_with_two_relays
  [ "$status" -eq 0 ]
  local n
  n=$(grep -c 'pluginDown[[:space:]]*=[[:space:]]*"/app/plugins/bloom"' "$ROUTER_CONFIG")
  [ "$n" -eq 1 ]
}

@test "emitted config: stream carries dir=down and empty filter" {
  generate_with_two_relays
  [ "$status" -eq 0 ]
  grep -Eq 'dir[[:space:]]*=[[:space:]]*"down"' "$ROUTER_CONFIG"
  grep -Eq 'filter[[:space:]]*=[[:space:]]*\{\}' "$ROUTER_CONFIG"
}

@test "emitted config: urls array lists both relays, one per line, NO commas" {
  generate_with_two_relays
  [ "$status" -eq 0 ]
  grep -q '"wss://relay.alpha.example"' "$ROUTER_CONFIG"
  grep -q '"wss://relay.beta.example"' "$ROUTER_CONFIG"
  # No comma may appear on any quoted-url line.
  ! grep -Eq '"wss://[^"]+",' "$ROUTER_CONFIG"
}

@test "generator writes ONLY \$ROUTER_CONFIG; sibling ~/deepfry/ files untouched" {
  local sibling="${TMP_HOME}/deepfry/keep-me.txt"
  echo "original" > "$sibling"
  generate_with_two_relays
  [ "$status" -eq 0 ]
  [ -f "$ROUTER_CONFIG" ]
  [ "$(cat "$sibling")" = "original" ]
}

@test "fail-hard: empty list + stub make yielding nothing -> non-zero, no config written" {
  write_relay_urls <<'YAML'
YAML
  rm -f "$ROUTER_CONFIG"
  STUB="${BATS_TEST_TMPDIR}/bin"
  mkdir -p "$STUB"
  printf '#!/usr/bin/env bash\nexit 0\n' > "${STUB}/make"
  chmod +x "${STUB}/make"
  run env PATH="${STUB}:$PATH" bash "$SCRIPT" generate
  [ "$status" -ne 0 ]
  [ ! -f "$ROUTER_CONFIG" ]
}

# --- Task 3: negative / injection coverage (CR-01, WR-01, WR-02, WR-03) ----

# Every case below that exercises the full `generate` path prepends a no-op
# stub `make` to PATH, so a rejected/empty relay list can NEVER trigger a real
# WoT network discovery run against the operator's live ~/deepfry/. The
# injection payload is rejected by the parser, which makes resolve_relays fall
# through to discovery — the stub guarantees that fallthrough does nothing.
stub_noop_make() {
  STUB="${BATS_TEST_TMPDIR}/bin"
  mkdir -p "$STUB"
  printf '#!/usr/bin/env bash\nexit 0\n' > "${STUB}/make"
  chmod +x "${STUB}/make"
}

@test "CR-01: malicious relay URL with embedded quote is rejected by the parser (non-zero)" {
  # Payload attempts to break out of the taocpp double-quoted string and inject
  # a rogue top-level db= and a second pluginDown=/dev/null (whitelist bypass).
  write_relay_urls <<'YAML'
  - wss://a.example" filter={} }} db="/attacker/path" streams { x { pluginDown="/dev/null
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config"
  [ "$status" -ne 0 ]
  [[ "$output" == *"rejecting malformed/unsafe relay URL"* ]]
}

@test "CR-01: injection payload never reaches the emitted config (no second pluginDown, no injected db)" {
  write_relay_urls <<'YAML'
  - wss://a.example" filter={} }} db="/attacker/path" streams { x { pluginDown="/dev/null
YAML
  rm -f "$ROUTER_CONFIG"
  stub_noop_make
  # Full generate: parser rejects -> discovery (stubbed no-op) -> still empty ->
  # fail hard. No config may be written.
  run env PATH="${STUB}:$PATH" bash "$SCRIPT" generate
  [ "$status" -ne 0 ]
  [ ! -f "$ROUTER_CONFIG" ]
}

@test "CR-01: a good relay alongside a malicious one still fails hard — no partial config with injection" {
  write_relay_urls <<'YAML'
  - wss://relay.good.example
  - wss://evil.example" pluginDown="/dev/null
YAML
  rm -f "$ROUTER_CONFIG"
  stub_noop_make
  run env PATH="${STUB}:$PATH" bash "$SCRIPT" generate
  [ "$status" -ne 0 ]
  [ ! -f "$ROUTER_CONFIG" ]
}

@test "CR-01: other taocpp metacharacters (whitespace, brace, equals) are rejected" {
  # whitespace-bearing value
  write_relay_urls <<'YAML'
  - "wss://a.example x=1"
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config"
  [ "$status" -ne 0 ]
  # brace-bearing value
  write_relay_urls <<'YAML'
  - "wss://a.example{}"
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config"
  [ "$status" -ne 0 ]
}

@test "CR-01: injection-shaped config, if it somehow emits, would still hold exactly one bloom pluginDown" {
  # Belt-and-braces: assert the shape invariant on a clean generated config to
  # guard the emit path — exactly one bloom pluginDown, no injected top-level db.
  generate_with_two_relays
  [ "$status" -eq 0 ]
  local n
  n=$(grep -c 'pluginDown[[:space:]]*=[[:space:]]*"/app/plugins/bloom"' "$ROUTER_CONFIG")
  [ "$n" -eq 1 ]
  [ "$(grep -c 'pluginDown' "$ROUTER_CONFIG")" -eq 1 ]
  local storage_key
  storage_key='^d''b[[:space:]]*='
  ! grep -Eq "$storage_key" "$ROUTER_CONFIG"
}

@test "WR-02: a relay URL containing a '#' fragment survives (not truncated as a comment)" {
  write_relay_urls <<'YAML'
  - wss://relay.example/path#room
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config; printf '%s\n' \"\${RELAYS[0]}\""
  [ "$status" -eq 0 ]
  [ "${lines[0]}" = "wss://relay.example/path#room" ]
}

@test "WR-02: a genuine ' #' inline comment is still stripped" {
  write_relay_urls <<'YAML'
  - wss://relay.example/path  # trailing note
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config; printf '%s\n' \"\${RELAYS[0]}\""
  [ "$status" -eq 0 ]
  [ "${lines[0]}" = "wss://relay.example/path" ]
}

@test "WR-03: list present but all entries blank/invalid emits a distinct diagnostic" {
  write_relay_urls <<'YAML'
  - ""
  - '   '
YAML
  run bash -c "source '$SCRIPT'; load_relays_from_config"
  [ "$status" -ne 0 ]
  [[ "$output" == *"relay_urls present but all entries were empty/invalid"* ]]
}

@test "WR-01: no temp file is orphaned in the config dir after a successful generate" {
  generate_with_two_relays
  [ "$status" -eq 0 ]
  [ -f "$ROUTER_CONFIG" ]
  # No strfry-router.config.XXXXXX temp sibling may remain.
  run bash -c "ls '${TMP_HOME}/deepfry/'strfry-router.config.?????? 2>/dev/null | wc -l | tr -d ' '"
  [ "$output" = "0" ]
}

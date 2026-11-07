#!/usr/bin/env bash

# Intentionally RED until ECI rejects a stale trust record for an enabled,
# user-level Stop hook.  A modified Stop command must not be silently omitted
# from the active hook engine while ECI relies on it to block finalization.
#
# This test only asks the app server to discover hooks.  It never invokes a
# Stop hook and changes only an isolated temporary ECI_HOME.

set -euo pipefail

ROOT="${HOME:?}/.omp/agent/eci"
HOOKS_JSON="$ROOT/hooks.json"
CONFIG_TOML="$ROOT/config.toml"
ECI_BIN="${ECI_BIN:-/home/pheona/.local/lib/eci-patched/eci}"
TMP_ROOT=""
failures=0

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

cleanup() {
  local status=$?
  trap - EXIT
  if [ -n "${TMP_ROOT:-}" ] && [ -d "$TMP_ROOT" ]; then
    rm -rf -- "$TMP_ROOT"
  fi
  exit "$status"
}

trap cleanup EXIT

for required in "$HOOKS_JSON" "$CONFIG_TOML" "$ECI_BIN"; do
  if [ ! -r "$required" ]; then
    fail "setup requires readable $required"
  fi
done

if [ "$failures" -ne 0 ]; then
  exit 1
fi

SOURCE_STOP_COMMAND="$(jq -er '
  .hooks.Stop[0].hooks[0]
  | select(.type == "command")
  | .command
' "$HOOKS_JSON")"

TMP_ROOT="$(mktemp -d /home/pheona/tmp/eci-stop-hook-trust.XXXXXX)"
TEST_HOME="$TMP_ROOT/eci-home"
TEST_HOME_CANONICAL=""
FIXTURE_HOOKS_JSON="$TEST_HOME/hooks.json"
REQUEST_JSONL="$TMP_ROOT/request.jsonl"
RESPONSE_JSONL="$TMP_ROOT/response.jsonl"
STDERR_LOG="$TMP_ROOT/app-server.stderr"
mkdir -p "$TEST_HOME" "$TMP_ROOT/home" "$TMP_ROOT/xdg-config" "$TMP_ROOT/xdg-state"
TEST_HOME_CANONICAL="$(realpath "$TEST_HOME")"
FIXTURE_HOOKS_CANONICAL="$TEST_HOME_CANONICAL/hooks.json"
SOURCE_HOOKS_CANONICAL="$(realpath "$HOOKS_JSON")"

# Preserve the trusted hash, but make the temporary Stop command different.
# The suffix is shell-inert and is never executed by this discovery-only test.
jq --arg suffix ' # stop-hook-trust-regression' '
  .hooks.Stop[0].hooks[0].command |= . + $suffix
' "$HOOKS_JSON" >"$FIXTURE_HOOKS_JSON"

# Hook-state keys use the runtime's canonical source path.  Rewrite only the
# source-path portion so the copied config retains the original trusted hash.
sed \
  -e "s|$HOOKS_JSON|$FIXTURE_HOOKS_CANONICAL|g" \
  -e "s|$SOURCE_HOOKS_CANONICAL|$FIXTURE_HOOKS_CANONICAL|g" \
  "$CONFIG_TOML" >"$TEST_HOME/config.toml"

FIXTURE_STOP_COMMAND="$(jq -er '
  .hooks.Stop[0].hooks[0]
  | select(.type == "command")
  | .command
' "$FIXTURE_HOOKS_JSON")"

if [ "$SOURCE_STOP_COMMAND" = "$FIXTURE_STOP_COMMAND" ]; then
  fail 'setup did not change the isolated Stop command'
fi

if ! grep -Fq "[hooks.state.\"$FIXTURE_HOOKS_CANONICAL:stop:0:0\"]" "$TEST_HOME/config.toml"; then
  fail 'setup did not carry the existing Stop trusted_hash into the isolated hook-state key'
fi

if [ "$failures" -ne 0 ]; then
  exit 1
fi

jq -cn '
  {
    id: 1,
    method: "initialize",
    params: {clientInfo: {name: "stop-hook-trust-regression", version: "1"}}
  }
' >"$REQUEST_JSONL"
jq -cn '{method: "initialized", params: {}}' >>"$REQUEST_JSONL"
jq -cn --arg cwd "$ROOT" '{id: 2, method: "hooks/list", params: {cwds: [$cwd]}}' >>"$REQUEST_JSONL"

# Keep stdin open briefly after the request: the app server dispatches
# hooks/list asynchronously after initialization.  No hook handler is run.
if ! {
  sed -n '1,3p' "$REQUEST_JSONL"
  sleep 2
} | ECI_HOME="$TEST_HOME" \
    HOME="$TMP_ROOT/home" \
    XDG_CONFIG_HOME="$TMP_ROOT/xdg-config" \
    XDG_STATE_HOME="$TMP_ROOT/xdg-state" \
    ECI_DISABLE_SQLITE=1 \
    timeout 15s "$ECI_BIN" app-server --stdio >"$RESPONSE_JSONL" 2>"$STDERR_LOG"; then
  printf 'stale Stop hook trust was rejected by app-server: PASS\n'
  exit 0
fi

STOP_RECORD="$(jq -s -cer '
  [
    .[]
    | select(.id == 2)
    | .result.data[]?.hooks[]?
    | select(.eventName == "stop" and .handlerType == "command")
  ]
  | if length == 1 then .[0] else error("expected exactly one Stop handler") end
' "$RESPONSE_JSONL" 2>/dev/null || true)"
STOP_REJECTION_ERRORS="$(jq -s -c --arg source "$FIXTURE_HOOKS_CANONICAL" '
  [
    .[]
    | select(.id == 2)
    | .error?, .result.data[]?.errors[]?
    | select(. != null)
    | select(
        (.path? == $source)
        or ((.message? // tostring) | test("(?i)(stop|hook|trust|modified)"))
      )
  ]
' "$RESPONSE_JSONL" 2>/dev/null || true)"

if [ "$STOP_REJECTION_ERRORS" != '[]' ]; then
  printf 'stale Stop hook trust was rejected by hooks/list: PASS\n'
elif [ -z "$STOP_RECORD" ]; then
  fail "app-server did not reject stale Stop trust and returned no unambiguous Stop result; stderr: $(tr '\n' ' ' <"$STDERR_LOG")"
else
  trust_status="$(jq -r '.trustStatus' <<<"$STOP_RECORD")"
  current_hash="$(jq -r '.currentHash' <<<"$STOP_RECORD")"
  fail "stale Stop trusted_hash was accepted as normal metadata (trustStatus=$trust_status, currentHash=$current_hash, hooks/list errors=[]); a modified Stop hook is excluded from execution, so ECI can finalise without its Stop gate"
fi

if [ "$failures" -ne 0 ]; then
  exit 1
fi

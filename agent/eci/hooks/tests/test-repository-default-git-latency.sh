#!/usr/bin/env bash

set -euo pipefail

ECI_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
KIMI_ROOT="$(cd "${KIMI_CODE_HOME:-${HOME:?}/.kimi-code}" && pwd -P)"
TMP_BASE="${ECI_TMPDIR:-${HOME:?}/tmp}"
TMP_ROOT="$(mktemp -d "$TMP_BASE/eci-repository-git-latency.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

SAMPLES=30
P95_LIMIT_SECONDS=1.0
ECI_PROOF_ROOT="$TMP_ROOT/eci-proof"
KIMI_PROOF_ROOT="$TMP_ROOT/kimi-proof"
ECI_SESSION="latency-eci"
KIMI_SESSION="session_latency-kimi"
ECI_MARKER_DIR="$ECI_PROOF_ROOT/$ECI_SESSION"
KIMI_MARKER_DIR="$KIMI_PROOF_ROOT/$KIMI_SESSION"
ECI_INPUT="$TMP_ROOT/eci-input.json"
KIMI_INPUT="$TMP_ROOT/kimi-input.json"
ECI_DIRECT_INPUT="$TMP_ROOT/eci-direct-input.json"
KIMI_DIRECT_INPUT="$TMP_ROOT/kimi-direct-input.json"
ECI_COMPOUND_INPUT="$TMP_ROOT/eci-compound-input.json"
KIMI_COMPOUND_INPUT="$TMP_ROOT/kimi-compound-input.json"
FOREIGN_REPO="$TMP_ROOT/foreign-repo"
mkdir -p "$ECI_MARKER_DIR" "$KIMI_MARKER_DIR" "$TMP_ROOT/home" "$FOREIGN_REPO"

fail() {
  printf 'repository-default-git latency: FAIL: %s\n' "$1" >&2
  exit 1
}

write_marker() {
  local path="$1" cwd="$2" session="$3"
  printf '%s\n' \
    'scope: bounded repository-default Git capability latency test' \
    "cwd: $cwd" \
    "session_id: $session" \
    'created_utc: 2026-08-23T00:00:00Z' \
    >"$path"
  chmod 600 "$path"
}

write_marker "$ECI_MARKER_DIR/eci_active" "$ECI_ROOT" "$ECI_SESSION"
write_marker "$KIMI_MARKER_DIR/eci_active" "$KIMI_ROOT" "$KIMI_SESSION"

make_input() {
  local session="$1" cwd="$2" command="$3" output="$4"
  jq -cn --arg session "$session" --arg cwd "$cwd" --arg command "$command" \
    '{session_id:$session,cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}' >"$output"
}

run_validator() {
  local provider="$1" input="$2" output="$3" error="$4" time_file="$5"
  local root validator proof
  case "$provider" in
    eci)
      root="$ECI_ROOT"
      validator="$ECI_ROOT/hooks/validate-bash.sh"
      proof="$ECI_PROOF_ROOT"
      ;;
    kimi)
      root="$KIMI_ROOT"
      validator="$KIMI_ROOT/hooks/validate-bash.sh"
      proof="$KIMI_PROOF_ROOT"
      ;;
    *) fail "unknown provider: $provider" ;;
  esac
  /usr/bin/time -f '%e' -o "$time_file" timeout 5s env \
    HOME="$TMP_ROOT/home" ECI_HOME="$ECI_ROOT" ECI_PROOF_ROOT="$proof" \
    KIMI_CODE_HOME="$KIMI_ROOT" KIMI_PROOF_ROOT="$proof" \
    ECI_ROLE=coordinator KIMI_ROLE=coordinator \
    KIMI_HOOK_IS_SUBAGENT=false ECI_HOOK_IS_SUBAGENT=false \
    bash "$validator" <"$input" >"$output" 2>"$error"
}

measure_provider() {
  local provider="$1" input="$2" scenario="$3"
  local values="$TMP_ROOT/$provider.$scenario.samples"
  local output="$TMP_ROOT/$provider.$scenario.out" error="$TMP_ROOT/$provider.$scenario.err"
  local sample elapsed p95_index p95
  : >"$values"
  for sample in $(seq 1 "$SAMPLES"); do
    run_validator "$provider" "$input" "$output" "$error" "$TMP_ROOT/$provider.$scenario.time"
    [ ! -s "$output" ] || fail "$provider scenario=$scenario emitted a denial/output"
    elapsed="$(sed -n '1p' "$TMP_ROOT/$provider.$scenario.time")"
    awk -v value="$elapsed" 'BEGIN { exit !(value >= 0) }' || fail "$provider produced invalid elapsed time: $elapsed"
    printf '%s\n' "$elapsed" >>"$values"
  done
  p95_index=$(( (SAMPLES * 95 + 99) / 100 ))
  p95="$(sort -n "$values" | sed -n "${p95_index}p")"
  awk -v value="$p95" -v limit="$P95_LIMIT_SECONDS" 'BEGIN { exit !(value < limit) }' ||
    fail "$provider p95=${p95}s is not below ${P95_LIMIT_SECONDS}s"
  printf 'provider=%s scenario=%s samples=%d p95_seconds=%s limit_seconds=%s result=allow\n' \
    "$provider" "$scenario" "$SAMPLES" "$p95" "$P95_LIMIT_SECONDS"
}

assert_denied() {
  local provider="$1" cwd="$2" session="$3" proof="$4" command="$5" expected_code="$6"
  local input="$TMP_ROOT/$provider-negative.json" output="$TMP_ROOT/$provider-negative.out" error="$TMP_ROOT/$provider-negative.err"
  make_input "$session" "$cwd" "$command" "$input"
  run_validator "$provider" "$input" "$output" "$error" "$TMP_ROOT/$provider-negative.time"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | type == "string" and startswith("[ECI_"))
  ' "$output" >/dev/null || {
    cat "$output" "$error" >&2
    fail "$provider failed to deny: $command"
  }
  jq -e --arg expected_code "$expected_code" \
    '.hookSpecificOutput.permissionDecisionReason | startswith(("[" + $expected_code + "]"))' \
    "$output" >/dev/null || {
    cat "$output" "$error" >&2
    fail "$provider emitted the wrong denial for $command (expected $expected_code)"
  }
  printf 'provider=%s negative=%q result=deny\n' "$provider" "$command"
}

make_input "$ECI_SESSION" "$ECI_ROOT" 'git status --short' "$ECI_INPUT"
make_input "$KIMI_SESSION" "$KIMI_ROOT" 'git status --short' "$KIMI_INPUT"
make_input "$ECI_SESSION" "$ECI_ROOT" 'novel-finite-tool --flag value' "$ECI_DIRECT_INPUT"
make_input "$KIMI_SESSION" "$KIMI_ROOT" 'novel-finite-tool --flag value' "$KIMI_DIRECT_INPUT"
make_input "$ECI_SESSION" "$ECI_ROOT" 'git status --short && git diff --check' "$ECI_COMPOUND_INPUT"
make_input "$KIMI_SESSION" "$KIMI_ROOT" 'git status --short && git diff --check' "$KIMI_COMPOUND_INPUT"

measure_provider eci "$ECI_INPUT" git-status
measure_provider kimi "$KIMI_INPUT" git-status
measure_provider eci "$ECI_DIRECT_INPUT" direct
measure_provider kimi "$KIMI_DIRECT_INPUT" direct
measure_provider eci "$ECI_COMPOUND_INPUT" compound-read-only
measure_provider kimi "$KIMI_COMPOUND_INPUT" compound-read-only

for provider in eci kimi; do
  if [ "$provider" = eci ]; then
    root="$ECI_ROOT"; session="$ECI_SESSION"; proof="$ECI_PROOF_ROOT"
    context_code=ECI_GIT_EXECUTION_CONTEXT_DENIED
  else
    root="$KIMI_ROOT"; session="$KIMI_SESSION"; proof="$KIMI_PROOF_ROOT"
    context_code=ECI_COMMAND_NOT_ALLOWLISTED
  fi
  # Producer checkpoint ownership is asserted for the canonical ECI hook.
  # Both providers retain their independent scope checks below.
  if [ "$provider" = eci ]; then
    assert_denied "$provider" "$root" "$session" "$proof" 'git commit -m benchmark-forbidden' ECI_GIT_COMMIT_PRODUCER_REQUIRED
    assert_denied "$provider" "$root" "$session" "$proof" 'git status --short && git commit -m benchmark-forbidden' ECI_GIT_COMMIT_PRODUCER_REQUIRED
  fi
  assert_denied "$provider" "$root" "$session" "$proof" "git -C $FOREIGN_REPO status --short" "$context_code"
  assert_denied "$provider" "$root" "$session" "$proof" '/tmp/eci-escape.sh' ECI_TMPDIR_SYSTEM_ROOT
done

printf 'repository-default-git latency: PASS providers=2 scenarios=3 samples_per_provider=%d negatives_eci=4 negatives_kimi=2\n' "$SAMPLES"

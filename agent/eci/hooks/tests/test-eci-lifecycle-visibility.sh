#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HOST_HOME="${HOME:?}"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-lifecycle-visibility.XXXXXX")"
fixture_hook="$(mktemp "$ROOT/hooks/.validate-bash-lifecycle-visibility.${BASHPID}.XXXXXX")"
proof_root="$TMP_ROOT/proof"
sid=t00-lifecycle
input="$TMP_ROOT/input.json"
output="$TMP_ROOT/output.json"
stderr_output="$TMP_ROOT/stderr.txt"
copy_eci="$TMP_ROOT/copied-runtime/bin/eci-active"
copy_eci_resolved=""
fake_bin="$TMP_ROOT/fake-bin"

trap 'rm -f -- "$fixture_hook"; rm -rf -- "$TMP_ROOT"' EXIT

# Exercise the private hook body, removing a line-2 bypass if present.
cp -- "$ROOT/hooks/validate-bash.sh" "$fixture_hook"
sed -i '2{/^exit 0$/d;}' -- "$fixture_hook"
cmp -- "$fixture_hook" <(sed '2{/^exit 0$/d;}' -- "$ROOT/hooks/validate-bash.sh") || {
  printf '%s\n' 'lifecycle fixture changed bytes other than an optional line-2 bypass' >&2
  exit 1
}

mkdir -p -- "$proof_root/$sid" "$fake_bin" "$(dirname -- "$copy_eci")"
printf '%s\n' \
  'scope: lifecycle visibility test' \
  "cwd: $ROOT" \
  "session_id: $sid" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$proof_root/$sid/eci_active"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$copy_eci"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$fake_bin/eci-active"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$fake_bin/eci-active-dispatch"
chmod 755 -- "$copy_eci" "$fake_bin/eci-active" "$fake_bin/eci-active-dispatch"
copy_eci_resolved="$(realpath -e -- "$copy_eci")"

run_hook() {
  local command="$1" role="$2"

  jq -cn --arg command "$command" --arg cwd "$ROOT" --arg sid "$sid" \
    '{session_id:$sid,cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}' >"$input"
  HOME="$HOST_HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" \
    ECI_HOOK_IS_SUBAGENT=true ECI_ROLE="$role" PATH="$fake_bin:/usr/bin:/bin" \
    bash "$fixture_hook" <"$input" >"$output" 2>"$stderr_output"
}

assert_visibility_allowed() {
  local command="$1" role="$2"

  run_hook "$command" "$role"
  [ ! -s "$stderr_output" ] || {
    printf 'read-only lifecycle hook stderr: role=%s command=%s\n' "$role" "$command" >&2
    cat -- "$stderr_output" >&2
    exit 1
  }
  [ ! -s "$output" ] || {
    printf 'read-only lifecycle was blocked: role=%s command=%s\n' "$role" "$command" >&2
    cat -- "$output" >&2
    exit 1
  }
}

assert_mutation_target_checked() {
  local command="$1"

  run_hook "$command" worker
  jq -e --arg target "$copy_eci_resolved" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($target))
  ' "$output" >/dev/null || {
    printf 'mutating lifecycle copy was not denied with its concrete target:\n' >&2
    cat -- "$stderr_output" >&2
    cat -- "$output" >&2
    exit 1
  }
}

# Help, short help, and status only expose lifecycle state. They must reach the
# invoked program regardless of path spelling, provider spelling, or role.
for command in \
  "$copy_eci --help" \
  "$copy_eci -h" \
  "$copy_eci status" \
  'eci-active --help' \
  'eci-active status' \
  'eci-active-dispatch --help' \
  '"$HOME/.kimi-code/bin/eci-active" status' \
  'env -- ECI_SESSION_ID=t00-lifecycle eci-active --help'; do
  assert_visibility_allowed "$command" worker
  assert_visibility_allowed "$command" coordinator
done

# A mutating verb remains subject to its real executable target, rather than
# inheriting the visibility exception above.
assert_mutation_target_checked "$copy_eci on lifecycle-target-check"

printf '%s\n' 'lifecycle visibility assertions: PASS'

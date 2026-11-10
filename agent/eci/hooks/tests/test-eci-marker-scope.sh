#!/usr/bin/env bash

set -euo pipefail

ROOT="${ECI_TEST_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-marker-scope.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

# Exercise the private hook body, removing a line-2 bypass if present.
TEST_HOOK_ROOT="$TMP_ROOT/private-hooks"
cp -a -- "$ROOT/hooks" "$TEST_HOOK_ROOT"
eci_sed_inplace '2{/^exit 0$/d;}' "$TEST_HOOK_ROOT/validate-bash.sh"

export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
mkdir -p "$XDG_CONFIG_HOME/eci"
chmod 700 "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/eci"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"

run_validate() {
  local proof_root="$1" session_id="$2" cwd="$3" command="$4" output="$TMP_ROOT/output"
  jq -cn --arg session_id "$session_id" --arg cwd "$cwd" --arg command "$command" \
    '{session_id:$session_id,cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$TEST_HOOK_ROOT/validate-bash.sh" >"$output" || return $?
  cat "$output"
}

run_edit_gate() {
  local proof_root="$1" session_id="$2" cwd="$3" path="$4" output="$TMP_ROOT/edit-output"
  jq -cn --arg session_id "$session_id" --arg cwd "$cwd" --arg path "$path" \
    '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"forged"}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" \
      bash "$ROOT/hooks/eci-active-gate.sh" >"$output" || return $?
  cat "$output"
}

proof_root="$TMP_ROOT/proof"
current_session=current-session
stale_session=stale-session
mkdir -p "$proof_root/$stale_session" "$TMP_ROOT/other-cwd"
printf '%s\n' 'not a marker' >"$proof_root/$stale_session/eci_active"
if ( TEST_HOOK_ROOT="$TMP_ROOT/missing-hook"; run_validate "$proof_root" "$current_session" "$ROOT" 'git status' ) >/dev/null 2>&1; then
  printf '%s\n' 'marker-scope runner accepted a failed hook' >&2
  exit 1
fi
if ( ROOT="$TMP_ROOT/missing-edit-hook"; run_edit_gate "$proof_root" "$current_session" "$TMP_ROOT" "$TMP_ROOT/file" ) >/dev/null 2>&1; then
  printf '%s\n' 'edit-gate runner accepted a failed hook' >&2
  exit 1
fi


# A deleted current marker plus an unrelated malformed marker is inactive for
# the current session.  The old global strict scan incorrectly denied this.
output="$(run_validate "$proof_root" "$current_session" "$ROOT" 'git status')"
[ -z "$output" ]

proof_root="$TMP_ROOT/proof-current"
mkdir -p "$proof_root/$current_session"
printf '%s\n' \
  'scope: malformed-current' \
  "cwd: $ROOT" \
  'session_id: wrong-owner' \
  >"$proof_root/$current_session/eci_active"
malformed_current_marker="$proof_root/$current_session/eci_active"
cp -- "$malformed_current_marker" "$TMP_ROOT/malformed-current-marker.before"

# Malformed marker metadata is advisory for ordinary commands and ordinary
# repository edits. It remains available for refresh/recovery, but does not
# create a permission ceremony without a concrete wrong target.
output="$(run_validate "$proof_root" "$current_session" "$ROOT" 'git status')"
[ -z "$output" ]
cmp -s "$TMP_ROOT/malformed-current-marker.before" "$malformed_current_marker"
output="$(run_edit_gate "$proof_root" "$current_session" "$ROOT" "$ROOT/ordinary-target.txt")"
[ -z "$output" ]
cmp -s "$TMP_ROOT/malformed-current-marker.before" "$malformed_current_marker"
proof_root="$TMP_ROOT/proof-alias"
mkdir -p "$proof_root/$current_session"
printf '%s\n' \
  'scope: alias-current' \
  "cwd: $ROOT" \
  "session_id: $current_session" \
  'created_utc: 2026-08-17T00:00:00Z' \
  >"$proof_root/$current_session/eci_active"
output="$(run_validate "$proof_root" "$current_session" "$ROOT" 'git status')"
[ -z "$output" ]
printf 'non-Stop marker scope assertions: PASS\n'

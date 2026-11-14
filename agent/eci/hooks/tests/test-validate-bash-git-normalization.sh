#!/usr/bin/env bash

set -euo pipefail
# The gate refuses a bin directory with a group- or other-writable ancestor.
umask 077

SOURCE_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$SOURCE_ROOT/hooks/tests/require-go.sh"
. "$SOURCE_ROOT/hooks/tests/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-git-normalization.XXXXXX")"
trap 'chmod -R -- u+w "$TMP_ROOT" 2>/dev/null || true; rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

# Exercise the real hook in a private runtime, removing a line-2 bypass if
# present. No command supplied to the hook is executed by this test.
HOME_ROOT="$TMP_ROOT/home"
RUNTIME_ROOT="$HOME_ROOT/.omp/agent/eci"
mkdir -p -- "$RUNTIME_ROOT" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
cp -a -- "$SOURCE_ROOT/hooks" "$RUNTIME_ROOT"
mkdir -p -- "$RUNTIME_ROOT/bin"
cp -a -- "$SOURCE_ROOT/bin/eci-command-gate-mode" "$RUNTIME_ROOT/bin/"
eci_sed_inplace '2{/^exit 0$/d;}' "$RUNTIME_ROOT/hooks/validate-bash.sh"

# Provision the gate binaries the hook verifies ($HOME/.omp/agent/eci/bin and
# their .sha256 receipts) through the ordinary builder, in the fixture HOME only.
# The Go caches stay on the caller's paths so no read-only module cache lands
# under TMP_ROOT.
GOMODCACHE="$("$ECI_TEST_GO" env GOMODCACHE)" GOCACHE="$("$ECI_TEST_GO" env GOCACHE)" \
  HOME="$HOME_ROOT" bash "$RUNTIME_ROOT/hooks/build-native-binaries.sh" \
  eci-command-plan eci-safe-import eci-hook-helper $([ "$(uname -s)" != Linux ] || echo eci-verexec) >/dev/null

printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"

REPO="$TMP_ROOT/repo"
FOREIGN_REPO="$TMP_ROOT/foreign-repo"
NON_REPO="$TMP_ROOT/not-a-repository"
mkdir -p -- "$REPO" "$FOREIGN_REPO" "$NON_REPO"
git -C "$REPO" init -q
git -C "$FOREIGN_REPO" init -q
REPO="$(eci_realpath_existing "$REPO")"
FOREIGN_REPO="$(eci_realpath_existing "$FOREIGN_REPO")"
NON_REPO="$(eci_realpath_existing "$NON_REPO")"

SESSION='git-normalization-session'
PROOF_ROOT="$TMP_ROOT/proof"
mkdir -p -- "$PROOF_ROOT/$SESSION"
printf '%s\n' \
  'scope: Git normalization regression' \
  "cwd: $REPO" \
  "session_id: $SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"
# The hook classifies the caller from its callback transcript; these are
# coordinator actions, so give them a main-session transcript in the fixture HOME.
MAIN_TRANSCRIPT="$HOME_ROOT/.omp/agent/sessions/git-normalization/main.jsonl"
mkdir -p -- "$(dirname -- "$MAIN_TRANSCRIPT")"
printf '%s\n' \
  "{\"type\":\"session\",\"version\":3,\"id\":\"$SESSION\",\"cwd\":\"$REPO\"}" \
  '{"type":"message","role":"user","content":"git normalization"}' >"$MAIN_TRANSCRIPT"

run_hook() {
  local command="$1" output="$TMP_ROOT/output.json"
  jq -cn --arg session "$SESSION" --arg cwd "$REPO" --arg command "$command" \
    --arg transcript "$MAIN_TRANSCRIPT" \
    '{session_id:$session,tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      ECI_ROLE=coordinator ECI_HOOK_IS_SUBAGENT=false \
      XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
      PATH="$RUNTIME_ROOT/bin:$PATH" \
      bash "$RUNTIME_ROOT/hooks/validate-bash.sh" >"$output" || return $?
  printf '%s\n' "$output"
}

assert_allowed() {
  local command="$1" output
  output="$(run_hook "$command")" || return $?
  if [ -s "$output" ]; then
    printf 'ordinary Git command was denied: %q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
}

assert_denied_code() {
  local command="$1" code="$2" output
  output="$(run_hook "$command")" || return $?
  jq -e --arg code "[$code]" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code))
  ' "$output" >/dev/null || {
    printf 'expected concrete %s denial for: %q\n' "$code" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

if ( HOME_ROOT="$TMP_ROOT/missing-home"; assert_allowed 'git status' ) >/dev/null 2>&1; then
  printf '%s\n' 'normalization assertion accepted a failed hook' >&2
  exit 1
fi

# These are normal coordinator Git actions.  They must not require a special
# route, a canonical executable spelling, a receipt, or a shell-shape ritual.
assert_allowed 'git branch topic'
assert_allowed 'git remote add origin https://example.invalid/project.git'
assert_allowed 'git push origin topic'
assert_allowed "env GIT_DIR=$REPO/.git /usr/bin/git branch topic"
assert_allowed "printf ready | git -C $REPO branch topic"
assert_allowed "git ci -m 'ordinary alias spelling'"
# Git itself owns an unresolved repository diagnostic; it is not an ECI denial.
assert_allowed "git -C $NON_REPO add -- file.txt"

# Repository selection stays native; whole-tree reset and broad staging remain
# checked, and an acknowledged whole-tree reset stays native.
assert_allowed "git -C $FOREIGN_REPO branch topic"
assert_allowed "printf ready | git -C $FOREIGN_REPO add -- file.txt"
assert_denied_code "git -C $REPO reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED
assert_denied_code "env GIT_DIR=$REPO/.git /usr/bin/git reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED
assert_allowed "ECI_WHOLE_TREE_RESET=1 git -C $REPO reset --hard"
assert_denied_code "git -C $REPO add -A" ECI_BROAD_DESTRUCTIVE_DENIED

printf '%s\n' 'validate-bash Git normalization: PASS'

#!/usr/bin/env bash

set -euo pipefail

SOURCE_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-git-normalization.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

# Exercise the real hook in a private runtime, removing a line-2 bypass if
# present. No command supplied to the hook is executed by this test.
HOME_ROOT="$TMP_ROOT/home"
RUNTIME_ROOT="$HOME_ROOT/.omp/agent/eci"
mkdir -p -- "$RUNTIME_ROOT" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
cp -a -- "$SOURCE_ROOT/hooks" "$RUNTIME_ROOT"
mkdir -p -- "$RUNTIME_ROOT/bin"
cp -a -- "$SOURCE_ROOT/bin/eci-command-gate-mode" "$RUNTIME_ROOT/bin/"
sed -i '2{/^exit 0$/d;}' -- "$RUNTIME_ROOT/hooks/validate-bash.sh"

# Build and attest the planner only inside the disposable fixture.  This keeps
# the test on the ordinary hook path without changing the shared runtime.
PLANNER_DIR="$RUNTIME_ROOT/hooks/lib/eci-command-plan-go"
(
  cd -- "$PLANNER_DIR"
  /usr/lib/go-1.26/bin/go build -trimpath -buildvcs=false -o eci-command-plan .
)
chmod 755 -- "$PLANNER_DIR/eci-command-plan"
planner_go_mod_sha="$(sha256sum -- "$PLANNER_DIR/go.mod" | awk '{print $1}')"
planner_main_sha="$(sha256sum -- "$PLANNER_DIR/main.go" | awk '{print $1}')"
planner_classifier_sha="$(sha256sum -- "$PLANNER_DIR/classifier.go" | awk '{print $1}')"
planner_binary_sha="$(sha256sum -- "$PLANNER_DIR/eci-command-plan" | awk '{print $1}')"
planner_binary_size="$(stat -Lc '%s' -- "$PLANNER_DIR/eci-command-plan")"
awk \
  -v go_mod="$planner_go_mod_sha" \
  -v main="$planner_main_sha" \
  -v classifier="$planner_classifier_sha" \
  -v binary="$planner_binary_sha" \
  -v size="$planner_binary_size" '
  $1 == "source_go.mod_sha256" { print $1 "\t" go_mod; next }
  $1 == "source_main.go_sha256" { print $1 "\t" main; next }
  $1 == "source_classifier.go_sha256" { print $1 "\t" classifier; next }
  $1 == "binary_sha256" { print $1 "\t" binary; next }
  $1 == "binary_size" { print $1 "\t" size; next }
  { print }
' "$PLANNER_DIR/.eci-command-plan.provenance" >"$PLANNER_DIR/.eci-command-plan.provenance.tmp"
mv -- "$PLANNER_DIR/.eci-command-plan.provenance.tmp" "$PLANNER_DIR/.eci-command-plan.provenance"
chmod 600 -- "$PLANNER_DIR/.eci-command-plan.provenance"

printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"

REPO="$TMP_ROOT/repo"
FOREIGN_REPO="$TMP_ROOT/foreign-repo"
NON_REPO="$TMP_ROOT/not-a-repository"
mkdir -p -- "$REPO" "$FOREIGN_REPO" "$NON_REPO"
git -C "$REPO" init -q
git -C "$FOREIGN_REPO" init -q
REPO="$(realpath -e -- "$REPO")"
FOREIGN_REPO="$(realpath -e -- "$FOREIGN_REPO")"
NON_REPO="$(realpath -e -- "$NON_REPO")"

SESSION='git-normalization-session'
PROOF_ROOT="$TMP_ROOT/proof"
mkdir -p -- "$PROOF_ROOT/$SESSION"
printf '%s\n' \
  'scope: Git normalization regression' \
  "cwd: $REPO" \
  "session_id: $SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"

run_hook() {
  local command="$1" output="$TMP_ROOT/output.json"
  jq -cn --arg session "$SESSION" --arg cwd "$REPO" --arg command "$command" \
    '{session_id:$session,cwd:$cwd,tool_input:{command:$command}}' |
    HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      ECI_ROLE=coordinator ECI_HOOK_IS_SUBAGENT=false \
      XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
      PATH="$RUNTIME_ROOT/bin:$PATH" \
      bash "$RUNTIME_ROOT/hooks/validate-bash.sh" >"$output"
  printf '%s\n' "$output"
}

assert_allowed() {
  local command="$1" output
  output="$(run_hook "$command")"
  if [ -s "$output" ]; then
    printf 'ordinary Git command was denied: %q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
}

assert_denied_code() {
  local command="$1" code="$2" output
  output="$(run_hook "$command")"
  jq -e --arg code "[$code]" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code))
  ' "$output" >/dev/null || {
    printf 'expected concrete %s denial for: %q\n' "$code" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

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

# The boundary is the resolved effect, not a Git spelling.  A mutation whose
# target resolves to another repository, and a reset of the whole worktree,
# remain concrete accidental-risk checks.
assert_denied_code "git -C $FOREIGN_REPO branch topic" ECI_GIT_CROSS_SCOPE_DENIED
assert_denied_code "printf ready | git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED
assert_denied_code "git -C $REPO reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED
assert_denied_code "env GIT_DIR=$REPO/.git /usr/bin/git reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED

printf '%s\n' 'validate-bash Git normalization: PASS'

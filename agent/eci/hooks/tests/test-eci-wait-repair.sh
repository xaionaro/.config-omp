#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="${ECI_TEST_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-wait-repair.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

export HOME="$TMP_ROOT/home"
export ECI_HOME="$HOME/.omp/agent/eci"
export ECI_PROOF_ROOT="$TMP_ROOT/proof"
export XDG_CONFIG_HOME="$TMP_ROOT/config"
session_id=t00-wait-repair
repo="$TMP_ROOT/repo"
mkdir -p -- "$ECI_HOME" "$ECI_PROOF_ROOT" "$XDG_CONFIG_HOME" "$repo"
cp -a -- "$ROOT/bin" "$ECI_HOME/bin"
cp -a -- "$ROOT/hooks" "$ECI_HOME/hooks"
fixture_cwd="$(pwd -P)"
git -C "$repo" init -q
git -C "$repo" config user.email eci-test@example.invalid
git -C "$repo" config user.name 'ECI wait-repair test'
printf '%s\n' fixture >"$repo/README"
git -C "$repo" add README
git -C "$repo" commit -qm fixture

run_active() {
  ECI_SESSION_ID="$session_id" ECI_PROOF_ROOT="$ECI_PROOF_ROOT" \
    ECI_HOME="$ECI_HOME" HOME="$HOME" "$ECI_HOME/bin/eci-active" "$@"
}

wait_report_fixture() {
  local report="$1"
  {
    printf '# ECI User-Owned Wait\n'
    printf 'state: user-owned-wait\n'
    printf 'blocker_id: wait-repair-test\n'
    printf 'state_fingerprint: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n'
    printf 'owner: user\n'
    printf 'brp_result: exhausted-no-feasible-internal-path\n'
    printf 'user_owned_input: unobtainable\n'
    printf 'unblock_kind: input\n'
    printf 'unblock: user-owned repair fixture\n'
  } >"$report"
}

run_stop() {
  local input="$1" output="$2"
  ECI_PROOF_ROOT="$ECI_PROOF_ROOT" HOME="$HOME" bash "$ECI_HOME/hooks/stop-gate.sh" \
    <"$input" >"$output"
}

session_dir="$ECI_PROOF_ROOT/$session_id"
run_active on "wait repair fixture" >"$TMP_ROOT/on.out" 2>"$TMP_ROOT/on.err"
marker="$session_dir/eci_active"
report="$session_dir/eci_user_owned_wait.md"
wait_report_fixture "$report"
report="$(eci_realpath_existing "$report")"
run_active wait "$report" >"$TMP_ROOT/wait.out" 2>"$TMP_ROOT/wait.err"
state="$session_dir/eci_wait"
marker_before="$TMP_ROOT/marker.before"
index_before="$TMP_ROOT/index.before"
worktree_before="$TMP_ROOT/worktree.before"
cp -- "$marker" "$marker_before"
cp -- "$repo/.git/index" "$index_before"
cp -- "$repo/README" "$worktree_before"

# A missing final LF is malformed and remains a blocking Stop condition.
truncate --size=-1 -- "$state"
input="$TMP_ROOT/input.json"
jq -cn --arg cwd "$fixture_cwd" --arg sid "$session_id" \
  --arg transcript "$TMP_ROOT/missing-transcript.jsonl" \
  '{session_id:$sid,cwd:$cwd,transcript_path:$transcript,stop_hook_active:false}' >"$input"
output="$TMP_ROOT/output.json"
run_stop "$input" "$output"
jq -e '.decision == "block" and ((.continue // false) | not)' "$output" >/dev/null
! grep -Eiq 'override|permissive|escape hatch|eci-wait-repair-authorize' "$output"

# A repair report from another path remains out of scope; the bad state is
# unchanged until the current session's exact report is supplied.
state_before_rejected="$TMP_ROOT/state.before-rejected"
cp -- "$state" "$state_before_rejected"
other_report="$TMP_ROOT/other-wait.md"
wait_report_fixture "$other_report"
if run_active wait-repair "$other_report" >"$TMP_ROOT/wrong-path.out" 2>"$TMP_ROOT/wrong-path.err"; then
  printf '%s\n' 'out-of-session wait repair unexpectedly succeeded' >&2
  exit 1
fi
cmp -s "$state_before_rejected" "$state"

# The active session can repair its own malformed wait state directly; no
# user-auth file, hash receipt, or role-only ceremony is required.
run_active wait-repair "$report" >"$TMP_ROOT/repair.out" 2>"$TMP_ROOT/repair.err"
[ "$(tail -c 1 -- "$state" | od -An -t x1 | tr -d '[:space:]')" = 0a ]
cmp -s "$marker_before" "$marker"
cmp -s "$index_before" "$repo/.git/index"
cmp -s "$worktree_before" "$repo/README"

run_stop "$input" "$output"
jq -e '.continue == true and (.decision == null)' "$output" >/dev/null
! grep -Eiq 'override|permissive|escape hatch|eci-wait-repair-authorize' "$output"

printf '%s\n' 'eci wait-repair assertions: PASS'

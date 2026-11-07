#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-aggregate-git-independence.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

proof_root="$TMP_ROOT/proof"
outer="$TMP_ROOT/outer"
independent="$outer/independent"
shared="$outer/shared-worktree"
non_top_level="$independent/subdirectory"
mkdir -p -- "$proof_root" "$outer"

init_repo() {
  local repo="$1"

  mkdir -p -- "$repo"
  git -C "$repo" init -q
  git -C "$repo" config user.name 'ECI aggregate Git topology test'
  git -C "$repo" config user.email 'eci-aggregate-git-topology@example.invalid'
  printf '%s\n' baseline >"$repo/tracked.txt"
  git -C "$repo" add -- tracked.txt
  git -C "$repo" commit -qm baseline
}

plan_for() {
  local path="$1" repo="$2"

  jq -cn --arg repo "$repo" \
    '{repositories:[{id:"member",repo_root:$repo}]}' >"$path"
}

run_active() {
  local session_id="$1"
  shift

  (
    cd -- "$outer"
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$session_id" \
      TMPDIR="$TMP_ROOT" "$ROOT/bin/eci-active" "$@"
  )
}

init_repo "$outer"
init_repo "$independent"
mkdir -p -- "$non_top_level"
git -C "$outer" worktree add -q -b aggregate-shared "$shared"

independent_plan="$TMP_ROOT/independent-plan.json"
shared_plan="$TMP_ROOT/shared-plan.json"
overlap_plan="$TMP_ROOT/overlap-plan.json"
non_top_plan="$TMP_ROOT/non-top-plan.json"
plan_for "$independent_plan" "$independent"
plan_for "$shared_plan" "$shared"
plan_for "$overlap_plan" "$outer"
plan_for "$non_top_plan" "$non_top_level"

# A nested Git repository with a different common directory is independent of
# its Git outer and may be selected for aggregate repair.
independent_sid=aggregate-git-independent
run_active "$independent_sid" on 'independent nested Git aggregate fixture' >/dev/null
run_active "$independent_sid" aggregate-migrate "$independent_plan"
[ -f "$proof_root/$independent_sid/eci-aggregate-plan.json" ]

# A linked worktree shares the outer common directory and must remain outside
# the aggregate member set even though it is a strict filesystem descendant.
shared_sid=aggregate-git-shared
run_active "$shared_sid" on 'shared Git common-dir aggregate fixture' >/dev/null
if run_active "$shared_sid" aggregate-migrate "$shared_plan" >"$TMP_ROOT/shared.out" 2>&1; then
  printf '%s\n' 'aggregate-migrate accepted a child sharing the outer Git common directory' >&2
  exit 1
fi
[ ! -e "$proof_root/$shared_sid/eci-aggregate-plan.json" ]

# Filesystem overlap and a non-top-level Git path remain concrete wrong-target
# failures even when the outer repository itself is otherwise eligible.
overlap_sid=aggregate-git-overlap
run_active "$overlap_sid" on 'overlap aggregate fixture' >/dev/null
if run_active "$overlap_sid" aggregate-migrate "$overlap_plan" >"$TMP_ROOT/overlap.out" 2>&1; then
  printf '%s\n' 'aggregate-migrate accepted the outer repository as its own child' >&2
  exit 1
fi

non_top_sid=aggregate-git-non-top
run_active "$non_top_sid" on 'non-top-level aggregate fixture' >/dev/null
if run_active "$non_top_sid" aggregate-migrate "$non_top_plan" >"$TMP_ROOT/non-top.out" 2>&1; then
  printf '%s\n' 'aggregate-migrate accepted a non-top-level child path' >&2
  exit 1
fi

# Commit publication remains CAS-protected after a target has been selected.
. "$ROOT/hooks/lib/eci-proof-state.sh"
printf '%s\n' reviewed >>"$independent/tracked.txt"
git -C "$independent" add -- tracked.txt
accepted_tree="$(git -C "$independent" write-tree)"
accepted_parent="$(git -C "$independent" rev-parse HEAD)"
accepted_ref="$(git -C "$independent" symbolic-ref -q HEAD)"
printf '%s\n' raced >"$independent/raced.txt"
git -C "$independent" add -- raced.txt
if aggregate_commit_accepted_tree_cas "$independent" "$accepted_tree" "$accepted_parent" "$accepted_ref" "$TMP_ROOT/message"; then
  printf '%s\n' 'aggregate commit CAS accepted an index changed after acceptance' >&2
  exit 1
fi
[ "$(git -C "$independent" rev-parse HEAD)" = "$accepted_parent" ]

printf '%s\n' 'ECI aggregate Git independence assertions: PASS'

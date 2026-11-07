#!/usr/bin/env bash

# R11 regression: current-session aggregate coordination residue is diagnostic
# only.  A regular stale record must not stop an explicitly selected safe
# aggregate stage, and a symlink residue must be left untouched rather than
# followed or rewritten.

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(realpath -e -- "$(mktemp -d "$TMP_PARENT/eci-aggregate-coordination-residue.XXXXXX")")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

session_id='aggregate-coordination-residue'
proof_root="$TMP_ROOT/proof"
outer_cwd="$TMP_ROOT/outer"
repo="$outer_cwd/repo-a"
session_dir="$proof_root/$session_id"
plan_source="$TMP_ROOT/eci-aggregate-plan.json.source"
target_path='tracked.txt'

mkdir -p -- "$proof_root" "$repo"
git -C "$repo" init -q
git -C "$repo" config user.name 'ECI aggregate coordination residue test'
git -C "$repo" config user.email 'eci-aggregate-coordination-residue@example.invalid'
printf '%s\n' baseline >"$repo/$target_path"
git -C "$repo" add -- "$target_path"
git -C "$repo" commit -qm 'fixture baseline'

jq -cn --arg repo "$repo" \
  '{schema:"eci-aggregate-plan-source/v1",repositories:[{id:"repo-a",repo_root:$repo}]}' \
  >"$plan_source"

run_active() {
  (
    cd -- "$outer_cwd"
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$session_id" \
      ECI_ROLE=main TMPDIR="$TMP_ROOT" "$ROOT/bin/eci-active" "$@"
  )
}

run_active on 'aggregate coordination residue fixture' >/dev/null
# Aggregate migration must resolve the current direct session/CWD mapping, not
# reject a regular marker merely because it retains harmless historic fields.
printf '%s\n' 'legacy_metadata: harmless historic marker context' >>"$session_dir/eci_active"
run_active aggregate-migrate "$plan_source" >/dev/null

assert_residue_does_not_block_selected_stage() {
  local label="$1" residue="$2" outside="$3" stage_output before after

  printf '%s\n' "safe selected aggregate change: $label" >>"$repo/$target_path"
  stage_output="$TMP_ROOT/$label-stage.out"
  if ! run_active aggregate-stage repo-a -- "$target_path" >"$stage_output" 2>&1; then
    cat -- "$stage_output" >&2
    printf 'ordinary aggregate stage was blocked by %s coordination residue\n' "$label" >&2
    exit 1
  fi
  git -C "$repo" diff --cached --name-only | grep -Fqx "$target_path"

  # A selected safe path must not relax the concrete target boundary.  This
  # invalid operand must leave the selected repository index unchanged.
  before="$(sha256sum -- "$repo/.git/index" | awk '{print $1}')"
  if run_active aggregate-stage repo-a -- "$outside" >"$TMP_ROOT/$label-outside.out" 2>&1; then
    printf 'aggregate stage accepted an outside path while testing %s residue\n' "$label" >&2
    exit 1
  fi
  after="$(sha256sum -- "$repo/.git/index" | awk '{print $1}')"
  [ "$after" = "$before" ]

  git -C "$repo" restore --staged -- "$target_path"
  git -C "$repo" restore -- "$target_path"
  [ -e "$residue" ] || [ -L "$residue" ]
}

rewrite_plan_as_local_residue() {
  local plan="$session_dir/eci-aggregate-plan.json" rewritten="$TMP_ROOT/eci-aggregate-plan.residue.json"

  # These values are historic bookkeeping, not the selected Git target.  The
  # planner must re-derive the live outer/repository bindings before staging.
  jq '
    .schema = "legacy-aggregate-plan" |
    .session_id = "stale-session-metadata" |
    .outer_cwd = "/stale/outer-cwd" |
    .marker_sha256 = "not-a-digest" |
    .repositories[0] |= del(.git_dir, .git_common_dir)
  ' "$plan" >"$rewritten"
  mv -- "$rewritten" "$plan"
}

case "${1:-all}" in
regular)
  regular_residue="$session_dir/eci_wait"
  printf '%s\n' 'stale user-owned wait metadata from an earlier coordination attempt' >"$regular_residue"
  assert_residue_does_not_block_selected_stage regular "$regular_residue" '../outside.txt'
  [ -f "$regular_residue" ] && [ ! -L "$regular_residue" ]
  ;;
symlink)
  foreign_residue="$TMP_ROOT/foreign-residue"
  symlink_residue="$session_dir/eci_wait"
  printf '%s\n' 'foreign residue must not be followed or changed' >"$foreign_residue"
  ln -s -- "$foreign_residue" "$symlink_residue"
  assert_residue_does_not_block_selected_stage symlink "$symlink_residue" '../outside.txt'
  [ -L "$symlink_residue" ]
  grep -Fqx 'foreign residue must not be followed or changed' "$foreign_residue"
  ;;
plan-metadata)
  plan_residue="$session_dir/eci-aggregate-plan.json"
  rewrite_plan_as_local_residue
  assert_residue_does_not_block_selected_stage plan-metadata "$plan_residue" '../outside.txt'
  [ -f "$plan_residue" ] && [ ! -L "$plan_residue" ]
  ;;
all)
  bash "$0" regular
  bash "$0" symlink
  bash "$0" plan-metadata
  ;;
*)
  printf 'usage: %s [regular|symlink|plan-metadata|all]\n' "$0" >&2
  exit 2
  ;;
esac

printf '%s\n' 'ECI aggregate coordination residue tests: PASS'

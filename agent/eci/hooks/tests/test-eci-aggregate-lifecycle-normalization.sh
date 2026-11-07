#!/usr/bin/env bash

# R11: aggregate lifecycle maintenance uses semantic selected-repository
# targets. Plan/message filename, directory aliases, role labels, and stale
# regular history are diagnostics rather than permissions for ordinary work.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(realpath -e -- "$(mktemp -d "$TMP_PARENT/eci-aggregate-lifecycle-normalization.XXXXXX")")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

session_id='aggregate-lifecycle-normalization'
proof_root="$TMP_ROOT/proof"
outer_cwd="$TMP_ROOT/outer"
repo="$outer_cwd/repo-a"
repo_alias="$outer_cwd/repo-alias"
plan_real_dir="$TMP_ROOT/plan-real"
plan_alias_dir="$TMP_ROOT/plan-alias"
plan_source="$plan_alias_dir/friendly-plan.json"
message_real_dir="$TMP_ROOT/message-real"
message_alias_dir="$TMP_ROOT/message-alias"
message_source="$message_alias_dir/ordinary-message.txt"
report="$TMP_ROOT/ordinary-close.md"
session_dir="$proof_root/$session_id"

mkdir -p -- "$proof_root" "$repo" "$plan_real_dir" "$message_real_dir"
ln -s -- "$plan_real_dir" "$plan_alias_dir"
ln -s -- "$message_real_dir" "$message_alias_dir"
git -C "$repo" init -q
git -C "$repo" config user.name 'ECI aggregate lifecycle normalization'
git -C "$repo" config user.email 'eci-aggregate-lifecycle-normalization@example.invalid'
printf '%s\n' baseline >"$repo/tracked.txt"
git -C "$repo" add -- tracked.txt
git -C "$repo" commit -qm 'baseline'
ln -s -- "$repo" "$repo_alias"

# Deliberately use an ordinary filename, a directory alias, a human-oriented
# field name, and pretty JSON. The lifecycle command must derive its compact
# local routing record from the semantic selected repository mapping.
jq -n --arg repo "$repo_alias" \
  '{purpose:"normal selected aggregate work",repositories:[{id:"repo-a",repo_root:$repo}]}' \
  >"$plan_source"
# Git accepts a normal non-empty message file without a trailing newline; it
# is not an ECI receipt or an input-format permission artifact.
printf '%s' 'normal selected aggregate commit' >"$message_source"
printf '%s\n' '# normal aggregate close' >"$report"

run_active_as() {
  local role="$1"
  shift

  (
    cd -- "$outer_cwd"
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$session_id" \
      ECI_ROLE="$role" TMPDIR="$TMP_ROOT" "$ROOT/bin/eci-active" "$@"
  )
}

run_active() {
  run_active_as worker "$@"
}

run_active_as coordinator on 'aggregate lifecycle normalization fixture' >/dev/null
run_active aggregate-migrate "$plan_source"

plan="$session_dir/eci-aggregate-plan.json"
[ -f "$plan" ] && [ ! -L "$plan" ] || {
  printf '%s\n' 'aggregate-migrate did not publish a normal selected plan' >&2
  exit 1
}
resolved_repo="$(realpath -e -- "$repo")"
jq -e --arg repo "$resolved_repo" '.repositories[0].repo_root == $repo' "$plan" >/dev/null || {
  printf '%s\n' 'aggregate-migrate did not normalize the selected repository alias' >&2
  cat -- "$plan" >&2
  exit 1
}

printf '%s\n' selected-change >>"$repo/tracked.txt"
run_active aggregate-stage repo-a ./tracked.txt
git -C "$repo" diff --cached --name-only | grep -Fqx tracked.txt

# A literal path named `--` is an ordinary in-repository filename once Git's
# operand separator has already been supplied. It is not a grammar failure.
printf '%s\n' 'ordinary separator-named file' >"$repo/--"
run_active aggregate-stage repo-a -- --
git -C "$repo" diff --cached --name-only | grep -Fqx -- --

index_before="$(sha256sum -- "$repo/.git/index" | awk '{print $1}')"
if run_active aggregate-stage repo-a ../outside.txt; then
  printf '%s\n' 'aggregate-stage accepted an outside selected-repository path' >&2
  exit 1
fi
[ "$(sha256sum -- "$repo/.git/index" | awk '{print $1}')" = "$index_before" ] || {
  printf '%s\n' 'aggregate-stage changed the selected repository index for an outside path' >&2
  exit 1
}

# Stale regular history is advisory/self-repair only. It must not force a
# manifest, receipt, hash, or specially named message source before a normal
# selected commit can use the same parent/tree CAS helper.
printf '%s\n' stale >"$session_dir/eci-aggregate.repo-a.commit-admitted.1"
printf '%s\n' stale >"$session_dir/eci-aggregate.repo-a.commit-message.1"
head_before="$(git -C "$repo" rev-parse HEAD)"
run_active aggregate-commit repo-a "$message_source"
head_after="$(git -C "$repo" rev-parse HEAD)"
[ "$head_after" != "$head_before" ] || {
  printf '%s\n' 'aggregate-commit did not publish the selected repository commit' >&2
  exit 1
}
git -C "$repo" show --format= --name-only "$head_after" | grep -Fqx tracked.txt

printf '%s\n' stale >"$session_dir/eci-aggregate-teardown-complete"
run_active aggregate-off "$report"
[ ! -e "$session_dir/eci_active" ] && [ ! -L "$session_dir/eci_active" ] || {
  printf '%s\n' 'aggregate-off retained the active marker because of stale regular history' >&2
  exit 1
}

printf '%s\n' 'ECI aggregate lifecycle normalization: PASS'

#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(eci_realpath_canonical "$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-aggregate-commit-tree.XXXXXX")")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

if grep -Fq 'ECI_AGGREGATE_COMMIT_TEST_STAGE_IGNORED_PATH' "$ROOT/bin/eci-active"; then
  printf '%s\n' 'production aggregate commit still contains the test-only environment staging seam' >&2
  exit 1
fi

repo="$TMP_ROOT/repo-a"
race_path="$repo/.eci-aggregate-race-unreviewed"
commit_message="$TMP_ROOT/eci-aggregate-commit-message.source"
mkdir -p -- "$repo/hooks"

git -C "$repo" init -q
git -C "$repo" config user.name 'ECI aggregate tree test'
git -C "$repo" config user.email 'eci-aggregate-tree-test@example.invalid'
printf '%s\n' baseline >"$repo/hooks/target.txt"
git -C "$repo" add -- hooks/target.txt
git -C "$repo" commit -qm 'fixture baseline'

printf '%s\n' 'reviewed aggregate change' >>"$repo/hooks/target.txt"
git -C "$repo" add -- hooks/target.txt
accepted_tree="$(git -C "$repo" write-tree)"
accepted_parent="$(git -C "$repo" rev-parse HEAD)"
accepted_ref="$(git -C "$repo" symbolic-ref -q HEAD)"
printf '%s\n' 'aggregate tree CAS commit' >"$commit_message"

# The test harness, rather than production environment state, introduces the
# post-admission index change. The helper must refuse it before publication.
printf '%s\n' '.eci-aggregate-race-unreviewed' >>"$repo/.git/info/exclude"
printf '%s\n' 'unreviewed index race content' >"$race_path"
git -C "$repo" add -f -- .eci-aggregate-race-unreviewed

. "$ROOT/hooks/lib/eci-proof-state.sh"

head_before="$(git -C "$repo" rev-parse HEAD)"
if aggregate_commit_accepted_tree_cas "$repo" "$accepted_tree" "$accepted_parent" "$accepted_ref" "$commit_message"; then
  printf '%s\n' 'aggregate CAS unexpectedly accepted an index mutation after admission' >&2
  exit 1
fi
[ "${aggregate_commit_tree_cas_failure:-}" = 'index tree changed after acceptance' ]
[ "$(git -C "$repo" rev-parse HEAD)" = "$head_before" ]
git -C "$repo" diff --cached --name-only | grep -Fqx .eci-aggregate-race-unreviewed

git -C "$repo" restore --staged -- .eci-aggregate-race-unreviewed
rm -f -- "$race_path"
aggregate_commit_accepted_tree_cas "$repo" "$accepted_tree" "$accepted_parent" "$accepted_ref" "$commit_message"
head_after="$(git -C "$repo" rev-parse HEAD)"
[ "$head_after" != "$head_before" ]
git -C "$repo" show --format= --name-only "$head_after" | grep -Fqx hooks/target.txt
if git -C "$repo" show --format= --name-only "$head_after" | grep -Fqx .eci-aggregate-race-unreviewed; then
  printf '%s\n' 'aggregate CAS included unreviewed race content' >&2
  exit 1
fi

printf '%s\n' 'ECI aggregate commit tree CAS tests: PASS'

#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${HOME:?}/tmp/eci-active-normal-git.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

repo="$TMP_ROOT/repo"
alias_repo="$TMP_ROOT/repo-alias"
mkdir -p -- "$repo"
git -C "$repo" init -q
ln -s -- "$repo" "$alias_repo"

# `approve-commit` remains a compatibility spelling only. Normal Git work
# must not need an owner role, a canonical repo spelling, an exact command
# form, or an approval artifact.
output="$TMP_ROOT/approve-commit.out"
if ! ECI_SESSION_ID=normal-git-cli-session ECI_ROLE=subagent \
  "$ROOT/bin/eci-active" approve-commit "$alias_repo" \
  "env GIT_EDITOR=true /usr/bin/git -C $alias_repo commit -am 'ordinary commit'" \
  >"$output" 2>&1; then
  cat -- "$output" >&2
  exit 1
fi
grep -Fq 'Normal Git commits do not need ECI approval artifacts.' "$output"
[ ! -e "$repo/.git-commit-approved-once" ]
[ ! -L "$repo/.git-commit-approved-once" ]

printf '%s\n' 'eci-active normal Git compatibility: PASS'

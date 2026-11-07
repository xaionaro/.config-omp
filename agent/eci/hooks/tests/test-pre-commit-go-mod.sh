#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
checker_source=$script_dir/../pre-commit-go-mod.sh
installer_source=$script_dir/../install-pre-commit-go-mod.sh
work_root=$(mktemp -d "${ECI_TMPDIR:-${KIMI_TMPDIR:-${HOME:?}/tmp}}/go-mod-hook-test.XXXXXX")
trap 'rm -rf -- "$work_root"' EXIT HUP INT TERM

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

expect_status() {
  expected=$1
  shift
  set +e
  "$@"
  actual=$?
  set -e
  if [[ $actual -ne $expected ]]; then
    fail "expected status $expected, got $actual: $*"
  fi
}

expect_failure() {
  set +e
  "$@"
  actual=$?
  set -e
  if [[ $actual -eq 0 ]]; then
    fail "expected failure: $*"
  fi
  return 0
}

expect_diagnostic() {
  local expected_status="$1" expected_code="$2" output actual field
  shift 2
  output=$(mktemp "$work_root/diagnostic.XXXXXX")
  set +e
  "$@" >"$output" 2>&1
  actual=$?
  set -e
  [[ "$actual" -eq "$expected_status" ]] ||
    fail "expected diagnostic status $expected_status, got $actual: $*"
  for field in "[$expected_code]" 'phase=PreCommit' 'operation=' 'subject=' 'reason:' 'remediation:'; do
    grep -Fq -- "$field" "$output" || {
      cat "$output" >&2
      fail "diagnostic missing $field: $*"
    }
  done
  rm -f -- "$output"
}

new_repo() {
  case_root=$(mktemp -d "$work_root/case.XXXXXX")
  mkdir -p "$case_root/hooks/lib"
  git -C "$case_root" init -q
  git -C "$case_root" config user.email test@example.invalid
  git -C "$case_root" config user.name test
  cp "$checker_source" "$case_root/hooks/pre-commit-go-mod.sh"
  cp "$installer_source" "$case_root/hooks/install-pre-commit-go-mod.sh"
  cp "$script_dir/../lib/eci-diagnostic.sh" "$case_root/hooks/lib/eci-diagnostic.sh"
  chmod 755 "$case_root/hooks/pre-commit-go-mod.sh" "$case_root/hooks/install-pre-commit-go-mod.sh"
}

run_checker() {
  (cd "$case_root" && bash hooks/pre-commit-go-mod.sh)
}

run_installer() {
  (cd "$case_root" && bash hooks/install-pre-commit-go-mod.sh "$@")
}

run_native_hook() {
  (cd "$case_root" && .git/hooks/pre-commit)
}

write_mod() {
  printf '%s\n' "$2" >"$case_root/$1"
  git -C "$case_root" add -- "$1"
}

new_repo no-module
printf 'plain\n' >"$case_root/README"
git -C "$case_root" add README
fake_bin=$case_root/fake-bin
mkdir "$fake_bin"
cat >"$fake_bin/go" <<'EOF'
#!/bin/sh
printf 'go must not run for a repository without go.mod\n' >&2
exit 99
EOF
cat >"$fake_bin/jq" <<'EOF'
#!/bin/sh
printf 'jq must not run for a repository without go.mod\n' >&2
exit 99
EOF
chmod 755 "$fake_bin/go" "$fake_bin/jq"
(cd "$case_root" && PATH="$fake_bin:$PATH" bash hooks/pre-commit-go-mod.sh)

new_repo remote
write_mod go.mod $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => example.com/fork v1.2.3'
run_checker

new_repo relative-local
write_mod go.mod $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => ../dependency'
expect_diagnostic 1 GO_MOD_LOCAL_REPLACE_DENIED run_checker

new_repo absolute-local
write_mod go.mod $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => /tmp/dependency'
expect_status 1 run_checker

new_repo workspace-local
write_mod go.mod $'module example.com/root\ngo 1.20'
printf '%s\n' $'go 1.20\n\nuse .\n\nreplace example.com/dependency => ../dependency' >"$case_root/go.work"
git -C "$case_root" add go.work
run_checker

workspace_absolute_case() {
  local label="$1" replacement="$2"
  new_repo "$label"
  write_mod go.mod $'module example.com/root\ngo 1.20'
  printf '%s\n' 'go 1.20' '' 'use .' '' "replace example.com/dependency => $replacement" >"$case_root/go.work"
  git -C "$case_root" add go.work
  expect_status 1 run_checker
}

workspace_absolute_case workspace-absolute-unix '/tmp/dependency'
workspace_absolute_case workspace-absolute-drive-forward 'C:/dependency'
workspace_absolute_case workspace-absolute-drive-back 'C:\dependency'
workspace_absolute_case workspace-absolute-rooted-server '\\server/dependency'
workspace_absolute_case workspace-absolute-rooted-single '\dependency'

workspace_use_absolute_case() {
  local label="$1" use_path="$2"
  new_repo "$label"
  write_mod go.mod $'module example.com/root\ngo 1.20'
  printf '%s\n' 'go 1.20' '' "use $use_path" >"$case_root/go.work"
  git -C "$case_root" add go.work
  expect_status 1 run_checker
}

workspace_use_absolute_case workspace-use-absolute-unix '/tmp/dependency'
workspace_use_absolute_case workspace-use-absolute-drive-forward 'C:/dependency'
workspace_use_absolute_case workspace-use-absolute-drive-back 'C:\dependency'
workspace_use_absolute_case workspace-use-absolute-rooted-server '\\server/dependency'
workspace_use_absolute_case workspace-use-absolute-rooted-single '\dependency'

new_repo workspace-use-relative
write_mod go.mod $'module example.com/root\ngo 1.20'
printf '%s\n' 'go 1.20' '' 'use ../dependency' >"$case_root/go.work"
git -C "$case_root" add go.work
run_checker

new_repo malformed
write_mod go.mod $'go 1.20'
expect_failure run_checker

new_repo multiple
write_mod go.mod $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => example.com/fork v1.2.3'
mkdir -p "$case_root/nested"
printf '%s\n' $'module example.com/nested\ngo 1.20\n\nreplace example.com/dependency => ../dependency' >"$case_root/nested/go.mod"
git -C "$case_root" add nested/go.mod
expect_failure run_checker
printf '%s\n' $'module example.com/nested\ngo 1.20\n\nreplace example.com/dependency => example.com/fork v1.2.3' >"$case_root/nested/go.mod"
git -C "$case_root" add nested/go.mod
run_checker

new_repo index-divergence
write_mod go.mod $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => example.com/fork v1.2.3'
printf '%s\n' $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => ../dependency' >"$case_root/go.mod"
run_checker
git -C "$case_root" add go.mod
expect_status 1 run_checker

new_repo newline-path
odd_dir=$'odd\nmodule'
mkdir -p "$case_root/$odd_dir"
printf '%s\n' $'module example.com/odd\ngo 1.20\n\nreplace example.com/dependency => ../dependency' >"$case_root/$odd_dir/go.mod"
git -C "$case_root" add -- "$odd_dir/go.mod"
expect_failure run_checker

new_repo installer
write_mod go.mod $'module example.com/root\ngo 1.20\n\nreplace example.com/dependency => ../dependency'
expect_status 0 run_installer
[[ -x $case_root/.git/hooks/pre-commit ]] || fail 'installer did not create an executable native hook'
expect_status 1 run_native_hook
expect_status 0 run_installer
printf '# unexpected\n' >"$case_root/.git/hooks/pre-commit"
expect_status 2 run_installer

new_repo hooks-path
git -C "$case_root" config core.hooksPath custom-hooks
expect_diagnostic 2 GO_HOOK_CONFIGURED_PATH_DENIED run_installer

new_repo hardlink
peer_root=$(mktemp -d "$work_root/peer.XXXXXX")
mkdir -p "$peer_root/hooks/lib"
git -C "$peer_root" init -q
git -C "$peer_root" config user.email test@example.invalid
git -C "$peer_root" config user.name test
cp "$checker_source" "$peer_root/hooks/pre-commit-go-mod.sh"
cp "$installer_source" "$peer_root/hooks/install-pre-commit-go-mod.sh"
cp "$script_dir/../lib/eci-diagnostic.sh" "$peer_root/hooks/lib/eci-diagnostic.sh"
chmod 755 "$peer_root/hooks/pre-commit-go-mod.sh" "$peer_root/hooks/install-pre-commit-go-mod.sh"
expect_status 0 run_installer --repair-hardlink "$peer_root"
[[ "$case_root/hooks/pre-commit-go-mod.sh" -ef "$peer_root/hooks/pre-commit-go-mod.sh" ]] || fail 'hard-link repair did not share an inode'

printf 'PASS: staged go.mod policy, index/worktree separation, NUL-safe paths, installer conflicts, and hard-link repair\n'

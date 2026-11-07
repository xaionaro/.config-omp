#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
. "$script_dir/lib/eci-diagnostic.sh"

go_installer_deny() {
  local code="$1" operation="$2" subject="$3" reason="$4" remediation="$5"
  printf '%s\n' "$(eci_diagnostic_reason "$code" "PreCommit" "$operation" "$subject" "$reason" "$remediation")" >&2
}

usage() {
  go_installer_deny "GO_HOOK_USAGE_INVALID" "go-hook-install" \
    "cwd=$(eci_diagnostic_value "$PWD"),argv=$(eci_diagnostic_value "$*")" \
    "usage: $0 [--repair-hardlink PEER_REPOSITORY]" \
    'invoke the installer with no arguments or --repair-hardlink followed by a peer repository'
}

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || {
  go_installer_deny "GO_HOOK_REPOSITORY_REQUIRED" "go-hook-install" \
    "cwd=$(eci_diagnostic_value "$PWD")" \
    'go.mod hook installer: run from a Git repository' \
    'run the installer from the target Git repository, then retry'
  exit 2
}
repo_root=$(cd "$repo_root" && pwd -P)
checker=$repo_root/hooks/pre-commit-go-mod.sh
if [[ ! -f $checker || ! -x $checker ]]; then
  go_installer_deny "GO_HOOK_CHECKER_MISSING" "go-hook-install" \
    "repo=$(eci_diagnostic_value "$repo_root"),checker=$(eci_diagnostic_value "$checker")" \
    "go.mod hook installer: expected executable checker at $checker" \
    'restore an executable hooks/pre-commit-go-mod.sh, then retry'
  exit 2
fi

repair_hardlink=false
if [[ $# -eq 0 ]]; then
  :
elif [[ $# -eq 2 && $1 == --repair-hardlink ]]; then
  repair_hardlink=true
  peer_root=$(git -C "$2" rev-parse --show-toplevel 2>/dev/null) || {
    go_installer_deny "GO_HOOK_PEER_REPOSITORY_INVALID" "go-hook-install" \
      "peer=$(eci_diagnostic_value "$2")" \
      "go.mod hook installer: peer is not a Git repository: $2" \
      'provide a valid peer Git repository, then retry hard-link repair'
    exit 2
  }
  peer_root=$(cd "$peer_root" && pwd -P)
  peer_checker=$peer_root/hooks/pre-commit-go-mod.sh
  if [[ ! -f $peer_checker ]]; then
    go_installer_deny "GO_HOOK_PEER_CHECKER_MISSING" "go-hook-install" \
      "peer=$(eci_diagnostic_value "$peer_root"),checker=$(eci_diagnostic_value "$peer_checker")" \
      "go.mod hook installer: peer checker is missing: $peer_checker" \
      'restore the peer hooks/pre-commit-go-mod.sh, then retry hard-link repair'
    exit 2
  fi
  if ! cmp -s "$checker" "$peer_checker"; then
    go_installer_deny "GO_HOOK_CHECKER_MISMATCH" "go-hook-install" \
      "checker=$(eci_diagnostic_value "$checker"),peer_checker=$(eci_diagnostic_value "$peer_checker")" \
      "go.mod hook installer: refusing to link differing checker bytes ($checker, $peer_checker)" \
      'synchronize checker bytes in both repositories, then retry hard-link repair'
    exit 2
  fi
else
  usage
  exit 2
fi

configured_hooks_path=$(git config --get core.hooksPath 2>/dev/null || true)

git_dir=$(git rev-parse --git-dir 2>/dev/null) || {
  go_installer_deny "GO_HOOK_GIT_DIR_UNRESOLVED" "go-hook-install" \
    "repo=$(eci_diagnostic_value "$repo_root")" \
    'go.mod hook installer: unable to resolve the Git directory' \
    'repair the repository Git metadata, then retry'
  exit 2
}
if [[ $git_dir != /* ]]; then
  git_dir=$repo_root/$git_dir
fi
git_dir=$(cd "$git_dir" && pwd -P)
hooks_dir=$git_dir/hooks
if [[ -n $configured_hooks_path ]]; then
  configured_path=$configured_hooks_path
  if [[ $configured_path != /* ]]; then
    configured_path=$repo_root/$configured_path
  fi
  configured_parent=$(cd "$(dirname "$configured_path")" 2>/dev/null && pwd -P) || {
    go_installer_deny "GO_HOOK_CONFIGURED_PATH_INVALID" "go-hook-install" \
      "core.hooksPath=$(eci_diagnostic_value "$configured_hooks_path")" \
      "go.mod hook installer: refusing configured core.hooksPath=$configured_hooks_path; its parent is unavailable" \
      'make core.hooksPath resolve to an existing .git/hooks parent, then retry'
    exit 2
  }
  configured_path=$configured_parent/$(basename "$configured_path")
  if [[ $configured_path != "$hooks_dir" ]]; then
    go_installer_deny "GO_HOOK_CONFIGURED_PATH_DENIED" "go-hook-install" \
      "core.hooksPath=$(eci_diagnostic_value "$configured_hooks_path"),expected=$(eci_diagnostic_value "$hooks_dir")" \
      "go.mod hook installer: refusing configured core.hooksPath=$configured_hooks_path; unset it or use .git/hooks" \
      'unset core.hooksPath or configure the default .git/hooks directory, then retry'
    exit 2
  fi
fi
effective_hooks_dir=$(git rev-parse --git-path hooks 2>/dev/null) || {
  go_installer_deny "GO_HOOK_PATH_UNRESOLVED" "go-hook-install" \
    "repo=$(eci_diagnostic_value "$repo_root")" \
    'go.mod hook installer: unable to resolve the effective hooks directory' \
    'repair Git hooks path resolution, then retry'
  exit 2
}
if [[ $effective_hooks_dir != /* ]]; then
  effective_hooks_dir=$repo_root/$effective_hooks_dir
fi
effective_hooks_dir=$(cd "$effective_hooks_dir" 2>/dev/null && pwd -P) || {
  go_installer_deny "GO_HOOK_PATH_UNAVAILABLE" "go-hook-install" \
    "hooks=$(eci_diagnostic_value "$effective_hooks_dir")" \
    "go.mod hook installer: effective hooks directory is unavailable: $effective_hooks_dir" \
    'create or restore the effective .git/hooks directory, then retry'
  exit 2
}
if [[ $effective_hooks_dir != "$hooks_dir" ]]; then
  go_installer_deny "GO_HOOK_PATH_NONDEFAULT" "go-hook-install" \
    "hooks=$(eci_diagnostic_value "$effective_hooks_dir"),expected=$(eci_diagnostic_value "$hooks_dir")" \
    "go.mod hook installer: effective hooks directory is non-default: $effective_hooks_dir" \
    'use the default .git/hooks directory, then retry'
  exit 2
fi
mkdir -p "$hooks_dir"

destination=$hooks_dir/pre-commit
if [[ -L $destination ]]; then
  go_installer_deny "GO_HOOK_DESTINATION_SYMLINK" "go-hook-install" \
    "destination=$(eci_diagnostic_value "$destination")" \
    "go.mod hook installer: refusing symlink at $destination" \
    'remove the symlink and create a regular managed hook, then retry'
  exit 2
fi

wrapper_tmp=$hooks_dir/.pre-commit.go-mod.$$
umask 077
cat >"$wrapper_tmp" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
exec "$repo_root/hooks/pre-commit-go-mod.sh"
EOF
chmod 755 "$wrapper_tmp"
trap 'rm -f -- "$wrapper_tmp"' EXIT HUP INT TERM
expected_digest=$(sha256sum "$wrapper_tmp" | cut -d' ' -f1)

if [[ -e $destination && ! -f $destination ]]; then
  go_installer_deny "GO_HOOK_DESTINATION_NONREGULAR" "go-hook-install" \
    "destination=$(eci_diagnostic_value "$destination")" \
    "go.mod hook installer: refusing non-regular existing hook $destination" \
    'replace the destination with a regular managed hook, then retry'
  exit 2
fi
if [[ -f $destination ]]; then
  if ! cmp -s "$wrapper_tmp" "$destination"; then
    go_installer_deny "GO_HOOK_DESTINATION_CONFLICT" "go-hook-install" \
      "destination=$(eci_diagnostic_value "$destination")" \
      "go.mod hook installer: refusing to overwrite unexpected existing hook $destination; move it aside or merge it, then rerun this installer" \
      'review and remove or merge the existing hook before retrying'
    exit 2
  fi
fi

if [[ $repair_hardlink == true && ! "$checker" -ef "$peer_checker" ]]; then
  link_tmp=$checker.hardlink.$$
  rm -f -- "$link_tmp"
  if ! ln "$peer_checker" "$link_tmp"; then
    go_installer_deny "GO_HOOK_HARDLINK_FAILED" "go-hook-install" \
      "checker=$(eci_diagnostic_value "$checker"),peer_checker=$(eci_diagnostic_value "$peer_checker")" \
      'go.mod hook installer: cannot hard-link checker files; both paths must share a filesystem' \
      'place both repositories on one filesystem or skip hard-link repair, then retry'
    rm -f -- "$link_tmp"
    exit 2
  fi
  chmod 755 "$link_tmp"
  mv -f -- "$link_tmp" "$checker"
fi
if [[ $repair_hardlink == true && ! "$checker" -ef "$peer_checker" ]]; then
  go_installer_deny "GO_HOOK_HARDLINK_VERIFY_FAILED" "go-hook-install" \
    "checker=$(eci_diagnostic_value "$checker"),peer_checker=$(eci_diagnostic_value "$peer_checker")" \
    'go.mod hook installer: checker hard-link repair did not produce one inode' \
    'verify both checker paths and retry hard-link repair'
  exit 2
fi

if [[ -f $destination ]]; then
  chmod 755 "$destination"
else
  mv -- "$wrapper_tmp" "$destination"
fi

actual_digest=$(sha256sum "$destination" | cut -d' ' -f1)
if [[ $actual_digest != "$expected_digest" || ! -x $destination ]]; then
  go_installer_deny "GO_HOOK_INSTALL_VERIFY_FAILED" "go-hook-install" \
    "destination=$(eci_diagnostic_value "$destination"),digest=$(eci_diagnostic_value "$actual_digest")" \
    "go.mod hook installer: installed hook failed byte/mode verification: $destination" \
    'restore the expected executable hook bytes and mode, then retry'
  exit 2
fi
printf 'installed Go module pre-commit hook at %s\n' "$destination"

#!/usr/bin/env bash
# Run one hooks test (or run.sh) with a per-test temporary HOME.
#
# Usage: run-isolated.sh <test-file-in-this-dir> [args...]
#
# The per-test work area lives under $XDG_RUNTIME_DIR (mode 0700). Its ancestors
# must be private because the planner refuses a bin directory with a
# group/other-writable ancestor, and ~/tmp is group-writable.
# HOME is set to <work>/home. The runtime is copied to <work>/home/.omp/agent/eci
# and the test runs from that copy, so ROOT equals the HOME-derived runtime path.
# The published Go helpers ($HOME/tmp/eci-runtime) are copied too. ~/.kimi-code is
# a scratch copy of the Kimi peer (KIMI_CODE_HOME), not a link: the peer sync and
# some tests write into it, and a write through a link would reach the account.
# ~/.omp/agent/AGENTS.md is a
# symlink to the account's global instructions (read by the reviewer prompt) and
# ~/.omp/agent/skills to the account's skills tree (read by tests).
# ~/.omp/agent/eci-binding-contract.md is a symlink to the account's binding contract (read by tests).
# (~/.agent, ~/tmp/agent) resolve under <work> and are removed on exit. Go caches
# stay on account paths.
# The copied launchers/install-launchers.sh links ~/.local/bin/{eci,eci-active} and
# ~/bin/eci under <work>, so launcher lookups resolve the copy, not the account.
set -euo pipefail
umask 077

account_home="$(realpath -e -- "${HOME:?HOME must be set}")"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
runtime_root="$(cd "$here/../.." && pwd -P)"
# The source repository that owns runtime_root. Tests that compare against the
# source HEAD read it from ECI_SOURCE_REPO; a runtime outside a repository leaves it unset.
if source_repo="$(git -C "$runtime_root" rev-parse --show-toplevel 2>/dev/null)"; then
  export ECI_SOURCE_REPO="$source_repo"
fi
test_name="${1:?usage: run-isolated.sh <test-file-in-this-dir> [args...]}"
shift

export GOPATH="${GOPATH:-$account_home/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export GOCACHE="${GOCACHE:-$account_home/.cache/go-build}"

base="${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR must be set; it is the private (0700) work area}/eci-hooktest"
mkdir -p -m 700 "$base"
work="$(realpath -e -- "$(mktemp -d "$base/eci-hooktest.XXXXXX")")"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

mkdir -p "$work/home/.omp/agent" "$work/home/tmp/mtlog"
cp -a "$runtime_root" "$work/home/.omp/agent/eci"
if [ -d "$account_home/tmp/eci-runtime" ]; then
  cp -a "$account_home/tmp/eci-runtime" "$work/home/tmp/eci-runtime"
fi
# The Kimi peer copy keeps every entry except the 180 MB upstream bin/kimi; cache/
# and search-index/ stay empty (no ECI test reads them). ECI_ACCOUNT_KIMI_CODE_HOME
# names the account peer for the regression probe, test-run-isolated-kimi-peer.sh.
kimi_account="$account_home/.kimi-code"
kimi_home="$work/home/.kimi-code"
mkdir -p -m 700 "$kimi_home/bin"
shopt -s dotglob nullglob
for entry in "$kimi_account"/*; do
  name="${entry##*/}"
  case "$name" in
    bin)
      for tool in "$entry"/*; do
        [ "${tool##*/}" = kimi ] || cp -a -- "$tool" "$kimi_home/bin/"
      done ;;
    cache|search-index) mkdir -m 700 -- "$kimi_home/$name" ;;
    *) cp -a -- "$entry" "$kimi_home/" ;;
  esac
done
shopt -u dotglob nullglob
export KIMI_CODE_HOME="$kimi_home"
export ECI_ACCOUNT_KIMI_CODE_HOME="$kimi_account"
ln -s "$account_home/.omp/agent/AGENTS.md" "$work/home/.omp/agent/AGENTS.md"
ln -s "$account_home/.omp/agent/skills" "$work/home/.omp/agent/skills"
ln -s "$account_home/.omp/agent/eci-binding-contract.md" "$work/home/.omp/agent/eci-binding-contract.md"

# config.toml keys trust state by the account's canonical hooks.json path. This
# per-host rewrite points the copy's hooks.state keys at the copied hooks.json,
# so the copy names the hooks.json the test runs. Only headers starting with the
# account key prefix change, and only in the copy; the real tree is never
# written. The runtime sync will own this rewrite.
rewrite_copied_hooks_state_prefix() {
  local config="$1" from_prefix="$2" to_prefix="$3" line tmp
  [ -f "$config" ] || return 0
  tmp="$(mktemp "$work/config-rewrite.XXXXXX")"
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      "$from_prefix"*) printf '%s%s\n' "$to_prefix" "${line#"$from_prefix"}" ;;
      *) printf '%s\n' "$line" ;;
    esac
  done <"$config" >"$tmp"
  cat -- "$tmp" >"$config"
  rm -f -- "$tmp"
}
rewrite_copied_hooks_state_prefix "$work/home/.omp/agent/eci/config.toml" \
  "[hooks.state.\"$account_home/.omp/agent/eci/hooks.json:" \
  "[hooks.state.\"$work/home/.omp/agent/eci/hooks.json:"

export HOME="$work/home"
export TMPDIR="$HOME/tmp/mtlog"
export ECI_PROOF_ROOT="$HOME/tmp/agent/proof"
# Launcher assertions read $HOME/.local/bin/eci and $HOME/bin/eci, which a fresh HOME lacks.
# Install them from the copied tracked launchers (symlinks only); set -e stops the run on error.
bash "$HOME/.omp/agent/eci/launchers/install-launchers.sh" >/dev/null

rc=0
cd "$HOME/.omp/agent/eci"
bash "$HOME/.omp/agent/eci/hooks/tests/$test_name" "$@" || rc=$?
exit "$rc"

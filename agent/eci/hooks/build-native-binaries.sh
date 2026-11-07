#!/usr/bin/env bash
# Build the native ECI binaries into $HOME/.omp/agent/eci/bin and write their receipts.
# Each binary <name> gets <name>.sha256 containing one sha256sum-format line: "<sha256-hex>  <name>".
# eci-command-plan and eci-hook-helper are executed through lib/eci-native-binary.sh, which verifies each receipt before running it; eci-safe-import is allowlisted there but has no launcher caller in this tree.
# eci-worker-git is built and installed with a receipt, but it is NOT run through that launcher. The skill runs it directly, so its receipt is informational and is not verified at runtime.
# owned-command is built from agent/eci/tools/owned-command and receipted like eci-worker-git; the skill runs it directly, so its receipt is informational.
set -euo pipefail

: "${HOME:?HOME must be set}"
case "$HOME" in /*) ;; *) echo "build-native-binaries: HOME must be an absolute path" >&2; exit 1 ;; esac

hook_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
go=/usr/lib/go-1.26/bin/go
[ -x "$go" ] || { echo "build-native-binaries: $go is missing; install Go 1.26" >&2; exit 1; }

bin_dir="$HOME/.omp/agent/eci/bin"
mkdir -p -- "$bin_dir"
chmod 0755 -- "$bin_dir"
stage="$(mktemp -d -- "$bin_dir/.build.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT

names=(eci-command-plan eci-safe-import eci-hook-helper eci-worker-git owned-command)
for name in "${names[@]}"; do
  case "$name" in
    owned-command) module="$hook_dir/../tools/$name" ;;
    *) module="$hook_dir/lib/$name-go" ;;
  esac
  [ -f "$module/go.mod" ] || { echo "build-native-binaries: missing Go module $module" >&2; exit 1; }
  echo "build-native-binaries: building $name"
  (cd -- "$module" && GOPROXY=off GOTOOLCHAIN=local "$go" build -o "$stage/$name" .)
  chmod 0755 -- "$stage/$name"
  digest="$(sha256sum -- "$stage/$name")"
  printf '%s  %s\n' "${digest%% *}" "$name" >"$stage/$name.sha256"
done

for name in "${names[@]}"; do
  mv -f -- "$stage/$name" "$bin_dir/$name"
  mv -f -- "$stage/$name.sha256" "$bin_dir/$name.sha256"
  echo "build-native-binaries: installed $bin_dir/$name"
done

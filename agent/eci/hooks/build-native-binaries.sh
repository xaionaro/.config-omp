#!/usr/bin/env bash
# Build the native ECI binaries into $HOME/.omp/agent/eci/bin and write their receipts.
# Each binary <name> gets <name>.sha256 containing one sha256sum-format line: "<sha256-hex>  <name>".
# eci-command-plan and eci-hook-helper are executed through lib/eci-native-binary.sh, which verifies each receipt before running it; eci-safe-import is allowlisted there but has no launcher caller in this tree.
# eci-worker-git is built and installed with a receipt, but it is NOT run through that launcher. The skill runs it directly, so its receipt is informational and is not verified at runtime.
# eci-command-gate-mode is built only on Linux hosts: its sources carry //go:build linux. Other hosts skip it.
# Compatible with macOS system bash 3.2 and BSD userland: no realpath, mapfile, or GNU-only flags.
set -euo pipefail

: "${HOME:?HOME must be set}"
case "$HOME" in /*) ;; *) echo "build-native-binaries: HOME must be an absolute path" >&2; exit 1 ;; esac

hook_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"

# go must be an absolute PATH entry: a shell function or a relative PATH entry cannot build.
go="$(command -v go || true)"
case "$go" in
  /*) ;;
  '') echo "build-native-binaries: go not found in PATH; install Go 1.24 or newer" >&2; exit 1 ;;
  *) echo "build-native-binaries: go must resolve to an absolute path, got $go" >&2; exit 1 ;;
esac
host_os="$(GOWORK=off GOTOOLCHAIN=local "$go" env GOHOSTOS)"
host_arch="$(GOWORK=off GOTOOLCHAIN=local "$go" env GOHOSTARCH)"
[ -n "$host_os" ] && [ -n "$host_arch" ] || { echo "build-native-binaries: go env reported no host platform" >&2; exit 1; }

# sha256_cmd prints "<hex>  <file>" lines: sha256sum where present, otherwise macOS shasum.
if command -v sha256sum >/dev/null 2>&1; then
  sha256_cmd=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  sha256_cmd=(shasum -a 256)
else
  echo "build-native-binaries: sha256sum or shasum is required" >&2
  exit 1
fi

bin_dir="$HOME/.omp/agent/eci/bin"
mkdir -p -- "$bin_dir"
chmod 0755 -- "$bin_dir"
stage="$(mktemp -d -- "$bin_dir/.build.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT

# Builds target the host platform and clear caller target and toolchain overrides.
# GOPROXY is inherited, not forced off: a fresh macOS module cache must be able to download
# golang.org/x/sys. go.sum, -mod=readonly and the checksum database still verify every module.
names=(eci-command-plan eci-safe-import eci-hook-helper eci-worker-git eci-active eci-command-gate-mode)
built=()
for name in "${names[@]}"; do
  case "$name" in
    eci-active) module="$hook_dir/../tools/$name" ;;
    eci-command-gate-mode)
      if [ "$host_os" != linux ]; then
        echo "build-native-binaries: skipping $name (Linux-only source)"
        continue
      fi
      module="$hook_dir/lib/$name-go" ;;
    *) module="$hook_dir/lib/$name-go" ;;
  esac
  [ -f "$module/go.mod" ] || { echo "build-native-binaries: missing Go module $module" >&2; exit 1; }
  echo "build-native-binaries: building $name"
  (cd -- "$module" && env -u GOOS -u GOARCH -u GOARM -u GOARM64 -u GOAMD64 \
    GOOS="$host_os" GOARCH="$host_arch" CGO_ENABLED=0 GOWORK=off GOFLAGS= GOTOOLCHAIN=local \
    "$go" build -mod=readonly -trimpath -buildvcs=false -o "$stage/$name" .)
  chmod 0755 -- "$stage/$name"
  digest="$("${sha256_cmd[@]}" "$stage/$name")"
  printf '%s  %s\n' "${digest%% *}" "$name" >"$stage/$name.sha256"
  built+=("$name")
done

for name in ${built[@]+"${built[@]}"}; do
  mv -f -- "$stage/$name" "$bin_dir/$name"
  mv -f -- "$stage/$name.sha256" "$bin_dir/$name.sha256"
  echo "build-native-binaries: installed $bin_dir/$name"
done

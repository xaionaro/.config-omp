#!/usr/bin/env bash
# Build the native ECI binaries into $HOME/.omp/agent/eci/bin and write their receipts.
# Each binary <name> gets <name>.sha256 containing one sha256sum-format line: "<sha256-hex>  <name>".
# eci-command-plan, eci-safe-import and eci-hook-helper are gate binaries; lib/eci-native-binary.sh runs them only after verifying their receipts. eci-safe-import has no launcher caller in this tree.
# eci-worker-git is built with a receipt but not launcher-wired: the typed Worker Git route is unavailable during active ECI, so Workers use native Git, and the receipt is not verified at runtime.
# eci-active and owned-command are on-demand wrappers in bin/ that build from source on first use; this script does not build them.
# eci-command-gate-mode is built only on Linux hosts; its sources carry //go:build linux.
# Every built binary must pass eci_runtime_binary_is_native for this host before any file is installed. That check is ELF-only until the loader gains Mach-O support.
# Runs under macOS system bash 3.2 with BSD userland; no GNU-only utilities.
set -euo pipefail

: "${HOME:?HOME must be set}"
case "$HOME" in /*) ;; *) echo "build-native-binaries: HOME must be an absolute path" >&2; exit 1 ;; esac

hook_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
die() {
  printf 'build-native-binaries: %s\n' "$*" >&2
  exit 1
}
source "$hook_dir/lib/eci-native-binary.sh"

# Select a PATH executable; a shell function or a relative PATH entry cannot build.
go=$(command -v go) || die 'go not found on PATH; install Go 1.24 or newer and retry'
case "$go" in
  /*) [ -x "$go" ] || die "$go is not executable" ;;
  *) die "go resolves to '$go', not an absolute path" ;;
esac
# The host platform comes from the selected toolchain, queried outside any module.
{ IFS= read -r host_os && IFS= read -r host_arch; } \
  <<<"$(cd / && GOTOOLCHAIN=local GOFLAGS=' ' "$go" env GOHOSTOS GOHOSTARCH)" \
  || die "$go did not report GOHOSTOS and GOHOSTARCH"
[ -n "$host_os" ] && [ -n "$host_arch" ] || die "$go reported an empty host platform"

# Receipts use sha256sum where present (Linux) and shasum -a 256 otherwise (macOS).
if command -v sha256sum >/dev/null 2>&1; then
  sha256_command=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  sha256_command=(shasum -a 256)
else
  die 'sha256sum or shasum is required'
fi

names=(eci-command-plan eci-safe-import eci-hook-helper eci-worker-git)
if [ "$host_os" = linux ]; then
  names+=(eci-command-gate-mode)
else
  echo "build-native-binaries: skipping eci-command-gate-mode (Linux-only source)"
fi

bin_dir="$HOME/.omp/agent/eci/bin"
mkdir -p -- "$bin_dir"
chmod 0755 -- "$bin_dir"
stage="$(mktemp -d -- "$bin_dir/.build.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT

# Each build targets the host and clears caller tuning and target overrides. GOPROXY is inherited:
# a fresh module cache downloads through the caller's proxy, and go.sum with GOSUMDB verifies every
# module. Nothing here disables that verification: no GONOSUMDB, GONOSUMCHECK, insecure flag or sum-database override is set.
for name in "${names[@]}"; do
  module="$hook_dir/lib/$name-go"
  [ -f "$module/go.mod" ] || die "missing Go module $module"
  echo "build-native-binaries: building $name"
  (cd -- "$module" && env -u GO386 -u GOAMD64 -u GOARM -u GOARM64 -u GOMIPS -u GOMIPS64 -u GOPPC64 -u GORISCV64 -u GOWASM \
    GOOS="$host_os" GOARCH="$host_arch" CGO_ENABLED=0 GOWORK=off GOFLAGS=' ' GOTOOLCHAIN=local \
    "$go" build -mod=readonly -trimpath -buildvcs=false -o "$stage/$name" .) \
    || die "$name build failed; rerun: bash \"$hook_dir/build-native-binaries.sh\""
  chmod 0755 -- "$stage/$name"
  eci_runtime_binary_is_native "$stage/$name" \
    || die "$name: built $host_os/$host_arch binary is not native for this host"
  digest="$("${sha256_command[@]}" -- "$stage/$name")"
  printf '%s  %s\n' "${digest%% *}" "$name" >"$stage/$name.sha256"
done

for name in "${names[@]}"; do
  mv -f -- "$stage/$name" "$bin_dir/$name"
  mv -f -- "$stage/$name.sha256" "$bin_dir/$name.sha256"
  echo "build-native-binaries: installed $bin_dir/$name"
done

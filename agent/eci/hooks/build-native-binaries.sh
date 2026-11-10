#!/usr/bin/env bash
# Build the native ECI binaries into $HOME/.omp/agent/eci/bin and write their receipts.
# Usage: build-native-binaries.sh [NAME...]. With no NAME it builds every binary this host lists below. Each NAME must
# be one of those, at most once; then only the named binaries are built and installed.
# Each binary <name> gets <name>.sha256 containing one sha256sum-format line: "<sha256-hex>  <name>".
# eci-command-plan, eci-safe-import and eci-hook-helper are gate binaries; lib/eci-native-binary.sh runs them only after verifying their receipts. eci-safe-import has no launcher caller in this tree.
# eci-worker-git is built with a receipt but not launcher-wired: the typed Worker Git route is unavailable during active ECI, so Workers use native Git, and the receipt is not verified at runtime.
# eci-active and owned-command are on-demand wrappers in bin/ that build from source on first use; this script does not build them.
# eci-command-gate-mode is built only on Linux hosts; its sources carry //go:build linux.
# eci-verexec (Linux only) is the native verifier the Linux launcher execs; it checks its own image against eci-verexec.sha256 before any gate binary.
# Every built binary must pass eci_runtime_binary_is_native for this host (ELF on Linux, Mach-O on macOS) before any file is installed.
# Runs under macOS system bash 3.2 with BSD userland; no GNU-only utilities.
set -euo pipefail

: "${HOME:?HOME must be set}"
case "$HOME" in /*) ;; *) echo "build-native-binaries: HOME must be an absolute path" >&2; exit 1 ;; esac

hook_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
die() {
  printf 'build-native-binaries: %s\n' "$*" >&2
  exit 1
}
source "$hook_dir/lib/eci-native-provision.sh"

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
  names+=(eci-verexec eci-command-gate-mode)
elif [ "$#" -eq 0 ]; then
  echo "build-native-binaries: skipping eci-verexec and eci-command-gate-mode (Linux-only; macOS keeps the bash verifier)"
fi
if [ "$#" -gt 0 ]; then
  requested=' '
  for name in "$@"; do
    # One name per argument: a name with whitespace could match several list entries at once below.
    case "$name" in
      '' | *[[:space:]]*) die "binary name '$name' is empty or contains whitespace; pass one name per argument" ;;
    esac
    case " ${names[*]} " in
      *" $name "*) ;;
      *) die "unknown binary '$name' for $host_os; known: ${names[*]}" ;;
    esac
    case "$requested" in
      *" $name "*) die "binary '$name' is named more than once" ;;
    esac
    requested="$requested$name "
  done
  names=("$@")
fi

bin_dir="$HOME/.omp/agent/eci/bin"
if [ ! -d "$bin_dir" ]; then
  [ ! -L "$bin_dir" ] || die "unresolved bin directory $bin_dir"
  mkdir -p -- "$bin_dir"
  chmod -- 0755 "$bin_dir"
fi
bin_dir="$(cd -P -- "$bin_dir" && pwd -P)" || die 'cannot resolve bin directory'
[ -O "$bin_dir" ] || die "bin directory not owned by current user: $bin_dir"
lock="$bin_dir/.planner-importer.publish.lock"
eci_native_leaf_unresolved "$lock" && die "publication lock is not a user-owned regular file: $lock"
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
  chmod -- 0755 "$stage/$name"
  eci_runtime_binary_is_native "$stage/$name" \
    || die "$name: built $host_os/$host_arch binary is not native for this host"
  digest="$("${sha256_command[@]}" -- "$stage/$name")"
  printf '%s  %s\n' "${digest%% *}" "$name" >"$stage/$name.sha256"
done

# Cooperate with planner maintenance and runtime synchronization on the same lock.
exec 6>>"$lock"
if ! eci_file_lock_acquire_fd 6 nonblocking 2>/dev/null; then
  echo "build-native-binaries: waiting for publication lock" >&2
  eci_file_lock_acquire_fd 6 blocking || die "cannot acquire publication lock $lock"
fi
status=0
for name in "${names[@]}"; do
  binary="$bin_dir/$name"
  receipt="$binary.sha256"
  if eci_native_leaf_unresolved "$binary" || eci_native_leaf_unresolved "$receipt"; then
    echo "build-native-binaries: refused $binary or $receipt: unresolved or foreign leaf" >&2
    status=1
    continue
  fi
  if cmp -s -- "$stage/$name" "$binary" && cmp -s -- "$stage/$name.sha256" "$receipt" &&
    [ -n "$(find "$binary" -prune -type f -links 1 -perm 755 -user "$(id -u)" -print)" ]; then
    echo "build-native-binaries: unchanged $binary and $receipt"
    continue
  fi
  # A missing receipt makes an interrupted publication eligible for reprovisioning.
  rm -f -- "$receipt"
  mv -f -- "$stage/$name" "$binary"
  mv -f -- "$stage/$name.sha256" "$receipt"
  echo "build-native-binaries: installed $binary and $receipt"
done
eci_file_lock_release_fd 6 || status=1
exit "$status"

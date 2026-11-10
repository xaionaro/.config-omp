#!/usr/bin/env bash
# Build the native ECI binaries into $HOME/.omp/agent/eci/bin and write their receipts.
# Usage: build-native-binaries.sh [--preserve-conflict] [NAME...] or --gate-repair.
# The fixed gate repair builds only unavailable planner/verifier pairs and retains their previous leaves.
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
gate_repair=false
if [ "${1-}" = --gate-repair ]; then
  [ "$#" -eq 1 ] || die '--gate-repair accepts no names'
  shift
  gate_repair=true
  if eci_runtime_verify eci-command-plan; then
    echo 'build-native-binaries: Bash gate binaries are already verified'
    exit 0
  fi
fi
preserve_conflict=false
if [ "${1-}" = --preserve-conflict ]; then
  preserve_conflict=true
  shift
fi

# Keep the transaction prefix so peer sync excludes retained data; retired records
# use a non-conflict suffix and retain their source transaction name for inspection.
# Called under the publication lock only after both leaves have been validated.
retain_conflicted_pair() {
  local name="$1" binary="$2" receipt="$3" retained record txn
  retained="$(mktemp -d -- "$bin_dir/.planner-importer.txn.XXXXXX")" || return 1
  echo "build-native-binaries: retaining conflict for $name in $retained" >&2
  if [ -e "$binary" ]; then mv -- "$binary" "$retained/$name" || return 1; fi
  if [ -e "$receipt" ]; then mv -- "$receipt" "$retained/$name.sha256" || return 1; fi
  while eci_native_conflict_record "$name"; do
    record="$eci_native_conflict"
    txn="${record%/*}"
    mv -- "$record" "$retained/${txn##*/}.retired" || return 1
  done
}

# Classify only failures for which the verifier establishes a bounded repair.
gate_repair_target() {
  local reason="$ECI_RUNTIME_DENY_REASON" target detail binary receipt
  case "$reason" in
    "ECI native gate binary eci-command-plan is unavailable or unverified ("*) target=eci-command-plan ;;
    "ECI native gate binary eci-verexec is unavailable or unverified ("*)
      [ "$eci_runtime_platform" = Linux ] || return 1
      target=eci-verexec ;;
    *) return 1 ;;
  esac
  detail="${reason#*\(}"
  detail="${detail%%\)*}"
  binary="$HOME/.omp/agent/eci/bin/$target"
  receipt="$binary.sha256"
  if eci_native_leaf_unresolved "$binary"; then
    echo "build-native-binaries: refused unresolved or foreign binary: $binary" >&2
    return 1
  fi
  if eci_native_leaf_unresolved "$receipt"; then
    echo "build-native-binaries: refused unresolved or foreign receipt: $receipt" >&2
    return 1
  fi
  if [ -e "$binary" ]; then
    if [ -z "$(find "$binary" -prune -type f -links 1 ! -perm -020 ! -perm -002 -print)" ] || [ ! -x "$binary" ]; then
      echo "build-native-binaries: refused unsafe binary mode or links: $binary" >&2
      return 1
    fi
  fi
  case "$detail" in
    'is missing or a symlink') [ ! -e "$binary" ] && [ ! -L "$binary" ] || return 1 ;;
    'is not an ELF binary for this host'|'is not a Mach-O executable for this host'|'has no sha256 receipt'|'has a malformed sha256 receipt'|'sha256 does not match its receipt') ;;
    *) return 1 ;;
  esac
  gate_target="$target"
}

# A staged Linux verifier proves the existing bin chain before its missing self-check.
gate_missing_verifier_chain() {
  local reason rc=0
  reason="$("$stage/eci-verexec" --check --deny-fd 3 eci-command-plan 3>&1 >/dev/null)" || rc=$?
  if [ "$rc" -eq 126 ] || [ "$rc" -eq 127 ]; then
    echo 'build-native-binaries: cannot execute staged verifier; choose an executable TMPDIR and retry --gate-repair' >&2
    return 1
  fi
  [ "$rc" -ne 0 ] || return 1
  case "$reason" in
    'ECI native gate binary eci-verexec is unavailable or unverified (is missing or a symlink); '*) return 0 ;;
    *) printf '%s\n' "$reason" >&2; return 1 ;;
  esac
}
if [ "$gate_repair" = true ]; then
  gate_repair_target || die "unsafe or unsupported gate failure: $ECI_RUNTIME_DENY_REASON"
fi


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
if [ "$gate_repair" = true ]; then
  # The target was classified before requiring Go.
  names=("$gate_target")
  echo 'build-native-binaries: fixed gate repair uses current working-tree source and changes shared binaries for all sessions' >&2
  GIT_OPTIONAL_LOCKS=0 git -C "$hook_dir/lib" status -suall -- eci-command-plan-go eci-verexec-go >&2 || echo 'build-native-binaries: source status unknown' >&2
fi

bin_dir="$HOME/.omp/agent/eci/bin"
if [ "$gate_repair" = true ]; then
  [ -d "$bin_dir" ] || die "gate bin directory unavailable: $bin_dir"
  stage="$(mktemp -d "${TMPDIR:-/tmp}/.eci-gate-repair.XXXXXX")" || die 'cannot create repair stage'
else
  if [ ! -d "$bin_dir" ]; then
    [ ! -L "$bin_dir" ] || die "unresolved bin directory $bin_dir"
    mkdir -p -- "$bin_dir"
    chmod -- 0755 "$bin_dir"
  fi
  stage=''
fi
publish=''
trap '[ -z "$publish" ] || rm -rf -- "$publish"; [ -z "$stage" ] || rm -rf -- "$stage"' EXIT
bin_dir="$(cd -P -- "$bin_dir" && pwd -P)" || die 'cannot resolve bin directory'
[ -O "$bin_dir" ] || die "bin directory not owned by current user: $bin_dir"
lock="$bin_dir/.planner-importer.publish.lock"
eci_native_leaf_unresolved "$lock" && die "publication lock is not a user-owned regular file: $lock"
if [ "$gate_repair" != true ]; then stage="$(mktemp -d -- "$bin_dir/.build.XXXXXX")"; fi

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
    || { if [ "$gate_repair" = true ]; then die "$name build failed; rerun: bash \"$hook_dir/build-native-binaries.sh\" --gate-repair"; else die "$name build failed; rerun: bash \"$hook_dir/build-native-binaries.sh\""; fi; }
  chmod -- 0755 "$stage/$name"
  if [ "$gate_repair" = true ] && [ ! -x "$stage/$name" ]; then
    die "staged $name is not executable; choose an executable TMPDIR and retry --gate-repair"
  fi
  eci_runtime_binary_is_native "$stage/$name" \
    || die "$name: built $host_os/$host_arch binary is not native for this host"
  digest="$("${sha256_command[@]}" -- "$stage/$name")"
  printf '%s  %s\n' "${digest%% *}" "$name" >"$stage/$name.sha256"
done
if [ "$gate_repair" = true ] && [ "$gate_target" = eci-verexec ] && [ ! -e "$bin_dir/eci-verexec" ]; then
  gate_missing_verifier_chain || die 'cannot establish trusted bin chain for missing verifier'
fi

# Cooperate with planner maintenance and runtime synchronization on the same lock.
exec 6>>"$lock"
if ! eci_file_lock_acquire_fd 6 nonblocking 2>/dev/null; then
  echo "build-native-binaries: waiting for publication lock" >&2
  eci_file_lock_acquire_fd 6 blocking || die "cannot acquire publication lock $lock"
fi
if [ "$gate_repair" = true ]; then
  if eci_runtime_verify eci-command-plan; then
    eci_file_lock_release_fd 6 || exit 1
    echo 'build-native-binaries: Bash gate binaries are already verified'
    exit 0
  fi
  gate_repair_target || die "unsafe or unsupported gate failure: $ECI_RUNTIME_DENY_REASON"
  [ "$gate_target" = "${names[0]}" ] || die 'gate failure changed while building; retry'
  if [ "$gate_target" = eci-verexec ] && [ ! -e "$bin_dir/eci-verexec" ]; then gate_missing_verifier_chain || die 'untrusted bin chain'; fi
  binary="$bin_dir/$gate_target"
  receipt="$binary.sha256"
  retain_conflicted_pair "$gate_target" "$binary" "$receipt" || die "could not retain $gate_target; retry repair"
  # Copy the staged pair onto the publication filesystem before the existing rename order.
  publish="$(mktemp -d -- "$bin_dir/.build.XXXXXX")" || die 'cannot create publication stage'
  cp -- "$stage/$gate_target" "$stage/$gate_target.sha256" "$publish/" || die 'cannot stage gate repair'
  mv -- "$publish/$gate_target" "$binary"
  mv -- "$publish/$gate_target.sha256" "$receipt"
  rmdir -- "$publish"
  publish=''
  eci_file_lock_release_fd 6 || exit 1
  if eci_runtime_verify eci-command-plan; then
    echo 'build-native-binaries: Bash gate binaries verified'
    exit 0
  fi
  if [ "$gate_target" = eci-verexec ]; then
    gate_repair_target || die "gate verification failed: $ECI_RUNTIME_DENY_REASON"
    [ "$gate_target" = eci-command-plan ] || die "verifier repair failed: $ECI_RUNTIME_DENY_REASON"
    rm -rf -- "$stage"
    trap - EXIT
    exec "$BASH" "$hook_dir/build-native-binaries.sh" --gate-repair
  fi
  die "gate verification failed: $ECI_RUNTIME_DENY_REASON"
fi
status=0
for name in "${names[@]}"; do
  binary="$bin_dir/$name"
  receipt="$binary.sha256"
  # Recheck under the shared lock: a planner may have recorded a conflict while we built.
  if eci_native_conflict_record "$name" && [ "$preserve_conflict" != true ]; then
    eci_native_stuck_advisory "$name"
    status=1
    continue
  fi
  if eci_native_leaf_unresolved "$binary" || eci_native_leaf_unresolved "$receipt"; then
    echo "build-native-binaries: refused $binary or $receipt: unresolved or foreign leaf" >&2
    status=1
    continue
  fi
  if eci_native_conflict_record "$name"; then
    retain_conflicted_pair "$name" "$binary" "$receipt" || die "could not retain conflict for $name; retry repair"
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

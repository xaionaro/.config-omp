# Trusted launcher for the native ECI gate binaries.
# shellcheck shell=bash
# Only three names resolve, each from $HOME/.omp/agent/eci/bin. A binary runs only after verification:
# it must be a regular file owned by the current user and not group/other-writable, match the host
# executable format (ELF on Linux, a thin Mach-O for the host CPU type on macOS), and hash to the
# receipt written by hooks/build-native-binaries.sh. Ancestor directories must be owned by the current
# user or root and not group/other-writable. Linux executes the verified descriptor (/proc/self/fd/N);
# macOS has no such path, so it executes a private copy made through a descriptor.

# Host header cache. Reset on every sourcing so an inherited value cannot stand in for /proc/$$/exe.
eci_native_host_header=''
# Set when a verification fails; names the binary and the provisioning step.
ECI_RUNTIME_DENY_REASON=''
# Set when a verification succeeds on Linux; the descriptor stays open until eci_runtime_close_verified.
ECI_RUNTIME_BINARY_FD=''
# Set when a verification succeeds: the path to execute, /proc/self/fd/N on Linux or the private copy on macOS.
ECI_RUNTIME_BINARY_EXEC=''
# macOS only: the private directory that holds the copy. eci_runtime_close_verified removes it.
eci_runtime_copy_dir=''
# Host OS from uname -s, read at sourcing so that no environment value can select a loader path.
eci_runtime_platform="$(uname -s)" || eci_runtime_platform=''

eci_runtime_build_step='$HOME/.omp/agent/eci/hooks/build-native-binaries.sh'

eci_runtime_denial() {
  ECI_RUNTIME_DENY_REASON="ECI native gate binary $1 is unavailable or unverified ($2); provision it with \"$eci_runtime_build_step\" and retry"
}

# Resolve $HOME/.omp/agent/eci/bin/<name> for the three allowed names. Nothing is built here.
eci_runtime_artifact_path() {
  local name="${1:-}"
  case "${HOME:-}" in /*) ;; *) return 1 ;; esac
  case "$name" in eci-command-plan|eci-safe-import|eci-hook-helper) ;; *) return 1 ;; esac
  printf '%s/.omp/agent/eci/bin/%s\n' "$HOME" "$name"
}

# The file at $1 must be an ELF executable whose class, byte order and machine match the host.
# Headers are written into the private scratch directory $2 and read back, so no
# command substitution runs while the verified descriptor path is being observed.
eci_runtime_elf_matches_host() {
  local path="$1" scratch="$2" index
  local -a candidate=() host=()
  od -An -v -tx1 -w20 -N20 -- "$path" >"$scratch/candidate" 2>/dev/null || return 1
  if [ -z "$eci_native_host_header" ]; then
    od -An -v -tx1 -w20 -N20 -- "/proc/$$/exe" >"$scratch/host" 2>/dev/null || return 1
    IFS= read -r eci_native_host_header <"$scratch/host" || true
  fi
  read -r -a candidate <"$scratch/candidate" || true
  read -r -a host <<<"$eci_native_host_header"
  [ "${#candidate[@]}" -eq 20 ] && [ "${#host[@]}" -eq 20 ] || return 1
  [ "${candidate[*]:0:4}" = '7f 45 4c 46' ] || return 1
  # ELF class, byte order and machine identify the host execution format.
  # Ignore the executable/shared-object distinction and padding bytes.
  for index in 4 5 18 19; do
    [ "${candidate[$index]}" = "${host[$index]}" ] || return 1
  done
  case "${candidate[7]}" in 00|03) ;; *) return 1 ;; esac
}

# Path-based host check used by runtime sync (pre-migration source 575bb81:6-25); the descriptor loader does not use it. macOS checks the Mach-O header instead of the ELF header.
eci_runtime_binary_is_native() {
  case "$eci_runtime_platform" in
    Linux) ;;
    Darwin) _eci_runtime_binary_is_native_darwin "$1"; return ;;
    *) return 1 ;;
  esac
  local path="$1" candidate_header host_header index
  local -a candidate=() host=()
  [ -f "$path" ] && [ ! -L "$path" ] && [ -x "$path" ] || return 1
  candidate_header="$(od -An -v -tx1 -w20 -N20 -- "$path" 2>/dev/null)" || return 1
  if [ -z "$eci_native_host_header" ]; then
    eci_native_host_header="$(od -An -v -tx1 -w20 -N20 -- "/proc/$$/exe" 2>/dev/null)" || return 1
  fi
  host_header="$eci_native_host_header"
  read -r -a candidate <<<"$candidate_header"
  read -r -a host <<<"$host_header"
  [ "${#candidate[@]}" -eq 20 ] && [ "${#host[@]}" -eq 20 ] || return 1
  [ "${candidate[*]:0:4}" = '7f 45 4c 46' ] || return 1
  # ELF class, byte order and machine identify the host execution format.
  # Ignore the executable/shared-object distinction and padding bytes.
  for index in 4 5 18 19; do
    [ "${candidate[$index]}" = "${host[$index]}" ] || return 1
  done
  case "${candidate[7]}" in 00|03) ;; *) return 1 ;; esac
}

# Every directory from $1 up to / must be owned by the current user or root and must not be group/other-writable.
eci_runtime_directory_chain_is_trusted() {
  local dir="$1" uid owner mode
  uid="$(id -u)" || return 1
  while :; do
    read -r owner mode < <(stat -c '%u %a' -- "$dir" 2>/dev/null) || return 1
    [[ "$mode" =~ ^[0-7]{3,4}$ ]] || return 1
    [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || return 1
    (( 8#$mode & 022 )) && return 1
    [ "$dir" = / ] && return 0
    dir="$(dirname -- "$dir")"
  done
}

# Verify the open descriptor $2 for artifact $1 whose resolved path must be $3.
# Every observation of /proc/self/fd/$2 is redirected into the private scratch
# directory $4 and read back from there; no command or process substitution
# runs while the descriptor is open.  Hashing reads the verified descriptor
# through its path, so the bytes hashed are those of the opened inode.
eci_runtime_verify_fd() {
  local name="$1" fd="$2" expected_path="$3" scratch="$4" receipt_path="$5" fdpath="/proc/self/fd/$2"
  local target owner mode kind receipt_line expected actual hash_line

  readlink -- "$fdpath" >"$scratch/target" 2>/dev/null || { eci_runtime_denial "$name" "descriptor cannot be resolved"; return 1; }
  IFS= read -r target <"$scratch/target" || true
  [ "$target" = "$expected_path" ] || { eci_runtime_denial "$name" "resolves outside the verified bin directory"; return 1; }
  stat -L -c '%u %a %F' -- "$fdpath" >"$scratch/stat" 2>/dev/null || { eci_runtime_denial "$name" "cannot be stat'ed"; return 1; }
  read -r owner mode kind <"$scratch/stat" || true
  case "$kind" in 'regular file'|'regular empty file') ;; *) eci_runtime_denial "$name" "is not a regular file"; return 1 ;; esac
  [ "$owner" = "$EUID" ] || { eci_runtime_denial "$name" "is not owned by the current user"; return 1; }
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || { eci_runtime_denial "$name" "has an unreadable mode"; return 1; }
  (( 8#$mode & 022 )) && { eci_runtime_denial "$name" "is group- or other-writable"; return 1; }
  (( 8#$mode & 0111 )) || { eci_runtime_denial "$name" "is not executable"; return 1; }
  eci_runtime_elf_matches_host "$fdpath" "$scratch" || { eci_runtime_denial "$name" "is not an ELF binary for this host"; return 1; }

  [ -f "$receipt_path" ] && [ ! -L "$receipt_path" ] || { eci_runtime_denial "$name" "has no sha256 receipt"; return 1; }
  IFS= read -r receipt_line <"$receipt_path" || true
  local name_re='^[a-z0-9-]+$' receipt_re
  [[ "$name" =~ $name_re ]] || { eci_runtime_denial "$name" "is not an allowed artifact name"; return 1; }
  receipt_re="^([0-9a-f]{64})  ${name}\$"
  [[ "$receipt_line" =~ $receipt_re ]] || { eci_runtime_denial "$name" "has a malformed sha256 receipt"; return 1; }
  expected="${BASH_REMATCH[1]}"
  sha256sum -- "$fdpath" >"$scratch/sha256" 2>/dev/null || { eci_runtime_denial "$name" "cannot be hashed"; return 1; }
  IFS= read -r hash_line <"$scratch/sha256" || true
  actual="${hash_line%% *}"
  [ "$actual" = "$expected" ] || { eci_runtime_denial "$name" "sha256 does not match its receipt"; return 1; }
}

# Open and verify $HOME/.omp/agent/eci/bin/<name>. On success ECI_RUNTIME_BINARY_EXEC names the
# verified binary: on Linux the descriptor stays open in ECI_RUNTIME_BINARY_FD; on macOS it is a
# private copy. Release either with eci_runtime_close_verified. On failure ECI_RUNTIME_DENY_REASON is set.
# The platform switch comes first, so macOS never reaches the Linux body and its named descriptors.
# The private scratch directory is created before the descriptor opens and removed before return.
eci_runtime_open_verified() {
  case "$eci_runtime_platform" in
    Darwin) _eci_runtime_open_verified_darwin "${1:-}"; return ;;
  esac
  local name="${1:-}" scratch rc=0
  ECI_RUNTIME_BINARY_FD=''
  ECI_RUNTIME_DENY_REASON=''

  scratch="$(mktemp -d -- "${TMPDIR:-/tmp}/eci-runtime-verify.XXXXXX" 2>/dev/null)" || { eci_runtime_denial "$name" "private verification directory cannot be created"; return 1; }
  _eci_runtime_open_verified_in "$name" "$scratch" || rc=$?
  rm -rf -- "$scratch"
  return "$rc"
}

_eci_runtime_open_verified_in() {
  local name="$1" scratch="$2" path bin_dir real_bin fd receipt_path
  ECI_RUNTIME_BINARY_EXEC=''
  case "$eci_runtime_platform" in
    Linux) ;;
    *) eci_runtime_denial "$name" "host OS ${eci_runtime_platform:-unknown} is not supported"; return 1 ;;
  esac

  path="$(eci_runtime_artifact_path "$name")" || { eci_runtime_denial "$name" "not an allowed artifact name or HOME is not absolute"; return 1; }
  bin_dir="${path%/*}"
  receipt_path="$path.sha256"
  real_bin="$(realpath -e -- "$bin_dir" 2>/dev/null)" || { eci_runtime_denial "$name" "bin directory is missing"; return 1; }
  eci_runtime_directory_chain_is_trusted "$real_bin" || { eci_runtime_denial "$name" "bin directory or an ancestor is not owned by the current user or root, or is group/other-writable"; return 1; }
  if [ -L "$path" ] || [ ! -f "$path" ]; then
    eci_runtime_denial "$name" "is missing or a symlink"
    return 1
  fi
  if ! exec {fd}<"$path"; then
    eci_runtime_denial "$name" "cannot be opened"
    return 1
  fi
  if ! eci_runtime_verify_fd "$name" "$fd" "$real_bin/$name" "$scratch" "$receipt_path"; then
    exec {fd}<&-
    return 1
  fi
  ECI_RUNTIME_BINARY_FD="$fd"
  ECI_RUNTIME_BINARY_EXEC="/proc/self/fd/$fd"
}

# Releases what a successful open holds: the Linux descriptor and the macOS copy directory.
# Idempotent, so any exit path after an open may call it.
eci_runtime_close_verified() {
  if [ -n "$ECI_RUNTIME_BINARY_FD" ]; then
    exec {ECI_RUNTIME_BINARY_FD}<&-
  fi
  if [ -n "$eci_runtime_copy_dir" ]; then
    rm -rf -- "$eci_runtime_copy_dir"
  fi
  ECI_RUNTIME_BINARY_FD=''
  ECI_RUNTIME_BINARY_EXEC=''
  eci_runtime_copy_dir=''
}

# macOS has no /proc/self/fd. The verified file is copied through a descriptor into a private
# 0500 file, and every check after the copy inspects that copy, so the bytes hashed are the bytes
# executed. The copy directory comes from mktemp (mode 0700) and eci_runtime_close_verified removes
# it. This branch uses no bash 4 syntax, so it parses and runs under /bin/bash 3.2.

# CPU type bytes at Mach-O header offset 4 (little-endian) for this host. hw.optional.arm64 reports
# the hardware, so Apple silicon reads as arm64 even from a Rosetta shell, where uname -m says x86_64.
eci_runtime_darwin_host_cputype() {
  local arm64='' machine=''
  arm64="$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null)" || arm64=''
  if [ "$arm64" = 1 ]; then
    printf '%s\n' '0c 00 00 01'
    return 0
  fi
  machine="$(uname -m 2>/dev/null)" || return 1
  case "$machine" in
    arm64) printf '%s\n' '0c 00 00 01' ;;
    x86_64) printf '%s\n' '07 00 00 01' ;;
    *) return 1 ;;
  esac
}

# True when $1 is a thin 64-bit little-endian Mach-O executable of CPU type $2. The 16 header bytes
# must be "cf fa ed fe" (MH_MAGIC_64), the CPU type at bytes 4-7, and "02 00 00 00" (MH_EXECUTE) at
# bytes 12-15. A fat binary starts with "ca fe ba be" and fails the first comparison.
eci_runtime_macho_header_matches() {
  local path="$1" cputype="$2" header_text=''
  local IFS=' '
  local -a header=()
  header_text="$(od -An -v -tx1 -N16 -- "$path" 2>/dev/null)" || return 1
  read -r -a header <<<"$header_text" || true
  [ "${#header[@]}" -eq 16 ] || return 1
  [ "${header[*]:0:4}" = 'cf fa ed fe' ] || return 1
  [ "${header[*]:4:4}" = "$cputype" ] || return 1
  [ "${header[*]:12:4}" = '02 00 00 00' ] || return 1
}

# Denies with the Mach-O reason unless $2 passes eci_runtime_macho_header_matches for CPU type $3.
eci_runtime_macho_header_check() {
  eci_runtime_macho_header_matches "$2" "$3" || {
    eci_runtime_denial "$1" "is not a Mach-O executable for this host"
    return 1
  }
}

# Darwin twin of eci_runtime_directory_chain_is_trusted: BSD stat prints the owner (%u) and the octal
# permission bits (%Lp). Regular expressions live in variables so that bash 3.2 parses them.
eci_runtime_darwin_directory_chain_is_trusted() {
  local dir="$1" uid record owner='' mode='' mode_re='^[0-7]{3,4}$'
  uid="$(id -u)" || return 1
  while :; do
    record="$(stat -f '%u %Lp' -- "$dir" 2>/dev/null)" || return 1
    read -r owner mode <<<"$record" || return 1
    [[ $mode =~ $mode_re ]] || return 1
    [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || return 1
    (( 8#$mode & 022 )) && return 1
    [ "$dir" = / ] && return 0
    dir="$(dirname -- "$dir")"
  done
}

# The scratch and copy directories are created under the temporary directory, where another user must
# not be able to rename or replace them. Trusted: a directory the chain check accepts, or a root-owned
# sticky directory (the 1000 bit set) whose own parents pass the chain check. Returns a status only.
eci_runtime_darwin_temp_dir_is_trusted() {
  local dir="$1" record owner='' mode='' mode_re='^[0-7]{3,4}$'
  record="$(stat -f '%u %Lp' -- "$dir" 2>/dev/null)" || return 1
  read -r owner mode <<<"$record" || return 1
  [[ $mode =~ $mode_re ]] || return 1
  if [ "$owner" = 0 ] && (( 8#$mode & 01000 )); then
    [ "$dir" = / ] && return 0
    eci_runtime_darwin_directory_chain_is_trusted "$(dirname -- "$dir")"
    return
  fi
  eci_runtime_darwin_directory_chain_is_trusted "$dir"
}

# Darwin twin of eci_runtime_binary_is_native: a regular file, not a symlink, executable, and the
# Mach-O header test for the host CPU type.
_eci_runtime_binary_is_native_darwin() {
  local path="$1" cputype=''
  [ -f "$path" ] && [ ! -L "$path" ] && [ -x "$path" ] || return 1
  cputype="$(eci_runtime_darwin_host_cputype)" || return 1
  eci_runtime_macho_header_matches "$path" "$cputype"
}

# Opens $2 on fixed descriptor 8 inside a subshell, so this shell never holds a descriptor and the
# descriptor closes when the subshell exits. The subshell records the descriptor's stat and the
# path's stat in $3, and copies the bytes to $4. The caller checks the recorded values, which
# describe the inode that the copied bytes came from.
_eci_runtime_darwin_copy_inode() {
  local name="$1" path="$2" scratch="$3" copy="$4" status=0
  local IFS=' '
  local mode_re='^[0-7]{3,4}$' fdstat='' pathstat='' fd_dev fd_ino owner mode kind path_dev path_ino
  (
    exec 8<"$path" || exit 1
    stat -f '%d %i %u %Lp %HT' <&8 >"$scratch/fdstat" || exit 2
    stat -f '%d %i' -- "$path" >"$scratch/pathstat" || exit 2
    cat <&8 >"$copy" || exit 3
  ) || status=$?
  case "$status" in
    0) ;;
    1) eci_runtime_denial "$name" "cannot be opened"; return 1 ;;
    2) eci_runtime_denial "$name" "cannot be stat'ed"; return 1 ;;
    *) eci_runtime_denial "$name" "cannot be copied for execution"; return 1 ;;
  esac
  IFS= read -r fdstat <"$scratch/fdstat" || { eci_runtime_denial "$name" "cannot be stat'ed"; return 1; }
  IFS= read -r pathstat <"$scratch/pathstat" || { eci_runtime_denial "$name" "cannot be stat'ed"; return 1; }
  read -r fd_dev fd_ino owner mode kind <<<"$fdstat"
  read -r path_dev path_ino <<<"$pathstat"
  [ "$fd_dev" = "$path_dev" ] && [ "$fd_ino" = "$path_ino" ] || { eci_runtime_denial "$name" "resolves outside the verified bin directory"; return 1; }
  case "$kind" in 'Regular File') ;; *) eci_runtime_denial "$name" "is not a regular file"; return 1 ;; esac
  [ "$owner" = "$EUID" ] || { eci_runtime_denial "$name" "is not owned by the current user"; return 1; }
  [[ $mode =~ $mode_re ]] || { eci_runtime_denial "$name" "has an unreadable mode"; return 1; }
  (( 8#$mode & 022 )) && { eci_runtime_denial "$name" "is group- or other-writable"; return 1; }
  (( 8#$mode & 0111 )) || { eci_runtime_denial "$name" "is not executable"; return 1; }
  chmod 0500 -- "$copy" || { eci_runtime_denial "$name" "cannot be made executable"; return 1; }
}

# Checks the receipt "<64 lowercase hex>  <name>" that hooks/build-native-binaries.sh writes, then the
# sha256 of the copy, which is the file that will run. The regular expression lives in a variable so
# that bash 3.2 parses it.
_eci_runtime_darwin_receipt_verified() {
  local name="$1" receipt_path="$2" copy="$3" scratch="$4"
  local name_re='^[a-z0-9-]+$' receipt_re='' receipt_line='' expected='' hash_line='' actual=''
  [[ $name =~ $name_re ]] || { eci_runtime_denial "$name" "is not an allowed artifact name"; return 1; }
  receipt_re="^([0-9a-f]{64})  ${name}\$"
  [ -f "$receipt_path" ] && [ ! -L "$receipt_path" ] || { eci_runtime_denial "$name" "has no sha256 receipt"; return 1; }
  IFS= read -r receipt_line <"$receipt_path" || true
  [[ $receipt_line =~ $receipt_re ]] || { eci_runtime_denial "$name" "has a malformed sha256 receipt"; return 1; }
  expected="${BASH_REMATCH[1]}"
  shasum -a 256 -- "$copy" >"$scratch/sha256" 2>/dev/null || { eci_runtime_denial "$name" "cannot be hashed"; return 1; }
  IFS= read -r hash_line <"$scratch/sha256" || true
  actual="${hash_line%% *}"
  [ "$actual" = "$expected" ] || { eci_runtime_denial "$name" "sha256 does not match its receipt"; return 1; }
}

# Path checks, then the descriptor copy, then the Mach-O, receipt and hash checks on the copy.
_eci_runtime_darwin_stage_verified_copy() {
  local name="$1" scratch="$2" cputype="$3"
  local path bin_dir real_bin copy
  path="$(eci_runtime_artifact_path "$name")" || { eci_runtime_denial "$name" "not an allowed artifact name or HOME is not absolute"; return 1; }
  bin_dir="${path%/*}"
  real_bin="$(cd -P -- "$bin_dir" 2>/dev/null && pwd -P)" || { eci_runtime_denial "$name" "bin directory is missing"; return 1; }
  eci_runtime_darwin_directory_chain_is_trusted "$real_bin" || { eci_runtime_denial "$name" "bin directory or an ancestor is not owned by the current user or root, or is group/other-writable"; return 1; }
  if [ -L "$path" ] || [ ! -f "$path" ]; then
    eci_runtime_denial "$name" "is missing or a symlink"
    return 1
  fi
  copy="$eci_runtime_copy_dir/$name"
  _eci_runtime_darwin_copy_inode "$name" "$real_bin/$name" "$scratch" "$copy" || return 1
  eci_runtime_macho_header_check "$name" "$copy" "$cputype" || return 1
  _eci_runtime_darwin_receipt_verified "$name" "$path.sha256" "$copy" "$scratch"
}

# Darwin entry, reached only through the platform switch at the top of eci_runtime_open_verified.
# The scratch directory is created and removed here on every path.
_eci_runtime_open_verified_darwin() {
  local name="$1" scratch rc=0 temp_dir='' cputype=''
  ECI_RUNTIME_BINARY_EXEC=''
  cputype="$(eci_runtime_darwin_host_cputype)" || { eci_runtime_denial "$name" "host CPU type is not supported"; return 1; }
  temp_dir="$(cd -P -- "${TMPDIR:-/tmp}" 2>/dev/null && pwd -P)" || { eci_runtime_denial "$name" "temporary directory is missing"; return 1; }
  eci_runtime_darwin_temp_dir_is_trusted "$temp_dir" || { eci_runtime_denial "$name" "temporary directory is not trusted: owned by another user, or group/other-writable without a root-owned sticky bit"; return 1; }
  scratch="$(mktemp -d -- "$temp_dir/eci-runtime-verify.XXXXXX" 2>/dev/null)" || { eci_runtime_denial "$name" "private verification directory cannot be created"; return 1; }
  _eci_runtime_darwin_open_in "$name" "$scratch" "$temp_dir" "$cputype" || rc=$?
  rm -rf -- "$scratch"
  return "$rc"
}

# Creates the private copy directory, then stages the verified copy. A denied open removes that
# directory here, so nothing is left behind.
_eci_runtime_darwin_open_in() {
  local name="$1" scratch="$2" temp_dir="$3" cputype="$4"
  eci_runtime_copy_dir="$(mktemp -d -- "$temp_dir/eci-runtime-exec.XXXXXX" 2>/dev/null)" || { eci_runtime_denial "$name" "private copy directory cannot be created"; return 1; }
  if ! _eci_runtime_darwin_stage_verified_copy "$name" "$scratch" "$cputype"; then
    eci_runtime_close_verified
    return 1
  fi
  ECI_RUNTIME_BINARY_EXEC="$eci_runtime_copy_dir/$name"
}

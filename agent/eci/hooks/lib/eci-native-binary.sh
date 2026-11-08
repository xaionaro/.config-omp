# Trusted launcher for the native ECI gate binaries.
# shellcheck shell=bash
# Only three names resolve, each from $HOME/.omp/agent/eci/bin. A binary runs only after verification:
# it must be a regular file owned by the current user and not group/other-writable, match the host
# executable format (ELF on Linux, a thin Mach-O for the host CPU type on macOS), and hash to the
# receipt written by hooks/build-native-binaries.sh. Ancestor directories must be owned by the current
# user or root and not group/other-writable. Callers use eci_runtime_verexec (verify, then run) or
# eci_runtime_verify (verify only). On Linux the static bin/eci-verexec applies these checks natively
# and execs the verified descriptor (/proc/self/fd/N); there is no bash verifier on Linux. macOS has no /proc,
# so eci_runtime_open_verified verifies in bash and executes a private copy made through a descriptor.

# The host OS and host ELF header are set once per shell process. validate-bash.sh sources this file and then
# sources eci-proof-state.sh, which sources it again; the second sourcing keeps both values. The guard is
# element [1] of an array that holds the process id. The environment can import only scalars, so neither the
# guard nor the values it protects can be inherited; a fresh process always recomputes them.
if [ "${eci_native_binary_guard[1]:-}" != "$$" ]; then
  eci_native_binary_guard=("$$" "$$")
  # Host header cache, filled on first use from /proc/$$/exe.
  eci_native_host_header=''
  # Host OS name as uname -s prints it, from the readonly BASH_VERSINFO[5] (the build's machine triple),
  # so no environment value can select a loader path; uname runs only for an OS other than Linux or macOS.
  case "${BASH_VERSINFO[5]}" in
    *-linux*) eci_runtime_platform=Linux ;;
    *-darwin*) eci_runtime_platform=Darwin ;;
    *) eci_runtime_platform="$(uname -s)" || eci_runtime_platform='' ;;
  esac
fi
# Set when a verification fails; names the binary and the provisioning step.
ECI_RUNTIME_DENY_REASON=''
# Set when a verification succeeds on macOS: the path of the private copy to execute.
ECI_RUNTIME_BINARY_EXEC=''
# macOS only: the private directory that holds the copy. eci_runtime_close_verified removes it.
eci_runtime_copy_dir=''

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

# Run a verified gate binary: eci_runtime_verexec NAME DENY_FILE|- [-- ARGS...]. The binary runs as a
# child with this shell's stdin, stdout and stderr; the status is the binary's. A denial is status 125
# with ECI_RUNTIME_DENY_REASON set; a binary that itself exits 125 leaves the reason empty.
# Linux: bin/eci-verexec, after a self-check of its own image, verifies the target and execs the
# verified descriptor through /proc/self/fd/N; it writes the denial on descriptor 3, which goes to DENY_FILE, or with "-" is
# captured by a command substitution while stdout passes through on descriptor 4. The shell holds no
# descriptor afterwards. Only builtin tests guard the verifier itself: it lives in the bin directory it
# checks, as this file lives in the hooks directory, and is trusted the same way.
# macOS keeps eci_runtime_open_verified and runs the private copy. Parses and runs under bash 3.2.
eci_runtime_verexec() {
  local name="${1:-}" deny_file="${2:-}" rc=0
  ECI_RUNTIME_DENY_REASON=''
  if [ "$#" -ge 2 ]; then shift 2; else shift "$#"; fi
  if [ "${1:-}" = -- ]; then shift; fi
  if [ "$eci_runtime_platform" = Darwin ]; then
    eci_runtime_open_verified "$name" || return 125
    "$ECI_RUNTIME_BINARY_EXEC" ${1+"$@"} || rc=$?
    eci_runtime_close_verified
    return "$rc"
  fi
  _eci_runtime_verifier "$name" || return 125
  if [ "$deny_file" = - ]; then
    { ECI_RUNTIME_DENY_REASON="$("$eci_runtime_verifier_path" --deny-fd 3 "$name" -- ${1+"$@"} 3>&1 1>&4 4>&-)"; } 4>&1 || rc=$?
  else
    "$eci_runtime_verifier_path" --deny-fd 3 "$name" -- ${1+"$@"} 3>"$deny_file" || rc=$?
    if [ "$rc" -eq 125 ]; then
      IFS= read -r ECI_RUNTIME_DENY_REASON <"$deny_file" || true
    fi
  fi
  [ "$rc" -eq 125 ] || ECI_RUNTIME_DENY_REASON=''
  return "$rc"
}

# Verify bin/NAME without running it: eci_runtime_verify NAME. Status 0 accepts; otherwise
# ECI_RUNTIME_DENY_REASON is set. Linux runs eci-verexec --check; macOS opens and releases the copy.
eci_runtime_verify() {
  local name="${1:-}" rc=0
  ECI_RUNTIME_DENY_REASON=''
  if [ "$eci_runtime_platform" = Darwin ]; then
    eci_runtime_open_verified "$name" || return 1
    eci_runtime_close_verified
    return 0
  fi
  _eci_runtime_verifier "$name" || return 1
  ECI_RUNTIME_DENY_REASON="$("$eci_runtime_verifier_path" --check --deny-fd 3 "$name" 3>&1 >/dev/null)" || rc=$?
  if [ "$rc" -ne 0 ] && [ -z "$ECI_RUNTIME_DENY_REASON" ]; then
    eci_runtime_denial eci-verexec "cannot be opened"
  fi
  [ "$rc" -eq 0 ] || return 1
  ECI_RUNTIME_DENY_REASON=''
}

# Linux and other non-macOS hosts: sets eci_runtime_verifier_path to bin/eci-verexec, or denies for
# NAME. The verifier gets builtin tests only; it checks its own image before anything else.
_eci_runtime_verifier() {
  eci_runtime_verifier_path=''
  case "$eci_runtime_platform" in
    Linux) ;;
    *) eci_runtime_denial "$1" "host OS ${eci_runtime_platform:-unknown} is not supported"; return 1 ;;
  esac
  case "${HOME:-}" in
    /*) ;;
    *) eci_runtime_denial "$1" "not an allowed artifact name or HOME is not absolute"; return 1 ;;
  esac
  eci_runtime_verifier_path="$HOME/.omp/agent/eci/bin/eci-verexec"
  if [ ! -f "$eci_runtime_verifier_path" ] || [ -L "$eci_runtime_verifier_path" ] || [ ! -O "$eci_runtime_verifier_path" ] || [ ! -x "$eci_runtime_verifier_path" ]; then
    eci_runtime_denial eci-verexec "is missing or a symlink"
    return 1
  fi
}

# macOS only: open and verify $HOME/.omp/agent/eci/bin/<name> into a private copy named by
# ECI_RUNTIME_BINARY_EXEC; release it with eci_runtime_close_verified. A new open first releases any
# earlier one, so a failed open never leaves EXEC naming an earlier binary. On failure
# ECI_RUNTIME_DENY_REASON is set. Any other host denies; Linux callers use eci_runtime_verify.
eci_runtime_open_verified() {
  eci_runtime_close_verified
  case "$eci_runtime_platform" in
    Darwin) _eci_runtime_open_verified_darwin "${1:-}"; return ;;
  esac
  eci_runtime_denial "${1:-}" "host OS ${eci_runtime_platform:-unknown} is not supported"
  return 1
}

# Releases what a successful open holds: the macOS copy directory. Idempotent, so any exit path
# after an open may call it.
eci_runtime_close_verified() {
  if [ -n "$eci_runtime_copy_dir" ]; then
    rm -rf -- "$eci_runtime_copy_dir"
  fi
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

# Every directory from $1 up to / must be owned by the current user or root and not group/other-writable
# (macOS; eci-verexec applies the same rule on Linux). BSD stat prints the owner (%u) and the octal
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
  # BSD %Mp carries the suid, sgid and sticky bits; %Lp carries only the permission bits.
  record="$(stat -f '%u %Mp%Lp' -- "$dir" 2>/dev/null)" || return 1
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
  chmod -- 0500 "$copy" || { eci_runtime_denial "$name" "cannot be made executable"; return 1; }
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

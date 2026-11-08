# shellcheck shell=bash
# Sourced by hooks tests: the portable tool forms of hooks/lib/eci-portable.sh, plus
# test-only helpers for GNU-only forms the tests used. Bash 3.2 compatible.
#
#   eci_sha256 / eci_realpath_* / eci_stat   from hooks/lib/eci-portable.sh
#   eci_sed_inplace SCRIPT FILE              sed SCRIPT FILE, written back to FILE (GNU sed -i);
#                                            mode is kept; fails and leaves FILE untouched on error
#   eci_sha256_hex FILE                      the 64-hex digest alone
#   eci_mtime FILE                           modification time with sub-second digits, for
#                                            equality checks only (GNU %y / BSD %Fm text)
#   eci_user_home                            the current user's home from the user database
#                                            (~user tilde expansion; getent is absent on macOS)
#   eci_tree_listing DIR DEPTH               sorted "relpath:type:size" lines for entries up to
#                                            DEPTH below DIR (find -printf '%P:%y:%s' stand-in)
#   eci_test_run_with_deadline SECS CMD...   CMD with stdin passed through, in its own process
#                                            group; at SECS (whole seconds) TERM to the group,
#                                            KILL 5 s later. CMD's status, or 124 on timeout
#                                            (GNU timeout stand-in)

. "${BASH_SOURCE[0]%/*}/../lib/eci-portable.sh"

eci_sed_inplace() {
  local script="${1:?script}" file="${2:?file}" dir tmp mode
  case "$file" in */*) dir="${file%/*}" ;; *) dir=. ;; esac
  tmp="$(mktemp "${dir:-/}/.sed-inplace.XXXXXX")" || return 1
  if ! sed -e "$script" -- "$file" >"$tmp"; then
    rm -f -- "$tmp"
    return 1
  fi
  mode="$(eci_stat mode "$file")" && chmod "$mode" -- "$tmp" && mv -f -- "$tmp" "$file" || { rm -f -- "$tmp"; return 1; }
}

eci_sha256_hex() {
  local line
  line="$(eci_sha256 "${1:?file}")" || return 1
  printf '%s\n' "${line%% *}"
}

eci_mtime() {
  if [ "$eci_portable_platform" = Darwin ]; then
    stat -f %Fm -- "${1:?file}"
  else
    stat -c %y -- "${1:?file}"
  fi
}

eci_user_home() {
  local user home
  user="$(id -un)" || return 1
  case "$user" in *[!A-Za-z0-9._-]*|'') return 1 ;; esac
  eval "home=~$user"
  case "$home" in /*) printf '%s\n' "$home" ;; *) return 1 ;; esac
}

eci_tree_listing() {
  local dir="${1:?dir}" depth="${2:?depth}" path
  find "$dir" -mindepth 1 -maxdepth "$depth" | LC_ALL=C sort | while IFS= read -r path; do
    printf '%s:%s:%s\n' "${path#"$dir"/}" "$(eci_stat type "$path")" "$(eci_stat size "$path")"
  done
}

eci_test_run_with_deadline() {
  local secs="${1:?seconds}"
  shift
  (
    # set -m gives the job its own process group (pgid = pid), so the watchdog
    # reaches CMD's children too.
    set -m
    "$@" <&0 &
    set +m
    pid=$! status=0 fired=0
    # fds closed so an orphaned sleep can never hold the caller's pipes open.
    (
      fired=0
      trap 'kill "${nap:-}" 2>/dev/null; exit "$fired"' TERM
      sleep "$secs" & nap=$!; wait "$nap"
      # Set before the kill: once the group dies, the caller may TERM this watchdog at once.
      fired=124
      kill -TERM -- "-$pid" 2>/dev/null || exit 0
      sleep 5 & nap=$!; wait "$nap"
      kill -KILL -- "-$pid" 2>/dev/null || true
      exit 124
    ) </dev/null >/dev/null 2>&1 &
    watchdog=$!
    wait "$pid" 2>/dev/null || status=$?
    kill -TERM "$watchdog" 2>/dev/null || true
    wait "$watchdog" 2>/dev/null || fired=$?
    [ "$fired" != 124 ] || exit 124
    exit "$status"
  )
}

# shellcheck shell=bash
# Portable forms of the GNU-only tools the ECI hooks used: sha256sum, realpath -e,
# realpath -m, realpath -ms, realpath -m --relative-to and stat -c. Sourced, never
# executed. The form is chosen once, from uname -s read at sourcing, so no environment
# value selects it: Linux keeps the GNU tools, macOS uses shasum, shell path resolvers
# and BSD stat -f. Bash 3.2 compatible.
#
#   eci_sha256 [FILE...]             sha256sum-format lines ("<64hex>  <name>"); stdin when no FILE
#   eci_realpath_existing PATH...    realpath -e: physical absolute path of each existing PATH
#   eci_realpath_canonical PATH...   realpath -m: symlinks resolved where they exist, missing
#                                    components and .. normalised; nothing need exist
#   eci_realpath_lexical PATH        realpath -ms: absolute and normalised, no symlink resolved
#   eci_realpath_relative BASE PATH  realpath -m --relative-to=BASE PATH
#   eci_stat FIELD PATH              FIELD: mode (octal permission bits, e.g. 755), dev, uid, size,
#                                    type (GNU %F wording: regular file, directory, symbolic link, ...)
#
# Example: eci_stat mode /tmp prints 1777 on both hosts.

eci_portable_platform="$(uname -s)" || eci_portable_platform=''

if [ "$eci_portable_platform" = Darwin ]; then
  eci_sha256_command=(shasum -a 256)
else
  eci_sha256_command=(sha256sum)
fi

eci_sha256() {
  "${eci_sha256_command[@]}" "$@"
}

# Resolves every symlink in each PATH, like realpath -e, one line per PATH. A missing
# component, a symlink loop (more than 40 hops) or an empty PATH fails the call. Several
# PATHs cost one realpath process on Linux.
eci_realpath_existing() {
  if [ "$#" -gt 1 ]; then
    local one
    for one in "$@"; do [ -n "$one" ] || return 1; done
    if [ "$eci_portable_platform" != Darwin ]; then
      realpath -e -- "$@"
      return
    fi
    for one in "$@"; do eci_realpath_existing "$one" || return 1; done
    return 0
  fi
  local path="${1:-}" dir base target hops=0
  [ -n "$path" ] || return 1
  if [ "$eci_portable_platform" != Darwin ]; then
    realpath -e -- "$path"
    return
  fi
  [ -e "$path" ] || return 1
  while :; do
    if [ -d "$path" ] && [ ! -L "$path" ]; then
      (CDPATH='' cd -P -- "$path" 2>/dev/null && pwd -P)
      return
    fi
    case "$path" in
      */*) dir="${path%/*}"; base="${path##*/}"; [ -n "$dir" ] || dir=/ ;;
      *) dir=.; base="$path" ;;
    esac
    dir="$(CDPATH='' cd -P -- "$dir" 2>/dev/null && pwd -P)" || return 1
    if [ -L "$dir/$base" ]; then
      hops=$((hops + 1))
      [ "$hops" -le 40 ] || return 1
      target="$(readlink -- "$dir/$base")" || return 1
      case "$target" in
        /*) path="$target" ;;
        *) path="$dir/$target" ;;
      esac
      [ -e "$path" ] || return 1
      continue
    fi
    [ -e "$dir/$base" ] || return 1
    case "$dir" in
      /) printf '/%s\n' "$base" ;;
      *) printf '%s/%s\n' "$dir" "$base" ;;
    esac
    return
  done
}

# Walks PATH one component at a time from the physical working directory (relative
# input) or /. With follow=1 an existing symlink is replaced by its target, as
# realpath -m does; with follow=0 the walk is purely lexical, as realpath -ms does.
# More than 40 link expansions in total fail, like a loop: GNU detects only real
# cycles, so a longer non-looping chain fails closed here and resolves on Linux.
eci_portable_canonicalize() {
  local follow="$1" path="${2:-}" resolved rest comp target hops=0
  [ -n "$path" ] || return 1
  case "$path" in
    /*) resolved='' ;;
    *) resolved="$(pwd -P)" || return 1; [ "$resolved" != / ] || resolved='' ;;
  esac
  rest="$path"
  while [ -n "$rest" ]; do
    case "$rest" in
      */*) comp="${rest%%/*}"; rest="${rest#*/}" ;;
      *) comp="$rest"; rest='' ;;
    esac
    case "$comp" in
      ''|.) continue ;;
      ..) resolved="${resolved%/*}"; continue ;;
    esac
    if [ "$follow" = 1 ] && [ -L "$resolved/$comp" ]; then
      hops=$((hops + 1))
      [ "$hops" -le 40 ] || return 1
      target="$(readlink -- "$resolved/$comp")" || return 1
      case "$target" in /*) resolved='' ;; esac
      rest="$target${rest:+/$rest}"
      continue
    fi
    resolved="$resolved/$comp"
  done
  printf '%s\n' "${resolved:-/}"
}

eci_realpath_canonical() {
  local one
  [ "$#" -gt 0 ] || return 1
  for one in "$@"; do [ -n "$one" ] || return 1; done
  if [ "$eci_portable_platform" != Darwin ]; then
    realpath -m -- "$@"
    return
  fi
  for one in "$@"; do eci_portable_canonicalize 1 "$one" || return 1; done
}

eci_realpath_lexical() {
  if [ "$eci_portable_platform" != Darwin ]; then
    [ -n "${1:-}" ] || return 1
    realpath -ms -- "$1"
    return
  fi
  eci_portable_canonicalize 0 "${1:-}"
}

# PATH relative to BASE after both are canonicalised like realpath -m; "." when equal.
eci_realpath_relative() {
  local base path up='' rel
  [ -n "${1:-}" ] && [ -n "${2:-}" ] || return 1
  if [ "$eci_portable_platform" != Darwin ]; then
    realpath -m --relative-to="$1" -- "$2"
    return
  fi
  base="$(eci_portable_canonicalize 1 "$1")" || return 1
  path="$(eci_portable_canonicalize 1 "$2")" || return 1
  [ "$base" != / ] || base=''
  while :; do
    case "$path/" in
      "$base"/*) break ;;
    esac
    base="${base%/*}"
    up="../$up"
  done
  rel="${path#"$base"}"
  rel="${rel#/}"
  rel="$up$rel"
  rel="${rel%/}"
  printf '%s\n' "${rel:-.}"
}

eci_stat() {
  local field="${1:-}" path="${2:-}" format value
  [ -n "$path" ] || return 1
  if [ "$eci_portable_platform" = Darwin ]; then
    case "$field" in
      mode) format='%Mp%Lp' ;; dev) format='%d' ;; uid) format='%u' ;; size) format='%z' ;; type) format='%HT' ;;
      *) return 1 ;;
    esac
    value="$(stat -f "$format" -- "$path")" || return 1
    case "$field" in
      mode) value="$(printf '%o' "$((8#$value))")" || return 1 ;;
      type)
        case "$value" in
          'Regular File')
            value='regular file'
            [ "$(stat -f %z -- "$path")" != 0 ] || value='regular empty file'
            ;;
          Directory) value=directory ;;
          'Symbolic Link') value='symbolic link' ;;
          FIFO) value=fifo ;;
          Socket) value=socket ;;
          'Character Device') value='character special file' ;;
          'Block Device') value='block special file' ;;
        esac
        ;;
    esac
    printf '%s\n' "$value"
    return
  fi
  case "$field" in
    mode) format='%a' ;; dev) format='%d' ;; uid) format='%u' ;; size) format='%s' ;; type) format='%F' ;;
    *) return 1 ;;
  esac
  stat -c "$format" -- "$path"
}

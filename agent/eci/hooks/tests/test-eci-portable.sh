#!/usr/bin/env bash
# eci-portable.sh under a BSD-emulation PATH: uname reports Darwin, sha256sum and
# realpath are absent (exit 127), readlink rejects -f and stat rejects -c but serves
# BSD stat -f. Each portable function must match the GNU tool field for field, and a
# hook library that uses them (eci_hook_omp_transcript_status) must classify the same
# transcript the same way under both PATHs. The portable functions run under
# ECI_TEST_PORTABLE_BASH when set (for example a bash 3.2 build, the macOS /bin/bash),
# else under bash; the hook-library check always uses bash (the hook suite needs 4.4).
set -euo pipefail
umask 077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-portable.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
# Fixtures live under a physical path: the oracle realpath and the shell resolvers may
# resolve a relative base differently when an ancestor of the working directory is a link.
TMP_ROOT="$(CDPATH='' cd -P -- "$TMP_ROOT" && pwd -P)"
fail() { printf 'eci-portable: FAIL %s\n' "$*" >&2; exit 1; }
PB="${ECI_TEST_PORTABLE_BASH:-bash}"
"$PB" -c 'printf "eci-portable: functions under bash %s\n" "$BASH_VERSION"'

bsd="$TMP_ROOT/bsd-bin"
mkdir -p "$bsd"
gnu_stat="$(command -v stat)"
gnu_sha="$(command -v sha256sum)"
gnu_readlink="$(command -v readlink)"
for tool in sha256sum realpath; do
  printf '#!/bin/sh\necho "%s: command not found (BSD emulation)" >&2\nexit 127\n' "$tool" >"$bsd/$tool"
done
printf '#!/bin/sh\necho Darwin\n' >"$bsd/uname"
cat >"$bsd/shasum" <<EOF
#!/bin/sh
[ "\$1" = -a ] && [ "\$2" = 256 ] || { echo "shasum emulation: want -a 256" >&2; exit 2; }
shift 2
exec "$gnu_sha" "\$@"
EOF
cat >"$bsd/readlink" <<EOF
#!/bin/sh
case "\$1" in -f|-e|-m) echo "readlink: illegal option -- \${1#-}" >&2; exit 1 ;; esac
exec "$gnu_readlink" "\$@"
EOF
cat >"$bsd/stat" <<EOF
#!/bin/bash
[ "\$1" = -f ] || { echo "stat: illegal option -- \${1#-}" >&2; exit 1; }
format="\$2"; shift 2; [ "\${1:-}" = -- ] && shift
case "\$format" in
  %Lp) m="\$("$gnu_stat" -c %a -- "\$@")" || exit 1; printf '%o\n' "\$(( 8#\$m & 8#777 ))" ;;
  %Mp%Lp) m="\$("$gnu_stat" -c %a -- "\$@")" || exit 1; printf '%04o\n' "\$(( 8#\$m ))" ;;
  %d) exec "$gnu_stat" -c %d -- "\$@" ;;
  %u) exec "$gnu_stat" -c %u -- "\$@" ;;
  %z) exec "$gnu_stat" -c %s -- "\$@" ;;
  %HT) t="\$("$gnu_stat" -c %F -- "\$@")" || exit 1
    case "\$t" in
      'regular file'|'regular empty file') echo 'Regular File' ;;
      directory) echo Directory ;; 'symbolic link') echo 'Symbolic Link' ;; fifo) echo FIFO ;; *) echo "\$t" ;;
    esac ;;
  *) echo "stat emulation: unsupported format \$format" >&2; exit 2 ;;
esac
EOF
chmod 755 "$bsd"/*
BSD_PATH="$bsd:$PATH"

# Path fixtures: a symlinked directory, a symlink chain, a relative link, a dangling link,
# a sticky directory, an empty file, and a CDPATH decoy directory named like a real one.
fx="$TMP_ROOT/fx"
mkdir -p "$fx/real/sub" "$fx/sticky" "$fx/decoy/real/sub"
printf 'data\n' >"$fx/real/sub/file"
printf 'decoy\n' >"$fx/decoy/real/sub/file"
: >"$fx/empty"
chmod 1700 "$fx/sticky"
ln -s real "$fx/dirlink"
ln -s "$fx/dirlink/sub/file" "$fx/abs-link"
ln -s abs-link "$fx/chain"
ln -s missing "$fx/dangling"
mkfifo "$fx/pipe"

run_portable() {
  local path_value="$1"; shift
  PATH="$path_value" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; shift; "$@"' _ "$ROOT" "$@"
}

for p in "$fx/dirlink/sub/file" "$fx/chain" "$fx/dirlink" "$fx/real/sub/../sub" "$fx" /; do
  want="$(realpath -e -- "$p")"
  got="$(run_portable "$BSD_PATH" eci_realpath_existing "$p")" || fail "realpath $p failed"
  [ "$got" = "$want" ] || fail "realpath $p: got $got want $want"
done
(cd "$fx" && got="$(PATH="$BSD_PATH" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; eci_realpath_existing chain' _ "$ROOT")" &&
  [ "$got" = "$(realpath -e -- chain)" ]) || fail "relative realpath"
# An exported CDPATH must not redirect a relative lookup to a same-named directory elsewhere.
for rel in real real/sub/file; do
  (cd "$fx" && got="$(CDPATH="$fx/decoy" PATH="$BSD_PATH" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; eci_realpath_existing "$2"' _ "$ROOT" "$rel")" &&
    [ "$got" = "$(realpath -e -- "$rel")" ]) || fail "CDPATH redirected relative realpath $rel"
done
for p in "$fx/missing" "$fx/dangling" "$fx/real/missing/x" ""; do
  if run_portable "$BSD_PATH" eci_realpath_existing "$p" >/dev/null 2>&1; then fail "realpath accepted $p"; fi
done

# realpath -m / -ms / --relative-to: nothing need exist; -m follows existing links, -ms none.
ln -s ../real/sub "$fx/real/up"
for p in dirlink/sub dirlink/sub/../.. dirlink/missing/x/../y real/up/../.. "$fx/dirlink/./sub//" missing/../dirlink . / ../x "$fx/real/up"; do
  for pair in eci_realpath_canonical:-m eci_realpath_lexical:-ms; do
    fn="${pair%%:*}" flag="${pair#*:}"
    want="$(cd "$fx" && realpath "$flag" -- "$p")"
    got="$(cd "$fx" && PATH="$BSD_PATH" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; "$2" "$3"' _ "$ROOT" "$fn" "$p")" || fail "$fn $p failed"
    [ "$got" = "$want" ] || fail "$fn $p: got $got want $want"
  done
done
for pair in "real dirlink/sub" "dirlink/sub real" "real/sub real/sub" "/ $fx/real" "$fx/real/sub/x $fx/dirlink" "real/a/b real/c/d"; do
  base="${pair%% *}" target="${pair#* }"
  want="$(cd "$fx" && realpath -m --relative-to="$base" -- "$target")"
  got="$(cd "$fx" && PATH="$BSD_PATH" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; eci_realpath_relative "$2" "$3"' _ "$ROOT" "$base" "$target")" || fail "relative $pair failed"
  [ "$got" = "$want" ] || fail "relative $pair: got $got want $want"
done

# Several paths: one output line per path, in order; any failing path fails the call.
want="$(cd "$fx" && realpath -m -- dirlink/sub missing/x "$fx/chain")"
got="$(cd "$fx" && PATH="$BSD_PATH" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; eci_realpath_canonical dirlink/sub missing/x "$2"' _ "$ROOT" "$fx/chain")" || fail "multi canonical failed"
[ "$got" = "$want" ] || fail "multi canonical: got $got want $want"
want="$(cd "$fx" && realpath -e -- dirlink/sub "$fx/chain")"
got="$(cd "$fx" && PATH="$BSD_PATH" "$PB" -c '. "$1/hooks/lib/eci-portable.sh"; eci_realpath_existing dirlink/sub "$2"' _ "$ROOT" "$fx/chain")" || fail "multi existing failed"
[ "$got" = "$want" ] || fail "multi existing: got $got want $want"
if run_portable "$BSD_PATH" eci_realpath_existing "$fx/real" "$fx/missing" >/dev/null 2>&1; then fail "multi existing accepted a missing path"; fi
if run_portable "$BSD_PATH" eci_realpath_canonical "$fx/real" "" >/dev/null 2>&1; then fail "multi canonical accepted an empty path"; fi

for p in "$fx/real/sub/file" "$fx/real" "$fx/pipe" "$fx/sticky" "$fx/empty"; do
  for pair in mode:%a dev:%d uid:%u size:%s type:%F; do
    field="${pair%%:*}" format="${pair#*:}"
    want="$(stat -c "$format" -- "$p")"
    got="$(run_portable "$BSD_PATH" eci_stat "$field" "$p")" || fail "stat $field $p failed"
    [ "$got" = "$want" ] || fail "stat $field $p: got '$got' want '$want'"
  done
done
if run_portable "$BSD_PATH" eci_stat bogus "$fx/real" >/dev/null 2>&1; then fail "stat accepted an unknown field"; fi

want="$(sha256sum -- "$fx/real/sub/file")"
got="$(run_portable "$BSD_PATH" eci_sha256 -- "$fx/real/sub/file")" || fail "sha256 file failed"
[ "$got" = "$want" ] || fail "sha256 file: got $got want $want"
want="$(printf 'abc' | sha256sum)"
got="$(printf 'abc' | run_portable "$BSD_PATH" eci_sha256)" || fail "sha256 stdin failed"
[ "$got" = "$want" ] || fail "sha256 stdin: got $got want $want"

# A hook library caller: the omp transcript classifier resolves paths and sizes.
home="$TMP_ROOT/home"
mkdir -p "$home/.omp/agent/sessions/-proj-"
transcript="$home/.omp/agent/sessions/-proj-/main.jsonl"
printf '%s\n' '{"type":"session","version":3,"id":"aaaa-main","cwd":"/work"}' '{"type":"message"}' >"$transcript"
classify() {
  HOME="$home" PATH="$1" bash -c '. "$1/hooks/lib/eci-proof-state.sh" && eci_hook_omp_transcript_status "$2"' _ "$ROOT" "$transcript" 2>/dev/null
}
gnu_status="$(classify "$PATH")"
bsd_status="$(classify "$BSD_PATH")"
[ "$gnu_status" = main ] || fail "GNU PATH classification: $gnu_status"
[ "$bsd_status" = "$gnu_status" ] || fail "BSD PATH classification: $bsd_status, GNU: $gnu_status"

printf 'eci-portable: PASS\n'

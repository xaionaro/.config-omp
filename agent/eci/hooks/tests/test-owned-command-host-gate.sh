#!/usr/bin/env bash
# owned-command host gate: Linux and macOS are supported; any other host is refused before the lock, the build, or a launch.
# On a Linux host the Darwin cases stand in for macOS with a uname shim and a flock-backed lockf shim, the same stand-in
# TEST-DISPOSITION.md records for the bash 3.2 file-lock rows. On macOS the real tools run and no shim is installed.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/owned-command-host-gate.XXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT
host=$(uname -s)
real_uname=$(command -v uname)
real_flock=$(command -v flock || true)
refusal='owned-command: supported on Linux and macOS only; FreeBSD is not supported'

fail() {
  printf 'owned-command host gate: %s\n' "$1" >&2
  exit 1
}

# write_uname DIR NAME writes a uname that reports NAME for -s and delegates every other call.
write_uname() {
  cat >"$1/uname" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = -s ]; then printf '%s\n' '$2'; exit 0; fi
exec '$real_uname' "\$@"
EOF
  chmod 0755 "$1/uname"
}

# The checks need shasum on every host and flock on Linux, where flock backs the lockf stand-in. macOS runs the real
# lockf, so a macOS host without flock still runs every case.
need=shasum
[ "$host" = Linux ] && need="$need flock"
for tool in $need; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'owned-command host gate: SKIP: %s is not installed; the host-gate checks need it on this host\n' "$tool"
    exit 0
  fi
done

# An unsupported host is refused before the lock file, the cache directory, or any build.
mkdir -p "$tmp/freebsd"
write_uname "$tmp/freebsd" FreeBSD
rc=0
out="$(PATH="$tmp/freebsd:$PATH" OWNED_COMMAND_CACHE="$tmp/refused-cache" OWNED_COMMAND_SOURCE="$tmp/no-such-module" \
  "$root/bin/owned-command" -deadline 1s -receipt "$tmp/refused.json" -- true 2>&1)" || rc=$?
[ "$rc" -eq 1 ] || fail "FreeBSD exit $rc, want 1"
[ "$out" = "$refusal" ] || fail "FreeBSD message was: $out"
[ ! -e "$tmp/refused-cache" ] || fail "FreeBSD refusal created the cache directory"
[ ! -e "$tmp/refused.json" ] || fail "FreeBSD refusal wrote a receipt"

case $host in
  Linux | Darwin) ;;
  *)
    printf '%s\n' "owned-command host gate: SKIP Darwin cases on host $host"
    printf '%s\n' 'owned-command host gate: PASS'
    exit 0
    ;;
esac

path=$PATH
if [ "$host" = Linux ]; then
  mkdir -p "$tmp/darwin"
  write_uname "$tmp/darwin" Darwin
  # Linux stand-in for the Darwin lockf forms that eci-file-lock.sh calls: blocking acquire (2 arguments), nonblocking
  # acquire (4 arguments) and release (5 arguments). Each call is logged so the test can see that the stand-in ran.
  # The release form is `lockf -s -t 0 /dev/fd/N COMMAND`: argument 3 is the timeout 0, and argument 4 names fd N.
  cat >"$tmp/darwin/lockf" <<EOF
#!/usr/bin/env bash
set -eu
printf '%s\n' "\$*" >>'$tmp/lockf.calls'
case \$# in
  2) exec '$real_flock' -x "\$2" ;;
  4) exec '$real_flock' -n -x "\$4" ;;
  5) '$real_flock' -u "\${4#/dev/fd/}"; exec "\$5" ;;
  *) printf 'lockf stand-in: unsupported arguments\n' >&2; exit 2 ;;
esac
EOF
  chmod 0755 "$tmp/darwin/lockf"
  path="$tmp/darwin:$PATH"
fi

# A supported host proceeds past the gate, builds the source module, and runs the Go binary with the caller's arguments.
mkdir -p "$tmp/stub"
printf 'module example.test/owned-command-stub\n\ngo 1.24.0\n' >"$tmp/stub/go.mod"
cat >"$tmp/stub/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("owned-command-stub: ran")
	for _, arg := range os.Args[1:] {
		fmt.Println("arg:", arg)
	}
}
EOF
rc=0
out="$(PATH="$path" OWNED_COMMAND_SOURCE="$tmp/stub" OWNED_COMMAND_CACHE="$tmp/cache" \
  "$root/bin/owned-command" -deadline 1s -receipt "$tmp/stub.json" -- true 2>"$tmp/supported.err")" || rc=$?
[ "$rc" -eq 0 ] || fail "supported host exit $rc, want 0: $(cat "$tmp/supported.err")"
case $out in *'owned-command-stub: ran'*) ;; *) fail "the Go binary did not run: $out" ;; esac
case $out in *'arg: -deadline'*) ;; *) fail "caller arguments were not forwarded: $out" ;; esac
if grep -q 'not supported' "$tmp/supported.err"; then
  fail "a supported host was refused: $(cat "$tmp/supported.err")"
fi
if [ "$host" = Linux ]; then
  [ -s "$tmp/lockf.calls" ] || fail "the Linux lockf stand-in was never called, so the Darwin lock path did not run"
fi

# The release form must unlock the descriptor named in argument 4. Its argument 3, the timeout 0, must not be unlocked.
if [ "$host" = Linux ]; then
  lock="$tmp/release.lock"
  rc=0
  (
    exec 9>"$lock" || exit 3
    flock -x 9 || exit 3
    "$tmp/darwin/lockf" -s -t 0 "/dev/fd/9" /usr/bin/true || exit 4
    flock -n "$lock" true || exit 5
  ) || rc=$?
  case $rc in
    0) ;;
    4) fail "the lockf release form exited non-zero" ;;
    5) fail "the lockf release form did not unlock /dev/fd/9, the descriptor named in argument 4" ;;
    *) fail "could not take the release-test lock (exit $rc)" ;;
  esac
fi

# --resolve-executable prints exactly one absolute executable path on stdout; build notices go to stderr only,
# and the printed path runs the same Go binary with the caller's arguments.
rc=0
PATH="$path" OWNED_COMMAND_SOURCE="$tmp/stub" OWNED_COMMAND_CACHE="$tmp/cache-resolve" \
  "$root/bin/owned-command" --resolve-executable >"$tmp/resolve.out" 2>"$tmp/resolve.err" || rc=$?
[ "$rc" -eq 0 ] || fail "--resolve-executable exit $rc: $(cat "$tmp/resolve.err")"
if ! grep -q 'building' "$tmp/resolve.err"; then fail "a cold resolve printed no build notice on stderr"; fi
[ "$(wc -l <"$tmp/resolve.out" | tr -d ' ')" -eq 1 ] || fail "--resolve-executable must print exactly one stdout line: $(cat "$tmp/resolve.out")"
if grep -q 'building' "$tmp/resolve.out"; then fail "a build notice reached stdout"; fi
resolved=$(cat "$tmp/resolve.out")
case $resolved in /*) ;; *) fail "resolved path is not absolute: $resolved" ;; esac
[ -x "$resolved" ] || fail "resolved path is not executable: $resolved"
case $("$resolved" -deadline 1s -- true) in *'owned-command-stub: ran'*) ;; *) fail "resolved path did not run the Go binary" ;; esac

printf '%s\n' 'owned-command host gate: PASS'

#!/usr/bin/env bash
# owned-command supports Linux and macOS. Any other host is refused before the lock, the build, or a launch.
# On a Linux host the Darwin cases stand in for macOS with a uname shim and a flock-backed lockf shim, the same
# stand-in TEST-DISPOSITION.md records for the bash 3.2 file-lock rows. On macOS the real tools run and no shim is installed.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/owned-command-platform-gate.XXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT
host=$(uname -s)
real_uname=$(command -v uname)
real_flock=$(command -v flock || true)
refusal='owned-command: supported on Linux and macOS only; FreeBSD is not supported'

fail() {
  printf 'owned-command platform gate: %s\n' "$1" >&2
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
    printf '%s\n' "owned-command platform gate: SKIP Darwin cases on host $host"
    printf '%s\n' 'owned-command platform gate: PASS'
    exit 0
    ;;
esac

path=$PATH
if [ "$host" = Linux ]; then
  mkdir -p "$tmp/darwin"
  write_uname "$tmp/darwin" Darwin
  cat >"$tmp/darwin/lockf" <<EOF
#!/usr/bin/env bash
# Linux stand-in for the Darwin lockf forms that eci-file-lock.sh calls: blocking acquire, nonblocking acquire, release.
set -eu
case \$# in
  2) exec '$real_flock' -x "\$2" ;;
  4) exec '$real_flock' -n -x "\$4" ;;
  5) '$real_flock' -u "\${3#/dev/fd/}"; exec "\$5" ;;
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

printf '%s\n' 'owned-command platform gate: PASS'

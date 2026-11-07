#!/usr/bin/env bash
# Receipt-case table for eci_runtime_verify_fd in hooks/lib/eci-native-binary.sh.
# Each case writes one receipt beside a host-native binary, verifies it on fixed
# descriptor 8, and asserts the accept/deny outcome with the exact deny reason.
# A fixed descriptor number keeps the table runnable under bash 3.2 as well as
# bash 4.1 and newer.
# Usage: test-eci-native-binary-loader.sh [path/to/eci-native-binary.sh]
set -euo pipefail

tests_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
loader="${1:-$tests_dir/../lib/eci-native-binary.sh}"

if [ "$(uname -s)" != Linux ]; then
  printf '%s\n' 'SKIP: receipt-case table covers the Linux descriptor check; the macOS path is covered by the full loader test'
  exit 0
fi

base="$(mktemp -d -- "${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR must be set: the fixture base must be private}/eci-native-binary-loader.XXXXXX")"
trap 'rm -rf -- "$base"' EXIT
bin_dir="$base/bin"
scratch="$base/scratch"
mkdir -p -- "$bin_dir" "$scratch" "$base/src"

printf 'module eciloaderfixture\n\ngo 1.21\n' >"$base/src/go.mod"
cat >"$base/src/main.go" <<'EOF'
package main

import "os"

func main() {
	os.Stdout.WriteString("eci-native-binary-loader fixture\n")
}
EOF
binary="$bin_dir/eci-hook-helper"
(cd -- "$base/src" && env GOTOOLCHAIN=local CGO_ENABLED=0 go build -buildvcs=false -o "$binary" .)
chmod 0755 -- "$binary"
real_bin="$(cd -P -- "$bin_dir" && pwd -P)"
receipt="$real_bin/eci-hook-helper.sha256"
digest="$(sha256sum -- "$binary" | cut -d ' ' -f 1)"
zero_digest="$(printf '%064d' 0)"
upper_digest="$(printf '%s' "$digest" | tr 'a-f' 'A-F')"

# Parse the loader in a separate shell before sourcing it: a syntax error must fail the
# table, not pass it. The defined-function check catches a partly loaded file.
if ! "$BASH" -n "$loader"; then
  printf 'FAIL: %s does not parse under bash %s\n' "$loader" "$BASH_VERSION" >&2
  exit 1
fi
if ! . "$loader" || ! declare -F eci_runtime_verify_fd >/dev/null 2>&1; then
  printf 'FAIL: %s does not define eci_runtime_verify_fd under bash %s\n' "$loader" "$BASH_VERSION" >&2
  exit 1
fi

cases=0
failures=0
run_case() {  # run_case <label> <receipt line, or NONE> <verified name> <expected outcome>
  local label="$1" content="$2" name="$3" expected="$4" got reason status
  if [ "$content" = NONE ]; then
    rm -f -- "$receipt"
  else
    printf '%s\n' "$content" >"$receipt"
  fi
  ECI_RUNTIME_DENY_REASON=''
  exec 8<"$binary"
  if eci_runtime_verify_fd "$name" 8 "$real_bin/eci-hook-helper" "$scratch" "$receipt"; then
    got=accept
  else
    reason=${ECI_RUNTIME_DENY_REASON#*\(}
    reason=${reason%%\)*}
    got="deny|$reason"
  fi
  exec 8<&-
  cases=$((cases + 1))
  status=PASS
  if [ "$got" != "$expected" ]; then
    status=FAIL
    failures=$((failures + 1))
  fi
  printf 'CASE %-18s %s %s\n' "$label" "$got" "$status"
}

run_case valid-receipt "$digest  eci-hook-helper" eci-hook-helper accept
run_case wrong-hash "$zero_digest  eci-hook-helper" eci-hook-helper 'deny|sha256 does not match its receipt'
run_case wrong-name "$digest  eci-command-plan" eci-hook-helper 'deny|has a malformed sha256 receipt'
run_case extra-suffix "$digest  eci-hook-helper extra" eci-hook-helper 'deny|has a malformed sha256 receipt'
run_case uppercase-hex "$upper_digest  eci-hook-helper" eci-hook-helper 'deny|has a malformed sha256 receipt'
run_case missing-two-spaces "$digest eci-hook-helper" eci-hook-helper 'deny|has a malformed sha256 receipt'
run_case missing-receipt NONE eci-hook-helper 'deny|has no sha256 receipt'
run_case name-metachar "$digest  eci-hook" eci.hook 'deny|is not an allowed artifact name'
printf 'receipt-case table: %s cases, %s failures\n' "$cases" "$failures"
[ "$failures" -eq 0 ]

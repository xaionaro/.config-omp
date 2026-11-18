#!/usr/bin/env bash
# Pinned selectors must execute without invoking Go on warm calls.
# A failing Go shim records any unexpected invocation.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp_base="${ECI_TMPDIR:-${XDG_RUNTIME_DIR:-/tmp}}"
tmp="$(mktemp -d "$tmp_base/eci-no-go-run.XXXXXX")"
trap 'chmod -R -- u+w "$tmp" 2>/dev/null || true; rm -rf -- "$tmp"' EXIT


home="$tmp/home"
mkdir -p "$home/.omp/agent" "$home/.agent/context/sessions/no-go-run" "$tmp/proof/no-go-run" "$tmp/shim" "$tmp/cache"
ln -s -- "$root" "$home/.omp/agent/eci"

log="$tmp/go.log"
: >"$log"
cat >"$tmp/shim/go" <<EOF
#!/usr/bin/env bash
printf '%s\t%s\n' "\$PWD" "\$*" >>"$log"
exit 97
EOF
chmod 0755 "$tmp/shim/go"

run_env=(env -i "HOME=$home" "PATH=$tmp/shim:$PATH" "TMPDIR=$tmp" "XDG_CACHE_HOME=$tmp/cache" "ECI_PROOF_ROOT=$tmp/proof" "ECI_SESSION_ID=no-go-run")

# Warm all wrappers before logging the checked calls.
"${run_env[@]}" bash "$root/bin/eci-active" --help >/dev/null 2>"$tmp/warm-active.err"
"${run_env[@]}" bash "$root/bin/status-report" >/dev/null 2>&1 || true
"${run_env[@]}" bash "$root/bin/eci-requirement-assignment" >/dev/null 2>&1 || true

# Checked calls.
"${run_env[@]}" bash "$root/bin/eci-active" ledger-append --json '{"event":"decision","summary":"no-go-run probe"}' \
  >"$tmp/ledger.out" 2>"$tmp/ledger.err"
printf '{}' >"$tmp/request.json"
"${run_env[@]}" bash "$root/bin/eci-requirement-assignment" --ledger "$tmp/proof/no-go-run/requirement.yaml" \
  --request "$tmp/request.json" >"$tmp/ra.out" 2>"$tmp/ra.err" || true

# Pinned wrappers must not invoke even the Go launcher.
[ ! -s "$log" ] || { printf 'no-go-run: unexpected Go invocation:\n' >&2; cat "$log" >&2; exit 1; }
printf 'no-go-run: PASS (pinned wrappers invoked no Go)\n'

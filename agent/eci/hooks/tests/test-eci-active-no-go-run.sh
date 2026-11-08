#!/usr/bin/env bash
# No per-call go run or module resolution. ledger-append (which runs bin/status-report refresh) and the
# requirement-assignment helper (bin/eci-requirement-assignment, the exec path requirement.go uses) must
# run only built binaries once their wrappers are warm. A logging go shim sits first on PATH and records
# every go invocation the wrappers make. The library's `env` and `version` queries are the fingerprint and
# are allowed; run, build, list, mod, get, download, install, test, vet and tool are forbidden. The
# wrappers are warmed first, so their cold builds stay outside the checked calls. This test does not use
# run-isolated.sh.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp_base="${ECI_TMPDIR:-${XDG_RUNTIME_DIR:-/tmp}}"
tmp="$(mktemp -d "$tmp_base/eci-no-go-run.XXXXXX")"
trap 'chmod -R -- u+w "$tmp" 2>/dev/null || true; rm -rf -- "$tmp"' EXIT

real_go="$(type -P go)" || { printf 'no-go-run: go is required on PATH\n' >&2; exit 1; }
gocache="$("$real_go" env GOCACHE)"
gomodcache="$("$real_go" env GOMODCACHE)"

home="$tmp/home"
mkdir -p "$home/.omp/agent" "$home/.agent/context/sessions/no-go-run" "$tmp/proof/no-go-run" "$tmp/shim" "$tmp/cache"
ln -s -- "$root" "$home/.omp/agent/eci"

log="$tmp/go.log"
: >"$log"
cat >"$tmp/shim/go" <<EOF
#!/usr/bin/env bash
printf '%s\t%s\n' "\$PWD" "\$*" >>"$log"
exec "$real_go" "\$@"
EOF
chmod 0755 "$tmp/shim/go"

run_env=(env -i "HOME=$home" "PATH=$tmp/shim:$PATH" "TMPDIR=$tmp" "GOTMPDIR=$tmp" "XDG_CACHE_HOME=$tmp/cache" "GOCACHE=$gocache" "GOMODCACHE=$gomodcache" "ECI_PROOF_ROOT=$tmp/proof" "ECI_SESSION_ID=no-go-run")

# Warm-up. Cold builds happen here, outside the checked calls. The build key includes the go launcher's
# path, so the warm-up runs through the same shim as the checked calls.
"${run_env[@]}" bash "$root/bin/eci-active" --help >/dev/null 2>"$tmp/warm-active.err"
"${run_env[@]}" bash "$root/bin/status-report" >/dev/null 2>&1 || true
"${run_env[@]}" bash "$root/bin/eci-requirement-assignment" >/dev/null 2>&1 || true
: >"$log"

# Checked calls.
"${run_env[@]}" bash "$root/bin/eci-active" ledger-append --json '{"event":"decision","summary":"no-go-run probe"}' \
  >"$tmp/ledger.out" 2>"$tmp/ledger.err" || true
printf '{}' >"$tmp/request.json"
"${run_env[@]}" bash "$root/bin/eci-requirement-assignment" --ledger "$tmp/proof/no-go-run/requirement.yaml" \
  --request "$tmp/request.json" >"$tmp/ra.out" 2>"$tmp/ra.err" || true

# Analysis.
tab="$(printf '\t')"
forbidden_re='^(run|build|list|mod|get|download|install|test|vet|tool)( |$)'
checked=0
allowed=0
forbidden=0
helper_seen=0
status_seen=0
while IFS= read -r line; do
  cwd=${line%%"$tab"*}
  args=${line#*"$tab"}
  checked=$((checked + 1))
  case $args in
    env | env\ * | version | version\ *) allowed=$((allowed + 1)) ;;
  esac
  if printf '%s\n' "$args" | grep -Eq "$forbidden_re"; then
    forbidden=$((forbidden + 1))
    printf 'FORBIDDEN go invocation in %s: go %s\n' "$cwd" "$args" >&2
  fi
  case $cwd in
    *eci-requirement-assignment-go*) helper_seen=$((helper_seen + 1)) ;;
  esac
  case $cwd in
    */tools/status-report) status_seen=$((status_seen + 1)) ;;
  esac
done <"$log"

[ "$checked" -gt 0 ] || { printf 'no-go-run: the checked calls logged no go query; the wrappers did not run\n' >&2; exit 1; }
[ "$forbidden" -eq 0 ] || { printf 'no-go-run: %s forbidden go invocation(s) during the checked calls\n' "$forbidden" >&2; exit 1; }
[ "$allowed" -eq "$checked" ] || { printf 'no-go-run: %s go invocation(s) were neither env nor version\n' "$((checked - allowed))" >&2; cat "$log" >&2; exit 1; }
[ "$helper_seen" -gt 0 ] || { printf 'no-go-run: the requirement-assignment wrapper did not run\n' >&2; exit 1; }
[ "$status_seen" -gt 0 ] || { printf 'no-go-run: the status-report wrapper did not run for the ledger append\n' >&2; exit 1; }
printf 'no-go-run: PASS (%s go queries, all env or version, none forbidden; status-report and requirement-assignment wrappers ran)\n' "$checked"

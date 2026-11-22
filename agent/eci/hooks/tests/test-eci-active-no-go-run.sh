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

# expect_call LABEL STATUS MARKER COMMAND...: COMMAND exits with exactly STATUS and its combined
# output contains MARKER, which only the selected executable prints.
expect_call() {
  local label=$1 want=$2 marker=$3 rc=0
  shift 3
  "${run_env[@]}" "$@" >"$tmp/$label.out" 2>"$tmp/$label.err" || rc=$?
  if [ "$rc" != "$want" ] || ! grep -qF -- "$marker" "$tmp/$label.out" "$tmp/$label.err"; then
    printf 'no-go-run: %s exited %s (want %s) or lacks %s\n' "$label" "$rc" "$want" "$marker" >&2
    cat "$tmp/$label.out" "$tmp/$label.err" >&2
    exit 1
  fi
}

# The Go log covers every call below, the warm-up calls included.
expect_call warm-active 0 'ledger-append --json <event-object>' bash "$root/bin/eci-active" --help
expect_call warm-status 2 'usage: status-report validate|render' bash "$root/bin/status-report" --help
expect_call warm-assignment 1 'flag: help requested' bash "$root/bin/eci-requirement-assignment" --help

# The old pinned planner must not be invoked until a coordinated cutover.
expect_call denied-planner 1 'maintain-planner unavailable until coordinated cutover' \
  bash "$root/bin/eci-active" maintain-planner
for marker in 'build-native-binaries.sh'; do
  grep -qF "$marker" "$tmp/denied-planner.err" || {
    printf 'no-go-run: denied-planner missing %s\n' "$marker" >&2
    cat "$tmp/denied-planner.err" >&2
    exit 1
  }
done

# ledger-append records the probe in the session log it names, then its status refresh runs the
# selected status-report, which reports the missing session report. The pinned eci-active appends to
# a JSON Lines log and prints its path.
expect_call ledger 0 'ECI high-level log appended at EOF: ' bash "$root/bin/eci-active" ledger-append \
  --json '{"event":"decision","summary":"no-go-run probe"}'
ledger_log=$(sed -n 's/^ECI high-level log appended at EOF: //p' "$tmp/ledger.out")
[ -f "$ledger_log" ] && grep -qF '"summary":"no-go-run probe"' "$ledger_log" || {
  printf 'no-go-run: ledger-append did not record the probe\n' >&2
  exit 1
}
grep -qF 'status-report refresh: lstat ' "$tmp/ledger.err"
grep -qF 'latest-status-report.yaml' "$tmp/ledger.err"

printf '{}' >"$tmp/request.json"
expect_call assignment 1 'requirement-assignment: requirements, outcome, target, instructions, actor id/role and reason are required' \
  bash "$root/bin/eci-requirement-assignment" --ledger "$tmp/proof/no-go-run/requirement.yaml" --request "$tmp/request.json"

# Pinned wrappers must not invoke even the Go launcher.
[ ! -s "$log" ] || { printf 'no-go-run: unexpected Go invocation:\n' >&2; cat "$log" >&2; exit 1; }
printf 'no-go-run: PASS (pinned wrappers invoked no Go)\n'

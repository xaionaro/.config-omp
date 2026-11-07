#!/usr/bin/env bash
# Exercise the registered Go command hook and missing-binary recovery privately.

set -euo pipefail

source_root="$(cd -- "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
# Keep compiler caches outside the isolated HOME; only the binary is cold.
export GOMODCACHE="$(go env GOMODCACHE)"
export GOCACHE="$(go env GOCACHE)"
export GOPATH="$(go env GOPATH)"
fixture="$(mktemp -d "${HOME:?}/tmp/command-hook-go.XXXXXX")"
fixture="$(realpath -e -- "$fixture")"
trap 'chmod -R u+w -- "$fixture"; rm -r -- "$fixture"' EXIT
fixture_home="$fixture/home"
runtime="$fixture_home/.omp/agent/eci"
mkdir -p -- "$runtime/bin" "$fixture_home/tmp" "$fixture/proof/hook-test" \
  "$fixture/config/eci" "$fixture/state"
cp -a -- "$source_root/hooks" "$runtime/hooks"
cp -- "$source_root/bin/eci-runtime-sync" "$runtime/bin/eci-runtime-sync"
cp -- "$source_root/bin/eci-active" "$runtime/bin/eci-active"
cp -- "$source_root/hooks.json" "$runtime/hooks.json"
# A candidate launcher can be proved privately before live activation.
if [ -n "${COMMAND_HOOK_GO_LAUNCHER_SOURCE:-}" ]; then
  cp -- "$COMMAND_HOOK_GO_LAUNCHER_SOURCE" "$runtime/hooks/validate-bash.sh"
fi
printf 'enforcing\n' >"$fixture/config/eci/command-gate-mode"
printf 'scope: Go command-hook integration\ncwd: %s\nsession_id: hook-test\ncreated_utc: 2026-10-04T00:00:00Z\n' \
  "$source_root" >"$fixture/proof/hook-test/eci_active"

registration_count="$(jq '[.hooks.PreToolUse[] | select(.matcher == "^Bash$") | .hooks[] | select(.type == "command")] | length' "$runtime/hooks.json")"
[ "$registration_count" -eq 1 ]
launcher="$(jq -er '.hooks.PreToolUse[] | select(.matcher == "^Bash$") | .hooks[] | select(.type == "command") | .command' "$runtime/hooks.json")"
binary="$fixture_home/tmp/eci-runtime/eci/eci-command-plan"
# An executable cache entry can survive a move from another architecture.
# Corrupt only ELF e_machine in a private copy to reproduce that condition.
mkdir -p -- "${binary%/*}"
cp -- /bin/true "$binary"
printf '\377\377' | dd of="$binary" bs=1 seek=18 conv=notrunc status=none
chmod 755 -- "$binary"
cp -- "$binary" "${binary%/*}/eci-safe-import"
source "$runtime/hooks/lib/eci-native-binary.sh"
eci_runtime_binary_is_native "$(realpath -e -- /bin/true)"
! eci_runtime_binary_is_native "$binary"
ln -s /bin/true "$fixture/native-link"
! eci_runtime_binary_is_native "$fixture/native-link"
git -C "$source_root" rev-parse HEAD >"$fixture/source-head"
sha256sum -- "$runtime/hooks/lib/eci-command-plan-go/"*.go \
  "$runtime/hooks/lib/eci-command-plan-go/go.mod" \
  "$runtime/hooks/lib/eci-command-plan-go/go.sum" \
  "$runtime/hooks/lib/eci-native-binary.sh" "$runtime/hooks/lib/eci-runtime-sync.sh" \
  "$runtime/bin/eci-runtime-sync" "$runtime/bin/eci-active" \
  "$runtime/hooks/validate-bash.sh" "$runtime/hooks.json" \
  "$source_root/hooks/tests/test-command-hook-go.sh" >"$fixture/sources.sha256"
uname -a >"$fixture/environment"
go version >>"$fixture/environment"
printf 'GOMODCACHE=%s\nGOCACHE=%s\nGOPATH=%s\nlauncher=%s\n' \
  "$GOMODCACHE" "$GOCACHE" "$GOPATH" "$launcher" >>"$fixture/environment"

# run_callback executes the configured command with a real worker callback.
run_callback() {
  local name="$1" command="$2" started_uptime ended_uptime idle_seconds
  jq -cn --arg cwd "$source_root" --arg command "$command" \
    '{session_id:"hook-test",cwd:$cwd,tool_input:{command:$command}}' >"$fixture/$name.input"
  date -u '+start_utc=%Y-%m-%dT%H:%M:%S.%NZ' >"$fixture/$name.metadata"
  read -r started_uptime idle_seconds </proc/uptime
  if ! /usr/bin/time -f 'elapsed_seconds=%e user_seconds=%U system_seconds=%S' \
    -o "$fixture/$name.time" \
    env HOME="$fixture_home" ECI_HOME="$runtime" ECI_HOOK_IS_SUBAGENT=true \
      GOOS=windows GOARCH=arm64 GOAMD64=v4 GOARM=7 GOARM64=v9.0 \
      ECI_PROOF_ROOT="$fixture/proof" XDG_CONFIG_HOME="$fixture/config" \
      XDG_STATE_HOME="$fixture/state" \
      bash -c "$launcher" <"$fixture/$name.input" >"$fixture/$name.output" \
      2>"$fixture/$name.stderr"; then
    cat -- "$fixture/$name.stderr" >&2
    return 1
  fi
  read -r ended_uptime idle_seconds </proc/uptime
  date -u '+end_utc=%Y-%m-%dT%H:%M:%S.%NZ' >>"$fixture/$name.metadata"
  printf 'start_uptime_seconds=%s\nend_uptime_seconds=%s\n' \
    "$started_uptime" "$ended_uptime" >>"$fixture/$name.metadata"
  printf '%s ' "$name"
  cat -- "$fixture/$name.time"
}

run_callback incompatible-cat 'cat AGENTS.md'
[ ! -s "$fixture/incompatible-cat.output" ]
[ -x "$binary" ] || {
  printf 'FAIL: first callback did not repair its incompatible Go binary\n' >&2
  exit 1
}
"$binary" --hook <"$fixture/incompatible-cat.input" >"$fixture/native.output"
[ ! -s "$fixture/native.output" ]
binary_identity="$(stat -c '%i:%Y:%s' -- "$binary")"
run_callback warm-cat 'cat AGENTS.md'
[ ! -s "$fixture/warm-cat.output" ]
run_callback warm-diff 'git diff -- AGENTS.md'
[ ! -s "$fixture/warm-diff.output" ]
[ "$(stat -c '%i:%Y:%s' -- "$binary")" = "$binary_identity" ]
run_callback denied-add 'git add -A'
jq -e '.hookSpecificOutput.hookEventName == "PreToolUse" and .hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_WORKER_GIT_OWNERSHIP_DENIED"))' \
  "$fixture/denied-add.output" >/dev/null
run_callback denied-control "touch $fixture/proof/hook-test/eci_wait"
jq -e '.hookSpecificOutput.hookEventName == "PreToolUse" and .hookSpecificOutput.permissionDecision == "deny"' \
  "$fixture/denied-control.output" >/dev/null
# The importer has its own consumer, so repairing the Bash hook alone must
# not leave an incompatible importer permanently selected by that consumer.
importer="${binary%/*}/eci-safe-import"
printf '\377\377' | dd of="$importer" bs=1 seek=18 conv=notrunc status=none
printf 'native import recovery\n' >"$fixture/import-source"
env HOME="$fixture_home" ECI_HOME="$runtime" ECI_PROOF_ROOT="$fixture/proof" \
  bash -c 'source "$1/bin/eci-active"; safe_import_fixed_session_leaf "$2" "$3" singleton-manifest' \
    _ "$runtime" "$fixture/import-source" "$fixture/proof/hook-test"
cmp -- "$fixture/import-source" "$fixture/proof/hook-test/eci-required-critics.json"
eci_runtime_binary_is_native "$importer"
importer_identity="$(stat -c '%i:%Y:%s' -- "$importer")"
env HOME="$fixture_home" ECI_HOME="$runtime" ECI_PROOF_ROOT="$fixture/proof" \
  bash -c 'source "$1/bin/eci-active"; safe_import_fixed_session_leaf "$2" "$3" singleton-manifest' \
    _ "$runtime" "$fixture/import-source" "$fixture/proof/hook-test"
[ "$(stat -c '%i:%Y:%s' -- "$importer")" = "$importer_identity" ]
# Trace separately so syscall tracing does not affect the timing probes.
env HOME="$fixture_home" ECI_HOME="$runtime" ECI_HOOK_IS_SUBAGENT=true \
  ECI_PROOF_ROOT="$fixture/proof" XDG_CONFIG_HOME="$fixture/config" \
  XDG_STATE_HOME="$fixture/state" \
  strace -f -e trace=execve -o "$fixture/exec.trace" bash -c "$launcher" \
  <"$fixture/warm-cat.input" >"$fixture/traced-cat.output" 2>"$fixture/traced-cat.stderr"
[ ! -s "$fixture/traced-cat.output" ]
grep -E 'execve\("/proc/self/fd/[0-9]+",.*"--hook"' "$fixture/exec.trace" >/dev/null
if grep -E 'execve\("[^"]*/python[0-9.]*"' "$fixture/exec.trace"; then
  printf 'FAIL: registered callback executed Python\n' >&2
  exit 1
fi
git -C "$source_root" check-ignore --quiet -- hooks/lib/eci-command-plan-go/eci-command-plan
[ -z "$(git -C "$source_root" ls-files -- hooks/lib/eci-command-plan-go/eci-command-plan)" ]
if [ -n "${COMMAND_HOOK_GO_PROOF_DIR:-}" ]; then
  mkdir -p -- "$COMMAND_HOOK_GO_PROOF_DIR"
  cp -- "$fixture/"*.input "$fixture/"*.output "$fixture/"*.stderr \
    "$fixture/"*.time "$fixture/"*.metadata "$fixture/exec.trace" \
    "$fixture/source-head" "$fixture/sources.sha256" "$fixture/environment" \
    "$binary" "$COMMAND_HOOK_GO_PROOF_DIR/"
  sha256sum -- "$binary" \
    >"$COMMAND_HOOK_GO_PROOF_DIR/artifacts.sha256"
  printf 'proof_dir=%s\n' "$COMMAND_HOOK_GO_PROOF_DIR"
fi
printf 'PASS: incompatible cache repaired, warm reads allowed, Git and control ownership gates active, Go execution without Python\n'

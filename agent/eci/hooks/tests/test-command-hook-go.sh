#!/usr/bin/env bash
# Exercise the registered Go command hook, its fail-closed gate and eci-active tool repair privately.

set -euo pipefail
# The native gate rejects group/other-writable ancestors; keep the fixture chain private.
umask 077

source_root="$(cd -- "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
. "$source_root/hooks/tests/portable-test.sh"
# Inode, mtime and size: an identity that changes when the file is rewritten or replaced.
file_identity() { printf '%s:%s:%s\n' "$(ls -di -- "$1" | awk '{print $1}')" "$(eci_mtime "$1")" "$(eci_stat size "$1")"; }
# Keep compiler caches outside the isolated HOME; only the binary is cold.
export GOMODCACHE="$(go env GOMODCACHE)"
export GOCACHE="$(go env GOCACHE)"
export GOPATH="$(go env GOPATH)"
fixture="$(mktemp -d "${HOME:?}/tmp/command-hook-go.XXXXXX")"
fixture="$(eci_realpath_existing "$fixture")"
trap 'chmod -R u+w -- "$fixture"; rm -r -- "$fixture"' EXIT
fixture_home="$fixture/home"
runtime="$fixture_home/.omp/agent/eci"
mkdir -p -- "$runtime/bin" "$fixture_home/tmp" "$fixture/proof/hook-test" \
  "$fixture/config/eci" "$fixture/state"
cp -a -- "$source_root/hooks" "$runtime/hooks"
cp -- "$source_root/bin/eci-runtime-sync" "$runtime/bin/eci-runtime-sync"
cp -- "$source_root/bin/eci-active" "$runtime/bin/eci-active"
cp -- "$source_root/hooks.json" "$runtime/hooks.json"
# bin/eci-active is the only build trigger (6c614b9); it needs its Go source in the runtime.
mkdir -p -- "$runtime/tools"
cp -a -- "$source_root/tools/eci-active" "$runtime/tools/eci-active"
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
binary="$runtime/bin/eci-command-plan"
importer="$runtime/bin/eci-safe-import"
# A gate tool can survive a move from another architecture. Corrupt only ELF
# e_machine in a private copy at the gate path to reproduce that condition.
cp -- /bin/true "$binary"
printf '\377\377' | dd of="$binary" bs=1 seek=18 conv=notrunc status=none
chmod 755 -- "$binary"
cp -- "$binary" "$fixture/incompatible-binary"
source "$runtime/hooks/lib/eci-native-binary.sh"
eci_runtime_binary_is_native "$(eci_realpath_existing /bin/true)"
! eci_runtime_binary_is_native "$binary"
ln -s /bin/true "$fixture/native-link"
! eci_runtime_binary_is_native "$fixture/native-link"
git -C "${ECI_SOURCE_REPO:-$source_root}" rev-parse HEAD >"$fixture/source-head"
eci_sha256 -- "$runtime/hooks/lib/eci-command-plan-go/"*.go \
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

# provision runs bin/eci-active, which rebuilds absent or host-incompatible gate tools on first use.
provision() {
  env HOME="$fixture_home" ECI_HOME="$runtime" ECI_PROOF_ROOT="$fixture/proof" \
    XDG_CONFIG_HOME="$fixture/config" XDG_STATE_HOME="$fixture/state" \
    "$runtime/bin/eci-active" status >"$fixture/provision-$1.output" 2>"$fixture/provision-$1.log"
}

# import_leaf runs the importer's consumer: manifest-write rebuilds a host-incompatible
# eci-safe-import, verifies it, and imports the fixed-session leaf through it.
import_leaf() {
  env HOME="$fixture_home" ECI_HOME="$runtime" ECI_PROOF_ROOT="$fixture/proof" ECI_SESSION_ID=hook-test \
    XDG_CONFIG_HOME="$fixture/config" XDG_STATE_HOME="$fixture/state" \
    "$runtime/bin/eci-active" manifest-write "$fixture/import-source" \
    >"$fixture/import-$1.output" 2>"$fixture/import-$1.log"
}

# run_callback executes the configured command with a real worker callback.
run_callback() {
  local name="$1" command="$2" started_uptime ended_uptime idle_seconds
  jq -cn --arg cwd "$source_root" --arg command "$command" \
    '{session_id:"hook-test",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' >"$fixture/$name.input"
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

# The hook never repairs a gate tool: an incompatible one is denied and left untouched.
run_callback incompatible-cat 'cat AGENTS.md'
jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_HOOK_NATIVE_GATE_UNAVAILABLE"))' \
  "$fixture/incompatible-cat.output" >/dev/null
cmp -- "$fixture/incompatible-binary" "$binary"
[ ! -e "$importer" ]
# The same applies to an absent tool.
rm -- "$binary"
run_callback absent-cat 'cat AGENTS.md'
jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_HOOK_NATIVE_GATE_UNAVAILABLE"))' \
  "$fixture/absent-cat.output" >/dev/null
[ ! -e "$binary" ]
# eci-active is the repair route; afterwards the same callback is allowed.
cp -- "$fixture/incompatible-binary" "$binary"
provision first
eci_runtime_binary_is_native "$binary"
eci_runtime_binary_is_native "$importer"
(cd -- "$runtime/bin" && eci_sha256 -c --quiet -- eci-command-plan.sha256 eci-safe-import.sha256 eci-verexec.sha256)
run_callback repaired-cat 'cat AGENTS.md'
[ ! -s "$fixture/repaired-cat.output" ]
"$binary" --hook <"$fixture/repaired-cat.input" >"$fixture/native.output"
[ ! -s "$fixture/native.output" ]
binary_identity="$(file_identity "$binary")"
run_callback warm-cat 'cat AGENTS.md'
[ ! -s "$fixture/warm-cat.output" ]
run_callback warm-diff 'git diff -- AGENTS.md'
[ ! -s "$fixture/warm-diff.output" ]
[ "$(file_identity "$binary")" = "$binary_identity" ]
# A whole-index add is broad-destructive before any ownership check (575bb81 hook_effects_test.go:250).
run_callback denied-add 'git add -A'
jq -e '.hookSpecificOutput.hookEventName == "PreToolUse" and .hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_BROAD_DESTRUCTIVE_DENIED"))' \
  "$fixture/denied-add.output" >/dev/null
run_callback denied-control "touch $fixture/proof/hook-test/eci_wait"
jq -e '.hookSpecificOutput.hookEventName == "PreToolUse" and .hookSpecificOutput.permissionDecision == "deny"' \
  "$fixture/denied-control.output" >/dev/null
# A corrupt importer is rebuilt by its consumer, the imported leaf carries the source
# bytes, and a native importer is kept on the next import.
printf '\377\377' | dd of="$importer" bs=1 seek=18 conv=notrunc status=none
! eci_runtime_binary_is_native "$importer"
printf '%s\n' '{"critics":[]}' >"$fixture/import-source"
import_leaf recovery
eci_runtime_binary_is_native "$importer"
cmp -- "$fixture/import-source" "$fixture/proof/hook-test/eci-required-critics.json"
importer_identity="$(file_identity "$importer")"
rm -- "$fixture/proof/hook-test/eci-required-critics.json"
import_leaf warm
cmp -- "$fixture/import-source" "$fixture/proof/hook-test/eci-required-critics.json"
[ "$(file_identity "$importer")" = "$importer_identity" ]
[ "$(file_identity "$binary")" = "$binary_identity" ]
# Trace separately so syscall tracing does not affect the timing probes.
env HOME="$fixture_home" ECI_HOME="$runtime" ECI_HOOK_IS_SUBAGENT=true \
  ECI_PROOF_ROOT="$fixture/proof" XDG_CONFIG_HOME="$fixture/config" \
  XDG_STATE_HOME="$fixture/state" \
  strace -f -e trace=execve,execveat -o "$fixture/exec.trace" bash -c "$launcher" \
  <"$fixture/warm-cat.input" >"$fixture/traced-cat.output" 2>"$fixture/traced-cat.stderr"
[ ! -s "$fixture/traced-cat.output" ]
# bin/eci-verexec runs the planner from its verified descriptor: execveat(fd, "", ..., AT_EMPTY_PATH).
grep -E 'execveat\([0-9]+, "", \[[^]]*"--hook".*AT_EMPTY_PATH' "$fixture/exec.trace" >/dev/null
if grep -E 'execve\("[^"]*/python[0-9.]*"' "$fixture/exec.trace"; then
  printf 'FAIL: registered callback executed Python\n' >&2
  exit 1
fi
# The isolated copy has no .git; run-isolated.sh exports the owning repository as ECI_SOURCE_REPO.
git -C "${ECI_SOURCE_REPO:?}" check-ignore --quiet -- agent/eci/hooks/lib/eci-command-plan-go/eci-command-plan
[ -z "$(git -C "$ECI_SOURCE_REPO" ls-files -- agent/eci/hooks/lib/eci-command-plan-go/eci-command-plan)" ]
if [ -n "${COMMAND_HOOK_GO_PROOF_DIR:-}" ]; then
  mkdir -p -- "$COMMAND_HOOK_GO_PROOF_DIR"
  cp -- "$fixture/"*.input "$fixture/"*.output "$fixture/"*.stderr \
    "$fixture/"*.time "$fixture/"*.metadata "$fixture/exec.trace" \
    "$fixture/source-head" "$fixture/sources.sha256" "$fixture/environment" \
    "$binary" "$COMMAND_HOOK_GO_PROOF_DIR/"
  eci_sha256 -- "$binary" \
    >"$COMMAND_HOOK_GO_PROOF_DIR/artifacts.sha256"
  printf 'proof_dir=%s\n' "$COMMAND_HOOK_GO_PROOF_DIR"
fi
printf 'PASS: incompatible or absent tool denied, eci-active repair, warm reads allowed, Git and control gates active, Go execution without Python\n'

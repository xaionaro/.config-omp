#!/usr/bin/env bash

set -Eeuo pipefail
trap 'status=$?; printf "maintain-planner test: line=%s status=%s command=%q\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_BASE="$(eci_realpath_existing "${ECI_TMPDIR:-${HOME:?}/tmp}")"
case "$TMP_BASE" in
  /tmp|/tmp/*|/) printf 'maintain-planner test: unsafe temporary root: %s\n' "$TMP_BASE" >&2; exit 1 ;;
esac
TMP_ROOT="$(mktemp -d "$TMP_BASE/eci-maintain-planner.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

HOME_ROOT="$TMP_ROOT/home"
HOME_ALIAS="$TMP_ROOT/home-alias"
FIXTURE_HOME="$HOME_ROOT/.omp/agent/eci"
KIMI_HOME="$HOME_ROOT/.kimi-code"
PROOF_ROOT="$TMP_ROOT/proof"
SESSION_ID='maintain-planner-session'
PLANNER_SENTINEL="$TMP_ROOT/planner-invoked"
DISPATCH_LOG="$TMP_ROOT/dispatch.log"
RUNTIME_RECEIPT="$FIXTURE_HOME/.eci-runtime-sync-manifest"
BASELINE_RECEIPT="$TMP_ROOT/full-runtime-receipt"

mkdir -p -- "$HOME_ROOT/tmp" "$FIXTURE_HOME/bin" \
  "$KIMI_HOME/bin" "$PROOF_ROOT/$SESSION_ID"
MAIN_TRANSCRIPT="$HOME_ROOT/.omp/agent/sessions/maintain-planner/main.jsonl"
mkdir -p -- "$(dirname -- "$MAIN_TRANSCRIPT")"
printf '%s\n' \
  "{\"type\":\"session\",\"version\":3,\"id\":\"$SESSION_ID\",\"cwd\":\"$FIXTURE_HOME\"}" \
  '{"type":"message","role":"user","content":"maintain planner"}' >"$MAIN_TRANSCRIPT"
ln -s -- "$HOME_ROOT" "$HOME_ALIAS"
cp -- "$ROOT/bin/eci-active" "$FIXTURE_HOME/bin/eci-active"
cp -- "$ROOT/bin/eci-active" "$KIMI_HOME/bin/eci-active"
cp -- "$ROOT/bin/eci-active-dispatch" "$FIXTURE_HOME/bin/eci-active-dispatch"
cp -- "$ROOT/hooks.json" "$FIXTURE_HOME/hooks.json"
cp -a -- "$ROOT/hooks/." "$FIXTURE_HOME/hooks/"
cp -a -- "$ROOT/tools" "$FIXTURE_HOME/tools"
eci_sed_inplace '2{/^exit 0$/d;}' "$FIXTURE_HOME/hooks/validate-bash.sh"

# Keep one managed source below a private directory so the receipt test can
# prove that a failed hook-tree enumeration is never treated as a partial
# producer manifest.
UNREADABLE_MANAGED_DIR="$FIXTURE_HOME/hooks/zzzz-managed-private"
UNREADABLE_MANAGED_FILE="$UNREADABLE_MANAGED_DIR/entry.sh"
mkdir -p -- "$UNREADABLE_MANAGED_DIR"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$UNREADABLE_MANAGED_FILE"
chmod 755 -- "$UNREADABLE_MANAGED_DIR" "$UNREADABLE_MANAGED_FILE"

cat >"$FIXTURE_HOME/bin/eci-runtime-sync" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >"${ECI_TEST_DISPATCH_LOG:?}"
EOF
chmod 755 -- "$FIXTURE_HOME/bin/eci-active" "$KIMI_HOME/bin/eci-active" \
  "$FIXTURE_HOME/bin/eci-active-dispatch" "$FIXTURE_HOME/bin/eci-runtime-sync"
fixture_active_digest="$(eci_sha256_hex "$FIXTURE_HOME/bin/eci-active")"
fixture_validate_bash_digest="$(eci_sha256_hex "$FIXTURE_HOME/hooks/validate-bash.sh")"
fixture_validate_bash_mode="$(eci_stat mode "$FIXTURE_HOME/hooks/validate-bash.sh")"

cat >"$FIXTURE_HOME/hooks/lib/eci-command-plan-go/eci-command-plan" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' invoked >"${ECI_TEST_PLANNER_SENTINEL:?}"
cat >/dev/null
exit 42
EOF
chmod 755 -- "$FIXTURE_HOME/hooks/lib/eci-command-plan-go/eci-command-plan"

write_full_runtime_receipt() {
  local relative source mode digest

  : >"$RUNTIME_RECEIPT"
  {
    printf '%s\n' hooks.json
    printf '%s\n' bin/eci-active
    printf '%s\n' bin/eci-active-dispatch
    printf '%s\n' bin/eci-runtime-sync
    [ -f "$FIXTURE_HOME/bin/eci-command-gate-mode" ] && printf '%s\n' bin/eci-command-gate-mode
    find "$FIXTURE_HOME/hooks" -type f ! -path "$FIXTURE_HOME/hooks/tests/*" \
      ! -path '*/__pycache__/*' ! -name '*.pyc' ! -name '*.pyo' -printf 'hooks/%P\n'
  } | LC_ALL=C sort -u | while IFS= read -r relative; do
    [ -n "$relative" ] || continue
    source="$FIXTURE_HOME/$relative"
    [ -f "$source" ] && [ ! -L "$source" ]
    mode="$(eci_stat mode "$source")"
    digest="$(eci_sha256_hex "$source")"
    printf '%s\t%s\t%s\n' "$relative" "$digest" "$mode" >>"$RUNTIME_RECEIPT"
  done
  chmod 600 -- "$RUNTIME_RECEIPT"
}

snapshot_full_runtime_receipt() {
  write_full_runtime_receipt
  cp -- "$RUNTIME_RECEIPT" "$BASELINE_RECEIPT"
}

restore_full_runtime_receipt() {
  cp -- "$BASELINE_RECEIPT" "$RUNTIME_RECEIPT"
  chmod 600 -- "$RUNTIME_RECEIPT"
}

printf '%s\n' \
  'scope: maintain-planner test' \
  "cwd: $FIXTURE_HOME" \
  "session_id: $SESSION_ID" \
  'created_utc: 2026-08-25T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION_ID/eci_active"

HOME="$HOME_ROOT" ECI_HOME="$FIXTURE_HOME" KIMI_CODE_HOME="$KIMI_HOME" \
  ECI_TEST_DISPATCH_LOG="$DISPATCH_LOG" \
  ECI_ROLE=coordinator \
  "$FIXTURE_HOME/bin/eci-active" maintain-planner
grep -Fx -- "planner-apply --target $KIMI_HOME" "$DISPATCH_LOG" >/dev/null

# Publish the native command planner where validate-bash resolves it. The
# non-native stub under the hooks tree must never execute.
mkdir -p -- "$HOME_ROOT/tmp/eci-runtime/eci"
(
  cd "$FIXTURE_HOME/hooks/lib/eci-command-plan-go"
  go build -o "$HOME_ROOT/tmp/eci-runtime/eci/eci-command-plan" .
)

snapshot_full_runtime_receipt

run_hook() {
  run_hook_with_eci_home "$1" "$FIXTURE_HOME" "${2-}"
}

run_hook_with_eci_home() {
  run_hook_with_home "$1" "$HOME_ROOT" "$2" "${3-}"
}

run_hook_with_home() {
  local command="$1" selected_home="$2" selected_eci_home="$3" transcript="${4-}" output="$TMP_ROOT/hook-output-$RANDOM"
  jq -cn --arg cwd "$FIXTURE_HOME" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:"maintain-planner-session",cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}} + (if $transcript == "" then {} else {transcript_path:$transcript} end)' |
    HOME="$selected_home" ECI_PROOF_ROOT="$PROOF_ROOT" ECI_HOME="$selected_eci_home" KIMI_CODE_HOME="$KIMI_HOME" \
      ECI_TEST_PLANNER_SENTINEL="$PLANNER_SENTINEL" PATH="$FIXTURE_HOME/bin:$PATH" \
      bash "$FIXTURE_HOME/hooks/validate-bash.sh" >"$output"
  printf '%s\n' "$output"
}

exact_command='$HOME/.omp/agent/eci/bin/eci-active maintain-planner'
exact_output="$(run_hook "$exact_command" "$MAIN_TRANSCRIPT")"
[ ! -e "$PLANNER_SENTINEL" ] || {
  printf '%s\n' 'exact maintenance route invoked the stale planner' >&2
  exit 1
}
[ ! -s "$exact_output" ] || {
  printf '%s\n' 'exact maintenance route was not allowed:' >&2
  cat -- "$exact_output" >&2
  exit 1
}

# The command planner treats the explicit double-quoted current-home spelling
# as the same lifecycle form. It must receive the same receipt-bound bootstrap
# route rather than falling through to the broken planner.
quoted_exact_command='"$HOME/.omp/agent/eci/bin/eci-active" maintain-planner'
quoted_exact_output="$(run_hook "$quoted_exact_command" "$MAIN_TRANSCRIPT")"
[ ! -e "$PLANNER_SENTINEL" ] || {
  printf '%s\n' 'quoted exact maintenance route invoked the stale planner' >&2
  exit 1
}

# The literal HOME spelling selects the ECI lifecycle root even when a
# caller exports an alternate ECI_HOME. Bootstrap must not turn that
# environment variable into lifecycle authority.
alternate_eci="$TMP_ROOT/alternate-eci"
mkdir -p -- "$alternate_eci"
alternate_home_output="$(run_hook_with_eci_home "$exact_command" "$alternate_eci" "$MAIN_TRANSCRIPT")"
[ ! -e "$PLANNER_SENTINEL" ] || {
  printf '%s\n' 'HOME-bound maintenance route invoked the stale planner under alternate ECI_HOME' >&2
  exit 1
}
[ ! -s "$alternate_home_output" ] || {
  printf '%s\n' 'HOME-bound maintenance route was not allowed under alternate ECI_HOME:' >&2
  cat -- "$alternate_home_output" >&2
  exit 1
}

# A parent alias of HOME must not turn the exact lexical `$HOME/.omp/agent/eci`
# lifecycle spelling into an unbound runtime root.  The receipt and all
# source files remain in the same physical provider directory.
aliased_home_output="$(run_hook_with_home "$exact_command" "$HOME_ALIAS" "$alternate_eci" "$MAIN_TRANSCRIPT")"
[ ! -e "$PLANNER_SENTINEL" ] || {
  printf '%s\n' 'HOME-parent alias maintenance route invoked the stale planner' >&2
  exit 1
}
[ ! -s "$aliased_home_output" ] || {
  printf '%s\n' 'HOME-parent alias maintenance route was not allowed:' >&2
  cat -- "$aliased_home_output" >&2
  exit 1
}
[ ! -s "$quoted_exact_output" ] || {
  printf '%s\n' 'quoted exact maintenance route was not allowed:' >&2
  cat -- "$quoted_exact_output" >&2
  exit 1
}

# With a working planner, equivalent spellings must resolve to the same
# lifecycle target. The adapter compares the actual executable rather than
# making punctuation, quoting, PATH, or HOME expansion a permission boundary.
snapshot_full_runtime_receipt
assert_maintenance_target_admitted() {
  local target_command="$1" target_output

  target_output="$(run_hook "$target_command" "$MAIN_TRANSCRIPT")"
  [ ! -s "$target_output" ] || {
    printf 'equivalent maintenance target was not admitted: %s\n' "$target_command" >&2
    cat -- "$target_output" >&2
    exit 1
  }
}

assert_maintenance_target_admitted '"$HOME"/.omp/agent/eci/bin/eci-active maintain-planner'
assert_maintenance_target_admitted '$ECI_HOME/bin/eci-active maintain-planner'
assert_maintenance_target_admitted 'eci-active maintain-planner'
assert_maintenance_target_admitted '~/.omp/agent/eci/bin/eci-active maintain-planner'
assert_maintenance_target_admitted "$FIXTURE_HOME/bin/eci-active maintain-planner"

kimi_target_output="$(run_hook '"$HOME/.kimi-code/bin/eci-active" maintain-planner')"
# ECI_LIFECYCLE_TARGET_DENIED is retired (not produced at HEAD or by source
# 575bb81; see test-validate-bash-classifier.sh:376-378). Assert the denial the
# hook actually produces for a foreign lifecycle target: a worker-role deny on
# the coordinator-owned lifecycle control predicate, with no token pin.
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-control")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-lifecycle-control")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("role=worker"))
' "$kimi_target_output" >/dev/null || {
  printf '%s\n' 'different Kimi lifecycle target was not denied as worker lifecycle control:' >&2
  cat -- "$kimi_target_output" >&2
  exit 1
}

assert_receipt_drift_admitted() {
  local label="$1" command="$2" output

  output="$(run_hook "$command" "$MAIN_TRANSCRIPT")"
  [ ! -s "$output" ] || {
    printf 'runtime receipt drift blocked planner maintenance: %s\n' "$label" >&2
    cat -- "$output" >&2
    exit 1
  }
  [ ! -e "$PLANNER_SENTINEL" ] || {
    printf 'runtime receipt case invoked the planner: %s\n' "$label" >&2
    exit 1
  }
}

zero_digest='0000000000000000000000000000000000000000000000000000000000000000'

# The producer-shaped full manifest is accepted by both exact spellings.
restore_full_runtime_receipt
valid_full_output="$(run_hook "$exact_command" "$MAIN_TRANSCRIPT")"
[ ! -s "$valid_full_output" ] || {
  printf '%s\n' 'full runtime receipt denied exact planner maintenance:' >&2
  cat -- "$valid_full_output" >&2
  exit 1
}

# A planner publish transaction is transient build state, not managed runtime
# receipt content and not a lifecycle admission prerequisite.
planner_transaction_dir="$FIXTURE_HOME/hooks/lib/eci-command-plan-go/.eci-command-plan.txn.fixture"
mkdir -p -- "$planner_transaction_dir"
printf '%s\n' transient >"$planner_transaction_dir/eci-command-plan"
assert_receipt_drift_admitted 'planner transaction residue' "$quoted_exact_command"
rm -f -- "$planner_transaction_dir/eci-command-plan"
rmdir -- "$planner_transaction_dir"

# The native gate's bin-chain check (eci-native-binary.sh:178) rejects any
# group- or other-writable directory on the bin path or above it. Both cases
# below are therefore expected to be denied by that check, and the exact-route
# admission earlier in this file is the baseline they are compared against.
restore_full_runtime_receipt
fixture_root_mode="$(eci_stat mode "$FIXTURE_HOME")"
chmod o+w -- "$FIXTURE_HOME"
world_writable_output="$(run_hook "$exact_command" "$MAIN_TRANSCRIPT")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_HOOK_NATIVE_GATE_UNAVAILABLE]"))
' "$world_writable_output" >/dev/null || {
  printf '%s\n' 'world-writable runtime root was not denied by the native gate:' >&2
  cat -- "$world_writable_output" >&2
  exit 1
}
chmod "$fixture_root_mode" -- "$FIXTURE_HOME"
restore_full_runtime_receipt
chmod g+w -- "$FIXTURE_HOME/bin" "$FIXTURE_HOME/hooks"
group_writable_dirs_output="$(run_hook "$quoted_exact_command" "$MAIN_TRANSCRIPT")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_HOOK_NATIVE_GATE_UNAVAILABLE]"))
' "$group_writable_dirs_output" >/dev/null || {
  printf '%s\n' 'group-writable bin directory was not denied by the native gate:' >&2
  cat -- "$group_writable_dirs_output" >&2
  exit 1
}
chmod g-w -- "$FIXTURE_HOME/bin" "$FIXTURE_HOME/hooks"
restore_full_runtime_receipt

# Any stale managed file, including the hook itself or the runtime-sync
# deployment entrypoint, denies before the broken planner can run.
restore_full_runtime_receipt
eci_sed_inplace "s#^bin/eci-active\t$fixture_active_digest\t#bin/eci-active\t$zero_digest\t#" "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'stale eci-active digest' "$exact_command"
assert_receipt_drift_admitted 'stale eci-active digest quoted' "$quoted_exact_command"
assert_receipt_drift_admitted 'stale eci-active digest through PATH target' 'eci-active maintain-planner'

restore_full_runtime_receipt
eci_sed_inplace "s#^hooks/validate-bash.sh\t$fixture_validate_bash_digest\t#hooks/validate-bash.sh\t$zero_digest\t#" "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'stale validate-bash digest' "$quoted_exact_command"

restore_full_runtime_receipt
stale_validate_bash_mode=600
[ "$fixture_validate_bash_mode" != "$stale_validate_bash_mode" ] || stale_validate_bash_mode=644
eci_sed_inplace "s#^hooks/validate-bash.sh\t$fixture_validate_bash_digest\t$fixture_validate_bash_mode\$#hooks/validate-bash.sh\t$fixture_validate_bash_digest\t$stale_validate_bash_mode#" "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'stale validate-bash mode' "$exact_command"

fixture_runtime_sync_digest="$(eci_sha256_hex "$FIXTURE_HOME/bin/eci-runtime-sync")"
restore_full_runtime_receipt
eci_sed_inplace "s#^bin/eci-runtime-sync\t$fixture_runtime_sync_digest\t#bin/eci-runtime-sync\t$zero_digest\t#" "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'stale runtime-sync digest' "$exact_command"

fixture_dispatch_digest="$(eci_sha256_hex "$FIXTURE_HOME/bin/eci-active-dispatch")"
restore_full_runtime_receipt
eci_sed_inplace "s#^bin/eci-active-dispatch\t$fixture_dispatch_digest\t#bin/eci-active-dispatch\t$zero_digest\t#" "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'stale lifecycle dispatcher digest' "$exact_command"

# Exact producer shape is required: reject malformed, missing, extra,
# duplicate, and noncanonical ordering records even if eci-active matches.
restore_full_runtime_receipt
printf 'hooks/not-a-receipt\tnot-a-digest\tbogus\n' >>"$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'malformed extra record' "$exact_command"

restore_full_runtime_receipt
eci_sed_inplace '/^hooks\/validate-bash.sh\t/d' "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'missing validate-bash record' "$quoted_exact_command"

restore_full_runtime_receipt
eci_sed_inplace '/^bin\/eci-active-dispatch\t/d' "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'missing lifecycle dispatcher record' "$exact_command"

# `find` failure must be terminal. With the late-sorting managed row removed,
# a process-substitution consumer used to accept the remaining partial
# manifest because it lost the producer generator's nonzero status.
restore_full_runtime_receipt
chmod 000 -- "$UNREADABLE_MANAGED_DIR"
eci_sed_inplace '/^hooks\/zzzz-managed-private\/entry.sh\t/d' "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'unreadable managed directory and missing receipt row' "$exact_command"
chmod 755 -- "$UNREADABLE_MANAGED_DIR"
restore_full_runtime_receipt

restore_full_runtime_receipt
printf 'hooks/extra\t%s\t644\n' "$zero_digest" >>"$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'extra record' "$exact_command"

restore_full_runtime_receipt
grep -F $'bin/eci-active\t' "$BASELINE_RECEIPT" >>"$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'duplicate eci-active record' "$quoted_exact_command"

restore_full_runtime_receipt
tac -- "$RUNTIME_RECEIPT" >"$TMP_ROOT/reversed-receipt"
mv -- "$TMP_ROOT/reversed-receipt" "$RUNTIME_RECEIPT"
chmod 600 -- "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'unsorted records' "$exact_command"

# Owner-only receipt metadata and canonical regular source files are also
# required; neither an unsafe receipt inode nor a symlinked listed source can
# authorize the bootstrap route.
restore_full_runtime_receipt
chmod 644 -- "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'receipt mode is not 0600' "$quoted_exact_command"

restore_full_runtime_receipt
ln -- "$RUNTIME_RECEIPT" "$TMP_ROOT/receipt-hardlink"
assert_receipt_drift_admitted 'receipt has multiple links' "$exact_command"
rm -f -- "$TMP_ROOT/receipt-hardlink"

restore_full_runtime_receipt
rm -f -- "$RUNTIME_RECEIPT"
cp -- "$BASELINE_RECEIPT" "$TMP_ROOT/receipt-symlink-target"
chmod 600 -- "$TMP_ROOT/receipt-symlink-target"
ln -s -- "$TMP_ROOT/receipt-symlink-target" "$RUNTIME_RECEIPT"
assert_receipt_drift_admitted 'receipt symlink' "$quoted_exact_command"
rm -f -- "$RUNTIME_RECEIPT"
restore_full_runtime_receipt
mv -- "$FIXTURE_HOME/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.sh"
ln -s -- "$TMP_ROOT/validate-bash.sh" "$FIXTURE_HOME/hooks/validate-bash.sh"
assert_receipt_drift_admitted 'listed source symlink' "$exact_command"
rm -f -- "$FIXTURE_HOME/hooks/validate-bash.sh"
mv -- "$TMP_ROOT/validate-bash.sh" "$FIXTURE_HOME/hooks/validate-bash.sh"
restore_full_runtime_receipt

# A resolved same-target lifecycle executable reaches its own CLI for argument
# validation. The hook owns callback binding, not a second verb/argv grammar.
extra_output="$(run_hook "$FIXTURE_HOME/bin/eci-active maintain-planner extra" "$MAIN_TRANSCRIPT")"
[ ! -s "$extra_output" ] || {
  printf '%s\n' 'same-target lifecycle extra argument was denied by the hook instead of reaching the CLI:' >&2
  cat -- "$extra_output" >&2
  exit 1
}

worker_output="$TMP_ROOT/worker-output"
jq -cn --arg cwd "$FIXTURE_HOME" --arg command "$FIXTURE_HOME/bin/eci-active maintain-planner" \
  '{session_id:"maintain-planner-session",cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}' |
  ECI_PROOF_ROOT="$PROOF_ROOT" ECI_HOME="$FIXTURE_HOME" KIMI_CODE_HOME="$KIMI_HOME" \
    ECI_HOOK_IS_SUBAGENT=true ECI_ROLE=worker \
    ECI_TEST_PLANNER_SENTINEL="$PLANNER_SENTINEL" PATH="$FIXTURE_HOME/bin:$PATH" \
    bash "$FIXTURE_HOME/hooks/validate-bash.sh" >"$worker_output"
jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$worker_output" >/dev/null

printf '%s\n' 'eci-maintain-planner: PASS'

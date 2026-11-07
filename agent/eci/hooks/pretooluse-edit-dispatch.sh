#!/usr/bin/env bash
# TODO: Reconcile the ECI control-gate dispatch after its marker-dependent hooks are migrated.

set -Eeuo pipefail

hook_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# Child-health failures are advisory: malformed output or stderr from a child
# never turns an edit into a denial by itself.  Every denial in this dispatch
# depends on the ECI marker check, so the proof-state lib must load and define
# it first; if it cannot, the gate cannot decide and denies.
if ! . "$hook_dir/lib/eci-proof-state.sh" 2>/dev/null || ! declare -F eci_marker_active_or_unknown >/dev/null 2>&1; then
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: lib/eci-proof-state.sh failed to load or does not define eci_marker_active_or_unknown; reinstall the ECI hooks and retry"}}'
  exit 0
fi
# Unexpected failure after the lib load.  With an active or unknown marker the
# tool call is denied with the fail-closed constant; with no marker the hook
# exits with the failing status, as errexit does in the source.  A subshell
# failure exits with its own status so no deny JSON is captured into a value,
# and the busy flag stops a failure inside this handler from re-entering it.
_dispatch_err_busy=""
_dispatch_err_trap() {
  local status="$1"
  if [ "$BASHPID" != "$$" ]; then
    exit "$status"
  fi
  if [ -z "$_dispatch_err_busy" ]; then
    _dispatch_err_busy=1
    if eci_marker_active_or_unknown; then
      printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_FAIL_CLOSED] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: an unexpected failure occurred while an active marker exists, so the edit cannot be classified; inspect the hook log and retry"}}'
      exit 0
    fi
  elif eci_marker_active_or_unknown; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_FAIL_CLOSED] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: a failure inside the fail-closed handler occurred while an active marker exists; inspect the hook log and retry"}}'
    exit 0
  fi
  exit "$status"
}
trap '_dispatch_err_trap "$?"' ERR
tmp_parent="${TMPDIR:-${ECI_TMPDIR:-/tmp}}"
if ! tmp_dir="$(mktemp -d -- "${tmp_parent%/}/eci-edit-dispatch.XXXXXX" 2>/dev/null)"; then
  # No scratch directory: deny only while a marker is active or unknown; with no marker exit 0 as the source does.
  if eci_marker_active_or_unknown; then
    if ! jq -cn --arg reason "[ECI_HOOK_TEMP_UNAVAILABLE] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: scratch directory creation failed under $tmp_parent; set TMPDIR or ECI_TMPDIR to a writable absolute directory and retry" \
      '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}'; then
      printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_TEMP_UNAVAILABLE] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: scratch directory creation failed; set TMPDIR or ECI_TMPDIR to a writable absolute directory and retry"}}'
    fi
  fi
  exit 0
fi
trap 'rm -rf -- "$tmp_dir"' EXIT HUP INT TERM

input_file="$tmp_dir/input.json"
cat >"$input_file"

if ! tool_name="$(jq -r '.tool_name // empty' <"$input_file" 2>/dev/null)"; then
  # Unparseable input denies only while a marker is active or unknown; with no marker exit 0 as the source does.
  if eci_marker_active_or_unknown; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_JQ_FAILED] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: jq is unavailable or could not parse the tool input while an active marker exists; provision jq and retry"}}'
  fi
  exit 0
fi
case "$tool_name" in
apply_patch) validator="$hook_dir/validate-apply-patch.sh" ;;
Edit | Write | MultiEdit | NotebookEdit) validator="$hook_dir/validate-edit-write.sh" ;;
*) exit 0 ;;
esac

# Keep the synchronous collection minimal. The target validator and active-ECI
# target guard may each return a concrete denial; the reminder is advisory.
security_reminder_candidate() {
  case "$1" in
  *'.github/workflows/'* | *'child_process.exec'* | *'exec('* | *'execSync('* | *'new Function'* | *'eval('* | \
    *'dangerouslySetInnerHTML'* | *'document.write'* | *'.innerHTML ='* | *'.innerHTML='* | *'pickle'* | \
    *'os.system'* | *'from os import system'*)
    return 0
    ;;
  *)
    return 1
    ;;
  esac
}

# Ordinary edit dispatch runs only concrete target guards. An explicit task
# risk-review request may opt into the advisory; without it, this process is
# not invoked and cannot create reminder state.
gates=(
  "$validator"
  "$hook_dir/eci-active-gate.sh"
)
reminder_index=""
reminder_fd=""
if [ "${ENABLE_SECURITY_REMINDER:-0}" = 1 ] && security_reminder_candidate "$(<"$input_file")"; then
  if . "$hook_dir/lib/eci-native-binary.sh" 2>/dev/null && eci_runtime_open_verified eci-hook-helper; then
    # The verified descriptor stays open only until the reminder is launched below, which happens before any validator forks.
    reminder_index="${#gates[@]}"
    gates+=("security-reminder")
    reminder_fd="$ECI_RUNTIME_BINARY_FD"
  else
    printf '%s\n' "${ECI_RUNTIME_DENY_REASON:-ECI native gate binary eci-hook-helper is unavailable: lib/eci-native-binary.sh failed to load; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry}" >&2
  fi
fi
pids=()
if [ -n "$reminder_index" ]; then
  # Launch the reminder on the verified descriptor, then release the descriptor so no validator inherits it.
  "/proc/self/fd/$reminder_fd" security-reminder <"$input_file" >"$tmp_dir/output-$reminder_index" 2>"$tmp_dir/error-$reminder_index" &
  pids[$reminder_index]=$!
  exec {reminder_fd}<&-
fi
for index in "${!gates[@]}"; do
  if [ "$index" = "$reminder_index" ]; then
    continue
  fi
  output_file="$tmp_dir/output-$index"
  error_file="$tmp_dir/error-$index"
  bash "${gates[$index]}" <"$input_file" >"$output_file" 2>"$error_file" &
  pids[$index]=$!
done

# A child may offer a concrete denial.  Its stderr and malformed output are
# advisory and never classify the edit by themselves.  A non-zero gate exit
# means the edit could not be classified: it is denied only while an ECI marker
# is active or unknown (ECI_EDIT_GATE_FAILED below); with no marker the source
# behavior holds.  Another healthy child may still identify a real target.
dispatch_max_output_bytes=65536
dispatch_candidate_is_valid() {
  local path="$1" bytes
  bytes="$(wc -c <"$path" 2>/dev/null || true)"
  case "$bytes" in
  '' | *[!0-9]*) return 1 ;;
  esac
  [ "$bytes" -gt 0 ] && [ "$bytes" -le "$dispatch_max_output_bytes" ] || return 1
  jq -s -e '
    (length == 1) and
    (.[0] |
      type == "object" and
      (keys | sort) == ["hookSpecificOutput"] and
      (.hookSpecificOutput |
        type == "object" and
        (keys | sort) == ["hookEventName", "permissionDecision", "permissionDecisionReason"] and
        .hookEventName == "PreToolUse" and
        (.permissionDecision | type == "string" and (. == "allow" or . == "deny")) and
        (.permissionDecisionReason | type == "string")
      )
    )
  ' "$path" >/dev/null 2>&1
}

dispatch_candidate_decision() {
  jq -r -s '.[0].hookSpecificOutput.permissionDecision' "$1" 2>/dev/null
}

statuses=()
for index in "${!pids[@]}"; do
  if wait "${pids[$index]}"; then
    statuses[$index]=0
  else
    statuses[$index]=$?
  fi
done

candidate_output=""
gate_failed=""
for index in "${!gates[@]}"; do
  # Ignore child stderr and malformed output.  Only a well-formed, explicit
  # denial is actionable; a non-zero gate exit is recorded for the marker gate.
  if [ "$index" != "$reminder_index" ] && [ "${statuses[$index]:-1}" -ne 0 ]; then
    gate_failed=1
  fi
  if [ "${statuses[$index]:-1}" -eq 0 ] &&
    [ -s "$tmp_dir/output-$index" ] &&
    dispatch_candidate_is_valid "$tmp_dir/output-$index" &&
    [ "$(dispatch_candidate_decision "$tmp_dir/output-$index")" = deny ] &&
    [ -z "$candidate_output" ]; then
    candidate_output="$tmp_dir/output-$index"
  fi
done

if [ -n "$candidate_output" ]; then
  jq -c . "$candidate_output"
elif [ -n "$gate_failed" ] && eci_marker_active_or_unknown; then
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_EDIT_GATE_FAILED] ECI edit dispatch denied by pretooluse-edit-dispatch.sh: a gate child exited non-zero while an active marker exists, so the edit cannot be classified; inspect the hook log and retry"}}'
fi

# The explicitly requested advisory is intentionally outside the provider
# envelope. It can inform the requested review, but never turns an ordinary
# edit into a denial.
if [ -n "$reminder_index" ] && [ -s "$tmp_dir/error-$reminder_index" ]; then
  cat -- "$tmp_dir/error-$reminder_index" >&2
fi

#!/usr/bin/env bash
# TODO: Reconcile the ECI control-gate dispatch after its marker-dependent hooks are migrated.

set -euo pipefail

hook_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# Child-health failures stay advisory: malformed output, stderr, or a non-zero
# exit from a child never turns an edit into a denial by itself. A failure to
# create the dispatch scratch directory is different: the gate cannot run, so
# it fails closed with an explicit deny.
tmp_parent="${TMPDIR:-${ECI_TMPDIR:-/tmp}}"
if ! tmp_dir="$(mktemp -d -- "${tmp_parent%/}/eci-edit-dispatch.XXXXXX" 2>/dev/null)"; then
  if ! jq -cn --arg reason "ECI edit dispatch denied by pretooluse-edit-dispatch.sh: scratch directory creation failed under $tmp_parent; set TMPDIR or ECI_TMPDIR to a writable absolute directory and retry" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}'; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"ECI edit dispatch denied by pretooluse-edit-dispatch.sh: scratch directory creation failed; set TMPDIR or ECI_TMPDIR to a writable absolute directory and retry"}}'
  fi
  exit 0
fi
trap 'rm -rf -- "$tmp_dir"' EXIT HUP INT TERM

input_file="$tmp_dir/input.json"
cat >"$input_file"

tool_name="$(jq -r '.tool_name // empty' <"$input_file" 2>/dev/null || true)"
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
if [ "${ENABLE_SECURITY_REMINDER:-0}" = 1 ] && security_reminder_candidate "$(<"$input_file")"; then
  if . "$hook_dir/lib/eci-native-binary.sh" 2>/dev/null && eci_runtime_open_verified eci-hook-helper; then
    # Release the verification descriptor before any validator forks; the reminder child opens its own.
    exec {ECI_RUNTIME_BINARY_FD}<&-
    reminder_index="${#gates[@]}"
    gates+=("security-reminder")
  else
    printf '%s\n' "${ECI_RUNTIME_DENY_REASON:-ECI native gate binary eci-hook-helper is unavailable: lib/eci-native-binary.sh failed to load; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry}" >&2
  fi
fi
pids=()
for index in "${!gates[@]}"; do
  output_file="$tmp_dir/output-$index"
  error_file="$tmp_dir/error-$index"
  if [ "$index" = "$reminder_index" ]; then
    ( eci_runtime_open_verified eci-hook-helper || { printf '%s\n' "$ECI_RUNTIME_DENY_REASON" >&2; exit 1; }
      "/proc/self/fd/$ECI_RUNTIME_BINARY_FD" security-reminder <"$input_file" >"$output_file" ) 2>"$error_file" &
  else
    bash "${gates[$index]}" <"$input_file" >"$output_file" 2>"$error_file" &
  fi
  pids[$index]=$!
done

# A child may offer a concrete denial, but its transport health is advisory:
# malformed JSON, stderr, and non-zero exit status mean only that this child
# could not help classify the edit.  Another healthy child may still identify
# a real control or cross-session target.
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
for index in "${!gates[@]}"; do
  # Ignore child stderr and health.  Only a well-formed, explicit denial is
  # actionable; all other child outcomes pass through without noise.
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
fi

# The explicitly requested advisory is intentionally outside the provider
# envelope. It can inform the requested review, but never turns an ordinary
# edit into a denial.
if [ -n "$reminder_index" ] && [ -s "$tmp_dir/error-$reminder_index" ]; then
  cat -- "$tmp_dir/error-$reminder_index" >&2
fi

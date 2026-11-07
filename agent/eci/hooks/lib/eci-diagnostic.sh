#!/usr/bin/env bash
# Shared, bounded diagnostics for ECI gate denials.
#
# Keep this shell-only and allocation-free on the allow path.  Gate failures
# call eci_diagnostic_reason with structured context so a denial reads like a
# compiler diagnostic instead of an opaque policy label.

eci_diagnostic_value() {
  local value="${1-}"
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  if [ "${#value}" -gt 4096 ]; then
    value="${value:0:4093}..."
  fi
  printf '%s' "$value"
}

eci_diagnostic_subject() {
  local value="${1-}"
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  if [ "${#value}" -gt 512 ]; then
    value="${value:0:509}..."
  fi
  printf '%s' "$value"
}

eci_diagnostic_code_for_reason() {
  local reason="${1-}"
  if [[ "$reason" =~ ^\[(ECI_[A-Z0-9_]+)\] ]]; then
    local explicit_code="${BASH_REMATCH[1]}"
    case "$explicit_code" in
      ECI_COMMAND_IDENTITY_UNSUPPORTED|ECI_GATE_DENIED|ECI_REVIEW_GATE_DENIED|ECI_STOP_GATE_DENIED|ECI_DIAGNOSTIC_DENIED|ECI_EDIT_ROUTE_DENIED)
        reason="${reason#\[$explicit_code\]}"; reason="${reason# }"; reason="${reason#:}"; reason="${reason# }" ;;
      *) printf '%s' "$explicit_code"; return 0 ;;
    esac
  fi
  case "$reason" in
    *command\ identity*|*unsupported\ wrapper*|*unrecognized\ command\ form*) printf '%s' "ECI_COMMAND_IDENTITY_INVALID" ;;
    *pre-reviewer*|*pre\ reviewer*) printf '%s' "ECI_PRE_REVIEWER_SKILL_OR_DELEGATION_REQUIRED" ;;
    *external-review*|*external\ review*) printf '%s' "ECI_STOP_REVIEW_VIOLATION_BLOCKED" ;;
    *git-mutation*|*git\ mutation*) printf '%s' "ECI_GIT_MUTATION_APPROVAL_REQUIRED" ;;
    *agent-dispatch*|*Agent\ envelope*) printf '%s' "ECI_FIRST_AGENT_DISPATCH_REQUIRED" ;;
    *'ECI_STOP_STATE_UNAVAILABLE'*) printf '%s' "ECI_STOP_STATE_UNAVAILABLE" ;;
    *'LOOP DETECTED'*) printf '%s' "ECI_STOP_LOOP_DETECTED" ;;
    *'active marker'*|*marker*unresolved*|*marker*invalid*) printf '%s' "ECI_STOP_MARKER_INVALID" ;;
    *review\ gate*usage*|Usage:\ *eci-review-gate*) printf '%s' "ECI_REVIEW_USAGE_DENIED" ;;
    *proof\ root*) printf '%s' "ECI_REVIEW_PROOF_ROOT_DENIED" ;;
    *session\ director*|*session\ directory*) printf '%s' "ECI_REVIEW_SESSION_DIR_DENIED" ;;
    *manifest*) printf '%s' "ECI_REVIEW_MANIFEST_DENIED" ;;
    *schema*) printf '%s' "ECI_REVIEW_SCHEMA_DENIED" ;;
    *binding*) printf '%s' "ECI_REVIEW_BINDING_DENIED" ;;
    *artifact*) printf '%s' "ECI_REVIEW_ARTIFACT_DENIED" ;;
    *target*) printf '%s' "ECI_REVIEW_TARGET_DENIED" ;;
    *diff*) printf '%s' "ECI_REVIEW_DIFF_DENIED" ;;
    *critic*) printf '%s' "ECI_REVIEW_CRITIC_DENIED" ;;
    *ledger*) printf '%s' "ECI_REVIEW_LEDGER_DENIED" ;;
    *transaction*) printf '%s' "ECI_REVIEW_TRANSACTION_DENIED" ;;
    *anchor*) printf '%s' "ECI_REVIEW_ANCHOR_DENIED" ;;
    *receipt*) printf '%s' "ECI_REVIEW_RECEIPT_DENIED" ;;
    *acceptance_version*|*acceptance\ version*) printf '%s' "ECI_REVIEW_ACCEPTANCE_VERSION_DENIED" ;;
    *version*) printf '%s' "ECI_REVIEW_VERSION_DENIED" ;;
    *git\ push*) printf '%s' "ECI_GIT_PUSH_DENIED" ;;
    *no-progress*|*no\ progress*|*no\ terminal\ output*|*blocked\ on\ 3\ times*) printf '%s' "ECI_NO_PROGRESS_BLOCK_DENIED" ;;
    *lock*) printf '%s' "ECI_REVIEW_LOCK_DENIED" ;;
    *could\ not*|*cannot*|*unable*) printf '%s' "ECI_REVIEW_IO_DENIED" ;;
    *unsafe\ *path*|*unsafe\ resolved*path*) printf '%s' "ECI_UNSAFE_PATH_DENIED" ;;
    *requires\ a\ current\ session*|*current\ session\ id*) printf '%s' "ECI_SESSION_REQUIRED" ;;
    *belongs\ to\ session*|*allowed\ sessions*) printf '%s' "ECI_SESSION_OWNERSHIP_DENIED" ;;
    *ownership\ check\ failed*) printf '%s' "ECI_OWNERSHIP_CHECK_FAILED" ;;
    *plan\ files*|*docs/plans*|*superpowers/plans*) printf '%s' "ECI_PLAN_PATH_DENIED" ;;
    *vendor*|*third-party*|*thirdparty*) printf '%s' "ECI_VENDOR_PATH_DENIED" ;;
    *git\ submodule*) printf '%s' "ECI_SUBMODULE_PATH_DENIED" ;;
    *local\ relative\ replace*|*local\ relative*go.mod*) printf '%s' "ECI_GOMOD_LOCAL_REPLACE_DENIED" ;;
    *finite\ memory\ cap*|*uncapped\ make*) printf '%s' "ECI_MAKE_CAP_REQUIRED" ;;
    *-count=1*|*-count\ 1*|*test\ cache*) printf '%s' "ECI_GO_TEST_CACHE_DENIED" ;;
    *go\ test\ output*|*captured\ to\ a\ file*) printf '%s' "ECI_GO_TEST_OUTPUT_DENIED" ;;
    *git\ reset*) printf '%s' "ECI_GIT_RESET_DENIED" ;;
    *)
      local normalized="${reason^^}"
      normalized="${normalized//[^A-Z0-9]/_}"
      while [[ "$normalized" == *__* ]]; do normalized="${normalized//__/_}"; done
      normalized="${normalized##_}"
      normalized="${normalized%_}"
      [ -n "$normalized" ] || normalized="UNSPECIFIED"
      normalized="${normalized:0:48}"
      printf 'ECI_GATE_FAILURE_%s' "$normalized"
      ;;
  esac
}

eci_diagnostic_reason() {
  local code="${1-}"
  [ -n "$code" ] || code="ECI_DIAGNOSTIC_CODE_MISSING"
  local phase="${2:-Hook}"
  local operation="${3:-context-validation}"
  local subject="${4:-missing-context-field}"
  local reason="${5:-required diagnostic context field is missing}"
  local remediation="${6:-supply a stable code, phase, operation, subject, reason, and remediation before retrying}"
  case "$code" in
    \[ECI_[A-Z0-9_]*\]) code="${code#[}"; code="${code%]}" ;;
  esac
  if [[ "$reason" =~ ^\[(ECI_[A-Z0-9_]+)\] ]]; then
    case "${BASH_REMATCH[1]}" in
      ECI_COMMAND_IDENTITY_UNSUPPORTED|ECI_GATE_DENIED|ECI_REVIEW_GATE_DENIED|ECI_STOP_GATE_DENIED|ECI_DIAGNOSTIC_DENIED|ECI_EDIT_ROUTE_DENIED)
        reason="${reason#\[${BASH_REMATCH[1]}\]}"; reason="${reason# }"; reason="${reason#:}"; reason="${reason# }" ;;
    esac
  fi
  case "$code" in
    ECI_COMMAND_IDENTITY_UNSUPPORTED|ECI_GATE_DENIED|ECI_REVIEW_GATE_DENIED|ECI_STOP_GATE_DENIED|ECI_DIAGNOSTIC_DENIED|ECI_EDIT_ROUTE_DENIED) code="$(eci_diagnostic_code_for_reason "$reason")" ;;
  esac
  local context=""
  context="[$code] ECI gate denied (phase=$phase, operation=$operation, subject=$(eci_diagnostic_subject "$subject")); reason: $(eci_diagnostic_value "$reason"); remediation: $(eci_diagnostic_value "$remediation")"
  printf '%s' "$context"
}

eci_command_identity_subject() {
  local command="${1-}" wrapper payload
  case "$command" in
    bash\ -c\ *|sh\ -c\ *|zsh\ -c\ *|dash\ -c\ *)
      wrapper="${command%% -c *} -c"
      payload="${command#* -c }"
      ;;
    bash\ *|sh\ *|zsh\ *|dash\ *)
      wrapper="${command%% *}"
      payload="${command#* }"
      ;;
    env\ *|command\ *|builtin\ *|exec\ *)
      wrapper="${command%% *}"
      payload="${command#* }"
      ;;
    python\ *|python3\ *|perl\ *|ruby\ *|node\ *|deno\ *|go\ run\ *)
      wrapper="${command%% *}"
      payload="${command#* }"
      case "$payload" in
        -c\ *|-e\ *|eval\ *|run\ *)
          wrapper="$wrapper ${payload%% *}"
          payload="${payload#* }"
          ;;
      esac
      ;;
    *)
      wrapper="literal"
      payload="$command"
      ;;
  esac
  printf 'wrapper=%s,payload=%s' "$(eci_diagnostic_value "$wrapper")" "$(eci_diagnostic_value "$payload")"
}

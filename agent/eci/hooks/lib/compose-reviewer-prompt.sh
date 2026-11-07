# Shellcheck-friendly library: compose the reviewer system prompt.
# shellcheck shell=bash

COMPOSE_REVIEWER_LIB_DIR="${BASH_SOURCE[0]%/*}"
# shellcheck source=eci-diagnostic.sh
. "$COMPOSE_REVIEWER_LIB_DIR/eci-diagnostic.sh"

compose_reviewer_diagnostic() {
  local code="$1"
  local subject="$2"
  local reason="$3"
  local remediation="$4"
  printf '%s\n' "$(eci_diagnostic_reason "$code" "Stop" "external-review-config" "$subject" "$reason" "$remediation")" >&2
}

compose_reviewer_prompt() {
  local wrapper="$1"
  local instructions="$HOME/.omp/agent/AGENTS.md"
  local stop_checklist="$HOME/.omp/agent/eci/hooks/stop-checklist.md"
  local import_summary="$HOME/.omp/agent/eci/memories/migration-import/ACTIVE-SUMMARY.md"
  local legacy_import_summary="$HOME/.omp/agent/eci/memories/claude"'-import/ACTIVE-SUMMARY.md'

  if [ ! -f "$wrapper" ]; then
    local safe_wrapper
    safe_wrapper="$(eci_diagnostic_value "$wrapper")"
    compose_reviewer_diagnostic \
      "ECI_REVIEW_PROMPT_WRAPPER_MISSING" \
      "path=$safe_wrapper,source=reviewer-rules" \
      "compose_reviewer_prompt: wrapper not found: $safe_wrapper" \
      "restore the reviewer-rules wrapper at the reported path, then retry"
    return 1
  fi
  if [ ! -f "$instructions" ]; then
    local safe_instructions
    safe_instructions="$(eci_diagnostic_value "$instructions")"
    compose_reviewer_diagnostic \
      "ECI_REVIEW_INSTRUCTIONS_MISSING" \
      "path=$safe_instructions,source=global-instructions" \
      "compose_reviewer_prompt: AGENTS.md not found: $safe_instructions" \
      "restore the global AGENTS.md instructions file at the reported path, then retry"
    return 1
  fi

  cat "$wrapper"
  printf '\n\n============================================================\n'
  printf '# AGENTS.md (user global instructions)\n'
  printf '============================================================\n\n'
  cat "$instructions"
  printf '\n'

  if [ -f "$stop_checklist" ]; then
    printf '\n============================================================\n'
    printf '# stop-checklist.md (acceptance criteria for ending a turn)\n'
    printf '============================================================\n\n'
    cat "$stop_checklist"
    printf '\n'
  fi

  if [ ! -f "$import_summary" ] && [ -f "$legacy_import_summary" ]; then
    import_summary="$legacy_import_summary"
  fi

  if [ -f "$import_summary" ]; then
    printf '\n============================================================\n'
    printf '# imported migration summary (migration notes)\n'
    printf '============================================================\n\n'
    cat "$import_summary"
    printf '\n'
  fi
}

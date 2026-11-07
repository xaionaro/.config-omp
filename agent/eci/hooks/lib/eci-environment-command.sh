#!/usr/bin/env bash

# Role-neutral tri-state recognizer for environment commands.  It reports
# only argv shape and query form; caller-owned gates decide whether a command
# otherwise reaches an actual mutation or control target.

source "${BASH_SOURCE[0]%/*}/eci-runtime-sync.sh"

environment_command_detail() {
  local direct_command="${1:-}" direct_name direct_fast=true
  local -a direct_words=()

  # The overwhelmingly common environment query is a direct, literal
  # `printenv NAME...` with bounded identifier names.  Keep
  # this path entirely in Bash: it avoids starting the Go helper for a query
  # whose grammar is already expressible as one argv check.  Anything that
  # is not provably this exact shape falls through to the complete recognizer
  # below, preserving its diagnostics and wrapper handling.
  read -r -a direct_words <<<"$direct_command"
  if [ "${direct_words[0]:-}" = printenv ] &&
     [ "${#direct_words[@]}" -ge 2 ] && [ "${#direct_words[@]}" -le 17 ]; then
    local -A direct_seen=()
    for direct_name in "${direct_words[@]:1}"; do
      if [[ ! "$direct_name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
        direct_fast=false
        break
      fi
      if [[ -n "${direct_seen[$direct_name]+set}" ]]; then
        direct_fast=false
        break
      fi
      direct_seen["$direct_name"]=1
    done
    if [ "$direct_fast" = true ]; then
      # This opaque compatibility value is consumed by validate-bash.  It
      # describes a direct named-query shape, not membership in an allowlist.
      printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
        ALLOW printenv 1 0 printenv direct-registered-query
      return 0
    fi
  fi

  local helper
  helper="$(eci_runtime_hook_helper_path)" || return 1
  "$helper" environment-command "$direct_command"
}

enforce_environment_command_boundary() {
  local detail state code segment argv_index token reason_key
  detail="$(environment_command_detail "$command" 2>/dev/null || true)"
  ECI_ENVIRONMENT_BOUNDARY_CHECKED=true
  ECI_ENVIRONMENT_COMMAND_STATE=""
  ECI_ENVIRONMENT_COMMAND_CODE=""
  ECI_ENVIRONMENT_COMMAND_SEGMENT=""
  ECI_ENVIRONMENT_COMMAND_ARGV_INDEX=""
  ECI_ENVIRONMENT_COMMAND_REASON=""
  [ -n "$detail" ] || return 0
  IFS=$'\t' read -r state code segment argv_index token reason_key <<<"$detail"
  ECI_ENVIRONMENT_COMMAND_STATE="$state"
  ECI_ENVIRONMENT_COMMAND_CODE="$code"
  ECI_ENVIRONMENT_COMMAND_SEGMENT="$segment"
  ECI_ENVIRONMENT_COMMAND_ARGV_INDEX="$argv_index"
  ECI_ENVIRONMENT_COMMAND_REASON="$reason_key"
  # This recognizer can describe ambiguous or unusual `env` / `printenv`
  # spelling, but that spelling by itself does not resolve to a harmful
  # target.  Environment inspection and wrapper setup are ordinary work;
  # downstream target-aware checks own any actual filesystem, control-state,
  # or destructive effect.  Keep the classification available to callers as
  # an advisory rather than turning it into an access boundary.
  [ "$state" = DENY ] || return 0
  ECI_ENVIRONMENT_COMMAND_STATE="ADVISORY"
}

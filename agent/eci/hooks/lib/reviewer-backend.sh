# Shellcheck-friendly library: parse no-credential reviewer backend specs.
# shellcheck shell=bash

REVIEWER_BACKEND_LIB_DIR="${BASH_SOURCE[0]%/*}"
# shellcheck source=eci-diagnostic.sh
. "$REVIEWER_BACKEND_LIB_DIR/eci-diagnostic.sh"

SYNTHETIC_USER_TAG_RE='^[[:space:]]*<(task-notification|command-name|command-message|command-args|local-command-stdout|local-command-caveat|system-reminder)>'

reviewer_backend_diagnostic() {
  local code="$1"
  local operation="$2"
  local subject="$3"
  local reason="$4"
  local remediation="$5"
  printf '%s\n' "$(eci_diagnostic_reason "$code" "Stop" "$operation" "$subject" "$reason" "$remediation")" >&2
}

reviewer_reset_backend() {
  REVIEWER_BACKEND=""
  REVIEWER_OLLAMA_HOST=""
  REVIEWER_OLLAMA_MODEL=""
  REVIEWER_OPENCODE_HOST=""
  REVIEWER_OPENCODE_MODEL=""
}

parse_reviewer_env() {
  local env_name="${1:-ECI_STOP_REVIEWER}"
  local raw="${!env_name:-}"

  reviewer_reset_backend
  [ -z "$raw" ] && return 0

  case "$raw" in
    ollama:*)
      local rest="${raw#ollama:}"
      if [[ "$rest" =~ ^([a-zA-Z][a-zA-Z0-9+.-]*://[^:/[:space:]]+(:[0-9]+)?)/?:(.+)$ ]]; then
        REVIEWER_BACKEND="ollama"
        REVIEWER_OLLAMA_HOST="${BASH_REMATCH[1]}"
        REVIEWER_OLLAMA_MODEL="${BASH_REMATCH[3]}"
        return 0
      fi
      local safe_raw
      safe_raw="$(eci_diagnostic_value "$raw")"
      reviewer_backend_diagnostic \
        "ECI_REVIEWER_BACKEND_MALFORMED" \
        "external-review-config" \
        "env=$env_name,backend=ollama" \
        "reviewer-backend: malformed $env_name=$safe_raw (expected ollama:scheme://host[:port]:MODEL)" \
        "set $env_name to ollama:scheme://host[:port]:MODEL or leave it empty, then retry"
      return 1
      ;;
    opencode-zen:*)
      local rest="${raw#opencode-zen:}"
      if [[ "$rest" =~ ^([a-zA-Z][a-zA-Z0-9+.-]*://[^:/[:space:]]+(:[0-9]+)?)/?:(.+)$ ]]; then
        REVIEWER_BACKEND="opencode-zen"
        REVIEWER_OPENCODE_HOST="${BASH_REMATCH[1]}"
        REVIEWER_OPENCODE_MODEL="${BASH_REMATCH[3]}"
        return 0
      fi
      local safe_raw
      safe_raw="$(eci_diagnostic_value "$raw")"
      reviewer_backend_diagnostic \
        "ECI_REVIEWER_BACKEND_MALFORMED" \
        "external-review-config" \
        "env=$env_name,backend=opencode-zen" \
        "reviewer-backend: malformed $env_name=$safe_raw (expected opencode-zen:scheme://host[:port]:MODEL)" \
        "set $env_name to opencode-zen:scheme://host[:port]:MODEL or leave it empty, then retry"
      return 1
      ;;
    *)
      local safe_raw safe_backend
      safe_raw="$(eci_diagnostic_value "$raw")"
      safe_backend="$(eci_diagnostic_value "${raw%%:*}")"
      reviewer_backend_diagnostic \
        "ECI_REVIEWER_BACKEND_UNSUPPORTED" \
        "external-review-config" \
        "env=$env_name,backend=$safe_backend" \
        "reviewer-backend: unknown $env_name=$safe_raw (review skipped; allowed: ollama:URL:MODEL, opencode-zen:URL:MODEL)" \
        "use ollama:URL:MODEL or opencode-zen:URL:MODEL, or leave reviewer configuration empty"
      return 1
      ;;
  esac
}

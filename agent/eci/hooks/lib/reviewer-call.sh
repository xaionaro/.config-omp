# Shellcheck-friendly library: no-credential reviewer call defaults/helpers.
# shellcheck shell=bash

: "${ECI_STOP_REVIEWER_TIMEOUT:=240}"
: "${ECI_EDIT_PRE_REVIEWER_TIMEOUT:=58}"

reviewer_ollama_options() {
  local seed="${1:-42}"
  jq -n --argjson seed "$seed" '{
    temperature: 0.3,
    top_k: 40,
    top_p: 0.9,
    seed: $seed,
    num_ctx: 32768,
    num_predict: 2048,
    repeat_penalty: 1.0
  }'
}

reviewer_strip_fences() {
  sed -E '/^[[:space:]]*```[a-zA-Z]*[[:space:]]*$/d; /^[[:space:]]*```[[:space:]]*$/d'
}

reviewer_run_with_timeout() {
  local timeout_secs="$1"
  shift
  if timeout --version 2>&1 | grep -Fq 'GNU coreutils'; then
    timeout --signal=TERM --kill-after=1s "${timeout_secs}s" "$@"
  elif command -v busybox >/dev/null 2>&1; then
    busybox timeout -s TERM -k 1 "$timeout_secs" "$@"
  else
    return 1
  fi
}

reviewer_tmp_root() {
  local root="${TMPDIR:-${HOME:?HOME must be set}/tmp}" canonical
  canonical="$(realpath -m -- "$root" 2>/dev/null || true)"
  if [ "$canonical" = /tmp ] || [[ "$canonical" == /tmp/* ]]; then
    root="${HOME:?HOME must be set}/tmp"
  fi
  mkdir -p -- "$root" 2>/dev/null || return 1
  printf '%s\n' "$root"
}

_reviewer_call_chat_impl() {
  local kind="$1"
  local sys_file="$2"
  local usr_file="$3"
  local schema_file="$4"
  local timeout_secs="$5"
  local curl_timeout_secs="$6"
  local raw response http_code body send_path

  send_path=""
  trap '[ -z "$send_path" ] || rm -f -- "$send_path"' EXIT HUP INT TERM

  case "$REVIEWER_BACKEND" in
    ollama)
      send_path=$(mktemp "$(reviewer_tmp_root)/eci-reviewer-call.XXXXXX") || return 1
      jq -n \
        --arg model "$REVIEWER_OLLAMA_MODEL" \
        --rawfile sys "$sys_file" \
        --rawfile usr "$usr_file" \
        --argjson schema "$(cat "$schema_file")" \
        --argjson options "$(reviewer_ollama_options 42)" \
        '{model:$model,stream:false,think:false,format:$schema,options:$options,
          messages:[{role:"system",content:$sys},{role:"user",content:$usr}]}' >"$send_path"
      response=$(reviewer_run_with_timeout "$curl_timeout_secs" \
        curl -s --max-time "$curl_timeout_secs" \
        -X POST "$REVIEWER_OLLAMA_HOST/api/chat" \
        -H 'Content-Type: application/json' \
        --data-binary "@$send_path" \
        -w '\n%{http_code}' 2>/dev/null)
      local exit_call=$?
      rm -f -- "$send_path"
      send_path=""
      [ "$exit_call" -eq 0 ] || return 1
      http_code=$(printf '%s' "$response" | tail -n1)
      body=$(printf '%s' "$response" | sed '$d')
      case "$http_code" in 2*) ;; *) return 1 ;; esac
      raw=$(printf '%s' "$body" | jq -r '.message.content // empty' 2>/dev/null)
      ;;
    opencode-zen)
      send_path=$(mktemp "$(reviewer_tmp_root)/eci-reviewer-call.XXXXXX") || return 1
      jq -n \
        --arg model "$REVIEWER_OPENCODE_MODEL" \
        --rawfile sys "$sys_file" \
        --rawfile usr "$usr_file" \
        --argjson schema "$(cat "$schema_file")" \
        --arg name "${kind}_verdict" \
        '{model:$model,stream:false,max_tokens:8192,max_completion_tokens:8192,
          temperature:0.3,top_p:0.9,seed:42,
          response_format:{type:"json_schema",json_schema:{name:$name,schema:$schema,strict:false}},
          messages:[{role:"system",content:$sys},{role:"user",content:$usr}]}' >"$send_path"
      response=$(reviewer_run_with_timeout "$curl_timeout_secs" \
        curl -s --max-time "$curl_timeout_secs" \
        -X POST "$REVIEWER_OPENCODE_HOST/zen/v1/chat/completions" \
        -H 'Content-Type: application/json' \
        --data-binary "@$send_path" \
        -w '\n%{http_code}' 2>/dev/null)
      local exit_call=$?
      rm -f -- "$send_path"
      send_path=""
      [ "$exit_call" -eq 0 ] || return 1
      http_code=$(printf '%s' "$response" | tail -n1)
      body=$(printf '%s' "$response" | sed '$d')
      case "$http_code" in 2*) ;; *) return 1 ;; esac
      raw=$(printf '%s' "$body" | jq -r '.choices[0].message.content // empty' 2>/dev/null)
      ;;
    *)
      return 1
      ;;
  esac

  [ -n "$raw" ] || return 1
  printf '%s' "$raw" | reviewer_strip_fences
}

reviewer_call_chat() {
  local kind="$1"
  local sys_file="$2"
  local usr_file="$3"
  local schema_file="$4"
  local timeout_secs="$5"
  local curl_timeout_secs
  local outer_term_secs
  local library

  case "$timeout_secs" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$timeout_secs" -ge 3 ] || return 1
  curl_timeout_secs=$((timeout_secs - 2))
  outer_term_secs=$((timeout_secs - 1))
  library="${BASH_SOURCE[0]}"

  REVIEWER_BACKEND="${REVIEWER_BACKEND:-}" \
  REVIEWER_OLLAMA_MODEL="${REVIEWER_OLLAMA_MODEL:-}" \
  REVIEWER_OLLAMA_HOST="${REVIEWER_OLLAMA_HOST:-}" \
  REVIEWER_OPENCODE_MODEL="${REVIEWER_OPENCODE_MODEL:-}" \
  REVIEWER_OPENCODE_HOST="${REVIEWER_OPENCODE_HOST:-}" \
    reviewer_run_with_timeout "$outer_term_secs" \
      bash -c 'source "$1"; _reviewer_call_chat_impl "${@:2}"' \
      reviewer-call "$library" "$kind" "$sys_file" "$usr_file" \
      "$schema_file" "$timeout_secs" "$curl_timeout_secs"
}

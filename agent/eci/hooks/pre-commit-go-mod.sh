#!/usr/bin/env bash

set -euo pipefail

hook_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
. "$hook_dir/lib/eci-diagnostic.sh"

go_hook_deny() {
  local code="$1" operation="$2" subject="$3" reason="$4" remediation="$5"
  printf '%s\n' "$(eci_diagnostic_reason "$code" "PreCommit" "$operation" "$subject" "$reason" "$remediation")" >&2
}

max_blob_bytes=${GO_MOD_HOOK_MAX_BYTES:-1048576}
if [[ ! ${max_blob_bytes} =~ ^[1-9][0-9]*$ ]]; then
  go_hook_deny "GO_HOOK_CONFIG_INVALID" "configuration" \
    "env=GO_MOD_HOOK_MAX_BYTES,value=$(eci_diagnostic_value "$max_blob_bytes")" \
    'go.mod hook: GO_MOD_HOOK_MAX_BYTES must be a positive integer' \
    'set GO_MOD_HOOK_MAX_BYTES to a positive integer, then retry the commit'
  exit 2
fi

tmp_root="${ECI_TMPDIR:-$HOME/tmp}"
tmp_root_canonical="$(realpath -m -- "$tmp_root" 2>/dev/null || true)"
[ -n "$tmp_root_canonical" ] || tmp_root_canonical="$tmp_root"
if [ "$tmp_root_canonical" = /tmp ] || [[ "$tmp_root_canonical" == /tmp/* ]]; then
  go_hook_deny "GO_HOOK_TMPDIR_UNSAFE" "temporary-directory" \
    "requested=$(eci_diagnostic_value "$tmp_root"),canonical=$(eci_diagnostic_value "$tmp_root_canonical")" \
    'go.mod hook: system temporary root /tmp is not an ECI scratch location' \
  'unset ECI_TMPDIR or set it to a writable home-scoped directory such as $HOME/tmp'
  exit 2
fi
mkdir -p -- "$tmp_root"
tmp_dir=$(mktemp -d "$tmp_root/go-mod-hook.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT HUP INT TERM

index_listing=$tmp_dir/index-listing
if ! git ls-files -z --cached >"$index_listing"; then
  go_hook_deny "GO_HOOK_INDEX_UNREADABLE" "go-index" \
    "repo=$(eci_diagnostic_value "$PWD")" \
    'go.mod hook: unable to enumerate the Git index' \
    'repair the repository index or Git access, then retry the commit'
  exit 2
fi

module_paths=()
workspace_paths=()
while IFS= read -r -d '' path; do
  case ${path##*/} in
    go.mod) module_paths+=("$path") ;;
    go.work) workspace_paths+=("$path") ;;
  esac
done <"$index_listing"

if ((${#module_paths[@]} == 0 && ${#workspace_paths[@]} == 0)); then
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  go_hook_deny "GO_TOOL_MISSING" "go-mod-check" "tool=go" \
    'go.mod hook: go is required to validate indexed go.mod files' \
    'install or expose the Go toolchain, then retry the commit'
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  go_hook_deny "GO_SCHEMA_TOOL_MISSING" "go-schema-check" "tool=jq" \
    'go.mod hook: jq is required to validate indexed go.mod files' \
    'install or expose jq, then retry the commit'
  exit 2
fi

for path in "${module_paths[@]}"; do
  size=$(git cat-file -s ":$path" 2>/dev/null) || {
    go_hook_deny "GO_MOD_INDEX_FILE_UNREADABLE" "go-mod-index" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.mod hook: unable to read indexed file $path" \
      'restore the indexed go.mod bytes or repair Git index access, then retry'
    exit 2
  }
  if [[ ! ${size} =~ ^[0-9]+$ ]]; then
    go_hook_deny "GO_MOD_INDEX_SIZE_INVALID" "go-mod-index" \
      "path=$(eci_diagnostic_value "$path"),size=$(eci_diagnostic_value "$size")" \
      "go.mod hook: Git returned an invalid size for indexed file $path" \
      'repair the Git index response, then retry'
    exit 2
  fi
  if ((size > max_blob_bytes)); then
    go_hook_deny "GO_MOD_INDEX_TOO_LARGE" "go-mod-index" \
      "path=$(eci_diagnostic_value "$path"),size=$(eci_diagnostic_value "$size"),limit=$(eci_diagnostic_value "$max_blob_bytes")" \
      "go.mod hook: indexed file $path is $size bytes; maximum is $max_blob_bytes" \
      'reduce the indexed go.mod size below the configured limit, then retry'
    exit 2
  fi

  mod_file=$(mktemp "$tmp_dir/module.XXXXXX.mod")
  json_file=$(mktemp "$tmp_dir/module.XXXXXX.json")
  if ! git cat-file blob ":$path" >"$mod_file" 2>/dev/null; then
    go_hook_deny "GO_MOD_INDEX_FILE_UNREADABLE" "go-mod-index" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.mod hook: unable to read indexed file $path" \
      'restore the indexed go.mod bytes or repair Git index access, then retry'
    exit 2
  fi

  if ! GOFLAGS= GOWORK=off go mod edit -json "$mod_file" >"$json_file" 2>"$tmp_dir/go-error"; then
    go_error=$(sed -n '1,8p' "$tmp_dir/go-error" 2>/dev/null || true)
    go_hook_deny "GO_MOD_PARSE_INVALID" "go-mod-parse" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.mod hook: indexed file $path is not a valid go.mod: $go_error" \
      'fix the indexed go.mod syntax, then retry the commit'
    exit 1
  fi

  if ! jq -e '
    (type == "object") and
    (.Module | type == "object") and
    (.Module | has("Path")) and
    (.Module.Path | type == "string" and length > 0) and
    ((has("Replace") | not) or (.Replace == null) or (.Replace | type == "array")) and
    all((.Replace // [])[];
      (type == "object") and
      (.Old | type == "object") and
      (.Old | has("Path")) and
      (.Old.Path | type == "string" and length > 0) and
      (.New | type == "object") and
      (.New | has("Path")) and
      (.New.Path | type == "string" and length > 0) and
      ((.New.Version // "") | type == "string")
    )
  ' "$json_file" >/dev/null 2>&1; then
    go_hook_deny "GO_MOD_SCHEMA_INVALID" "go-mod-schema" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.mod hook: go produced an invalid module schema for indexed file $path" \
      'use a supported go.mod schema and retry the commit'
    exit 2
  fi

  local_replacement=$(jq -r '
    (.Replace // [])[] | select((.New.Version // "") == "") | .New.Path
  ' "$json_file" | sed -n '1p')
  if [[ -n ${local_replacement} ]]; then
    go_hook_deny "GO_MOD_LOCAL_REPLACE_DENIED" "go-mod-policy" \
      "path=$(eci_diagnostic_value "$path"),replace=$(eci_diagnostic_value "$local_replacement")" \
      "go.mod hook: indexed file $path contains local replacement $local_replacement; put local replacements in go.work" \
      'move the local replacement to a relative go.work entry, then retry the commit'
    exit 1
  fi
done

for path in "${workspace_paths[@]}"; do
  size=$(git cat-file -s ":$path" 2>/dev/null) || {
    go_hook_deny "GO_WORK_INDEX_FILE_UNREADABLE" "go-work-index" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.work hook: unable to read indexed file $path" \
      'restore the indexed go.work bytes or repair Git index access, then retry'
    exit 2
  }
  if [[ ! ${size} =~ ^[0-9]+$ ]]; then
    go_hook_deny "GO_WORK_INDEX_SIZE_INVALID" "go-work-index" \
      "path=$(eci_diagnostic_value "$path"),size=$(eci_diagnostic_value "$size")" \
      "go.work hook: Git returned an invalid size for indexed file $path" \
      'repair the Git index response, then retry'
    exit 2
  fi
  if ((size > max_blob_bytes)); then
    go_hook_deny "GO_WORK_INDEX_TOO_LARGE" "go-work-index" \
      "path=$(eci_diagnostic_value "$path"),size=$(eci_diagnostic_value "$size"),limit=$(eci_diagnostic_value "$max_blob_bytes")" \
      "go.work hook: indexed file $path is $size bytes; maximum is $max_blob_bytes" \
      'reduce the indexed go.work size below the configured limit, then retry'
    exit 2
  fi

  work_file=$(mktemp "$tmp_dir/workspace.XXXXXX.work")
  json_file=$(mktemp "$tmp_dir/workspace.XXXXXX.json")
  if ! git cat-file blob ":$path" >"$work_file" 2>/dev/null; then
    go_hook_deny "GO_WORK_INDEX_FILE_UNREADABLE" "go-work-index" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.work hook: unable to read indexed file $path" \
      'restore the indexed go.work bytes or repair Git index access, then retry'
    exit 2
  fi

  absolute_workspace_use=$(awk '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }
    function is_absolute(value, colon) {
      if (value ~ /^\// || substr(value, 1, 1) == sprintf("%c", 92)) {
        return 1
      }
      colon = index(value, ":")
      return colon == 2 &&
        (substr(value, 3, 1) == "/" || substr(value, 3, 1) == sprintf("%c", 92))
    }
    {
      line = $0
      sub(/[[:space:]]*\/\/.*/, "", line)
      line = trim(line)
      if (line == "use (") {
        in_use_block = 1
        next
      }
      if (in_use_block && line == ")") {
        in_use_block = 0
        next
      }
      if (line ~ /^use[[:space:]]+/) {
        candidate = line
        sub(/^use[[:space:]]+/, "", candidate)
      } else if (in_use_block) {
        candidate = line
      } else {
        next
      }
      candidate = trim(candidate)
      if (candidate == "") {
        next
      }
      split(candidate, fields, /[[:space:]]+/)
      if (is_absolute(fields[1])) {
        print fields[1]
        exit
      }
    }
  ' "$work_file" | sed -n '1p')
  if [[ -n "$absolute_workspace_use" ]]; then
    go_hook_deny "GO_WORK_ABSOLUTE_USE_DENIED" "go-work-policy" \
      "path=$(eci_diagnostic_value "$path"),use=$(eci_diagnostic_value "$absolute_workspace_use")" \
      "go.work hook: indexed file $path contains absolute use path $absolute_workspace_use; use a relative module path" \
      'replace the absolute use path with a relative module path, then retry the commit'
    exit 1
  fi

  if ! GOFLAGS= GOWORK=off go work edit -json "$work_file" >"$json_file" 2>"$tmp_dir/go-error"; then
    go_error=$(sed -n '1,8p' "$tmp_dir/go-error" 2>/dev/null || true)
    go_hook_deny "GO_WORK_PARSE_INVALID" "go-work-parse" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.work hook: indexed file $path is not a valid go.work: $go_error" \
      'fix the indexed go.work syntax, then retry the commit'
    exit 1
  fi

  if ! jq -e '
    (type == "object") and
    ((has("Use") | not) or (.Use == null) or (.Use | type == "array")) and
    all((.Use // [])[];
      (type == "object") and
      (.DiskPath | type == "string" and length > 0)
    ) and
    ((has("Replace") | not) or (.Replace == null) or (.Replace | type == "array")) and
    all((.Replace // [])[];
      (type == "object") and
      (.Old | type == "object") and
      (.Old | has("Path")) and
      (.Old.Path | type == "string" and length > 0) and
      (.New | type == "object") and
      (.New | has("Path")) and
      (.New.Path | type == "string" and length > 0) and
      ((.New.Version // "") | type == "string")
    )
  ' "$json_file" >/dev/null 2>&1; then
    go_hook_deny "GO_WORK_SCHEMA_INVALID" "go-work-schema" \
      "path=$(eci_diagnostic_value "$path")" \
      "go.work hook: go produced an invalid workspace schema for indexed file $path" \
      'use a supported go.work schema and retry the commit'
    exit 2
  fi

  absolute_workspace_use=$(jq -r '
    (.Use // [])[] |
    select(.DiskPath | test("^(\/|[A-Za-z]:[\\\\/]|\\\\|\\\\\\\\)")) |
    .DiskPath
  ' "$json_file" | sed -n '1p')
  if [[ -n "$absolute_workspace_use" ]]; then
    go_hook_deny "GO_WORK_ABSOLUTE_USE_DENIED" "go-work-policy" \
      "path=$(eci_diagnostic_value "$path"),use=$(eci_diagnostic_value "$absolute_workspace_use")" \
      "go.work hook: indexed file $path contains absolute use path $absolute_workspace_use; use a relative module path" \
      'replace the absolute use path with a relative module path, then retry the commit'
    exit 1
  fi

  absolute_local_replacement=$(jq -r '
    (.Replace // [])[] |
    select((.New.Version // "") == "" and (.New.Path | test("^(\/|[A-Za-z]:[\\\\/]|\\\\|\\\\\\\\)"))) |
    .New.Path
  ' "$json_file" | sed -n '1p')
  if [[ -n ${absolute_local_replacement} ]]; then
    go_hook_deny "GO_WORK_ABSOLUTE_REPLACE_DENIED" "go-work-policy" \
      "path=$(eci_diagnostic_value "$path"),replace=$(eci_diagnostic_value "$absolute_local_replacement")" \
      "go.work hook: indexed file $path contains absolute local replacement $absolute_local_replacement; use a relative path in go.work" \
      'replace the absolute local replacement with a relative path in go.work, then retry the commit'
    exit 1
  fi
done

exit 0

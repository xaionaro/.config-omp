#!/usr/bin/env bash
# TODO: Retire ECI marker-based refresh and cleanup decisions; native goals own lifecycle.
# SessionStart hook: save git HEAD as the stop-hook baseline.

set -Eeuo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/lib/eci-proof-state.sh"
. "$HOOK_DIR/lib/eci-tmp.sh"
eci_install_advisory_fail_open_trap session-snapshot

input=$(cat)
if ! printf '%s' "$input" | jq -e '
  type == "object" and
  (.session_id? | type) == "string" and
  ((has("transcript_path") | not) or (.transcript_path | type) == "string") and
  ((has("cwd") | not) or (.cwd | type) == "string")
' >/dev/null 2>&1; then
  exit 0
fi
session_id=$(printf '%s' "$input" | jq -r '.session_id' 2>/dev/null || true)
transcript_path=$(printf '%s' "$input" | jq -r '.transcript_path // empty' 2>/dev/null || true)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null || true)
[ -z "$cwd" ] && cwd="$PWD"

eci_valid_session_id "$session_id" || exit 0

[ -n "$transcript_path" ] || exit 0

root="$(eci_proof_root)"
eci_proof_root_is_safe || exit 0

if [ ! -d "$root" ]; then
  mkdir -p "$root" || exit 0
  eci_proof_root_is_safe || exit 0
fi

lock_held=false
lock_path="$(eci_lock_path 2>/dev/null || true)"
if [ -n "$lock_path" ] &&
  { [ ! -e "$lock_path" ] || [ -f "$lock_path" ]; } && [ ! -L "$lock_path" ]; then
  if exec {eci_lock_fd}>>"$lock_path" 2>/dev/null &&
    eci_file_lock_acquire_fd "$eci_lock_fd" nonblocking 2>/dev/null; then
    lock_held=true
    trap 'eci_file_lock_release_fd "$eci_lock_fd" 2>/dev/null || true' EXIT
  else
    if [ -n "${eci_lock_fd:-}" ]; then
      eci_file_lock_release_fd "$eci_lock_fd" 2>/dev/null || true
    fi
  fi
fi

# Do not create scratch state until the input, proof root, session path, and
# mutation lock have all been validated. Lock-busy and unsafe-input branches
# remain read-only.
if [ "$lock_held" = true ]; then
  eci_init_tmp || true
fi

side_stop=$(eci_existing_state_file side-stop side_stop "$session_id" "$cwd" 2>/dev/null || true)
if eci_side_stop_is_active_for_session "$side_stop" "$session_id"; then
  if [ "$lock_held" = true ]; then
    eci_bind_side_stop_to_session "$side_stop" "$session_id" || true
  fi
  exit 0
fi

proof_dir="$root/$session_id"
baseline="$proof_dir/baseline_head"
baseline_binding="$proof_dir/baseline_head.binding"
nested_marker="$proof_dir/ate_nested_eci_active"
baseline_max_bytes=4096
aggregate_plan="$proof_dir/eci-aggregate-plan.json"
aggregate_active=false
aggregate_plan_present=false

nested_marker_is_active() {
  local bytes line_count writer
  local -a lines=()

  [ -f "$nested_marker" ] && [ ! -L "$nested_marker" ] || return 1
  eci_marker_is_valid_for_cwd "$eci_marker" "$(eci_canonical_cwd "$cwd")" || return 1
  bytes="$(wc -c <"$nested_marker" 2>/dev/null || true)"
  case "$bytes" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$bytes" -le 1024 ] || return 1
  [ "$(tail -c 1 -- "$nested_marker" 2>/dev/null | od -An -t x1 | tr -d '[:space:]')" = 0a ] || return 1
  line_count="$(awk 'END { print NR + 0 }' "$nested_marker" 2>/dev/null || printf '0')"
  # Legacy three-line nested markers lack ownership/authentication and must not
  # keep a session in an active refresh state.
  [ "$line_count" -eq 8 ] || return 1
  mapfile -t lines <"$nested_marker" || return 1
  [ "${#lines[@]}" -eq 8 ] || return 1
  [[ "${lines[0]}" == "outer_session_id: $session_id" ]] || return 1
  [[ "${lines[1]}" == "outer_marker: $proof_dir/eci_active" ]] || return 1
  [ "${lines[2]}" = 'owner: ate' ] || return 1
  [[ "${lines[3]}" == writer_session_id:\ * ]] || return 1
  writer="${lines[3]#writer_session_id: }"
  eci_valid_session_id "$writer" || return 1
  [[ "${lines[4]}" =~ ^acceptance_version:[[:space:]]*[1-9][0-9]{0,8}$ ]] || return 1
  [[ "${lines[5]}" =~ ^step:[[:space:]]*(0|[1-9][0-9]{0,8})$ ]] || return 1
  [[ "${lines[6]}" =~ ^iteration:[[:space:]]*(0|[1-9][0-9]{0,8})$ ]] || return 1
  [ "${lines[7]}" = 'state: active' ] || return 1
}

direct_marker_is_active() {
  [ -f "$eci_marker" ] && [ ! -L "$eci_marker" ] || return 1
  eci_marker_is_valid_for_cwd "$eci_marker" "$(eci_canonical_cwd "$cwd")"
}

eci_session_dir_is_safe "$root" "$session_id" || exit 0

if [ "$lock_held" != true ]; then
  ctx='Load ~/.omp/agent/AGENTS.md and matching ~/.omp/agent/skills when applicable.'
  eci_marker="$proof_dir/eci_active"
  if [ -d "$proof_dir" ] && [ ! -L "$proof_dir" ] &&
      { direct_marker_is_active || nested_marker_is_active; }; then
    ctx='ECI is active. ECI refresh signal (not proof of compaction): after compaction, the coordinator/lead must immediately re-read the entire skills/explore-critique-implement/SKILL.md and re-invoke it before the next decision/tool.'
  fi
  jq -n --arg ctx "$ctx" '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:$ctx}}'
  exit 0
fi

mkdir -p "$proof_dir"
eci_session_dir_is_safe "$root" "$session_id" || exit 0

# A recovered aggregate session owns one non-Git parent marker and namespaced
# member proof. Never create the singleton baseline files beside that plan,
# even when SessionStart happens from inside one sibling repository.
if [ -e "$aggregate_plan" ] || [ -L "$aggregate_plan" ]; then
  aggregate_plan_present=true
  if [ -f "$proof_dir/eci_active" ] && [ ! -L "$proof_dir/eci_active" ]; then
    aggregate_outer_raw="$(eci_state_value "$proof_dir/eci_active" cwd false 2>/dev/null || true)"
    if [ -n "$aggregate_outer_raw" ]; then
      aggregate_outer="$(eci_canonical_cwd "$aggregate_outer_raw")"
      if eci_aggregate_plan_is_valid "$aggregate_plan" "$session_id" \
        "$aggregate_outer" "$proof_dir/eci_active"; then
        aggregate_active=true
      fi
    fi
  fi
fi

# The baseline and binding are separate legacy files, so publish them with
# no-overwrite hard links and repair a half-publication on the next
# SessionStart.  A crash after either link cannot leave the review gate
# permanently wedged: the missing mate is reconstructed only from a bounded,
# validated record bound to the current session/repository/cwd.
baseline_context_is_valid() {
  local raw
  baseline_repo="$(eci_git_repo_root_for_path "$cwd" 2>/dev/null || true)"
  [ -n "$baseline_repo" ] || return 1
  baseline_repo="$(realpath -m -- "$baseline_repo" 2>/dev/null || true)"
  baseline_git_dir_raw="$(eci_git_safe -C "$baseline_repo" rev-parse --git-dir 2>/dev/null || true)"
  baseline_git_common_raw="$(eci_git_safe -C "$baseline_repo" rev-parse --git-common-dir 2>/dev/null || true)"
  case "$baseline_git_dir_raw" in
    /*) baseline_git_dir="$baseline_git_dir_raw" ;;
    *) baseline_git_dir="$baseline_repo/$baseline_git_dir_raw" ;;
  esac
  case "$baseline_git_common_raw" in
    /*) baseline_git_common="$baseline_git_common_raw" ;;
    *) baseline_git_common="$baseline_repo/$baseline_git_common_raw" ;;
  esac
  baseline_git_dir="$(realpath -m -- "$baseline_git_dir" 2>/dev/null || true)"
  baseline_git_common="$(realpath -m -- "$baseline_git_common" 2>/dev/null || true)"
  baseline_cwd="$(eci_canonical_cwd "$cwd")"
  [ -n "$baseline_git_dir" ] && [ -n "$baseline_git_common" ] && [ -n "$baseline_cwd" ] || return 1
  [ -d "$baseline_repo" ] && [ ! -L "$baseline_repo" ] || return 1
  [ -d "$baseline_git_dir" ] && [ ! -L "$baseline_git_dir" ] || return 1
  [ -d "$baseline_git_common" ] && [ ! -L "$baseline_git_common" ] || return 1
}

baseline_head_file_is_valid() {
  local bytes line_count
  [ -f "$baseline" ] && [ ! -L "$baseline" ] || return 1
  bytes="$(wc -c <"$baseline" 2>/dev/null || true)"
  case "$bytes" in ''|*[!0-9]*) return 1 ;; esac
  [ "$bytes" -le "$baseline_max_bytes" ] || return 1
  [ "$(tail -c 1 -- "$baseline" 2>/dev/null | od -An -t x1 | tr -d '[:space:]')" = 0a ] || return 1
  line_count="$(awk 'END { print NR + 0 }' "$baseline" 2>/dev/null || printf '0')"
  [ "$line_count" -eq 1 ] || return 1
  mapfile -t baseline_lines <"$baseline" || return 1
  [ "${#baseline_lines[@]}" -eq 1 ] || return 1
  baseline_head="${baseline_lines[0]}"
  [[ "$baseline_head" =~ ^[0-9a-f]{40,64}$ ]] || return 1
  eci_git_safe -C "$baseline_repo" cat-file -e "$baseline_head^{commit}" >/dev/null 2>&1
}

baseline_binding_file_is_valid() {
  local bytes line_count
  [ -f "$baseline_binding" ] && [ ! -L "$baseline_binding" ] || return 1
  bytes="$(wc -c <"$baseline_binding" 2>/dev/null || true)"
  case "$bytes" in ''|*[!0-9]*) return 1 ;; esac
  [ "$bytes" -le "$baseline_max_bytes" ] || return 1
  [ "$(tail -c 1 -- "$baseline_binding" 2>/dev/null | od -An -t x1 | tr -d '[:space:]')" = 0a ] || return 1
  line_count="$(awk 'END { print NR + 0 }' "$baseline_binding" 2>/dev/null || printf '0')"
  [ "$line_count" -eq 7 ] || return 1
  mapfile -t baseline_binding_lines <"$baseline_binding" || return 1
  [ "${#baseline_binding_lines[@]}" -eq 7 ] || return 1
  [ "${baseline_binding_lines[0]}" = 'schema: eci-baseline-binding/v1' ] || return 1
  [ "${baseline_binding_lines[1]}" = "session_id: $session_id" ] || return 1
  [ "${baseline_binding_lines[2]}" = "cwd: $baseline_cwd" ] || return 1
  [ "${baseline_binding_lines[3]}" = "repo_root: $baseline_repo" ] || return 1
  [ "${baseline_binding_lines[4]}" = "git_dir: $baseline_git_dir" ] || return 1
  [ "${baseline_binding_lines[5]}" = "git_common_dir: $baseline_git_common" ] || return 1
  baseline_head="${baseline_binding_lines[6]#base_oid: }"
  [ "${baseline_binding_lines[6]}" = "base_oid: $baseline_head" ] || return 1
  [[ "$baseline_head" =~ ^[0-9a-f]{40,64}$ ]] || return 1
  eci_git_safe -C "$baseline_repo" cat-file -e "$baseline_head^{commit}" >/dev/null 2>&1
}

publish_baseline_head() {
  local tmp="$baseline.tmp.$$"
  [ ! -e "$tmp" ] && [ ! -L "$tmp" ] || return 1
  (set -C; printf '%s\n' "$baseline_head" >"$tmp") || { rm -f -- "$tmp"; return 1; }
  if (set -C; ln -- "$tmp" "$baseline" 2>/dev/null); then
    rm -f -- "$tmp"
    return 0
  fi
  rm -f -- "$tmp"
  return 1
}

publish_baseline_binding() {
  local tmp="$baseline_binding.tmp.$$"
  [ ! -e "$tmp" ] && [ ! -L "$tmp" ] || return 1
  (set -C; printf 'schema: eci-baseline-binding/v1\nsession_id: %s\ncwd: %s\nrepo_root: %s\ngit_dir: %s\ngit_common_dir: %s\nbase_oid: %s\n' \
    "$session_id" "$baseline_cwd" "$baseline_repo" "$baseline_git_dir" "$baseline_git_common" "$baseline_head" >"$tmp") || {
    rm -f -- "$tmp"
    return 1
  }
  if (set -C; ln -- "$tmp" "$baseline_binding" 2>/dev/null); then
    rm -f -- "$tmp"
    return 0
  fi
  rm -f -- "$tmp"
  return 1
}

if [ "$aggregate_plan_present" != true ] && baseline_context_is_valid; then
  baseline_present=false
  binding_present=false
  [ -e "$baseline" ] || [ -L "$baseline" ] && baseline_present=true
  [ -e "$baseline_binding" ] || [ -L "$baseline_binding" ] && binding_present=true

  if [ "$baseline_present" = true ]; then
    baseline_head_file_is_valid || baseline_present=false
  fi
  if [ "$binding_present" = true ]; then
    baseline_binding_file_is_valid || binding_present=false
  fi

  # A valid lone file is the recoverable half-publication case. Preserve unsafe
  # or malformed partial state for refresh/recovery; it never blocks ordinary work.
  if [ "$baseline_present" = true ] && [ "$binding_present" = false ] &&
    [ ! -e "$baseline_binding" ] && [ ! -L "$baseline_binding" ]; then
    publish_baseline_binding || true
  elif [ "$baseline_present" = false ] && [ "$binding_present" = true ] &&
    [ ! -e "$baseline" ] && [ ! -L "$baseline" ]; then
    publish_baseline_head || true
  elif [ "$baseline_present" = false ] && [ "$binding_present" = false ] &&
    [ ! -e "$baseline" ] && [ ! -L "$baseline" ] &&
    [ ! -e "$baseline_binding" ] && [ ! -L "$baseline_binding" ]; then
    baseline_head="$(eci_git_safe -C "$baseline_repo" rev-parse HEAD 2>/dev/null || true)"
    if [[ "$baseline_head" =~ ^[0-9a-f]{40,64}$ ]] &&
      eci_git_safe -C "$baseline_repo" cat-file -e "$baseline_head^{commit}" >/dev/null 2>&1; then
      if publish_baseline_head; then
        [ "${ECI_SESSION_SNAPSHOT_FAIL_AFTER:-}" = baseline ] && exit 75
        if publish_baseline_binding; then
          [ "${ECI_SESSION_SNAPSHOT_FAIL_AFTER:-}" = binding ] && exit 75
        else
          rm -f -- "$baseline"
        fi
      fi
    fi
  fi
fi

rm -f "$proof_dir/skip_stop"

prune_marker_dirs() {
  local state_root="$1"
  local marker_name="$2"
  local dir marker

  [ -d "$state_root" ] || return 0
  find "$state_root" -mindepth 1 -maxdepth 1 -type d -mtime +30 -print 2>/dev/null |
    while IFS= read -r dir; do
      marker="$dir/$marker_name"
      if [ -f "$marker" ] && [ ! -L "$marker" ]; then
        case "$marker_name" in
          eci_active) continue ;;
          skip_stop)
            [ -n "$(find "$marker" -mmin -60 -print 2>/dev/null)" ] && continue
            ;;
        esac
      fi
      rm -rf "$dir"
    done
}

while IFS= read -r -d '' dir; do
  marker="$dir/eci_active"
  if [ -d "$dir" ] && [ ! -L "$dir" ] &&
      [ -f "$marker" ] && [ ! -L "$marker" ]; then
    continue
  fi
  rm -rf -- "$dir"
done < <(find "$root" -mindepth 1 -maxdepth 1 -type d -name '019*' -mtime +30 -print0 2>/dev/null)
find "$root/history" -mindepth 1 -maxdepth 1 -type f -mtime +30 -delete 2>/dev/null || true
for state_root in skills audit reviewer reviewer-dumps; do
  find "$root/$state_root" -mindepth 1 -maxdepth 1 -mtime +30 -exec rm -rf {} + 2>/dev/null || true
done
find "$root/touched-repos/sessions" -mindepth 1 -maxdepth 1 -type d -mtime +30 -exec rm -rf {} + 2>/dev/null || true
prune_marker_dirs "$root/eci/sessions" eci_active
prune_marker_dirs "$root/eci/cwd" eci_active
prune_marker_dirs "$root/skip-stop/sessions" skip_stop
prune_marker_dirs "$root/skip-stop/cwd" skip_stop
prune_marker_dirs "$root/side-stop/sessions" side_stop

ctx='Load ~/.omp/agent/AGENTS.md and matching ~/.omp/agent/skills when applicable.'
eci_marker="$proof_dir/eci_active"
if [ -d "$proof_dir" ] && [ ! -L "$proof_dir" ] &&
    { [ "$aggregate_active" = true ] || direct_marker_is_active || nested_marker_is_active; }; then
  ctx='ECI is active. ECI refresh signal (not proof of compaction): after compaction, the coordinator/lead must immediately re-read the entire skills/explore-critique-implement/SKILL.md and re-invoke it before the next decision/tool.'
fi

jq -n --arg ctx "$ctx" '{
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext: $ctx
  }
}'

# Shellcheck-friendly library: drop reviewer violations not grounded in rules.
# shellcheck shell=bash

reviewer_filter_migration_summary="$HOME/.omp/agent/eci/memories/migration-import/ACTIVE-SUMMARY.md"
reviewer_filter_legacy_summary="$HOME/.omp/agent/eci/memories/claude"'-import/ACTIVE-SUMMARY.md'

if [ -z "${REVIEWER_FILTER_CORPUS_FILES+x}" ]; then
  REVIEWER_FILTER_CORPUS_FILES="$HOME/.omp/agent/AGENTS.md $HOME/.omp/agent/eci/hooks/stop-checklist.md $reviewer_filter_migration_summary"
  if [ -f "$reviewer_filter_legacy_summary" ]; then
    REVIEWER_FILTER_CORPUS_FILES="$REVIEWER_FILTER_CORPUS_FILES $reviewer_filter_legacy_summary"
  fi
fi
: "${REVIEWER_FILTER_THRESHOLD:=0.15}"

reviewer_filter_lib_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"

filter_violations() {
  local result_json="$1"
  shift || true
  [ -n "$result_json" ] || { printf '%s' "$result_json"; return 0; }

  local static_corpus_text extra_text filtered
  static_corpus_text=$(
    # shellcheck disable=SC2086 # Space-separated corpus list is intentional.
    cat $REVIEWER_FILTER_CORPUS_FILES 2>/dev/null || true
  )
  extra_text=$(
    for corpus_file in "$@"; do
      if [ -n "$corpus_file" ]; then
        cat "$corpus_file" 2>/dev/null || true
      fi
    done
  )

  local filter_ok=0
  if . "$reviewer_filter_lib_dir/eci-native-binary.sh" 2>/dev/null && eci_runtime_open_verified eci-hook-helper; then
    filtered=$(printf '%s' "$result_json" | "/proc/self/fd/$ECI_RUNTIME_BINARY_FD" reviewer-filter "$static_corpus_text" "$extra_text" "$REVIEWER_FILTER_THRESHOLD" 2>/dev/null) && filter_ok=1
    exec {ECI_RUNTIME_BINARY_FD}<&-
  else
    printf '%s\n' "${ECI_RUNTIME_DENY_REASON:-ECI native gate binary eci-hook-helper is unavailable: lib/eci-native-binary.sh failed to load; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry}" >&2
  fi
  if [ "$filter_ok" = 1 ] && [ -n "$filtered" ]; then
    printf '%s' "$filtered"
  else
    # Fail closed: the filter could not verify any violation, so none are returned.
    # Verdict stays "fail", so the caller still blocks the stop.
    printf '%s\n' "ECI native gate binary eci-hook-helper reviewer-filter produced no result; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" >&2
    printf '%s' '{"verdict":"fail","violations":[]}'
  fi
}

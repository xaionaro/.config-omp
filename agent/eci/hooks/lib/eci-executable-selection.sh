#!/usr/bin/env bash
# Select one explicitly retained same-bin deployment; never rebuild on failure.
# TODO: document how .selected-executables is produced and changed: the v1 line
# format, that each key is an opaque 64-hex build key, the manual reselection
# steps, and the wrapper's 24-hour prune of unselected keys. The current pins carry
# fingerprint-era keys from the builder before f7503e3; they are not content digests
# and are not checked against the files. Nothing in the tree records how the current
# pin was built.
# bin/eci-active refuses maintain-planner until a coordinated cutover replaces
# the selected executable that predates workspace-safe planner builds.
# TODO: the coordinated cutover also activates the stuck/awaits renames that the
# status-report and eci-active sources already use: STUCK, stuck_work_risks
# ("Stuck work/Risks"), lane awaits, ledger state stuck, and stuck_change. Before
# reselection, migrate each live session's latest-status-report.yaml and
# project-understanding.yaml (and high_level_log.jsonl if the activated build
# reads it); right after reselecting (.selected-executables is untracked), commit
# the switch of the names still kept for the pinned executables in
# tools/status-report/README.md (and drop its names note), skills
# writing-status-reports, context-ledger, fast-path.md, coordinator.md and
# policy-pressure-tests.md, hooks/tests/test-status-report-launcher.sh and the
# eci-requirement-assignment-go integration fixtures; then rebuild the command
# planner. Site list and migration commands: ledger record
# work:stuck-awaits-terms:phase2 (session 01a1bbec).
eci_select_executable() {
  local name=$1 selection line extra version active report assignment leaf
  selection="$eci_home/bin/.selected-executables"
  if [ -L "$selection" ] || [ ! -f "$selection" ]; then
    printf '%s: selected deployment unavailable; source was not built\n' "$name" >&2
    return 1
  fi
  extra=
  if ! {
    IFS= read -r line && ! IFS= read -r extra && [ -z "$extra" ]
  } < "$selection"; then
    printf '%s: invalid selected deployment\n' "$name" >&2
    return 1
  fi
  read -r version active report assignment extra <<< "$line"
  if [ "$version" != v1 ] || [ -n "$extra" ] ||
    [[ ! $active =~ ^\.eci-active\.[0-9a-f]{64}$ ]] ||
    [[ ! $report =~ ^\.status-report\.[0-9a-f]{64}$ ]] ||
    [[ ! $assignment =~ ^\.eci-requirement-assignment\.[0-9a-f]{64}$ ]]; then
    printf '%s: invalid selected deployment\n' "$name" >&2
    return 1
  fi
  case "$name" in
    eci-active) leaf=$active ;;
    status-report) leaf=$report ;;
    eci-requirement-assignment) leaf=$assignment ;;
    *) printf 'unsupported selected tool: %s\n' "$name" >&2; return 1 ;;
  esac
  eci_go_tool_executable="$eci_home/bin/$leaf"
  if [ -L "$eci_go_tool_executable" ] || [ ! -f "$eci_go_tool_executable" ] || [ ! -x "$eci_go_tool_executable" ]; then
    printf '%s: selected executable unavailable; source was not built\n' "$name" >&2
    return 1
  fi
}

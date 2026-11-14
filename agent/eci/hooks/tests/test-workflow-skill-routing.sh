#!/usr/bin/env bash

set -euo pipefail
shopt -s globstar
# Checks workflow-skill structure that agents follow mechanically: Markdown links and anchors resolve, role rows route only to their allowed modules, and AGENTS.md imports the binding contract. Policy wording is reviewed, not pinned: prose pins broke on every rewording and spent minutes re-proving their own checks.

ROOT="${WORKFLOW_SKILL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)}"
AGENTS="$ROOT/../AGENTS.md"
ECI="$ROOT/../skills/explore-critique-implement/SKILL.md"
ECI_BINDING_CONTRACT="$ROOT/../eci-binding-contract.md"
ATE="$ROOT/../skills/agent-teams-execution/SKILL.md"
GATE_CATALOG="$ROOT/hooks/gate-escape-hatches.md"

fail() {
  printf 'workflow routing assertion failed: %s\n' "$*" >&2
  exit 1
}

require_line() {
  local file="$1" line="$2"
  grep -Fqx -- "$line" "$file" || fail "$file is missing line: $line"
}

# heading_anchors prints the GitHub anchor of each Markdown heading outside code fences; a repeated anchor gets -1, -2, and so on.
heading_anchors() {
  awk '
    /^[[:space:]]*(```|~~~)/ { fence = !fence; next }
    fence || !/^#+[[:space:]]/ { next }
    {
      anchor = tolower($0)
      sub(/^#+[[:space:]]+/, "", anchor)
      sub(/[[:space:]]+#+[[:space:]]*$/, "", anchor)
      gsub(/[^[:alnum:] _-]/, "", anchor)
      gsub(/ /, "-", anchor)
      count = seen[anchor]++
      print (count ? anchor "-" count : anchor)
    }
  ' "$1"
}

assert_local_links_resolve() {
  local file link path target fragment
  local -A anchors=()
  # Every Markdown file of these workflow skills, so a new module is checked without editing this list.
  local -a documents=(
    "$ROOT"/../skills/{explore-critique-implement,agent-teams-execution,context-ledger,debugging-discipline,writing-status-reports}/**/*.md
    "$ROOT"/../skills/references/**/*.md
    "$ECI_BINDING_CONTRACT"
    "$GATE_CATALOG"
  )

  for file in "${documents[@]}"; do
    [ -s "$file" ] || fail "missing or empty workflow document: $file"
    while IFS= read -r link; do
      link="${link#](}"
      case "$link" in
        http://*|https://*|mailto:*) continue ;;
      esac
      path="${link%%#*}"
      target="$file"
      if [ -n "$path" ]; then
        target="${file%/*}/$path"
        [ -e "$target" ] || fail "dangling link from $file: $link"
      fi
      [[ "$link" == *'#'* ]] || continue
      fragment="${link#*#}"
      [ -n "${anchors[$target]+set}" ] || anchors[$target]=$'\n'"$(heading_anchors "$target")"$'\n'
      [[ "${anchors[$target]}" == *$'\n'"$fragment"$'\n'* ]] || fail "dangling anchor from $file: $link"
    done < <(grep -oE '\]\([^)]+' "$file" || true)
  done
}

# A role row starts with its role cell; later cells, such as the spawn table's preset column, may name the same role.
role_rows() {
  local file="$1" marker="$2"
  marker="$marker" awk 'index($0, ENVIRON["marker"]) == 1' "$file"
}

# Control modules serve the Supervisor and Orchestrators; a row may route one only when its role contract allows it.
assert_no_control_routes() {
  local file="$1" marker="$2" allowed="${3:-}" row forbidden
  row="$(role_rows "$file" "$marker")"
  [[ -n "$row" && "$row" != *$'\n'* ]] || fail "$file must contain exactly one role row: $marker"
  for forbidden in \
    'references/coordinator.md' \
    'coordinator-runtime.md' \
    'references/orchestration.md' \
    'blocker-resolution-protocol' \
    'pause-all-work.md' \
    'stop-recovery.md' \
    'policy-pressure-tests.md' \
    'review-policy.md'; do
    [[ "$forbidden" == "$allowed" || "$row" != *"$forbidden"* ]] || fail "$marker routes to a control module outside its role contract: $forbidden"
  done
}

assert_role_rows_are_local() {
  local marker snitch_row eci_coordinator_row ate_coordinator_row critic_row
  for marker in \
    '| `explorer` |' \
    '| Producer (`implementer`) |' \
    '| Critic A/B/C, E2E |' \
    '| `fast-owner` (Fast Owner Producer) |'; do
    assert_no_control_routes "$ECI" "$marker"
  done
  assert_no_control_routes "$ECI" '| `critic-step2` |' 'review-policy.md'
  critic_row="$(role_rows "$ECI" '| `critic-step2` |')"
  [[ "$critic_row" == *'(../references/workflow-runtime/review-policy.md)'* ]] || fail 'ECI critic-step2 row must route review policy'
  for marker in \
    '| Snitch |' \
    '| Explorer/researcher |' \
    '| Designer, Design Reviewer, FDR |' \
    '| `Executor` |' \
    '| Execution Reviewer A/B/C |' \
    '| Test Designer/Executor/Reviewer, Verifier, QA |'; do
    assert_no_control_routes "$ATE" "$marker"
  done

  eci_coordinator_row="$(role_rows "$ECI" '| Supervisor |')"
  [[ "$eci_coordinator_row" == *'review-policy.md'* ]] ||
    fail 'ECI coordinator row must retain review-policy routing'
  ate_coordinator_row="$(role_rows "$ATE" '| Coordinator, Lead |')"
  [[ "$ate_coordinator_row" == *'review-policy.md'* ]] ||
    fail 'ATE coordinator row must retain review-policy routing'

  snitch_row="$(role_rows "$ATE" '| Snitch |')"
  [[ "$snitch_row" == *'[snitch](references/snitch.md)'* && "$snitch_row" != *'../'* ]] ||
    fail 'Snitch is not routed only to its local module'
}

assert_debugging_role_routes() {
  local row

  row="$(role_rows "$ECI" '| `explorer` |')"
  [[ "$row" == *'[debugging-discipline](../debugging-discipline/SKILL.md)'* ]] ||
    fail 'ECI explorer row must route to debugging-discipline'

  row="$(role_rows "$ECI" '| Producer (`implementer`) |')"
  [[ "$row" == *'[debugging-discipline](../debugging-discipline/SKILL.md)'* ]] ||
    fail 'ECI implementer row must route code/debug work to debugging-discipline'

  row="$(role_rows "$ATE" '| `Executor` |')"
  [[ "$row" == *'[debugging-discipline](../debugging-discipline/SKILL.md)'* ]] ||
    fail 'ATE Executor row must route debug work to debugging-discipline'
}

assert_binding_contract_import() {
  require_line "$AGENTS" '@~/.omp/agent/eci-binding-contract.md'
  [ -s "$ECI_BINDING_CONTRACT" ] || fail "missing binding contract: $ECI_BINDING_CONTRACT"
}

assert_local_links_resolve
assert_role_rows_are_local
assert_debugging_role_routes
assert_binding_contract_import
printf '%s\n' 'workflow skill routing assertions: PASS'

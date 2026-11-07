#!/usr/bin/env bash

# Exercise ECI's installed skill consumers; no model turn or Sentry request.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SKILL="$ROOT/skills/sentry-iterate/SKILL.md"
METADATA="$ROOT/skills/sentry-iterate/agents/openai.yaml"

fail() {
  printf 'sentry-iterate consumer check failed: %s\n' "$*" >&2
  exit 1
}

[[ -f "$SKILL" ]] || fail "missing skill: $SKILL"
[[ -f "$METADATA" ]] || fail 'missing invocation configuration'
# This skill intentionally has only the native invocation policy, with no UI fields.
[[ "$(<"$METADATA")" == $'policy:\n  allow_implicit_invocation: false' ]] ||
  fail 'expected the minimal explicit-only invocation policy'
command -v eci >/dev/null || fail 'eci is required'
command -v jq >/dev/null || fail 'jq is required'
cd "$ROOT"

# A nonempty ordinary catalog with an enabled control prevents vacuous absence.
eci debug prompt-input 'Summarize the local workspace guidance.' | jq -e '
  [.[] | select(.role == "developer") | .content[]?
    | select(.type == "input_text") | .text
    | select(contains("### Available skills"))] as $catalogs
  | ($catalogs | join("\n")) as $catalog
  | {developer_catalog_count: ($catalogs | length),
     sentry_implicit: ($catalog | test("(?m)^- sentry-iterate:")),
     eci_implicit: ($catalog | test("(?m)^- explore-critique-implement:"))}
  | select(.developer_catalog_count > 0 and .sentry_implicit == false and .eci_implicit == true)
' || fail 'ordinary prompt must omit sentry-iterate and retain ECI'

coproc SENTRY_SKILL_PROBE { exec eci app-server --stdio; }
probe_pid=$SENTRY_SKILL_PROBE_PID
probe_read=${SENTRY_SKILL_PROBE[0]}
probe_write=${SENTRY_SKILL_PROBE[1]}
trap 'kill "$probe_pid" 2>/dev/null || true; wait "$probe_pid" 2>/dev/null || true' EXIT

read_response() {
  local expected_id="$1" response deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    IFS= read -r -t "$((deadline - SECONDS))" response <&"$probe_read" || return 1
    if jq -e --argjson id "$expected_id" '.id == $id' <<<"$response" >/dev/null; then
      jq -e 'has("result") and (has("error") | not)' <<<"$response" >/dev/null || return 1
      printf '%s\n' "$response"
      return 0
    fi
  done
  return 1
}

printf '%s\n' '{"id":1,"method":"initialize","params":{"clientInfo":{"name":"sentry-skill-consumer-check","version":"1.0.0"}}}' >&"$probe_write"
read_response 1 >/dev/null || fail 'app-server initialization failed'
printf '%s\n' '{"method":"initialized"}' >&"$probe_write"
jq -nc --arg cwd "$ROOT" '{id:2,method:"skills/list",params:{cwds:[$cwd],forceReload:true}}' >&"$probe_write"
response=$(read_response 2) || fail 'app-server skill discovery failed'
jq -e '
  .result.data as $data
  | [$data[].skills[] | select(.name == "sentry-iterate")] as $skills
  | {cwd_count: ($data | length), errors: [$data[].errors[]],
     sentry: ($skills | map({name,path,enabled}))}
  | select(.cwd_count == 1 and .errors == [] and (.sentry | length) == 1
      and .sentry[0].enabled == true)
' <<<"$response" || fail 'explicit-only skill must remain enabled and discoverable'
discovered_path=$(jq -r '.result.data[].skills[] | select(.name == "sentry-iterate") | .path' <<<"$response")
[[ "$discovered_path" -ef "$SKILL" ]] || fail 'discovered skill does not resolve to the intended file'

printf 'sentry-iterate native consumer checks: PASS\n'

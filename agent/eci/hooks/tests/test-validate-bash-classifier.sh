#!/usr/bin/env bash

set -Eeuo pipefail
trap 'status=$?; printf "classifier failure: line=%s status=%s command=%q\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Pass this spelling to the hook literally.  It is deliberately not $ROOT:
# lifecycle authority belongs only to the current HOME-derived ECI root.
eci_lifecycle='$HOME/.omp/agent/eci/bin/eci-active'
classifier_tmp_parent="$(realpath -m -- "${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}")"
mkdir -p -- "$classifier_tmp_parent"
TMP_ROOT="$(mktemp -d "$classifier_tmp_parent/eci-classifier-${BASHPID}.XXXXXX")"
classifier_hook_fixture="$(mktemp "$ROOT/hooks/.validate-bash-classifier.${BASHPID}.XXXXXX")"
trap 'rm -f -- "$classifier_hook_fixture"' EXIT
cp -- "$ROOT/hooks/validate-bash.sh" "$classifier_hook_fixture"
sed -i '2{/^exit 0$/d;}' -- "$classifier_hook_fixture"
cmp -- "$classifier_hook_fixture" <(sed '2{/^exit 0$/d;}' -- "$ROOT/hooks/validate-bash.sh") || {
  printf '%s\n' 'classifier fixture changed bytes other than an optional line-2 bypass' >&2
  exit 1
}
# Exercise the current-session temporary-root alias explicitly.  The alias is
# a direct child named tmp of an otherwise isolated home and resolves to the
# canonical non-system parent used by this test.  It must not admit siblings
# of that canonical root.
tmpdir_alias_home="$TMP_ROOT/tmpdir-alias-home"
mkdir -p -- "$tmpdir_alias_home"
ln -s -- "$classifier_tmp_parent" "$tmpdir_alias_home/tmp"
session_temp_sibling="$classifier_tmp_parent/eci-unbound-session-${BASHPID}"
session_temp_deeper="$TMP_ROOT/eci-classifier-deeper-${BASHPID}"
session_temp_link="$classifier_tmp_parent/eci-session-link-${BASHPID}"
mkdir -p -- "$session_temp_sibling" "$session_temp_deeper"
chmod 755 -- "$session_temp_sibling" "$session_temp_deeper"
ln -s -- "$TMP_ROOT" "$session_temp_link"
# Cross-provider assertions must not trust a concurrently edited peer worktree.
# Clone its committed HEAD into the test sandbox instead.  The peer lifecycle
# recognizer intentionally binds Kimi to $HOME/.kimi-code, so keep that
# temporary parent as kimi_home for peer-only hook invocations below.
kimi_source_root="${KIMI_CODE_HOME:-${HOME:-}/.kimi-code}"
kimi_root="$kimi_source_root"
kimi_home="${HOME:-}"
if [[ "$kimi_source_root" = /* ]] && [ -d "$kimi_source_root" ] &&
  [ ! -L "$kimi_source_root" ] &&
  [ "$(realpath -m -- "$kimi_source_root")" = "$kimi_source_root" ] &&
  kimi_source_head="$(git -C "$kimi_source_root" rev-parse --verify HEAD^{commit} 2>/dev/null)"; then
  kimi_home="$TMP_ROOT/kimi-home"
  kimi_root="$kimi_home/.kimi-code"
  mkdir -p -- "$kimi_home"
  git clone --quiet --no-checkout --no-local --no-tags "$kimi_source_root" "$kimi_root"
  git -C "$kimi_root" checkout --quiet --detach "$kimi_source_head"
  [ "$(git -C "$kimi_root" rev-parse HEAD)" = "$kimi_source_head" ]
  [ -z "$(git -C "$kimi_root" status --porcelain)" ]

  # The committed snapshot has no runtime receipt.  Add a test-local receipt
  # so the altered-peer assertion below proves lifecycle byte binding rather
  # than merely a non-canonical path denial.
  kimi_active_digest="$(sha256sum -- "$kimi_root/bin/eci-active" | awk '{print $1}')"
  kimi_active_mode="$(stat -c '%a' -- "$kimi_root/bin/eci-active")"
  printf 'bin/eci-active\t%s\t%s\n' "$kimi_active_digest" "$kimi_active_mode" >"$kimi_root/.eci-runtime-sync-manifest"
  chmod 600 -- "$kimi_root/.eci-runtime-sync-manifest"
  printf '/.eci-runtime-sync-manifest\n' >>"$kimi_root/.git/info/exclude"
  [ -z "$(git -C "$kimi_root" status --porcelain)" ]
fi
subagent_transcript=""
export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$XDG_CONFIG_HOME/eci"
chmod 700 "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/eci"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"

proof_root="$TMP_ROOT/proof"
mkdir -p "$proof_root/t00-session"
printf '%s\n' \
  'scope: classifier test' \
  "cwd: $ROOT" \
  'session_id: t00-session' \
  'created_utc: 2026-08-14T00:00:00Z' \
  >"$proof_root/t00-session/eci_active"
high_level_log="$proof_root/t00-session/high_level_log.jsonl"
printf '%s\n' '{"event":"decision","summary":"baseline"}' >"$high_level_log"
evidence_dir="$proof_root/t00-session/evidence"
evidence_file="$evidence_dir/inspection.txt"
instructions_file="$proof_root/t00-session/instructions.md"
mkdir -p "$evidence_dir"
printf '%s\n' 'proof evidence' >"$evidence_file"
printf '%s\n' '# coordinator instructions' >"$instructions_file"
printf '%s\n' 'outside proof root' >"$TMP_ROOT/outside-proof.txt"
ln -s -- "$TMP_ROOT/outside-proof.txt" "$evidence_dir/outside-link"
log_bytes="$(wc -c <"$high_level_log")"
log_sha256="$(sha256sum -- "$high_level_log" | awk '{print $1}')"
printf '%s\n' \
  'schema: eci-high-level-log-anchor/v1' \
  'session_id: t00-session' \
  "log_path: $high_level_log" \
  "bytes: $log_bytes" \
  "sha256: $log_sha256" \
  >"$proof_root/t00-session/high_level_log.anchor"

ledger_append_from_marker_cwd() {
  local ledger_root="$1" ledger_session="$2" ledger_entry="$3"

  (
    cd "$ROOT"
    ECI_PROOF_ROOT="$ledger_root" ECI_SESSION_ID="$ledger_session" ECI_HOME="$ROOT" \
      "$ROOT/bin/eci-active" ledger-append --json "$(jq -cn --arg summary "$ledger_entry" '{event:"decision",summary:$summary}')"
  )
}

# The lifecycle command must bind an append to the marker's declared CWD;
# selecting the proof root and session alone must not authorize another CWD.
wrong_cwd_log_sha256="$(sha256sum -- "$high_level_log" | awk '{print $1}')"
if (
  cd "$TMP_ROOT"
  ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID=t00-session ECI_HOME="$ROOT" \
    "$ROOT/bin/eci-active" ledger-append --json '{"event":"decision","summary":"wrong-cwd probe"}'
) >"$TMP_ROOT/wrong-cwd-ledger.out" 2>"$TMP_ROOT/wrong-cwd-ledger.err"; then
  printf '%s\n' 'ledger append unexpectedly accepted a marker from another cwd' >&2
  exit 1
fi
grep -Fq 'ECI high-level log append rejected a marker bound to another session or cwd.' "$TMP_ROOT/wrong-cwd-ledger.err"
[ "$(sha256sum -- "$high_level_log" | awk '{print $1}')" = "$wrong_cwd_log_sha256" ]

# The runtime route validates the same marker/cwd binding and advances the
# bounded prefix anchor under the mutation lock.  Entries are written to the
# HOME context root, not the proof root: tools/eci-active ledger.go
# appendLedgerEntry resolves contextSessionDir (proof.go), the same ledger
# location (pre-migration source 575bb81 moved ledgers to the context root).  The proof-root
# $high_level_log above remains a control-path fixture only.
context_ledger="$HOME/.agent/context/sessions/t00-session/high_level_log.jsonl"
ledger_append_from_marker_cwd "$proof_root" t00-session 'coordinator entry' >/dev/null
first_timestamp_line="$(tail -n 1 -- "$context_ledger")"
jq -e '.schema == "eci-high-level-log/v1" and .event == "decision" and .summary == "coordinator entry" and (.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))' <<<"$first_timestamp_line" >/dev/null
ledger_append_from_marker_cwd "$proof_root" t00-session 'second timestamp entry' >/dev/null
second_timestamp_line="$(tail -n 1 -- "$context_ledger")"
jq -e '.schema == "eci-high-level-log/v1" and .event == "decision" and .summary == "second timestamp entry" and (.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))' <<<"$second_timestamp_line" >/dev/null
first_timestamp="$(jq -r '.timestamp' <<<"$first_timestamp_line")"
second_timestamp="$(jq -r '.timestamp' <<<"$second_timestamp_line")"
[[ "$first_timestamp" < "$second_timestamp" || "$first_timestamp" = "$second_timestamp" ]]

subagent_home="$TMP_ROOT/subagent-home"
subagent_eci_home="$subagent_home/.omp/agent/eci"
external_skill_root="$TMP_ROOT/external/skills/escape"
mkdir -p "$subagent_eci_home/bin" "$subagent_eci_home/sessions" \
  "$subagent_eci_home/skills/test" "$external_skill_root"
cp -- "$ROOT/bin/eci-active" "$subagent_eci_home/bin/eci-active"
chmod +x "$subagent_eci_home/bin/eci-active"
printf '%s\n' '# worker ECI instructions' >"$subagent_eci_home/AGENTS.md"
printf '%s\n' '# worker agent instructions' >"$subagent_eci_home/AGENTS.md"
printf '%s\n' '# worker test skill' >"$subagent_eci_home/skills/test/SKILL.md"
mkfifo "$subagent_eci_home/skills/test/not-a-source.fifo"
printf '%s\n' '# outside skill' >"$external_skill_root/SKILL.md"
ln -s -- "$external_skill_root" "$subagent_eci_home/skills/escape"

run_hook() {
  local command="$1" output
  output="${2:-$TMP_ROOT/output}"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_hook_with_canonical_tmpdir() {
  local command="$1" output
  output="$TMP_ROOT/output-with-canonical-tmpdir"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    TMPDIR="$classifier_tmp_parent" ECI_TMPDIR="$classifier_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_hook_with_tmpdir_home_alias() {
  local command="$1" output
  output="$TMP_ROOT/output-with-tmpdir-home-alias"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    HOME="$tmpdir_alias_home" TMPDIR="$tmpdir_alias_home/tmp" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

assert_planner_allows() {
  local command="$1" planner_output
  planner_output="$(
    jq -cn --arg cwd "$ROOT" --arg command "$command" \
      '{provider:"omp",role:"coordinator",cwd:$cwd,marker:"active",active_session:"t00-session",command:$command,active_markers:[],approved_roots:[$cwd]}' |
      "$ROOT/hooks/lib/eci-command-plan-go/eci-command-plan"
  )" || {
    printf 'planner rejected expected finite shell argv: %q output=%s\n' \
      "$command" "$planner_output" >&2
    return 1
  }
  jq -e '.decision == "allow" and (.diagnostic | not)' <<<"$planner_output" >/dev/null || {
    printf 'planner allow assertion mismatch: command=%q output=%s\n' \
      "$command" "$planner_output" >&2
    return 1
  }
}

# The coordinator trace diagnostic is a typed planner defer, not a generic
# shell pipeline admission. Keep the asserted topology byte-for-byte bound to
# the literal callback command so the adapter can select its reviewed-script
# route without reparsing arbitrary redirections.
assert_planner_reviewed_trace_deferred() {
  local command="$1" planner_output planner_status

  if planner_output="$(
    jq -cn --arg cwd "$ROOT" --arg command "$command" \
      '{provider:"omp",role:"coordinator",cwd:$cwd,marker:"active",active_session:"t00-session",command:$command,active_markers:[],approved_roots:[$cwd]}' |
      "$ROOT/hooks/lib/eci-command-plan-go/eci-command-plan"
  )"; then
    planner_status=0
  else
    planner_status=$?
  fi
  [ "$planner_status" -eq 3 ] || {
    printf 'planner did not defer exact reviewed trace: command=%q status=%s output=%s\n' \
      "$command" "$planner_status" "$planner_output" >&2
    return 1
  }
  jq -e --arg command "$command" '
    def exact_keys($expected): (keys | sort) == $expected;
    type == "object" and
    (.decision == "defer") and
    (.deferred_route == "reviewed-script-trace") and
    (.diagnostic == null) and
    ((.capabilities // []) | type == "array" and length == 0) and
    (.plan | type == "object" and exact_keys(["trace"])) and
    (.plan.trace | type == "object" and
      exact_keys(["command", "lines", "operator", "redirect", "script", "shell", "shell_flag", "sink", "sink_flag"]) and
      .command == $command and
      (.shell == "bash" or .shell == "sh") and
      .shell_flag == "-x" and
      .redirect == "2>&1" and
      .operator == "|" and
      .sink == "tail" and
      .sink_flag == "-n" and
      (.lines | type == "string" and test("^(?:[1-9]|[1-9][0-9]|1[0-9]{2}|200)$")) and
      .command == (.shell + " " + .shell_flag + " " + .script + " " + .redirect + " " + .operator + " " + .sink + " " + .sink_flag + " " + .lines)
    )
  ' <<<"$planner_output" >/dev/null || {
    printf 'planner trace topology mismatch: command=%q output=%s\n' "$command" "$planner_output" >&2
    return 1
  }
}

# A copied HOME-selected runtime keeps this test independent from the live
# classifier. Stale classifier bytes are diagnostic state, and ordinary
# same-target maintenance must not depend on callback marker/session/CWD
# metadata that cannot identify a different executable target.
copied_home_fixture_index=0
prepare_stale_copied_home() {
  copied_home_fixture_index=$((copied_home_fixture_index + 1))
  COPIED_HOME="$TMP_ROOT/copied-home-$copied_home_fixture_index"
  COPIED_ROOT="$COPIED_HOME/.omp/agent/eci"
  COPIED_PROOF_ROOT="$TMP_ROOT/copied-proof-$copied_home_fixture_index"
  COPIED_SESSION="callback-session-$copied_home_fixture_index"
  COPIED_OUT="$TMP_ROOT/copied-output-$copied_home_fixture_index.json"
  COPIED_OTHER_CWD="$COPIED_HOME/other-cwd"

  mkdir -p -- "$COPIED_HOME" "$COPIED_ROOT" "$COPIED_PROOF_ROOT" "$COPIED_OTHER_CWD" \
    "$COPIED_HOME/.kimi-code/bin" "$COPIED_HOME/xdg-config/eci" "$COPIED_HOME/xdg-state"
  # An absent transcript resolves to the worker role (hook_context.go
  # hookTranscriptRole; fail-closed by e1ce6f5).  These lifecycle cases are
  # coordinator-owned, so the callback names a depth-two main transcript under
  # the copied HOME's sessions root.
  COPIED_TRANSCRIPT="$COPIED_HOME/.omp/agent/sessions/-copied-project-/main.jsonl"
  mkdir -p -- "$(dirname -- "$COPIED_TRANSCRIPT")"
  printf '%s\n' '{"type":"session","version":3,"id":"copied-home-main","cwd":"/work"}' \
    '{"type":"message","message":{"role":"user","content":"hello"}}' >"$COPIED_TRANSCRIPT"
  cp -a -- "$ROOT"/. "$COPIED_ROOT"/
  sed -i '2{/^exit 0$/d;}' -- "$COPIED_ROOT/hooks/validate-bash.sh"
  cmp -- "$COPIED_ROOT/hooks/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$ROOT/hooks/validate-bash.sh") || {
    printf '%s\n' 'copied-home fixture changed bytes other than an optional line-2 bypass' >&2
    return 1
  }
  printf '\n// stale copied-home classifier fixture\n' >>"$COPIED_ROOT/hooks/lib/eci-command-plan-go/classifier.go"
  cp -- "$COPIED_ROOT/bin/eci-active" "$COPIED_HOME/.kimi-code/bin/eci-active"
  chmod 755 -- "$COPIED_HOME/.kimi-code/bin/eci-active"
  printf '%s\n' enforcing >"$COPIED_HOME/xdg-config/eci/command-gate-mode"
}

copied_home_lifecycle_command() {
  local session_assignment="$1" verb="$2"

  printf 'env %s ECI_ROLE=coordinator "$HOME/.omp/agent/eci/bin/eci-active" %s' \
    "$session_assignment" "$verb"
}

activate_copied_home_marker() {
  mkdir -p -- "$COPIED_PROOF_ROOT/$COPIED_SESSION"
  printf 'scope: copied-home lifecycle fixture\ncwd: %s\nsession_id: %s\n' \
    "$COPIED_ROOT" "$COPIED_SESSION" >"$COPIED_PROOF_ROOT/$COPIED_SESSION/eci_active"
}

run_copied_home_hook() {
  local command="$1" callback_session="${2:-$COPIED_SESSION}" callback_cwd="${3:-$COPIED_ROOT}" output="${4:-$COPIED_OUT}"

  jq -cn --arg cwd "$callback_cwd" --arg command "$command" --arg session "$callback_session" \
    --arg transcript "$COPIED_TRANSCRIPT" \
    '{session_id:$session,tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    (
      cd "$COPIED_ROOT"
      HOME="$COPIED_HOME" ECI_HOME="$COPIED_ROOT" ECI_PROOF_ROOT="$COPIED_PROOF_ROOT" \
        ECI_PROOF_ROOT_CONFIGURED="$COPIED_PROOF_ROOT" ECI_PROOF_ROOT_CANONICAL="$COPIED_PROOF_ROOT" \
        ECI_PROOF_ROOT_STABLE_ALIAS="$COPIED_PROOF_ROOT" XDG_CONFIG_HOME="$COPIED_HOME/xdg-config" \
        XDG_STATE_HOME="$COPIED_HOME/xdg-state" KIMI_CODE_HOME="$COPIED_HOME/.kimi-code" \
        PATH="$COPIED_ROOT/bin:$PATH" bash "$COPIED_ROOT/hooks/validate-bash.sh"
    ) >"$output"
  printf '%s\n' "$output"
}

assert_copied_home_admitted() {
  local command="$1" callback_session="$2" callback_cwd="$3" output

  output="$(run_copied_home_hook "$command" "$callback_session" "$callback_cwd")"
  [ ! -s "$output" ] || {
    printf 'copied-home same-target lifecycle command was unexpectedly denied: command=%q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

run_hook_without_marker() {
  local command="$1" output
  output="$TMP_ROOT/output-without-marker"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-no-marker",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$TMP_ROOT/no-marker-proof" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

# Stale planner/provenance data cannot turn ordinary current-HOME lifecycle
# maintenance into a callback-metadata ceremony. The first case has no marker
# and a mismatched command session. With a valid active marker, both
# session-independent maintenance verbs remain bound to the same resolved
# executable even when either provider's session assignment is stale. The
# final case has a marker for another callback CWD/session and an invalid
# command session. A real Kimi copy remains a different concrete mutating
# target and must still be denied before normal fallback.
prepare_stale_copied_home
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command ECI_SESSION_ID=wrong-session maintain-planner)" \
  "$COPIED_SESSION" "$COPIED_ROOT"
activate_copied_home_marker
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command ECI_SESSION_ID=wrong-session maintain-planner)" \
  "$COPIED_SESSION" "$COPIED_ROOT"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command ECI_SESSION_ID=wrong-session sync-runtime)" \
  "$COPIED_SESSION" "$COPIED_ROOT"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command KIMI_SESSION_ID=wrong-session maintain-planner)" \
  "$COPIED_SESSION" "$COPIED_ROOT"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command KIMI_SESSION_ID=wrong-session sync-runtime)" \
  "$COPIED_SESSION" "$COPIED_ROOT"
# The hook identifies the resolved maintenance verb, while eci-active owns
# its own trailing-argument validation. Stale provider metadata must not turn
# either ordinary CLI usage error into an identity denial.
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command ECI_SESSION_ID=wrong-session 'maintain-planner extra')" \
  "$COPIED_SESSION" "$COPIED_ROOT"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command ECI_SESSION_ID=wrong-session 'sync-runtime extra')" \
  "$COPIED_SESSION" "$COPIED_ROOT"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command KIMI_SESSION_ID=wrong-session 'maintain-planner extra')" \
  "$COPIED_SESSION" "$COPIED_ROOT"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command KIMI_SESSION_ID=wrong-session 'sync-runtime extra')" \
  "$COPIED_SESSION" "$COPIED_ROOT"
# Retired: session-targeting and foreign-target lifecycle denials expected
# hook-side ECI_LIFECYCLE_IDENTITY_DENIED and ECI_LIFECYCLE_TARGET_DENIED. Neither
# code is produced at HEAD or by the pre-migration source 575bb81, and lifecycle
# verbs are retired (ECI_LIFECYCLE_RETIRED route in tools/eci-active).
mkdir -p -- "$COPIED_PROOF_ROOT/foreign-session"
printf 'scope: foreign copied-home marker\ncwd: %s\nsession_id: foreign-session\n' \
  "$COPIED_ROOT" >"$COPIED_PROOF_ROOT/foreign-session/eci_active"
assert_copied_home_admitted \
  "$(copied_home_lifecycle_command ECI_SESSION_ID=invalid/session sync-runtime)" \
  "$COPIED_SESSION" "$COPIED_OTHER_CWD"

if [ "${VALIDATE_BASH_CLASSIFIER_COPIED_HOME_LIFECYCLE_ONLY:-false}" = true ]; then
  printf '%s\n' 'validate-bash classifier copied-home lifecycle: PASS'
  exit 0
fi

# Command-gate permissive mode is telemetry for inactive callbacks only. An
# active ECI marker must preserve each targeted denial locally rather than
# passing it to `eci-command-gate-mode finalize`, which would otherwise turn
# the callback into an allow. An opaque shell shape that does not identify a
# concrete harmful target is hook-transparent; exercise the targeted legacy
# denial and that transparent fallback, as well as a missing structural test
# target. A missing ordinary script path is likewise transparent: the shell is
# responsible for reporting that normal execution failure, not this gate.
printf '%s\n' permissive >"$XDG_CONFIG_HOME/eci/command-gate-mode"
active_permissive_gate_log="$XDG_STATE_HOME/eci/command-gate/would-deny.jsonl"
[ ! -e "$active_permissive_gate_log" ]
active_permissive_case=0
assert_active_permissive_denied_without_finalize() {
  local command="$1" code="$2" operation="$3" output
  active_permissive_case=$((active_permissive_case + 1))
  output="$TMP_ROOT/active-permissive-${active_permissive_case}.json"
  run_hook "$command" "$output" >/dev/null
  jq -e --arg code "$code" --arg operation "$operation" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("operation=" + $operation)))
  ' "$output" >/dev/null
  [ ! -e "$active_permissive_gate_log" ] || {
    printf 'active permissive denial unexpectedly invoked eci-command-gate-mode finalize: command=%q\n' "$command" >&2
    return 1
  }
}

# An active marker does not make a coordinator behavioral instruction into a
# PreToolUse denial. It remains transparent and must not reach permissive-mode
# telemetry; concrete harmful effects are covered by their targeted denials.
active_permissive_push_output="$TMP_ROOT/active-permissive-push.json"
run_hook 'git push' "$active_permissive_push_output" >/dev/null
[ ! -s "$active_permissive_push_output" ] || {
  printf 'active ordinary git push was unexpectedly denied: %s\n' \
    "$(cat -- "$active_permissive_push_output")" >&2
  exit 1
}
[ ! -e "$active_permissive_gate_log" ] || {
  printf '%s\n' 'active ordinary git push unexpectedly invoked eci-command-gate-mode finalize' >&2
  exit 1
}
# Unknown syntax alone is not an accidental-mistake signal.  The command gate
# must not turn it into an allowlist denial merely because the parser cannot
# classify both ordinary components.
active_permissive_opaque_output="$TMP_ROOT/active-permissive-opaque.json"
run_hook $'cat /dev/null\ntouch unexpected' "$active_permissive_opaque_output" >/dev/null
[ ! -s "$active_permissive_opaque_output" ] || {
  printf 'opaque ordinary shell shape was unexpectedly denied: %s\n' \
    "$(cat -- "$active_permissive_opaque_output")" >&2
  exit 1
}
[ ! -e "$active_permissive_gate_log" ] || {
  printf 'opaque ordinary shell shape unexpectedly invoked eci-command-gate-mode finalize\n' >&2
  exit 1
}
active_permissive_missing_structural_output="$TMP_ROOT/active-permissive-missing-structural-output"
run_hook 'bash hooks/tests/not-present.sh' "$active_permissive_missing_structural_output" >/dev/null
[ ! -s "$active_permissive_missing_structural_output" ] || {
  printf 'missing ordinary script path was unexpectedly denied:\n' >&2
  cat -- "$active_permissive_missing_structural_output" >&2
  exit 1
}
[ ! -e "$active_permissive_gate_log" ] || {
  printf '%s\n' 'active missing ordinary script path unexpectedly invoked eci-command-gate-mode finalize' >&2
  exit 1
}

inactive_permissive_output="$(run_hook_without_marker 'git push')"
[ ! -s "$inactive_permissive_output" ]
[ ! -e "$active_permissive_gate_log" ] || {
  printf '%s\n' 'inactive ordinary git push unexpectedly invoked eci-command-gate-mode finalize' >&2
  exit 1
}
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"

# Retired: the deferred generic fixture expected a capability-free status-3 defer
# from a fake planner under a copied hook tree, bound by a provenance receipt
# (pre-migration source 575bb81: validate-bash.sh:8, bin/eci-runtime-sync:54,71).
# HEAD opens the planner only as $HOME/.omp/agent/eci/bin/eci-command-plan through
# eci_runtime_open_verified (validate-bash.sh:66), which verifies it against the
# .sha256 receipt that hooks/build-native-binaries.sh writes
# (hooks/lib/eci-native-binary.sh:122-132, :164-167). The fixture plants no such
# binary, so the hook fails closed with ECI_HOOK_NATIVE_GATE_UNAVAILABLE ("is
# missing or a symlink") before the defer path. The provenance receipt is written
# by the planner refresh (tools/eci-active/planner.go:254, :270) and has no
# production reader at HEAD.

run_hook_with_kimi_home() {
  local command="$1" companion_root="$2" companion_home output
  companion_home="$(dirname -- "$companion_root")"
  output="${3:-$TMP_ROOT/output-with-kimi-home}"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    HOME="$companion_home" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$companion_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_peer_hook() {
  run_hook_with_kimi_home "$1" "$kimi_root"
}

run_altered_peer_hook() {
  run_hook_with_kimi_home "$1" "$altered_kimi_root"
}

run_hook_without_kimi_home() {
  local command="$1" output
  output="$TMP_ROOT/output-without-kimi-home"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    env -u KIMI_CODE_HOME HOME="$kimi_home" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_hook_with_transcript() {
  local command="$1" output transcript
  output="$TMP_ROOT/output-with-transcript"
  transcript="$TMP_ROOT/main-transcript.jsonl"
  printf '%s\n' '{"timestamp":"2026-08-18T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session"}}' >"$transcript"
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_hook_with_transcript_pager() {
  local command="$1" output transcript
  output="$TMP_ROOT/output-with-transcript-pager"
  transcript="$TMP_ROOT/main-transcript-pager.jsonl"
  printf '%s\n' '{"timestamp":"2026-08-18T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session"}}' >"$transcript"
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" GIT_PAGER=cat PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_subagent_hook() {
  local command="$1" transcript_order="${2:-type-first}" output transcript
  output="${3:-$TMP_ROOT/subagent-output}"
  transcript="${4:-$subagent_eci_home/sessions/eci-validate-bash-subagent-$BASHPID.jsonl}"
  subagent_transcript="$transcript"
  case "$transcript_order" in
    type-first)
      printf '%s\n' '{"timestamp":"2026-08-15T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}' >"$transcript"
      ;;
    payload-first)
      printf '%s\n' '{"timestamp":"2026-08-15T00:00:00.000Z","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}},"type":"session_meta"}' >"$transcript"
      ;;
    payload-before-and-after-type)
      printf '%s\n' '{"timestamp":"2026-08-15T00:00:00.000Z","payload":{"id":"benign"},"type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}' >"$transcript"
      ;;
    *)
      printf 'unknown subagent transcript order: %s\n' "$transcript_order" >&2
      return 1
      ;;
  esac
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" HOME="$subagent_home" ECI_HOME="$subagent_eci_home" KIMI_CODE_HOME="$kimi_root" \
      PATH="$subagent_eci_home/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

# Run independent worker command matrices concurrently while retaining one
# real validator process and one assertion per case.  The ordinary shell,
# interpreter, and named-runtime matrices below are read-only policy probes;
# they do not share mutable proof state.  Give each case private output and
# transcript paths so parallelism cannot turn fixture reuse into a race.
run_subagent_matrix_parallel() {
  local expectation="$1" matrix_name="$2"; shift 2
  local matrix_root="$TMP_ROOT/parallel-$matrix_name-$BASHPID"
  local command output transcript result failed=0 index=0
  local -a pids=() commands=() results=()
  mkdir -p -- "$matrix_root"
  for command in "$@"; do
    output="$matrix_root/output-$index.json"
    transcript="$subagent_eci_home/sessions/eci-validate-bash-matrix-$matrix_name-$BASHPID-$index.jsonl"
    result="$matrix_root/result-$index.txt"
    commands[index]="$command"
    results[index]="$result"
    (
      run_subagent_hook "$command" type-first "$output" "$transcript" >/dev/null
      case "$expectation" in
        allowed)
          [ ! -s "$output" ] || {
            printf 'ordinary finite worker argv was denied: %s\n' "$command" >&2
            cat -- "$output" >&2
            exit 1
          }
          ;;
        denied)
          jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
            printf 'worker command was unexpectedly allowed: %s\n' "$command" >&2
            cat -- "$output" >&2
            exit 1
          }
          ;;
        dynamic-interpreter)
          jq -e '
            .hookSpecificOutput.permissionDecision == "deny" and
            (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
            (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-interpreter-launch"))
          ' "$output" >/dev/null || {
            printf 'named runtime was unexpectedly allowed: %s\n' "$command" >&2
            cat -- "$output" >&2
            exit 1
          }
          ;;
        *)
          printf 'unknown parallel matrix expectation: %s\n' "$expectation" >&2
          exit 1
          ;;
      esac
    ) >"$result" 2>&1 &
    pids[index]=$!
    index=$((index + 1))
  done
  for index in "${!pids[@]}"; do
    if ! wait "${pids[index]}"; then
      failed=1
      printf 'parallel worker matrix failed: matrix=%s command=%q\n' \
        "$matrix_name" "${commands[index]}" >&2
      cat -- "${results[index]}" >&2
    fi
  done
  return "$failed"
}

run_subagent_transcript_matrix_parallel() {
  local command="$1" matrix_name="$2"; shift 2
  local matrix_root="$TMP_ROOT/parallel-$matrix_name-$BASHPID"
  local order output transcript result failed=0 index=0
  local -a pids=() orders=() results=()
  mkdir -p -- "$matrix_root"
  for order in "$@"; do
    output="$matrix_root/output-$index.json"
    transcript="$subagent_eci_home/sessions/eci-validate-bash-matrix-$matrix_name-$BASHPID-$index.jsonl"
    result="$matrix_root/result-$index.txt"
    orders[index]="$order"
    results[index]="$result"
    (
      run_subagent_hook "$command" "$order" "$output" "$transcript" >/dev/null
      jq -e '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_COORDINATOR_ROUTE_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("phase=PreToolUse")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=coordinator-route")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("subject=provider=eci,role=worker,marker=active,command=wrapper=literal")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("rejected command=wrapper=literal")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("coordinator-only temporary-directory setup")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
      ' "$output" >/dev/null
    ) >"$result" 2>&1 &
    pids[index]=$!
    index=$((index + 1))
  done
  for index in "${!pids[@]}"; do
    if ! wait "${pids[index]}"; then
      failed=1
      printf 'parallel transcript matrix failed: matrix=%s order=%s\n' "$matrix_name" "${orders[index]}" >&2
      cat -- "${results[index]}" >&2
    fi
  done
  return "$failed"
}

# The coordinator-side literal matrices are also independent read-only
# admission probes.  Keep one fresh hook process and one exact assertion per
# command, but avoid serial startup overhead and give every case private
# output.  This helper deliberately has no production-policy shortcut.
run_hook_matrix_parallel() {
  local expectation="$1" matrix_name="$2"; shift 2
  local matrix_root="$TMP_ROOT/parallel-$matrix_name-$BASHPID"
  local command output result failed=0 index=0
  local -a pids=() commands=() results=()
  mkdir -p -- "$matrix_root"
  for command in "$@"; do
    output="$matrix_root/output-$index.json"
    result="$matrix_root/result-$index.txt"
    commands[index]="$command"
    results[index]="$result"
    (
      run_hook "$command" "$output" >/dev/null
      case "$expectation" in
        allowed)
          [ ! -s "$output" ] || {
            printf 'ordinary coordinator argv was denied: %s\n' "$command" >&2
            cat -- "$output" >&2
            exit 1
          }
          ;;
        unknown)
          jq -e '
            .hookSpecificOutput.permissionDecision == "deny" and
            (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_") and contains("phase=") and contains("operation=") and contains("reason:") and contains("remediation:"))
          ' "$output" >/dev/null || {
            printf 'protected coordinator argv was unexpectedly allowed: %s\n' "$command" >&2
            cat -- "$output" >&2
            exit 1
          }
          ;;
        *)
          printf 'unknown coordinator parallel matrix expectation: %s\n' "$expectation" >&2
          exit 1
          ;;
      esac
    ) >"$result" 2>&1 &
    pids[index]=$!
    index=$((index + 1))
  done
  for index in "${!pids[@]}"; do
    if ! wait "${pids[index]}"; then
      failed=1
      printf 'parallel coordinator matrix failed: matrix=%s command=%q\n' \
        "$matrix_name" "${commands[index]}" >&2
      cat -- "${results[index]}" >&2
    fi
  done
  return "$failed"
}

# General callback form for matrices whose assertion is more specific than a
# simple allow/deny check.  The callback receives the command and a runner
# which returns this case's private output path, so existing exact-diagnostic
# assertions can be reused without duplicating their jq predicates.
matrix_output_runner() {
  printf '%s\n' "${MATRIX_OUTPUT:?}"
}

run_matrix_parallel() {
  local role="$1" validator="$2" matrix_name="$3"; shift 3
  local matrix_root="$TMP_ROOT/parallel-$matrix_name-$BASHPID"
  local command output transcript result failed=0 index=0
  local -a pids=() commands=() results=()
  mkdir -p -- "$matrix_root"
  for command in "$@"; do
    output="$matrix_root/output-$index.json"
    transcript="$subagent_eci_home/sessions/eci-validate-bash-matrix-$matrix_name-$BASHPID-$index.jsonl"
    result="$matrix_root/result-$index.txt"
    commands[index]="$command"
    results[index]="$result"
    (
      case "$role" in
        coordinator) run_hook "$command" "$output" >/dev/null ;;
        worker) run_subagent_hook "$command" type-first "$output" "$transcript" >/dev/null ;;
        peer) run_hook_with_kimi_home "$command" "$kimi_root" "$output" >/dev/null ;;
        *) printf 'unknown parallel matrix role: %s\n' "$role" >&2; exit 1 ;;
      esac
      MATRIX_OUTPUT="$output" "$validator" "$command" matrix_output_runner
    ) >"$result" 2>&1 &
    pids[index]=$!
    index=$((index + 1))
  done
  for index in "${!pids[@]}"; do
    if ! wait "${pids[index]}"; then
      failed=1
      printf 'parallel callback matrix failed: matrix=%s command=%q\n' \
        "$matrix_name" "${commands[index]}" >&2
      cat -- "${results[index]}" >&2
    fi
  done
  return "$failed"
}

run_subagent_hook_at_root() {
  local command="$1" alternate_root="$2" alternate_home="$3" output transcript
  output="$TMP_ROOT/subagent-output-at-root"
  transcript="$subagent_eci_home/sessions/eci-validate-bash-subagent-$BASHPID-at-root.jsonl"
  subagent_transcript="$transcript"
  printf '%s\n' '{"timestamp":"2026-08-15T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}' >"$transcript"
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$alternate_root" HOME="$alternate_home" ECI_HOME="$subagent_eci_home" \
      ECI_HOOK_IS_SUBAGENT=true ECI_ROLE=worker PATH="$subagent_eci_home/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output"
  printf '%s\n' "$output"
}

run_role_hook() {
  local role="$1" command="$2"
  case "$role" in
    coordinator) run_hook "$command" ;;
    worker) run_subagent_hook "$command" ;;
    *) printf 'unknown test role: %s\n' "$role" >&2; return 1 ;;
  esac
}

assert_role_environment_denied() {
  local role="$1" command="$2" code="$3" token="$4" argv_index="$5"
  local forbidden_value="${6:-}" reason_fragment="${7:-}" expected_segment="${8:-1}" runner="${9:-}" output
  if [ -n "$runner" ]; then
    output="$("$runner" "$command")"
  else
    output="$(run_role_hook "$role" "$command")"
  fi
  jq -e --arg code "[$code]" --arg token "$token" --arg argv_index "$argv_index" --arg expected_segment "$expected_segment" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("phase=PreToolUse")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=environment-boundary")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("segment=" + $expected_segment))) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("token=" + $token))) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("argv_index=" + $argv_index))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'environment denial mismatch: role=%s command=%q\n' "$role" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
  if [ -n "$forbidden_value" ] && grep -F -- "$forbidden_value" "$output" >/dev/null; then
    printf 'environment context diagnostic leaked assignment value: role=%s command=%q\n' "$role" "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
  if [ -n "$reason_fragment" ] && ! grep -F -- "$reason_fragment" "$output" >/dev/null; then
    printf 'environment diagnostic omitted reason detail: role=%s command=%q detail=%s\n' \
      "$role" "$command" "$reason_fragment" >&2
    cat -- "$output" >&2
    return 1
  fi
}

assert_environment_case() {
  local role="$1" command="$2" runner="${3:-}" code="" token="" index="" forbidden="" reason="" segment=""
  case "$command" in
    "env"|"env | sort"|"env | sort | rg '^PATH='"|"env FOO=bar")
      code="ECI_ENVIRONMENT_ENUMERATION_DENIED"; token="${command%% *}"; index=0 ;;
    "printenv")
      code="ECI_ENVIRONMENT_ENUMERATION_DENIED"; token="printenv"; index=0 ;;
    "printenv PATH PATH")
      code="ECI_ENVIRONMENT_ENUMERATION_DENIED"; token="PATH"; index=2; reason="duplicated" ;;
    "printenv PATH=bad")
      code="ECI_ENVIRONMENT_ENUMERATION_DENIED"; token="PATH=bad"; index=1; reason="identifier" ;;
    "printenv -- PATH")
      code="ECI_ENVIRONMENT_OPTION_DENIED"; token="--"; index=1; reason="unsupported" ;;
    "env -S 'novel-tool'")
      code="ECI_ENVIRONMENT_OPTION_DENIED"; token="-S"; index=1; reason="split-string" ;;
    "env -u")
      code="ECI_ENVIRONMENT_OPTION_DENIED"; token="-u"; index=1; reason="missing its required argument" ;;
    "env --unknown novel-tool")
      code="ECI_ENVIRONMENT_OPTION_DENIED"; token="--unknown"; index=1; reason="unsupported" ;;
    "env --unset= novel-tool")
      code="ECI_ENVIRONMENT_OPTION_DENIED"; token="--unset="; index=1; reason="not a valid identifier" ;;
    "printenv PATH | env")
      code="ECI_ENVIRONMENT_ENUMERATION_DENIED"; token="env"; index=0; segment=2 ;;
    "env BASH_ENV=eci-private-bash-value bash script.sh")
      code="ECI_ENVIRONMENT_CONTEXT_DENIED"; token="BASH_ENV"; index=1; forbidden="eci-private-bash-value" ;;
    "env GIT_DIR=eci-private-git-value git status")
      code="ECI_ENVIRONMENT_CONTEXT_DENIED"; token="GIT_DIR"; index=1; forbidden="eci-private-git-value" ;;
    *)
      printf 'unknown environment fixture: role=%s command=%q\n' "$role" "$command" >&2
      return 1
      ;;
  esac
  assert_role_environment_denied "$role" "$command" "$code" "$token" "$index" "$forbidden" "$reason" "${segment:-1}" "$runner"
}

assert_environment_broad_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("path=/")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=broad-destructive"))
  ' "$output" >/dev/null || {
    printf 'environment broad denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_initial_case() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  case "$command" in
    "adb devices -l"|"touch hooks/generated-worker-source"|"xargs novel-worker-tool")
      # An unfamiliar wrapper is not itself an accidental effect; concrete
      # destructive, control, and dynamic-interpreter cases are tested below.
      [ ! -s "$output" ] || {
        printf 'ordinary worker command was denied: %q\n' "$command" >&2
        cat -- "$output" >&2
        return 1
      }
      ;;
    "rm -rf /")
      jq -e '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=broad-destructive")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("class=broad executable=rm token=/ target=/ kind=recursive-root-delete")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
      ' "$output" >/dev/null
      ;;
    *)
      printf 'unknown initial worker fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
}

assert_worker_environment_case() {
  assert_environment_case worker "$@"
}

assert_coordinator_git_environment_context_denied() {
  local command="$1" output
  output="$(run_hook "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_GIT_EXECUTION_CONTEXT_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=git-execution-context")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=GIT_DIR"))
  ' "$output" >/dev/null || {
    printf 'coordinator Git environment-context denial mismatch: command=%q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_allowed() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  [ ! -s "$output" ] || {
    cat "$output" >&2
    return 1
  }
}

assert_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'assert_denied failed: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_shell_expansion_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_SYNTAX_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=shell-expansion")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'shell expansion denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_any_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'expected denial: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_subagent_worker_launcher_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_LAUNCHER_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-launcher"))
  ' "$output" >/dev/null
}

assert_script_batch_denied_by_manifest() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("reviewed digest manifest"))
  ' "$output" >/dev/null || {
    printf 'assert_script_batch_denied_by_manifest failed: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_unknown() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_") and contains("phase=") and contains("operation=") and contains("reason:") and contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'assert_unknown failed: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_git_whole_worktree_staging_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e --arg repository "$ROOT" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=git-mutation")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("effect=whole-worktree-staging")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("target=" + $repository))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'Git whole-worktree staging denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_coordinator_protected_git_target_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$($runner "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_EDIT_ROUTING_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=edit-routing")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-protected-target")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("target=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'Coordinator protected Git target denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_compound_mutation_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COMPOUND_MUTATION_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=compound-mutation")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=compound-mutation")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'compound mutation denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_stat_format_allowed() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  [ ! -s "$output" ] || {
    printf 'stat metadata read unexpectedly produced a user-facing ECI denial: command=%q output=%s\n' \
      "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_stat_format_case() {
  local command="$1" runner="${2:-run_hook}"
  case "$command" in
    *"%Q"*|*'$(printf %s)'*|*"--printf="*) ;;
    *)
      printf 'unknown stat-format fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
  assert_stat_format_allowed "$command" "$runner"
}

# A non-capable archive form must reach the legacy Git parser rather than the
# planner fast path. The legacy result itself may be allow or deny; a Python
# process is the observable route boundary while strace is available.
assert_archive_legacy_git_route() {
  local name command path_value git_dir trace output
  name="$1"
  command="$2"
  path_value="${3:-$ROOT/bin:$PATH}"
  git_dir="${4:-}"

  command -v strace >/dev/null 2>&1 || return 0

  trace="$TMP_ROOT/archive-legacy-$name.trace"
  output="$TMP_ROOT/archive-legacy-$name.output"
  if [ -n "$git_dir" ]; then
    jq -cn --arg cwd "$ROOT" --arg command "$command" \
      '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
      GIT_DIR="$git_dir" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$path_value" \
        strace -f -qq -e trace=process -o "$trace" \
          bash "$classifier_hook_fixture" >"$output"
  else
    jq -cn --arg cwd "$ROOT" --arg command "$command" \
      '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
      ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$path_value" \
        strace -f -qq -e trace=process -o "$trace" \
          bash "$classifier_hook_fixture" >"$output"
  fi
  grep -Eq 'execve\(".*/python3"' "$trace" || {
    printf 'archive command unexpectedly bypassed the legacy Git route: %q\n' "$command" >&2
    return 1
  }
}

run_archive_legacy_matrix_parallel() {
  local name command path git_dir result failed=0 index=0
  local -a pids=() names=() results=()
  while [ "$#" -gt 0 ]; do
    name="$1"; command="$2"; path="$3"; git_dir="$4"
    shift 4
    result="$TMP_ROOT/archive-legacy-$name.result"
    names[index]="$name"
    results[index]="$result"
    (
      if [ "$git_dir" = "-" ]; then
        assert_archive_legacy_git_route "$name" "$command" "$path"
      else
        assert_archive_legacy_git_route "$name" "$command" "$path" "$git_dir"
      fi
    ) >"$result" 2>&1 &
    pids[index]=$!
    index=$((index + 1))
  done
  for index in "${!pids[@]}"; do
    if ! wait "${pids[index]}"; then
      failed=1
      printf 'parallel archive route failed: case=%s\n' "${names[index]}" >&2
      cat -- "${results[index]}" >&2
    fi
  done
  return "$failed"
}

zero_marker_commit_output="$(run_hook_without_marker "git commit -m 'inactive boundary'")"
[ ! -s "$zero_marker_commit_output" ] || {
  cat -- "$zero_marker_commit_output" >&2
  exit 1
}

# Inline payload spelling is not by itself an accidental wrong-target signal
# for a coordinator. The hook leaves it transparent; the coordinator's normal
# delegation behavior decides whether an implementer should perform any edit.
launcher_output="$(run_hook "bash -c 'printf launcher'")"
[ ! -s "$launcher_output" ] || {
  printf '%s\n' 'ordinary coordinator inline payload was unexpectedly denied' >&2
  cat -- "$launcher_output" >&2
  exit 1
}
# The one coordinator diagnostic that carries stderr through a sink remains a
# typed structural route. The exact four bounds prove both
# trusted shell names and both inclusive line limits; ordinary shell parsing
# must never turn a redirect/pipeline into a generic admission.
for reviewed_trace in \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 200" \
  "sh -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "sh -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 200"; do
  assert_planner_reviewed_trace_deferred "$reviewed_trace"
done
run_hook_matrix_parallel allowed coordinator-self-trace \
  "bash hooks/tests/test-validate-bash-classifier.sh" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh" \
  "bash -x -n hooks/tests/test-validate-bash-classifier.sh" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 200" \
  "sh -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "sh -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 200" \
  "bash -x hooks/tests/test-eci-command-plan.sh 2>&1 | tail -n 1"
# Canonical structural traces remain admitted even when the test was not in a
# former reviewed-byte digest list. The command gate does not reject an
# ordinary test merely because its trace/pipeline spelling is outside a former
# typed topology; no concrete destructive or control target is present.
run_hook_matrix_parallel allowed coordinator-self-trace-ordinary-shapes \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 0" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 201" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh | tail -n 1" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>/dev/null | tail -n 1" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | head -n 1" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail --lines 1" \
  "bash -x -n hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "bash -xn hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "env ECI_TRACE=1 bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "command bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1" \
  "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1 ; true"

# A typed reviewed-script route can offer richer routing metadata, but it is
# not a denial boundary. This ordinary test pipeline has no concrete harmful
# target, so a missing trace decoration remains hook-transparent.
compound_script_output="$(run_hook "bash -x hooks/tests/test-validate-bash-classifier.sh | tail -n 1")"
[ ! -s "$compound_script_output" ] || {
  printf 'ordinary test pipeline was unexpectedly denied:\n' >&2
  cat -- "$compound_script_output" >&2
  exit 1
}
direct_script_output="$(run_hook "bash -x hooks/tests/test-validate-bash-classifier.sh")"
[ ! -s "$direct_script_output" ]
reviewed_trace_output="$(run_hook "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1")"
[ ! -s "$reviewed_trace_output" ]

# Syntax-only parsing is a default read-only check, including for executable
# runtime scripts whose canonical names intentionally have no .sh suffix.
assert_allowed "bash -n bin/eci-active"
assert_allowed "sh -n bin/eci-active"
assert_allowed "bash -n bin/eci-active" run_subagent_hook
assert_allowed "sh -n bin/eci-active" run_subagent_hook
# Multiple ordinary syntax-check targets are not a control violation. The
# shell owns their argument semantics; do not manufacture a shape denial.
assert_allowed "bash -n bin/eci-active hooks/validate-bash.sh"
assert_allowed "bash -n hooks/validate-bash.sh hooks/tests/test-validate-bash-classifier.sh hooks/tests/test-eci-repository-allow.sh && git diff --check"

missing_instructions="$proof_root/t00-session/instructions.md"
missing_output="$(run_hook "cat $missing_instructions")"
[ ! -s "$missing_output" ]
worker_missing_output="$(run_subagent_hook "find -P $proof_root/t00-session/missing -maxdepth 1 -print")"
[ ! -s "$worker_missing_output" ] || {
  printf 'generic missing path was unexpectedly denied:\n' >&2
  cat -- "$worker_missing_output" >&2
  exit 1
}

assert_worker_protected_hook_mode_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-control")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("class=source executable=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("path=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("resolved=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("kind=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=hook-mode-repair")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'worker protected hook-mode mutation was not denied: %q\n' "$command" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_inactive_no_hook_mode_repair() {
  local command="$1" output
  output="$(run_hook_without_marker "$command")"
  if grep -Fq -- 'predicate=hook-mode-repair' "$output"; then
    printf 'inactive command unexpectedly selected hook-mode-repair: %q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
}

assert_worker_no_hook_mode_repair() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  if grep -Fq -- 'predicate=hook-mode-repair' "$output"; then
    printf 'worker command unexpectedly selected hook-mode-repair: %q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
}

assert_ledger_append_only_denied() {
  local command="$1" path="$2" runner="${3:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e --arg path "$path" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LEDGER_APPEND_ONLY]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=ledger-append-only")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("token=" + $path))) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("path=" + $path))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=append-only-ledger")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation: use \"$HOME/.omp/agent/eci/bin/eci-active\" ledger-append --json")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation: use eci-active ledger-append --json") | not)
  ' "$output" >/dev/null || {
    printf 'assert_ledger_append_only_denied failed: command=%q path=%q output=%s\n' "$command" "$path" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

# `run_matrix_parallel` supplies a command plus an output-path runner. Keep
# the ledger assertion's explicit path contract intact and derive its literal
# target only in this narrow matrix adapter.
assert_ledger_append_only_denied_matrix() {
  local command="$1" runner="${2:-run_hook}" path
  path="${command##* }"
  [ -n "$path" ] || {
    printf 'ledger matrix command has no literal target: %q\n' "$command" >&2
    return 1
  }
  assert_ledger_append_only_denied "$command" "$path" "$runner"
}

assert_ledger_redirect_denied() {
  local command="$1" code="$2" detail="$3" runner="${4:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e --arg code "[$code]" --arg detail "$detail" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains($detail))
  ' "$output" >/dev/null || {
    printf 'ledger redirect denial mismatch: command=%q code=%s detail=%s\n' "$command" "$code" "$detail" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_lifecycle_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e --arg command "$command" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_ARGUMENTS_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("phase=PreToolUse")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-lifecycle")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("provider arguments")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("canonical ECI/Kimi eci-active control binary")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null
}

assert_eci_lifecycle_spelling_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_CANONICAL_PATH_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-lifecycle")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=canonical-lifecycle-spelling")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'ECI lifecycle spelling was not denied: %q\n' "$command" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_lifecycle_target_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_TARGET_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-lifecycle")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason: ECI lifecycle mutation has a different concrete executable target")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'Lifecycle concrete target was not denied: %q\n' "$command" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_lifecycle_identity_denied() {
  local command="$1" expected_name="$2" observed_name="$3" runner="${4:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e --arg expected_name "$expected_name" --arg observed_name "$observed_name" '
    .hookSpecificOutput.permissionDecision == "deny" and
    ((.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_LIFECYCLE_IDENTITY_DENIED]")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_IDENTITY_DENIED]"))) and
    (.hookSpecificOutput.permissionDecisionReason | contains($expected_name)) and
    (.hookSpecificOutput.permissionDecisionReason | contains($observed_name)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null
}

assert_lifecycle_identity_case() {
  local command="$1" runner="${2:-run_hook}" expected_name="${3:-ECI_SESSION_ID}"
  case "$command" in
    *"KIMI_SESSION_ID=t00-session"*)
      assert_lifecycle_identity_denied "$command" "$expected_name" KIMI_SESSION_ID "$runner"
      ;;
    *"ECI_SESSION_ID=t00-session"*)
      assert_lifecycle_identity_denied "$command" "$expected_name" ECI_SESSION_ID "$runner"
      ;;
    *"ECI_SESSION_ID=wrong-session"*)
      assert_lifecycle_identity_denied "$command" "$expected_name" wrong-session "$runner"
      ;;
    *"KIMI_SESSION_ID=wrong-session"*)
      assert_lifecycle_identity_denied "$command" "$expected_name" wrong-session "$runner"
      ;;
    *)
      printf 'unknown lifecycle identity fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
}

assert_eci_lifecycle_identity_case() {
  assert_lifecycle_identity_case "$1" "${2:-run_hook}" ECI_SESSION_ID
}

assert_kimi_lifecycle_identity_case() {
  assert_lifecycle_identity_case "$1" "${2:-run_hook}" KIMI_SESSION_ID
}

# A transcriptless active coordinator callback for ordinary test execution is
# allowed by the planner contract. It must not acquire a synthetic denial or a
# strace-based execution restriction merely because no transcript was supplied.
no_transcript_output="$TMP_ROOT/no-transcript-ordinary.output"
jq -cn --arg cwd "$ROOT" --arg command 'go test ./...' \
  '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
  env -u ECI_HOOK_IS_SUBAGENT -u ECI_ROLE \
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
    bash "$classifier_hook_fixture" >"$no_transcript_output"
[ ! -s "$no_transcript_output" ] || {
  printf 'transcriptless ordinary generic route was unexpectedly denied:\n' >&2
  cat -- "$no_transcript_output" >&2
  exit 1
}

# Explicit repository inspection and lifecycle routes remain covered separately
# from ordinary literal execution; only concrete control or ownership effects
# take a separate route while ECI is active.
run_hook_matrix_parallel allowed coordinator-explicit-inspection \
  "git -C $ROOT status --short" \
  "git -C $ROOT status --branch" \
  "git -C $ROOT status --short --branch" \
  "git -C $ROOT status --short --branch -- hooks/validate-bash.sh" \
  "git -C $ROOT status --short hooks/validate-bash.sh" \
  "git -C $ROOT status --short -- hooks/validate-bash.sh" \
  "git -C $ROOT diff --stat hooks/validate-bash.sh" \
  "git -C $ROOT diff --stat -- hooks/validate-bash.sh" \
  "git -C $ROOT diff --cached --stat" \
  "git -C $ROOT diff --staged --stat" \
  "git -C $ROOT diff -- AGENTS.md" \
  "git -C $ROOT diff AGENTS.md" \
  "git -C $ROOT log -1 --oneline hooks/validate-bash.sh" \
  "git -C $ROOT log -5 --oneline hooks/validate-bash.sh" \
  "git -C $ROOT log -1 -- AGENTS.md" \
  "date -u +%Y-%m-%dT%H:%M:%SZ" \
  "date --utc +%Y-%m-%dT%H:%M:%SZ" \
  "git -C $ROOT show --stat -1 hooks/validate-bash.sh" \
  "git -C $ROOT show -- AGENTS.md" \
  "$eci_lifecycle --help" \
  "\"$eci_lifecycle\" --help"
# Read-only lifecycle status/help is ordinary discovery regardless of command
# spelling. Control mutations and identity mismatches remain covered below.
run_hook_matrix_parallel allowed coordinator-lifecycle-spelling \
  "eci-active --help" \
  "~/.omp/agent/eci/bin/eci-active status" \
  "~/.omp/agent/eci/bin/eci-active --help" \
  "$ROOT/bin/eci-active --help" \
  "\$ECI_HOME/bin/eci-active --help"
run_hook_matrix_parallel allowed coordinator-general-unowned \
  "unrecognized-command" \
  "date -u +%s"
run_hook_matrix_parallel allowed coordinator-general-pipeline \
  "git diff -- hooks/validate-bash.sh | sed -n '1,260p'" \
  "printf source | tee hooks/generated-coordinator-pipeline" \
  "printf source && touch hooks/generated-coordinator-compound" \
  "rm -f hooks/generated-coordinator-cleanup"
run_hook_matrix_parallel allowed coordinator-home-spelling \
  'printf "%s\\n" "$HOME"' \
  'cat "$HOME/.omp/agent/eci/AGENTS.md"'
assert_allowed "ECI_SESSION_ID=t00-session $eci_lifecycle status"
run_hook_matrix_parallel allowed coordinator-lifecycle-env \
  "env ECI_SESSION_ID=t00-session $eci_lifecycle status" \
  "env KIMI_SESSION_ID=t00-session $eci_lifecycle status" \
  "env ECI_SESSION_ID=t00-session $eci_lifecycle ledger-append --json '{\"event\":\"decision\",\"summary\":\"bounded coordinator entry\"}'"
system_tmp="$(printf '/%s' tmp)"
system_tmp_lifecycle_output="$(run_hook "env TMPDIR=$system_tmp ECI_SESSION_ID=t00-session $eci_lifecycle --help")"
[ ! -s "$system_tmp_lifecycle_output" ] || {
  printf 'read-only lifecycle help was unexpectedly denied for TMPDIR=/tmp:\n' >&2
  cat -- "$system_tmp_lifecycle_output" >&2
  exit 1
}
run_matrix_parallel coordinator assert_eci_lifecycle_identity_case lifecycle-identity \
  "env ECI_SESSION_ID=wrong-session $eci_lifecycle ledger-append --json '{\"event\":\"decision\",\"summary\":\"mismatch probe\"}'"

# ECI ownership admission is ecosystem-neutral: after reserved ownership and
# shell-indirection checks, finite direct argv vectors for ordinary build/test
# tools are admitted without maintaining an executable allowlist.
run_hook_matrix_parallel allowed ordinary-coordinator-literal \
  "go test ./..." \
  "cargo test --workspace" \
  "pytest -q tests" \
  "python3 -m pytest tests" \
  "interpreter-tool --module test-suite --flag value" \
  "python3 tools/check.py --mode strict" \
  "node scripts/check.mjs" \
  "npm test -- --runInBand" \
  "make -B test" \
  "cmake --build build --target test" \
  "ctest --test-dir build --output-on-failure" \
  "ninja -C build test" \
  "meson test -C build" \
  "mvn -q test" \
  "./gradlew test" \
  "novel-tool --flag value" \
  "./tools/repo-check --context" \
  "rg -n -C 3 'needle' hooks"

# Opaque shell/interpreter spelling is not a source-write or target boundary.
# Keep it hook-transparent unless a later recognizer sees a concrete wrong
# target. These are intentionally not an executable allowlist.
run_hook_matrix_parallel allowed ordinary-coordinator-opaque \
  "eval 'go test ./...'" \
  "bash scripts/test.sh" \
  "python3 -c 'print(1)'" \
  "python3 -" \
  "node -e 'console.log(1)'" \
  "interpreter-tool -c 'dynamic payload'" \
  "env -S python3 -m pytest tests" \
  "go test \$(printf ./...)" \
  $'go test ./...\nprintf done'

# A coordinator routes checkpoints to the producer; broad destructive reset
# remains denied by its effect.
run_hook_matrix_parallel unknown coordinator-git-commit \
  "git commit -m 'checkpoint'"
run_hook_matrix_parallel unknown protected-coordinator-git-mutation \
  "git reset --hard"

if [[ "$kimi_root" = /* ]] && [ -d "$kimi_root" ] && [ ! -L "$kimi_root" ] &&
  [ -f "$kimi_root/bin/eci-active" ] && [ ! -L "$kimi_root/bin/eci-active" ]; then
  # Visibility-only probes do not alter provider state, so a ECI hook does
  # not reject a Kimi-root executable merely because it is a peer spelling.
  run_matrix_parallel peer assert_allowed kimi-peer-read-only \
    "$kimi_root/bin/eci-active status" \
    "$kimi_root/bin/eci-active --help" \
    "env KIMI_SESSION_ID=t00-session $kimi_root/bin/eci-active status" \
    "env ECI_SESSION_ID=t00-session $kimi_root/bin/eci-active status"
  # Mutating or identity-sensitive peer-provider lifecycle operations still
  # target a different concrete executable and remain denied.
  run_matrix_parallel peer assert_lifecycle_target_denied kimi-peer-denied \
    "$kimi_root/bin/eci-active on 'peer coordinator scope'" \
    "$kimi_root/bin/eci-active off ${HOME:?}/tmp/eci-peer-disengage.md"
  run_matrix_parallel peer assert_lifecycle_target_denied kimi-peer-identity \
    "env KIMI_SESSION_ID=wrong-session $kimi_root/bin/eci-active ledger-append --json '{\"event\":\"decision\",\"summary\":\"mismatch probe\"}'"
  run_matrix_parallel peer assert_lifecycle_target_denied kimi-peer-lifecycle \
    "$kimi_root/bin/eci-active on peer-scope extra"
  # Worker ownership checks apply to lifecycle mutations, not visibility-only
  # status/help probes that have no control-plane effect.
  run_subagent_matrix_parallel allowed kimi-peer-worker-route \
    "$kimi_root/bin/eci-active status"
  altered_kimi_home="$TMP_ROOT/altered-kimi-home"
  altered_kimi_root="$altered_kimi_home/.kimi-code"
  mkdir -p -- "$altered_kimi_home"
  cp -a -- "$kimi_root" "$altered_kimi_root"
  printf '\n' >>"$altered_kimi_root/bin/eci-active"
  altered_peer_output="$(run_hook_with_kimi_home "$altered_kimi_root/bin/eci-active status" "$altered_kimi_root")"
  # Read-only visibility remains transparent even when the peer executable
  # differs from its recorded bytes; the command has no control-plane effect.
  [ ! -s "$altered_peer_output" ]
  # A mutation still resolves to the altered peer executable and is denied by
  # concrete target, independently of the read-only digest mismatch.
  assert_lifecycle_target_denied \
    "$altered_kimi_root/bin/eci-active off $TMP_ROOT/altered-peer-disengage.md" \
    run_altered_peer_hook
  if [ -f "$altered_kimi_root/hooks/tests/test-block-no-progress.sh" ]; then
    printf '\n' >>"$altered_kimi_root/hooks/tests/test-block-no-progress.sh"
    altered_script_output="$(run_altered_peer_hook "bash $altered_kimi_root/hooks/tests/test-block-no-progress.sh")"
    # Ordinary script execution does not require a matching peer digest or
    # receipt; only concrete control and ownership effects are gated.
    [ ! -s "$altered_script_output" ]
  fi
fi
run_hook_matrix_parallel allowed coordinator-process-inspection \
  "ps -o pid,etime,stat,cmd" \
  "ps -o pid,cmd" \
  "ps -o pid,etime,stat,cmd | head -n 5"
run_matrix_parallel worker assert_worker_initial_case worker-initial-boundaries \
  "adb devices -l" \
  "touch hooks/generated-worker-source" \
  "xargs novel-worker-tool" \
  "rm -rf /"
run_hook_matrix_parallel allowed coordinator-initial-source-write \
  "touch hooks/generated-coordinator-source"
run_hook_matrix_parallel allowed coordinator-initial-temp-write \
  "touch ${HOME:?}/tmp/eci-finite-literal-probe"
run_subagent_matrix_parallel allowed ordinary-worker-shell \
  "python3 -m pytest tests" \
  "python3 tools/check.py" \
  "python3 tools/check.py -c" \
  "node scripts/check.mjs" \
  "nodejs scripts/check.mjs" \
  "perl tools/check.pl" \
  "ruby tools/check.rb" \
  "php tools/check.php" \
  "php -f tools/check.php" \
  "php -F tools/check.php" \
  "interpreter-tool --module test-suite --flag value" \
  "python-tool --module test-suite --flag value" \
  "./tools/eci-review-gate.sh verify" \
  "novel-worker-tool --flag value"
# Inline/stdin interpreter selectors and runtime selectors without a concrete
# target are not an accidental-effect boundary by spelling alone. The compiled
# planner may report that it cannot classify their payload, but the shell
# adapter keeps that parser uncertainty transparent; concrete target-aware
# routes below still own actual control/destructive effects.
run_subagent_matrix_parallel allowed ordinary-worker-interpreter \
  "bash -e scripts/test.sh" \
  "bash -x scripts/test.sh" \
  "bash -O extglob scripts/test.sh" \
  "bash --noprofile scripts/test.sh" \
  "sh -e scripts/test.sh" \
  "python3 -" \
  "interpreter-tool -c 'dynamic payload'"
run_subagent_matrix_parallel allowed ordinary-worker-runtime \
  "python3" \
  "python3 -cprint" \
  "python3.11 -cprint" \
  "python3 -W ignore" \
  "python3 -X dev" \
  "python3 -i tools/check.py" \
  "node" \
  "nodejs --eval=code" \
  "node -p code" \
  "node --loader loader.mjs" \
  "perl" \
  "perl5 -Ecode" \
  "perl -I ${HOME:?}/tmp" \
  "ruby" \
  "ruby3.3 -ecode" \
  "ruby -I lib" \
  "php" \
  "php8.2 -r=code" \
  "php -recho" \
  "php -Becho" \
  "php -Recho" \
  "php -Eecho" \
  "php --process-end=code" \
  "php -d memory_limit=1G" \
  "php -a"
worker_help_output="$(run_subagent_hook "eci-active --help")"
[ ! -s "$worker_help_output" ]
worker_ps_output="$(run_subagent_hook "ps -o pid,etime,stat,cmd")"
[ ! -s "$worker_ps_output" ]
run_hook_matrix_parallel allowed coordinator-ordinary-source-write \
  "touch hooks/generated-coordinator-source-again" \
  "chmod 644 hooks/generated-coordinator-source-again" \
  "sed -i '1s/^/generated /' hooks/generated-coordinator-source-again"
chmod_worker_output="$(run_subagent_hook "chmod 755 hooks/pre-commit-go-mod.sh")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-control")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("class=source executable=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=hooks/pre-commit-go-mod.sh")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("path=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("resolved=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("kind=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=hook-mode-repair")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
' "$chmod_worker_output" >/dev/null
run_matrix_parallel worker assert_worker_protected_hook_mode_denied worker-hook-mode-syntax \
  "env chmod 755 hooks/pre-commit-go-mod.sh" \
  "command chmod 755 hooks/pre-commit-go-mod.sh" \
  'ch"mod" 755 hooks/pre-commit-go-mod.sh' \
  'ch"mod" 644 hooks/pre-commit-go-mod.sh' \
  'chmod "755" hooks/pre-commit-go-mod.sh' \
  'chmod 755 "hooks/pre-commit-go-mod.sh"' \
  "/bin/chmod 755 hooks/pre-commit-go-mod.sh" \
  "chmod 755 ./hooks/pre-commit-go-mod.sh" \
  "chmod 644 hooks/pre-commit-go-mod.sh" \
  "chmod 644 hooks/validate-bash.sh" \
  "chmod 600 hooks/install-pre-commit-go-mod.sh" \
  "chmod 700 hooks/tests/test-pre-commit-go-mod.sh" \
  "chmod 644 $ROOT/hooks/validate-bash.sh" \
  "chmod -R 644 hooks" \
  "chmod 755 hooks/install-pre-commit-go-mod.sh" \
  "chmod 755 hooks/validate-bash.sh" \
  "chmod 755 hooks/tests/test-pre-commit-go-mod.sh" \
  "chmod 755 hooks/pre-commit-go-mod.sh"
run_matrix_parallel worker assert_worker_protected_hook_mode_denied worker-hook-mode-ownership \
  "env chmod 755 hooks/pre-commit-go-mod.sh" \
  "command chmod 755 hooks/pre-commit-go-mod.sh" \
  'ch"mod" 755 hooks/pre-commit-go-mod.sh' \
  'ch"mod" 644 hooks/pre-commit-go-mod.sh' \
  'chmod "755" hooks/pre-commit-go-mod.sh' \
  'chmod 755 "hooks/pre-commit-go-mod.sh"' \
  "/bin/chmod 755 hooks/pre-commit-go-mod.sh" \
  "chmod 755 ./hooks/pre-commit-go-mod.sh" \
  "chmod 644 hooks/pre-commit-go-mod.sh" \
  "chmod -R 644 hooks" \
  "chmod --recursive 644 hooks" \
  "chmod -R 644 ." \
  "chmod -R 644 .." \
  "chmod -vR 644 hooks" \
  "chmod --rec 755 hooks" \
  "chmod 755 -R hooks" \
  "chmod 755 --rec hooks" \
  "chmod 755 --recursive hooks" \
  "chmod 755 -vR hooks" \
  "chmod 755 hooks --rec" \
  "chmod 755 hooks -R" \
  "chmod --ref ordinary.txt hooks/validate-bash.sh" \
  "chmod --ref=ordinary.txt hooks/validate-bash.sh" \
  "chmod hooks/validate-bash.sh --ref=ordinary.txt" \
  "chmod --ref ordinary.txt -R hooks" \
  "chmod -R --ref=ordinary.txt hooks" \
  "env chmod --ref=ordinary.txt hooks/validate-bash.sh" \
  "stdbuf -oL chmod --ref=ordinary.txt hooks/validate-bash.sh" \
  "busybox -- chmod --ref=ordinary.txt hooks/validate-bash.sh" \
  "stdbuf -oL chmod --rec 755 hooks" \
  "busybox -- chmod 755 hooks --rec" \
  "stdbuf -oL chmod -R 644 hooks" \
  "busybox -- chmod --recursive 644 hooks" \
  "chmod 755 hooks/install-pre-commit-go-mod.sh" \
  "chmod 755 hooks/validate-bash.sh" \
  "chmod 755 hooks/tests/test-pre-commit-go-mod.sh"
run_matrix_parallel worker assert_worker_no_hook_mode_repair worker-hook-mode-punctuation \
  "chmod 755 hooks/pre-commit-go-mod.sh && printf after" \
  "printf before; chmod 755 hooks/pre-commit-go-mod.sh" \
  "chmod 755 hooks/pre-commit-go-mod.sh | printf after" \
  "printf before | chmod 755 hooks/pre-commit-go-mod.sh" \
  "bash -c 'chmod 755 hooks/pre-commit-go-mod.sh'"
run_subagent_matrix_parallel allowed ordinary-recursive-worker \
  "chmod -R 644 $TMP_ROOT/ordinary-recursive" \
  "chmod 644 -R $TMP_ROOT/ordinary-recursive" \
  "chmod 644 $TMP_ROOT/ordinary-recursive -R" \
  "chmod --rec 644 $TMP_ROOT/ordinary-recursive" \
  "chmod 755 hooks -- --rec" \
  "chmod --ref=hooks/validate-bash.sh $TMP_ROOT/ordinary-reference" \
  "chmod --ref hooks/validate-bash.sh $TMP_ROOT/ordinary-reference" \
  "chmod $TMP_ROOT/ordinary-reference --ref=hooks/validate-bash.sh" \
  "chmod -R --ref=hooks/validate-bash.sh $TMP_ROOT/ordinary-reference"
assert_inactive_no_hook_mode_repair "chmod 755 hooks/pre-commit-go-mod.sh"
pager_output="$(run_hook_with_transcript_pager "git -C $ROOT status --short")"
[ ! -s "$pager_output" ]
if [[ "$kimi_root" = /* ]] && [ -d "$kimi_root" ] && [ ! -L "$kimi_root" ] &&
  [ "$(realpath -m -- "$kimi_root")" = "$kimi_root" ]; then
  if [ -f "$kimi_root/hooks/tests/test-block-no-progress.sh" ] && [ ! -L "$kimi_root/hooks/tests/test-block-no-progress.sh" ]; then
    run_hook_matrix_parallel allowed kimi-entrypoint-forms \
      "bash $kimi_root/hooks/tests/test-block-no-progress.sh" \
      "$kimi_root/hooks/tests/test-block-no-progress.sh"
    default_kimi_output="$(run_hook_without_kimi_home "$kimi_root/hooks/tests/test-block-no-progress.sh")"
    [ ! -s "$default_kimi_output" ] || {
      cat -- "$default_kimi_output" >&2
      return 1
    }
  fi
  run_hook_matrix_parallel allowed kimi-project-inspection \
    "git -C $kimi_root status --short" \
    "git -C $kimi_root status --short --branch" \
    "git -C $kimi_root log -5 --oneline" \
    "git -C $kimi_root diff -- AGENTS.md" \
    "git -C $kimi_root diff --stat"
  run_matrix_parallel coordinator assert_any_denied kimi-control-write \
    "chmod 755 $kimi_root/hooks/pre-commit-go-mod.sh $kimi_root/hooks/install-pre-commit-go-mod.sh"
  run_hook_matrix_parallel allowed kimi-install-entrypoints \
    "bash hooks/install-pre-commit-go-mod.sh" \
    "bash $kimi_root/hooks/install-pre-commit-go-mod.sh" \
    "bash hooks/install-pre-commit-go-mod.sh --repair-hardlink $kimi_root"
  self_repair_output="$(run_hook "bash hooks/install-pre-commit-go-mod.sh --repair-hardlink $ROOT")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_ROUTE_ARGUMENTS_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=coordinator-hardlink-repair")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-hardlink-repair")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$self_repair_output" >/dev/null
fi
run_hook_matrix_parallel allowed coordinator-session-inspection \
  "ls -ld $ROOT/sessions" \
  "find /tmp -maxdepth 1 -type d -name 'eci-edit-controls.*' -print" \
  "readlink -f $ROOT/sessions" \
  "find /tmp -maxdepth 1 -type d -print"
# This exercises the validator's real `shutil.which("mktemp")` resolution;
# on the deployment host it resolves through the trusted cargo coreutils path.
mktemp_template="$classifier_tmp_parent/eci-probe.XXXXXX"
run_hook_matrix_parallel allowed coordinator-mktemp \
  "mktemp -d $mktemp_template" \
  "mktemp -d $mktemp_template"
run_hook_matrix_parallel unknown coordinator-mktemp-invalid \
  "mktemp -d $mktemp_template extra" \
  "mktemp -d $classifier_tmp_parent/eci-probe-\$(date).XXXXXX"
run_subagent_transcript_matrix_parallel "mktemp -d $mktemp_template" worker-mktemp-transcript \
  type-first payload-first payload-before-and-after-type
run_matrix_parallel coordinator assert_allowed coordinator-glob \
  "ls -la /tmp/*"
run_hook_matrix_parallel allowed coordinator-path-inspection \
  "realpath /usr/bin/tail" \
  "ls -l /usr/bin/tail" \
  "readlink /usr/bin/tail"
assert_worker_dynamic_find_action_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-find-action"))
  ' "$output" >/dev/null
}

assert_coordinator_dynamic_find_action_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-find-action")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'coordinator dynamic find-action denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_git_ownership_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output token
  token="${command#git }"
  token="${token%% *}"
  output="$("$runner" "$command")"
  jq -e --arg token "token=$token" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains($token)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'worker Git ownership denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}
run_matrix_parallel worker assert_worker_dynamic_find_action_denied worker-dynamic-find-action \
  "find /tmp -maxdepth 1 -type d -delete" \
  "find /tmp -maxdepth 1 -type d -exec true \\;"
run_subagent_matrix_parallel allowed worker-tmp-inspection \
  "find /tmp -maxdepth 1 -type d -print"

run_matrix_parallel coordinator assert_coordinator_dynamic_find_action_denied coordinator-dynamic-find-actions \
  "find /tmp -maxdepth 1 -type f -exec file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec ./file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec /tmp/file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec ../file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec file -C -m $TMP_ROOT/magic-file {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec file --compile -m $TMP_ROOT/magic-file {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec printf '%s' {} \\; && printf done" \
  "find /tmp -maxdepth 1 -type f -delete && printf done"
run_matrix_parallel coordinator assert_allowed coordinator-safe-compound-find \
  "find /tmp -maxdepth 1 -type f -print && printf done"

# Coordinator project inspection accepts finite literal lists from the
# validated companion Kimi root, including bounded find/stat forms.
kimi_find_a="$kimi_root/.eci-runner-test.0dcfk8kp"
kimi_find_b="$kimi_root/.eci-runner-test.pjj7ll1a"
run_hook_matrix_parallel allowed coordinator-proof-inspection \
  "ls -ld $kimi_root $kimi_find_a $kimi_find_b" \
  "find $kimi_find_a $kimi_find_b -maxdepth 2 -type f -print | head -n 40" \
  "stat -Lc '%i %a %n' $kimi_root/hooks/validate-bash.sh $kimi_root/hooks/tests/run.sh" \
  "stat -Lc '%i %a %h %n' $kimi_root/hooks/validate-bash.sh $kimi_root/hooks/tests/run.sh" \
  "stat -c '%d:%i %a %h %n' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '%y %s %n' $kimi_root/hooks/validate-bash.sh" \
  "stat -Lc '%F %s %n' $kimi_root/hooks/validate-bash.sh" \
  "stat -Lc '%F %N' $kimi_root/hooks/validate-bash.sh" \
  "stat -Lc '%i %a %h %s %n' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '%s' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '%x' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '%x %s %n' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '%a %n' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '%A %n' $kimi_root/hooks/validate-bash.sh" \
  "ls -1 $evidence_dir" \
  "find -P $evidence_dir -maxdepth 2 -type f -print" \
  "stat -c '%a %n' $evidence_file" \
  "stat -Lc '%i %a %h %n' $evidence_file" \
  "cat $instructions_file" \
  "sed -n '1p' $instructions_file" \
  "rg -n 'proof evidence' $evidence_dir" \
  "rg -n -i 'proof evidence' $evidence_dir" \
  "readlink -f $evidence_file" \
  "realpath $evidence_file" \
  "realpath -e $evidence_file && stat -Lc '%d:%i %a %h %n' $proof_root"
run_matrix_parallel coordinator assert_stat_format_case coordinator-stat-format \
  "stat -c '%Q' $kimi_root/hooks/validate-bash.sh" \
  "stat -c '\$(printf %s)' $kimi_root/hooks/validate-bash.sh" \
  "stat --printf='%s' $kimi_root/hooks/validate-bash.sh"
run_matrix_parallel coordinator assert_coordinator_dynamic_find_action_denied coordinator-syntax-denial \
  "find $kimi_find_a $kimi_find_b -maxdepth 2 -type f -exec rm -f {} \;"
run_hook_matrix_parallel allowed coordinator-proof-inspection-output \
  "find $kimi_find_a $kimi_find_b -maxdepth 2 -type f -print > $TMP_ROOT/find-output" \
  "find $kimi_find_a $kimi_find_b* -maxdepth 2 -type f -print" \
  "ls -ld $kimi_root $kimi_find_b*" \
  "stat -Lc '%i %a %n' $kimi_root/hooks/validate-bash.sh > $TMP_ROOT/stat-output"
run_hook_matrix_parallel allowed coordinator-finite-argv \
  "find $kimi_find_a $kimi_find_b -maxdepth 2 -type f -print /etc/passwd" \
  "find $kimi_find_a $kimi_find_b/../outside -maxdepth 2 -type f -print" \
  "ls -ld $kimi_root $kimi_find_b/../outside" \
  "stat -Lc '%i %a %n' $kimi_root/hooks/../outside"
# The compound cleanup route intentionally permits only a direct child of the
# canonical temporary parent.  The command is only classified, never run.
compound_cleanup_target="$classifier_tmp_parent/eci-classifier-compound-${BASHPID}-cleanup"
assert_allowed "realpath -e $evidence_file && rm -f $compound_cleanup_target" run_hook_with_canonical_tmpdir
assert_compound_mutation_denied "realpath -e $evidence_file && rm -f $TMP_ROOT/denied" run_hook_with_canonical_tmpdir
# A current session may spell its canonical temporary root through exactly
# $HOME/tmp.  Exercise the generic cleanup route directly: it must allow one
# generated file beneath the resolved root but never a sibling/outside root.
tmpdir_alias_cleanup_target="$classifier_tmp_parent/eci-classifier-alias-${BASHPID}-cleanup"
tmpdir_alias_outside_target="${classifier_tmp_parent}-outside/eci-classifier-alias-${BASHPID}-cleanup"
assert_allowed "rm -f $tmpdir_alias_cleanup_target" run_hook_with_tmpdir_home_alias
assert_denied "rm -f $tmpdir_alias_outside_target" run_hook_with_tmpdir_home_alias
# The reviewed compound route may clean one regular leaf inside the private
# session directory created directly beneath the configured temporary root.
# It cannot reach a sibling session directory, a deeper descendant, an
# outside root, or the root through its $HOME/tmp alias.
# This runner deliberately points HOME at an isolated alias with no
# $HOME/.omp/agent/eci planner, exercising the same target-aware route during a
# planner-unavailable source-build gap.
session_temp_cleanup_target="$TMP_ROOT/eci-classifier-session-cleanup-${BASHPID}"
session_temp_sibling_target="$session_temp_sibling/eci-classifier-session-cleanup-${BASHPID}"
session_temp_deeper_target="$session_temp_deeper/eci-classifier-session-cleanup-${BASHPID}"
session_temp_outside_target="${classifier_tmp_parent}-outside/eci-classifier-session-cleanup-${BASHPID}"
session_temp_alias_target="$tmpdir_alias_home/tmp/${TMP_ROOT##*/}/eci-classifier-session-cleanup-${BASHPID}"
session_temp_link_target="$session_temp_link/eci-classifier-session-cleanup-${BASHPID}"
assert_allowed "git status --short || rm -f $session_temp_cleanup_target" run_hook_with_tmpdir_home_alias
assert_allowed "bash hooks/tests/test-eci-fast-path.sh && rm -f $session_temp_cleanup_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_sibling_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_deeper_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_outside_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_alias_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_link_target" run_hook_with_tmpdir_home_alias
assert_denied "git status --short || rm -f $TMP_ROOT/../${TMP_ROOT##*/}/eci-classifier-session-cleanup-${BASHPID}" run_hook_with_tmpdir_home_alias
assert_denied "git status --short || rm -f $TMP_ROOT/*" run_hook_with_tmpdir_home_alias
assert_allowed "git status --short || printf after" run_hook_with_tmpdir_home_alias
compound_broad_output="$(run_hook "realpath -e $evidence_file && rm -rf /")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=broad-destructive")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
' "$compound_broad_output" >/dev/null
assert_denied "cat $evidence_dir/outside-link"
# The same proof-root escape must remain actionable when the planner is
# unavailable under an isolated HOME.  The fallback checks the already
# classified read effect, not the path spelling or planner/receipt state.
alias_proof_escape_output="$(run_hook_with_tmpdir_home_alias "cat $evidence_dir/outside-link")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PROOF_PATH_ESCAPE_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=proof-path-ownership")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=proof-path-escape"))
' "$alias_proof_escape_output" >/dev/null
# A generic path outside the proof namespace and an in-root regular file are
# ordinary read targets; only the lexical proof-root symlink escape is denied.
assert_allowed "cat $TMP_ROOT/outside-proof.txt" run_hook_with_tmpdir_home_alias
assert_allowed "cat $evidence_file" run_hook_with_tmpdir_home_alias
run_subagent_matrix_parallel denied worker-kimi-protected-inspection \
  "find $kimi_find_a $kimi_find_b -maxdepth 2 -type f -print | head -n 40" \
  "readlink -f $ROOT/sessions"
run_subagent_matrix_parallel allowed worker-kimi-finite-inspection \
  "stat -Lc '%F %N' $kimi_root/hooks/validate-bash.sh" \
  "find $TMP_ROOT -maxdepth 1 -type f -print"

# The compiled coordinator planner admits finite literal Git pathspec reads;
# the legacy fallback remains bounded separately when planner admission is
# unavailable.
printf -v git_diff_pathspecs_16 ' AGENTS.md%.0s' {1..16}
git_diff_pathspecs_17="$git_diff_pathspecs_16 AGENTS.md"
run_hook_matrix_parallel allowed git-read-argv \
  "git -C $ROOT -C $ROOT status --short" \
  "git -C $ROOT -C $ROOT diff -- hooks/validate-bash.sh" \
  "git -C $ROOT status --short ../outside" \
  "git -C $ROOT diff --stat /etc/passwd" \
  "git -C $ROOT diff -- /etc/passwd" \
  "git -C $ROOT diff -- ../outside" \
  "git -C $ROOT diff --$git_diff_pathspecs_16" \
  "git -C $ROOT diff --$git_diff_pathspecs_17" \
  "git -C $ROOT log -1 --oneline ':(exclude)hooks'" \
  "git -C $ROOT show --stat -1 hooks//validate-bash.sh"
# Git context selection and exclude pathspec spelling are read-only concerns.
# Keep them transparent so Git/provider handling can resolve the actual target;
# foreign/repeated context is not itself an accidental mutation.
run_hook_matrix_parallel allowed coordinator-git-context-inspection \
  "git -C $TMP_ROOT status --short" \
  "git -C $ROOT -C $TMP_ROOT status --short" \
  "git -C $ROOT diff -- :(exclude)AGENTS.md" \
  "git -C $ROOT diff -- ':(exclude)AGENTS.md'" \
  "git -C $ROOT -c user.name=test status --short" \
  "GIT_DIR=$TMP_ROOT git -C $ROOT status --short"

# Read-only ls remains admitted through the full classifier when a transcript
# prevents the transcriptless fast path, including absolute inspection paths.
ls_output="$(run_hook_with_transcript "ls -la $ROOT")"
[ ! -s "$ls_output" ] || {
  cat -- "$ls_output" >&2
  exit 1
}

assert_commit() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  case "$command" in
    "git commit -a --amend --no-verify --signoff")
      jq -e '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=git-mutation")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("remediation:")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("unrecognized command form") | not)
      ' "$output" >/dev/null
      ;;
    *)
      jq -e '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_GIT_COMMIT_PRODUCER_REQUIRED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("unrecognized command form") | not)
      ' "$output" >/dev/null
      ;;
  esac
}

# Active-ECI local index staging resolves its repository and selector before
# deciding whether the concrete effect is ordinary or broad/destructive.
run_hook_matrix_parallel allowed coordinator-git-prep \
  "git add -- hooks/validate-bash.sh" \
  "git mv -- hooks/validate-bash.sh hooks/validate-bash.sh" \
  "git restore --staged -- hooks/validate-bash.sh" \
  "git add -- ../outside" \
  "git mv -- hooks/validate-bash.sh ../outside" \
  "git restore --staged hooks/validate-bash.sh" \
  "env git add -- hooks/validate-bash.sh"
run_matrix_parallel coordinator assert_git_whole_worktree_staging_denied coordinator-git-prep-unsafe \
  "git add ."
run_matrix_parallel coordinator assert_coordinator_protected_git_target_denied coordinator-protected-git-target \
  "git rm -- hooks/validate-bash.sh" \
  "git rm -r -- hooks/validate-bash.sh"
run_subagent_matrix_parallel allowed worker-git-staging \
  "git add -- hooks.json" \
  "git add README.md"
run_matrix_parallel worker assert_worker_git_ownership_denied worker-git-prep \
  "git rm -- hooks/validate-bash.sh" \
  "git mv -- hooks/validate-bash.sh hooks/validate-bash.sh" \
  "git restore --staged -- hooks/validate-bash.sh"

assert_denied() {
  local command="$1" output
  output="$(run_hook "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'assert_denied failed: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

# Executable identity is not an allowlist boundary. A finite direct argv stays
# ordinary even when a task-owned executable happens to be named `git`.
fake_bin="$TMP_ROOT/fake-bin"
mkdir -p "$fake_bin"
cp -- /bin/true "$fake_bin/git"
chmod +x "$fake_bin/git"
run_hook_matrix_parallel allowed fake-git-coordinator \
  "$fake_bin/git status"
# The active-worker route resolves the concrete Git target for status too;
# this must not fall through to a lexical Git parser or a foreign-repo denial.
run_subagent_matrix_parallel allowed fake-git-worker \
  "$fake_bin/git status"
fake_git_history_output="$(run_subagent_hook "$fake_bin/git rebase topic")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=rebase"))
' "$fake_git_history_output" >/dev/null

assert_subagent_lifecycle_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    ((.hookSpecificOutput.permissionDecisionReason | contains("Only the main/orchestrator may mutate ECI lifecycle")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("Only the main thread/orchestrator may disengage ECI with eci-active off")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("ECI_COMMAND_WRAPPER_UNSUPPORTED")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("ECI_COMMAND_DYNAMIC_INDIRECTION_DENIED")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("ECI_PLAN_DYNAMIC_LAUNCH_DENIED")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("ECI_ENVIRONMENT_OPTION_DENIED")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("ECI_LIFECYCLE_CANONICAL_PATH_DENIED")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("unsupported shell/interpreter wrapper")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("unsupported shell/interpreter launcher")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("ECI_CONTROL_OWNER_REQUIRED")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("command path owned by the coordinator")))
  ' "$output" >/dev/null
}

assert_subagent_unknown_command_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'failed subagent command: %s\n' "$command" >&2
    cat "$output" >&2
    return 1
  }
}

assert_worker_dynamic_launch_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    ((.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_SYNTAX_DENIED]"))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    ((.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-launch")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("predicate=leading-assignment"))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'worker dynamic-launch denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_wrapped_git_ownership_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("git rebase topic")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=rebase")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("coordinator"))
  ' "$output" >/dev/null || {
    printf 'wrapped worker Git ownership denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_chronic_worker_route_case() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  case "$command" in
    "chronic /tmp/eci-escape.sh")
      [ ! -s "$output" ]
      ;;
    "chronic git rebase topic")
      jq -e '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("git rebase topic")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("token=rebase")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("coordinator"))
      ' "$output" >/dev/null
      ;;
    *)
      printf 'unknown chronic fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
}

assert_copied_lifecycle_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e --arg copied_target "${copied_eci:-}" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (
      (
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-control")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("targets coordinator-owned lifecycle/control state"))
      ) or
      (
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_TARGET_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-lifecycle")) and
        ($copied_target == "" or (.hookSpecificOutput.permissionDecisionReason | contains(("target=" + $copied_target))))
      )
    ) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'copied lifecycle denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_control_owner_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-control")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-lifecycle-control"))
  ' "$output" >/dev/null || {
    printf 'worker control-owner denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_dynamic_interpreter_or_git_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (
      (
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("token=-c")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=1")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-interpreter-launch"))
      ) or
      (
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("token=rebase"))
      )
    )
  ' "$output" >/dev/null || {
    printf 'worker dynamic-interpreter denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

eci="$ROOT/bin/eci-active"
# Keep the executable path above for the test's real lifecycle setup, but pass
# only this raw spelling to the hook as a lifecycle command.
hook_eci="$eci_lifecycle"
# Activation is a coordinator-owned ECI lifecycle control action.  It must be
# admitted by the command classifier so a new ECI task can start through the
# canonical lifecycle binary rather than bypassing the PreToolUse boundary.
assert_allowed "$hook_eci on classifier-activation"
assert_allowed "$hook_eci --help"
assert_allowed "$hook_eci status"
assert_allowed "$hook_eci ledger-append --json '{\"event\":\"decision\",\"summary\":\"entry with \`literal\` and \$dollar\"}'"

# Exercise the real wait/resume path as a parser regression.  In particular,
# resume must clear the state without taking a syntax-error branch.
wait_report="$proof_root/t00-session/eci_user_owned_wait.md"
printf '%s\n' \
  '# ECI User-Owned Wait' \
  'state: user-owned-wait' \
  'blocker_id: classifier-resume' \
  'state_fingerprint: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  'owner: user' \
  'brp_result: exhausted-no-feasible-internal-path' \
  'user_owned_input: unobtainable' \
  'unblock_kind: input' \
  'unblock: changed user-owned input required' \
  >"$wait_report"
ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID=t00-session ECI_HOME="$ROOT" \
  "$eci" wait "$wait_report" >/dev/null
ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID=t00-session ECI_HOME="$ROOT" \
  "$eci" resume bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb >/dev/null
[ ! -e "$proof_root/t00-session/eci_wait" ]

run_hook_matrix_parallel allowed coordinator-lifecycle-shapes \
  "$hook_eci off $TMP_ROOT/disengage.md" \
  "$hook_eci wait $TMP_ROOT/eci_user_owned_wait.md" \
  "$hook_eci resume aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
  "$ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "$hook_eci ledger-append --json '{\"event\":\"decision\",\"summary\":\"one-line-entry\"}'" \
  "$hook_eci nested-enter 1 2 t00-session" \
  "$hook_eci nested-accept" \
  "$hook_eci nested-exit" \
  "$hook_eci manifest-write $proof_root/t00-session/eci-required-critics.json.source"

# Aggregate staging is a narrow parent-owned lifecycle bridge: it has a
# declared repository selector, exactly one separator, and literal relative
# operands. Wrapper/environment forms must not become an alternate route.
run_hook_matrix_parallel allowed coordinator-aggregate-stage-lifecycle \
  "$hook_eci aggregate-stage repo-a -- hooks/target.txt" \
  "$hook_eci aggregate-stage repo-a -- 'new staged file.txt' -leading-dash.txt"
assert_lifecycle_denied "$hook_eci aggregate-stage repo-a hooks/target.txt"
assert_lifecycle_denied "$hook_eci aggregate-stage repo-a -- --"
assert_lifecycle_denied "$hook_eci aggregate-stage repo-a -- ../outside"
assert_lifecycle_denied "$hook_eci aggregate-stage repo-a -- eci_active"
assert_lifecycle_denied "env ECI_SESSION_ID=t00-session $hook_eci aggregate-stage repo-a -- hooks/target.txt"
stale_report="$proof_root/019ff790-0000-7000-8000-000000000001/disengage.md"
assert_allowed "$hook_eci off $stale_report"

# An alternate root is not an authority alias, even where it resolves to the
# same deployed executable. The source spelling remains part of the contract.
alias_home="$TMP_ROOT/eci-home-alias"
ln -s "$ROOT" "$alias_home"
assert_eci_lifecycle_spelling_denied "$alias_home/bin/eci-active off $TMP_ROOT/disengage.md"

# Canonical review-gate identity is protected before ordinary finite-literal
# admission, including shell-script invocation and malformed argument shapes.
assert_worker_review_gate_denied() {
  local launcher="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$launcher")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_REVIEW_GATE_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-review-gate")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("provider=eci")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("marker=active")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("segment=1")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("path=n/a")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-lifecycle-control")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'worker review-gate denial mismatch: command=%q output=%s\n' "$launcher" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_control_script_denied() {
  local launcher="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$launcher")"
  jq -e --arg canonical_target "$ROOT/hooks/eci-active-gate.sh" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_CONTROL_SCRIPT_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-control-script")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("subject=provider=eci")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("marker=active")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("canonical_target=" + $canonical_target))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("invocation=shell-script")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("argv=[]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'worker control-script denial mismatch: command=%q output=%s\n' "$launcher" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_worker_gate_case() {
  local command="$1" runner="${2:-run_subagent_hook}"
  case "$command" in
    *"eci-review-gate.sh"*) assert_worker_review_gate_denied "$command" "$runner" ;;
    "bash -n hooks/eci-active-gate.sh"|"sh -n hooks/eci-active-gate.sh") assert_allowed "$command" "$runner" ;;
    *"eci-active-gate.sh"*) assert_worker_control_script_denied "$command" "$runner" ;;
    *)
      printf 'unknown worker gate fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
}

run_matrix_parallel worker assert_worker_gate_case worker-gate-entrypoints \
  "$ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "bash $ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "bash -e $ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "bash -x $ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "bash -O extglob $ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "bash --noprofile $ROOT/hooks/eci-review-gate.sh commit t00-session" \
  "bash $ROOT/hooks/eci-review-gate.sh unknown t00-session" \
  "bash $ROOT/hooks/eci-review-gate.sh commit" \
  "bash $ROOT/hooks/eci-review-gate.sh commit t00-session extra" \
  "bash -O extglob $ROOT/hooks/eci-active-gate.sh" \
  "bash --noprofile $ROOT/hooks/eci-active-gate.sh" \
  "bash -n hooks/eci-active-gate.sh" \
  "sh -n hooks/eci-active-gate.sh" \
  "sh -e $ROOT/hooks/eci-active-gate.sh"
output="$(run_subagent_hook "python3 -c 'open(\"$proof_root/t00-session/eci_wait\",\"w\").write(\"x\")'")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=-c")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=1")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-interpreter-launch"))
' "$output" >/dev/null
# Explicit hook test entry points remain bounded worker routes.
run_subagent_matrix_parallel allowed worker-test-entrypoints \
  "bash hooks/tests/test-eci-fast-path.sh" \
  "bash hooks/tests/test-validate-bash-git-approvals.sh" \
  "bash hooks/tests/test-pre-commit-go-mod.sh"

# A worker test route is structural: a regular, canonical direct-child
# hooks/tests/*.sh path stays admissible after harmless content changes. The
# copy has its own active marker and worker home, so the real validator
# resolves the script from the fixture CWD without changing tracked source.
isolated_worker_home="$TMP_ROOT/isolated-home"
isolated_worker_root="$isolated_worker_home/.omp/agent/eci"
isolated_worker_session="isolated-worker-session"
isolated_worker_proof_root="$TMP_ROOT/isolated-worker-proof"
isolated_worker_script_relative="hooks/tests/test-eci-fast-path.sh"
isolated_worker_script="$isolated_worker_root/$isolated_worker_script_relative"
isolated_worker_transcript="$isolated_worker_root/sessions/eci-validate-bash-isolated-$BASHPID.jsonl"
mkdir -p -- "$isolated_worker_root/bin" "$isolated_worker_root/hooks/lib" "$isolated_worker_root/sessions" \
  "$(dirname -- "$isolated_worker_script")" "$isolated_worker_proof_root/$isolated_worker_session"
git -C "$isolated_worker_root" init --quiet
cp -- "$ROOT/bin/eci-active" "$isolated_worker_root/bin/eci-active"
cp -- "$ROOT/hooks/lib/eci-proof-state.sh" "$isolated_worker_root/hooks/lib/eci-proof-state.sh"
cp -- "$ROOT/hooks/lib/eci-file-lock.sh" "$isolated_worker_root/hooks/lib/eci-file-lock.sh"
chmod +x "$isolated_worker_root/bin/eci-active"
printf '%s\n' '# isolated worker ECI instructions' >"$isolated_worker_root/AGENTS.md"
printf '%s\n' '# isolated worker agent instructions' >"$isolated_worker_root/AGENTS.md"
cp -- "$ROOT/$isolated_worker_script_relative" "$isolated_worker_script"
printf '%s\n' \
  'scope: isolated worker script fixture' \
  "cwd: $isolated_worker_root" \
  "session_id: $isolated_worker_session" \
  'created_utc: 2026-08-24T00:00:00Z' \
  >"$isolated_worker_proof_root/$isolated_worker_session/eci_active"
printf '%s\n' '{"timestamp":"2026-08-15T00:00:00.000Z","type":"session_meta","payload":{"id":"isolated-worker-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"fixture-parent","depth":1,"agent_nickname":"Fixture","agent_role":"default"}}}}}' \
  >"$isolated_worker_transcript"

run_isolated_worker_hook() {
  local command="$1" output
  output="$TMP_ROOT/isolated-worker-output"
  (
    cd "$isolated_worker_root"
    jq -cn --arg cwd "$isolated_worker_root" --arg command "$command" \
      --arg transcript "$isolated_worker_transcript" --arg session "$isolated_worker_session" \
      '{session_id:$session,tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
      HOME="$isolated_worker_home" ECI_PROOF_ROOT="$isolated_worker_proof_root" \
        ECI_HOME="$isolated_worker_root" PATH="$isolated_worker_root/bin:$PATH" \
        bash "$classifier_hook_fixture" >"$output"
  )
  printf '%s\n' "$output"
}

assert_isolated_worker_structural_denied() {
  local label="$1" command="$2" output

  output="$(run_isolated_worker_hook "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'isolated worker structural negative was admitted: %s\n' "$label" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    exit 1
  }
}

assert_isolated_worker_installer_denied() {
  local command="$1" output

  output="$(run_isolated_worker_hook "$command")"
  jq -e --arg canonical_target "$isolated_worker_non_test" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_HOOK_INSTALLER_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-hook-installer")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-hook-installer")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("canonical_target=" + $canonical_target))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'isolated worker installer denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_isolated_worker_script_target_denied() {
  local label="$1" command="$2" expected_resolved="$3" output

  output="$(run_isolated_worker_hook "$command")"
  jq -e --arg expected_resolved "$expected_resolved" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_SCRIPT_TARGET_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-script-target")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-script-target")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("candidate=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("resolved=" + $expected_resolved))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("repository-allow-on")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'isolated worker script-target denial mismatch: label=%s command=%q output=%s\n' \
      "$label" "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_isolated_worker_structural_allowed() {
  local label="$1" command="$2" output

  output="$(run_isolated_worker_hook "$command")"
  [ ! -s "$output" ] || {
    printf 'isolated worker benign structural form was denied: %s command=%q output=%s\n' \
      "$label" "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

live_worker_script="$ROOT/$isolated_worker_script_relative"
live_worker_script_sha_before="$(sha256sum -- "$live_worker_script" | awk '{print $1}')"
isolated_clean_worker_output="$(run_isolated_worker_hook "bash $isolated_worker_script_relative")"
[ ! -s "$isolated_clean_worker_output" ]
printf '%s\n' '# isolated worker mutation probe' >>"$isolated_worker_script"
isolated_mutated_worker_output="$(run_isolated_worker_hook "bash $isolated_worker_script_relative")"
[ ! -s "$isolated_mutated_worker_output" ]

isolated_structural_worker_relative="hooks/tests/structural-worker.sh"
isolated_structural_worker="$isolated_worker_root/$isolated_structural_worker_relative"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$isolated_structural_worker"
chmod 644 -- "$isolated_structural_worker"
isolated_structural_worker_output="$(run_isolated_worker_hook "bash $isolated_structural_worker_relative")"
[ ! -s "$isolated_structural_worker_output" ]
isolated_structural_sh_output="$(run_isolated_worker_hook "sh $isolated_structural_worker_relative")"
[ ! -s "$isolated_structural_sh_output" ]

isolated_worker_non_test_relative="hooks/install-pre-commit-go-mod.sh"
isolated_worker_non_test="$isolated_worker_root/$isolated_worker_non_test_relative"
mkdir -p -- "$(dirname -- "$isolated_worker_non_test")"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$isolated_worker_non_test"
chmod 755 -- "$isolated_worker_non_test"
isolated_worker_non_script_relative="hooks/tests/structural-worker.txt"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$isolated_worker_root/$isolated_worker_non_script_relative"
isolated_worker_outside="$TMP_ROOT/isolated-worker-outside.sh"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$isolated_worker_outside"
ln -s -- "$isolated_worker_outside" "$isolated_worker_root/hooks/tests/structural-link.sh"
ln -s -- "$isolated_worker_root/hooks/tests" "$isolated_worker_root/hooks/tests-link"
isolated_worker_peer_root="$TMP_ROOT/isolated-worker-peer/.omp/agent/eci"
isolated_worker_peer_script="$isolated_worker_peer_root/$isolated_structural_worker_relative"
mkdir -p -- "$(dirname -- "$isolated_worker_peer_script")"
git -C "$isolated_worker_peer_root" init --quiet
cp -- "$isolated_structural_worker" "$isolated_worker_peer_script"
chmod 644 -- "$isolated_worker_peer_script"
assert_isolated_worker_installer_denied "bash $isolated_worker_non_test_relative"
assert_isolated_worker_installer_denied "bash hooks/tests/../install-pre-commit-go-mod.sh"
assert_isolated_worker_structural_allowed non-shell-suffix "bash $isolated_worker_non_script_relative"
assert_isolated_worker_script_target_denied symlink-outside \
  'bash hooks/tests/structural-link.sh' "$isolated_worker_outside"
assert_isolated_worker_structural_allowed parent-symlink \
  'bash hooks/tests-link/structural-worker.sh'
assert_isolated_worker_script_target_denied peer-workspace \
  "bash $isolated_worker_peer_script" "$isolated_worker_peer_script"
(
  cd -- "$isolated_worker_root"
  env ECI_PROOF_ROOT="$isolated_worker_proof_root" ECI_SESSION_ID="$isolated_worker_session" ECI_ROLE=worker \
    "$isolated_worker_root/bin/eci-active" repository-allow-on "$isolated_worker_peer_root" \
    'worker owns a narrow dependency script execution'
)
assert_isolated_worker_structural_allowed peer-workspace-allow \
  "bash $isolated_worker_peer_script"
assert_isolated_worker_structural_allowed bash-option "bash -n $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed sh-option "sh -n $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed environment-wrapper "env bash $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed compound \
  "bash $isolated_structural_worker_relative && bash $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed assignment "FOO=bar bash $isolated_structural_worker_relative"
live_worker_script_sha_after="$(sha256sum -- "$live_worker_script" | awk '{print $1}')"
[ "$live_worker_script_sha_before" = "$live_worker_script_sha_after" ]

# Only exact two-token bash|sh test launches use the structural canonical-test
# boundary. Syntax-only and compound forms remain allowed when they do not
# execute or mutate control state. Keep this fixture isolated so its mutation
# cannot alter tracked test source.
isolated_coordinator_home="$TMP_ROOT/isolated-coordinator-home"
isolated_coordinator_root="$isolated_coordinator_home/.omp/agent/eci"
isolated_coordinator_peer="$isolated_coordinator_home/.kimi-code"
isolated_coordinator_session="isolated-coordinator-session"
isolated_coordinator_proof_root="$classifier_tmp_parent/tmp/agent/proof"
isolated_coordinator_script_relative="hooks/tests/test-eci-fast-path.sh"
isolated_coordinator_script="$isolated_coordinator_root/$isolated_coordinator_script_relative"
mkdir -p -- "$(dirname -- "$isolated_coordinator_script")" \
  "$isolated_coordinator_peer" \
  "$isolated_coordinator_proof_root/$isolated_coordinator_session"
cp -- "$ROOT/$isolated_coordinator_script_relative" "$isolated_coordinator_script"
printf '%s\n' \
  'scope: isolated coordinator script fixture' \
  "cwd: $isolated_coordinator_root" \
  "session_id: $isolated_coordinator_session" \
  'created_utc: 2026-08-25T00:00:00Z' \
  >"$isolated_coordinator_proof_root/$isolated_coordinator_session/eci_active"

run_isolated_coordinator_hook() {
  local command="$1" output
  output="$TMP_ROOT/isolated-coordinator-output"
  (
    cd "$isolated_coordinator_root"
    jq -cn --arg cwd "$isolated_coordinator_root" --arg command "$command" \
      --arg session "$isolated_coordinator_session" \
      '{session_id:$session,tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$isolated_coordinator_home" TMPDIR="$classifier_tmp_parent" ECI_TMPDIR="$classifier_tmp_parent" \
        ECI_PROOF_ROOT="$isolated_coordinator_proof_root" ECI_HOME="$isolated_coordinator_root" \
        KIMI_CODE_HOME="$isolated_coordinator_peer" \
        PATH="$ROOT/bin:$PATH" bash "$classifier_hook_fixture" >"$output"
  )
  printf '%s\n' "$output"
}

assert_isolated_coordinator_structural_allowed() {
  local command="$1" output

  output="$(run_isolated_coordinator_hook "$command")"
  [ ! -s "$output" ] || {
    printf 'isolated coordinator structural test route was not admitted: %s\n' "$command" >&2
    cat -- "$output" >&2
    exit 1
  }
}

assert_isolated_coordinator_structural_denied() {
  local command="$1" output

  output="$(run_isolated_coordinator_hook "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'isolated coordinator structural negative was admitted: %s\n' "$command" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    exit 1
  }
}

assert_isolated_coordinator_script_target_denied() {
  local label="$1" command="$2" expected_resolved="$3" output

  output="$(run_isolated_coordinator_hook "$command")"
  jq -e --arg expected_resolved "$expected_resolved" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_SCRIPT_TARGET_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=coordinator-script-target")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-script-target")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("resolved=" + $expected_resolved))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason=the resolved script target is outside the selected provider root")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'isolated coordinator script-target denial mismatch: label=%s command=%q output=%s\n' \
      "$label" "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_isolated_coordinator_manifest_route_denied() {
  local command="$1" canonical_target="$2" output

  output="$(run_isolated_coordinator_hook "$command")"
  jq -e --arg canonical_target "$canonical_target" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_MANIFEST_ROUTE_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=coordinator-manifest-route")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-manifest-route")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("canonical_target=" + $canonical_target))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_EMIT_* assignments are control metadata")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'isolated coordinator manifest-route denial mismatch: command=%q output=%s\n' \
      "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

isolated_clean_coordinator_output="$(run_isolated_coordinator_hook "bash $isolated_coordinator_script_relative")"
[ ! -s "$isolated_clean_coordinator_output" ]
printf '%s\n' '# isolated coordinator mutation probe' >>"$isolated_coordinator_script"
for isolated_structural_command in \
  "bash $isolated_coordinator_script_relative" \
  "sh $isolated_coordinator_script_relative" \
  "bash -n $isolated_coordinator_script_relative" \
  "bash -x -n $isolated_coordinator_script_relative" \
  "bash $isolated_coordinator_script_relative && bash $isolated_coordinator_script_relative"; do
  assert_isolated_coordinator_structural_allowed "$isolated_structural_command"
done
assert_isolated_coordinator_structural_allowed \
  "bash $isolated_coordinator_script_relative && bash -n hooks/eci-active-gate.sh"

isolated_coordinator_outside="$TMP_ROOT/isolated-coordinator-outside.sh"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$isolated_coordinator_outside"
assert_isolated_coordinator_script_target_denied outside-root \
  "bash $isolated_coordinator_outside" "$isolated_coordinator_outside"

isolated_repair_script_relative="hooks/install-pre-commit-go-mod.sh"
isolated_repair_script="$isolated_coordinator_root/$isolated_repair_script_relative"
mkdir -p -- "$(dirname -- "$isolated_repair_script")"
cp -- "$ROOT/$isolated_repair_script_relative" "$isolated_repair_script"
printf '%s\n' '# isolated hard-link repair mutation probe' >>"$isolated_repair_script"
assert_isolated_coordinator_structural_allowed \
  "bash $isolated_repair_script_relative"
assert_isolated_coordinator_structural_allowed \
  "bash $isolated_repair_script_relative --repair-hardlink $isolated_coordinator_peer"
assert_isolated_coordinator_structural_denied \
  "bash $isolated_repair_script_relative --repair-hardlink $TMP_ROOT"
assert_isolated_coordinator_structural_allowed \
  "bash -n $isolated_repair_script_relative"
assert_isolated_coordinator_structural_allowed \
  "bash $isolated_coordinator_script_relative --repair-hardlink $isolated_coordinator_peer"

isolated_manifest_script_relative="hooks/tests/test-eci-review-gate.sh"
isolated_manifest_script="$isolated_coordinator_root/$isolated_manifest_script_relative"
isolated_manifest_source_dir="$classifier_tmp_parent/isolated-manifest-source"
isolated_manifest_source_path="$isolated_manifest_source_dir/eci-required-critics.json.source"
mkdir -p -- "$(dirname -- "$isolated_manifest_script")" "$isolated_manifest_source_dir"
cp -- "$ROOT/$isolated_manifest_script_relative" "$isolated_manifest_script"
isolated_manifest_assignments="ECI_EMIT_CURRENT_MANIFEST=1 ECI_EMIT_SESSION_ID=$isolated_coordinator_session ECI_TEST_REPO=$isolated_coordinator_root ECI_EMIT_PROOF_ROOT=$isolated_coordinator_proof_root ECI_EMIT_KIND=current ECI_EMIT_SOURCE_PATH=$isolated_manifest_source_path"
assert_isolated_coordinator_structural_allowed \
  "$isolated_manifest_assignments bash $isolated_manifest_script_relative"
assert_isolated_coordinator_manifest_route_denied \
  "$isolated_manifest_assignments bash $isolated_coordinator_script_relative" \
  "$isolated_coordinator_root/$isolated_manifest_script_relative"

assert_eci_lifecycle_spelling_denied "eci-active nested-exit"

# Startup-sensitive execution context must not be attached to an allowlisted
# test script.  In particular, a BASH_ENV probe must be denied before the
# reviewed script could start; validation itself must not execute the probe.
bash_env_probe="$TMP_ROOT/bash-env-probe"
printf '%s\n' "touch '$TMP_ROOT/bash-env-ran'" >"$bash_env_probe"
run_matrix_parallel coordinator assert_any_denied coordinator-environment-injection \
  "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "CDPATH=$TMP_ROOT bash hooks/tests/test-eci-fast-path.sh" \
  "PYTHONSTARTUP=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "RUBYOPT=-r$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "NODE_OPTIONS=--require=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "PERL5OPT=-I$TMP_ROOT bash hooks/tests/test-eci-fast-path.sh" \
  "PATH=$TMP_ROOT bash hooks/tests/test-eci-fast-path.sh" \
  "env BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "env -S 'BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh'"
[ ! -e "$TMP_ROOT/bash-env-ran" ]
output="$(run_subagent_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh")"
jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null
[ ! -e "$TMP_ROOT/bash-env-ran" ]
run_matrix_parallel worker assert_any_denied worker-environment-injection \
  "env BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh"

# The coordinator boundary is effect-based, not value- or spelling-based.
# Existing context files/paths attached to an existing interpreter script are
# denied; ordinary, empty, unresolved, and `env --` command-argv values pass.
run_hook_matrix_parallel allowed coordinator-environment-context-unresolved \
  "BASH_ENV=ordinary-value bash hooks/tests/test-eci-fast-path.sh" \
  "env BASH_ENV=ordinary-value bash hooks/tests/test-eci-fast-path.sh" \
  "BASH_ENV=/tmp/eci-missing-startup-file bash hooks/tests/test-eci-fast-path.sh" \
  "env BASH_ENV=/tmp/eci-missing-startup-file bash hooks/tests/test-eci-fast-path.sh" \
  "env -- BASH_ENV=/etc/profile bash hooks/tests/test-eci-fast-path.sh"
coordinator_environment_effect_output="$(run_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_ENVIRONMENT_CONTEXT_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=environment-boundary")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=interpreter-context")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=BASH_ENV"))
' "$coordinator_environment_effect_output" >/dev/null
[ ! -e "$TMP_ROOT/bash-env-ran" ]

run_matrix_parallel worker assert_subagent_lifecycle_denied worker-environment-lifecycle \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active nested-enter 1 2 t00-session" \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active nested-accept" \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active nested-exit" \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active manifest-write $proof_root/t00-session/eci-required-critics.json.source" \
  "env -u ECI_ROLE command bash $subagent_eci_home/bin/eci-active nested-exit"

# A copied lifecycle binary is an ordinary executable until its visible
# lifecycle mutation is resolved. Read-only/no-verb invocations do not prove
# a control-state effect; a copied mutation target is still rejected by the
# concrete lifecycle-target check.
copied_eci="$TMP_ROOT/eci-active-copy"
copied_worker="$TMP_ROOT/worker-script"
cp -- "$ROOT/bin/eci-active" "$copied_eci"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$copied_worker"
chmod +x "$copied_eci" "$copied_worker"
run_subagent_matrix_parallel allowed copied-lifecycle-read \
  "env $copied_eci" \
  "timeout 5 $copied_eci" \
  "$copied_eci status" \
  "env FOO=bar $copied_eci status" \
  "env -- $copied_eci status" \
  "env -i $copied_eci status" \
  "env -u PATH $copied_eci status" \
  "stdbuf -oL $copied_eci status" \
  "busybox -- $copied_eci status" \
  "prlimit --nofile=1024 $copied_eci status" \
  "chronic $copied_eci status"
run_matrix_parallel worker assert_copied_lifecycle_denied copied-lifecycle-mutation \
  "$copied_eci nested-exit" \
  "env FOO=bar $copied_eci nested-exit" \
  "env -- $copied_eci nested-exit" \
  "env -i $copied_eci nested-exit" \
  "env -u PATH $copied_eci nested-exit" \
  "stdbuf -oL $copied_eci nested-exit" \
  "busybox -- $copied_eci nested-exit" \
  "prlimit --nofile=1024 $copied_eci nested-exit" \
  "chronic $copied_eci nested-exit"
run_subagent_matrix_parallel allowed copied-worker-direct \
  "$copied_worker" \
  "stdbuf -oL $copied_worker" \
  "busybox -- $copied_worker" \
  "chronic $copied_worker"
run_subagent_matrix_parallel allowed copied-worker-wrapped \
  "env FOO=bar $copied_worker" \
  "env -- $copied_worker" \
  "prlimit --nofile=1024 $copied_worker"

# Shell context changes are not an ECI effect until they resolve a concrete
# lifecycle/control invocation.  Ordinary exports, hash aliases, and unknown
# PATH-selected helpers remain transparent; a PATH that resolves bare
# eci-active to the canonical lifecycle binary is still owned by the
# coordinator through the normal lifecycle route.
run_subagent_matrix_parallel allowed transparent-worker-context \
  "export PATH=$TMP_ROOT" \
  "hash -p $copied_worker eci-active" \
  "PATH=$TMP_ROOT eci-unknown-helper"
run_matrix_parallel worker assert_subagent_lifecycle_denied concrete-worker-context \
  "PATH=$ROOT/bin eci-active off"
run_subagent_matrix_parallel allowed gitleaks-basic \
  "gitleaks detect -r"
# Ordinary evidence outputs belong to the active worker's proof directory;
# their output option is not itself a control-state mutation. Control-path
# names (for example high_level_log.jsonl/anchor or live markers) remain covered
# by the target-aware ownership tests above and in the planner unit suite.
run_subagent_matrix_parallel allowed worker-evidence-output \
  "gitleaks detect --report-path $proof_root/t00-session/report.json" \
  "gitleaks detect --report-path=$proof_root/t00-session/report.json" \
  "diff --to-file $proof_root/t00-session/diff.out $ROOT/hooks/validate-bash.sh" \
  "sort -o $proof_root/t00-session/sort.out $ROOT/hooks/validate-bash.sh"

# A renamed or arbitrary executable is not a lifecycle capability merely
# because its basename or arguments resemble a lifecycle route.  The hook
# must inspect the resolved effect; this fixture has no control-state effect.
renamed_eci="$subagent_eci_home/bin/eci-stage"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$renamed_eci"
chmod +x "$renamed_eci"
run_matrix_parallel worker assert_allowed renamed-lifecycle \
  "eci-stage ledger-append --json '{\"event\":\"decision\",\"summary\":\"one-line-entry\"}'" \
  "eci-stage nested-enter 1 2 t00-session"

# eval is inspected for concrete lifecycle/control or acceptance effects; an
# ordinary literal payload is transparent.
run_subagent_matrix_parallel denied worker-eval-indirection \
  "eval '$subagent_eci_home/bin/eci-active ledger-append --json \"{\\\"event\\\":\\\"decision\\\",\\\"summary\\\":\\\"one-line-entry\\\"}\"'" \
  "eval '$subagent_eci_home/bin/eci-active nested-enter 1 2 t00-session'" \
  "eval 'git rebase topic'"
run_subagent_matrix_parallel allowed worker-eval-transparent \
  "eval 'printf ordinary'" \
  "eval 'git status'"

# Sourcing an unknown script has no resolved ECI effect. Canonical lifecycle
# and control paths remain covered by their dedicated effect checks.
run_subagent_matrix_parallel allowed worker-source-indirection \
  "source /tmp/eci-escape.sh" \
  ". /tmp/eci-escape.sh"

# A wrapper without a child has no effect for ECI to own; the utility itself
# will report its usage/error.  Keep malformed syntax out of the permission
# boundary.  A visible Git acceptance mutation still stays coordinator-owned
# beneath a complete wrapper, rather than becoming an ordinary script route.
run_matrix_parallel worker assert_allowed transparent-wrapper-scripts \
  "env FOO=bar" \
  "env --" \
  "env FOO=bar timeout 5" \
  "exec" \
  "nohup" \
  "setsid" \
  "sudo" \
  "doas" \
  "systemd-run --unit eci" \
  "timeout 5" \
  "time" \
  "nice" \
  "prlimit --cpu=1"
run_matrix_parallel worker assert_worker_wrapped_git_ownership_denied transparent-wrapper-git \
  "env FOO=bar git rebase topic" \
  "env -- git rebase topic" \
  "env FOO=bar timeout 5 git rebase topic" \
  "exec git rebase topic" \
  "nohup git rebase topic" \
  "setsid git rebase topic" \
  "sudo git rebase topic" \
  "doas git rebase topic" \
  "systemd-run --unit eci git rebase topic" \
  "timeout 5 git rebase topic" \
  "time git rebase topic" \
  "nice git rebase topic" \
  "prlimit --cpu=1 git rebase topic"

# A direct ordinary path remains distinct from an env-wrapped launcher.
assert_allowed "/var/eci-escape.sh" run_subagent_hook
assert_allowed "./worker-wrapper-probe" run_subagent_hook
run_matrix_parallel worker assert_worker_wrapped_git_ownership_denied single-wrapped-worker-git \
  "env FOO=bar git rebase topic"

# chronic is transparent when its script target is unresolved; a concrete
# acceptance-sensitive Git child still reaches the worker Git ownership route.
run_matrix_parallel worker assert_chronic_worker_route_case chronic-worker-routes \
  "chronic /tmp/eci-escape.sh" \
  "chronic git rebase topic"

# Lifecycle ownership is based on the visible mutation verb, not successful
# CLI arity.  Extra arguments and `on` must not become worker escape routes.
run_matrix_parallel worker assert_subagent_lifecycle_denied worker-visible-lifecycle-arity \
  "env -u ECI_ROLE $subagent_eci_home/bin/eci-active on worker-scope extra" \
  "env -u ECI_ROLE $subagent_eci_home/bin/eci-active aggregate-stage repo-a -- tracked.txt" \
  "source $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md extra" \
  "env -u ECI_ROLE bash -c 'source $subagent_eci_home/bin/eci-active on worker-scope extra'"

# Producers make their own checkpoints, including wrapped ordinary commits.
run_matrix_parallel worker assert_allowed worker-git-commit \
  "git commit -qm --amend" \
  "env -u ECI_ROLE git commit -qm --dry-run"
run_matrix_parallel worker assert_worker_dynamic_interpreter_or_git_denied worker-git-interpreter \
  "bash -c 'git rebase topic'"

# Unsupported shell launchers must not hide lifecycle mutation from the
# subagent ownership gate. These strings are parsed as control commands, not
# executed by this regression.
run_matrix_parallel worker assert_subagent_lifecycle_denied worker-shell-lifecycle \
  "source $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md" \
  ". $subagent_eci_home/bin/eci-active wait $TMP_ROOT/eci_user_owned_wait.md" \
  "$subagent_eci_home/bin/eci-active ledger-append --json '{\"event\":\"decision\",\"summary\":\"one-line-entry\"}'" \
  "source bin/eci-active wait $TMP_ROOT/eci_user_owned_wait.md" \
  "env -u ECI_ROLE bash -c 'source bin/eci-active wait $TMP_ROOT/eci_user_owned_wait.md'" \
  "env -S '$subagent_eci_home/bin/eci-active resume aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'" \
  "xargs -n 1 $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md"
output="$(run_subagent_hook "find . -exec $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md \\;")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=-exec")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-find-action"))
' "$output" >/dev/null

# Lifecycle identity does not make dynamic shell indirection admissible.
# Coordinators use the canonical executable directly rather than source or
# inline-code launchers.
for dynamic_lifecycle in \
  "source ./bin/eci-active off $TMP_ROOT/disengage.md" \
  "env -u ECI_ROLE bash -c 'source $ROOT/bin/eci-active wait $TMP_ROOT/eci_user_owned_wait.md'"; do
  output="$(run_hook "$dynamic_lifecycle")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=source") or contains("token=-c")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-launch") or contains("predicate=dynamic-interpreter-launch"))
  ' "$output" >/dev/null
done

assert_subagent_control_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("phase=PreToolUse")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null
}

assert_worker_branch_remote_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'worker branch/remote denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_instruction_read_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output path failure
  path="${command#cat }"
  case "$path" in
    *"/skills/missing/SKILL.md") failure="missing-instruction-source" ;;
    *"/external/skills/escape/SKILL.md") failure="outside-instruction-root" ;;
    *"/skills/escape/SKILL.md") failure="symlink-escape" ;;
    *"/skills/test/not-a-source.fifo") failure="not-regular-file" ;;
    *)
      printf 'unknown instruction fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
  output="$("$runner" "$command")"
  jq -e --arg token "$path" --arg failure "$failure" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_INSTRUCTION_READ_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-instruction-read")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("token=" + $token))) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("failure=" + $failure))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("resolved=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("instruction_root=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'instruction-read denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_diagnostic_denied() {
  local command="$1" runner="${2:-run_subagent_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'diagnostic denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

assert_git_environment_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_SYNTAX_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=GIT_DIR=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=leading-assignment"))
  ' "$output" >/dev/null || {
    printf 'Git environment denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

# Workers may edit repository files, but cannot mutate proof-root control
# records through generic shell/file utilities or a nested shell payload.
run_matrix_parallel worker assert_subagent_control_denied worker-control-mutation \
  "rm -f $proof_root/t00-session/eci_active" \
  "unlink $proof_root/t00-session/eci_active" \
  "shred -f $proof_root/t00-session/eci_active" \
  "find $proof_root -name eci_active -delete" \
  "env -u ECI_ROLE rm -f $proof_root/t00-session/eci_wait" \
  "printf forged > $proof_root/t00-session/eci_wait" \
  "printf forged > $proof_root/t00-session/high_level_log.anchor" \
  "cp $ROOT/hooks/validate-bash.sh $proof_root/t00-session/eci-required-critics.json" \
  "bash -c 'printf forged > $proof_root/t00-session/eci-teardown-complete'" \
  "touch $proof_root/t00-session/eci-acceptance-anchor" \
  "command rm -f $proof_root/t00-session/eci_active" \
  "timeout 5 bash -c 'printf forged > $proof_root/t00-session/eci_wait'" \
  "env -S 'printf forged > $proof_root/t00-session/eci-required-critics.json'" \
  "find . -exec rm -f $proof_root/t00-session/eci-teardown-complete \\;" \
  "dd if=/dev/null of=$proof_root/t00-session/eci_wait" \
  "touch $proof_root/t00-session/eci-required-critics.json" \
  "touch $proof_root/t00-session/eci-required-critics.commit.1.ledger" \
  "touch $proof_root/t00-session/eci-critic-identities.ledger" \
  "touch $proof_root/t00-session/eci-acceptance-anchor" \
  "touch $proof_root/t00-session/eci-acceptance-transaction" \
  "touch $proof_root/t00-session/eci-teardown-complete" \
  "touch $proof_root/t00-session/baseline_head" \
  "rsync $ROOT/hooks/validate-bash.sh $proof_root/t00-session/eci_wait" \
  "rsync --log-file=$proof_root/t00-session/eci_wait $ROOT/hooks/validate-bash.sh $TMP_ROOT/worker-copy" \
  "rsync --batch-file=$proof_root/t00-session/eci_wait $ROOT/hooks/validate-bash.sh $TMP_ROOT/worker-copy" \
  "dd of=$proof_root/t00-session/eci_wait if=/dev/null" \
  "dd if=/dev/null of=$proof_root/t00-session/eci_wait"

# Lexical control paths remain protected even when their final component is a
# symlink resolving outside the proof root.
symlink_target="$TMP_ROOT/external-control"
printf forged >"$symlink_target"
ln -s "$symlink_target" "$proof_root/t00-session/eci_wait-link"
run_matrix_parallel worker assert_subagent_control_denied worker-control-symlink \
  "rm -f $proof_root/t00-session/eci_wait-link" \
  "printf forged > $proof_root/t00-session/eci_wait-link"

# Tilde expansion must resolve against the active proof-root home, not evade
# the path-aware control-file mutation check.
tilde_home="$TMP_ROOT/tilde-home"
tilde_root="$tilde_home/tmp/agent/proof"
mkdir -p "$tilde_root/t00-session"
printf '%s\n' \
  'scope: tilde-path mutation probe' \
  "cwd: $ROOT" \
  'session_id: t00-session' \
  'created_utc: 2026-08-15T00:00:00Z' \
  >"$tilde_root/t00-session/eci_active"
output="$(run_subagent_hook_at_root "rm -f ~/tmp/agent/proof/t00-session/eci_active" "$tilde_root" "$tilde_home")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("resolved="))
' "$output" >/dev/null

# The cache parent may itself be symlinked.  Canonicalize the candidate before
# applying the proof-root control-path check so this deployment layout cannot
# evade the worker mutation boundary.
symlink_home="$TMP_ROOT/symlink-home"
symlink_target="$TMP_ROOT/symlink-target"
mkdir -p "$symlink_home" "$symlink_target/agent"
ln -s "$symlink_target" "$symlink_home/tmp"
ln -s "$proof_root" "$symlink_target/agent/proof"
output="$(run_subagent_hook_at_root "rm -f ~/tmp/agent/proof/t00-session/eci_active" "$proof_root" "$symlink_home")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("resolved="))
' "$output" >/dev/null
ordinary_worker_file="$TMP_ROOT/ordinary-worker-file"
output="$(run_subagent_hook "printf ordinary > $ordinary_worker_file")"
# A worker-owned ordinary file write is legitimate task work. Redirection
# syntax alone is not a control-state effect and must not trigger a denial.
[ ! -s "$output" ]

# Branch/remote commands resolve to concrete, targeted repository effects.
# Coordinators own ordinary targeted Git actions, so these remain transparent
# and do not require an approval artifact, receipt, or special route. Workers
# still hit the repository-ownership boundary below.
run_hook_matrix_parallel allowed coordinator-branch-remote \
  "git branch -d doomed" \
  "git branch feature" \
  "git branch --set-upstream-to=origin/main" \
  "git remote add origin https://example.invalid/repo.git" \
  "git remote set-url origin https://example.invalid/repo.git"
run_matrix_parallel worker assert_worker_branch_remote_denied worker-branch-remote \
  "git branch --set-upstream-to=origin/main" \
  "git branch feature" \
  "git remote set-url origin https://example.invalid/repo.git"
# Active raw Git archive always defers to the provider's legacy route. The
# provider retains the final worker/coordinator decision and checks output
# targets; the compiled planner must not grant direct archive admission.
assert_unknown "git archive HEAD"
assert_unknown "git archive HEAD" run_subagent_hook
run_hook_matrix_parallel unknown coordinator-git-archive-fast-path-unsafe \
  "git archive --format=tar --output=$proof_root/t00-session/archive.tar HEAD" \
  "env git archive HEAD" \
  "env FOO=bar git archive HEAD" \
  "env LD_PRELOAD=/tmp/libevil.so git archive HEAD" \
  "env LD_AUDIT=/tmp/libevil.so git archive HEAD"

# Direct, fake-PATH, inherited-context, path-qualified, compound, repository,
# remote, exec, and other archive shapes all reach the legacy route.
run_archive_legacy_matrix_parallel \
  direct "git archive HEAD" "$ROOT/bin:$PATH" - \
  fake-path "git archive HEAD" "$fake_bin:$ROOT/bin:$PATH" - \
  inherited-git-context "git archive HEAD" "$ROOT/bin:$PATH" "$TMP_ROOT/inherited-git-dir" \
  direct-path "$fake_bin/git archive HEAD" "$ROOT/bin:$PATH" - \
  direct-path-no-pager "$fake_bin/git --no-pager archive HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-compound "git archive HEAD && printf after" "$ROOT/bin:$PATH" - \
  archive-shape-repository "git -C $ROOT archive HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-remote-eq "git archive --remote=origin HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-remote "git archive --remote origin HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-exec-eq "git archive --exec=git-upload-archive HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-exec "git archive --exec git-upload-archive HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-output "git archive --output=$proof_root/t00-session/archive.tar HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-zip "git archive --format=zip --output=$proof_root/t00-session/archive.zip HEAD" "$ROOT/bin:$PATH" - \
  archive-shape-readme "git archive --format=tar --output=$proof_root/t00-session/archive.tar HEAD README" "$ROOT/bin:$PATH" -
assert_unknown "git -c core.pager=cat archive HEAD"
archive_control_output="$(run_subagent_hook "git archive --output=$proof_root/t00-session/eci_active HEAD")"
jq -e --arg target "$proof_root/t00-session/eci_active" '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains($target)) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:")) and
  (
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-control"))
    or
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_LIVE_CONTROL_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-live-control"))
  )
' "$archive_control_output" >/dev/null

# Explorers may inspect project, skill, Git, and bounded proof metadata without
# being mistaken for worker launchers.  These are literal read-only forms;
# shell indirection, redirects, and proof-state writes remain covered below.
run_subagent_matrix_parallel allowed explorer-project-reads \
  "sed -n '1p' AGENTS.md" \
  "sed -n '1p' ../skills/explore-critique-implement/SKILL.md" \
  "printenv PATH" \
  "rg --files -g '*.md' ." \
  "find . -maxdepth 1 -type f -print"
# Workers may inspect coordinator handoff documents and current control state
# to keep their work aligned. Redirects and concrete writes still take the
# target-aware control-path route below.
run_subagent_matrix_parallel allowed worker-handoff-reads \
  "cat $instructions_file" \
  "sed -n '1p' $instructions_file" \
  "rg -n coordinator $high_level_log"
run_subagent_matrix_parallel allowed worker-current-control-reads \
  "cat $proof_root/t00-session/eci_active" \
  "sed -n '1p' $proof_root/t00-session/eci_active"
# Workers must be able to load canonical provider instructions and installed
# skill resources even when those files are hard-linked elsewhere.  Claimed
# instruction paths that are missing, outside configured roots, or escape
# through a symlink fail with their exact path-ownership reason.
run_subagent_matrix_parallel allowed worker-instruction-reads \
  "wc -l $ROOT/AGENTS.md $ROOT/../skills/explore-critique-implement/SKILL.md $ROOT/../skills/writing-status-reports/SKILL.md $ROOT/skills/harness-tuning/SKILL.md" \
  "wc -l $subagent_eci_home/AGENTS.md $subagent_eci_home/AGENTS.md $subagent_eci_home/skills/test/SKILL.md" \
  "rg -n worker $subagent_eci_home/skills/test" \
  "git log --format=%H -- skills/writing-status-reports/SKILL.md AGENTS.md"
# A repository-default Git history pathspec names history, not a current
# worktree read. The missing path must remain admissible only through the
# exact compiled history capability; current-state and escaping near-misses
# remain on the instruction-source guard.
[ ! -e "$ROOT/AGENTS.md" ] || {
  printf 'expected missing current instruction fixture: %s\n' "$ROOT/AGENTS.md" >&2
  exit 1
}
run_subagent_matrix_parallel allowed worker-git-history-missing-current-path \
  "git log --format=%H -- AGENTS.md"
for worker_git_history_near_miss in \
  "git diff --check -- AGENTS.md" \
  "git log --format=%H -- ../AGENTS.md"; do
  worker_git_history_output="$(run_subagent_hook "$worker_git_history_near_miss")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_INSTRUCTION_READ_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-instruction-read")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$worker_git_history_output" >/dev/null || {
    printf 'Git history pathspec near-miss denial mismatch: command=%q output=%s\n' \
      "$worker_git_history_near_miss" "$worker_git_history_output" >&2
    [ ! -e "$worker_git_history_output" ] || cat -- "$worker_git_history_output" >&2
    exit 1
  }
done
run_matrix_parallel worker assert_instruction_read_denied worker-instruction-failures \
  "cat $subagent_eci_home/skills/missing/SKILL.md" \
  "cat $external_skill_root/SKILL.md" \
  "cat $subagent_eci_home/skills/escape/SKILL.md" \
  "cat $subagent_eci_home/skills/test/not-a-source.fifo"
run_matrix_parallel worker assert_diagnostic_denied worker-protected-git \
  "git commit --amend -m checkpoint" \
  "git config user.name worker" \
  "git reset --hard HEAD" \
  "git checkout -- hooks/validate-bash.sh" \
  "git worktree add /tmp/eci-worker-tree HEAD"
# Resolved read-only Git inspection is ordinary worker project work. Protected Git
# mutations and executable-helper options are rejected by their own ownership
# recognizers rather than by a read-command allowlist.
run_hook_matrix_parallel allowed coordinator-git-inspection \
  "git status --short" \
  "git status --short --branch" \
  "git submodule status" \
  "git diff --stat" \
  "git log --format=%H -- skills/coding-go/SKILL.md AGENTS.md" \
  "git status" \
  "git diff --check" \
  "git show" \
  "git ls-files" \
  "git log -1 --oneline" \
  "git branch --all --contains HEAD" \
  "git branch --list 'main*'" \
  "git branch --contains HEAD"
run_subagent_matrix_parallel allowed worker-git-inspection \
  "git status --short" \
  "git --git-dir=.git status --short" \
  "git -C $ROOT status --short" \
  "git status --short --branch" \
  "git submodule status" \
  "git diff --stat" \
  "git log --format=%H -- skills/coding-go/SKILL.md AGENTS.md" \
  "git status" \
  "git diff --check" \
  "git show" \
  "git ls-files" \
  "git log -1 --oneline" \
  "git branch --all --contains HEAD" \
  "git branch --list 'main*'" \
  "git branch --contains HEAD" \
  "systemd-run --working-directory=. --setenv=GIT_DIR=.git --setenv=GIT_WORK_TREE=. git status --short"
worker_git_chain_output="$(run_subagent_hook 'git status --short && git submodule status && git diff --stat')"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COMMAND_NONLITERAL_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=direct-argv")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operator/token=&&")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
' "$worker_git_chain_output" >/dev/null
run_hook_matrix_parallel allowed coordinator-environment-allowed \
  "printenv PATH" \
  "printenv PATH PWD" \
  "printenv ECI_UNREGISTERED_TEST_VALUE" \
  "printenv PATH ECI_UNREGISTERED_TEST_VALUE" \
  "rg -n 'env | sort' hooks/validate-bash.sh"
run_subagent_matrix_parallel allowed worker-environment-allowed \
  "printenv PATH" \
  "printenv PATH PWD" \
  "printenv ECI_UNREGISTERED_TEST_VALUE" \
  "printenv PATH ECI_UNREGISTERED_TEST_VALUE" \
  "rg -n 'env | sort' hooks/validate-bash.sh"
inherited_node_options_output="$TMP_ROOT/inherited-node-options-output"
jq -cn --arg cwd "$ROOT" --arg command 'node literal.js' \
  '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
  NODE_OPTIONS=--require=/dev/null ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
    bash "$classifier_hook_fixture" >"$inherited_node_options_output"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_ENVIRONMENT_CONTEXT_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=environment-boundary")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=NODE_OPTIONS")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=inherited-environment-context")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
' "$inherited_node_options_output" >/dev/null
# Coordinator env spelling is not a command-admission boundary. The hook does
# not execute these commands or read their output; any real output path is
# responsible for redaction. A later concrete Git target remains covered
# separately below.
run_hook_matrix_parallel allowed coordinator-environment-ordinary \
  "env" \
  "env | sort" \
  "env | sort | rg '^PATH='" \
  "printenv" \
  "printenv PATH PATH" \
  "printenv PATH=bad" \
  "printenv -- PATH" \
  "env FOO=bar" \
  "env -S 'novel-tool'" \
  "env -u" \
  "env --unknown novel-tool" \
  "env --unset= novel-tool" \
  "printenv PATH | env" \
  "env BASH_ENV=ordinary-value bash script.sh"
assert_coordinator_git_environment_context_denied "env GIT_DIR=/tmp/other git status"
run_matrix_parallel worker assert_worker_environment_case worker-environment-denied \
  "env" \
  "env | sort" \
  "env | sort | rg '^PATH='" \
  "printenv" \
  "printenv PATH PATH" \
  "printenv PATH=bad" \
  "printenv -- PATH" \
  "env FOO=bar" \
  "env -S 'novel-tool'" \
  "env -u" \
  "env --unknown novel-tool" \
  "env --unset= novel-tool" \
  "printenv PATH | env" \
  "env GIT_DIR=eci-private-git-value git status"
run_matrix_parallel coordinator assert_environment_broad_denied coordinator-environment-broad \
  "env FOO=bar rm -rf /"
run_matrix_parallel worker assert_environment_broad_denied worker-environment-broad \
  "env FOO=bar rm -rf /"
run_hook_matrix_parallel allowed coordinator-novel-environment \
  "env FOO=bar novel-tool --flag value" \
  "env -i novel-tool" \
  "env -u FOO novel-tool" \
  "env -- novel-tool --flag value" \
  'env "novel-tool" --flag value'
# Finite environment prefixes do not create an ownership boundary by
# themselves; ordinary non-worker tools remain admitted for active workers.
run_subagent_matrix_parallel allowed worker-novel-environment \
  "env FOO=bar novel-tool --flag value" \
  "env -- novel-tool --flag value" \
  "env -i novel-tool" \
  "env -u FOO novel-tool"
# An unresolved startup-context value or missing script has no resolved
# execution effect. It remains ordinary worker work; only the concrete probe
# above (an existing context file attached to an existing script) is denied.
run_subagent_matrix_parallel allowed worker-environment-context-unresolved \
  "env BASH_ENV=eci-private-bash-value bash script.sh" \
  "BASH_ENV=eci-private-bash-value bash script.sh"
worker_unknown_output="$(run_subagent_hook "unrecognized-worker-command")"
[ ! -s "$worker_unknown_output" ]

# Transcriptless coordinator-shaped payloads still use the same ownership
# gate.  An ordinary finite read outside the repository is not a protected
# ECI capability and must not require executable- or path-name allowlisting.
transcriptless_outside_output="$(run_hook "cat /etc/passwd")"
[ ! -s "$transcriptless_outside_output" ]


# Read-only batches may contain only finite semicolon/pipeline segments.  The
# active coordinator path admits these segments, while ambiguous operators and
# wrappers remain compiler-diagnostic denials.
reviewed_script_batch="bash hooks/tests/test-eci-fast-path.sh && bash hooks/tests/test-eci-post-compact-refresh.sh"
assert_allowed "$reviewed_script_batch"
run_hook_matrix_parallel allowed coordinator-reviewed-batches \
  "git status --short || true" \
  "git status --short --branch && git diff --stat" \
  "rg -n 'ECI' $ROOT/hooks/validate-bash.sh | head -n 5" \
  "git diff -U8 -- hooks/validate-bash.sh hooks/tests/test-validate-bash-classifier.sh hooks/tests/test-eci-command-syntax-gating.sh | rg -n -C 18 'coordinator_compound_inspection_route|eci_finite_literal_argv|WORKER_COMMAND|operator/token=|adb devices|rev-parse|git status --short --branch'" \
  "ps -eo pid,ppid,etimes,stat,args | rg 'test-validate-bash|test-eci-command-syntax|test-pretooluse-latency|validate-bash.sh|worker_stat_root_cause|eci exec'" \
  "printf '%s\\n' coordinator ; git -C $ROOT status --short ; rg -n 'ECI' hooks/validate-bash.sh | head -n 5" \
  "ls -la $ROOT | wc -l" \
  "git status --short ; git diff --stat" \
  "rg --files -g '*.md' . | head -n 5" \
  "ls -la $ROOT && wc -l $ROOT/AGENTS.md" \
  "git -C $ROOT log -1 --oneline" \
  "git -C $ROOT diff --check" \
  "git -C $ROOT submodule status" \
  "bash hooks/tests/test-eci-fast-path.sh && rm -f $TMP_ROOT/denied" \
  "git status --short || rm -f $TMP_ROOT/denied" \
  "ls -la $ROOT || wc -l $ROOT/AGENTS.md"
# Each batch child is admitted from its canonical structural identity, not a
# reviewed-byte manifest.  A direct-child readable test script remains safe
# after harmless content changes or when it was not in a former digest list.
structural_script_batch="bash hooks/tests/test-eci-fast-path.sh && bash hooks/tests/test-eci-command-plan.sh"
assert_allowed "$structural_script_batch"
run_hook_matrix_parallel allowed coordinator-structural-batch \
  "$structural_script_batch"
run_hook_matrix_parallel allowed coordinator-structural-diagnostics \
  "bash -n hooks/tests/test-eci-review-gate.sh" \
  "bash -x hooks/tests/test-eci-review-gate.sh" \
  "bash -x hooks/tests/test-eci-review-gate.sh 2>&1 | tail -n 200"
run_hook_matrix_parallel allowed coordinator-read-only-syntax-compounds \
  "bash hooks/tests/test-eci-fast-path.sh && bash -n hooks/eci-active-gate.sh" \
  "bash hooks/tests/test-eci-command-plan.sh && bash -n hooks/eci-active-gate.sh"
run_hook_matrix_parallel unknown coordinator-rejected-batches \
  "bash hooks/tests/test-eci-fast-path.sh && bash -c 'true'" \
  "bash -x hooks/tests/test-eci-review-gate.sh 2>&1 | cat" \
  "bash -x hooks/tests/test-eci-review-gate.sh > $TMP_ROOT/trace" \
  "find -P $ROOT -maxdepth 1 -name AGENTS.md -o -name AGENTS.md 2>/dev/null | head -n 5" \
  "ls -la $ROOT > $TMP_ROOT/read-output"
run_hook_matrix_parallel unknown coordinator-transparent-shell-wrappers \
  "stdbuf -oL bash arbitrary-script.sh" \
  "busybox sh arbitrary-script.sh" \
  "chronic bash hooks/tests/test-eci-fast-path.sh"
# Environment enumeration is also transparent for a coordinator. It is not a
# source-write, control-file, cross-session, or destructive target.
run_hook_matrix_parallel allowed coordinator-environment-batches \
  "env" \
  "env | sort" \
  "env | sort | rg '^ECI_UNREGISTERED_TEST_VALUE='" \
  "env | sort | rg '^(PATH|ECI_UNREGISTERED_TEST_VALUE)='"
run_hook_matrix_parallel allowed coordinator-branch-remote-inspection \
  "git branch --show-current" \
  "git remote -v"
run_subagent_matrix_parallel allowed worker-branch-inspection-options \
  "git branch --abbrev=12 --column=always --color=always --list 'release/*'" \
  "git branch -l 'release/*'" \
  "git branch --contains HEAD --format '%(refname)' --sort committerdate"
run_hook_matrix_parallel allowed coordinator-branch-remote-tail \
  "git branch -d doomed" \
  "git remote add origin https://example.invalid/repo.git" \
  "git remote show origin"
run_matrix_parallel worker assert_worker_branch_remote_denied worker-branch-remote-tail \
  "git branch -d doomed" \
  "git branch --abbrev feature" \
  "git branch --column feature" \
  "git branch --color feature" \
  "git branch --format --delete" \
  "git remote add origin https://example.invalid/repo.git"

run_matrix_parallel coordinator assert_eci_lifecycle_spelling_denied coordinator-lifecycle-location-denied \
  "${HOME:?}/tmp/eci-active off $TMP_ROOT/disengage.md" \
  "PATH=/tmp eci-active off $TMP_ROOT/disengage.md" \
  "ECI_HOME=/tmp eci-active off $TMP_ROOT/disengage.md" \
  "env PATH=/tmp eci-active off $TMP_ROOT/disengage.md" \
  "env -i eci-active off $TMP_ROOT/disengage.md" \
  "env -u PATH eci-active off $TMP_ROOT/disengage.md" \
  "command -p eci-active off $TMP_ROOT/disengage.md"
# The provider dispatcher is denial-only for every active ECI role.  A
# worker must not turn its direct, bare, or bounded-env spelling into an
# ordinary finite command merely because only eci-active has a positive route.
run_matrix_parallel worker assert_eci_lifecycle_spelling_denied worker-dispatcher-lifecycle-lookalikes \
  '$HOME/.omp/agent/eci/bin/eci-active-dispatch --help' \
  'eci-active-dispatch --help' \
  'env -- ECI_SESSION_ID=t00-session "$HOME/.omp/agent/eci/bin/eci-active-dispatch" --help'
run_hook_matrix_parallel unknown coordinator-git-context-unknown \
  "git -c user.name=test commit" \
  "git -c diff.external=/tmp/evil diff" \
  "git diff --textconv" \
  "git diff --ext-diff" \
  "git -c core.pager=cat status --short" \
  "git commit --git-dir /tmp/other.git" \
  "git commit --work-tree /tmp/other" \
  "git commit --exec-path /tmp/other" \
  "git --config-env user.name=GIT_NAME commit" \
  "git --exec-path /tmp commit" \
  "git --namespace test commit" \
  "env git commit" \
  "timeout 5 git commit" \
  "systemd-run --unit eci git commit" \
  "xargs git commit"
run_hook_matrix_parallel allowed coordinator-git-read-only-finite-controls \
  "git status --short" \
  "git log -1" \
  "git diff --check" \
  "git grep -n needle -- hooks" \
  "git ls-files" \
  "git rev-parse HEAD" \
  "git branch --all --contains HEAD"
run_hook_matrix_parallel unknown coordinator-git-read-only-execution-escapes \
  "git grep --open-files-in-pager=/bin/sh needle" \
  "git grep --open-files-in-pager /bin/sh needle" \
  "git grep -O /bin/sh needle" \
  "git cat-file --filters HEAD:README" \
  "git log --show-signature -1" \
  "git --paginate log -1" \
  "git diff --check --no-index /etc/passwd /etc/hosts"
run_matrix_parallel coordinator assert_commit coordinator-git-commit \
  "git commit -m 'bounded message'" \
  "git commit --message=bounded" \
  "git commit -a --amend --no-verify --signoff" \
  "git commit --amend" \
  "git commit -c prior-message"
run_hook_matrix_parallel unknown coordinator-git-environment-context \
  "env GIT_EXTERNAL_DIFF=/tmp/evil git diff --stat" \
  "env GIT_DIR=/tmp/other.git git status --short" \
  "GIT_EXTERNAL_DIFF=/tmp/evil git diff --stat"
assert_unknown ""
# A quoted search pattern containing the words `go test` is not an invocation
# of the Go test executable and must remain on the read-only path.
run_hook_matrix_parallel allowed coordinator-search-inspection \
  "rg -n 'go test' hooks/validate-bash.sh" \
  "rg -n -i 'go test' hooks/validate-bash.sh" \
  "rg -n -i subagent $kimi_root/hooks $kimi_root/bin" \
  "git grep -n -i subagent -- hooks bin" \
  "stat -c '%d:%i %a %h %n' hooks/pre-commit-go-mod.sh $kimi_root/hooks/pre-commit-go-mod.sh" \
  "rg -c 'run_hook|run_subagent_hook|assert_' hooks/tests/test-validate-bash-classifier.sh"
quoted_backtick_pattern="rg -n 'literal \` text' hooks/validate-bash.sh"
run_hook_matrix_parallel allowed coordinator-literal-patterns \
  "$quoted_backtick_pattern" \
  "rg -n '...{64}...' hooks/validate-bash.sh" \
  'rg -n "...{64}..." hooks/validate-bash.sh' \
  "stat -c '%y %n' hooks/validate-bash.sh"
# The compiled plan is authoritative for a finite direct shell-script argv
# that names a normal relative path absent from the checkout.  It must reach
# the status-0 fast path; execution, rather than the hook, reports a missing
# script.  A sibling and an extra script argument remain finite planner grants.
for planner_approved_missing_shell in \
  "bash hooks/tests/test-not-allowlisted.sh" \
  "bash hooks/tests/test-not-allowlisted-sibling.sh" \
  "bash hooks/tests/test-not-allowlisted.sh extra"; do
  assert_planner_allows "$planner_approved_missing_shell"
done
run_hook_matrix_parallel allowed coordinator-planner-approved-missing-shell \
  "bash hooks/tests/test-not-allowlisted.sh" \
  "bash hooks/tests/test-not-allowlisted-sibling.sh" \
  "bash hooks/tests/test-not-allowlisted.sh extra" \
  "sed -n '1p' $high_level_log" \
  "sed --quiet '1,2p' $high_level_log"
# An existing in-root script remains ordinary work even when its route has no
# special parser metadata; only a concrete control/target effect is gated.
assert_allowed "bash hooks/tests/test-eci-command-plan.sh"
run_hook_matrix_parallel unknown coordinator-shell-launcher-near-misses \
  "env FOO=bar bash hooks/tests/test-not-allowlisted.sh" \
  "bash -c 'true'"
# The coordinator inspection route admits bounded read-only inspection of
# approved repository sources as well as proof-root evidence.
run_hook_matrix_parallel allowed coordinator-source-read \
  "sed -n '1p' $ROOT/hooks/validate-bash.sh"
run_matrix_parallel coordinator assert_ledger_append_only_denied_matrix coordinator-ledger-write \
  "sed -i '1p' $high_level_log" \
  "sed -i '1p' $proof_root/t00-session/high_level_log.anchor"
run_hook_matrix_parallel allowed coordinator-sed-shapes \
  "sed -n '1d' $high_level_log" \
  "sed -n '1p' $high_level_log $high_level_log" \
  "sed -n '1p' -" \
  "sed -n '1p' $proof_root/t00-session/../t00-session/high_level_log.jsonl"
assert_allowed "awk '{print 1}' $ROOT/hooks/validate-bash.sh"
parameter_output="$(run_hook 'printf "%s" "$UNTRUSTED_COMMAND"')"
jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_SYNTAX_DENIED]")) and (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-expansion"))' "$parameter_output" >/dev/null
# Preserve the multiline parser regression without sending its cleanup target
# through the prohibited system temporary root. The companion assertion proves
# that system-root diagnosis remains ahead of this otherwise finite plan.
assert_allowed $'cat /dev/null\nrm -f '"$classifier_tmp_parent"'/eci-multiline-marker'
multiline_system_tmp_output="$(run_hook $'cat /dev/null\nrm -f /tmp/eci-multiline-marker')"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_TMPDIR_SYSTEM_ROOT]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=temporary-path")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=/tmp/eci-multiline-marker")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("path=/tmp/eci-multiline-marker")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=system-temporary-root")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
' "$multiline_system_tmp_output" >/dev/null
assert_denied $'sh -c "cat /dev/null\nrm -f /tmp/eci-nested-marker"'
assert_denied $'eval "cat /dev/null\nrm -f /tmp/eci-eval-marker"'

# A finite ordinary redirect has a concrete bounded destination. It is not a
# control-state write merely because shell redirection syntax is used.
run_hook_matrix_parallel allowed coordinator-redirections \
  "cat $ROOT/hooks/validate-bash.sh > $TMP_ROOT/read-output" \
  "cat $ROOT/hooks/validate-bash.sh >> $TMP_ROOT/read-output" \
  "cat < $ROOT/hooks/validate-bash.sh" \
  "cat << EOF" \
  "cat >| $TMP_ROOT/read-output"
run_hook_matrix_parallel allowed coordinator-diff-output \
  "diff -o $TMP_ROOT/diff-output $ROOT/hooks/validate-bash.sh $ROOT/hooks/validate-bash.sh" \
  "diff --output $TMP_ROOT/diff-output $ROOT/hooks/validate-bash.sh $ROOT/hooks/validate-bash.sh" \
  "diff --to-file $TMP_ROOT/diff-output $ROOT/hooks/validate-bash.sh" \
  "sort -o $TMP_ROOT/sort-output $ROOT/hooks/validate-bash.sh" \
  "gitleaks detect -r" \
  "gitleaks detect --report-path $TMP_ROOT/report-output" \
  "gitleaks detect --report-path=$TMP_ROOT/report-output"

# Process substitution and brace spelling do not identify a broad or control
# target here. Keep them transparent; the command is not executed by this
# hook, and a real destructive target would still be checked downstream.
run_hook_matrix_parallel allowed coordinator-process-substitution \
  "cat <(rm -f $TMP_ROOT/process-substitution-marker)" \
  "cat >($TMP_ROOT/process-substitution-marker)" \
  "echo {danger}"
[ ! -e "$TMP_ROOT/process-substitution-marker" ]

# The planner resolves the redirect target and effect. Current-session EOF
# append is role-neutral ordinary work; current rewrites and anchors retain
# their concrete ownership diagnostics without requiring lifecycle preflight.
assert_allowed "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"appended\"}' >> $high_level_log"
assert_allowed "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"appended\"}' 2>> $high_level_log" run_subagent_hook
assert_ledger_redirect_denied \
  "printf '%s\\n' rewritten > $high_level_log" \
  ECI_LEDGER_REWRITE_DENIED effect=overwrite
assert_ledger_redirect_denied \
  "printf '%s\\n' rewritten >| $high_level_log" \
  ECI_LEDGER_REWRITE_DENIED effect=force-overwrite
assert_ledger_redirect_denied \
  "printf '%s\\n' anchor >> $proof_root/t00-session/high_level_log.anchor" \
  ECI_LEDGER_ANCHOR_WRITE_DENIED target=high_level_log.anchor
assert_allowed "printf '%s\\n' ordinary >> ./high_level_log.jsonl"
log_alias="$proof_root/t00-session/high-level-log-alias.md"
ln -s "$high_level_log" "$log_alias"
assert_allowed "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"alias\"}' >> $log_alias"
# A symlink remains a bounded inspection target only when its resolved path
# stays under an approved root.
assert_allowed "sed -n '1p' $log_alias"
outside_log="$TMP_ROOT/outside-high-level-log.md"
printf '%s\n' '# outside proof root' >"$outside_log"
outside_log_alias="$proof_root/t00-session/high-level-log-outside-alias.md"
ln -s "$outside_log" "$outside_log_alias"
outside_alias_output="$(run_hook "sed -n '1p' $outside_log_alias")"
jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PROOF_PATH_ESCAPE_DENIED]")) and (.hookSpecificOutput.permissionDecisionReason | contains("operation=proof-path-ownership"))' "$outside_alias_output" >/dev/null
before_log="$(cat -- "$high_level_log")"
# The real route performs the append and advances its anchor under the
# mutation lock; its output is intentionally not admitted as a raw redirect.
ledger_append_from_marker_cwd "$proof_root" t00-session 'append-route' >/dev/null
append_route_line="$(tail -n 1 -- "$high_level_log")"
jq -e '.schema == "eci-high-level-log/v1" and .event == "decision" and .summary == "append-route" and (.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))' <<<"$append_route_line" >/dev/null

# The lifecycle route preflights the total byte budget before writing. A
# nearly-full anchored log must remain byte-identical when the next entry
# would cross the cap.
cap_session="cap-session"
cap_root="$TMP_ROOT/cap-proof"
cap_dir="$cap_root/$cap_session"
mkdir -p "$cap_dir"
printf '%s\n' \
  'scope: cap probe' \
  "cwd: $ROOT" \
  "session_id: $cap_session" \
  'created_utc: 2026-08-15T00:00:00Z' \
  >"$cap_dir/eci_active"
cap_log="$cap_dir/high_level_log.jsonl"
head -c 1048536 /dev/zero | tr '\0' x | jq -Rsc '{event:"decision",summary:.}' >"$cap_log"
cap_bytes="$(wc -c <"$cap_log")"
cap_hash="$(sha256sum -- "$cap_log" | awk '{print $1}')"
printf '%s\n' \
  'schema: eci-high-level-log-anchor/v1' \
  "session_id: $cap_session" \
  "log_path: $cap_log" \
  "bytes: $cap_bytes" \
  "sha256: $cap_hash" \
  >"$cap_dir/high_level_log.anchor"
cap_anchor_hash="$(sha256sum -- "$cap_dir/high_level_log.anchor" | awk '{print $1}')"
if ledger_append_from_marker_cwd "$cap_root" "$cap_session" '0123456789' >"$TMP_ROOT/cap-append.out" 2>"$TMP_ROOT/cap-append.err"; then
  printf '%s\n' 'oversized ledger append unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'ECI high-level log append would exceed its bounded size limit; refusing before write.' "$TMP_ROOT/cap-append.err"
[ "$(wc -c <"$cap_log")" = "$cap_bytes" ]
[ "$(sha256sum -- "$cap_log" | awk '{print $1}')" = "$cap_hash" ]
[ "$(sha256sum -- "$cap_dir/high_level_log.anchor" | awk '{print $1}')" = "$cap_anchor_hash" ]

# A middle insertion, replacement, or truncation invalidates the anchored
# prefix and must not be admitted as another append. Restore the exact bytes
# and anchor between probes so the final positive route remains meaningful.
stable_log="$(cat -- "$high_level_log")"
stable_anchor="$(cat -- "$proof_root/t00-session/high_level_log.anchor")"
printf '%s\n%s' 'forged middle' "$stable_log" >"$high_level_log"
assert_unknown "printf '%s\\n' rejected >> $high_level_log"
printf '%s' "$stable_log" >"$high_level_log"
printf '%s' "$stable_anchor" >"$proof_root/t00-session/high_level_log.anchor"
: >"$high_level_log"
assert_unknown "printf '%s\\n' rejected-after-truncate >> $high_level_log"
printf '%s' "$stable_log" >"$high_level_log"
printf '%s' "$stable_anchor" >"$proof_root/t00-session/high_level_log.anchor"

# Keep the user-authorized Git reset/worktree boundary in the focused
# classifier suite so its red/green behavior is exercised by the allowlisted
# test entry point.
bash "$ROOT/hooks/tests/test-validate-bash-git-approvals.sh"
bash "$ROOT/hooks/tests/test-coordinator-protected-git-target.sh"
printf '%s\n' 'validate-bash classifier tests: PASS'

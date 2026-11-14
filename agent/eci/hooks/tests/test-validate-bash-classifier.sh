#!/usr/bin/env bash

set -Eeuo pipefail
# The gate refuses a bin directory with a group- or other-writable ancestor.
umask 077
# Gate-binary builds under fixture HOMEs keep the Go caches on the caller's
# paths, so no read-only module cache lands under TMP_ROOT.
. "$(dirname -- "${BASH_SOURCE[0]}")/require-go.sh"
classifier_gomodcache="$("$ECI_TEST_GO" env GOMODCACHE)"
classifier_gocache="$("$ECI_TEST_GO" env GOCACHE)"
trap 'status=$?; printf "classifier failure: line=%s status=%s command=%q\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
# Pass this spelling to the hook literally.  It is deliberately not $ROOT:
# lifecycle authority belongs only to the current HOME-derived ECI root.
eci_lifecycle='$HOME/.omp/agent/eci/bin/eci-active'
classifier_tmp_parent="$(eci_realpath_canonical "${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}")"
mkdir -p -- "$classifier_tmp_parent"
TMP_ROOT="$(mktemp -d "$classifier_tmp_parent/eci-classifier-${BASHPID}.XXXXXX")"
classifier_hook_fixture="$(mktemp "$ROOT/hooks/.validate-bash-classifier.${BASHPID}.XXXXXX")"
trap 'rm -f -- "$classifier_hook_fixture"' EXIT
cp -- "$ROOT/hooks/validate-bash.sh" "$classifier_hook_fixture"
eci_sed_inplace '2{/^exit 0$/d;}' "$classifier_hook_fixture"
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
  [ "$(eci_realpath_canonical "$kimi_source_root")" = "$kimi_source_root" ] &&
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
  kimi_active_digest="$(eci_sha256_hex "$kimi_root/bin/eci-active")"
  kimi_active_mode="$(eci_stat mode "$kimi_root/bin/eci-active")"
  printf 'bin/eci-active\t%s\t%s\n' "$kimi_active_digest" "$kimi_active_mode" >"$kimi_root/.eci-runtime-sync-manifest"
  chmod 600 -- "$kimi_root/.eci-runtime-sync-manifest"
  printf '/.eci-runtime-sync-manifest\n' >>"$kimi_root/.git/info/exclude"
  [ -z "$(git -C "$kimi_root" status --porcelain)" ]
  # Peer callbacks run with HOME at the Kimi companion home; provision the gate
  # binaries the hook verifies there too, in this fixture only.
  GOMODCACHE="$classifier_gomodcache" GOCACHE="$classifier_gocache" \
    HOME="$kimi_home" bash "$ROOT/hooks/build-native-binaries.sh" \
    eci-command-plan eci-safe-import eci-hook-helper $([ "$(uname -s)" != Linux ] || echo eci-verexec) >/dev/null
  # Peer callbacks are coordinator calls; name a main transcript under that HOME.
  mkdir -p -- "$kimi_home/.omp/agent/sessions/-classifier-coordinator-"
  printf '%s\n' "{\"type\":\"session\",\"version\":3,\"id\":\"t00-session\",\"cwd\":\"$ROOT\"}" \
    '{"type":"message","message":{"role":"user","content":"classifier peer coordinator"}}' \
    >"$kimi_home/.omp/agent/sessions/-classifier-coordinator-/main.jsonl"
fi
subagent_transcript=""
# The Git matrices bind the marker cwd to the repository root, as in the source
# layout (575bb81 ROOT is the ~/.codex repository root). run-isolated.sh copies
# agent/eci without its enclosing .git, so the disposable copy gets its own root.
if [ "$(git -C "$ROOT" rev-parse --show-toplevel 2>/dev/null)" != "$ROOT" ]; then
  case "$ROOT" in
    "${XDG_RUNTIME_DIR:-/nonexistent}"/eci-hooktest/*)
      git -C "$ROOT" init --quiet
      git -C "$ROOT" add -A
      git -C "$ROOT" -c user.name=eci-test -c user.email=eci-test@invalid commit --quiet -m fixture
      ;;
    *)
      printf 'classifier Git matrices need ROOT as its own repository root; run via run-isolated.sh: %s\n' "$ROOT" >&2
      exit 1
      ;;
  esac
fi
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
log_sha256="$(eci_sha256_hex "$high_level_log")"
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
wrong_cwd_log_sha256="$(eci_sha256_hex "$high_level_log")"
if (
  cd "$TMP_ROOT"
  ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID=t00-session ECI_HOME="$ROOT" \
    "$ROOT/bin/eci-active" ledger-append --json '{"event":"decision","summary":"wrong-cwd probe"}'
) >"$TMP_ROOT/wrong-cwd-ledger.out" 2>"$TMP_ROOT/wrong-cwd-ledger.err"; then
  printf '%s\n' 'ledger append unexpectedly accepted a marker from another cwd' >&2
  exit 1
fi
grep -Fq 'ECI high-level log append rejected a marker bound to another session or cwd.' "$TMP_ROOT/wrong-cwd-ledger.err"
[ "$(eci_sha256_hex "$high_level_log")" = "$wrong_cwd_log_sha256" ]

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
mkdir -p "$subagent_eci_home/bin" "$subagent_eci_home/sessions" "$subagent_eci_home/hooks" \
  "$subagent_eci_home/skills/test" "$external_skill_root"
cp -- "$ROOT/hooks/eci-review-gate.sh" "$ROOT/hooks/eci-active-gate.sh" "$subagent_eci_home/hooks/"
cp -- "$ROOT/bin/eci-active" "$subagent_eci_home/bin/eci-active"
chmod +x "$subagent_eci_home/bin/eci-active"
# Provision the gate binaries the hook verifies under the worker HOME
# ($HOME/.omp/agent/eci/bin and their .sha256 receipts), in this fixture only.
GOMODCACHE="$classifier_gomodcache" GOCACHE="$classifier_gocache" \
  HOME="$subagent_home" bash "$ROOT/hooks/build-native-binaries.sh" \
  eci-command-plan eci-safe-import eci-hook-helper $([ "$(uname -s)" != Linux ] || echo eci-verexec) >/dev/null
# Callbacks that switch HOME verify the gate binaries under that HOME; copy the
# built binaries and their receipts there. Coordinator callbacks under such a
# HOME also name a main transcript in its sessions root.
provision_gate_home() {
  local home="$1" name
  mkdir -p -- "$home/.omp/agent/eci/bin"
  for name in eci-command-plan eci-safe-import eci-hook-helper $([ "$(uname -s)" != Linux ] || echo eci-verexec); do
    cp -p -- "$subagent_home/.omp/agent/eci/bin/$name" "$subagent_home/.omp/agent/eci/bin/$name.sha256" \
      "$home/.omp/agent/eci/bin/"
  done
}
write_coordinator_transcript() {
  local home="$1" session="$2" cwd="$3" dir
  dir="$home/.omp/agent/sessions/-classifier-coordinator-"
  mkdir -p -- "$dir"
  printf '%s\n' "{\"type\":\"session\",\"version\":3,\"id\":\"$session\",\"cwd\":\"$cwd\"}" \
    '{"type":"message","message":{"role":"user","content":"classifier coordinator"}}' >"$dir/main.jsonl"
}
provision_gate_home "$tmpdir_alias_home"
write_coordinator_transcript "$tmpdir_alias_home" t00-session "$ROOT"
printf '%s\n' '# worker ECI instructions' >"$subagent_eci_home/AGENTS.md"
printf '%s\n' '# worker agent instructions' >"$subagent_eci_home/AGENTS.md"
printf '%s\n' '# worker test skill' >"$subagent_eci_home/skills/test/SKILL.md"
mkfifo "$subagent_eci_home/skills/test/not-a-source.fifo"
printf '%s\n' '# outside skill' >"$external_skill_root/SKILL.md"
ln -s -- "$external_skill_root" "$subagent_eci_home/skills/escape"

# An absent transcript resolves to the worker role; run_hook drives coordinator
# callbacks, so it names a depth-two main transcript under this HOME's sessions root.
coordinator_transcript="$HOME/.omp/agent/sessions/-classifier-coordinator-/main.jsonl"
mkdir -p -- "$(dirname -- "$coordinator_transcript")"
printf '%s\n' "{\"type\":\"session\",\"version\":3,\"id\":\"t00-session\",\"cwd\":\"$ROOT\"}" \
  '{"type":"message","message":{"role":"user","content":"classifier coordinator"}}' >"$coordinator_transcript"

run_hook() {
  local command="$1" output
  output="${2:-$TMP_ROOT/output}"
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$coordinator_transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output" || return $?
  printf '%s\n' "$output"
}

run_hook_with_canonical_tmpdir() {
  local command="$1" output
  output="$TMP_ROOT/output-with-canonical-tmpdir"
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$coordinator_transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    TMPDIR="$classifier_tmp_parent" ECI_TMPDIR="$classifier_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output" || return $?
  printf '%s\n' "$output"
}

run_hook_with_tmpdir_home_alias() {
  local command="$1" output
  output="$TMP_ROOT/output-with-tmpdir-home-alias"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    --arg transcript "$tmpdir_alias_home/.omp/agent/sessions/-classifier-coordinator-/main.jsonl" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    HOME="$tmpdir_alias_home" TMPDIR="$tmpdir_alias_home/tmp" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output" || return $?
  printf '%s\n' "$output"
}

assert_planner_allows() {
  local command="$1" planner_output
  planner_output="$(
    jq -cn --arg cwd "$ROOT" --arg command "$command" \
      '{provider:"omp",role:"coordinator",cwd:$cwd,marker:"active",active_session:"t00-session",command:$command,active_markers:[],approved_roots:[$cwd]}' |
      "$subagent_eci_home/bin/eci-command-plan"
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
      "$subagent_eci_home/bin/eci-command-plan"
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
  eci_sed_inplace '2{/^exit 0$/d;}' "$COPIED_ROOT/hooks/validate-bash.sh"
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
    ) >"$output" || return $?
  printf '%s\n' "$output"
}

assert_copied_home_admitted() {
  local command="$1" callback_session="$2" callback_cwd="$3" output

  output="$(run_copied_home_hook "$command" "$callback_session" "$callback_cwd")" || return $?
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
      bash "$classifier_hook_fixture" >"$output" || return $?
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
if ( COPIED_HOME="$TMP_ROOT/missing-home"; assert_copied_home_admitted 'printf ordinary' "$COPIED_SESSION" "$COPIED_ROOT" ) >/dev/null 2>&1; then
  printf '%s\n' 'copied-home assertion accepted a failed hook' >&2
  exit 1
fi
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

# Permissive mode leaves ordinary commands transparent, regardless of a stale
# marker. A missing ordinary script remains the shell's execution error.
printf '%s\n' permissive >"$XDG_CONFIG_HOME/eci/command-gate-mode"
active_permissive_gate_log="$XDG_STATE_HOME/eci/command-gate/would-deny.jsonl"
[ ! -e "$active_permissive_gate_log" ]

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
# eci_runtime_verexec (validate-bash.sh), which verifies it against the
# .sha256 receipt that hooks/build-native-binaries.sh writes
# (hooks/lib/eci-verexec-go, through hooks/lib/eci-native-binary.sh). The fixture plants no such
# binary, so the hook fails closed with ECI_HOOK_NATIVE_GATE_UNAVAILABLE ("is
# missing or a symlink") before the defer path. The provenance receipt is written
# by the planner refresh (tools/eci-active/planner.go:254, :270) and has no
# production reader at HEAD.

run_hook_with_kimi_home() {
  local command="$1" companion_root="$2" companion_home output
  companion_home="$(dirname -- "$companion_root")"
  output="${3:-$TMP_ROOT/output-with-kimi-home}"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    --arg transcript "$companion_home/.omp/agent/sessions/-classifier-coordinator-/main.jsonl" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    HOME="$companion_home" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$companion_root" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output" || return $?
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
    --arg transcript "$kimi_home/.omp/agent/sessions/-classifier-coordinator-/main.jsonl" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    env -u KIMI_CODE_HOME HOME="$kimi_home" ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output" || return $?
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
      bash "$classifier_hook_fixture" >"$output" || return $?
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
      bash "$classifier_hook_fixture" >"$output" || return $?
  printf '%s\n' "$output"
}

run_subagent_hook() {
  local command="$1" transcript_order="${2:-type-first}" output transcript hook_home="${5:-$subagent_home}"
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
    ECI_PROOF_ROOT="$proof_root" HOME="$hook_home" ECI_HOME="$subagent_eci_home" KIMI_CODE_HOME="$kimi_root" \
      PATH="$subagent_eci_home/bin:$PATH" \
      bash "$classifier_hook_fixture" >"$output" || return $?
  printf '%s\n' "$output"
}

# python_script_required_filter accepts a denial naming the Python script route
# and its #!/usr/bin/python3 hatch.
python_script_required_filter='
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PYTHON_SCRIPT_REQUIRED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("#!/usr/bin/python3"))
'

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
      run_subagent_hook "$command" type-first "$output" "$transcript" >/dev/null || exit $?
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
        control-owner)
          jq -e '
            .hookSpecificOutput.permissionDecision == "deny" and
            (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]"))
          ' "$output" >/dev/null || {
            printf 'worker peer control probe was not denied ECI_CONTROL_OWNER_REQUIRED: %s\n' "$command" >&2
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
        python-script-required)
          jq -e "$python_script_required_filter" "$output" >/dev/null || {
            printf 'direct worker Python call was not denied ECI_PYTHON_SCRIPT_REQUIRED: %s\n' "$command" >&2
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
      run_hook "$command" "$output" >/dev/null || exit $?
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
        python-script-required)
          jq -e "$python_script_required_filter" "$output" >/dev/null || {
            printf 'direct coordinator Python call was not denied ECI_PYTHON_SCRIPT_REQUIRED: %s\n' "$command" >&2
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
        coordinator) run_hook "$command" "$output" >/dev/null || exit $? ;;
        worker) run_subagent_hook "$command" type-first "$output" "$transcript" >/dev/null || exit $? ;;
        # A real Worker shares the coordinator HOME, so $ROOT is its own
        # protected provider root (protectedHookModeRoots is HOME-derived).
        worker-root-owner) run_subagent_hook "$command" type-first "$output" "$transcript" "$HOME" >/dev/null || exit $? ;;
        peer) run_hook_with_kimi_home "$command" "$kimi_root" "$output" >/dev/null || exit $? ;;
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
      bash "$classifier_hook_fixture" >"$output" || return $?
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
      # Under a live marker the control-writer check precedes broad destruction
      # (same order at 575bb81); either code is a correct denial of this argv.
      jq -e '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
        ((.hookSpecificOutput.permissionDecisionReason | contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
         (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-live-control")) or
         (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_SESSIONS_ROOT_DENIED]")) or
         (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")))
      ' "$output" >/dev/null
      ;;
    *)
      printf 'unknown initial worker fixture: %q\n' "$command" >&2
      return 1
      ;;
  esac
}

assert_allowed() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")" || return $?
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
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=broad-destructive")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=git-broad-effect")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("path=" + $repository + ","))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'Git whole-worktree staging denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}
# Removing or relocating a live Bash-gate file blocks every later Bash call; the
# denial names the in-place, index-only and acknowledged routes.
assert_live_gate_removal_denied() {
  local command="$1" runner="${2:-run_hook}" output
  output="$("$runner" "$command")"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIVE_BASH_GATE_REMOVAL_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=live-bash-gate-removal")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_LIVE_HOOK_REMOVAL=1"))
  ' "$output" >/dev/null || {
    printf 'live Bash-gate removal denial mismatch: command=%q output=%s\n' "$command" "$output" >&2
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
run_matrix_parallel coordinator assert_allowed lifecycle-identity \
  "env ECI_SESSION_ID=wrong-session $eci_lifecycle ledger-append --json '{\"event\":\"decision\",\"summary\":\"mismatch probe\"}'"

# ECI ownership admission is ecosystem-neutral: after reserved ownership and
# shell-indirection checks, finite direct argv vectors for ordinary build/test
# tools are admitted without maintaining an executable allowlist.
run_hook_matrix_parallel allowed ordinary-coordinator-literal \
  "go test ./..." \
  "cargo test --workspace" \
  "pytest -q tests" \
  "interpreter-tool --module test-suite --flag value" \
  "./tools/check.py --mode strict" \
  "node scripts/check.mjs" \
  "npm test -- --runInBand" \
  "cmake --build build --target test" \
  "ctest --test-dir build --output-on-failure" \
  "ninja -C build test" \
  "meson test -C build" \
  "mvn -q test" \
  "./gradlew test" \
  "novel-tool --flag value" \
  "./tools/repo-check --context" \
  "rg -n -C 3 'needle' hooks"

# The make-cap gate has no verified launcher route on Darwin.
output="$(run_hook "make -B test")"
if [ "$(uname -s)" = Darwin ]; then
  [ ! -s "$output" ] || {
    printf 'uncapped make was unexpectedly denied on Darwin:\n' >&2
    cat -- "$output" >&2
    exit 1
  }
else
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_MAKE_MEMORY_CAP_REQUIRED]"))
  ' "$output" >/dev/null || {
    printf 'uncapped make was not denied ECI_MAKE_MEMORY_CAP_REQUIRED: make -B test\n' >&2
    cat -- "$output" >&2
    exit 1
  }
fi

# Opaque shell/interpreter spelling is not a source-write or target boundary.
# Keep it hook-transparent unless a later recognizer sees a concrete wrong
# target. These are intentionally not an executable allowlist.
run_hook_matrix_parallel allowed ordinary-coordinator-opaque \
  "eval 'go test ./...'" \
  "bash scripts/test.sh" \
  "node -e 'console.log(1)'" \
  "interpreter-tool -c 'dynamic payload'" \
  "go test \$(printf ./...)" \
  $'go test ./...\nprintf done'

# Direct Python interpreter calls are a user-required route: the hatch is an
# executable script file run by path, whatever the interpreter's arguments.
run_hook_matrix_parallel python-script-required coordinator-python-direct \
  "python3 -m pytest tests" \
  "python3 tools/check.py --mode strict" \
  "python3 -c 'print(1)'" \
  "python3 -" \
  "env -S python3 -m pytest tests"

# Ordinary commits and reset do not infer ownership from legacy markers.
run_hook_matrix_parallel allowed coordinator-git-commit \
  "git commit -m 'checkpoint'"
run_hook_matrix_parallel allowed coordinator-git-reset \
  "git reset --hard"

if [[ "$kimi_root" = /* ]] && [ -d "$kimi_root" ] && [ ! -L "$kimi_root" ] &&
  [ -f "$kimi_root/bin/eci-active" ] && [ ! -L "$kimi_root/bin/eci-active" ]; then
  # The callback does not gate script execution by provider root.
  run_matrix_parallel peer assert_allowed kimi-peer-read-only \
    "$kimi_root/bin/eci-active status" \
    "$kimi_root/bin/eci-active --help" \
    "env KIMI_SESSION_ID=t00-session $kimi_root/bin/eci-active status" \
    "env ECI_SESSION_ID=t00-session $kimi_root/bin/eci-active status"
  # Lifecycle identity is validated by the invoked tool, not inferred from markers.
  run_matrix_parallel peer assert_allowed kimi-peer-denied \
    "$kimi_root/bin/eci-active on 'peer coordinator scope'" \
    "$kimi_root/bin/eci-active off ${HOME:?}/tmp/eci-peer-disengage.md"
  run_matrix_parallel peer assert_allowed kimi-peer-identity \
    "env KIMI_SESSION_ID=wrong-session $kimi_root/bin/eci-active ledger-append --json '{\"event\":\"decision\",\"summary\":\"mismatch probe\"}'"
  run_matrix_parallel peer assert_allowed kimi-peer-lifecycle \
    "$kimi_root/bin/eci-active on peer-scope extra"
  # Worker status reads are ordinary script invocations.
  run_subagent_matrix_parallel allowed kimi-peer-worker-route \
    "$kimi_root/bin/eci-active status"
  altered_kimi_home="$TMP_ROOT/altered-kimi-home"
  altered_kimi_root="$altered_kimi_home/.kimi-code"
  mkdir -p -- "$altered_kimi_home"
  cp -a -- "$kimi_home/.omp" "$altered_kimi_home/.omp"
  cp -a -- "$kimi_root" "$altered_kimi_root"
  printf '\n' >>"$altered_kimi_root/bin/eci-active"
  # A changed script remains ordinary unless its concrete effect is unsafe.
  assert_allowed "$altered_kimi_root/bin/eci-active status" run_altered_peer_hook
  assert_allowed \
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
  "./tools/check.py" \
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
  "interpreter-tool -c 'dynamic payload'"
run_subagent_matrix_parallel allowed ordinary-worker-runtime \
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
run_subagent_matrix_parallel python-script-required worker-python-direct \
  "python3 -m pytest tests" \
  "python3 tools/check.py" \
  "python3 tools/check.py -c" \
  "python3 -" \
  "python3" \
  "python3 -cprint" \
  "python3.11 -cprint" \
  "python3 -W ignore" \
  "python3 -X dev" \
  "python3 -i tools/check.py"
worker_help_output="$(run_subagent_hook "eci-active --help")"
[ ! -s "$worker_help_output" ]
worker_ps_output="$(run_subagent_hook "ps -o pid,etime,stat,cmd")"
[ ! -s "$worker_ps_output" ]
run_hook_matrix_parallel allowed coordinator-ordinary-source-write \
  "touch hooks/generated-coordinator-source-again" \
  "chmod 644 hooks/generated-coordinator-source-again" \
  "sed -i '1s/^/generated /' hooks/generated-coordinator-source-again"
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
pager_output="$(run_hook_with_transcript_pager "git -C $ROOT status --short")"
[ ! -s "$pager_output" ]
if [[ "$kimi_root" = /* ]] && [ -d "$kimi_root" ] && [ ! -L "$kimi_root" ] &&
  [ "$(eci_realpath_canonical "$kimi_root")" = "$kimi_root" ]; then
  if [ -f "$kimi_root/hooks/tests/test-block-no-progress.sh" ] && [ ! -L "$kimi_root/hooks/tests/test-block-no-progress.sh" ]; then
    run_hook_matrix_parallel allowed kimi-entrypoint-forms \
      "bash $kimi_root/hooks/tests/test-block-no-progress.sh" \
      "$kimi_root/hooks/tests/test-block-no-progress.sh"
    default_kimi_output="$(run_hook_without_kimi_home "$kimi_root/hooks/tests/test-block-no-progress.sh")"
    [ ! -s "$default_kimi_output" ] || {
      cat -- "$default_kimi_output" >&2
      exit 1
    }
  fi
  run_hook_matrix_parallel allowed kimi-project-inspection \
    "git -C $kimi_root status --short" \
    "git -C $kimi_root status --short --branch" \
    "git -C $kimi_root log -5 --oneline" \
    "git -C $kimi_root diff -- AGENTS.md" \
    "git -C $kimi_root diff --stat"
  # (a) coordinator hook-mode Defer: contract :175-181; 575bb81 and omp allow the plain chmod too.
  run_hook_matrix_parallel allowed kimi-control-write \
    "chmod 755 $kimi_root/hooks/pre-commit-go-mod.sh $kimi_root/hooks/install-pre-commit-go-mod.sh"
  run_hook_matrix_parallel allowed kimi-install-entrypoints \
    "bash hooks/install-pre-commit-go-mod.sh" \
    "bash $kimi_root/hooks/install-pre-commit-go-mod.sh" \
    "bash hooks/install-pre-commit-go-mod.sh --repair-hardlink $kimi_root"
  self_repair_output="$(run_hook "bash hooks/install-pre-commit-go-mod.sh --repair-hardlink $ROOT")"
  [ ! -s "$self_repair_output" ]
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
# Contract :179-181 / 575bb81 classifier.go:3746-3747: an unfamiliar utility form is
# not a mistake by itself; mktemp argument errors carry no concrete effect.
run_hook_matrix_parallel allowed coordinator-mktemp-invalid \
  "mktemp -d $mktemp_template extra" \
  "mktemp -d $classifier_tmp_parent/eci-probe-\$(date).XXXXXX"
run_subagent_matrix_parallel allowed worker-mktemp \
  "mktemp -d $mktemp_template"
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

# WITHHELD (TD classifier/dynamic-find-action): inherited admission at 575bb81 and omp
# (classifier.go `!request.HookMode` guard); PEER4 follow-up.
run_subagent_matrix_parallel allowed worker-tmp-inspection \
  "find /tmp -maxdepth 1 -type d -print"

run_hook_matrix_parallel allowed coordinator-dynamic-find-actions \
  "find /tmp -maxdepth 1 -type f -exec file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec ./file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec file -C -m $TMP_ROOT/magic-file {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec file --compile -m $TMP_ROOT/magic-file {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec printf '%s' {} \\; && printf done" \
  "find /tmp -maxdepth 1 -type f -delete && printf done"
# WITHHELD: coordinator find -exec ../file and -exec /tmp/file (TD classifier/dynamic-find-action,
# sweep-P1P2 D3): find -exec evades the COORDINATOR_SCRIPT_TARGET_DENIED both trees enforce on the
# direct launch; see TD-WITHHELD-QUOTES.md.
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
run_hook_matrix_parallel allowed coordinator-kimi-find-exec \
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
assert_allowed "realpath -e $evidence_file && rm -f $TMP_ROOT/denied" run_hook_with_canonical_tmpdir
# A current session may spell its canonical temporary root through exactly
# $HOME/tmp.  Exercise the generic cleanup route directly: it allows one
# generated file beneath the resolved root, and also a path in an outside
# sibling of that root, because the sibling is unowned and in no other
# session's scope (contract :181).
tmpdir_alias_cleanup_target="$classifier_tmp_parent/eci-classifier-alias-${BASHPID}-cleanup"
tmpdir_alias_outside_target="${classifier_tmp_parent}-outside/eci-classifier-alias-${BASHPID}-cleanup"
assert_allowed "rm -f $tmpdir_alias_cleanup_target" run_hook_with_tmpdir_home_alias
# Plain rm outside the alias root: an unowned sibling of the temporary root, in
# no other session's scope (contract :181).
assert_allowed "rm -f $tmpdir_alias_outside_target" run_hook_with_tmpdir_home_alias
# The reviewed compound route may clean one regular leaf inside the private
# session directory created directly beneath the configured temporary root.
# It cannot reach a sibling session directory, a deeper descendant, or the root
# through its $HOME/tmp alias (rows withheld below); an unowned outside sibling
# root is admitted (contract :181).
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
# The outside sibling root is unowned: it is never created and no session or
# owner binds it (contract :181).
assert_allowed "git status --short || rm -f $session_temp_outside_target" run_hook_with_tmpdir_home_alias
# WITHHELD (TD classifier session-temp compound rm, PEER4 follow-up): the sibling
# session, deeper, alias, link, `$TMP_ROOT/../<self>` and `$TMP_ROOT/*` rows target
# session-bound paths; both trees admit them. Quoted in TD-WITHHELD-QUOTES.md
# "classifier session-temp compound rm".
assert_allowed "git status --short || printf after" run_hook_with_tmpdir_home_alias
compound_broad_output="$(run_hook "realpath -e $evidence_file && rm -rf /")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=broad-destructive")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
' "$compound_broad_output" >/dev/null
# The same proof-root escape must remain actionable when the planner is
# unavailable under an isolated HOME.  The fallback checks the already
# classified read effect, not the path spelling or planner/receipt state.
# A generic path outside the proof namespace and an in-root regular file are
# ordinary read targets, as is the lexical proof-root symlink escape (read only).
assert_allowed "cat $TMP_ROOT/outside-proof.txt" run_hook_with_tmpdir_home_alias
assert_allowed "cat $evidence_file" run_hook_with_tmpdir_home_alias
assert_allowed "cat $evidence_dir/outside-link"
alias_proof_escape_output="$(run_hook_with_tmpdir_home_alias "cat $evidence_dir/outside-link")"
[ ! -s "$alias_proof_escape_output" ]
# WITHHELD (TD "worker protected runner/sessions inspection admitted", PEER4
# follow-up): inherited admission (omp and source 575bb81 both admit); the
# original matrix is quoted verbatim in the TD row.
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


# Active-ECI local index staging resolves its repository and selector before
# deciding whether the concrete effect is ordinary or broad/destructive.
run_hook_matrix_parallel allowed coordinator-git-prep \
  "git add -- hooks/validate-bash.sh" \
  "git restore --staged -- hooks/validate-bash.sh" \
  "git add -- ../outside" \
  "git restore --staged hooks/validate-bash.sh" \
  "env git add -- hooks/validate-bash.sh"
# (a) native Git staging of the cwd selector: contract :175-181; 575bb81 classifier.go:3758-3759.
run_matrix_parallel coordinator assert_allowed coordinator-git-prep-dot \
  "git add ."
# ADDED: the explicit whole-worktree form both trees deny.
run_matrix_parallel coordinator assert_git_whole_worktree_staging_denied coordinator-git-prep-unsafe-all \
  "git add -A"
run_matrix_parallel coordinator assert_live_gate_removal_denied coordinator-protected-git-target \
  "git rm -- hooks/validate-bash.sh" \
  "git rm -r -- hooks/validate-bash.sh" \
  "git mv -- hooks/validate-bash.sh hooks/validate-bash.sh" \
  "git mv -- hooks/validate-bash.sh ../outside"
run_matrix_parallel coordinator assert_allowed coordinator-protected-git-target-hatch \
  "ECI_LIVE_HOOK_REMOVAL=1 git rm -- hooks/validate-bash.sh" \
  "git rm --cached -- hooks/validate-bash.sh" \
  "git checkout -- hooks/validate-bash.sh"
# Exact native Worker staging is not gated by legacy marker state.
run_matrix_parallel worker assert_allowed worker-git-staging-add \
  "git add -- hooks.json" \
  "git add README.md"
run_subagent_matrix_parallel allowed worker-git-staging \
  "git restore --staged -- hooks/validate-bash.sh"
run_matrix_parallel worker-root-owner assert_live_gate_removal_denied worker-git-prep \
  "git rm -- hooks/validate-bash.sh" \
  "git mv -- hooks/validate-bash.sh hooks/validate-bash.sh"

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
assert_allowed "$fake_bin/git rebase topic" run_subagent_hook


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
assert_allowed "$hook_eci aggregate-stage repo-a hooks/target.txt"
assert_allowed "$hook_eci aggregate-stage repo-a -- --"
assert_allowed "$hook_eci aggregate-stage repo-a -- ../outside"
assert_allowed "$hook_eci aggregate-stage repo-a -- eci_active"
assert_allowed "env ECI_SESSION_ID=t00-session $hook_eci aggregate-stage repo-a -- hooks/target.txt"
stale_report="$proof_root/019ff790-0000-7000-8000-000000000001/disengage.md"
assert_allowed "$hook_eci off $stale_report"

# An alternate root is not an authority alias, even where it resolves to the
# same deployed executable. The source spelling remains part of the contract.
alias_home="$TMP_ROOT/eci-home-alias"
ln -s "$ROOT" "$alias_home"
assert_allowed "$alias_home/bin/eci-active off $TMP_ROOT/disengage.md"

# Active-marker Worker lifecycle ownership is a direct-planner concern;
# Bash callbacks do not activate those legacy marker checks.
# WITHHELD (TD "worker inline interpreter launch admitted", PEER4 follow-up): inherited admission (omp and
# source 575bb81 both admit); the original assertions are quoted verbatim in the TD row.
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
provision_gate_home "$isolated_worker_home"

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
  ) || return $?
  printf '%s\n' "$output"
}



assert_isolated_worker_structural_allowed() {
  local label="$1" command="$2" output

  output="$(run_isolated_worker_hook "$command")" || return $?
  [ ! -s "$output" ] || {
    printf 'isolated worker benign structural form was denied: %s command=%q output=%s\n' \
      "$label" "$command" "$output" >&2
    [ ! -e "$output" ] || cat -- "$output" >&2
    return 1
  }
}

live_worker_script="$ROOT/$isolated_worker_script_relative"
live_worker_script_sha_before="$(eci_sha256_hex "$live_worker_script")"
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
assert_isolated_worker_structural_allowed installer "bash $isolated_worker_non_test_relative"
assert_isolated_worker_structural_allowed installer-alias "bash hooks/tests/../install-pre-commit-go-mod.sh"
assert_isolated_worker_structural_allowed non-shell-suffix "bash $isolated_worker_non_script_relative"
assert_isolated_worker_structural_allowed symlink-outside 'bash hooks/tests/structural-link.sh'
assert_isolated_worker_structural_allowed parent-symlink \
  'bash hooks/tests-link/structural-worker.sh'
assert_isolated_worker_structural_allowed peer-workspace "bash $isolated_worker_peer_script"
assert_isolated_worker_structural_allowed bash-option "bash -n $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed sh-option "sh -n $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed environment-wrapper "env bash $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed compound \
  "bash $isolated_structural_worker_relative && bash $isolated_structural_worker_relative"
assert_isolated_worker_structural_allowed assignment "FOO=bar bash $isolated_structural_worker_relative"
live_worker_script_sha_after="$(eci_sha256_hex "$live_worker_script")"
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
provision_gate_home "$isolated_coordinator_home"
write_coordinator_transcript "$isolated_coordinator_home" "$isolated_coordinator_session" "$isolated_coordinator_root"

run_isolated_coordinator_hook() {
  local command="$1" output
  output="$TMP_ROOT/isolated-coordinator-output"
  (
    cd "$isolated_coordinator_root"
    jq -cn --arg cwd "$isolated_coordinator_root" --arg command "$command" \
      --arg session "$isolated_coordinator_session" \
      --arg transcript "$isolated_coordinator_home/.omp/agent/sessions/-classifier-coordinator-/main.jsonl" \
      '{session_id:$session,tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
      HOME="$isolated_coordinator_home" TMPDIR="$classifier_tmp_parent" ECI_TMPDIR="$classifier_tmp_parent" \
        ECI_PROOF_ROOT="$isolated_coordinator_proof_root" ECI_HOME="$isolated_coordinator_root" \
        KIMI_CODE_HOME="$isolated_coordinator_peer" \
        PATH="$ROOT/bin:$PATH" bash "$classifier_hook_fixture" >"$output"
  ) || return $?
  printf '%s\n' "$output"
}

assert_isolated_coordinator_structural_allowed() {
  local command="$1" output

  output="$(run_isolated_coordinator_hook "$command")" || return $?
  [ ! -s "$output" ] || {
    printf 'isolated coordinator structural test route was not admitted: %s\n' "$command" >&2
    cat -- "$output" >&2
    exit 1
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
assert_isolated_coordinator_structural_allowed "bash $isolated_coordinator_outside"

isolated_repair_script_relative="hooks/install-pre-commit-go-mod.sh"
isolated_repair_script="$isolated_coordinator_root/$isolated_repair_script_relative"
mkdir -p -- "$(dirname -- "$isolated_repair_script")"
cp -- "$ROOT/$isolated_repair_script_relative" "$isolated_repair_script"
printf '%s\n' '# isolated hard-link repair mutation probe' >>"$isolated_repair_script"
assert_isolated_coordinator_structural_allowed \
  "bash $isolated_repair_script_relative"
assert_isolated_coordinator_structural_allowed \
  "bash $isolated_repair_script_relative --repair-hardlink $isolated_coordinator_peer"
assert_isolated_coordinator_structural_allowed \
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
assert_isolated_coordinator_structural_allowed \
  "$isolated_manifest_assignments bash $isolated_coordinator_script_relative"
assert_allowed "eci-active nested-exit"

# WITHHELD (TD "worker env-injection prefixes admitted", PEER4 follow-up):
# BASH_ENV startup-context assignment prefixes on an allowlisted test script are
# admitted by omp and by source 575bb81, so the deny matrices and the
# interpreter-context check are out of the live run until the product denies
# them. Validation itself must still never execute the probe.
bash_env_probe="$TMP_ROOT/bash-env-probe"
printf '%s\n' "touch '$TMP_ROOT/bash-env-ran'" >"$bash_env_probe"
run_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" >/dev/null
run_subagent_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" >/dev/null
[ ! -e "$TMP_ROOT/bash-env-ran" ]
# (a) startup variables a non-interactive bash script never reads (contract :179
# "environment expansion"); both trees allow the unwrapped script.
run_hook_matrix_parallel allowed coordinator-environment-inert \
  "ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "CDPATH=$TMP_ROOT bash hooks/tests/test-eci-fast-path.sh" \
  "PYTHONSTARTUP=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "RUBYOPT=-r$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "NODE_OPTIONS=--require=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "PERL5OPT=-I$TMP_ROOT bash hooks/tests/test-eci-fast-path.sh" \
  "PATH=$TMP_ROOT bash hooks/tests/test-eci-fast-path.sh"
[ ! -e "$TMP_ROOT/bash-env-ran" ]

# The coordinator boundary is effect-based, not value- or spelling-based.
# Existing context files/paths attached to an existing interpreter script are
# denied; ordinary, empty, unresolved, and `env --` command-argv values pass.
run_hook_matrix_parallel allowed coordinator-environment-context-unresolved \
  "BASH_ENV=ordinary-value bash hooks/tests/test-eci-fast-path.sh" \
  "env BASH_ENV=ordinary-value bash hooks/tests/test-eci-fast-path.sh" \
  "BASH_ENV=/tmp/eci-missing-startup-file bash hooks/tests/test-eci-fast-path.sh" \
  "env BASH_ENV=/tmp/eci-missing-startup-file bash hooks/tests/test-eci-fast-path.sh" \
  "env -- BASH_ENV=/etc/profile bash hooks/tests/test-eci-fast-path.sh"
[ ! -e "$TMP_ROOT/bash-env-ran" ]

run_matrix_parallel worker assert_allowed worker-environment-lifecycle \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active nested-enter 1 2 t00-session" \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active nested-accept" \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active nested-exit" \
  "env -u ECI_ROLE bash $subagent_eci_home/bin/eci-active manifest-write $proof_root/t00-session/eci-required-critics.json.source" \
  "env -u ECI_ROLE command bash $subagent_eci_home/bin/eci-active nested-exit"

# A copied lifecycle executable remains an ordinary callback script target.
mkdir -p -- "$subagent_home/tmp"
copied_eci="$subagent_home/tmp/eci-active-copy"
copied_worker="$subagent_home/tmp/worker-script"
cp -- "$ROOT/bin/eci-active" "$copied_eci"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$copied_worker"
chmod +x "$copied_eci" "$copied_worker"
# OUT OF LIVE RUN (TD "copied lifecycle no-verb over-strict", PEER4 low-medium):
# inherited over-strict (omp and source 575bb81 both deny ECI_CONTROL_OWNER_REQUIRED);
# quoted in TD-WITHHELD-QUOTES.md "classifier P5b: copied-lifecycle-read no-verb row".
run_subagent_matrix_parallel allowed copied-lifecycle-read \
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
run_matrix_parallel worker assert_allowed copied-lifecycle-mutation \
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
run_matrix_parallel worker assert_allowed concrete-worker-context \
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
renamed_eci="$subagent_eci_home/bin/eci-renamed"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$renamed_eci"
chmod +x "$renamed_eci"
run_matrix_parallel worker assert_allowed renamed-lifecycle \
  "eci-renamed ledger-append --json '{\"event\":\"decision\",\"summary\":\"one-line-entry\"}'" \
  "eci-renamed nested-enter 1 2 t00-session"

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
run_matrix_parallel worker assert_allowed transparent-wrapper-git \
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
run_matrix_parallel worker assert_allowed single-wrapped-worker-git \
  "env FOO=bar git rebase topic"

# Transparent wrappers do not restore the retired Git family gate.
run_matrix_parallel worker assert_allowed chronic-worker-routes \
  "chronic /tmp/eci-escape.sh" \
  "chronic git rebase topic"

# Callback classification does not infer lifecycle ownership from argument shape.
run_matrix_parallel worker assert_allowed worker-visible-lifecycle-arity \
  "env -u ECI_ROLE $subagent_eci_home/bin/eci-active on worker-scope extra" \
  "env -u ECI_ROLE $subagent_eci_home/bin/eci-active aggregate-stage repo-a -- tracked.txt" \
  "source $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md extra" \
  "env -u ECI_ROLE bash -c 'source $subagent_eci_home/bin/eci-active on worker-scope extra'"

run_matrix_parallel worker assert_allowed worker-git-commit \
  "git commit -qm --amend" \
  "env -u ECI_ROLE git commit -qm --dry-run"
run_matrix_parallel worker assert_allowed worker-git-interpreter \
  "bash -c 'git rebase topic'"

# Shell spellings do not restore the retired lifecycle ownership gate.
run_matrix_parallel worker assert_allowed worker-shell-lifecycle \
  "source $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md" \
  ". $subagent_eci_home/bin/eci-active wait $TMP_ROOT/eci_user_owned_wait.md" \
  "$subagent_eci_home/bin/eci-active ledger-append --json '{\"event\":\"decision\",\"summary\":\"one-line-entry\"}'" \
  "env -S '$subagent_eci_home/bin/eci-active resume aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'"

# Lifecycle identity does not make dynamic shell indirection admissible.
# Coordinator source/inline launches of the canonical executable are admitted (contract :179-181).
for dynamic_lifecycle in \
  "source ./bin/eci-active off $TMP_ROOT/disengage.md" \
  "env -u ECI_ROLE bash -c 'source $ROOT/bin/eci-active wait $TMP_ROOT/eci_user_owned_wait.md'"; do
  output="$(run_hook "$dynamic_lifecycle")"
  [ ! -s "$output" ]
done

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
run_subagent_matrix_parallel allowed worker-branch-remote \
  "git branch --set-upstream-to=origin/main" \
  "git branch feature" \
  "git remote set-url origin https://example.invalid/repo.git"
# A Worker's raw stdout `git archive HEAD` is native Git: hook-mode Git defers
# (575bb81 classifier.go:3757-3761), recorded for Workers at
# agent/eci-binding-contract.md:151. Output targets stay gated below.
assert_allowed "git archive HEAD" run_subagent_hook
assert_allowed "git archive HEAD"
assert_allowed "git -c core.pager=cat archive HEAD"
# WITHHELD (TD classifier/P5a coordinator inherited admissions): coordinator-git-archive-fast-path-unsafe; both trees admit (P3 coordinator archive family).

# Archive shapes are ordinary native Git under the hook: the 575bb81 and HEAD
# planners both admit each case for coordinator and worker. --output into the
# proof root follows ordinary-write policy (a plain redirect is also admitted).
assert_archive_case_allowed() {
  local name="$1" command="$2" path_value="$3" git_dir="$4" role output
  local -a env_args=(ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_root" PATH="$path_value")
  [ "$git_dir" = "-" ] || env_args+=(GIT_DIR="$git_dir")
  for role in coordinator worker; do
    output="$TMP_ROOT/archive-case-$name-$role.output"
    if [ "$role" = coordinator ]; then
      jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$coordinator_transcript" \
        '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}'
    else
      jq -cn --arg cwd "$ROOT" --arg command "$command" \
        '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}'
    fi | env "${env_args[@]}" bash "$classifier_hook_fixture" >"$output" || return $?
    [ ! -s "$output" ] || {
      printf 'archive case was not admitted: case=%s role=%s command=%q\n' "$name" "$role" "$command" >&2
      cat -- "$output" >&2
      return 1
    }
  done
}
if ( classifier_hook_fixture="$TMP_ROOT/no-such-hook"; assert_archive_case_allowed broken 'git archive HEAD' "$ROOT/bin:$PATH" - ) >/dev/null 2>&1; then
  printf '%s\n' 'archive assertion accepted a failed hook' >&2
  exit 1
fi
archive_case_failed=0
for archive_case in \
  'direct|git archive HEAD|root|-' \
  'fake-path|git archive HEAD|fake|-' \
  'inherited-git-context|git archive HEAD|root|inherited' \
  'direct-path|FAKE/git archive HEAD|root|-' \
  'direct-path-no-pager|FAKE/git --no-pager archive HEAD|root|-' \
  'archive-shape-compound|git archive HEAD && printf after|root|-' \
  'archive-shape-repository|git -C ROOT archive HEAD|root|-' \
  'archive-shape-remote-eq|git archive --remote=origin HEAD|root|-' \
  'archive-shape-remote|git archive --remote origin HEAD|root|-' \
  'archive-shape-exec-eq|git archive --exec=git-upload-archive HEAD|root|-' \
  'archive-shape-exec|git archive --exec git-upload-archive HEAD|root|-' \
  'archive-shape-output|git archive --output=PROOF/t00-session/archive.tar HEAD|root|-' \
  'archive-shape-zip|git archive --format=zip --output=PROOF/t00-session/archive.zip HEAD|root|-' \
  'archive-shape-readme|git archive --format=tar --output=PROOF/t00-session/archive.tar HEAD README|root|-'; do
  IFS='|' read -r archive_name archive_command archive_path archive_git_dir <<<"$archive_case"
  archive_command="${archive_command//FAKE/$fake_bin}"
  archive_command="${archive_command//PROOF/$proof_root}"
  archive_command="${archive_command/-C ROOT /-C $ROOT }"
  case "$archive_path" in root) archive_path="$ROOT/bin:$PATH" ;; fake) archive_path="$fake_bin:$ROOT/bin:$PATH" ;; esac
  [ "$archive_git_dir" != inherited ] || archive_git_dir="$TMP_ROOT/inherited-git-dir"
  assert_archive_case_allowed "$archive_name" "$archive_command" "$archive_path" "$archive_git_dir" || archive_case_failed=1
done
[ "$archive_case_failed" -eq 0 ]

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
# WITHHELD (TD classifier P3 inherited worker admissions): the history pathspec
# near-miss loop (git diff --check -- AGENTS.md; git log -- ../AGENTS.md) is an
# inherited admission at 575bb81 and omp; quoted in TD-WITHHELD-QUOTES.md.
run_subagent_matrix_parallel allowed worker-instruction-reads \
  "cat $subagent_eci_home/skills/missing/SKILL.md" \
  "cat $subagent_eci_home/skills/test/not-a-source.fifo"
run_subagent_matrix_parallel allowed worker-native-git \
  "git commit --amend -m checkpoint" \
  "git reset --hard HEAD"
run_matrix_parallel worker-root-owner assert_allowed worker-protected-git \
  "git checkout -- hooks/validate-bash.sh"
run_subagent_matrix_parallel allowed worker-native-worktree-add \
  "git worktree add /tmp/eci-worker-tree HEAD"
# Read-only Git inspection and native path operations remain available to Workers;
# concrete broad effects are checked independently.
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
[ ! -s "$worker_git_chain_output" ]
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
assert_allowed "env GIT_DIR=/tmp/other git status"
run_subagent_matrix_parallel allowed worker-git-dir-environment \
  "env GIT_DIR=eci-private-git-value git status"
run_matrix_parallel coordinator assert_environment_broad_denied coordinator-environment-broad \
  "env FOO=bar rm -rf /"
assert_environment_broad_denied "env FOO=bar rm -rf /" run_subagent_hook
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
# Contract :179-181 / 575bb81 classifier.go:3746-3747: punctuation, wrappers and
# ordinary redirects into scratch are not concrete effects for a coordinator.
run_hook_matrix_parallel allowed coordinator-rejected-batches \
  "bash hooks/tests/test-eci-fast-path.sh && bash -c 'true'" \
  "bash -x hooks/tests/test-eci-review-gate.sh 2>&1 | cat" \
  "bash -x hooks/tests/test-eci-review-gate.sh > $TMP_ROOT/trace" \
  "find -P $ROOT -maxdepth 1 -name AGENTS.md -o -name AGENTS.md 2>/dev/null | head -n 5" \
  "ls -la $ROOT > $TMP_ROOT/read-output"
run_hook_matrix_parallel allowed coordinator-transparent-shell-wrappers \
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
run_subagent_matrix_parallel allowed worker-branch-remote-tail \
  "git config user.name worker" \
  "git branch -d doomed" \
  "git branch --abbrev feature" \
  "git branch --column feature" \
  "git branch --color feature" \
  "git branch --format --delete" \
  "git remote add origin https://example.invalid/repo.git"
run_hook_matrix_parallel allowed coordinator-lifecycle-location \
  "${HOME:?}/tmp/eci-active off $TMP_ROOT/disengage.md" \
  "PATH=/tmp eci-active off $TMP_ROOT/disengage.md" \
  "ECI_HOME=/tmp eci-active off $TMP_ROOT/disengage.md" \
  "env PATH=/tmp eci-active off $TMP_ROOT/disengage.md" \
  "env -i eci-active off $TMP_ROOT/disengage.md" \
  "env -u PATH eci-active off $TMP_ROOT/disengage.md" \
  "command -p eci-active off $TMP_ROOT/disengage.md"

# WITHHELD (TD classifier/P5a coordinator inherited admissions): 4 coordinator-git-context-unknown rows; both trees admit.
run_hook_matrix_parallel allowed coordinator-git-read-only-finite-controls \
  "git status --short" \
  "git log -1" \
  "git diff --check" \
  "git grep -n needle -- hooks" \
  "git ls-files" \
  "git rev-parse HEAD" \
  "git branch --all --contains HEAD"
# WITHHELD (TD classifier/P5a coordinator inherited admissions): coordinator-git-read-only-execution-escapes (6 rows); both trees admit.
# The --no-index row moves to coordinator-git-inspection-spellings (allowed) below.
run_matrix_parallel coordinator assert_allowed coordinator-git-commit \
  "git commit -m 'bounded message'" \
  "git commit --message=bounded" \
  "git commit --amend" \
  "git commit -c prior-message"
run_matrix_parallel coordinator assert_allowed coordinator-git-commit-all \
  "git commit -a --amend --no-verify --signoff"
# WITHHELD (TD classifier/P5a coordinator inherited admissions): 2 GIT_EXTERNAL_DIFF rows of coordinator-git-environment-context; both trees admit.
# Contract :179-181 / 575bb81 classifier.go:3758-3759: ordinary Git inspection,
# including pager config, --no-index reads and an environment-selected repository.
run_hook_matrix_parallel allowed coordinator-git-inspection-spellings \
  "git -c core.pager=cat status --short" \
  "git diff --check --no-index /etc/passwd /etc/hosts" \
  "env GIT_DIR=/tmp/other.git git status --short"
assert_allowed ""
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
# Contract :179-181 / 575bb81 classifier.go:3746-3747.
run_hook_matrix_parallel allowed coordinator-shell-launcher-near-misses \
  "env FOO=bar bash hooks/tests/test-not-allowlisted.sh" \
  "bash -c 'true'"
# The coordinator inspection route admits bounded read-only inspection of
# approved repository sources as well as proof-root evidence.
run_hook_matrix_parallel allowed coordinator-source-read \
  "sed -n '1p' $ROOT/hooks/validate-bash.sh"
run_hook_matrix_parallel allowed coordinator-sed-shapes \
  "sed -n '1d' $high_level_log" \
  "sed -n '1p' $high_level_log $high_level_log" \
  "sed -n '1p' -" \
  "sed -n '1p' $proof_root/t00-session/../t00-session/high_level_log.jsonl"
assert_allowed "awk '{print 1}' $ROOT/hooks/validate-bash.sh"
parameter_output="$(run_hook 'printf "%s" "$UNTRUSTED_COMMAND"')"
[ ! -s "$parameter_output" ]
# Preserve the multiline parser regression without sending its cleanup target
# through the prohibited system temporary root.
assert_allowed $'cat /dev/null\nrm -f '"$classifier_tmp_parent"'/eci-multiline-marker'
assert_allowed $'cat /dev/null\nrm -f /tmp/eci-multiline-marker'
assert_allowed $'sh -c "cat /dev/null\nrm -f /tmp/eci-nested-marker"'
assert_allowed $'eval "cat /dev/null\nrm -f /tmp/eci-eval-marker"'

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

# Current-session appends remain ordinary callback work. Marker-era ledger
# rewrite and anchor ownership checks are retained only by the direct planner.
assert_allowed "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"appended\"}' >> $high_level_log"
assert_allowed "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"appended\"}' 2>> $high_level_log" run_subagent_hook
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
[ ! -s "$outside_alias_output" ]
before_log="$(cat -- "$high_level_log")"
# The real route performs the append and advances its anchor under the
# mutation lock; its output is intentionally not admitted as a raw redirect.
ledger_append_from_marker_cwd "$proof_root" t00-session 'append-route' >/dev/null
append_route_line="$(tail -n 1 -- "$context_ledger")"
jq -e '.schema == "eci-high-level-log/v1" and .event == "decision" and .summary == "append-route" and (.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))' <<<"$append_route_line" >/dev/null

# The lifecycle route preflights the total byte budget before writing. A
# nearly-full anchored log must remain byte-identical when the next entry
# would cross the cap.

# A middle insertion, replacement, or truncation invalidates the anchored
# prefix and must not be admitted as another append. Restore the exact bytes
# and anchor between probes so the final positive route remains meaningful.
stable_log="$(cat -- "$high_level_log")"
stable_anchor="$(cat -- "$proof_root/t00-session/high_level_log.anchor")"
printf '%s\n%s' 'forged middle' "$stable_log" >"$high_level_log"
printf '%s' "$stable_log" >"$high_level_log"
printf '%s' "$stable_anchor" >"$proof_root/t00-session/high_level_log.anchor"
: >"$high_level_log"
printf '%s' "$stable_log" >"$high_level_log"
printf '%s' "$stable_anchor" >"$proof_root/t00-session/high_level_log.anchor"

# Keep the user-authorized Git reset/worktree boundary in the focused
# classifier suite so its red/green behavior is exercised by the allowlisted
# test entry point.
bash "$ROOT/hooks/tests/test-validate-bash-git-approvals.sh"
bash "$ROOT/hooks/tests/test-coordinator-protected-git-target.sh"
printf '%s\n' 'validate-bash classifier tests: PASS'

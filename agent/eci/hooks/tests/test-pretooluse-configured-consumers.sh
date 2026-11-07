#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-configured-pretooluse.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

FIXTURE_HOME="$TMP_ROOT/home"
FIXTURE_ROOT="$FIXTURE_HOME/.omp/agent/eci"
FIXTURE_KIMI_ROOT="$FIXTURE_HOME/.kimi-code"
FIXTURE_KIMI_CONFIG="$FIXTURE_KIMI_ROOT/config.toml"
FIXTURE_PROOF="$TMP_ROOT/proof"
FIXTURE_SESSION='configured-pretooluse'
FIXTURE_KIMI_SESSION='configured-kimi-pretooluse'
FIXTURE_CONFIG="$TMP_ROOT/config"
FIXTURE_STATE="$TMP_ROOT/state"
LIVE_KIMI_ROOT="${KIMI_CODE_HOME:-${HOME:?}/.kimi-code}"
LIVE_KIMI_CONFIG="$LIVE_KIMI_ROOT/config.toml"
live_kimi_config_before="$(sha256sum -- "$LIVE_KIMI_CONFIG" | awk '{print $1}')"

mkdir -p -- "$FIXTURE_ROOT" "$FIXTURE_KIMI_ROOT" "$FIXTURE_PROOF/$FIXTURE_SESSION" \
  "$FIXTURE_PROOF/$FIXTURE_KIMI_SESSION" "$FIXTURE_CONFIG/eci" "$FIXTURE_STATE"
cp -a -- "$ROOT/hooks" "$FIXTURE_ROOT/hooks"
cp -a -- "$ROOT/hooks" "$FIXTURE_KIMI_ROOT/hooks"
cp -- "$LIVE_KIMI_CONFIG" "$FIXTURE_KIMI_CONFIG"
mkdir -p -- "$FIXTURE_ROOT/bin"
cp -- "$ROOT/bin/eci-runtime-sync" "$FIXTURE_ROOT/bin/eci-runtime-sync"
sed -i '2{/^exit 0$/d;}' -- "$FIXTURE_ROOT/hooks/validate-bash.sh"
sed -i '2{/^exit 0$/d;}' -- "$FIXTURE_ROOT/hooks/pretooluse-edit-dispatch.sh"
sed -i '2{/^exit 0$/d;}' -- "$FIXTURE_KIMI_ROOT/hooks/validate-bash.sh"
printf '%s\n' enforcing >"$FIXTURE_CONFIG/eci/command-gate-mode"
printf 'scope: configured consumer regression\ncwd: %s\nsession_id: %s\ncreated_utc: 2026-09-13T00:00:00Z\n' \
  "$FIXTURE_ROOT" "$FIXTURE_SESSION" >"$FIXTURE_PROOF/$FIXTURE_SESSION/eci_active"
printf 'scope: configured Kimi consumer regression\ncwd: %s\nsession_id: %s\ncreated_utc: 2026-09-13T00:00:00Z\n' \
  "$FIXTURE_KIMI_ROOT" "$FIXTURE_KIMI_SESSION" >"$FIXTURE_PROOF/$FIXTURE_KIMI_SESSION/eci_active"
FIXTURE_KIMI_MARKER="$(realpath -e -- "$FIXTURE_PROOF/$FIXTURE_KIMI_SESSION/eci_active")"

# Build the canonical fixture planner through the ordinary private publisher,
# then remove the Kimi fixture's local planner directory. The configured Kimi
# callback must therefore consume the coherent canonical planner authority.
HOME="$FIXTURE_HOME" KIMI_CODE_HOME="$FIXTURE_KIMI_ROOT" \
  "$FIXTURE_ROOT/bin/eci-runtime-sync" planner-apply --target "$FIXTURE_KIMI_ROOT" >/dev/null
rm -rf -- "$FIXTURE_KIMI_ROOT/hooks/lib/eci-command-plan-go"
mkdir -p -- "$FIXTURE_KIMI_ROOT/hooks/lib/eci-command-plan-go"
KIMI_LOCAL_PLANNER="$FIXTURE_KIMI_ROOT/hooks/lib/eci-command-plan-go/eci-command-plan"
KIMI_LOCAL_PLANNER_SENTINEL="$TMP_ROOT/kimi-local-planner-selected"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf selected >"${KIMI_LOCAL_PLANNER_SENTINEL:?}"' \
  'printf "%s\\n" "{\\"hookSpecificOutput\\":{\\"permissionDecision\\":\\"deny\\"}}"' \
  >"$KIMI_LOCAL_PLANNER"
chmod 755 -- "$KIMI_LOCAL_PLANNER"

cat >"$FIXTURE_ROOT/hooks/validate-apply-patch.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"synthetic configured apply_patch validator denial"}}'
EOF
cat >"$FIXTURE_ROOT/hooks/validate-edit-write.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"synthetic configured edit validator denial"}}'
EOF
cat >"$FIXTURE_ROOT/hooks/eci-active-gate.sh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod 755 -- "$FIXTURE_ROOT/hooks/validate-apply-patch.sh" \
  "$FIXTURE_ROOT/hooks/validate-edit-write.sh" "$FIXTURE_ROOT/hooks/eci-active-gate.sh"

run_configured_consumer() {
  local matcher="$1" input="$2" required_reason="$3" callback output

  callback="$(jq -er --arg matcher "$matcher" \
    '.hooks.PreToolUse[] | select(.matcher == $matcher) | .hooks[] | select(.type == "command") | .command' \
    "$ROOT/hooks.json")"
  output="$TMP_ROOT/${matcher//[^[:alnum:]]/_}.json"
  printf '%s' "$input" |
    HOME="$FIXTURE_HOME" ECI_HOME="$FIXTURE_ROOT" KIMI_CODE_HOME="$FIXTURE_KIMI_ROOT" \
      ECI_PROOF_ROOT="$FIXTURE_PROOF" KIMI_PROOF_ROOT="$FIXTURE_PROOF" \
      XDG_CONFIG_HOME="$FIXTURE_CONFIG" XDG_STATE_HOME="$FIXTURE_STATE" \
      bash -c 'cd -- "$1" && exec bash -c "$2"' _ "$FIXTURE_ROOT" "$callback" >"$output"
  jq -e --arg required_reason "$required_reason" '
    .hookSpecificOutput.hookEventName == "PreToolUse" and
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($required_reason))
  ' "$output" >/dev/null || {
    printf 'configured consumer did not reach its enabled guard: matcher=%s callback=%s\n' \
      "$matcher" "$callback" >&2
    cat -- "$output" >&2
    return 1
  }
}

run_configured_consumer_allow() {
  local matcher="$1" input="$2" callback output

  callback="$(jq -er --arg matcher "$matcher" \
    '.hooks.PreToolUse[] | select(.matcher == $matcher) | .hooks[] | select(.type == "command") | .command' \
    "$ROOT/hooks.json")"
  output="$TMP_ROOT/${matcher//[^[:alnum:]]/_}-allow.json"
  printf '%s' "$input" |
    HOME="$FIXTURE_HOME" ECI_HOME="$FIXTURE_ROOT" KIMI_CODE_HOME="$FIXTURE_KIMI_ROOT" \
      ECI_PROOF_ROOT="$FIXTURE_PROOF" KIMI_PROOF_ROOT="$FIXTURE_PROOF" \
      XDG_CONFIG_HOME="$FIXTURE_CONFIG" XDG_STATE_HOME="$FIXTURE_STATE" \
      bash -c 'cd -- "$1" && exec bash -c "$2"' _ "$FIXTURE_ROOT" "$callback" >"$output"
  [ ! -s "$output" ] || {
    printf 'enabled configured consumer denied an ordinary dynamic command: matcher=%s callback=%s\n' \
      "$matcher" "$callback" >&2
    cat -- "$output" >&2
    return 1
  }
}

for fixture_hook in "$FIXTURE_ROOT/hooks/validate-bash.sh" "$FIXTURE_KIMI_ROOT/hooks/validate-bash.sh"; do
  [ "$(sed -n '2p' "$fixture_hook")" != 'exit 0' ] || {
    printf 'fixture hook retained staged line-2 bypass: %s\n' "$fixture_hook" >&2
    exit 1
  }
done

run_configured_consumer '^Bash$' \
  "$(jq -cn --arg cwd "$FIXTURE_ROOT" --arg session "$FIXTURE_SESSION" \
    '{session_id:$session,cwd:$cwd,tool_input:{command:"rm -rf /"}}')" \
  ECI_BROAD_DESTRUCTIVE_DENIED
run_configured_consumer_allow '^Bash$' \
  "$(jq -cn --arg cwd "$FIXTURE_ROOT" --arg session "$FIXTURE_SESSION" \
    '{session_id:$session,cwd:$cwd,tool_input:{command:"interpreter-tool -c \u0027dynamic payload\u0027"}}')"
run_configured_consumer '^apply_patch$' \
  "$(jq -cn --arg cwd "$FIXTURE_ROOT" --arg session "$FIXTURE_SESSION" \
    '{session_id:$session,cwd:$cwd,tool_name:"apply_patch",tool_input:{patch:"*** Begin Patch"}}')" \
  'synthetic configured apply_patch validator denial'
run_configured_consumer '^(Edit|Write|MultiEdit|NotebookEdit)$' \
  "$(jq -cn --arg cwd "$FIXTURE_ROOT" --arg session "$FIXTURE_SESSION" \
    '{session_id:$session,cwd:$cwd,tool_name:"Edit",tool_input:{file_path:"notes.txt",old_string:"old",new_string:"new"}}')" \
  'synthetic configured edit validator denial'

kimi_bash_consumer="$(awk '
  $0 == "[[hooks]]" { event = 0; matcher = 0; next }
  index($0, "event = \"PreToolUse\"") == 1 { event = 1; next }
  index($0, "matcher = \"^Bash$\"") == 1 { matcher = 1; next }
  event && matcher && $0 ~ /^[[:space:]]*command[[:space:]]*=/ {
    value = $0
    sub(/^[^=]*=[[:space:]]*"/, "", value)
    sub(/"[[:space:]]*$/, "", value)
    gsub(/\\"/, "\"", value)
    if (value != "") { print value; found = 1; exit }
  }
  END { if (!found) exit 1 }
' "$FIXTURE_KIMI_CONFIG")" || {
  printf 'missing Kimi PreToolUse /^Bash$/ command in fixture config: %s\n' "$FIXTURE_KIMI_CONFIG" >&2
  exit 1
}

run_kimi_bash_consumer() {
  local label="$1" command="$2" expected="$3" output input

  input="$TMP_ROOT/kimi-$label-input.json"
  output="$TMP_ROOT/kimi-$label-output.json"
  jq -cn --arg cwd "$FIXTURE_KIMI_ROOT" --arg session "$FIXTURE_KIMI_SESSION" --arg command "$command" \
    '{session_id:$session,cwd:$cwd,tool_input:{command:$command}}' >"$input"
  if ! timeout 75s env \
    HOME="$FIXTURE_HOME" ECI_HOME="$FIXTURE_ROOT" KIMI_CODE_HOME="$FIXTURE_KIMI_ROOT" \
    ECI_PROOF_ROOT="$FIXTURE_PROOF" KIMI_PROOF_ROOT="$FIXTURE_PROOF" \
    KIMI_LOCAL_PLANNER_SENTINEL="$KIMI_LOCAL_PLANNER_SENTINEL" \
    XDG_CONFIG_HOME="$FIXTURE_CONFIG" XDG_STATE_HOME="$FIXTURE_STATE" \
    bash -c 'cd -- "$1" && exec bash -c "$2"' _ "$FIXTURE_KIMI_ROOT" "$kimi_bash_consumer" <"$input" >"$output"; then
    printf 'configured Kimi Bash consumer did not finish within bounded timeout: %s\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
  case "$expected" in
    allow)
      [ ! -s "$output" ] || {
        printf 'configured Kimi Bash consumer denied an ordinary command: %s\n' "$command" >&2
        cat -- "$output" >&2
        return 1
      }
      ;;
    broad-deny)
      jq -e --arg session "$FIXTURE_KIMI_SESSION" --arg cwd "$FIXTURE_KIMI_ROOT" \
        --arg marker "$FIXTURE_KIMI_MARKER" '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]")) and
        (.hookSpecificOutput.permissionDecisionReason | contains("callback_session=" + $session)) and
        (.hookSpecificOutput.permissionDecisionReason | contains("callback_cwd=" + $cwd)) and
        (.hookSpecificOutput.permissionDecisionReason | contains("callback_marker=" + $marker))
      ' "$output" >/dev/null || {
        printf 'configured Kimi Bash consumer did not report broad-target denial: %s\n' "$command" >&2
        cat -- "$output" >&2
        return 1
      }
      ;;
    *)
      printf 'unknown Kimi Bash expected result: %s\n' "$expected" >&2
      return 2
      ;;
  esac
}

run_kimi_bash_consumer ordinary 'env | sort' allow
run_kimi_bash_consumer dynamic-allow "interpreter-tool -c 'dynamic payload'" allow
run_kimi_bash_consumer broad-target 'rm -rf /' broad-deny
[ ! -e "$KIMI_LOCAL_PLANNER_SENTINEL" ] || {
  printf 'configured Kimi Bash consumer selected its local planner instead of canonical ECI authority\n' >&2
  exit 1
}
[ "$(sha256sum -- "$LIVE_KIMI_CONFIG" | awk '{print $1}')" = "$live_kimi_config_before" ] || {
  printf 'configured Kimi consumer test changed live Kimi config: %s\n' "$LIVE_KIMI_CONFIG" >&2
  exit 1
}

printf '%s\n' 'configured PreToolUse consumers: PASS'

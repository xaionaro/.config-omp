#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
CANONICAL_ECI_HOME="${HOME:?HOME must be set}/.omp/agent/eci"
HOOKS_JSON="$CANONICAL_ECI_HOME/hooks.json"
CONFIG_TOML="$CANONICAL_ECI_HOME/config.toml"
LAUNCHERS=(
  "$HOME/.local/bin/eci"
  "$HOME/bin/eci"
)
EXPECTED_HOOK_COMMANDS=(
  "bash -lc 'exec \"\$HOME/.omp/agent/eci/hooks/session-snapshot.sh\"'"
  "bash -c 'exec \"\$HOME/.omp/agent/eci/hooks/eci-post-compact-refresh.sh\"'"
  "bash -lc 'exec \"\$HOME/.omp/agent/eci/hooks/prompt-task-reminder.sh\"'"
  "bash \"\$HOME/.omp/agent/eci/hooks/validate-bash.sh\""
  "bash \"\$HOME/.omp/agent/eci/hooks/pretooluse-edit-dispatch.sh\""
  "bash \"\$HOME/.omp/agent/eci/hooks/pretooluse-edit-dispatch.sh\""
)
failures=0

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

[ "$ROOT" = "$CANONICAL_ECI_HOME" ] ||
  fail "test root must be the HOME-derived canonical ECI home: root=$ROOT home=$CANONICAL_ECI_HOME"

for launcher in "${LAUNCHERS[@]}"; do
  if ! grep -Eq '^[[:space:]]*export[[:space:]]+ECI_HOME="\$HOME/\.omp/agent/eci"[[:space:]]*$' "$launcher"; then
    fail "$launcher must force ECI_HOME=\"\$HOME/.omp/agent/eci\""
  fi
done

# The contract is intentionally evaluated under a different HOME rather than
# relying on the account used to run this test. The exact launcher line above
# is then the only source of the selected ECI home.
controlled_home='/eci-runtime-single-source-controlled-home'
controlled_eci_home="$(
  env -i HOME="$controlled_home" PATH=/usr/bin:/bin \
    bash -c 'export ECI_HOME="$HOME/.omp/agent/eci"; printf %s "$ECI_HOME"'
)"
[ "$controlled_eci_home" = "$controlled_home/.omp/agent/eci" ] ||
  fail "HOME-derived launcher contract expanded to $controlled_eci_home"

if ! grep -Fqx 'Also follow `$HOME/.omp/agent/AGENTS.md` as user-level guidance when it does not conflict with higher-priority ECI instructions.' "$CONFIG_TOML"; then
  fail "developer guidance must select the literal \$HOME/.omp/agent/AGENTS.md source"
fi
if ! grep -Fqx 'Ported skills live in `$HOME/.omp/agent/skills`; use matching skills for Go, Python, tests, debugging, UI, Android, porting, skill/prompt work, and explicit delegated-agent work.' "$CONFIG_TOML"; then
  fail "developer guidance must select the literal \$HOME/.omp/agent/skills source"
fi
if grep -Fq '$ECI_HOME/' "$CONFIG_TOML"; then
  fail "developer guidance must not select an inherited ECI_HOME source"
fi

if ! jq -e '
  [.hooks | .. | objects | select(.type? == "command") | .command] |
  length > 0 and all(.[]; type == "string")
' "$HOOKS_JSON" >/dev/null; then
  fail "$HOOKS_JSON must contain command hooks"
else
  mapfile -t hook_commands < <(
    jq -r '.hooks | .. | objects | select(.type? == "command") | .command' "$HOOKS_JSON"
  )
  if [ "${#hook_commands[@]}" -ne "${#EXPECTED_HOOK_COMMANDS[@]}" ]; then
    fail "$HOOKS_JSON changed its configured hook command count"
  fi
  for index in "${!EXPECTED_HOOK_COMMANDS[@]}"; do
    hook_command="${hook_commands[$index]:-}"
    if [ "$hook_command" != "${EXPECTED_HOOK_COMMANDS[$index]}" ]; then
      fail "$HOOKS_JSON changed hook target or HOME-rooted launcher at index $index: $hook_command"
    fi
    case "$hook_command" in
      *'$HOME/.omp/agent/eci/hooks/'*) ;;
      *) fail "hook command must use the literal \$HOME/.omp/agent/eci/hooks/ root: $hook_command" ;;
    esac
    case "$hook_command" in
      *'/home/'*|*'/mnt/'*|*'${ECI_HOME'*|*'$ECI_HOME'*|*'~/.omp/agent/eci'*)
        fail "hook command must not select a fallback or alternate ECI root: $hook_command"
        ;;
    esac
  done
fi

if [ "$failures" -ne 0 ]; then
  exit 1
fi

printf 'canonical ECI runtime selection assertions: PASS\n'

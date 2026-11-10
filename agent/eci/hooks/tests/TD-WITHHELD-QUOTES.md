# Withheld and moved-out assertions: verbatim quotes for restoration

Line numbers are advisory. Blocks retain their original content; each section states whether restoration remains appropriate.

## test-coordinator-protected-git-target.sh (historical)

These verbatim historical assertions describe retired protected-target and marker-scope behavior. They are not instructions to restore a denial: the Bash callback no longer denies protected hook checkout, removal, or relocation because those checks lacked a bounded legitimate hatch.

### Inherited admissions, withheld (37 rows; both trees allow, deny expected)

These historical rows expected the retired protected-target denial. Do not restore them as denials; the `printf` line shows the original value-file fixture.

### v2 lines 269-270 (after: `assert_protected_denial 'git rm -r -- hooks/validate-bash.sh'`)
```bash
assert_protected_denial 'git rm -r -- hooks'
assert_protected_denial 'git rm -r -- .'
```

### v2 lines 272-272 (after: `assert_protected_denial 'git mv -- hooks/validate-bash.sh hooks/renamed-hook.sh'`)
```bash
assert_protected_denial 'git mv -- hooks hooks-renamed'
```

### v2 lines 277-280 (after: `assert_protected_denial 'nice -n 5 git rm -- hooks/validate-bash.sh'`)
```bash
assert_protected_denial 'git rm -- :/hooks/validate-bash.sh'
assert_protected_denial "git rm -- ':(top)hooks/validate-bash.sh'"
assert_protected_denial 'git rm -r -- :/'
assert_protected_denial "git rm -r -- ':(top)'"
```

### v2 lines 286-288 (after: `assert_protected_denial 'git rm -- hooks/validate-bash.sh > /tmp/eci-protected-target-test`)
```bash
assert_protected_denial 'git restore -- hooks/validate-bash.sh'
assert_protected_denial 'git restore --source=HEAD -- hooks/validate-bash.sh'
assert_protected_denial 'git restore --source HEAD -- hooks/validate-bash.sh'
```

### v2 lines 293-294 (after: `assert_protected_denial 'git checkout --detach --no-detach HEAD -- hooks/validate-bash.sh'`)
```bash
assert_protected_denial 'git checkout hooks/validate-bash.sh'
assert_protected_denial 'git checkout ./hooks/validate-bash.sh'
```

### v2 lines 300-301 (after: `assert_protected_denial_with_timeout 10 'git checkout --patc --unified=3 -- hooks/validate`)
```bash
assert_protected_denial "git checkout --pathspec-from-file=\"$protected_pathspec_file\" --"
assert_protected_denial "git checkout --pathspec-from-file \"$protected_pathspec_file\" --"
```

### v2 lines 309-313 (after: `done`)
```bash
printf '%s\n' 'hooks/validate-bash.sh' >"$literal_dash_target_file"
for pathspec_option in --pathspec-fr --pathspec-from --pathspec-from-f --pathspec-from-fi --pathspec-from-fil --pathspec-from-file; do
  assert_protected_denial "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option --"
done
assert_protected_denial "git --work-tree=\"$ROOT\" restore -- hooks/validate-bash.sh"
```

### v2 lines 315-315 (after: `assert_protected_denial "git --work-tree=\"$ROOT\" checkout -- hooks/validate-bash.sh"`)
```bash
assert_protected_denial "GIT_WORK_TREE=\"$ROOT\" git restore -- hooks/validate-bash.sh"
```

### v2 lines 317-328 (after: `assert_protected_denial "GIT_WORK_TREE=\"$ROOT\" git checkout -- hooks/validate-bash.sh"`)
```bash
assert_protected_denial "git --git-dir=\"$ROOT/.git\" --work-tree=\"$ROOT\" restore -- hooks/validate-bash.sh"
assert_protected_denial "git rm --pathspec-from-file=\"$protected_pathspec_file\""
assert_protected_denial "git rm --pathspec-from-file \"$protected_pathspec_file\""
assert_protected_denial "git rm --pathspec-fr=\"$protected_pathspec_file\""
assert_protected_denial "git rm --pathspec-fr \"$protected_pathspec_file\""
assert_protected_denial "git rm --pathspec-from-file=\"$protected_pathspec_nul_file\" --pathspec-file-nul"
assert_protected_denial "git restore --pathspec-from-file=\"$protected_pathspec_file\""
assert_protected_denial "git checkout --pathspec-from-file=\"$protected_pathspec_file\""
assert_protected_denial "git checkout --pathspec-fr=\"$protected_pathspec_file\""
assert_protected_denial "git checkout --pathspec-fr \"$protected_pathspec_file\""
assert_protected_denial "git checkout --pathspec-fr=\"$protected_pathspec_file\" --unified=-1"
assert_protected_denial "git checkout --pathspec-from-file=\"$protected_pathspec_nul_file\" --pathspec-file-nul"
```

### v2 lines 336-337 (after: `assert_protected_denial 'bash -c "git rm -- hooks/validate-bash.sh"'`)
```bash
assert_protected_denial 'sh -c "git restore -- hooks/validate-bash.sh"'
assert_protected_denial 'env bash -c "git checkout hooks/validate-bash.sh"'
```

### v2 lines 411-411 (after: `assert_allowed 'git checkout --patch --inter-hunk-context=3x -- hooks/validate-bash.sh'`)
```bash
assert_protected_denial 'git checkout --conflict merge hooks/validate-bash.sh'
```

### Inherited over-strict denies, moved out (32 rows; both trees deny, allow expected)

These historical rows expected allow against the retired protected-target denial. They remain historical examples, not instructions to restore that predicate.

### v2 lines 295-297 (after: `assert_protected_denial 'git checkout ./hooks/validate-bash.sh'`)
```bash
assert_allowed 'git checkout -b iter10-invalid HEAD -- hooks/validate-bash.sh'
assert_allowed 'git checkout -B iter10-invalid HEAD -- hooks/validate-bash.sh'
assert_allowed 'git checkout --orphan iter10-invalid HEAD -- hooks/validate-bash.sh'
```

### v2 lines 345-345 (after: `assert_protected_denial_at_session "$alias_session" "$alias_cwd" 'git rm -- hooks/validate`)
```bash
assert_allowed "git rm -- hooks/$(basename -- "$symlink_alias")"
```

### v2 lines 356-359 (after: `assert_allowed 'git checkout --no-detach=foo hooks/validate-bash.sh'`)
```bash
assert_allowed 'git checkout --detach=foo --no-detach HEAD -- hooks/validate-bash.sh'
assert_allowed 'git checkout --detach=foo --no-detach -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch=foo --unified=3 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --no-patc --unified=3 -- hooks/validate-bash.sh'
```

### v2 lines 362-364 (after: `assert_allowed "git checkout --pathspec-from-file=\"$protected_pathspec_file\" --patch"`)
```bash
for patch_option in --p --pa --pat; do
  assert_allowed "git checkout $patch_option --unified=3 -- hooks/validate-bash.sh"
done
```

### v2 lines 369-371 (after: `assert_allowed 'git checkout -B --no-detach hooks/validate-bash.sh'`)
```bash
assert_allowed 'git checkout --orphan --no-detach -- hooks/validate-bash.sh'
assert_allowed 'git checkout -b --no-detach -- hooks/validate-bash.sh'
assert_allowed 'git checkout -B --no-detach -- hooks/validate-bash.sh'
```

### v2 lines 391-393 (after: `assert_protected_denial 'git checkout --inter-hunk-context=-1 -- hooks/validate-bash.sh'`)
```bash
assert_allowed 'git checkout --unified=-0 -- hooks/validate-bash.sh'
assert_allowed 'git checkout -U-0 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --inter-hunk-context=-0 -- hooks/validate-bash.sh'
```

### v2 lines 397-397 (after: `assert_protected_denial 'git checkout --patch --inter-hunk-context=-0 -- hooks/validate-ba`)
```bash
assert_allowed 'git checkout --unified=-1 --unified=3 -- hooks/validate-bash.sh'
```

### v2 lines 399-399 (after: `assert_protected_denial 'git checkout --unified=3 --unified=-1 -- hooks/validate-bash.sh'`)
```bash
assert_allowed 'git checkout -U-1 -U3 -- hooks/validate-bash.sh'
```

### v2 lines 401-410 (after: `assert_protected_denial 'git checkout -U3 -U-1 -- hooks/validate-bash.sh'`)
```bash
assert_allowed 'git checkout --inter-hunk-context=3 --unified=-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --unified=-1 --inter-hunk-context=3 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=-2 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=-3 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --inter-hunk-context=-2 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=bogus -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=3x -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=999999999999999999999999999999999999999999999999999 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch -Ubogus -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --inter-hunk-context=3x -- hooks/validate-bash.sh'
```

### v2 lines 425-427 (after: `assert_allowed 'git rm -- hooks/tests/test-coordinator-protected-git-target.sh'`)
```bash
assert_allowed 'git mv -- hooks/validate-bash.sh hooks/validate-bash.sh'
assert_allowed 'git mv -- hooks/validate-bash.sh ../outside'
assert_allowed 'git mv -- hooks/missing-source hooks/validate-bash.sh'
```

### Inherited cross-scope denies, moved out (49 rows; both trees deny ECI_GIT_CROSS_SCOPE_DENIED predicate=git-cross-scope)

These historical rows describe marker-based cross-scope behavior, which the Bash callback no longer activates. Four protected-denial rows also refer to the retired protected-target gate; do not restore those denials.

### v2 lines 302-308 (after: `assert_protected_denial "git checkout --pathspec-from-file \"$protected_pathspec_file\" --`)
```bash
for pathspec_option in --pathspec-fr --pathspec-from --pathspec-from-f --pathspec-from-fi --pathspec-from-fil --pathspec-from-file; do
  assert_allowed_at_session "$literal_dash_session" "$literal_dash_cwd" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option --"
  assert_allowed_at_session "$literal_dash_session" "$literal_dash_cwd" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option -- hooks/validate-bash.sh"
  assert_allowed_at_session "$literal_dash_session" "$literal_dash_cwd" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option -- --bogus"
  assert_allowed_at_session "$literal_dash_session" "$literal_dash_cwd" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option -- -z"
  assert_allowed_at_session "$literal_dash_missing_session" "$literal_dash_missing_cwd" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option --"
done
```

### v2 lines 329-334 (after: `assert_protected_denial "git checkout --pathspec-from-file=\"$protected_pathspec_nul_file\`)
```bash
for pathspec_option in --pathspec-fr --pathspec-from --pathspec-from-f --pathspec-from-fi --pathspec-from-fil --pathspec-from-file; do
  assert_allowed_at_session "$pathspec_value_session" "$TMP_ROOT" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option --detach"
  assert_allowed_at_session "$pathspec_value_session" "$TMP_ROOT" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout $pathspec_option=--detach"
done
assert_allowed_at_session "$pathspec_value_session" "$TMP_ROOT" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout --pathspec-from-file -- hooks/validate-bash.sh"
assert_allowed_at_session "$pathspec_value_session" "$TMP_ROOT" "git --work-tree=\"$ROOT\" --git-dir=\"$ROOT/.git\" checkout --pathspec-from-file"
```

### v2 lines 339-343 (after: `assert_protected_denial 'bash -c "echo before; git rm -- hooks/validate-bash.sh"'`)
```bash
assert_protected_denial_at "$hooks_cwd" 'git -C .. rm -- hooks/validate-bash.sh'
assert_protected_denial_at "$hooks_cwd" 'env -C .. git rm -- hooks/validate-bash.sh'
assert_protected_denial_at "$hooks_cwd" 'sudo -D .. git rm -- hooks/validate-bash.sh'
assert_protected_denial_at "$hooks_cwd" 'sudo --chdir .. git rm -- hooks/validate-bash.sh'
assert_allowed_at "$hooks_cwd" 'sudo -C 3 git rm -- hooks/validate-bash.sh'
```

## classifier P1 C6: dynamic find actions, kept rows (worker -delete, -exec true; coordinator -exec /tmp/file, -exec ../file; sweep-P1P2 rev 3 D2/D3)

```bash
run_matrix_parallel worker assert_worker_dynamic_find_action_denied worker-dynamic-find-action \
  "find /tmp -maxdepth 1 -type d -delete" \
  "find /tmp -maxdepth 1 -type d -exec true \\;"
run_matrix_parallel coordinator assert_coordinator_dynamic_find_action_denied coordinator-dynamic-find-actions \
  "find /tmp -maxdepth 1 -type f -exec /tmp/file --brief {} \\;" \
  "find /tmp -maxdepth 1 -type f -exec ../file --brief {} \\;"
```

## classifier P2 C4: worker-gate-entrypoints (foreign-root gate scripts; helpers and matrix)

```bash
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
```

## classifier P2 C5: BASH_ENV injection (coordinator and worker; fragment 2 is P3 row :2810 below)

```bash
run_matrix_parallel coordinator assert_any_denied coordinator-environment-injection \
  "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "env BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "env -S 'BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh'"
[ ! -e "$TMP_ROOT/bash-env-ran" ]
output="$(run_subagent_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh")"
jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null
[ ! -e "$TMP_ROOT/bash-env-ran" ]
run_matrix_parallel worker assert_any_denied worker-environment-injection \
  "env BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh" \
  "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh"
```

## classifier P2 C6: worker-shell-lifecycle original matrix (rows 4, 5, 7 withheld) and the find -exec check

```bash
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
```

## classifier P3: inherited admissions kept withheld (35 items: 34 rows + cap block; sweep-P3Nested rev 3), copied verbatim from P3/TD-WITHHELD-QUOTES.md


Source: the pre-withhold integration copy of `test-validate-bash-classifier.sh` (3734 lines, sha256 2470248fd3707133…). "Row" is the probe7 numbering. "now lines" is the range in that copy.

**Line ranges are advisory.** w/ keeps changing. Restore by content: paste each block back in place, after the surrounding context it names. Every block is byte-exact.

### classifier P3: deleted helpers needed by quotes (restore-only)

Verbatim from base 3cefa24 `test-validate-bash-classifier.sh` :785, :821, :901-903, :1326 and :2962. They are not live; restore them only together with the quotes below that call them (`assert_instruction_read_denied`, `assert_worker_environment_case`, `assert_eci_lifecycle_spelling_denied`). Their wording pins are carried as-is.

```bash
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

assert_worker_environment_case() {
  assert_environment_case worker "$@"
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
```

### classifier P3: wrapper/indirect control writes

Historical active-marker callback expectations; do not restore these as Bash callback denials.

#### Row :3105 rows :3117,:3118,:3119,:3122: now lines 3095,3096,3097,3100 (inside the matrix at line 3083; only these 4 rows are withheld)
```bash
run_matrix_parallel worker assert_subagent_control_denied worker-control-mutation \
  "timeout 5 bash -c 'printf forged > $proof_root/t00-session/eci_wait'" \
  "env -S 'printf forged > $proof_root/t00-session/eci-required-critics.json'" \
  "find . -exec rm -f $proof_root/t00-session/eci-teardown-complete \\;" \
  "touch $proof_root/t00-session/eci-required-critics.commit.1.ledger" \
```

### classifier P3: symlink control writes

Historical active-marker callback expectations; do not restore these as Bash callback denials.

#### Row :3139 (2 rows): now lines 3117-3119
```bash
run_matrix_parallel worker assert_subagent_control_denied worker-control-symlink \
  "rm -f $proof_root/t00-session/eci_wait-link" \
  "printf forged > $proof_root/t00-session/eci_wait-link"
```

### classifier P3: instruction-read

#### Row :3298 (2 kept rows; missing and fifo moved to (a)): now lines 3276-3280
```bash
run_matrix_parallel worker assert_instruction_read_denied worker-instruction-failures \
  "cat $external_skill_root/SKILL.md" \
  "cat $subagent_eci_home/skills/escape/SKILL.md"
```

#### History pathspec near-miss loop (worker `git diff --check -- AGENTS.md`, `git log --format=%H -- ../AGENTS.md`; same family, added at integration; both 575bb81 and 42f3b1f planners admit both, run)
```bash
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
```

### classifier P3: worker environment

#### Row :3396 (13 kept rows; GIT_DIR git status moved to (a)): now lines 3374-3388
```bash
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
  "printenv PATH | env"
```

### classifier P3: dispatcher

#### Row :3527 (3 rows): now lines 3505-3508
```bash
run_matrix_parallel worker assert_eci_lifecycle_spelling_denied worker-dispatcher-lifecycle-lookalikes \
  '$HOME/.omp/agent/eci/bin/eci-active-dispatch --help' \
  'eci-active-dispatch --help' \
  'env -- ECI_SESSION_ID=t00-session "$HOME/.omp/agent/eci/bin/eci-active-dispatch" --help'
```

### classifier P3: python -c control write

#### Row :2439: now lines 2416-2424
```bash
output="$(run_subagent_hook "python3 -c 'open(\"$proof_root/t00-session/eci_wait\",\"w\").write(\"x\")'")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=-c")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=1")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-interpreter-launch"))
' "$output" >/dev/null
```

### classifier P3: interpreter env context

#### Row :2794: now lines 2771-2772
```bash
output="$(run_subagent_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh")"
jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null
```

#### Row :2810: now lines 2787-2794
```bash
coordinator_environment_effect_output="$(run_hook "BASH_ENV=$bash_env_probe bash hooks/tests/test-eci-fast-path.sh")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_ENVIRONMENT_CONTEXT_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=environment-boundary")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=interpreter-context")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=BASH_ENV"))
' "$coordinator_environment_effect_output" >/dev/null
```

#### Row :3368: now lines 3341-3353
```bash
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
```

### classifier P3: dynamic launch

#### Row :2988: now lines 2965-2972
```bash
output="$(run_subagent_hook "find . -exec $subagent_eci_home/bin/eci-active off $TMP_ROOT/disengage.md \\;")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=-exec")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("predicate=dynamic-find-action"))
' "$output" >/dev/null
```

### classifier P3: tilde/symlink HOME control rm

#### Row :3156: now lines 3133-3139
```bash
output="$(run_subagent_hook_at_root "rm -f ~/tmp/agent/proof/t00-session/eci_active" "$tilde_root" "$tilde_home")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("resolved="))
' "$output" >/dev/null
```

#### Row :3173: now lines 3150-3156
```bash
output="$(run_subagent_hook_at_root "rm -f ~/tmp/agent/proof/t00-session/eci_active" "$proof_root" "$symlink_home")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("resolved="))
' "$output" >/dev/null
```

### classifier P3: git archive --output=eci_active

#### Row :3230: now lines 3207-3220
```bash
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
```

### classifier P3: raw >> tampered ledger

#### Row :3743: now lines 3721-3721
```bash
assert_unknown "printf '%s\\n' rejected >> $high_level_log"
```

#### Row :3747: now lines 3725-3725
```bash
assert_unknown "printf '%s\\n' rejected-after-truncate >> $high_level_log"
```

### classifier P3: no ledger cap

#### Row :3703-:3735 (cap block; :3732 is noise): now lines 3684-3713
```bash
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
```

## classifier P5a: coordinator inherited admissions (17 rows; both 575bb81 and 42f3b1f planners admit, run)

### coordinator-git-archive-fast-path-unsafe (w/ :2786-2791, whole matrix; P3 coordinator archive family)

```bash
run_hook_matrix_parallel unknown coordinator-git-archive-fast-path-unsafe \
  "git archive --format=tar --output=$proof_root/t00-session/archive.tar HEAD" \
  "env git archive HEAD" \
  "env FOO=bar git archive HEAD" \
  "env LD_PRELOAD=/tmp/libevil.so git archive HEAD" \
  "env LD_AUDIT=/tmp/libevil.so git archive HEAD"
```

### coordinator-git-context-unknown (w/ :3072-3074 and :3085; :3075 core.pager flipped to allowed; the other 10 rows stay live)

Restore these rows inside the `run_hook_matrix_parallel unknown coordinator-git-context-unknown \` matrix. Put the trailing `\` back on the `"systemd-run --unit eci git commit"` line before appending the last row.

```bash
  "git -c diff.external=/tmp/evil diff" \
  "git diff --textconv" \
  "git diff --ext-diff" \
  "xargs git commit"
```

### coordinator-git-read-only-execution-escapes (w/ :3094-3100; row :3101 flipped to allowed)

The lines are byte-verbatim from w/. On restore, drop the trailing ` \` from the `"git --paginate log -1"` line, because :3101 no longer follows it.

```bash
run_hook_matrix_parallel unknown coordinator-git-read-only-execution-escapes \
  "git grep --open-files-in-pager=/bin/sh needle" \
  "git grep --open-files-in-pager /bin/sh needle" \
  "git grep -O /bin/sh needle" \
  "git cat-file --filters HEAD:README" \
  "git log --show-signature -1" \
  "git --paginate log -1" \
```

### coordinator-git-environment-context (w/ :3109 and :3111; row :3110 flipped to allowed)

```bash
run_hook_matrix_parallel unknown coordinator-git-environment-context \
  "env GIT_EXTERNAL_DIFF=/tmp/evil git diff --stat" \
  "GIT_EXTERNAL_DIFF=/tmp/evil git diff --stat"
```

## classifier P5b: worker-kimi-protected-inspection (w :1811-1813)

```bash
run_subagent_matrix_parallel denied worker-kimi-protected-inspection \
  "find $kimi_find_a $kimi_find_b -maxdepth 2 -type f -print | head -n 40" \
  "readlink -f $ROOT/sessions"
```

## classifier P5b: copied-lifecycle-read no-verb row (inherited over-strict; w :2493-2494, row removed from the live matrix)

```bash
run_subagent_matrix_parallel allowed copied-lifecycle-read \
  "env $copied_eci" \
```

Restore as the first row of the live `copied-lifecycle-read` matrix, before `timeout 5 $copied_eci`.

## classifier session-temp compound rm (base e84c894 :1920-1926 without the outside row; withheld)

Restore after `assert_allowed "bash hooks/tests/test-eci-fast-path.sh && rm -f $session_temp_cleanup_target" run_hook_with_tmpdir_home_alias`, keeping the live outside-sibling allow. The helper (base e84c894 :1036-1051) is not live; restore it with the rows.

```bash
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
```

```bash
assert_compound_mutation_denied "git status --short || rm -f $session_temp_sibling_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_deeper_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_alias_target" run_hook_with_tmpdir_home_alias
assert_compound_mutation_denied "git status --short || rm -f $session_temp_link_target" run_hook_with_tmpdir_home_alias
assert_denied "git status --short || rm -f $TMP_ROOT/../${TMP_ROOT##*/}/eci-classifier-session-cleanup-${BASHPID}" run_hook_with_tmpdir_home_alias
assert_denied "git status --short || rm -f $TMP_ROOT/*" run_hook_with_tmpdir_home_alias
```

## test-eci-command-syntax-gating.sh (Job PORT, fsck)

Withheld until PEER makes the worker `git fsck --lost-found` denial reachable in hook mode. See the matching TEST-DISPOSITION.md row.

### 2f602d2 lines 272-296 (after: the closing `}` of `assert_foreign_marker_denied`)
```bash

# Known defect, recorded and kept red (shared by the pre-migration source and omp): the worker
# denial for `git fsck --lost-found` is unreachable while the marker is active. Both classifiers
# return DecisionDefer for worker git commands first (classifier.go:3767-3769, the same lines in
# source 575bb81 and omp HEAD), so the worker-git-ownership branch (classifier.go:3772-3782, the
# same lines in both trees) never runs. The pre-migration test expects this denial at its
# lines 282-290. The classifier is not changed here.
assert_worker_fsck_lost_found_denied() {
  run_hook 'env git fsck --lost-found | head -n 20' worker
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason |
      contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]") and
      contains("operation=worker-git-ownership") and
      contains("token=--lost-found") and
      contains("predicate=worker-git-ownership") and
      contains("worker argv selects Git fsck --lost-found writer") and
      (contains("predicate=worker-env-git-fsck-lost-found") | not)
    )
  ' "$OUTPUT" >/dev/null || {
    printf '%s\n' 'worker Git fsck --lost-found denial contract mismatch' >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}
```

### 2f602d2 lines 395-404 (after: `assert_current_control_operand_boundaries` call block; before `assert_broad_root_denied`, and the call after `assert_foreign_marker_denied`)
```bash
# These checks run before the row-4 call below: that call exits under set -e,
# so a check placed after it runs only in a copy with row 4 removed.
assert_broad_root_denied
assert_current_marker_denied
assert_foreign_marker_denied

# `git fsck --lost-found` writes recovered objects under .git/lost-found.  Its
# worker denial preserves the concrete Git ownership boundary, independent of
# the surrounding env wrapper or pipeline syntax.
assert_worker_fsck_lost_found_denied
```

## test-workflow-skill-routing.sh (Job PORT, absent-text pins)

Withheld until the sentences exist in the named docs. See the matching TEST-DISPOSITION.md row.

### 2f602d2 line 3309 (after: `'least restriction contract'` and the blank line)
```bash
  require_text "$STYLE_ADMISSION" 'Style sources guide the change; a brief or tool output is review context, not a write permit.'
```

### 2f602d2 line 3318 (after: the `$ECI` "Every enabled denial" pin)
```bash
  require_text "$GATE_CATALOG" 'Every enabled gate below has a bounded legitimate path.'
```

### 2f602d2 line 3322 (after: `require_text "$GATE_CATALOG" 'repository-allow-on'`)
```bash
  require_text "$GATE_CATALOG" 'The session-scoped 600-second self-edit hatch changes routing only; it does not override Producer ownership or authorize new tracked contributions or Git index/commit operations.'
```

## test-eci-command-plan.sh (Job PORT, kimi c/f)

Group c and f rows are retired: 575bb81, probed with its native environment, and 3ee2a18 both allow these kimi inputs (~/kprobe/kimi-probe.tsv). See the matching TEST-DISPOSITION.md row.

### 3ee2a18 lines 259-264 (group c, per role and marker)
```bash
      if [ "$provider" = kimi ]; then
        assert_denied "$provider" "$role" "$active" 'env | sort' \
          ECI_ENVIRONMENT_ENUMERATION_DENIED
        assert_denied "$provider" "$role" "$active" 'env -S "novel-finite-tool"' \
          ECI_ENVIRONMENT_OPTION_DENIED
      fi
```

### 3ee2a18 lines 308-310 (group c, worker active)
```bash
  if [ "$provider" = kimi ]; then
    assert_denied "$provider" worker active 'env' ECI_ENVIRONMENT_ENUMERATION_DENIED
  fi
```

### 3ee2a18 lines 354-355 and 364-371 (group f, worker active)
```bash
  ln -s -- "$session_root/goal_state" "$TMP_ROOT/$provider-live-symlink"
  ln -- "$session_root/goal_state" "$TMP_ROOT/$provider-live-hardlink"
  if [ "$provider" = kimi ]; then
    assert_plan_denied "$provider" worker active \
      "cat $session_root/goal_state" ECI_PLAN_LIVE_CONTROL_DENIED
    assert_denied "$provider" worker active \
      "cat $TMP_ROOT/$provider-live-symlink" ECI_PLAN_LIVE_CONTROL_DENIED
    assert_plan_denied "$provider" worker active \
      "cat $TMP_ROOT/$provider-live-hardlink" ECI_PLAN_LIVE_CONTROL_DENIED
  fi
```

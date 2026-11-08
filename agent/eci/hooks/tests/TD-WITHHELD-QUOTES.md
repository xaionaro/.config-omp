# Withheld and moved-out assertions: verbatim quotes for restoration

Line numbers are advisory. Restore by content: paste each block back after the context line it names. Every block is byte-exact.

## test-coordinator-protected-git-target.sh (Job PORT)

Source of the quotes: the v2 fixture of this file (sha256 4ec2fe7a59bab26a…). In v2 the runtime is a private copy at `$TMP_ROOT/home/.omp/agent/eci`, git-inited, with ROOT pointing at it. Each block is a group of adjacent v2 lines. Decisions on omp 42f3b1f and 575bb81 are identical for every row (per-assertion capture of all 199). See the three matching rows in TEST-DISPOSITION.md.

### Inherited admissions, withheld (37 rows; both trees allow, deny expected)

Each of these expects ECI_COORDINATOR_EDIT_ROUTING_REQUIRED predicate=coordinator-protected-target, but gets an empty hook output (allow). Restore them after PEER4 extends protected-target inspection. The `printf` line provisions the `--` value file used by the loop that follows it.

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

Each of these gets ECI_COORDINATOR_EDIT_ROUTING_REQUIRED predicate=coordinator-protected-target, but no physical overwrite of a protected hook is selected. The cases are branch-creation, patch and invalid-context checkouts, `git mv` no-ops and out-of-tree targets, and the untracked alias symlink. Restore them unchanged once the predicate stops denying them.

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

There are 45 assert_allowed_at_session / assert_allowed_at rows and 4 assert_protected_denial_at rows. Sessions t03, t04 and t05 deliberately put their marker cwd outside ROOT, because the `--`/`--detach` pathspec value file lives in that cwd. The rows then select ROOT with explicit `--work-tree`/`--git-dir`. The hooks_cwd rows (marker cwd `$ROOT/hooks`) reach ROOT through `-C ..`, `env -C ..` or `sudo -D/--chdir ..`. No recorded decision covers the cross-scope policy for these shapes. Restore them after the PEER4 policy decision, adjusted to whatever code it rules.

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

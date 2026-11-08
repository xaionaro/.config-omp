#!/usr/bin/env bash

set -euo pipefail

# These bounded files are synthetic contract fixtures, not reviewer reports;
# no fabricated production evidence is admitted by the runtime gate.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
test_tmp_parent="${TMPDIR:-}"
if [ -z "$test_tmp_parent" ] && [ -d /dev/shm ] && [ -w /dev/shm ]; then
  test_tmp_parent=/dev/shm
else
  test_tmp_parent="${test_tmp_parent:-/tmp}"
fi
test_tmp_parent="$(eci_realpath_canonical "$test_tmp_parent")"
TMP_ROOT="$(mktemp -d "$test_tmp_parent/eci-review-gate.XXXXXX")"
untracked_target=""
root_fixture=""
trap 'rm -f -- "${untracked_target:-}" "${root_fixture:-}" 2>/dev/null || true; rm -rf -- "$TMP_ROOT"' EXIT

sha() { eci_sha256_hex "$1"; }

# Rebind every identity-bearing artifact when a synthetic row deliberately
# receives a fresh child identity.  The review gate treats the spawn request,
# report marker, adjudication, and manifest row as one closed contract; merely
# changing the row/adjudication leaves a fixture invalid before the test's
# intended assertion is reached.
rebind_manifest_row_identity() {
  local manifest="$1" index="$2" child="$3"
  local session_dir sid row target_id role phase provider semantic_role provenance
  local spawn report adjudication rebound_spawn rebound_report rebound_adjudication
  local spawn_sha report_sha adjudication_sha tmp

  session_dir="$(dirname -- "$manifest")"
  sid="$(basename -- "$session_dir")"
  row="$(jq -c --argjson index "$index" '.rows[$index]' "$manifest")"
  target_id="$(jq -r '.target_id' <<<"$row")"
  role="$(jq -r '.critic_role' <<<"$row")"
  phase="$(jq -r '.gate_phase' <<<"$row")"
  provider="$(jq -r '.critic_provider' <<<"$row")"
  semantic_role="$(jq -r '.critic_semantic_role' <<<"$row")"
  provenance="$(jq -r '.critic_provenance' <<<"$row")"
  spawn="$(jq -r '.spawn_request_artifact' <<<"$row")"
  report="$(jq -r '.report_artifact' <<<"$row")"
  adjudication="$(jq -r '.adjudication_artifact' <<<"$row")"

  rebound_spawn="$spawn.rebound-$index"
  rebound_report="$report.rebound-$index"
  rebound_adjudication="$adjudication.rebound-$index"
  sed -E \
    -e "s/^target_id: .*/target_id: $target_id/" \
    -e "s/^child_identity: .*/child_identity: $child/" \
    "$spawn" >"$rebound_spawn"
  sed -E "s/^eci_critic_identity: .*/eci_critic_identity: session_id=$sid;target_id=$target_id;critic_role=$role;gate_phase=$phase;child_identity=$child;critic_provider=$provider;critic_semantic_role=$semantic_role;critic_provenance=$provenance/" "$report" >"$rebound_report"
  spawn_sha="$(sha "$rebound_spawn")"
  report_sha="$(sha "$rebound_report")"
  jq -c --arg target "$target_id" --arg role "$role" --arg phase "$phase" \
    --arg child "$child" --arg provider "$provider" --arg semantic_role "$semantic_role" \
    --arg provenance "$provenance" --arg report "$report_sha" \
    '.target_id = $target | .critic_role = $role | .gate_phase = $phase | .child_identity = $child |
     .critic_provider = $provider | .critic_semantic_role = $semantic_role | .critic_provenance = $provenance |
     .report_sha256 = $report' "$adjudication" >"$rebound_adjudication"
  adjudication_sha="$(sha "$rebound_adjudication")"
  tmp="$manifest.tmp"
  jq -c --argjson index "$index" --arg child "$child" \
    --arg spawn "$rebound_spawn" --arg spawn_sha "$spawn_sha" \
    --arg report "$rebound_report" --arg report_sha "$report_sha" \
    --arg adjudication "$rebound_adjudication" --arg adjudication_sha "$adjudication_sha" \
    '.rows[$index] |= (.child_identity = $child |
      .spawn_request_artifact = $spawn | .spawn_request_sha256 = $spawn_sha |
      .report_artifact = $report | .report_sha256 = $report_sha |
      .adjudication_artifact = $adjudication | .adjudication_sha256 = $adjudication_sha)' \
    "$manifest" >"$tmp"
  mv -- "$tmp" "$manifest"
}

build_manifest() {
  local proof_root="$1" kind="$2" e2e_required="${3:-false}"
  local sid="${ECI_EMIT_SESSION_ID:-session-$kind}" target_id="target-$kind" session_dir diff diff_sha rows='[]'
  local spec role phase provider semantic_role provenance authority model_class spawn report spawn_sha report_sha adjudication adjudication_sha e2e='' e2e_sha='' intention intention_sha
  local repo_root git_dir git_common base head staged worktree status target_path target_version
  local git_dir_raw git_common_raw test_repo="${ECI_TEST_REPO:-$ROOT}" test_target="${ECI_TEST_TARGET:-}"

  [ -n "$test_target" ] || test_target="$test_repo/hooks/eci-review-gate.sh"

  session_dir="$proof_root/$sid"
  mkdir -p "$session_dir"
  diff="$session_dir/diff.txt"
  head="$(git -C "$test_repo" rev-parse HEAD)"
  if git -C "$test_repo" diff --cached --quiet --binary; then
    if git -C "$test_repo" diff --quiet --binary; then
      git -C "$test_repo" diff --binary "$head" "$head" >"$diff"
    else
      git -C "$test_repo" diff --binary >"$diff"
    fi
  else
    git -C "$test_repo" diff --cached --binary >"$diff"
  fi
  diff_sha="$(sha "$diff")"
  if [ "$e2e_required" = true ]; then
    e2e="$session_dir/e2e.txt"
    printf 'e2e for %s\n' "$kind" >"$e2e"
    e2e_sha="$(sha "$e2e")"
  fi

  intention="$session_dir/C-prewrite-intention.txt"
  printf 'C prewrite intention for %s\n' "$kind" >"$intention"
  intention_sha="$(sha "$intention")"
  repo_root="$test_repo"
  git_dir_raw="$(git -C "$test_repo" rev-parse --git-dir)"
  git_common_raw="$(git -C "$test_repo" rev-parse --git-common-dir)"
  git_dir="$(eci_realpath_canonical "$test_repo/$git_dir_raw")"
  git_common="$(eci_realpath_canonical "$test_repo/$git_common_raw")"
  base="$head"
  staged="$(git -C "$test_repo" diff --cached --binary | eci_sha256 | awk '{print $1}')"
  worktree="$(git -C "$test_repo" diff --binary | eci_sha256 | awk '{print $1}')"
  status="$(git -C "$test_repo" status --porcelain=v1 --untracked-files=all | eci_sha256 | awk '{print $1}')"
  target_path="$test_target"
  target_version="$(sha "$target_path")"

  # C-prewrite is an optional policy prefix; postwrite acceptance requires A/B/C.
  for spec in C:prewrite A:postwrite B:postwrite C:postwrite; do
    role="${spec%%:*}"
    phase="${spec#*:}"
    spawn="$session_dir/${role}-${phase}-spawn.txt"
    report="$session_dir/${role}-${phase}-report.txt"
    adjudication="$session_dir/${role}-${phase}-adjudication.json"
    provider='unavailable_by_schema'
    semantic_role="ECI Critic $role"
    if [ "$role" = C ]; then
      provenance='requested-special'; authority='authoritative'; model_class='special'
    else
      provenance='ordinary'; authority='non-authoritative'; model_class='ordinary'
    fi
    child="child-$kind-$role-$phase"
    printf 'schema: eci-critic-spawn/v2\nprovider: %s\nsemantic_role: %s\nauthority: %s\nrequired_model_class: %s\nparent_session: %s\ntarget_id: target-%s\ncritic_role: %s\ngate_phase: %s\nchild_identity: %s\nprovenance: %s\n' \
      "$provider" "$semantic_role" "$authority" "$model_class" "$sid" "$kind" "$role" "$phase" "$child" "$provenance" >"$spawn"
    printf 'eci_critic_identity: session_id=%s;target_id=target-%s;critic_role=%s;gate_phase=%s;child_identity=%s;critic_provider=%s;critic_semantic_role=%s;critic_provenance=%s\n' \
      "$sid" "$kind" "$role" "$phase" "$child" "$provider" "$semantic_role" "$provenance" >"$report"
    case "$role" in
      A)
        printf '%s\n' \
          'evidence_type: focused implementation and diagnostic verification' \
          'evidence_commands:' \
          '  bash hooks/tests/test-eci-edit-control-paths.sh: PASS' \
          '  bash hooks/tests/test-eci-command-syntax-gating.sh: PASS' \
          '  bash hooks/tests/test-eci-diagnostic-specificity.sh: PASS' \
          'assessment: coordinator and worker edit boundaries, literal command grammar, and compiler-style diagnostic fields were verified.' >>"$report"
        ;;
      B)
        printf '%s\n' \
          'evidence_type: focused lifecycle and stop-loop verification' \
          'evidence_commands:' \
          '  bash hooks/tests/test-eci-marker-scope.sh: PASS' \
          '  bash hooks/tests/test-stop-loop-guidance.sh: PASS' \
          '  bash hooks/tests/test-eci-fast-path.sh: PASS' \
          'assessment: marker ownership, convergent Stop remediation, and bounded callback behavior were verified.' >>"$report"
        ;;
      C)
        printf '%s\n' \
          'evidence_type: focused policy, parity, and latency verification' \
          'evidence_commands:' \
          '  bash hooks/tests/test-pre-commit-go-mod.sh: PASS' \
          '  bash hooks/tests/test-pretooluse-latency.sh: PASS' \
          '  hard-linked ECI/Kimi Go checker inode parity probe: PASS' \
          'assessment: Go replacement policy, shared-hook parity, and configured synchronous-chain latency were verified.' >>"$report"
        ;;
    esac
    printf 'eci_critic_verdict: APPROVED\n' >>"$report"
    spawn_sha="$(sha "$spawn")"
    report_sha="$(sha "$report")"
    jq -cn \
      --arg target "$target_id" --arg role "$role" --arg gate_phase "$phase" \
      --arg child "$child" --arg report_sha "$report_sha" \
      --arg provider "$provider" --arg semantic_role "$semantic_role" --arg provenance "$provenance" \
      '{schema:"eci-critic-adjudication/v1",target_id:$target,critic_role:$role,gate_phase:$gate_phase,child_identity:$child,critic_provider:$provider,critic_semantic_role:$semantic_role,critic_provenance:$provenance,report_sha256:$report_sha,source_verdict:"APPROVED",decision:"accepted",reason:"accepted after focused implementation, lifecycle, policy, parity, diagnostic, and latency evidence"}' \
      >"$adjudication"
    adjudication_sha="$(sha "$adjudication")"
    rows="$(jq -cn \
      --argjson old "$rows" \
      --arg tid "$target_id" --arg kind "$kind" --arg diff "$diff" --arg diff_sha "$diff_sha" \
      --arg role "$role" --arg phase "$phase" --arg child "child-$kind-$role-$phase" \
      --arg provider "$provider" --arg semantic_role "$semantic_role" --arg provenance "$provenance" \
      --arg spawn "$spawn" --arg spawn_sha "$spawn_sha" --arg report "$report" --arg report_sha "$report_sha" \
      --arg adjudication "$adjudication" --arg adjudication_sha "$adjudication_sha" \
      --arg e2e "$e2e" --arg e2e_sha "$e2e_sha" --argjson required "$e2e_required" \
      --arg repo_root "$repo_root" --arg git_dir "$git_dir" --arg git_common "$git_common" \
      --arg base "$base" --arg head "$head" --arg staged "$staged" --arg worktree "$worktree" --arg status "$status" \
      --arg target_path "$target_path" --arg target_version "$target_version" \
      --arg intention "$intention" --arg intention_sha "$intention_sha" \
      '$old + [{target_id:$tid,target_kind:$kind,diff_artifact:$diff,diff_sha256:$diff_sha,critic_role:$role,gate_phase:$phase,child_identity:$child,critic_provider:$provider,critic_semantic_role:$semantic_role,critic_provenance:$provenance,spawn_request_artifact:$spawn,spawn_request_sha256:$spawn_sha,report_artifact:$report,report_sha256:$report_sha,adjudication_artifact:$adjudication,adjudication_sha256:$adjudication_sha,verdict:"PASS",e2e_required:$required,e2e_artifact:(if $required then $e2e else null end),e2e_sha256:(if $required then $e2e_sha else null end),repo_root:$repo_root,git_dir:$git_dir,git_common_dir:$git_common,base_oid:$base,head_oid:$head,staged_diff_sha256:$staged,worktree_diff_sha256:$worktree,status_sha256:$status,target_path:$target_path,target_version:$target_version,intention_artifact:(if $role == "C" and $phase == "prewrite" then $intention else null end),intention_sha256:(if $role == "C" and $phase == "prewrite" then $intention_sha else null end),acceptance_version:"1"}]')"
  done

  jq -cn \
    --arg tid "$target_id" --arg kind "$kind" --arg diff "$diff" --arg diff_sha "$diff_sha" \
    --argjson required "$e2e_required" --argjson rows "$rows" \
    --arg target_path "$target_path" --arg repo_root "$repo_root" --arg git_dir "$git_dir" --arg git_common "$git_common" \
    --arg base "$base" --arg head "$head" --arg staged "$staged" --arg worktree "$worktree" --arg status "$status" --arg target_version "$target_version" \
    '{schema:"eci-required-critics/v2",current_target_id:$tid,current_target_kind:$kind,current_diff_artifact:$diff,current_diff_sha256:$diff_sha,current_target_path:$target_path,repo_root:$repo_root,git_dir:$git_dir,git_common_dir:$git_common,base_oid:$base,head_oid:$head,staged_diff_sha256:$staged,worktree_diff_sha256:$worktree,status_sha256:$status,target_file_hashes:{($target_path):$target_version},acceptance_version:"1",targets:[{target_id:$tid,target_kind:$kind,diff_artifact:$diff,diff_sha256:$diff_sha,e2e_required:$required,target_path:$target_path,target_version:$target_version}],rows:$rows}' \
    >"$session_dir/eci-required-critics.json"
  printf '%s\n' "$sid"
}

run_gate() {
  local proof_root="$1" phase="$2" sid="$3" out="$4" err="$5"
  local gate_cwd="${ECI_TEST_CWD:-$ROOT}"
  ECI_PROOF_ROOT="$proof_root" ECI_REVIEW_CWD="$gate_cwd" "$ROOT/hooks/eci-review-gate.sh" "$phase" "$sid" >"$out" 2>"$err"
}

assert_reject() {
  local proof_root="$1" sid="$2" needle="$3" out="$TMP_ROOT/gate.out" err="$TMP_ROOT/gate.err"
  if run_gate "$proof_root" final "$sid" "$out" "$err"; then
    printf 'expected rejection: %s\n' "$needle" >&2
    return 1
  fi
  grep -Fq "$needle" "$err"
}

# These path-contract tests must reach artifact/target validation, rather than
# being short-circuited by unrelated staged/worktree changes in this checkout.
prepare_clean_review_gate_fixture() {
  local fixture_repo="$1" fixture_target="$fixture_repo/hooks/eci-review-gate.sh"

  mkdir -p "${fixture_target%/*}"
  cp -- "$ROOT/hooks/eci-review-gate.sh" "$fixture_target"
  printf '%s\n' '# fixture intentionally ignores nothing' >"$fixture_repo/.gitignore"
  git -C "$fixture_repo" init -q
  git -C "$fixture_repo" config user.name 'ECI test'
  git -C "$fixture_repo" config user.email 'eci-test@example.invalid'
  git -C "$fixture_repo" add -- .gitignore hooks/eci-review-gate.sh
  git -C "$fixture_repo" commit -qm 'clean review-gate fixture'
  git -C "$fixture_repo" cat-file -e HEAD:.gitignore
  git -C "$fixture_repo" cat-file -e HEAD:hooks/eci-review-gate.sh

  printf '%s\n' '# controlled review-gate fixture change' >>"$fixture_target"
  git -C "$fixture_repo" diff --cached --quiet --binary
  if git -C "$fixture_repo" diff --quiet --binary; then
    printf 'fixture is missing its controlled worktree change: %s\n' "$fixture_repo" >&2
    return 1
  fi
  [ "$(git -C "$fixture_repo" diff --name-only --no-renames)" = 'hooks/eci-review-gate.sh' ]
  [ "$(git -C "$fixture_repo" diff --numstat --no-renames)" = $'1\t0\thooks/eci-review-gate.sh' ]
  [ "$(git -C "$fixture_repo" status --porcelain=v1 --untracked-files=all)" = ' M hooks/eci-review-gate.sh' ]
}

test_e2e_artifact_10mib_boundary_accepts_exact_and_rejects_next_byte() {
  local proof_root="$TMP_ROOT/e2e-artifact-boundary" fixture_repo="$TMP_ROOT/e2e-artifact-boundary-repo"
  local fixture_target sid manifest e2e e2e_sha tmp
  fixture_target="$fixture_repo/hooks/eci-review-gate.sh"

  mkdir -p "${fixture_target%/*}"
  cp -- "$ROOT/hooks/eci-review-gate.sh" "$fixture_target"
  git -C "$fixture_repo" init -q
  git -C "$fixture_repo" config user.name 'ECI test'
  git -C "$fixture_repo" config user.email 'eci-test@example.invalid'
  git -C "$fixture_repo" add hooks/eci-review-gate.sh
  git -C "$fixture_repo" commit -qm 'clean E2E artifact boundary fixture'
  printf '%s\n' '# controlled E2E artifact boundary fixture change' >>"$fixture_target"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root true)"
    manifest="$proof_root/$sid/eci-required-critics.json"
    e2e="$(jq -r '.rows[0].e2e_artifact' "$manifest")"

    truncate -s 10485760 "$e2e"
    e2e_sha="$(sha "$e2e")"
    tmp="$manifest.tmp"
    jq -c --arg sha "$e2e_sha" \
      '(.rows[] | select(.e2e_required == true) | .e2e_sha256) = $sha' \
      "$manifest" >"$tmp"
    mv -- "$tmp" "$manifest"
    if ! run_gate "$proof_root" final "$sid" "$TMP_ROOT/e2e-artifact-exact.out" "$TMP_ROOT/e2e-artifact-exact.err"; then
      exit 1
    fi

    truncate -s 10485761 "$e2e"
    e2e_sha="$(sha "$e2e")"
    jq -c --arg sha "$e2e_sha" \
      '(.rows[] | select(.e2e_required == true) | .e2e_sha256) = $sha' \
      "$manifest" >"$tmp"
    mv -- "$tmp" "$manifest"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/e2e-artifact-oversized.out" "$TMP_ROOT/e2e-artifact-oversized.err"; then
      exit 1
    fi
    grep -Fq 'oversized E2E artifact' "$TMP_ROOT/e2e-artifact-oversized.err"
    grep -Fq 'limit 10485760 bytes' "$TMP_ROOT/e2e-artifact-oversized.err"
  )
}

test_valid_target_kinds() {
  local kind proof_root sid
  for kind in root subtask candidate-fix; do
    proof_root="$TMP_ROOT/valid-$kind"
    sid="$(build_manifest "$proof_root" "$kind")"
    if ! run_gate "$proof_root" final "$sid" "$TMP_ROOT/$kind.out" "$TMP_ROOT/$kind.err"; then
      return 1
    fi
    grep -Fq "phase=final" "$TMP_ROOT/$kind.out"
  done
}

test_git_external_diff_is_neutralized() {
  local proof_root="$TMP_ROOT/git-env" sid helper touched
  helper="$TMP_ROOT/external-diff-helper"
  touched="$TMP_ROOT/external-diff-ran"
  printf '#!/usr/bin/env bash\ntouch -- %q\n' "$touched" >"$helper"
  chmod +x "$helper"
  sid="$(build_manifest "$proof_root" root)"
  GIT_EXTERNAL_DIFF="$helper" GIT_CONFIG_PARAMETERS="'diff.external=$helper'" \
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/git-env.out" "$TMP_ROOT/git-env.err"
  [ ! -e "$touched" ]
}

test_git_uses_fixed_binary_and_no_textconv() {
  local repo="$TMP_ROOT/git-textconv-repo" fakebin="$TMP_ROOT/fake-git-bin"
  local helper="$TMP_ROOT/textconv-helper" fake_touched="$TMP_ROOT/fake-git-ran"
  local textconv_touched="$TMP_ROOT/textconv-ran"
  mkdir -p "$repo" "$fakebin"
  git -C "$repo" init -q
  git -C "$repo" config user.name 'ECI test'
  git -C "$repo" config user.email eci-test@example.invalid
  printf '%s\n' before >"$repo/file.txt"
  printf '%s\n' 'file.txt diff=eci-textconv' >"$repo/.gitattributes"
  git -C "$repo" add file.txt .gitattributes
  git -C "$repo" commit -qm 'textconv fixture'
  printf '%s\n' after >"$repo/file.txt"
  printf '#!/usr/bin/env bash\ntouch -- %q\ncat\n' "$textconv_touched" >"$helper"
  chmod +x "$helper"
  git -C "$repo" config diff.eci-textconv.textconv "$helper"
  printf '%s\n' '#!/usr/bin/env bash' "touch -- $fake_touched" 'exec /usr/bin/git "$@"' >"$fakebin/git"
  chmod +x "$fakebin/git"
  PATH="$fakebin:$PATH" bash -c \
    '. "$1/hooks/lib/eci-proof-state.sh"; eci_git_safe -C "$2" diff -- file.txt >/dev/null' \
    bash "$ROOT" "$repo"
  [ ! -e "$fake_touched" ]
  [ ! -e "$textconv_touched" ]
}

test_valid_e2e_binding() {
  local proof_root="$TMP_ROOT/valid-e2e" sid
  sid="$(build_manifest "$proof_root" candidate-fix true)"
  run_gate "$proof_root" final "$sid" "$TMP_ROOT/e2e.out" "$TMP_ROOT/e2e.err"
  grep -Fq 'phase=final' "$TMP_ROOT/e2e.out"
}

test_off_boundary_requires_and_accepts_manifest() {
  local proof_root="$TMP_ROOT/off-boundary" sid session_dir manifest saved
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  {
    printf 'scope: off boundary\n'
    printf 'cwd: %s\n' "$ROOT"
    printf 'session_id: %s\n' "$sid"
    printf 'created_utc: 2026-01-01T00:00:00Z\n'
  } >"$session_dir/eci_active"
  manifest="$session_dir/eci-required-critics.json"
  saved="$TMP_ROOT/off-boundary-manifest.json"
  cp "$manifest" "$saved"
  rm -f "$manifest"
  if run_gate "$proof_root" off "$sid" "$TMP_ROOT/off-missing.out" "$TMP_ROOT/off-missing.err"; then
    return 1
  fi
  grep -Fq 'missing canonical manifest' "$TMP_ROOT/off-missing.err"
  [ -f "$session_dir/eci_active" ]
  mv "$saved" "$manifest"

  printf 'scope: malformed-only\n' >"$session_dir/eci_active"
  if run_gate "$proof_root" off "$sid" "$TMP_ROOT/off-malformed.out" "$TMP_ROOT/off-malformed.err"; then
    return 1
  fi
  grep -Fq 'direct marker path/owner binding is malformed' "$TMP_ROOT/off-malformed.err"
  [ -f "$session_dir/eci_active" ]
  {
    printf 'scope: off boundary\n'
    printf 'cwd: %s\n' "$ROOT"
    printf 'session_id: %s\n' "$sid"
    printf 'created_utc: 2026-01-01T00:00:00Z\n'
  } >"$session_dir/eci_active"
  run_gate "$proof_root" off "$sid" "$TMP_ROOT/off-valid.out" "$TMP_ROOT/off-valid.err"
  grep -Fq 'phase=off' "$TMP_ROOT/off-valid.out"
}

test_sha256_repository_binding_accepts_object_format_oid_lengths() {
  local repo="$TMP_ROOT/sha256-repo" proof_root="$TMP_ROOT/sha256-proof"
  local sid=sha256-session session_dir manifest base head git_dir_raw git_common_raw git_dir git_common binding
  mkdir -p "$repo" "$proof_root/$sid"
  git -C "$repo" init --object-format=sha256 -q
  git -C "$repo" config user.name 'ECI test'
  git -C "$repo" config user.email 'eci-test@example.invalid'
  printf '%s\n' 'sha256 repository binding' >"$repo/target.txt"
  git -C "$repo" add target.txt
  git -C "$repo" commit -qm 'sha256 binding fixture'
  base="$(git -C "$repo" rev-parse HEAD)"
  head="$base"
  git_dir_raw="$(git -C "$repo" rev-parse --git-dir)"
  git_common_raw="$(git -C "$repo" rev-parse --git-common-dir)"
  git_dir="$(eci_realpath_canonical "$repo/$git_dir_raw")"
  git_common="$(eci_realpath_canonical "$repo/$git_common_raw")"
  [[ "$base" =~ ^[0-9a-f]{64}$ && "$head" =~ ^[0-9a-f]{64}$ ]] || return 1
  manifest="$proof_root/$sid/eci-required-critics.json"
  jq -cn --arg root "$repo" --arg gd "$git_dir" --arg gc "$git_common" --arg base "$base" --arg head "$head" \
    '{repo_root:$root,git_dir:$gd,git_common_dir:$gc,base_oid:$base,head_oid:$head}' >"$manifest"
  binding="$(bash -c '. "$1/hooks/lib/eci-proof-state.sh"; eci_live_repo_binding_sha256 "$2"' bash "$ROOT" "$manifest")"
  [[ "$binding" =~ ^[0-9a-f]{64}$ ]]
}

test_manifest_verdict_mapping_is_closed() {
  local proof_root="$TMP_ROOT/verdict-mapping" sid session_dir manifest original
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  original="$TMP_ROOT/verdict-original.json"
  cp "$manifest" "$original"
  for verdict in CONDITIONAL REJECTED; do
    jq -c --arg verdict "$verdict" '.rows[0].verdict = $verdict' "$original" >"$manifest"
    assert_reject "$proof_root" "$sid" 'verdict'
  done
}

test_adjudication_binds_nonapproved_critic_report() {
  local proof_root="$TMP_ROOT/adjudication" sid session_dir manifest original adj report tmp new_sha report_sha
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  original="$TMP_ROOT/adjudication-original.json"
  cp "$manifest" "$original"
  adj="$(jq -r '.rows[0].adjudication_artifact' "$manifest")"
  tmp="$adj.tmp"

  # A PASS row cannot hide a rejected independent report behind an accepted
  # decision.  The coordinator record must explicitly bind the downgrade.
  jq -c '.source_verdict = "REJECTED" | .decision = "accepted"' "$adj" >"$tmp"
  mv "$tmp" "$adj"
  new_sha="$(sha "$adj")"
  jq -c --arg sha "$new_sha" '.rows[0].adjudication_sha256 = $sha' "$original" >"$manifest"
  assert_reject "$proof_root" "$sid" 'unbound or silent critic adjudication'

  jq -c '.source_verdict = "REJECTED" | .decision = "downgraded" | .reason = "bounded coordinator adjudication"' "$adj" >"$tmp"
  mv "$tmp" "$adj"
  report="$(jq -r '.rows[0].report_artifact' "$manifest")"
  sed 's/^eci_critic_verdict: APPROVED$/eci_critic_verdict: REJECTED/' "$report" >"$tmp"
  mv "$tmp" "$report"
  report_sha="$(sha "$report")"
  jq -c --arg report_sha "$report_sha" '.report_sha256 = $report_sha' "$adj" >"$tmp"
  mv "$tmp" "$adj"
  new_sha="$(sha "$adj")"
  jq -c --arg report_sha "$report_sha" --arg sha "$new_sha" \
    '.rows[0].report_sha256 = $report_sha | .rows[0].adjudication_sha256 = $sha' \
    "$original" >"$manifest"
  run_gate "$proof_root" final "$sid" "$TMP_ROOT/adjudication.out" "$TMP_ROOT/adjudication.err"
  grep -Fq 'phase=final' "$TMP_ROOT/adjudication.out"
}

test_report_verdict_is_bound_to_adjudication() {
  local proof_root="$TMP_ROOT/report-verdict" sid session_dir manifest report adj tmp report_sha adj_sha
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  report="$(jq -r '.rows[0].report_artifact' "$manifest")"
  adj="$(jq -r '.rows[0].adjudication_artifact' "$manifest")"
  tmp="$report.tmp"
  sed 's/^eci_critic_verdict: APPROVED$/eci_critic_verdict: REJECTED/' "$report" >"$tmp"
  mv "$tmp" "$report"
  report_sha="$(sha "$report")"
  # Keep the adjudication source verdict and decision unchanged, but bind its
  # report hash to the mutated report. The gate must reject the contradictory
  # canonical report marker rather than accepting hash consistency alone.
  jq -c --arg report_sha "$report_sha" '.report_sha256 = $report_sha' "$adj" >"$tmp"
  mv "$tmp" "$adj"
  adj_sha="$(sha "$adj")"
  jq -c --arg report_sha "$report_sha" --arg adj_sha "$adj_sha" \
    '.rows[0].report_sha256 = $report_sha | .rows[0].adjudication_sha256 = $adj_sha' \
    "$manifest" >"$manifest.tmp"
  mv "$manifest.tmp" "$manifest"
  assert_reject "$proof_root" "$sid" 'canonical verdict marker'
}

test_report_text_contract() {
  local kind proof_root sid session_dir manifest report adj tmp report_sha adj_sha
  for kind in marker-only no-final-lf invalid-utf8; do
    proof_root="$TMP_ROOT/report-contract-$kind"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    manifest="$session_dir/eci-required-critics.json"
    report="$(jq -r '.rows[0].report_artifact' "$manifest")"
    adj="$(jq -r '.rows[0].adjudication_artifact' "$manifest")"
    tmp="$report.tmp"
    case "$kind" in
      marker-only)
        printf 'eci_critic_verdict: APPROVED\n' >"$tmp"
        ;;
      no-final-lf)
        printf 'report body\neci_critic_verdict: APPROVED' >"$tmp"
        ;;
      invalid-utf8)
        printf 'report body \377\neci_critic_verdict: APPROVED\n' >"$tmp"
        ;;
    esac
    mv "$tmp" "$report"
    report_sha="$(sha "$report")"
    jq -c --arg report_sha "$report_sha" '.report_sha256 = $report_sha' "$adj" >"$tmp"
    mv "$tmp" "$adj"
    adj_sha="$(sha "$adj")"
    jq -c --arg report_sha "$report_sha" --arg adj_sha "$adj_sha" \
      '.rows[0].report_sha256 = $report_sha | .rows[0].adjudication_sha256 = $adj_sha' \
      "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    assert_reject "$proof_root" "$sid" 'report artifact text contract'
  done
}

test_gate_fails_closed_when_mutation_lock_is_busy() {
  local proof_root="$TMP_ROOT/gate-lock" sid lock_fd
  sid="$(build_manifest "$proof_root" root)"
  exec {lock_fd}>>"$proof_root/.eci-active.lock"
  flock -n "$lock_fd"
  if run_gate "$proof_root" final "$sid" "$TMP_ROOT/gate-lock.out" "$TMP_ROOT/gate-lock.err"; then
    return 1
  fi
  grep -Fq 'mutation lock is busy' "$TMP_ROOT/gate-lock.err"
  flock -u "$lock_fd"
  eval "exec ${lock_fd}>&-"
}

test_missing_each_critic_and_c_phase() {
  local role proof_root="$TMP_ROOT/missing" sid session_dir manifest original
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  original="$TMP_ROOT/original.json"
  cp "$manifest" "$original"
  for role in A B C; do
    jq -c --arg role "$role" '.rows |= map(select(.critic_role != $role))' "$original" >"$manifest"
    assert_reject "$proof_root" "$sid" "Critic $role"
  done
}

test_postwrite_accepts_without_optional_prewrite() {
  local proof_root="$TMP_ROOT/no-prewrite" sid session_dir manifest out err
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  jq -c '.rows |= map(select(.gate_phase != "prewrite"))' "$manifest" >"$manifest.tmp"
  mv "$manifest.tmp" "$manifest"
  out="$TMP_ROOT/no-prewrite.out"; err="$TMP_ROOT/no-prewrite.err"
  run_gate "$proof_root" final "$sid" "$out" "$err"
  grep -Fq 'phase=final' "$out"
}

test_stale_hashes() {
  local field proof_root="$TMP_ROOT/stale" sid session_dir manifest original
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  original="$TMP_ROOT/stale-original.json"
  cp "$manifest" "$original"
  for field in diff_sha256 spawn_request_sha256 report_sha256; do
    cp "$original" "$manifest"
    jq -c --arg field "$field" '.rows[0][$field] = ("0" * 64)' "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    assert_reject "$proof_root" "$sid" 'stale'
  done
}

test_duplicate_schema_and_e2e_rejections() {
  local proof_root="$TMP_ROOT/schema" sid session_dir manifest original
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  original="$TMP_ROOT/schema-original.json"
  cp "$manifest" "$original"

  jq -c '.targets += [.targets[0]]' "$original" >"$manifest"
  assert_reject "$proof_root" "$sid" 'duplicate governed target'

  jq -c '.unknown = true' "$original" >"$manifest"
  assert_reject "$proof_root" "$sid" 'manifest v2 schema'

  jq -c '{rows:.rows,schema:.schema,current_target_id:.current_target_id,current_target_kind:.current_target_kind,current_diff_artifact:.current_diff_artifact,current_diff_sha256:.current_diff_sha256,targets:.targets}' "$original" >"$manifest"
  assert_reject "$proof_root" "$sid" 'manifest v2 schema'

  jq -c '.rows[0].e2e_artifact = "unexpected"' "$original" >"$manifest"
  assert_reject "$proof_root" "$sid" 'manifest v2 schema'
}

test_manifest_is_immutable_after_admission() {
  local proof_root="$TMP_ROOT/immutable" sid session_dir manifest adjudication
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  run_gate "$proof_root" final "$sid" "$TMP_ROOT/immutable.out" "$TMP_ROOT/immutable.err"
  adjudication="$(jq -r '.rows[0].adjudication_artifact' "$manifest")"
  jq -c '.child_identity = "changed-child"' "$adjudication" >"$adjudication.tmp"
  mv "$adjudication.tmp" "$adjudication"
  jq -c --arg sha "$(sha "$adjudication")" '.rows[0].child_identity = "changed-child" | .rows[0].adjudication_sha256 = $sha' "$manifest" >"$manifest.tmp"
  mv "$manifest.tmp" "$manifest"
  assert_reject "$proof_root" "$sid" 'changed manifest after admission'
}

test_append_only_rejects_same_phase_manifest_rewrite() {
  local proof_root="$TMP_ROOT/append" sid session_dir manifest
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  run_gate "$proof_root" final "$sid" "$TMP_ROOT/append-root.out" "$TMP_ROOT/append-root.err"
  jq -c '
    .targets += [(.targets[0] | .target_id = "target-candidate" | .target_kind = "candidate-fix")] |
    .rows += [.rows[0:4][] | .target_id = "target-candidate" | .target_kind = "candidate-fix" | .child_identity = (.child_identity + "-candidate")]
  ' "$manifest" >"$manifest.tmp"
  mv "$manifest.tmp" "$manifest"
  if run_gate "$proof_root" final "$sid" "$TMP_ROOT/append-candidate.out" "$TMP_ROOT/append-candidate.err"; then
    return 1
  fi
  grep -Eq 'changed manifest|reused|unbound or silent critic adjudication' "$TMP_ROOT/append-candidate.err"
}

test_main_commit_boundary_requires_manifest() {
  local proof_root="$TMP_ROOT/commit-gate" sid=commit-session out
  mkdir -p "$proof_root/$sid" "$TMP_ROOT/home"
  printf 'scope: commit gate\n' >"$proof_root/$sid/eci_active"
  out="$TMP_ROOT/commit-gate.out"
  jq -cn --arg cwd "$ROOT" --arg sid "$sid" \
    '{session_id:$sid,cwd:$cwd,tool_input:{command:"git commit -m checked"}}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/validate-bash.sh" >"$out"
  jq -e '
    (.hookSpecificOutput.permissionDecision == "deny") and
    ((.hookSpecificOutput.permissionDecisionReason | contains("commit boundary denied")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("marker ownership"))) and
    ((.hookSpecificOutput.permissionDecisionReason | contains("manifest")) or
     (.hookSpecificOutput.permissionDecisionReason | contains("marker")))
  ' "$out" >/dev/null
}

test_reviewed_dirty_commit_and_off_review_are_nonblocking() {
  local repo proof_root sid session_dir
  repo="$TMP_ROOT/reviewed-commit-repo"
  proof_root="$TMP_ROOT/reviewed-commit-proof"
  mkdir -p "$repo"
  git -C "$repo" init -q
  git -C "$repo" config user.email eci-test@example.invalid
  git -C "$repo" config user.name 'ECI Test'
  printf 'base\n' >"$repo/target.txt"
  git -C "$repo" add target.txt
  git -C "$repo" commit -qm initial
  printf 'reviewed dirty change\n' >>"$repo/target.txt"
  git -C "$repo" add target.txt

  sid="$(ECI_TEST_REPO="$repo" ECI_TEST_TARGET="$repo/target.txt" build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  {
    printf 'scope: reviewed commit lifecycle\n'
    printf 'cwd: %s\n' "$repo"
    printf 'session_id: %s\n' "$sid"
    printf 'created_utc: 2026-01-01T00:00:00Z\n'
  } >"$session_dir/eci_active"

  ECI_TEST_CWD="$repo" run_gate "$proof_root" commit "$sid" \
    "$TMP_ROOT/reviewed-commit-gate.out" "$TMP_ROOT/reviewed-commit-gate.err"
  grep -Fq 'phase=commit' "$TMP_ROOT/reviewed-commit-gate.out" || return 1
  grep -Fq 'fresh named least-restriction critic' "$TMP_ROOT/reviewed-commit-gate.out" || return 1
  [ ! -e "$session_dir/eci-commit-admitted" ] && [ ! -L "$session_dir/eci-commit-admitted" ] || return 1

  ECI_TEST_CWD="$repo" run_gate "$proof_root" off "$sid" \
    "$TMP_ROOT/reviewed-off-gate.out" "$TMP_ROOT/reviewed-off-gate.err"
  grep -Fq 'phase=off' "$TMP_ROOT/reviewed-off-gate.out" || return 1
  grep -Fq 'fresh named least-restriction critic' "$TMP_ROOT/reviewed-off-gate.out" || return 1
  [ -f "$session_dir/eci_active" ] && [ ! -L "$session_dir/eci_active" ] || return 1
  git -C "$repo" commit -qm reviewed

}

test_manifest_write_is_role_neutral_and_target_safe() {
  local proof_root="$TMP_ROOT/manifest-write" sid session_dir manifest out
  local temp_source configured_tmp outside_tmp outside_source symlink_source nonregular_source traversal_source control_source
  local unsafe_destination_sid unsafe_destination_dir unsafe_destination_target unsafe_node_sid unsafe_node_dir
  local canonical_cache symlink_home canonical_proof canonical_session canonical_manifest canonical_source canonical_tmp
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  temp_source="$TMP_ROOT/eci-required-critics.json.source"
  cp "$manifest" "$temp_source"
  rm -f -- "$manifest"
  ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" on 'manifest publication from coordinator temp' >/dev/null
  TMPDIR="$test_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$temp_source" >"$TMP_ROOT/manifest-write-temp.out"
  [ -f "$manifest" ] && cmp -s "$temp_source" "$manifest"

  # A configured TMPDIR is ordinary caller context; any readable regular
  # source is imported into the fixed current-session destination.
  configured_tmp="$TMP_ROOT/configured-tmp"
  mkdir -p "$configured_tmp"
  cp "$manifest" "$configured_tmp/eci-required-critics.json.source"
  rm -f -- "$manifest"
  TMPDIR="$configured_tmp" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$configured_tmp/eci-required-critics.json.source" \
    >"$TMP_ROOT/manifest-write-configured-tmp.out"
  [ -f "$manifest" ] && cmp -s "$configured_tmp/eci-required-critics.json.source" "$manifest"

  out="$TMP_ROOT/manifest-write-worker.out"
  ECI_ROLE=eci-implementer ECI_HOOK_IS_SUBAGENT=true \
    ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" TMPDIR="$test_tmp_parent" \
    "$ROOT/bin/eci-active" manifest-write "$temp_source" >"$out" 2>&1
  [ -f "$manifest" ] && cmp -s "$temp_source" "$manifest"
  outside_tmp="$TMP_ROOT/outside-tmp"
  mkdir -p "$outside_tmp"
  outside_source="$outside_tmp/ordinary-readable-manifest.note"
  cp "$manifest" "$outside_source"
  TMPDIR="$configured_tmp" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$outside_source" >"$out" 2>&1
  cmp -s "$outside_source" "$manifest"

  symlink_source="$TMP_ROOT/symlink-source/eci-required-critics.json.source"
  mkdir -p "${symlink_source%/*}"
  ln -s "$temp_source" "$symlink_source"
  TMPDIR="$test_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$symlink_source" >"$out" 2>&1
  cmp -s "$temp_source" "$symlink_source"
  cmp -s "$temp_source" "$manifest"

  nonregular_source="$TMP_ROOT/nonregular-manifest-source"
  mkdir -p "$nonregular_source"
  if TMPDIR="$test_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$nonregular_source" >"$out" 2>&1; then
    return 1
  fi
  grep -Fq 'manifest-write source or fixed destination is unsafe.' "$out"
  [ -d "$nonregular_source" ]

  traversal_source="$TMP_ROOT/traversal/../eci-required-critics.json.source"
  mkdir -p "$TMP_ROOT/traversal"
  TMPDIR="$test_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$traversal_source" >"$out" 2>&1
  cmp -s "$temp_source" "$manifest"

  control_source="$TMP_ROOT/eci_active"
  cp "$manifest" "$control_source"
  TMPDIR="$test_tmp_parent" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$control_source" >"$out" 2>&1
  cmp -s "$control_source" "$manifest"

  # Nonregular sources and fixed local destinations must remain untouched.
  unsafe_destination_sid="$(build_manifest "$proof_root" unsafe-destination)"
  unsafe_destination_dir="$proof_root/$unsafe_destination_sid"
  unsafe_destination_target="$TMP_ROOT/foreign-manifest-destination"
  ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$unsafe_destination_sid" \
    "$ROOT/bin/eci-active" on 'unsafe manifest destination fixture' >/dev/null
  printf '%s\n' 'foreign manifest destination bytes' >"$unsafe_destination_target"
  rm -f -- "$unsafe_destination_dir/eci-required-critics.json"
  ln -s -- "$unsafe_destination_target" "$unsafe_destination_dir/eci-required-critics.json"
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$unsafe_destination_sid" \
    "$ROOT/bin/eci-active" manifest-write "$temp_source" >"$out" 2>&1; then
    return 1
  fi
  [ -L "$unsafe_destination_dir/eci-required-critics.json" ]
  grep -Fqx 'foreign manifest destination bytes' "$unsafe_destination_target"

  unsafe_node_sid="$(build_manifest "$proof_root" unsafe-node)"
  unsafe_node_dir="$proof_root/$unsafe_node_sid"
  ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$unsafe_node_sid" \
    "$ROOT/bin/eci-active" on 'nonregular manifest destination fixture' >/dev/null
  rm -f -- "$unsafe_node_dir/eci-required-critics.json"
  mkdir "$unsafe_node_dir/eci-required-critics.json"
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$unsafe_node_sid" \
    "$ROOT/bin/eci-active" manifest-write "$temp_source" >"$out" 2>&1; then
    return 1
  fi
  [ -d "$unsafe_node_dir/eci-required-critics.json" ]

  # The deployed default may have a symlinked HOME/tmp parent.  The shared
  # resolver must canonicalize that ancestor so lifecycle ownership and
  # manifest publication use the same marker/session/cwd paths.
  canonical_cache="$TMP_ROOT/canonical-cache"
  symlink_home="$TMP_ROOT/symlink-home"
  canonical_proof="$canonical_cache/agent/proof"
  mkdir -p "$canonical_proof" "$symlink_home"
  ln -s "$canonical_cache" "$symlink_home/tmp"
  sid="$(build_manifest "$canonical_proof" root)"
  canonical_session="$canonical_proof/$sid"
  canonical_manifest="$canonical_session/eci-required-critics.json"
  canonical_tmp="$TMP_ROOT/canonical-tmp"
  mkdir -p "$canonical_tmp"
  canonical_source="$canonical_tmp/eci-required-critics.json.source"
  cp "$canonical_manifest" "$canonical_source"
  rm -f -- "$canonical_manifest"
  HOME="$symlink_home" TMPDIR="$canonical_tmp" ECI_PROOF_ROOT= ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" on 'canonical default-root resolution' >/dev/null
  HOME="$symlink_home" TMPDIR="$canonical_tmp" ECI_PROOF_ROOT= ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" manifest-write "$canonical_source" >"$TMP_ROOT/canonical-manifest-write.out"
  [ -f "$canonical_manifest" ] && cmp -s "$canonical_source" "$canonical_manifest"
}

test_malformed_commit_identity_does_not_skip_active_gate() {
  local proof_root="$TMP_ROOT/malformed-identity" out
  local malformed_home="$TMP_ROOT/malformed-home"
  mkdir -p "$proof_root/commit-session" "$malformed_home/.config/eci"
  chmod 700 "$malformed_home" "$malformed_home/.config" "$malformed_home/.config/eci"
  printf '%s\n' enforcing >"$malformed_home/.config/eci/command-gate-mode"
  chmod 600 "$malformed_home/.config/eci/command-gate-mode"
  {
    printf 'scope: malformed identity\n'
    printf 'cwd: %s\n' "$ROOT"
    printf 'session_id: commit-session\n'
    printf 'created_utc: 2026-01-01T00:00:00Z\n'
  } >"$proof_root/commit-session/eci_active"
  out="$TMP_ROOT/malformed-identity.out"
  jq -cn --arg cwd "$ROOT" \
    '{session_id:[],cwd:$cwd,tool_input:{command:"git commit -m checked"}}' |
    HOME="$malformed_home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/validate-bash.sh" >"$out"
  jq -e '
    (.hookSpecificOutput.permissionDecision == "deny") and
    (.hookSpecificOutput.permissionDecisionReason | contains("malformed hook identity"))
  ' "$out" >/dev/null || {
    cat "$out" >&2
    return 1
  }
}

test_commit_parser_wrappers_and_unknown_fail_closed() {
  local proof_root="$TMP_ROOT/parser-contract" command out
  local parser_home="$TMP_ROOT/parser-home" parser_config="$TMP_ROOT/parser-config"
  mkdir -p "$proof_root/commit-session" "$parser_home" "$parser_config/eci"
  chmod 700 "$parser_home" "$parser_config" "$parser_config/eci"
  printf '%s\n' enforcing >"$parser_config/eci/command-gate-mode"
  chmod 600 "$parser_config/eci/command-gate-mode"
  {
    printf 'scope: parser contract\n'
    printf 'cwd: %s\n' "$ROOT"
    printf 'session_id: commit-session\n'
    printf 'created_utc: 2026-01-01T00:00:00Z\n'
  } >"$proof_root/commit-session/eci_active"
  for command in \
    'git commit -m checked' \
    'git -C /tmp commit -m checked' \
    'git --git-dir=/tmp/.git commit -m checked' \
    'git --work-tree=/tmp commit -m checked' \
    'sudo -u builder git -C /tmp commit -m checked' \
    'doas -u builder git commit -m checked' \
    'nohup git commit -m checked' \
    'setsid git commit -m checked' \
    'timeout -k 1 5 git commit -m checked' \
    'command -p git commit -m checked' \
    'exec -a eci git commit -m checked' \
    'env -C /tmp ECI_TEST=1 git commit -m checked' \
    'systemd-run --unit eci git commit -m checked' \
    'sh -c "git commit -m checked"'; do
    out="$TMP_ROOT/parser-${#command}.out"
    jq -cn --arg command "$command" --arg cwd "$ROOT" \
      '{session_id:"commit-session",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" \
        bash "$ROOT/hooks/validate-bash.sh" >"$out"
    jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$out" >/dev/null || return 1
  done
  out="$TMP_ROOT/parser-unknown.out"
  jq -cn --arg cwd "$ROOT" \
    '{session_id:"commit-session",cwd:$cwd,tool_input:{command:"sh -c \"$ECI_DYNAMIC_COMMAND\""}}' |
    HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/validate-bash.sh" >"$out"
  jq -e '.hookSpecificOutput.permissionDecisionReason | contains("ECI_PLAN_SYNTAX_DENIED") and contains("predicate=dynamic-expansion")' "$out" >/dev/null

  for command in 'make test'; do
    out="$TMP_ROOT/parser-benign-${#command}.out"
    jq -cn --arg command "$command" --arg cwd "$ROOT" '{session_id:"commit-session",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" bash "$ROOT/hooks/validate-bash.sh" >"$out"
    [ ! -s "$out" ] || return 1
  done

  out="$TMP_ROOT/parser-leading-assignment.out"
  jq -cn --arg cwd "$ROOT" \
    '{session_id:"commit-session",cwd:$cwd,tool_input:{command:"FOO=bar make test"}}' |
    HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/validate-bash.sh" >"$out"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_PLAN_SYNTAX_DENIED") and contains("predicate=leading-assignment"))
  ' "$out" >/dev/null || return 1

  for command in 'git alias ci' 'git ci -m checked'; do
    out="$TMP_ROOT/parser-git-context-${#command}.out"
    jq -cn --arg command "$command" --arg cwd "$ROOT" '{session_id:"commit-session",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" bash "$ROOT/hooks/validate-bash.sh" >"$out"
    jq -e '.hookSpecificOutput.permissionDecisionReason | contains("ECI_GIT_EXECUTION_CONTEXT_DENIED") and contains("git-execution-context")' "$out" >/dev/null || return 1
  done

  for command in \
    'git config --local user.name checked' \
    'git tag release' \
    'python3 -c "print(1)"' \
    'bash arbitrary-script.sh' \
    "rm -f $proof_root/commit-session/eci_active"; do
    out="$TMP_ROOT/parser-unknown-${#command}.out"
    jq -cn --arg command "$command" --arg cwd "$ROOT" \
      '{session_id:"commit-session",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" \
        bash "$ROOT/hooks/validate-bash.sh" >"$out"
    case "$command" in
      rm\ *)
        expected_code='[ECI_COMMAND_NOT_ALLOWLISTED]'
        expected_wording='coordinator-cleanup-route'
        ;;
      python3\ -c\ *|python\ -c\ *|python2\ -c\ *)
        expected_code='[ECI_PLAN_DYNAMIC_LAUNCH_DENIED]'
        expected_wording='predicate=dynamic-interpreter-launch'
        ;;
      bash\ *|sh\ *|zsh\ *|dash\ *|env\ *|command\ *|builtin\ *|exec\ *|python\ *|python2\ *|python3\ *|perl\ *|ruby\ *|node\ *|deno\ *|go\ run\ *)
        expected_code='[ECI_COMMAND_WRAPPER_UNSUPPORTED]'
        expected_wording='unsupported wrapper/interpreter'
        ;;
      *)
        expected_code='[ECI_COMMAND_NOT_ALLOWLISTED]'
        expected_wording='unrecognized command form'
        ;;
    esac
    jq -e --arg expected_code "$expected_code" --arg expected_wording "$expected_wording" \
      '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains($expected_code) and contains($expected_wording))' "$out" >/dev/null
    if [ "$expected_code" = '[ECI_COMMAND_NOT_ALLOWLISTED]' ] &&
      [ "$expected_wording" = 'unrecognized command form' ]; then
      jq -e '
        .hookSpecificOutput.permissionDecisionReason |
        contains("delegate bounded implementation, exploration, debugging, or test work to an ECI worker/subagent") and
        contains("wait for and collect its result") and
        contains("do not treat this denial alone as a blocker")
      ' "$out" >/dev/null
    fi
  done

  for command in \
    'git status --short' \
    'git diff --stat' \
    'git log -1' \
    'pwd' \
    'rg --files hooks/tests' \
    'bash hooks/tests/run.sh'; do
    out="$TMP_ROOT/parser-known-${#command}.out"
    jq -cn --arg command "$command" --arg cwd "$ROOT" \
      '{session_id:"commit-session",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" \
        bash "$ROOT/hooks/validate-bash.sh" >"$out"
    [ ! -s "$out" ] || return 1
  done

  for command in 'bash -n hooks/validate-bash.sh' 'sh -n hooks/validate-bash.sh' 'dash -n hooks/validate-bash.sh' 'zsh -n hooks/validate-bash.sh'; do
    out="$TMP_ROOT/parser-read-only-${#command}.out"
    jq -cn --arg command "$command" --arg cwd "$ROOT" \
      '{session_id:"commit-session",cwd:$cwd,tool_input:{command:$command}}' |
      HOME="$parser_home" XDG_CONFIG_HOME="$parser_config" ECI_PROOF_ROOT="$proof_root" \
        bash "$ROOT/hooks/validate-bash.sh" >"$out"
    # validate-bash emits no decision for a proven read-only command; any JSON
    # output here would be a denial or malformed contract response.
    [ ! -s "$out" ] || return 1
  done
}

test_root_and_session_symlink_fail_closed() {
  local parent="$TMP_ROOT/symlink-parent" root="$TMP_ROOT/symlink-root" sid=symlink-session target="$TMP_ROOT/target"
  local cache_real="$TMP_ROOT/cache-real" cache_link="$TMP_ROOT/cache-link" linked_root linked_sid final_real final_link
  local fixture_repo="$TMP_ROOT/symlink-repo" fixture_target="$TMP_ROOT/symlink-repo/hooks/eci-review-gate.sh"
  mkdir -p "$cache_real/proof-target"
  ln -s "$cache_real" "$cache_link"
  linked_root="$cache_link/proof"
  ln -s "$cache_real/proof-target" "$cache_real/proof"
  # This branch tests proof-root path handling.  Use an isolated repository
  # with only a controlled target diff so unrelated coordinator worktree
  # changes cannot win the gate race and hide the intended diagnostic.
  mkdir -p "${fixture_target%/*}"
  cp -- "$ROOT/hooks/eci-review-gate.sh" "$fixture_target"
  git -C "$fixture_repo" init -q
  git -C "$fixture_repo" config user.name 'ECI test'
  git -C "$fixture_repo" config user.email 'eci-test@example.invalid'
  git -C "$fixture_repo" add hooks/eci-review-gate.sh
  git -C "$fixture_repo" commit -qm 'clean symlink fixture'
  printf '%s\n' '# controlled symlink-fixture change' >>"$fixture_target"
  if ! (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    linked_sid="$(build_manifest "$linked_root" root)"
    if run_gate "$linked_root" final "$linked_sid" "$TMP_ROOT/linked.out" "$TMP_ROOT/linked.err"; then
      exit 1
    fi
    grep -Fq 'unsafe proof root' "$TMP_ROOT/linked.err"
  ); then
    return 1
  fi

  mkdir -p "$parent/$sid" "$target"
  ln -s "$parent" "$root"
  if ECI_PROOF_ROOT="$root" "$ROOT/hooks/eci-review-gate.sh" final "$sid" >"$TMP_ROOT/symlink-root.out" 2>"$TMP_ROOT/symlink-root.err"; then
    return 1
  fi
  final_real="$TMP_ROOT/final-real"
  final_link="$TMP_ROOT/final-link"
  mkdir -p "$final_real/$sid"
  ln -s "$final_real" "$final_link"
  if ECI_PROOF_ROOT="$final_link/" "$ROOT/hooks/eci-review-gate.sh" final "$sid" >"$TMP_ROOT/symlink-root-trailing.out" 2>"$TMP_ROOT/symlink-root-trailing.err"; then
    return 1
  fi
  root="$TMP_ROOT/regular-root"
  mkdir -p "$root/$sid" "$target"
  rm -rf "$root/$sid"
  ln -s "$target" "$root/$sid"
  if ECI_PROOF_ROOT="$root" "$ROOT/hooks/eci-review-gate.sh" final "$sid" >"$TMP_ROOT/symlink-session.out" 2>"$TMP_ROOT/symlink-session.err"; then
    return 1
  fi
}

test_artifact_and_target_paths_are_lexically_canonical() {
  local proof_root="$TMP_ROOT/path-contract" fixture_repo="$TMP_ROOT/path-contract-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir manifest original bad_path
  local noncanonical_target target_version
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    manifest="$session_dir/eci-required-critics.json"
    original="$TMP_ROOT/path-contract-original.json"
    cp "$manifest" "$original"
    for bad_path in relative-diff.txt "$session_dir//diff.txt" "$session_dir/../$sid/diff.txt"; do
      jq -c --arg bad "$bad_path" \
        '.current_diff_artifact = $bad | .targets |= map(.diff_artifact = $bad)' \
        "$original" >"$manifest"
      assert_reject "$proof_root" "$sid" '[ECI_REVIEW_ARTIFACT_DENIED]'
      grep -Fq "reason: ECI required-critic review gate denied diff artifact for target target-root: $bad_path" "$TMP_ROOT/gate.err"
    done

    noncanonical_target="$fixture_repo/../$(basename -- "$fixture_repo")/hooks/eci-review-gate.sh"
    target_version="$(sha "$fixture_target")"
    jq -c --arg path "$noncanonical_target" --arg version "$target_version" \
      '.current_target_path = $path |
       .target_file_hashes = {($path): $version} |
       .targets |= map(.target_path = $path | .target_version = $version) |
       .rows |= map(.target_path = $path | .target_version = $version)' \
      "$original" >"$manifest"
    assert_reject "$proof_root" "$sid" '[ECI_REVIEW_TARGET_DENIED]'
    grep -Fq "reason: ECI required-critic review gate denied target outside the canonical repository: $noncanonical_target" "$TMP_ROOT/gate.err"
  )
}

test_target_must_be_in_trusted_changed_paths() {
  local proof_root="$TMP_ROOT/changed-paths" fixture_repo="$TMP_ROOT/changed-paths-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir manifest target_path target_version
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    manifest="$session_dir/eci-required-critics.json"
    target_path="$fixture_repo/.gitignore"
    target_version="$(sha "$target_path")"
    jq -c --arg path "$target_path" --arg version "$target_version" \
      '.current_target_path = $path |
       .target_file_hashes = {($path): $version} |
       .targets |= map(.target_path = $path | .target_version = $version) |
       .rows |= map(.target_path = $path | .target_version = $version)' \
      "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    assert_reject "$proof_root" "$sid" '[ECI_REVIEW_TARGET_DENIED]'
    grep -Fq "reason: ECI required-critic review gate denied target not present in the trusted changed-path set: $target_path" "$TMP_ROOT/gate.err"
  )
}

test_untracked_target_bytes_are_bound_by_admission() {
  local proof_root="$TMP_ROOT/untracked-target" sid session_dir manifest target_path target_version out err
  target_path="$ROOT/hooks/.eci-review-gate-untracked-$BASHPID"
  untracked_target="$target_path"
  printf '%s\n' 'untracked target initial bytes' >"$target_path"
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  target_version="$(sha "$target_path")"
  jq -c --arg path "$target_path" --arg version "$target_version" \
    '.current_target_path = $path |
     .target_file_hashes = {($path): $version} |
     .targets |= map(.target_path = $path | .target_version = $version) |
     .rows |= map(.target_path = $path | .target_version = $version)' \
    "$manifest" >"$manifest.tmp"
  mv "$manifest.tmp" "$manifest"
  out="$TMP_ROOT/untracked-target.out"
  err="$TMP_ROOT/untracked-target.err"
  if run_gate "$proof_root" final "$sid" "$out" "$err"; then
    return 1
  fi
  grep -Fq 'untracked-only governed target' "$err"
  printf '%s\n' 'untracked target changed bytes' >"$target_path"
  if run_gate "$proof_root" final "$sid" "$out" "$err"; then
    return 1
  fi
  grep -Fq 'untracked-only governed target' "$err"
  rm -f -- "$target_path"
  untracked_target=""
}

test_deletion_target_uses_explicit_tombstone_route() {
  local proof_root="$TMP_ROOT/deletion-target" sid session_dir manifest deleted_path out err
  sid="$(build_manifest "$proof_root" root)"
  session_dir="$proof_root/$sid"
  manifest="$session_dir/eci-required-critics.json"
  deleted_path="$ROOT/hooks/.eci-review-gate-deleted-$BASHPID"
  [ ! -e "$deleted_path" ] || return 1
  jq -c --arg path "$deleted_path" \
    '.current_target_path = $path |
     .target_file_hashes = {($path): ("0000000000000000000000000000000000000000000000000000000000000000")} |
     .targets |= map(.target_path = $path | .target_version = ("0000000000000000000000000000000000000000000000000000000000000000")) |
     .rows |= map(.target_path = $path | .target_version = ("0000000000000000000000000000000000000000000000000000000000000000"))' \
    "$manifest" >"$manifest.tmp"
  mv "$manifest.tmp" "$manifest"
  out="$TMP_ROOT/deletion-target.out"
  err="$TMP_ROOT/deletion-target.err"
  if run_gate "$proof_root" final "$sid" "$out" "$err"; then
    return 1
  fi
  grep -Fq 'deletion-only governed targets' "$err"
}

test_prewrite_requires_exact_c_admission() {
  local proof_root="$TMP_ROOT/prewrite" fixture_repo="$TMP_ROOT/prewrite-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir out err manifest original ledger
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    printf '%s\n' 'scope: active prewrite' >"$session_dir/eci_active"
    manifest="$session_dir/eci-required-critics.json"
    original="$TMP_ROOT/prewrite-original.json"
    cp "$manifest" "$original"
    jq -c '.rows |= map(select(.critic_role == "C" and .gate_phase == "prewrite"))' "$original" >"$manifest"
    ledger="$session_dir/eci-required-critics.prewrite.1.ledger"
    if ECI_PREWRITE_TARGET="$fixture_target" \
      ECI_PREWRITE_WRITER_SESSION='wrong-writer' \
      run_gate "$proof_root" prewrite "$sid" "$TMP_ROOT/prewrite-invalid.out" "$TMP_ROOT/prewrite-invalid.err"; then
      return 1
    fi
    [ ! -e "$ledger" ] || [ ! -s "$ledger" ] || return 1
    out="$TMP_ROOT/prewrite.out"; err="$TMP_ROOT/prewrite.err"
    ECI_PREWRITE_TARGET="$fixture_target" \
      ECI_PREWRITE_WRITER_SESSION='session-root' \
      run_gate "$proof_root" prewrite "$sid" "$out" "$err"
    grep -Fq 'phase=prewrite' "$out"
    grep -Fq 'target_path: ' "$session_dir/eci-prewrite-admitted.1"
  )
}

test_acceptance_version_and_prewrite_lifecycle() {
  local proof_root sid manifest original fixture_repo="$TMP_ROOT/acceptance-version-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh"

  proof_root="$TMP_ROOT/version-header"
  sid="$(build_manifest "$proof_root" root)"
  manifest="$proof_root/$sid/eci-required-critics.json"
  original="$TMP_ROOT/version-header.json"
  cp "$manifest" "$original"
  jq -c '.acceptance_version = "0"' "$original" >"$manifest"
  assert_reject "$proof_root" "$sid" 'manifest v2 schema'

  prepare_clean_review_gate_fixture "$fixture_repo"
  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"

    proof_root="$TMP_ROOT/version-row"
    sid="$(build_manifest "$proof_root" root)"
    manifest="$proof_root/$sid/eci-required-critics.json"
    jq -c '.rows[0].acceptance_version = "2"' "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    assert_reject "$proof_root" "$sid" 'changed acceptance_version'

    proof_root="$TMP_ROOT/prewrite-late"
    sid="$(build_manifest "$proof_root" root)"
    manifest="$proof_root/$sid/eci-required-critics.json"
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/prewrite-late-final.out" "$TMP_ROOT/prewrite-late-final.err"
    jq -c '.rows |= map(select(.critic_role == "C" and .gate_phase == "prewrite"))' "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    if ECI_PREWRITE_TARGET="$fixture_target" \
      ECI_PREWRITE_WRITER_SESSION='session-root' \
      run_gate "$proof_root" prewrite "$sid" "$TMP_ROOT/prewrite-late.out" "$TMP_ROOT/prewrite-late.err"; then
      return 1
    fi
    [ -s "$TMP_ROOT/prewrite-late.err" ]

    proof_root="$TMP_ROOT/version-snapshot"
    sid="$(build_manifest "$proof_root" root)"
    manifest="$proof_root/$sid/eci-required-critics.json"
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/version-snapshot-v1.out" "$TMP_ROOT/version-snapshot-v1.err"
    jq -c '.acceptance_version = "2" | .rows |= map(.acceptance_version = "2")' "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/version-snapshot-v2.out" "$TMP_ROOT/version-snapshot-v2.err"; then
      return 1
    fi
    grep -Fq 'unchanged snapshot' "$TMP_ROOT/version-snapshot-v2.err"
    [ -f "$proof_root/$sid/eci-required-critics.final.1.ledger" ]
    [ "$(wc -l <"$proof_root/$sid/eci-required-critics.final.1.ledger")" -eq 4 ]
  )
}

test_commit_to_changed_snapshot_off_transition() {
  local first_root="$TMP_ROOT/transition-first" second_root="$TMP_ROOT/transition-second"
  local fixture_repo="$TMP_ROOT/transition-repo" fixture_target="$TMP_ROOT/transition-repo/hooks/eci-review-gate.sh"
  local sid session_dir second_session_dir transition manifest out err
  prepare_clean_review_gate_fixture "$fixture_repo"
  root_fixture="$fixture_repo/hooks/.eci-review-gate-transition-$BASHPID"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$first_root" root)"
    run_gate "$first_root" commit "$sid" "$TMP_ROOT/transition-commit.out" "$TMP_ROOT/transition-commit.err"
    session_dir="$first_root/$sid"
    transition="$root_fixture"
    # Keep this fixture until the off snapshot consumes it.  The top-level EXIT
    # cleanup is the failure fallback; a process-wide RETURN trap here can be
    # inherited by callers running bash with functrace and disturb diagnostics.
    printf '%s\n' transition >"$transition"

    build_manifest "$second_root" root >/dev/null
    second_session_dir="$second_root/$sid"
    cp "$session_dir/eci-acceptance-anchor" "$second_session_dir/eci-acceptance-anchor"
    cp "$session_dir/eci-required-critics.commit.1.ledger" "$second_session_dir/eci-required-critics.commit.1.ledger"
    cp "$session_dir/eci-critic-identities.ledger" "$second_session_dir/eci-critic-identities.ledger"
    manifest="$second_session_dir/eci-required-critics.json"
    jq -c '.acceptance_version = "2" | .rows |= map(.acceptance_version = "2" | .child_identity += "-v2")' \
      "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    # The v2 transition intentionally uses fresh child identities.  Rebind each
    # copied adjudication artifact to that identity before testing the off
    # boundary; otherwise the closed adjudication contract rejects the fixture
    # before the transition lineage is exercised.
    for index in $(jq -r 'range(.rows | length)' "$manifest"); do
      child="$(jq -r ".rows[$index].child_identity" "$manifest")"
      rebind_manifest_row_identity "$manifest" "$index" "$child"
    done
    {
      printf 'scope: transition\n'
      printf 'cwd: %s\n' "$fixture_repo"
      printf 'session_id: %s\n' "$sid"
      printf 'created_utc: 2026-01-01T00:00:00Z\n'
    } >"$second_session_dir/eci_active"
    out="$TMP_ROOT/transition-off.out"
    err="$TMP_ROOT/transition-off.err"
    if ! run_gate "$second_root" off "$sid" "$out" "$err"; then
      cat "$err" >&2
      return 1
    fi
    grep -Fq 'phase=off' "$out"
    rm -f -- "$transition"
  )
  root_fixture=""
}

test_final_to_off_consumes_admitted_identity() {
  local proof_root="$TMP_ROOT/final-off" fixture_repo="$TMP_ROOT/final-off-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir out err
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/final-off-final.out" "$TMP_ROOT/final-off-final.err"
    {
      printf 'scope: final-off\n'
      printf 'cwd: %s\n' "$fixture_repo"
      printf 'session_id: %s\n' "$sid"
      printf 'created_utc: 2026-01-01T00:00:00Z\n'
    } >"$session_dir/eci_active"
    out="$TMP_ROOT/final-off-off.out"
    err="$TMP_ROOT/final-off-off.err"
    run_gate "$proof_root" off "$sid" "$out" "$err"
    grep -Fq 'phase=off' "$out"
  )
}

test_identity_reuse_across_target_is_rejected() {
  local proof_root="$TMP_ROOT/identity-reuse" fixture_repo="$TMP_ROOT/identity-reuse-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir manifest
  local index child
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    manifest="$session_dir/eci-required-critics.json"
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/identity-reuse-root.out" "$TMP_ROOT/identity-reuse-root.err"
    jq -c '
      .targets += [(.targets[0] | .target_id = "target-candidate" | .target_kind = "candidate-fix")] |
      .rows += [.rows[0:4][] | .target_id = "target-candidate" | .target_kind = "candidate-fix" | .child_identity = (.child_identity + "-candidate")] |
      .rows[5].child_identity = .rows[1].child_identity
    ' "$manifest" >"$manifest.tmp"
    mv "$manifest.tmp" "$manifest"
    # Rebind the synthetic candidate rows so the closed adjudication record is
    # valid and the assertion reaches the intended duplicate-identity check.
    for index in 4 5 6 7; do
      child="$(jq -r ".rows[$index].child_identity" "$manifest")"
      rebind_manifest_row_identity "$manifest" "$index" "$child"
    done
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/identity-reuse.out" "$TMP_ROOT/identity-reuse.err"; then
      return 1
    fi
    grep -Fq 'reused critic identity' "$TMP_ROOT/identity-reuse.err" ||
      grep -Fq 'reused child identity' "$TMP_ROOT/identity-reuse.err"
  )
}

test_historical_admission_state_cannot_be_recreated() {
  local proof_root="$TMP_ROOT/historical-state" fixture_repo="$TMP_ROOT/historical-state-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir ledger anchor
  local original_ledger first_char replacement first_line second_line
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    ledger="$session_dir/eci-required-critics.final.1.ledger"
    anchor="$session_dir/eci-acceptance-anchor"
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/historical-first.out" "$TMP_ROOT/historical-first.err"
    original_ledger="$TMP_ROOT/historical-state-original.ledger"
    cp -- "$ledger" "$original_ledger"
    first_char="$(head -c 1 -- "$ledger")"
    case "$first_char" in
      0) replacement=1 ;;
      *) replacement=0 ;;
    esac
    { printf '%s' "$replacement"; tail -c +2 -- "$ledger"; } >"$ledger.mutated"
    mv -- "$ledger.mutated" "$ledger"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/historical-mutated.out" "$TMP_ROOT/historical-mutated.err"; then
      return 1
    fi
    grep -Fq 'mutated historical critic ledger' "$TMP_ROOT/historical-mutated.err"
    cp -- "$original_ledger" "$ledger"
    first_line="$(sed -n '1p' "$ledger")"
    second_line="$(sed -n '2p' "$ledger")"
    {
      printf '%s\n' "$second_line"
      printf '%s\n' "$first_line"
      tail -n +3 -- "$ledger"
    } >"$ledger.reordered"
    mv -- "$ledger.reordered" "$ledger"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/historical-reordered.out" "$TMP_ROOT/historical-reordered.err"; then
      return 1
    fi
    grep -Fq 'mutated historical critic ledger' "$TMP_ROOT/historical-reordered.err"
    cp -- "$original_ledger" "$ledger"
    rm -f -- "$ledger"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/historical-missing-ledger.out" "$TMP_ROOT/historical-missing-ledger.err"; then
      return 1
    fi
    grep -Fq 'shortened or deleted ledger' "$TMP_ROOT/historical-missing-ledger.err"

    proof_root="$TMP_ROOT/historical-anchor"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    anchor="$session_dir/eci-acceptance-anchor"
    run_gate "$proof_root" final "$sid" "$TMP_ROOT/historical-anchor-first.out" "$TMP_ROOT/historical-anchor-first.err"
    rm -f -- "$anchor"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/historical-missing-anchor.out" "$TMP_ROOT/historical-missing-anchor.err"; then
      return 1
    fi
    grep -Fq 'historical critic evidence without its acceptance anchor' "$TMP_ROOT/historical-missing-anchor.err"
  )
}

test_acceptance_transaction_recovers_each_publication_boundary() {
  local boundary proof_root sid session_dir transaction anchor ledger
  local fixture_repo="$TMP_ROOT/recovery-publication-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh"
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    for boundary in ledger identity anchor; do
      proof_root="$TMP_ROOT/recovery-$boundary"
      sid="$(build_manifest "$proof_root" root)"
      session_dir="$proof_root/$sid"
      transaction="$session_dir/eci-acceptance-transaction"
      anchor="$session_dir/eci-acceptance-anchor"
      ledger="$session_dir/eci-required-critics.final.1.ledger"
      if ECI_REVIEW_GATE_TEST_FAIL_AFTER="$boundary" run_gate "$proof_root" final "$sid" \
        "$TMP_ROOT/recovery-$boundary-fail.out" "$TMP_ROOT/recovery-$boundary-fail.err"; then
        return 1
      fi
      [ -f "$transaction" ] && [ ! -L "$transaction" ] || return 1
      case "$boundary" in
        ledger) [ -f "$ledger" ] && [ ! -e "$anchor" ] || return 1 ;;
        identity) [ -f "$ledger" ] && [ -f "$session_dir/eci-critic-identities.ledger" ] && [ ! -e "$anchor" ] || return 1 ;;
        anchor) [ -f "$anchor" ] || return 1 ;;
      esac
      run_gate "$proof_root" final "$sid" \
        "$TMP_ROOT/recovery-$boundary-replay.out" "$TMP_ROOT/recovery-$boundary-replay.err"
      [ -f "$anchor" ] && [ ! -e "$transaction" ] || return 1
    done
  )
}

test_acceptance_transaction_binds_published_prefixes() {
  local proof_root="$TMP_ROOT/recovery-prefix" fixture_repo="$TMP_ROOT/recovery-prefix-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir transaction out err
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    transaction="$session_dir/eci-acceptance-transaction"
    out="$TMP_ROOT/recovery-prefix-fail.out"
    err="$TMP_ROOT/recovery-prefix-fail.err"
    if ECI_REVIEW_GATE_TEST_FAIL_AFTER=ledger run_gate "$proof_root" final "$sid" "$out" "$err"; then
      return 1
    fi
    [ -f "$transaction" ] && [ ! -L "$transaction" ] || return 1
    grep -Fxq 'schema: eci-acceptance-transaction/v2' "$transaction" || return 1
    grep -Fxq 'state: ledger-published' "$transaction" || return 1
    grep -Fq 'ledger_sha256: ' "$transaction" || return 1
    grep -Fq 'identity_sha256: ' "$transaction" || return 1

    # A recovery record whose published prefix no longer matches must not be
    # silently repaired as if the ledger/identity publication never happened.
    eci_sed_inplace 's/^ledger_sha256: .*/ledger_sha256: 0000000000000000000000000000000000000000000000000000000000000000/' "$transaction"
    if run_gate "$proof_root" final "$sid" "$TMP_ROOT/recovery-prefix-tampered.out" "$TMP_ROOT/recovery-prefix-tampered.err"; then
      return 1
    fi
    grep -Fq 'transaction ledger prefix' "$TMP_ROOT/recovery-prefix-tampered.err"

    proof_root="$TMP_ROOT/recovery-identity-prefix"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    transaction="$session_dir/eci-acceptance-transaction"
    if ECI_REVIEW_GATE_TEST_FAIL_AFTER=identity run_gate "$proof_root" final "$sid" \
      "$TMP_ROOT/recovery-identity-prefix-fail.out" "$TMP_ROOT/recovery-identity-prefix-fail.err"; then
      return 1
    fi
    [ -f "$transaction" ] && [ ! -L "$transaction" ] || return 1
    grep -Fxq 'schema: eci-acceptance-transaction/v2' "$transaction" || return 1
    grep -Fxq 'state: identity-published' "$transaction" || return 1
    grep -Fq 'identity_sha256: ' "$transaction" || return 1
    eci_sed_inplace 's/^identity_sha256: .*/identity_sha256: 0000000000000000000000000000000000000000000000000000000000000000/' "$transaction"
    if run_gate "$proof_root" final "$sid" \
      "$TMP_ROOT/recovery-identity-prefix-tampered.out" "$TMP_ROOT/recovery-identity-prefix-tampered.err"; then
      return 1
    fi
    grep -Fq 'transaction identity prefix' "$TMP_ROOT/recovery-identity-prefix-tampered.err"
  )
}

test_unanchored_identity_row_is_rejected() {
  local proof_root="$TMP_ROOT/unanchored-identity" fixture_repo="$TMP_ROOT/unanchored-identity-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh" sid session_dir identity
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    identity="$session_dir/eci-critic-identities.ledger"
    run_gate "$proof_root" final "$sid" \
      "$TMP_ROOT/unanchored-identity-first.out" "$TMP_ROOT/unanchored-identity-first.err"
    printf 'commit:1:%s:%s:%s:%s:%s\n' \
      "$(printf '%064d' 0)" "$(printf '%064d' 0)" "$(printf '%064d' 0)" \
      "$(printf '%064d' 0)" "$(printf '%064d' 0)" >>"$identity"
    if run_gate "$proof_root" final "$sid" \
      "$TMP_ROOT/unanchored-identity-replay.out" "$TMP_ROOT/unanchored-identity-replay.err"; then
      return 1
    fi
    grep -Fq 'unanchored critic identity row' "$TMP_ROOT/unanchored-identity-replay.err"
  )
}

test_spawn_and_report_identity_bindings_are_required() {
  local proof_root fixture_repo="$TMP_ROOT/spawn-report-binding-repo"
  local fixture_target="$fixture_repo/hooks/eci-review-gate.sh"
  local sid session_dir manifest spawn report adjudication tmp new_sha report_sha adjudication_sha
  prepare_clean_review_gate_fixture "$fixture_repo"

  (
    export ECI_TEST_REPO="$fixture_repo"
    export ECI_TEST_TARGET="$fixture_target"
    export ECI_TEST_CWD="$fixture_repo"
    proof_root="$TMP_ROOT/spawn-binding"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    manifest="$session_dir/eci-required-critics.json"
    spawn="$(jq -r '.rows[1].spawn_request_artifact' "$manifest")"
    tmp="$spawn.tmp"
    sed 's/^authority: non-authoritative$/authority: authoritative/' "$spawn" >"$tmp"
    mv "$tmp" "$spawn"
    new_sha="$(sha "$spawn")"
    jq -c --arg sha "$new_sha" '.rows[1].spawn_request_sha256 = $sha' "$manifest" >"$tmp"
    mv "$tmp" "$manifest"
    assert_reject "$proof_root" "$sid" 'spawn authority binding'

    proof_root="$TMP_ROOT/report-binding"
    sid="$(build_manifest "$proof_root" root)"
    session_dir="$proof_root/$sid"
    manifest="$session_dir/eci-required-critics.json"
    report="$(jq -r '.rows[0].report_artifact' "$manifest")"
    adjudication="$(jq -r '.rows[0].adjudication_artifact' "$manifest")"
    tmp="$report.tmp"
    sed 's/^eci_critic_identity: .*$/eci_critic_identity: session_id=wrong-session;target_id=target-root;critic_role=C;gate_phase=prewrite;child_identity=child-root-C-prewrite;critic_provider=unavailable_by_schema;critic_semantic_role=ECI Critic C;critic_provenance=requested-special/' "$report" >"$tmp"
    mv "$tmp" "$report"
    report_sha="$(sha "$report")"
    jq -c --arg sha "$report_sha" '.report_sha256 = $sha' "$adjudication" >"$tmp"
    mv "$tmp" "$adjudication"
    adjudication_sha="$(sha "$adjudication")"
    jq -c --arg report_sha "$report_sha" --arg adjudication_sha "$adjudication_sha" \
      '.rows[0].report_sha256 = $report_sha | .rows[0].adjudication_sha256 = $adjudication_sha' \
      "$manifest" >"$tmp"
    mv "$tmp" "$manifest"
    assert_reject "$proof_root" "$sid" 'report identity binding'
  )
}

test_nested_marker_lifecycle_is_role_neutral_and_target_safe() {
  local proof_root="$TMP_ROOT/nested" sid=session-nested session_dir
  session_dir="$proof_root/$sid"
  mkdir -p "$session_dir"
  printf '%s\n' 'scope: nested lifecycle' >"$session_dir/eci_active"
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" nested-enter 1 1 "$sid" >/dev/null 2>&1; then
    return 1
  fi
  [ ! -e "$session_dir/ate_nested_eci_active" ] || return 1
  {
    printf 'scope: nested lifecycle\n'
    printf 'cwd: %s\n' "$ROOT"
    printf 'session_id: %s\n' "$sid"
    printf 'created_utc: 2026-01-01T00:00:00Z\n'
  } >"$session_dir/eci_active"
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" ECI_ACCEPTANCE_VERSION=0 \
    "$ROOT/bin/eci-active" nested-enter 1 1 "$sid" >/dev/null 2>&1; then
    return 1
  fi
  [ ! -e "$session_dir/ate_nested_eci_active" ] || return 1
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" ECI_ACCEPTANCE_VERSION=1000000000 \
    "$ROOT/bin/eci-active" nested-enter 1 1 "$sid" >/dev/null 2>&1; then
    return 1
  fi
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" nested-enter 1000000000 1 "$sid" >/dev/null 2>&1; then
    return 1
  fi
  [ ! -e "$session_dir/ate_nested_eci_active" ] || return 1
  ECI_ROLE=eci-implementer ECI_HOOK_IS_SUBAGENT=true \
    ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" nested-enter 1 1 "$sid" >/dev/null
  [ -f "$session_dir/ate_nested_eci_active" ] || return 1
  ECI_ROLE=eci-implementer ECI_HOOK_IS_SUBAGENT=true \
    ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" nested-accept >/dev/null
  [ -f "$session_dir/ate_nested_eci_completion" ] || return 1
  if ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" nested-enter 1 2 "$sid" >/dev/null 2>&1; then
    return 1
  fi
  [ -f "$session_dir/ate_nested_eci_completion" ] || return 1
  if ECI_PROOF_ROOT="$proof_root" ECI_REVIEW_CWD="$ROOT" \
    "$ROOT/hooks/eci-review-gate.sh" final "$sid" >"$TMP_ROOT/nested-gate.out" 2>"$TMP_ROOT/nested-gate.err"; then
    return 1
  fi
  grep -Fq 'nested ECI target is active' "$TMP_ROOT/nested-gate.err"
  ECI_ROLE=eci-implementer ECI_HOOK_IS_SUBAGENT=true \
    ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
    "$ROOT/bin/eci-active" nested-exit >/dev/null
  [ ! -e "$session_dir/ate_nested_eci_active" ]
  set +x
}

test_lock_receipt_spoof_is_ignored() {
  local proof_root="$TMP_ROOT/spoof" sid
  sid="$(build_manifest "$proof_root" root)"
  if ECI_REVIEW_GATE_LOCK_HELD=true run_gate "$proof_root" final "$sid" "$TMP_ROOT/spoof.out" "$TMP_ROOT/spoof.err"; then
    return 1
  fi
  grep -Fq 'lock' "$TMP_ROOT/spoof.err"
}

test_clean_teardown_history_is_nonblocking() {
  local source="$ROOT/bin/eci-active"
  grep -Fq 'reconcile_teardown_history' "$source"
  grep -Fq 'validate_disengage_report "$report_path"' "$source"
  ! grep -Fq 'provider-native critic/receipt evidence is missing or invalid' "$source"
  ! grep -Fq 'write_teardown_receipt' "$source"
  bash "$ROOT/hooks/tests/test-eci-off-history-reconciliation.sh"
}

# Coordinator-only helper for producing a current reviewed admission fixture.
# It is explicitly opt-in and exits before the synthetic contract suite; normal
# test runs remain unchanged.  The generated reports contain the focused
# evidence listed below and the caller may export a bounded source copy for
# manifest-write publication.
if [ "${ECI_EMIT_CURRENT_MANIFEST:-0}" = 1 ]; then
  emit_proof_root="${1:-${ECI_EMIT_PROOF_ROOT:?proof root required via argument or ECI_EMIT_PROOF_ROOT}}"
  emit_kind="${2:-${ECI_EMIT_KIND:-current}}"
  build_manifest "$emit_proof_root" "$emit_kind" "${ECI_EMIT_E2E_REQUIRED:-false}"
  if [ -n "${ECI_EMIT_SOURCE_PATH:-}" ]; then
    emit_sid="${ECI_EMIT_SESSION_ID:-session-$emit_kind}"
    cp -- "$emit_proof_root/$emit_sid/eci-required-critics.json" "$ECI_EMIT_SOURCE_PATH"
  fi
  exit 0
fi

test_reviewed_dirty_commit_and_off_review_are_nonblocking
test_manifest_write_is_role_neutral_and_target_safe
test_malformed_commit_identity_does_not_skip_active_gate
test_commit_parser_wrappers_and_unknown_fail_closed
test_root_and_session_symlink_fail_closed
test_e2e_artifact_10mib_boundary_accepts_exact_and_rejects_next_byte
test_artifact_and_target_paths_are_lexically_canonical
test_target_must_be_in_trusted_changed_paths
test_untracked_target_bytes_are_bound_by_admission
test_deletion_target_uses_explicit_tombstone_route
test_prewrite_requires_exact_c_admission
test_acceptance_version_and_prewrite_lifecycle
test_commit_to_changed_snapshot_off_transition
test_final_to_off_consumes_admitted_identity
test_identity_reuse_across_target_is_rejected
test_historical_admission_state_cannot_be_recreated
test_acceptance_transaction_recovers_each_publication_boundary
test_acceptance_transaction_binds_published_prefixes
test_unanchored_identity_row_is_rejected
test_spawn_and_report_identity_bindings_are_required
test_nested_marker_lifecycle_is_role_neutral_and_target_safe
test_lock_receipt_spoof_is_ignored
test_clean_teardown_history_is_nonblocking
printf '%s\n' 'ECI required-critic review gate tests: PASS'

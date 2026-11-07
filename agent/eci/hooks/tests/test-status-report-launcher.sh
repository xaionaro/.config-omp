#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

cat >"$test_dir/report file.yaml" <<'YAML'
timestamp: "2026-11-02T15:24:00Z"
state: Working
progress: []
decisions: []
blockers_risks: []
verification: []
next_focus: Finish
scope_complete: false
tasks:
  - id: '1'
    title: Complete the package
    priority: high
    impact: Report works
    effort: {min_minutes: 20, max_minutes: 30, kind: elapsed, basis: educated guess}
    status: CLOSED
    evidence: acceptance verified
top_priorities: ['1']
priority_estimates:
  - {priority: high, min_minutes: 20, max_minutes: 30, as_of: '2026-11-02T15:24:00Z', basis: educated guess, assumptions: review available, dependencies: none, critical_path: latest parallel endpoint}
completion_estimate: {min_minutes: 20, max_minutes: 30, as_of: '2026-11-02T15:24:00Z', basis: educated guess, assumptions: review available, dependencies: none, critical_path: latest parallel endpoint}
timeline:
  - job: '1'
    parent_chain: '1'
    start: '2026-11-02T14:24:00Z'
    end: '2026-11-02T14:54:00Z'
    fast: 'checkpoint'
    normal: 'unknown'
YAML

cd "$test_dir"
if "$repo_root/bin/status-report" validate 'report file.yaml' >validation.txt; then
	printf 'missing session unexpectedly validated\n' >&2
	exit 1
fi
grep -q 'INCOMPLETE' validation.txt
"$repo_root/bin/status-report" render <'report file.yaml' >output.txt
grep -q 'INCOMPLETE' output.txt
grep -q 'Full task tree' output.txt
grep -q '100%' output.txt
grep -Fq '| 2026-11-02T14:24:00Z – 2026-11-02T14:54:00Z | checkpoint | unknown |' output.txt
if "$repo_root/bin/status-report" validate missing.yaml >/dev/null 2>&1; then
	printf 'missing input unexpectedly succeeded\n' >&2
	exit 1
fi
cat >project-understanding.yaml <<'YAML'
schema: project-understanding/v1
sections: {}
records: {}
YAML
"$repo_root/bin/status-report" render --session-dir . 'report file.yaml' >relative-output.txt
if grep -Eq 'session context unavailable|ledger unavailable' relative-output.txt; then
	printf 'relative session directory was not resolved from the caller directory\n' >&2
	exit 1
fi
grep -q 'Full task tree' relative-output.txt
"$repo_root/bin/status-report" render --session-dir . - <'report file.yaml' >relative-stdin.txt
"$repo_root/bin/status-report" render "$test_dir/report file.yaml" --session-dir . >relative-dir.txt
for result in relative-stdin.txt relative-dir.txt; do
	if grep -Eq 'session context unavailable|ledger unavailable' "$result"; then
		printf 'relative session directory was not used with %s\n' "$result" >&2
		exit 1
	fi
	grep -q 'Full task tree' "$result"
done
mkdir 'session dir'
cp 'report file.yaml' 'session dir/latest-status-report.yaml'
cp project-understanding.yaml 'session dir/project-understanding.yaml'
"$repo_root/bin/status-report" refresh 'session dir' requirement_change
grep -q 'scope_complete: false' 'session dir/latest-status-report.yaml'
grep -Fq 'Requirement change requires scope review and status report refresh.' 'session dir/latest-status-report.yaml'
cp 'session dir/latest-status-report.yaml' before-repeat.yaml
"$repo_root/bin/status-report" refresh "$test_dir/session dir" requirement_change
cmp before-repeat.yaml 'session dir/latest-status-report.yaml'
for invalid in empty extra; do
	if [[ $invalid == empty ]]; then
		if "$repo_root/bin/status-report" refresh '' decision >invalid-output.txt 2>&1; then
			printf 'empty refresh directory unexpectedly succeeded\n' >&2
			exit 1
		fi
	else
		if "$repo_root/bin/status-report" refresh 'session dir' decision extra >invalid-output.txt 2>&1; then
			printf 'extra refresh argument unexpectedly succeeded\n' >&2
			exit 1
		fi
	fi
	grep -q 'usage: status-report refresh' invalid-output.txt
done
cmp before-repeat.yaml 'session dir/latest-status-report.yaml'

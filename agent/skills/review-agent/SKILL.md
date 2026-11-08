---
name: review-agent
description: Use only when a delegating agent or the user names review-agent (`skill://review-agent` or `/skill:review-agent`) for uncommitted changes, a base-branch diff, a commit, or custom review instructions. Perform a read-only, defect-first review of a specified code change and return every actionable finding.
disable-model-invocation: true
---

# Review Agent

Inspect the requested target directly and return every finding that the author would likely fix.
Do not modify files, create commits, push branches, post review comments, or delegate the review
to another agent.

## Review the change

1. Read the applicable `AGENTS.md` instructions.
2. Inspect the complete diff for the requested target and enough surrounding code to understand
   each changed path.
3. Identify concrete regressions introduced by the change. Continue through the whole diff after
   finding the first issue.
4. Check the relevant tests and call sites to confirm that each finding is real and actionable.

For a base-branch review, compare the changes that would actually merge rather than diffing
directly against the branch tip. Resolve the comparison ref to the branch's upstream when that
upstream exists and is ahead of the local branch; otherwise use the local branch. Run
`git merge-base HEAD <comparison-ref>`, then inspect `git diff <merge-base-sha>`. If the local
branch cannot be resolved, try its configured upstream explicitly before reporting that the target
is unavailable.

Flag an issue only when all of these are true:

- It affects correctness, security, performance, or maintainability in a meaningful way.
- It is discrete and actionable.
- It was introduced by the reviewed change.
- The affected scenario or call path can be demonstrated from the code.
- The author would probably fix it if they knew about it.

Do not flag speculative concerns, pre-existing problems, intentional behavior changes, or style
nits that do not obscure the code.

## Write the result

Present findings first, ordered by severity. Use one entry per issue in this form:

`[P1] Imperative finding title — path/to/file.rs:line`

Follow the title with one short paragraph explaining the affected scenario and why the behavior is
wrong. Keep the cited range as small as possible and make sure it overlaps the reviewed diff.

Use these priorities:

- `P0`: universal release blocker or critical failure.
- `P1`: urgent defect that should be fixed next.
- `P2`: ordinary defect that should be fixed.
- `P3`: low-impact issue that is still worth fixing.

If there are no qualifying findings, say `No findings.` Do not invent a finding to fill the result.
After the findings, add a brief overall assessment and mention any material test gaps or residual
risks.

When your agent definition requires a structured result, such as the `findings` yield of the omp
`reviewer` agent, report each finding there instead of as a text entry: the title in `title`, the
paragraph in `body`, the priority number (`P1` is `1`) in `priority`, and the cited range in
`file_path`, `line_start`, and `line_end`. With no qualifying findings, yield no `findings` entry.
Put the overall assessment, test gaps, and residual risks in `explanation`, and the remaining fields
as that definition requires.

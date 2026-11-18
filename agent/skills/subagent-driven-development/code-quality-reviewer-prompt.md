# Code Quality Reviewer Prompt Template

Use this template when dispatching a code quality reviewer subagent.

**Purpose:** Verify implementation is well-built (clean, covered by tests, maintainable)

**Only dispatch after spec compliance review passes.**

Dispatch an explorer with:

`agent_type` maps to `agent` (`scout`, the read-only omp agent); `reasoning_effort` has no `task` equivalent and is dropped; `message` maps to `task`.

```
task(
  agent="scout",
  task="""
Role label: code-quality-reviewer

<stop-hook-boundary>
Follow [Ref 8].
</stop-hook-boundary>

<additional-checks>
Also check each item in [Ref 9].
</additional-checks>

[Filled requesting-code-review/code-reviewer.md prompt, with entries 8 and 9 below appended to its References list before your own entries]

8. ~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "Every subagent prompt must include: Follow any Stop-hook prompt in that session, including required proof/checklist files. Fix blockers within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval."
9. ~/.omp/agent/skills/subagent-driven-development/code-quality-reviewer-prompt.md, entry 9 (`<additional-checks>`): "- Does each file have one clear responsibility with a well-defined interface?" / "- Are units decomposed so they can be understood and tested independently?" / "- Is the implementation following the file structure from the plan?" / "- Did this implementation create new files that are already large, or significantly grow existing files? (Don't flag pre-existing file sizes — focus on what this change contributed.)"
"""
)
```

Fill the prompt with:
- `WHAT_WAS_IMPLEMENTED`: [from implementer's report]
- `PLAN_OR_REQUIREMENTS`: Task N from [plan-file]
- `BASE_SHA`: [commit before task]
- `HEAD_SHA`: [current commit]
- `DESCRIPTION`: [task summary]
- `REQUEST_SOURCE`: `~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "Execute plan by dispatching fresh subagent per task, with two-stage review after each: spec compliance review first, then code quality review."`; number your own entries from 10

After spawn, print the roster entry: `code-quality-reviewer: <runtime name> [explorer]`.

**Code reviewer returns:** Strengths, Issues (P0–P3), Assessment

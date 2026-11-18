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
Follow [Ref N+1].
</stop-hook-boundary>

<additional-checks>
Also check each item in [Ref N+2].
</additional-checks>

[Filled requesting-code-review/code-reviewer.md prompt. N is the number of its last References entry. Append entries N+1 and N+2 below to its References list, then your own entries. Write N+1 and N+2 as numbers in those entries and in the two markers above]

N+1. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "[its Stop-hook handling item, quoted when you fill this]"
N+2. ~/.omp/agent/skills/subagent-driven-development/code-quality-reviewer-prompt.md, the `<additional-checks>` entry: "- Does each file have one clear responsibility with a well-defined interface?" / "- Are units decomposed so they can be understood and tested independently?" / "- Is the implementation following the file structure from the plan?" / "- Did this implementation create new files that are already large, or significantly grow existing files? (Don't flag pre-existing file sizes — focus on what this change contributed.)"
"""
)
```

Fill the prompt with:
- `WHAT_WAS_IMPLEMENTED`: [from implementer's report]
- `PLAN_OR_REQUIREMENTS`: Task N from [plan-file]
- `BASE_SHA`: [commit before task]
- `HEAD_SHA`: [current commit]
- `DESCRIPTION`: [task summary]
- `REQUEST_SOURCE`: `~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "[its 'Execute plan by dispatching' sentence, quoted when you fill this]"`
- References entries ending `quoted when you fill this`, in the filled prompt and in entry N+1: the named text, quoted verbatim from its current source

After spawn, print the roster entry: `code-quality-reviewer: <runtime name> [explorer]`.

**Code reviewer returns:** Strengths, Issues (P0–P3), Assessment

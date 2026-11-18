# Plan Document Reviewer Prompt Template

Use this template when dispatching a plan document reviewer subagent.

**Purpose:** Verify the plan is complete, matches the spec, and has proper task decomposition.

**Dispatch after:** The complete plan is written.

`agent_type` maps to `agent` (`scout`, the read-only omp agent); `reasoning_effort` has no `task` equivalent and is dropped; `message` maps to `task`.

```
task(
  agent="scout",
  task="""
Role label: plan-document-reviewer

<task>
Review [PLAN_FILE_PATH] against [SPEC_FILE_PATH] [Ref 1].
</task>

<stop-hook-boundary>
Follow [Ref 2].
</stop-hook-boundary>

<check>
Check each row in [Ref 3].
</check>

<calibration>
Follow [Ref 4].
</calibration>

<report>
Report in the form [Ref 5] gives.
</report>

References
1. ~/.omp/agent/skills/writing-plans/plan-document-reviewer-prompt.md: "**Purpose:** Verify the plan is complete, matches the spec, and has proper task decomposition."
2. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "[its Stop-hook handling item, quoted when you fill this]"
3. ~/.omp/agent/skills/writing-plans/plan-document-reviewer-prompt.md References, entry 3 (`<check>`): "| Category | Look For |" / "| Completeness | TODOs, placeholders, incomplete tasks, missing steps |" / "| Spec alignment | Plan covers spec requirements, no major scope creep |" / "| Task decomposition | Clear boundaries, actionable steps |" / "| Buildability | An engineer can follow the plan without getting stuck |"
4. ~/.omp/agent/skills/writing-plans/plan-document-reviewer-prompt.md References, entry 4 (`<calibration>`): "Only flag issues that would break implementation." / "Approve unless requirements are missing, steps contradict, placeholders remain, or tasks are too vague to act on." / "Treat wording, style, and nice-to-have suggestions as advisory."
5. ~/.omp/agent/skills/writing-plans/plan-document-reviewer-prompt.md References, entry 5 (`<report>`): "## Plan Review" / "**Status:** Approved | Issues Found" / "**Issues (if any):**" / "- [Task X, Step Y]: [specific issue] - [why it matters for implementation]" / "**Recommendations (advisory; approval never awaits them):**" / "- [suggestions for improvement]"
[Your entries, numbered from 6]
"""
)
```

After spawn, print the roster entry: `plan-document-reviewer: <runtime name> [explorer]`.

**Reviewer returns:** Status, Issues (if any), Recommendations

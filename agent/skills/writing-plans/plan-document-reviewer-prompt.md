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
Review [PLAN_FILE_PATH] against [SPEC_FILE_PATH].
</task>

<stop-hook-boundary>
Follow any Stop-hook prompt in this session, including required proof/checklist files.
Fix blockers within your assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval.
</stop-hook-boundary>

<check>
| Category | Look For |
|----------|----------|
| Completeness | TODOs, placeholders, incomplete tasks, missing steps |
| Spec alignment | Plan covers spec requirements, no major scope creep |
| Task decomposition | Clear boundaries, actionable steps |
| Buildability | An engineer can follow the plan without getting stuck |
</check>

<calibration>
Only flag issues that would break implementation.
Approve unless requirements are missing, steps contradict, placeholders remain, or tasks are too vague to act on.
Treat wording, style, and nice-to-have suggestions as advisory.
</calibration>

<report>
## Plan Review

**Status:** Approved | Issues Found

**Issues (if any):**
- [Task X, Step Y]: [specific issue] - [why it matters for implementation]

**Recommendations (advisory, do not block approval):**
- [suggestions for improvement]
</report>
"""
)
```

After spawn, print the roster entry: `plan-document-reviewer: <runtime name> [explorer]`.

**Reviewer returns:** Status, Issues (if any), Recommendations

# Spec Compliance Reviewer Prompt Template

Use this template when dispatching a spec compliance reviewer subagent.

**Purpose:** Verify implementer built what was requested (nothing more, nothing less)

`agent_type` maps to `agent` (`scout`, the read-only omp agent); `reasoning_effort` has no `task` equivalent and is dropped; `message` maps to `task`.

```
task(
  agent="scout",
  task="""
Role label: spec-compliance-reviewer

<task>
Verify the implementation matches the task requirements [Ref 2].
</task>

<requested>
[FULL TEXT of task requirements, its [Ref N] markers renumbered from 6]
</requested>

<implementer-report>
[From implementer's report]
</implementer-report>

<stop-hook-boundary>
Follow [Ref 1].
</stop-hook-boundary>

<verification-rule>
Follow [Ref 3].
</verification-rule>

<check>
Check each item in [Ref 4].
</check>

<report>
Report as [Ref 5] sets.
</report>

References
1. ~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "Every subagent prompt must include: Follow any Stop-hook prompt in that session, including required proof/checklist files. Fix blockers within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval."
2. ~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "Execute plan by dispatching fresh subagent per task, with two-stage review after each: spec compliance review first, then code quality review."
3. ~/.omp/agent/skills/subagent-driven-development/spec-reviewer-prompt.md References, entry 3 (`<verification-rule>`): "Do not trust the implementer report." / "Read the changed code and compare it to requirements line by line."
4. ~/.omp/agent/skills/subagent-driven-development/spec-reviewer-prompt.md References, entry 4 (`<check>`): "- Missing requirements" / "- Claims not backed by code" / "- Extra or unrequested work" / "- Over-engineering" / "- Misunderstood requirements" / "- Wrong problem or wrong implementation approach"
5. ~/.omp/agent/skills/subagent-driven-development/spec-reviewer-prompt.md References, entry 5 (`<report>`): "- Spec compliant, if everything matches after code inspection" / "- Issues found, with exact missing or extra behavior and file:line references"
[The plan's entries for the requirements' markers, renumbered from 6, then your own entries]
"""
)
```

After spawn, print the roster entry: `spec-compliance-reviewer: <runtime name> [explorer]`.

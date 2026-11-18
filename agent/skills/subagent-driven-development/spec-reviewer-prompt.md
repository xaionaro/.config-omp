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
1. ~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "[its 'Every subagent prompt must include' line, quoted when you fill this]"
2. ~/.omp/agent/skills/subagent-driven-development/SKILL.md ## Authorization: "[its two-stage review sentence, quoted when you fill this]"
3. ~/.omp/agent/skills/subagent-driven-development/spec-reviewer-prompt.md References, entry 3 (`<verification-rule>`): "Do not trust the implementer report." / "Read the changed code and compare it to requirements line by line."
4. ~/.omp/agent/skills/subagent-driven-development/spec-reviewer-prompt.md References, entry 4 (`<check>`): "- Missing requirements" / "- Claims not backed by code" / "- Extra or unrequested work" / "- Over-engineering" / "- Misunderstood requirements" / "- Wrong problem or wrong implementation approach"
5. ~/.omp/agent/skills/subagent-driven-development/spec-reviewer-prompt.md References, entry 5 (`<report>`): "- Spec compliant, if everything matches after code inspection" / "- Issues found, with exact missing or extra behavior and file:line references"
[The plan's entries for the requirements' markers, renumbered from 6, then your own entries]
"""
)
```

After spawn, print the roster entry: `spec-compliance-reviewer: <runtime name> [explorer]`.

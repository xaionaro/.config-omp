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
Verify the implementation matches the task requirements.
</task>

<requested>
[FULL TEXT of task requirements]
</requested>

<implementer-report>
[From implementer's report]
</implementer-report>

<stop-hook-boundary>
Follow any Stop-hook prompt in this session, including required proof/checklist files.
Fix blockers within your assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval.
</stop-hook-boundary>

<verification-rule>
Do not trust the implementer report.
Read the changed code and compare it to requirements line by line.
</verification-rule>

<check>
- Missing requirements
- Claims not backed by code
- Extra or unrequested work
- Over-engineering
- Misunderstood requirements
- Wrong problem or wrong implementation approach
</check>

<report>
- Spec compliant, if everything matches after code inspection
- Issues found, with exact missing or extra behavior and file:line references
</report>
"""
)
```

After spawn, print the roster entry: `spec-compliance-reviewer: <runtime name> [explorer]`.
